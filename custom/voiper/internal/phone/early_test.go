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

func TestDelayedReliableOfferEarlyUpdateSelectsLatestPreparedMedia(t *testing.T) {
	manager := testManager(t)
	ctx, cancel := context.WithTimeout(manager.ctx, 8*time.Second)
	defer cancel()
	settings := media.Settings{BindAddress: "127.0.0.1:0", ICEPolicy: "required", Codecs: []string{"PCMA"}, DisableAutoRecovery: true}
	original, err := media.NewCall(ctx, "original-offer", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	replacement, err := media.NewCall(ctx, "updated-offer", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := sipgo.NewClient(ua)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	invites, pracks, acks, byes := make(chan *wire.Request, 4), make(chan *wire.Request, 4), make(chan *wire.Request, 4), make(chan *wire.Request, 4)
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "3600"))
		_ = tx.Respond(res)
	})
	server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
		invites <- req.Clone()
		_ = tx.Respond(wire.NewResponseFromRequest(req, 180, "Ringing", nil))
	})
	server.OnPrack(func(req *wire.Request, tx wire.ServerTransaction) {
		_ = tx.Respond(wire.NewResponseFromRequest(req, 200, "OK", nil))
		pracks <- req.Clone()
	})
	server.OnAck(func(req *wire.Request, _ wire.ServerTransaction) { acks <- req.Clone() })
	server.OnBye(func(req *wire.Request, tx wire.ServerTransaction) {
		_ = tx.Respond(wire.NewResponseFromRequest(req, 200, "OK", nil))
		byes <- req.Clone()
	})
	served := make(chan struct{})
	go func() { defer close(served); _ = server.ServeUDP(listener) }()
	defer func() { listener.Close(); <-served }()
	changed := make(chan struct{}, 32)
	manager.mu.Lock()
	manager.settings = settings
	manager.emit = func(string, any) {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	manager.mu.Unlock()
	cfg := config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0", DelayedOffer: true, ICEPolicy: "required", Codecs: []string{"PCMA"}}
	if err := manager.Enable("office", cfg); err != nil {
		t.Fatal(err)
	}
	defer manager.Disable("office")
	waitRegistrationState(t, manager, changed, "registered")
	if err := manager.Dial("office", "sip:peer@"+listener.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}
	invite := delayedPeerRequest(t, ctx, invites)
	if len(invite.Body()) != 0 {
		t.Fatal("delayed call sent an initial offer")
	}
	response := wire.NewResponseFromRequest(invite, 183, "Session Progress", original.LocalSDP("127.0.0.1"))
	response.To().Params.Add("tag", "early-update-peer")
	response.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	response.AppendHeader(wire.NewHeader("Contact", "<sip:peer@"+listener.LocalAddr().String()+">"))
	response.AppendHeader(wire.NewHeader("Require", "100rel"))
	response.AppendHeader(wire.NewHeader("RSeq", "1"))
	destination, err := net.ResolveUDPAddr("udp", invite.Source())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listener.WriteTo([]byte(response.String()), destination); err != nil {
		t.Fatal(err)
	}
	prack := delayedPeerRequest(t, ctx, pracks)
	initialCredentials := iceCredential(string(prack.Body()))
	if initialCredentials == "" {
		t.Fatal("PRACK did not answer with ICE credentials")
	}
	var updatedAnswer []byte
	for sequence := uint32(10); sequence < 20; sequence++ {
		update := wire.NewRequest(wire.UPDATE, invite.Contact().Address)
		update.AppendHeader(&wire.FromHeader{Address: response.To().Address, Params: response.To().Params.Clone()})
		update.AppendHeader(&wire.ToHeader{Address: invite.From().Address, Params: invite.From().Params.Clone()})
		update.AppendHeader(wire.HeaderClone(invite.CallID()))
		update.AppendHeader(&wire.CSeqHeader{SeqNo: sequence, MethodName: wire.UPDATE})
		update.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
		update.SetBody(replacement.LocalSDP("127.0.0.1"))
		res, err := peer.Do(ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode == 200 {
			updatedAnswer = res.Body()
			break
		}
		if res.StatusCode != 491 {
			t.Fatalf("early UPDATE failed: %s", res)
		}
		// PRACK response and UPDATE can cross while offer/answer is serialized.
		time.Sleep(10 * time.Millisecond)
	}
	updatedCredentials := iceCredential(string(updatedAnswer))
	if updatedCredentials == "" || updatedCredentials == initialCredentials {
		t.Fatal("early UPDATE did not allocate its own prepared transport")
	}
	id := string(*invite.CallID())
	manager.mu.Lock()
	call := manager.calls[id]
	preparedOnly := call != nil && call.media == nil
	manager.mu.Unlock()
	if !preparedOnly {
		t.Fatal("provisional media was selected before the final dialog")
	}
	response.StatusCode, response.Reason = 200, "OK"
	response.RemoveHeader("Require")
	response.RemoveHeader("RSeq")
	if _, err := listener.WriteTo([]byte(response.String()), destination); err != nil {
		t.Fatal(err)
	}
	if ack := delayedPeerRequest(t, ctx, acks); len(ack.Body()) != 0 {
		t.Fatal("final ACK repeated the answer already negotiated in PRACK/UPDATE")
	}
	manager.mu.Lock()
	call = manager.calls[id]
	var selected *media.Call
	if call != nil {
		selected = call.media
	}
	manager.mu.Unlock()
	if selected == nil || iceCredential(string(selected.LocalSDP("127.0.0.1"))) != updatedCredentials {
		t.Fatal("final selection attached stale prepared media")
	}
	if stats := selected.Stats(); stats.ICEState == "connected" || stats.PacketsSent != 0 || stats.AudioRecoveryPending {
		t.Fatalf("capture or ICE started before the peer completed connectivity: %+v", stats)
	}
	if err := manager.Hangup(id); err != nil {
		t.Fatal(err)
	}
	delayedPeerRequest(t, ctx, byes)
	if len(manager.Snapshot().Calls) != 0 {
		t.Fatal("hangup retained selected early media")
	}
}

func iceCredential(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		if strings.HasPrefix(line, "a=ice-ufrag:") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
