package sip

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
)

func TestTLSRegistrationTrust(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"registrar.test"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		if req.Contact() == nil || req.Contact().Address.Port == 0 {
			respond(req, tx, 400, "Missing Contact Port")
			return
		}
		response := wire.NewResponseFromRequest(req, 200, "OK", nil)
		response.AppendHeader(wire.NewHeader("Expires", "60"))
		tx.Respond(response)
	})
	acknowledgments := make(chan *wire.Request, 2)
	server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
		response := wire.NewResponseFromRequest(req, 200, "OK", []byte(offerSDP))
		response.AppendHeader(&wire.ContactHeader{Address: wire.Uri{Scheme: "sips", User: "bob", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port}})
		response.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
		_ = tx.Respond(response)
		<-tx.Done()
	})
	server.OnAck(func(req *wire.Request, _ wire.ServerTransaction) { acknowledgments <- req.Clone() })
	server.OnBye(func(req *wire.Request, tx wire.ServerTransaction) { respond(req, tx, 200, "OK") })
	go server.ServeTLS(listener)
	for _, scenario := range []struct{ trusted, discover bool }{{}, {trusted: true}, {trusted: true, discover: true}} {
		trusted := scenario.trusted
		name := "untrusted"
		if trusted {
			name = "trusted"
		}
		if scenario.discover {
			name = "trusted SRV failover"
		}
		t.Run(name, func(t *testing.T) {
			config := Config{Server: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "alice", Transport: "tls", LocalAddress: "127.0.0.1:0"}
			if scenario.discover {
				unused, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := unused.Addr().(*net.TCPAddr).Port
				unused.Close()
				resolver, _ := failoverDNS(t, []*net.SRV{{Target: "target.test.", Port: uint16(port)}, {Target: "target.test.", Port: uint16(config.Port), Priority: 1}}, map[string][][4]byte{"target.test.": {{127, 0, 0, 1}}})
				config.Server, config.Port, config.Resolver = "registrar.test", 0, resolver
			}
			if trusted {
				config.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
			}
			events := make(chan Event, 16)
			client, err := NewClient(config, func(event Event) { events <- event })
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = client.Register(ctx)
			if trusted && err != nil {
				t.Fatalf("trusted TLS rejected: %v", err)
			}
			if !trusted && err == nil {
				t.Fatal("untrusted TLS accepted")
			}
			if trusted {
				id, err := client.Dial(ctx, "bob", []byte(offerSDP))
				if err != nil {
					t.Fatal(err)
				}
				nextEvent(t, events, "connected")
				select {
				case ack := <-acknowledgments:
					if ack.Transport() != "TLS" || string(*ack.CallID()) != id {
						t.Fatal("TLS ACK changed call identity or transport")
					}
				case <-ctx.Done():
					t.Fatal("trusted TLS call was not acknowledged")
				}
				if err := client.Hangup(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
