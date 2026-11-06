package sip

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/pion/sdp/v3"
)

func TestOutgoingDelayedOffer(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var callbacks atomic.Int32
	answer := strings.Replace(offerSDP, "audio 8000", "audio 9000", 1)
	err := caller.DialDelayedID(ctx, "delayed", peer.client.contact.Address.String(), func(_ context.Context, offer []byte) ([]byte, error) {
		callbacks.Add(1)
		if string(offer) != offerSDP {
			return nil, errors.New("unexpected remote offer")
		}
		return []byte(answer), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	if len(invite.Body()) != 0 || invite.ContentType() != nil || hasToken(invite.GetHeaders("Supported"), "100rel") {
		t.Fatal("delayed INVITE advertised early negotiation or included an offer")
	}
	nextEvent(t, events, "ringing")
	provisional := wire.NewResponseFromRequest(invite, 183, "Session Progress", []byte(offerSDP))
	provisional.SetDestination(invite.Source())
	peer.sendResponse(t, provisional)
	event := nextEvent(t, events, "ringing")
	if len(event.SDP) != 0 || !event.DelayedOffer {
		t.Fatal("unreliable provisional offer exposed as negotiated media")
	}
	response := peer.success(t, invite, "selected", false)
	ack := nextForkRequest(t, peer.acks)
	if string(ack.Body()) != answer || ack.ContentType() == nil {
		t.Fatal("ACK did not carry the selected answer")
	}
	connected := nextEvent(t, events, "connected")
	if !connected.DelayedOffer || string(connected.SDP) != offerSDP {
		t.Fatal("connected did not identify the accepted delayed offer")
	}
	peer.sendResponse(t, response)
	if retransmission := nextForkRequest(t, peer.acks); string(retransmission.Body()) != answer {
		t.Fatal("retransmitted answer changed")
	}
	extra := newForkPeer(t, false)
	extra.success(t, invite, "extra", true)
	assertRejectedOffer(t, nextForkRequest(t, extra.acks).Body())
	if bye := nextForkRequest(t, extra.byes); len(bye.Body()) != 0 || bye.ContentType() != nil {
		t.Fatal("fork BYE retained the SDP answer")
	}
	if callbacks.Load() != 1 {
		t.Fatal("fork or retransmission invoked the answer callback")
	}
	if err := caller.Hangup(ctx, "delayed"); err != nil {
		t.Fatal(err)
	}
}

func TestOutgoingDelayedOfferFailureEndsAnsweredDialog(t *testing.T) {
	for _, failure := range []string{"callback", "empty answer", "missing offer", "wrong type", "cancel callback"} {
		t.Run(failure, func(t *testing.T) {
			peer := newForkPeer(t, false)
			caller, events := newTestClient(t, "alice")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var callbacks atomic.Int32
			err := caller.DialDelayedID(ctx, "failure", peer.client.contact.Address.String(), func(ctx context.Context, _ []byte) ([]byte, error) {
				callbacks.Add(1)
				if failure == "callback" {
					return nil, errors.New("no compatible codec")
				}
				if failure == "cancel callback" {
					cancel()
					return []byte(offerSDP), nil
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			invite := nextForkRequest(t, peer.invites)
			response := wire.NewResponseFromRequest(invite, 200, "OK", []byte(offerSDP))
			response.To().Params.Add("tag", "failed")
			response.AppendHeader(wire.HeaderClone(&peer.client.contact))
			response.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
			if failure == "missing offer" {
				response.SetBody(nil)
			}
			if failure == "wrong type" {
				response.ReplaceHeader(wire.NewHeader("Content-Type", "text/plain"))
			}
			response.SetDestination(invite.Source())
			peer.sendResponse(t, response)
			ack := nextForkRequest(t, peer.acks)
			if failure != "missing offer" {
				assertRejectedOffer(t, ack.Body())
			}
			nextForkRequest(t, peer.byes)
			ended := nextEvent(t, events, "ended")
			if ended.Message == "" {
				t.Fatal("negotiation failure has no explanation")
			}
			if (failure == "missing offer" || failure == "wrong type") && callbacks.Load() != 0 {
				t.Fatal("invalid offer reached the callback")
			}
		})
	}
}

func TestCanceledDelayedOfferRejectsLateAnswer(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var callbacks atomic.Int32
	if err := caller.DialDelayedID(context.Background(), "canceled-delayed", peer.client.contact.Address.String(), func(context.Context, []byte) ([]byte, error) {
		callbacks.Add(1)
		return []byte(offerSDP), nil
	}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	nextEvent(t, events, "ringing")
	if err := caller.Hangup(context.Background(), "canceled-delayed"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "ended")
	peer.success(t, invite, "late", false)
	assertRejectedOffer(t, nextForkRequest(t, peer.acks).Body())
	nextForkRequest(t, peer.byes)
	if callbacks.Load() != 0 {
		t.Fatal("late answer allocated selected-call media")
	}
}

func assertRejectedOffer(t *testing.T, body []byte) {
	t.Helper()
	var answer sdp.SessionDescription
	if err := answer.Unmarshal(body); err != nil || len(answer.MediaDescriptions) != 1 {
		t.Fatalf("invalid rejection answer: %s", body)
	}
	media := answer.MediaDescriptions[0]
	if media.MediaName.Media != "audio" || media.MediaName.Port.Value != 0 {
		t.Fatalf("unwanted media was not rejected: %s", body)
	}
}

func TestDelayedOfferRejectsRequiredReliableProvisional(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	var callbacks atomic.Int32
	if err := caller.DialDelayedID(context.Background(), "required-reliable", peer.client.contact.Address.String(), func(context.Context, []byte) ([]byte, error) {
		callbacks.Add(1)
		return []byte(offerSDP), nil
	}); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	response := wire.NewResponseFromRequest(invite, 183, "Session Progress", []byte(offerSDP))
	response.To().Params.Add("tag", "reliable")
	response.AppendHeader(wire.HeaderClone(&peer.client.contact))
	response.AppendHeader(wire.NewHeader("Require", "100rel"))
	response.AppendHeader(wire.NewHeader("RSeq", "1"))
	response.SetDestination(invite.Source())
	peer.sendResponse(t, response)
	ended := nextEvent(t, events, "ended")
	if !strings.Contains(ended.Message, "reliable provisional") || callbacks.Load() != 0 {
		t.Fatalf("unsupported early negotiation was not canceled: %+v", ended)
	}
}

func TestRejectDelayedOfferPreservesStreamsWithoutPeerSecrets(t *testing.T) {
	offer := strings.Replace(offerSDP, "a=sendrecv\r\n", "a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:secret\r\na=ice-pwd:secret\r\nm=video 9000 RTP/AVP 96 97\r\na=rtpmap:96 VP8/90000\r\n", 1)
	body := rejectDelayedOffer([]byte(offer))
	var answer sdp.SessionDescription
	if err := answer.Unmarshal(body); err != nil || len(answer.MediaDescriptions) != 2 {
		t.Fatalf("invalid multi-stream rejection: %s", body)
	}
	for index, media := range answer.MediaDescriptions {
		if media.MediaName.Port.Value != 0 || media.MediaName.Media != []string{"audio", "video"}[index] {
			t.Fatalf("stream %d was changed or accepted", index)
		}
	}
	if strings.Contains(string(body), "secret") {
		t.Fatal("rejection reflected offered keys or ICE credentials")
	}
	if rejectDelayedOffer([]byte("not SDP")) != nil || rejectDelayedOffer(make([]byte, 65537)) != nil {
		t.Fatal("invalid or oversized SDP accepted")
	}
}
