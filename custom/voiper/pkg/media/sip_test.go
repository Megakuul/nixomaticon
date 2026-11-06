package media

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

func TestSIPCallCarriesDuplexAudio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eventsA, eventsB := make(chan sip.Event, 32), make(chan sip.Event, 32)
	caller, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "alice", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) { eventsA <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	receiver, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) { eventsB <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	next := func(events <-chan sip.Event, kind string) sip.Event {
		t.Helper()
		for {
			select {
			case event := <-events:
				if event.Type == kind {
					return event
				}
			case <-ctx.Done():
				t.Fatalf("waiting for SIP %s: %v", kind, ctx.Err())
				return sip.Event{}
			}
		}
	}
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMU", "PCMA"}}
	a, err := NewCall(ctx, "call", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.openAudio = syntheticAudio
	if err = caller.DialID(ctx, "call", "sip:bob@"+receiver.LocalAddr(), a.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	incoming := next(eventsB, "incoming")
	b, err := NewCall(ctx, incoming.CallID, settings, incoming.SDP)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.openAudio = syntheticAudio
	if err = receiver.Answer(ctx, incoming.CallID, b.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if err = b.Connect(incoming.SDP); err != nil {
		t.Fatal(err)
	}
	connected := next(eventsA, "connected")
	if err = a.Connect(connected.SDP); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 320)
	for i := 0; i < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], 1200)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	heardA, heardB := false, false
	output := make([]byte, 320)
	for !heardA || !heardB {
		select {
		case <-ctx.Done():
			t.Fatal("SIP connected but two-way audio failed")
		case <-ticker.C:
			a.stream.Capture.Write(frame)
			b.stream.Capture.Write(frame)
			for _, side := range []struct {
				call  *Call
				heard *bool
			}{{a, &heardA}, {b, &heardB}} {
				n := side.call.stream.Playback.Read(output)
				for i := 0; i+1 < n; i += 2 {
					if int16(binary.LittleEndian.Uint16(output[i:])) > 500 {
						*side.heard = true
					}
				}
			}
		}
	}
	receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) { return b.AnswerOffer(offer, "127.0.0.1") })
	caller.SetAnswerCommitHandler(func(_ string, offer, answer []byte) error { return a.AcceptAnswer(offer, answer) })
	if err = caller.ReinviteWithOffer(ctx, "call", func() ([]byte, error) { return codecOffer(t, a, "PCMA"), nil }); err != nil {
		t.Fatal(err)
	}
	next(eventsA, "updated")
	exchangePCM(t, a, b)
	if a.Stats().Codec != "PCMA" || b.Stats().Codec != "PCMA" {
		t.Fatal("SIP re-INVITE did not commit codec update")
	}
	if err = caller.Hangup(ctx, "call"); err != nil {
		t.Fatal(err)
	}
	next(eventsA, "ended")
	next(eventsB, "ended")
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
}
