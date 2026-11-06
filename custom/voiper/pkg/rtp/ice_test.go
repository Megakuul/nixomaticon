package rtp

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pion/turn/v5"
)

func TestICEHostAndTURNRelay(t *testing.T) {
	for _, relay := range []bool{false, true} {
		name := "host"
		if relay {
			name = "relay"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var servers []ICEServer
			if relay {
				listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				server, err := turn.NewServer(turn.ServerConfig{Realm: "voiper-test", AuthHandler: func(request *turn.RequestAttributes) (string, []byte, bool) {
					return request.Username, turn.GenerateAuthKey(request.Username, request.Realm, "password"), request.Username == "voiper"
				}, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: listener, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}}}})
				if err != nil {
					listener.Close()
					t.Fatal(err)
				}
				defer server.Close()
				servers = []ICEServer{{URLs: []string{"turn:" + listener.LocalAddr().String() + "?transport=udp"}, Username: "voiper", Credential: "password"}}
			}
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
			offer, err := a.gatherICE(ctx, servers, relay)
			if err != nil {
				t.Fatal(err)
			}
			answer, err := b.gatherICE(ctx, servers, relay)
			if err != nil {
				t.Fatal(err)
			}
			if relay {
				for _, description := range []*ICEDescription{&offer, &answer} {
					candidates := []string{}
					for _, candidate := range description.Candidates {
						if strings.Contains(candidate, " typ relay ") {
							candidates = append(candidates, candidate)
						}
					}
					if len(candidates) == 0 {
						t.Fatal("TURN server did not produce relay candidate")
					}
					description.Candidates = candidates
				}
			}
			a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000)
			b.SetRemote(a.LocalAddr(), a.ControlAddr(), 8000)
			connected := make(chan error, 1)
			go func() { connected <- a.ConnectICE(ctx, answer, true) }()
			if err = b.ConnectICE(ctx, offer, false); err != nil {
				t.Fatal(err)
			}
			if err = <-connected; err != nil {
				t.Fatal(err)
			}
			if err = a.Send(8, []byte{1, 2, 3}, 160, false); err != nil {
				t.Fatal(err)
			}
			select {
			case packet := <-b.Packets():
				if string(packet.Payload) != string([]byte{1, 2, 3}) {
					t.Fatal("corrupted ICE payload")
				}
			case <-ctx.Done():
				t.Fatal("ICE RTP did not arrive")
			}
			if !a.Stats().RTCPMux || a.Stats().ICEState != "connected" {
				t.Fatalf("ICE state %+v", a.Stats())
			}
			if relay && a.Stats().ICECandidateType != "relay" && b.Stats().ICECandidateType != "relay" {
				t.Fatal("test did not use a TURN relay")
			}
		})
	}
}

func TestICEGatherFallbackPreservesRTPPort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
	before := a.LocalAddr().String()
	if _, err = a.GatherICE(ctx, nil); err != nil {
		t.Fatal(err)
	}
	a.DeclineICE()
	if a.LocalAddr().String() != before {
		t.Fatal("fallback changed advertised port")
	}
	a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000)
	b.SetRemote(a.LocalAddr(), a.ControlAddr(), 8000)
	if err = b.Send(8, []byte{42}, 160, false); err != nil {
		t.Fatal(err)
	}
	select {
	case packet := <-a.Packets():
		if packet.Payload[0] != 42 {
			t.Fatal("wrong fallback payload")
		}
	case <-ctx.Done():
		t.Fatal("fallback RTP lost")
	}
}

func TestICEConnectCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	local, err := session.GatherICE(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- session.ConnectICE(ctx, local, true) }()
	cancel()
	select {
	case err = <-finished:
		if err == nil {
			t.Fatal("cancelled ICE connection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("ICE cancellation blocked")
	}
	session.Close()
}

func TestICEGatherCancellation(t *testing.T) {
	blocker, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	session, err := New(context.Background(), "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = session.GatherICE(ctx, []ICEServer{{URLs: []string{"stun:" + blocker.LocalAddr().String()}}})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("gather cancellation: %v after %s", err, time.Since(start))
	}
}

func TestICEOversizedDatagramsDoNotStopMedia(t *testing.T) {
	for _, source := range []string{"unknown-socket", "selected-peer"} {
		t.Run(source, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
			offer, err := a.GatherICE(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			answer, err := b.GatherICE(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.SetRemote(b.LocalAddr(), b.LocalAddr(), 8000)
			b.SetRemote(a.LocalAddr(), a.LocalAddr(), 8000)
			connected := make(chan error, 1)
			go func() { connected <- a.ConnectICE(ctx, answer, true) }()
			if err = b.ConnectICE(ctx, offer, false); err != nil {
				t.Fatal(err)
			}
			if err = <-connected; err != nil {
				t.Fatal(err)
			}
			if source == "unknown-socket" {
				sender, err := net.DialUDP("udp", nil, b.LocalAddr())
				if err != nil {
					t.Fatal(err)
				}
				_, err = sender.Write(make([]byte, 9000))
				sender.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = a.iceConn.Write(make([]byte, 5000)); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.Send(8, []byte{42}, 160, false); err != nil {
				t.Fatal(err)
			}
			select {
			case packet := <-b.Packets():
				if packet == nil || len(packet.Payload) != 1 || packet.Payload[0] != 42 {
					t.Fatal("wrong media after oversized datagram")
				}
			case <-ctx.Done():
				t.Fatal("oversized datagram stopped media reception")
			}
		})
	}
}
