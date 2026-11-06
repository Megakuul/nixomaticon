package media

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/pkg/sip"
)

func TestBodylessSIPReinviteValidatesBeforeApplyingMedia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan sip.Event, 32)
	receiver, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	next := func(kind string) sip.Event {
		t.Helper()
		for {
			select {
			case event := <-events:
				if event.Type == kind {
					return event
				}
			case <-ctx.Done():
				t.Fatalf("waiting for SIP %s", kind)
				return sip.Event{}
			}
		}
	}
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	go server.ServeUDP(socket)
	peer, err := sipgo.NewClient(ua, sipgo.WithClientAddr(socket.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	contact := wire.ContactHeader{Address: wire.Uri{Scheme: "sip", User: "alice", Host: "127.0.0.1", Port: socket.LocalAddr().(*net.UDPAddr).Port}}
	dialogs := sipgo.NewDialogClientCache(peer, contact)
	var recipient wire.Uri
	if err = wire.ParseUri("sip:bob@"+receiver.LocalAddr(), &recipient); err != nil {
		t.Fatal(err)
	}
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA", "PCMU"}, DisableAutoRecovery: true}
	a, err := NewCall(ctx, "peer", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.openAudio = syntheticAudio
	offer := a.LocalSDP("127.0.0.1")
	dialog, err := dialogs.Invite(ctx, recipient, offer, wire.NewHeader("Content-Type", "application/sdp"))
	if err != nil {
		t.Fatal(err)
	}
	defer dialog.Close()
	incoming := next("incoming")
	b, err := NewCall(ctx, incoming.CallID, settings, incoming.SDP)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.openAudio = syntheticAudio
	answered := make(chan error, 1)
	go func() { answered <- receiver.Answer(ctx, incoming.CallID, b.LocalSDP("127.0.0.1")) }()
	if err = dialog.WaitAnswer(ctx, sipgo.AnswerOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = dialog.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-answered; err != nil {
		t.Fatal(err)
	}
	if err = b.Connect(offer); err != nil {
		t.Fatal(err)
	}
	if err = a.Connect(dialog.InviteResponse.Body()); err != nil {
		t.Fatal(err)
	}
	receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) {
		if offer == nil {
			return b.CurrentOffer("127.0.0.1"), nil
		}
		return b.AnswerOffer(offer, "127.0.0.1")
	})
	receiver.SetAnswerHandler(func(_ string, answer []byte) error { return b.ValidateAnswer(answer) })
	var commits atomic.Int32
	receiver.SetAnswerCommitHandler(func(_ string, offer, answer []byte) error {
		if err := b.AcceptAnswer(offer, answer); err != nil {
			return err
		}
		commits.Add(1)
		return nil
	})
	for _, valid := range []bool{true, false} {
		previous := b.pipeline
		response, err := dialog.Do(ctx, wire.NewRequest(wire.INVITE, recipient))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("bodyless re-INVITE status %d", response.StatusCode)
		}
		answer, err := a.AnswerOffer(response.Body(), "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if !valid {
			answer = []byte(strings.Replace(string(answer), "RTP/AVP 8 101", "RTP/AVP 0 101", 1))
		}
		ack := wire.NewRequest(wire.ACK, recipient)
		ack.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
		ack.SetBody(answer)
		if err = dialog.WriteRequest(ack); err != nil {
			t.Fatal(err)
		}
		if valid {
			next("updated")
		} else {
			next("renegotiation-failed")
		}
		if commits.Load() != 1 || b.pipeline != previous || b.Stats().Codec != "PCMA" {
			t.Fatal("answer validation unexpectedly replaced media")
		}
		exchangePCM(t, a, b)
	}
	if err = dialog.Bye(ctx); err != nil {
		t.Fatal(err)
	}
	next("ended")
}
