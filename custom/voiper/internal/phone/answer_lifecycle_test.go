package phone

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/pkg/media"
	"github.com/megakuul/voiper/pkg/sip"
)

// The caller cannot start ICE until it receives the SDP answer. Answering must
// therefore complete SIP before waiting for connectivity or opening audio.
func TestIncomingICEAnswerPrecedesConnectivity(t *testing.T) {
	manager := testManager(t)
	ctx, cancel := context.WithTimeout(manager.ctx, 5*time.Second)
	defer cancel()
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	registrar, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	registrar.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "3600"))
		_ = tx.Respond(res)
	})
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() { defer close(served); registrar.ServeUDP(listener) }()
	defer func() { listener.Close(); <-served }()
	incoming, changed := make(chan struct{}, 1), make(chan struct{}, 32)
	manager.mu.Lock()
	manager.settings = media.Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, DisableAutoRecovery: true}
	manager.emit = func(name string, _ any) {
		select {
		case changed <- struct{}{}:
		default:
		}
		if name == "incoming-call" {
			select {
			case incoming <- struct{}{}:
			default:
			}
		}
	}
	manager.mu.Unlock()
	cfg := config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "bob", LocalAddress: "127.0.0.1:0", ICEPolicy: "required", Codecs: []string{"PCMA"}}
	if err = manager.Enable("office", cfg); err != nil {
		t.Fatal(err)
	}
	defer manager.Disable("office")
	waitRegistrationState(t, manager, changed, "registered")
	owner, err := manager.getAccount("office")
	if err != nil {
		t.Fatal(err)
	}
	callerMedia, err := media.NewCall(ctx, "ice-caller", media.Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, ICEPolicy: "required", DisableAutoRecovery: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer callerMedia.Close()
	offer := callerMedia.LocalSDP("127.0.0.1")
	if !strings.Contains(string(offer), "a=ice-ufrag:") {
		t.Fatal("caller did not produce an ICE offer")
	}
	callerEvents := make(chan sip.Event, 16)
	caller, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "alice", LocalAddress: "127.0.0.1:0"}, func(e sip.Event) { callerEvents <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	id, err := caller.Dial(ctx, "sip:bob@"+owner.client.LocalAddr(), offer)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-incoming:
	case <-ctx.Done():
		t.Fatal("incoming call was not delivered")
	}
	// Delay the initial worker before Connect, as unfinished early media can do.
	initialReady := make(chan struct{})
	defer close(initialReady)
	manager.mu.Lock()
	manager.calls[id].earlyDone = initialReady
	manager.mu.Unlock()
	answered := make(chan error, 1)
	go func() { answered <- manager.Answer(id) }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-callerEvents:
			if event.Type == "ended" {
				t.Fatalf("call failed before receiving SDP answer: %s", event.Message)
			}
			if event.Type != "connected" {
				continue
			}
			if !strings.Contains(string(event.SDP), "a=ice-ufrag:") {
				t.Fatal("SIP answer is missing ICE credentials")
			}
			select {
			case err := <-answered:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("Answer remained blocked after SIP ACK")
			}
			manager.mu.Lock()
			current := manager.calls[id]
			ready := current != nil && current.mediaConnected
			manager.mu.Unlock()
			if current == nil || ready {
				t.Fatal("pending media call ended or connected without caller starting ICE")
			}
			updateCtx, stopUpdate := context.WithTimeout(ctx, time.Second)
			err := caller.Reinvite(updateCtx, id, []byte(strings.Replace(string(offer), "a=sendrecv", "a=sendonly", 1)))
			stopUpdate()
			var response *sip.ResponseError
			if !errors.As(err, &response) || response.Status != 488 {
				t.Fatalf("renegotiation overtook initial media setup: %v", err)
			}
			if stats := current.media.Stats(); stats.DeviceSampleRate != 0 || stats.Held {
				t.Fatalf("rejected renegotiation changed pending media: %+v", stats)
			}
			if err := manager.Hangup(id); err != nil {
				t.Fatal(err)
			}
			return
		case <-timer.C:
			cancel()
			t.Fatal("SIP answer waited for ICE connectivity")
		}
	}
}
