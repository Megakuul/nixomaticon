package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

// This peer speaks LDAP over a real TLS socket, including bind and search. It
// deliberately omits pagination controls to exercise non-paged servers too.
func TestDirectoryLDAPWithPrivateCA(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "directory fixture"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		for {
			request, err := ber.ReadPacket(conn)
			if err != nil {
				finished <- err
				return
			}
			id := request.Children[0].Value
			operation := request.Children[1]
			response := func(body *ber.Packet) error {
				packet := ber.NewSequence("")
				packet.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, ""))
				packet.AppendChild(body)
				_, err := conn.Write(packet.Bytes())
				return err
			}
			result := func(tag ber.Tag, code int64) error {
				body := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "")
				body.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, ""))
				body.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", ""))
				body.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", ""))
				return response(body)
			}
			switch operation.Tag {
			case ldap.ApplicationBindRequest:
				if operation.Children[1].Value != "uid=reader,dc=example" || operation.Children[2].Data.String() != "fixture-password" {
					finished <- result(ldap.ApplicationBindResponse, ldap.LDAPResultInvalidCredentials)
					return
				}
				if err = result(ldap.ApplicationBindResponse, 0); err != nil {
					finished <- err
					return
				}
			case ldap.ApplicationSearchRequest:
				body := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultEntry, nil, "")
				body.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "uid=alice,dc=example", ""))
				attrs := ber.NewSequence("")
				for _, attribute := range [][2]string{{"entryUUID", "7a2a-fixture"}, {"displayName", "Alice"}, {"telephoneNumber", "100"}, {"mobile", "+491234"}} {
					field := ber.NewSequence("")
					field.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, attribute[0], ""))
					values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "")
					values.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, attribute[1], ""))
					field.AppendChild(values)
					attrs.AppendChild(field)
				}
				body.AppendChild(attrs)
				if err = response(body); err != nil {
					finished <- err
					return
				}
				finished <- result(ldap.ApplicationSearchResultDone, 0)
				return
			default:
				finished <- net.ErrClosed
				return
			}
		}
	}()
	cfg := DirectoryConfig{URL: "ldaps://" + listener.Addr().String(), BaseDN: "dc=example", BindDN: "uid=reader,dc=example", Password: "fixture-password", CAFile: caFile, Limit: 100}
	result, err := LookupDirectory(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if result.Truncated || result.Skipped != 0 || len(result.Contacts) != 1 {
		t.Fatalf("lookup: %+v", result)
	}
	contact := result.Contacts[0]
	if contact.DirectoryID != "uuid:7a2a-fixture" || contact.Name != "Alice" || contact.Address != "100" || len(contact.Numbers) != 1 || contact.Numbers[0].Address != "+491234" {
		t.Fatalf("contact: %+v", contact)
	}
}

func TestDirectoryCancellationInterruptsTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = LookupDirectory(ctx, DirectoryConfig{URL: "ldaps://" + listener.Addr().String(), BaseDN: "dc=example"})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("TLS cancellation took %v: %v", time.Since(started), err)
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("connection was not accepted")
	}
}
