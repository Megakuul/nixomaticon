package sip

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func sendEarlyOffer(t *testing.T, peer *forkPeer, invite *wire.Request, tag string, sequence int, body string) *wire.Response {
	t.Helper()
	response := wire.NewResponseFromRequest(invite, 183, "Session Progress", []byte(body))
	response.To().Params.Add("tag", tag)
	response.AppendHeader(wire.HeaderClone(&peer.client.contact))
	response.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	response.AppendHeader(wire.NewHeader("Require", "100rel"))
	response.AppendHeader(wire.NewHeader("RSeq", fmt.Sprint(sequence)))
	response.SetDestination(invite.Source())
	peer.sendResponse(t, response)
	return response
}

func TestReliableDelayedOffersSelectOneForkAndCancelOthers(t *testing.T) {
	first := newForkPeer(t, false)
	second := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	type candidate struct {
		ctx    context.Context
		offer  string
		answer string
	}
	prepared := make(chan candidate, 4)
	var selected atomic.Int32
	var preparations atomic.Int32
	err := caller.DialDelayedOptionsID(context.Background(), "early-forks", first.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(ctx context.Context, offer []byte) (DelayedAnswer, error) {
		index := preparations.Add(1)
		answer := strings.Replace(string(offer), "audio ", fmt.Sprintf("audio %d", index), 1)
		prepared <- candidate{ctx, string(offer), answer}
		return DelayedAnswer{SDP: []byte(answer), Select: func() error { selected.Store(index); return nil }}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, first.invites)
	if !hasToken(invite.GetHeaders("Supported"), "100rel") || len(invite.Body()) != 0 {
		t.Fatal("fork-aware delayed INVITE did not advertise reliable offer support")
	}
	firstResponse := sendEarlyOffer(t, first, invite, "first", 100, offerSDP)
	firstAck := nextForkRequest(t, first.pracks)
	firstCandidate := <-prepared
	secondOffer := strings.Replace(offerSDP, "audio 8000", "audio 9000", 1)
	sendEarlyOffer(t, second, invite, "second", 900, secondOffer)
	secondAck := nextForkRequest(t, second.pracks)
	secondCandidate := <-prepared
	if string(firstAck.Body()) != firstCandidate.answer || string(secondAck.Body()) != secondCandidate.answer || selected.Load() != 0 {
		t.Fatal("PRACK did not answer each fork independently before selection")
	}
	if !strings.HasPrefix(firstAck.GetHeader("RAck").Value(), "100 ") || !strings.HasPrefix(secondAck.GetHeader("RAck").Value(), "900 ") {
		t.Fatal("fork RSeq values interfered")
	}
	first.sendResponse(t, firstResponse)
	final := wire.NewResponseFromRequest(invite, 200, "OK", nil)
	final.To().Params.Add("tag", "second")
	final.AppendHeader(wire.HeaderClone(&second.client.contact))
	final.SetDestination(invite.Source())
	second.sendResponse(t, final)
	if ack := nextForkRequest(t, second.acks); len(ack.Body()) != 0 {
		t.Fatal("final ACK repeated the answer already sent in PRACK")
	}
	connected := nextEvent(t, events, "connected")
	if selected.Load() != 2 || string(connected.SDP) != secondOffer || !connected.DelayedOffer || preparations.Load() != 2 {
		t.Fatal("final response selected the wrong prepared media")
	}
	select {
	case <-firstCandidate.ctx.Done():
	default:
		t.Fatal("unselected media candidate survived")
	}
	select {
	case <-secondCandidate.ctx.Done():
		t.Fatal("selected candidate context ended with the dialing worker")
	default:
	}
	first.success(t, invite, "first", false)
	if ack := nextForkRequest(t, first.acks); len(ack.Body()) != 0 {
		t.Fatal("extra fork ACK repeated a completed offer/answer exchange")
	}
	nextForkRequest(t, first.byes)
	if err := caller.Hangup(context.Background(), "early-forks"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondCandidate.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("selected media survived dialog teardown")
	}
}

func TestReliableDelayedOfferRejectsChangedFinalSDP(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var selected atomic.Bool
	if err := caller.DialDelayedOptionsID(context.Background(), "changed-final", peer.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(context.Context, []byte) (DelayedAnswer, error) {
		return DelayedAnswer{SDP: []byte(offerSDP), Select: func() error { selected.Store(true); return nil }}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	sendEarlyOffer(t, peer, invite, "selected", 1, strings.Replace(offerSDP, "audio 8000", "audio 9000", 1))
	nextForkRequest(t, peer.pracks)
	peer.success(t, invite, "selected", false)
	nextForkRequest(t, peer.acks)
	nextForkRequest(t, peer.byes)
	if ended := nextEvent(t, events, "ended"); !strings.Contains(ended.Message, "changed") || selected.Load() {
		t.Fatalf("changed final SDP activated stale media: %+v", ended)
	}
}

func TestDelayedOptionsFinalOfferSelectsBeforeConnected(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var selected atomic.Bool
	if err := caller.DialDelayedOptionsID(context.Background(), "final-options", peer.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(context.Context, []byte) (DelayedAnswer, error) {
		return DelayedAnswer{SDP: []byte(offerSDP), Select: func() error { selected.Store(true); return nil }}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	peer.success(t, invite, "selected", false)
	if ack := nextForkRequest(t, peer.acks); string(ack.Body()) != offerSDP || !selected.Load() {
		t.Fatal("final offer answer was not selected before ACK")
	}
	nextEvent(t, events, "connected")
	caller.Hangup(context.Background(), "final-options")
}

func TestReliableDelayedOfferMayFollowBodylessRinging(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var preparations atomic.Int32
	if err := caller.DialDelayedOptionsID(context.Background(), "early-ringing", peer.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(context.Context, []byte) (DelayedAnswer, error) {
		preparations.Add(1)
		return DelayedAnswer{SDP: []byte(offerSDP)}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	sendEarlyOffer(t, peer, invite, "selected", 1, "")
	if prack := nextForkRequest(t, peer.pracks); len(prack.Body()) != 0 || preparations.Load() != 0 {
		t.Fatal("bodyless provisional response attempted SDP negotiation")
	}
	sendEarlyOffer(t, peer, invite, "selected", 2, offerSDP)
	if prack := nextForkRequest(t, peer.pracks); string(prack.Body()) != offerSDP || preparations.Load() != 1 {
		t.Fatal("later reliable offer did not receive an answer")
	}
	peer.success(t, invite, "selected", false)
	if ack := nextForkRequest(t, peer.acks); len(ack.Body()) != 0 {
		t.Fatal("final response repeated the early answer")
	}
	nextEvent(t, events, "connected")
	caller.Hangup(context.Background(), "early-ringing")
}

func TestReliableDelayedOfferRejectedForkDoesNotPreventAnotherAnswer(t *testing.T) {
	first, second := newForkPeer(t, false), newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var preparations atomic.Int32
	if err := caller.DialDelayedOptionsID(context.Background(), "early-rejected", first.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(context.Context, []byte) (DelayedAnswer, error) {
		if preparations.Add(1) == 1 {
			return DelayedAnswer{}, fmt.Errorf("unsupported codec")
		}
		return DelayedAnswer{SDP: []byte(offerSDP)}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, first.invites)
	sendEarlyOffer(t, first, invite, "rejected", 1, offerSDP)
	if prack := nextForkRequest(t, first.pracks); !strings.Contains(string(prack.Body()), "m=audio 0 ") {
		t.Fatal("unsupported fork was not answered with rejected media")
	}
	sendEarlyOffer(t, second, invite, "accepted", 1, offerSDP)
	if prack := nextForkRequest(t, second.pracks); string(prack.Body()) != offerSDP {
		t.Fatal("independent fork did not negotiate successfully")
	}
	second.success(t, invite, "accepted", false)
	nextForkRequest(t, second.acks)
	nextEvent(t, events, "connected")
	caller.Hangup(context.Background(), "early-rejected")
}

func TestReliableDelayedOfferCancellationReleasesPreparation(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	preparing := make(chan context.Context, 1)
	var selected atomic.Bool
	if err := caller.DialDelayedOptionsID(context.Background(), "early-cancel", peer.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(ctx context.Context, _ []byte) (DelayedAnswer, error) {
		preparing <- ctx
		<-ctx.Done()
		return DelayedAnswer{SDP: []byte(offerSDP), Select: func() error { selected.Store(true); return nil }}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	sendEarlyOffer(t, peer, invite, "canceled", 1, offerSDP)
	var candidate context.Context
	select {
	case candidate = <-preparing:
	case <-time.After(time.Second):
		t.Fatal("offer preparation did not start")
	}
	if err := caller.Hangup(context.Background(), "early-cancel"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-candidate.Done():
	case <-time.After(time.Second):
		t.Fatal("hangup did not cancel preparation")
	}
	peer.success(t, invite, "canceled", false)
	nextForkRequest(t, peer.acks)
	nextForkRequest(t, peer.byes)
	nextEvent(t, events, "ended")
	if selected.Load() {
		t.Fatal("canceled preparation activated media")
	}
}
