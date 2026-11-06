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

func earlyUpdate(t *testing.T, peer *forkPeer, invite *wire.Request, provisional *wire.Response, sequence uint32, offer string) *wire.Response {
	t.Helper()
	req := wire.NewRequest(wire.UPDATE, invite.Contact().Address)
	req.AppendHeader(wire.HeaderClone(provisional.To()))
	from := req.To()
	req.RemoveHeader("To")
	req.AppendHeader(&wire.FromHeader{Address: from.Address, Params: from.Params})
	req.AppendHeader(&wire.ToHeader{Address: invite.From().Address, Params: invite.From().Params.Clone()})
	req.AppendHeader(wire.HeaderClone(invite.CallID()))
	req.AppendHeader(&wire.CSeqHeader{SeqNo: sequence, MethodName: wire.UPDATE})
	req.AppendHeader(wire.HeaderClone(&peer.client.contact))
	if offer != "" {
		req.SetBody([]byte(offer))
		req.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := peer.client.client.Do(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestEarlyUpdateReplacesDelayedCandidateAndKeepsLatestOffer(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	prepared := make(chan context.Context, 4)
	var preparations, selected atomic.Int32
	if err := caller.DialDelayedOptionsID(context.Background(), "early-update", peer.client.contact.Address.String(), DelayedOfferOptions{Prepare: func(ctx context.Context, offer []byte) (DelayedAnswer, error) {
		index := preparations.Add(1)
		prepared <- ctx
		if strings.Contains(string(offer), "audio 7000") {
			return DelayedAnswer{}, fmt.Errorf("unsupported media")
		}
		return DelayedAnswer{SDP: append([]byte(nil), offer...), Select: func() error { selected.Store(index); return nil }}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	nextEvent(t, events, "ringing")
	provisional := sendEarlyOffer(t, peer, invite, "selected", 1, offerSDP)
	nextForkRequest(t, peer.pracks)
	nextEvent(t, events, "ringing")
	original := <-prepared
	if response := earlyUpdate(t, peer, invite, provisional, 20, ""); response.StatusCode != 200 || preparations.Load() != 1 {
		t.Fatal("bodyless early UPDATE renegotiated media")
	}
	if response := earlyUpdate(t, peer, invite, provisional, 21, strings.Replace(offerSDP, "audio 8000", "audio 7000", 1)); response.StatusCode != 488 {
		t.Fatalf("rejected replacement returned %d", response.StatusCode)
	}
	rejected := <-prepared
	if rejected.Err() == nil || original.Err() != nil {
		t.Fatal("rejected replacement changed original media lifetime")
	}
	updatedOffer := strings.Replace(offerSDP, "audio 8000", "audio 9000", 1)
	if response := earlyUpdate(t, peer, invite, provisional, 22, updatedOffer); response.StatusCode != 200 || string(response.Body()) != updatedOffer {
		t.Fatalf("early UPDATE not answered: %s", response)
	}
	replacement := <-prepared
	if original.Err() == nil || replacement.Err() != nil || selected.Load() != 0 {
		t.Fatal("early UPDATE did not replace only the prepared candidate")
	}
	if response := earlyUpdate(t, peer, invite, provisional, 21, ""); response.StatusCode != 500 {
		t.Fatal("early UPDATE accepted an old remote sequence")
	}
	peer.success(t, invite, "selected", false)
	if ack := nextForkRequest(t, peer.acks); len(ack.Body()) != 0 {
		t.Fatal("final ACK repeated negotiated answer")
	}
	if connected := nextEvent(t, events, "connected"); string(connected.SDP) != updatedOffer || selected.Load() != 3 {
		t.Fatal("final original SDP rolled back the early UPDATE")
	}
	caller.Hangup(context.Background(), "early-update")
	if replacement.Err() == nil {
		t.Fatal("final selected media survived hangup")
	}
}

func TestEarlyUpdateUsesEarlyHandlerAndPreservesFinalNegotiation(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var earlyCalls, establishedCalls atomic.Int32
	caller.SetEarlyOfferHandler(func(_ string, offer []byte) ([]byte, error) {
		earlyCalls.Add(1)
		return append([]byte(nil), offer...), nil
	})
	caller.SetOfferHandler(func(string, []byte) ([]byte, error) {
		establishedCalls.Add(1)
		return []byte(offerSDP), nil
	})
	id, err := caller.Dial(context.Background(), peer.client.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	nextEvent(t, events, "ringing")
	provisional := sendEarlyOffer(t, peer, invite, "selected", 1, offerSDP)
	nextForkRequest(t, peer.pracks)
	nextEvent(t, events, "ringing")
	updated := strings.Replace(offerSDP, "audio 8000", "audio 9000", 1)
	if response := earlyUpdate(t, peer, invite, provisional, 50, updated); response.StatusCode != 200 {
		t.Fatalf("early UPDATE failed: %s", response)
	}
	peer.success(t, invite, "selected", false)
	nextForkRequest(t, peer.acks)
	connected := nextEvent(t, events, "connected")
	if earlyCalls.Load() != 1 || establishedCalls.Load() != 0 || string(connected.SDP) != updated || len(connected.AnswerTo) != 0 {
		t.Fatal("final response lost early negotiation or used established handler")
	}
	if response := earlyUpdate(t, peer, invite, provisional, 49, updated); response.StatusCode != 500 {
		t.Fatal("confirmed dialog lost early remote CSeq")
	}
	caller.Hangup(context.Background(), id)
}

func TestIncomingBodylessEarlyUpdatePreservesInitialACK(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	id, err := caller.Dial(context.Background(), receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, receiverEvents, "incoming")
	nextEvent(t, callerEvents, "ringing")
	cl, err := receiver.getCall(id)
	if err != nil {
		t.Fatal(err)
	}
	cl.mu.Lock()
	invite := cl.incoming.InviteRequest.Clone()
	cl.mu.Unlock()
	req := wire.NewRequest(wire.UPDATE, receiver.contact.Address)
	req.AppendHeader(wire.HeaderClone(invite.From()))
	req.AppendHeader(wire.HeaderClone(invite.To()))
	req.AppendHeader(wire.HeaderClone(invite.CallID()))
	req.AppendHeader(&wire.CSeqHeader{SeqNo: invite.CSeq().SeqNo + 1, MethodName: wire.UPDATE})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := caller.client.Do(ctx, req)
	if err != nil || response.StatusCode != 200 || len(response.Body()) != 0 {
		t.Fatalf("bodyless early UPDATE failed: %v, %v", response, err)
	}
	if err := receiver.Answer(ctx, id, []byte(offerSDP)); err != nil {
		t.Fatalf("early UPDATE invalidated initial ACK sequence: %v", err)
	}
	nextEvent(t, callerEvents, "connected")
	nextEvent(t, receiverEvents, "connected")
	caller.Hangup(context.Background(), id)
}

func TestInitialOfferReliableAnswerMayBeAbsentFromFinalResponse(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	id, err := caller.Dial(context.Background(), peer.client.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	nextEvent(t, events, "ringing")
	provisional := sendEarlyOffer(t, peer, invite, "selected", 1, offerSDP)
	nextForkRequest(t, peer.pracks)
	nextEvent(t, events, "ringing")
	final := provisional.Clone()
	final.StatusCode, final.Reason = 200, "OK"
	final.RemoveHeader("Require")
	final.RemoveHeader("RSeq")
	final.RemoveHeader("Content-Type")
	final.SetBody(nil)
	peer.sendResponse(t, final)
	nextForkRequest(t, peer.acks)
	connected := nextEvent(t, events, "connected")
	if string(connected.SDP) != offerSDP || string(connected.AnswerTo) != offerSDP {
		t.Fatal("bodyless final response lost the reliable answer or its original offer")
	}
	caller.Hangup(context.Background(), id)
}

func TestEarlyUpdateLocalAnswerSurvivesSessionRefresh(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	localAnswer := strings.Replace(offerSDP, "audio 8000", "audio 10000", 1)
	caller.SetEarlyOfferHandler(func(string, []byte) ([]byte, error) { return []byte(localAnswer), nil })
	id, err := caller.Dial(context.Background(), peer.client.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	nextEvent(t, events, "ringing")
	provisional := sendEarlyOffer(t, peer, invite, "selected", 1, offerSDP)
	nextForkRequest(t, peer.pracks)
	nextEvent(t, events, "ringing")
	remoteOffer := strings.Replace(offerSDP, "audio 8000", "audio 9000", 1)
	if response := earlyUpdate(t, peer, invite, provisional, 10, remoteOffer); response.StatusCode != 200 || string(response.Body()) != localAnswer {
		t.Fatal("early UPDATE did not accept new local media")
	}
	final := provisional.Clone()
	final.StatusCode, final.Reason = 200, "OK"
	final.RemoveHeader("Require")
	final.RemoveHeader("RSeq")
	final.AppendHeader(wire.NewHeader("Session-Expires", "1800;refresher=uac"))
	peer.sendResponse(t, final)
	nextForkRequest(t, peer.acks)
	nextEvent(t, events, "connected")
	cl, _ := caller.getCall(id)
	cl.mu.Lock()
	cl.session.deadline = time.Now().Add(cl.session.interval/2 + 10*time.Millisecond)
	wake := cl.session.wake
	cl.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
	refresh := nextForkRequest(t, peer.invites)
	if string(refresh.Body()) != localAnswer {
		t.Fatal("session refresh sent the obsolete initial offer after early UPDATE")
	}
	peer.success(t, refresh, "selected", false)
	nextForkRequest(t, peer.acks)
	if err := caller.Hangup(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
