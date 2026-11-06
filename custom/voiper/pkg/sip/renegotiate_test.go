package sip

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func sendBodylessInvite(t *testing.T, sender *Client, id string) (*wire.Request, *wire.Response) {
	t.Helper()
	cl, err := sender.getCall(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cl.operation.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	defer cl.operation.Unlock()
	cl.mu.Lock()
	remote := cl.remote
	cl.mu.Unlock()
	request, response, transaction, err := sender.dialogExchange(ctx, cl, wire.NewRequest(wire.INVITE, remote))
	if transaction != nil {
		defer transaction.Terminate()
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("bodyless INVITE rejected: %s", response.StartLine())
	}
	return request, response
}

func TestBodylessReinviteAnswerInACK(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "initial-callee"
		if reverse {
			name = "initial-caller"
		}
		t.Run(name, func(t *testing.T) {
			caller, callerEvents := newTestClient(t, "alice")
			callee, calleeEvents := newTestClient(t, "bob")
			id := connectTestCall(t, caller, callee, callerEvents, calleeEvents)
			sender, receiver, events := caller, callee, calleeEvents
			if reverse {
				sender, receiver, events = callee, caller, callerEvents
			}
			localOffer := strings.Replace(offerSDP, "o=- 1 1", "o=- 1 2", 1)
			receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) {
				if offer != nil {
					return nil, errors.New("expected nil offer")
				}
				return []byte(localOffer), nil
			})
			var validations atomic.Int32
			receiver.SetAnswerHandler(func(_ string, answer []byte) error {
				validations.Add(1)
				if string(answer) != offerSDP {
					return errors.New("invalid answer")
				}
				return nil
			})
			request, response := sendBodylessInvite(t, sender, id)
			if string(response.Body()) != localOffer {
				t.Fatal("200 did not contain requested local offer")
			}
			cl, _ := receiver.getCall(id)
			cl.mu.Lock()
			before := string(cl.localSDP)
			cl.mu.Unlock()
			if before != offerSDP || validations.Load() != 0 {
				t.Fatal("pending offer changed established session before ACK")
			}
			senderCall, _ := sender.getCall(id)
			if err := sender.acknowledgeInvite(senderCall, request, response, []byte(offerSDP)); err != nil {
				t.Fatal(err)
			}
			updated := nextEvent(t, events, "updated")
			if string(updated.SDP) != offerSDP || validations.Load() != 1 {
				t.Fatal("ACK answer was not validated and delivered once")
			}
			cl.mu.Lock()
			after := string(cl.localSDP)
			cl.mu.Unlock()
			if after != localOffer {
				t.Fatal("accepted local offer was not retained for session refresh")
			}
			if err := sender.acknowledgeInvite(senderCall, request, response, []byte(offerSDP)); err != nil {
				t.Fatal(err)
			}
			if err := sender.Hangup(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			nextEvent(t, events, "ended")
			if validations.Load() != 1 {
				t.Fatal("duplicate ACK repeated media negotiation")
			}
		})
	}
}

func TestBodylessReinviteInvalidACKPreservesSession(t *testing.T) {
	for _, answer := range []string{"", "invalid SDP"} {
		name := "missing-answer"
		if answer != "" {
			name = "malformed-answer"
		}
		t.Run(name, func(t *testing.T) {
			caller, callerEvents := newTestClient(t, "alice")
			receiver, events := newTestClient(t, "bob")
			id := connectTestCall(t, caller, receiver, callerEvents, events)
			receiver.SetOfferHandler(func(_ string, _ []byte) ([]byte, error) {
				return []byte(strings.Replace(offerSDP, "o=- 1 1", "o=- 1 2", 1)), nil
			})
			receiver.SetAnswerHandler(func(_ string, _ []byte) error { return errors.New("malformed answer") })
			request, response := sendBodylessInvite(t, caller, id)
			cl, _ := caller.getCall(id)
			if err := caller.acknowledgeInvite(cl, request, response, []byte(answer)); err != nil {
				t.Fatal(err)
			}
			failed := nextEvent(t, events, "renegotiation-failed")
			if failed.Message == "" {
				t.Fatal("missing negotiation failure detail")
			}
			preserved, err := receiver.getCall(id)
			if err != nil {
				t.Fatal("bad ACK answer ended established call")
			}
			preserved.mu.Lock()
			localSDP, ready := string(preserved.localSDP), preserved.ready
			preserved.mu.Unlock()
			if localSDP != offerSDP || !ready {
				t.Fatal("bad ACK answer changed established session")
			}
			if err := caller.SendDTMF(context.Background(), id, "5"); err != nil {
				t.Fatal(err)
			}
			nextEvent(t, events, "dtmf")
		})
	}
}

