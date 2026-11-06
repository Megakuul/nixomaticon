package rtp

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	pion "github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

type peerDTLSSocket struct {
	net.PacketConn
	media chan []byte
}

func (p *peerDTLSSocket) ReadFrom(data []byte) (int, net.Addr, error) {
	for {
		n, address, err := p.PacketConn.ReadFrom(data)
		if err != nil {
			return n, address, err
		}
		if n > 0 && data[0] >= 128 && data[0] <= 191 {
			select {
			case p.media <- append([]byte(nil), data[:n]...):
			default:
			}
			continue
		}
		return n, address, nil
	}
}

func TestDTLSSRTPWithIndependentPionPeer(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, profile := range []dtls.SRTPProtectionProfile{dtls.SRTP_AEAD_AES_128_GCM, dtls.SRTP_AES128_CM_HMAC_SHA1_80} {
			name := "server"
			if active {
				name = "client"
			}
			t.Run(fmt.Sprintf("%s/%04x", name, profile), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				session, err := New(ctx, "127.0.0.1:0", 8000)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				localFingerprint, err := session.PrepareDTLS()
				if err != nil {
					t.Fatal(err)
				}
				socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				peerSocket := &peerDTLSSocket{PacketConn: socket, media: make(chan []byte, 8)}
				defer peerSocket.Close()
				peerAddress := socket.LocalAddr().(*net.UDPAddr)
				session.SetRemote(peerAddress, peerAddress, 8000)
				session.SetRTCPMux(true)
				certificate, err := selfsign.GenerateSelfSigned()
				if err != nil {
					t.Fatal(err)
				}
				options := []dtls.Option{dtls.WithCertificates(certificate), dtls.WithSRTPProtectionProfiles(profile), dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret), dtls.WithInsecureSkipVerify(true), dtls.WithVerifyPeerCertificate(func(raw [][]byte, _ [][]*x509.Certificate) error {
					if len(raw) == 0 || certificateFingerprint(raw[0]) != localFingerprint {
						return errors.New("unexpected caller certificate")
					}
					return nil
				})}
				var peer *dtls.Conn
				if active {
					serverOptions := []dtls.ServerOption{dtls.WithClientAuth(dtls.RequireAnyClientCert)}
					for _, option := range options {
						serverOptions = append(serverOptions, option)
					}
					peer, err = dtls.ServerWithOptions(peerSocket, session.LocalAddr(), serverOptions...)
				} else {
					clientOptions := make([]dtls.ClientOption, len(options))
					for i, option := range options {
						clientOptions[i] = option
					}
					peer, err = dtls.ClientWithOptions(peerSocket, session.LocalAddr(), clientOptions...)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				done := make(chan error, 1)
				go func() { done <- peer.HandshakeContext(ctx) }()
				if err = session.ConnectDTLS(ctx, certificateFingerprint(certificate.Certificate[0]), active); err != nil {
					t.Fatal(err)
				}
				if err = <-done; err != nil {
					t.Fatal(err)
				}
				state, ok := peer.ConnectionState()
				if !ok {
					t.Fatal("peer has no DTLS state")
				}
				protection := srtp.ProtectionProfile(profile)
				saltSize, _ := protection.SaltLen()
				material, err := state.ExportKeyingMaterial("EXTRACTOR-dtls_srtp", nil, 32+2*saltSize)
				if err != nil {
					t.Fatal(err)
				}
				clientKey, serverKey := material[:16], material[16:32]
				clientSalt, serverSalt := material[32:32+saltSize], material[32+saltSize:]
				localKey, localSalt, remoteKey, remoteSalt := clientKey, clientSalt, serverKey, serverSalt
				if active {
					localKey, localSalt, remoteKey, remoteSalt = serverKey, serverSalt, clientKey, clientSalt
				}
				outgoing, err := srtp.CreateContext(localKey, localSalt, protection)
				if err != nil {
					t.Fatal(err)
				}
				incoming, err := srtp.CreateContext(remoteKey, remoteSalt, protection)
				if err != nil {
					t.Fatal(err)
				}
				if err = session.Send(8, []byte{1, 2, 3}, 160, false); err != nil {
					t.Fatal(err)
				}
				select {
				case packet := <-peerSocket.media:
					raw, err := incoming.DecryptRTP(nil, packet, nil)
					if err != nil {
						t.Fatal(err)
					}
					var parsed pion.Packet
					if err = parsed.Unmarshal(raw); err != nil || string(parsed.Payload) != string([]byte{1, 2, 3}) {
						t.Fatalf("peer decoded incorrect audio: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("independent peer received no SRTP")
				}
				plain, err := (&pion.Packet{Header: pion.Header{Version: 2, PayloadType: 8, SSRC: 1234, SequenceNumber: 1}, Payload: []byte{4, 5, 6}}).Marshal()
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := outgoing.EncryptRTP(nil, plain, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = socket.WriteTo(encrypted, session.LocalAddr()); err != nil {
					t.Fatal(err)
				}
				select {
				case packet := <-session.Packets():
					if string(packet.Payload) != string([]byte{4, 5, 6}) {
						t.Fatal("local SRTP payload corrupted")
					}
				case <-ctx.Done():
					t.Fatal("local endpoint received no SRTP")
				}
				if !session.Stats().Encrypted {
					t.Fatal("completed DTLS handshake not reported encrypted")
				}
				peer.Close()
				deadline := time.After(time.Second)
				for session.Stats().DTLSError == "" {
					select {
					case <-deadline:
						t.Fatal("peer DTLS closure was not reported")
					case <-time.After(5 * time.Millisecond):
					}
				}
				if session.Stats().Encrypted {
					t.Fatal("closed DTLS association still reported encrypted")
				}
				if err := session.Send(8, []byte{9}, 160, false); err == nil {
					t.Fatal("sent media after peer DTLS closure")
				}
			})
		}
	}
}

func TestDTLSFingerprintValidation(t *testing.T) {
	valid := "sha-256 " + strings.Repeat("AB:", 31) + "AB"
	for _, value := range []string{"", strings.Replace(valid, "sha-256", "sha-1", 1), valid + ":00", strings.Replace(valid, "AB", "XX", 1), "sha-256 0000", valid + " extra"} {
		if _, err := NormalizeDTLSFingerprint(value); err == nil {
			t.Fatalf("accepted malformed fingerprint %q", value)
		}
	}
	if normalized, err := NormalizeDTLSFingerprint(strings.ToLower(valid)); err != nil || normalized != valid {
		t.Fatal("valid lowercase fingerprint rejected")
	}
}

func TestDTLSCancellationAndPlaintextBlocked(t *testing.T) {
	session, err := New(context.Background(), "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.PrepareDTLS(); err != nil {
		t.Fatal(err)
	}
	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	address := peer.LocalAddr().(*net.UDPAddr)
	session.SetRemote(address, address, 8000)
	session.SetRTCPMux(true)
	plain, _ := (&pion.Packet{Header: pion.Header{Version: 2, PayloadType: 8, SSRC: 1234, SequenceNumber: 1}, Payload: []byte{7}}).Marshal()
	peer.WriteTo(plain, session.LocalAddr())
	if err = session.Send(8, []byte{1}, 160, false); err == nil {
		t.Fatal("sent plaintext before DTLS completed")
	}
	select {
	case <-session.Packets():
		t.Fatal("accepted plaintext before DTLS completed")
	case <-time.After(30 * time.Millisecond):
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err = session.ConnectDTLS(ctx, "sha-256 "+strings.Repeat("00:", 31)+"00", true); err == nil {
		t.Fatal("silent peer completed handshake")
	}
	if session.Stats().Encrypted {
		t.Fatal("failed DTLS handshake reported encrypted")
	}
	done := make(chan struct{})
	go func() { session.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DTLS close blocked")
	}
}

func TestDTLSRejectsMismatchedCertificate(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("active=%t", active), func(t *testing.T) {

			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			a, err := New(ctx, "127.0.0.1:0", 8000)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := New(ctx, "127.0.0.1:0", 8000)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			fingerprint, err := a.PrepareDTLS()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = b.PrepareDTLS(); err != nil {
				t.Fatal(err)
			}
			a.SetRemote(b.LocalAddr(), b.LocalAddr(), 8000)
			b.SetRemote(a.LocalAddr(), a.LocalAddr(), 8000)
			a.SetRTCPMux(true)
			b.SetRTCPMux(true)
			done := make(chan error, 1)
			go func() { done <- b.ConnectDTLS(ctx, fingerprint, !active) }()
			wrong := "sha-256 " + strings.Repeat("00:", 31) + "00"
			if err = a.ConnectDTLS(ctx, wrong, active); err == nil || !strings.Contains(err.Error(), "fingerprint") {
				t.Fatalf("certificate mismatch not rejected: %v", err)
			}
			<-done
			if a.Stats().Encrypted || b.Stats().Encrypted {
				t.Fatal("certificate mismatch enabled secure media")
			}
		})
	}
}

func TestCloseInterruptsDTLSHandshake(t *testing.T) {
	session, err := New(context.Background(), "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	session.PrepareDTLS()
	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	remote := peer.LocalAddr().(*net.UDPAddr)
	session.SetRemote(remote, remote, 8000)
	session.SetRTCPMux(true)
	done := make(chan error, 1)
	go func() {
		done <- session.ConnectDTLS(context.Background(), "sha-256 "+strings.Repeat("AB:", 31)+"AB", true)
	}()
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = peer.ReadFrom(make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { session.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for the DTLS handshake timeout")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed DTLS handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("DTLS handshake survived Close")
	}
}
