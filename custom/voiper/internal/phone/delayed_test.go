package phone

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/pkg/media"
)

func TestOutgoingDelayedICEAnswerLifecycle(t *testing.T) {
	for _, action := range []string{"hangup after ACK", "cancel gathering", "replace account while gathering"} {
		t.Run(action, func(t *testing.T) {
			manager := testManager(t)
			ctx, cancel := context.WithTimeout(manager.ctx, 8*time.Second)
			defer cancel()
			peerMedia, err := media.NewCall(ctx, "delayed-peer", media.Settings{BindAddress: "127.0.0.1:0", ICEPolicy: "required", Codecs: []string{"PCMA"}, DisableAutoRecovery: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer peerMedia.Close()
			offer := peerMedia.LocalSDP("127.0.0.1")
			ua, err := sipgo.NewUA()
			if err != nil {
				t.Fatal(err)
			}
			defer ua.Close()
			server, err := sipgo.NewServer(ua)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			invites, acks, byes := make(chan *wire.Request, 4), make(chan *wire.Request, 4), make(chan *wire.Request, 4)
			server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
				res := wire.NewResponseFromRequest(req, 200, "OK", nil)
				res.AppendHeader(wire.NewHeader("Expires", "3600"))
				_ = tx.Respond(res)
			})
			server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
				invites <- req.Clone()
				res := wire.NewResponseFromRequest(req, 200, "OK", offer)
				res.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
				res.AppendHeader(wire.NewHeader("Contact", "<sip:peer@"+listener.LocalAddr().String()+">"))
				_ = tx.Respond(res)
			})
			server.OnAck(func(req *wire.Request, _ wire.ServerTransaction) { acks <- req.Clone() })
			server.OnBye(func(req *wire.Request, tx wire.ServerTransaction) {
				byes <- req.Clone()
				_ = tx.Respond(wire.NewResponseFromRequest(req, 200, "OK", nil))
			})
			served := make(chan struct{})
			go func() { defer close(served); server.ServeUDP(listener) }()
			defer func() { listener.Close(); <-served }()
			changed := make(chan struct{}, 32)
			manager.mu.Lock()
			manager.settings = media.Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, DisableAutoRecovery: true}
			manager.emit = func(string, any) {
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			manager.mu.Unlock()
			cfg := config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0", DelayedOffer: true, ICEPolicy: "required", Codecs: []string{"PCMA"}}
			var gathering <-chan error
			if action != "hangup after ACK" {
				stun, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer stun.Close()
				cfg.ICEServers = []config.ICEServer{{URLs: []string{"stun:" + stun.LocalAddr().String()}}}
				seen := make(chan error, 1)
				go func() {
					packet := make([]byte, 1500)
					_, _, err := stun.ReadFrom(packet)
					seen <- err
				}()
				gathering = seen
			}
			if err := manager.Enable("office", cfg); err != nil {
				t.Fatal(err)
			}
			defer manager.Disable("office")
			waitRegistrationState(t, manager, changed, "registered")
			if err := manager.Dial("office", "sip:peer@"+listener.LocalAddr().String()); err != nil {
				t.Fatal(err)
			}
			invite := delayedPeerRequest(t, ctx, invites)
			if len(invite.Body()) != 0 || invite.ContentType() != nil {
				t.Fatal("account delayed-offer setting did not produce a bodyless INVITE")
			}
			id := string(*invite.CallID())
			if gathering != nil {
				select {
				case err := <-gathering:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("delayed answer did not begin candidate gathering")
				}
				if action == "replace account while gathering" {
					cfg.Username = "replacement"
					cfg.ICEServers = nil
					if err := manager.Enable("office", cfg); err != nil {
						t.Fatal(err)
					}
					waitRegistrationState(t, manager, changed, "registered")
					if calls := manager.Snapshot().Calls; len(calls) != 0 {
						t.Fatalf("replaced account restored an old call: %+v", calls)
					}
					return
				}
				if err := manager.Hangup(id); err != nil {
					t.Fatal(err)
				}
			}
			ack := delayedPeerRequest(t, ctx, acks)
			if ack.ContentType() == nil || !strings.Contains(string(ack.Body()), "m=audio ") {
				t.Fatal("ACK did not deliver an SDP answer")
			}
			if action == "cancel gathering" {
				if !strings.Contains(string(ack.Body()), "m=audio 0 ") {
					t.Fatal("canceled candidate gathering accepted remote media")
				}
			} else {
				if !strings.Contains(string(ack.Body()), "a=ice-ufrag:") {
					t.Fatal("ACK did not deliver local ICE credentials")
				}
				calls := manager.Snapshot().Calls
				if len(calls) != 1 || calls[0].State == "connected" || calls[0].Stats.ICEState == "connected" || calls[0].Stats.PacketsSent != 0 || calls[0].Stats.AudioRecoveryPending {
					t.Fatalf("media started before the peer completed ICE: %+v", calls)
				}
				if err := manager.Hangup(id); err != nil {
					t.Fatal(err)
				}
			}
			bye := delayedPeerRequest(t, ctx, byes)
			if string(*bye.CallID()) != id || len(manager.Snapshot().Calls) != 0 {
				t.Fatal("hangup did not end the original dialog and local call")
			}
		})
	}
}

func delayedPeerRequest(t *testing.T, ctx context.Context, requests <-chan *wire.Request) *wire.Request {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-ctx.Done():
		t.Fatal("timed out waiting for peer SIP request")
		return nil
	}
}