func TestBodylessReinviteIgnoresMismatchedACK(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, events := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, events)
	receiver.SetOfferHandler(func(_ string, _ []byte) ([]byte, error) { return []byte(offerSDP), nil })
	receiver.SetAnswerHandler(func(_ string, answer []byte) error {
		if string(answer) != offerSDP {
			return errors.New("unexpected answer")
		}
		return nil
	})
	request, response := sendBodylessInvite(t, caller, id)
	cl, _ := caller.getCall(id)
	wrongRequest := request.Clone()
	wrongRequest.CSeq().SeqNo++
	if err := caller.acknowledgeInvite(cl, wrongRequest, response, []byte("wrong sequence")); err != nil {
		t.Fatal(err)
	}
	if err := caller.acknowledgeInvite(cl, request, response, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "updated")
}

func TestAbortReleasesBodylessReinviteWait(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, events := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, events)
	receiver.SetOfferHandler(func(_ string, _ []byte) ([]byte, error) { return []byte(offerSDP), nil })
	receiver.SetAnswerHandler(func(_ string, _ []byte) error { return nil })
	sendBodylessInvite(t, caller, id)
	cl, _ := receiver.getCall(id)
	if err := receiver.AbortCall(id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cl.operation.Lock(ctx); err != nil {
		t.Fatal("ACK wait did not release after cancellation")
	}
	cl.operation.Unlock()
}

func TestMissingReinviteACKEndsRemoteDialog(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the RFC3261 32-second ACK deadline")
	}
	caller, callerEvents := newTestClient(t, "alice")
	receiver, events := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, events)
	receiver.SetOfferHandler(func(_ string, _ []byte) ([]byte, error) { return []byte(offerSDP), nil })
	receiver.SetAnswerHandler(func(_ string, _ []byte) error { return nil })
	sendBodylessInvite(t, caller, id)
	deadline := time.NewTimer(35 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Type != "ended" {
				continue
			}
			if !strings.Contains(event.Message, "ACK timed out") {
				t.Fatalf("unexpected termination: %+v", event)
			}
			nextEvent(t, callerEvents, "ended")
			return
		case <-deadline.C:
			t.Fatal("missing ACK did not terminate remote dialog")
		}
	}
}

func TestNextOfferWaitsForAnswerCommit(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) { return offer, nil })
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var commits atomic.Int32
	caller.SetAnswerCommitHandler(func(callID string, offer, answer []byte) error {
		if callID != id || string(offer) != string(answer) {
			return errors.New("commit did not receive the actual offer/answer pair")
		}
		if commits.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		firstDone <- caller.ReinviteWithOffer(ctx, id, func() ([]byte, error) { return []byte(offerSDP), nil })
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first negotiation did not reach commit")
	}
	builtSecond := make(chan struct{})
	go func() {
		secondDone <- caller.ReinviteWithOffer(ctx, id, func() ([]byte, error) {
			select {
			case <-builtSecond:
			default:
				close(builtSecond)
			}
			return []byte(strings.Replace(offerSDP, "o=- 1 1", "o=- 1 2", 1)), nil
		})
	}()
	select {
	case <-builtSecond:
		t.Fatal("next offer overwrote media state before previous answer committed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for _, result := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("negotiation did not finish")
		}
	}
	if commits.Load() != 2 {
		t.Fatal("answers were not committed exactly once")
	}
}
