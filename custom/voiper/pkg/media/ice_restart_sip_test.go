package media

import (
	"context"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

func TestSIPReinviteCommitsICERestart(t *testing.T) {
	a, b := connectedICECalls(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callerEvents, calleeEvents := make(chan sip.Event, 32), make(chan sip.Event, 32)
	caller, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "alice", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) { callerEvents <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	callee, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) { calleeEvents <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	next := func(events <-chan sip.Event, kind string) sip.Event {
		t.Helper()
		for {
			select {
			case event := <-events:
				if event.Type == kind {
					return event
				}
			case <-ctx.Done():
				t.Fatalf("waiting for %s: %v", kind, ctx.Err())
				return sip.Event{}
			}
		}
	}
	callee.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) { return b.AnswerOffer(offer, "127.0.0.1") })
	caller.SetAnswerCommitHandler(func(_ string, offer, answer []byte) error { return a.AcceptAnswer(offer, answer) })
	if err = caller.DialID(ctx, "restart", "sip:bob@"+callee.LocalAddr(), a.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	incoming := next(calleeEvents, "incoming")
	if err = callee.Answer(ctx, incoming.CallID, b.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	next(callerEvents, "connected")
	exchangePCM(t, a, b)
	before := a.Stats().LocalAddress
	var offer []byte
	err = caller.ReinviteWithOffer(ctx, "restart", func() ([]byte, error) {
		var gatherErr error
		offer, gatherErr = a.RestartOffer(ctx, "127.0.0.1")
		return offer, gatherErr
	})
	if err != nil {
		a.CancelRestart(offer)
		t.Fatal(err)
	}
	next(callerEvents, "updated")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for a.Stats().ICERestartPending || b.Stats().ICERestartPending {
		select {
		case <-ctx.Done():
			t.Fatal("SIP answer did not complete ICE restart")
		case <-ticker.C:
		}
	}
	if a.Stats().LocalAddress == before || a.Stats().LastError != "" || b.Stats().LastError != "" {
		t.Fatalf("restart failed: %+v %+v", a.Stats(), b.Stats())
	}
	exchangePCM(t, a, b)
	if err = caller.Hangup(ctx, "restart"); err != nil {
		t.Fatal(err)
	}
	next(calleeEvents, "ended")
}
