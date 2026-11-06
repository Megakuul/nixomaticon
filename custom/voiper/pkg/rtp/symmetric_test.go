package rtp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pion/rtcp"
	pion "github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

func TestAuthenticatedSymmetricRTP(t *testing.T) {
	for _, mux := range []bool{false, true} {
		t.Run(map[bool]string{false: "separate RTCP", true: "RTCP mux"}[mux], func(t *testing.T) {
			s, err := New(context.Background(), "127.0.0.1:0", 8000)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			old := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
			if err = s.SetRemote(old, old, 8000); err != nil {
				t.Fatal(err)
			}
			s.SetRTCPMux(mux)
			s.SetSymmetricRTP(true)
			peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			moved := peer.LocalAddr().(*net.UDPAddr)
			foreign := &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: moved.Port}
			keyA, keyB := make([]byte, 30), make([]byte, 30)
			keyA[0], keyB[0] = 1, 2
			cipher, err := srtp.CreateContext(keyB[:16], keyB[16:], srtp.ProtectionProfileAes128CmHmacSha1_80)
			if err != nil {
				t.Fatal(err)
			}
			packet := func(seq uint16) ([]byte, []byte) {
				t.Helper()
				plain, err := (&pion.Packet{Header: pion.Header{Version: 2, PayloadType: 8, SSRC: 42, SequenceNumber: seq}, Payload: []byte{1}}).Marshal()
				if err != nil {
					t.Fatal(err)
				}
				secure, err := cipher.EncryptRTP(nil, plain, nil)
				if err != nil {
					t.Fatal(err)
				}
				return plain, secure
			}
			plain, secure := packet(1)
			s.handleRTP(plain, moved, false)
			if s.Stats().RemoteAddress != old.String() {
				t.Fatal("plain RTP learned a peer")
			}
			if err = s.ConfigureSRTP(keyA, keyB); err != nil {
				t.Fatal(err)
			}
			tampered := append([]byte(nil), secure...)
			tampered[len(tampered)-1] ^= 1
			s.handleRTP(plain, moved, false)
			s.handleRTP(tampered, moved, false)
			s.handleRTP(secure, foreign, false)
			if s.Stats().RemoteAddress != old.String() {
				t.Fatal("untrusted packet redirected SRTP")
			}
			s.SetSymmetricRTP(false)
			s.handleRTP(secure, moved, false)
			if s.Stats().RemoteAddress != old.String() {
				t.Fatal("disabled rebinding redirected SRTP")
			}
			s.SetSymmetricRTP(true)
			s.handleRTP(secure, moved, false)
			if s.Stats().RemoteAddress != moved.String() {
				t.Fatal("authenticated port change rejected")
			}
			s.handleRTP(secure, old, false)
			if s.Stats().RemoteAddress != moved.String() {
				t.Fatal("replay redirected SRTP")
			}
			_, delayed := packet(2)
			_, newest := packet(3)
			s.handleRTP(newest, moved, false)
			s.handleRTP(delayed, old, false)
			if s.Stats().RemoteAddress != moved.String() {
				t.Fatal("delayed packet restored the old port")
			}
			if err = s.Send(8, []byte{9}, 160, false); err != nil {
				t.Fatal(err)
			}
			if err = peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, 1500)
			if _, _, err = peer.ReadFromUDP(data); err != nil {
				t.Fatalf("return media did not use learned port: %v", err)
			}
			control := &net.UDPAddr{IP: old.IP, Port: moved.Port + 1}
			report := func() []byte {
				t.Helper()
				plain, err := (&rtcp.ReceiverReport{SSRC: 42}).Marshal()
				if err != nil {
					t.Fatal(err)
				}
				data, err := cipher.EncryptRTCP(nil, plain, nil)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			valid := report()
			bad := append([]byte(nil), valid...)
			bad[len(bad)-1] ^= 1
			s.handleControl(bad, control, false)
			s.handleControl(valid, foreign, false)
			s.mu.Lock()
			before := s.remoteControl.String()
			s.mu.Unlock()
			if before == control.String() {
				t.Fatal("untrusted SRTCP redirected control")
			}
			s.handleControl(valid, control, false)
			s.handleControl(valid, old, false)
			delayedControl, newestControl := report(), report()
			s.handleControl(newestControl, control, false)
			s.handleControl(delayedControl, old, false)
			s.mu.Lock()
			after := s.remoteControl.String()
			s.mu.Unlock()
			if after != control.String() {
				t.Fatal("authenticated SRTCP rebinding or replay filtering failed")
			}
			if mux && s.Stats().RemoteAddress != control.String() {
				t.Fatal("multiplexed RTP/RTCP paths diverged")
			}

			// A signaled source change is first validated on the selected path;
			// its RTCP source can then learn a later authenticated port mapping.
			s.mu.Lock()
			selected := s.remote
			s.mu.Unlock()
			if err = s.SetRemote(selected, control, 8000); err != nil {
				t.Fatal(err)
			}
			newSource, err := (&pion.Packet{Header: pion.Header{Version: 2, PayloadType: 8, SSRC: 99, SequenceNumber: 1}, Payload: []byte{1}}).Marshal()
			if err != nil {
				t.Fatal(err)
			}
			newSource, err = cipher.EncryptRTP(nil, newSource, nil)
			if err != nil {
				t.Fatal(err)
			}
			s.handleRTP(newSource, selected, false)
			newReport := func() []byte {
				t.Helper()
				plain, err := (&rtcp.ReceiverReport{SSRC: 99}).Marshal()
				if err != nil {
					t.Fatal(err)
				}
				data, err := cipher.EncryptRTCP(nil, plain, nil)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			s.handleControl(newReport(), control, false)
			nextControl := &net.UDPAddr{IP: control.IP, Port: control.Port + 1}
			s.handleControl(newReport(), nextControl, false)
			s.handleControl(report(), control, false)
			s.mu.Lock()
			after = s.remoteControl.String()
			s.mu.Unlock()
			if after != nextControl.String() {
				t.Fatal("new RTCP source could not rebind or old source stole its path")
			}
		})
	}
}

func TestSymmetricRTPDoesNotOverrideICEOrDTLS(t *testing.T) {
	from := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2000}
	remote := &net.UDPAddr{IP: from.IP, Port: 1000}
	s := &Session{symmetric: true, incoming: &srtp.Context{}, protectionProfile: srtp.ProtectionProfileAes128CmHmacSha1_80}
	if !s.canRebind(from, remote) {
		t.Fatal("valid SDES candidate rejected")
	}
	s.ice = &iceTransport{}
	if s.canRebind(from, remote) {
		t.Fatal("rebinding overrides ICE")
	}
	s.ice = nil
	s.dtls = &dtlsTransport{}
	if s.canRebind(from, remote) {
		t.Fatal("rebinding overrides DTLS")
	}
}
