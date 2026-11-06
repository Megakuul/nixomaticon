package sip

import (
	"context"
	"fmt"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type receivedRefer struct {
	request *wire.Request
	tx      wire.ServerTransaction
}

func transferPeer(t *testing.T) (*Client, <-chan Event, <-chan receivedRefer) {
	t.Helper()
	events := make(chan Event, 32)
	refers := make(chan receivedRefer, 4)
	client, err := newClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(e Event) { events <- e }, func(c *Client) {
		c.server.OnRefer(func(req *wire.Request, tx wire.ServerTransaction) { refers <- receivedRefer{req.Clone(), tx} })
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, events, refers
}

func transferNotify(t *testing.T, peer *Client, id, event, state, contentType, body string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := peer.inDialog(ctx, id, wire.NOTIFY, []byte(body), wire.NewHeader("Event", event), wire.NewHeader("Subscription-State", state), wire.NewHeader("Content-Type", contentType))
	if res == nil {
		t.Fatalf("NOTIFY had no response: %v", err)
	}
	return res.StatusCode
}

func noTransferEvent(t *testing.T, events <-chan Event) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if event.Type == "transfer" {
				t.Fatalf("unexpected transfer event: %+v", event)
			}
		default:
			return
		}
	}
}

func TestTransferNotificationRequiresPendingRequestAndValidatedOutcome(t *testing.T) {
	caller, events := newTestClient(t, "alice")
	peer, peerEvents, refers := transferPeer(t)
	id := connectTestCall(t, caller, peer, events, peerEvents)
	if code := transferNotify(t, peer, id, "refer", "terminated", "message/sipfrag", "SIP/2.0 200 OK\r\n"); code != 481 {
		t.Fatalf("unsolicited transfer notification got %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- caller.Transfer(ctx, id, "sip:carol@127.0.0.1") }()
	var refer receivedRefer
	select {
	case refer = <-refers:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := caller.Transfer(ctx, id, "sip:other@127.0.0.1"); err == nil {
		t.Fatal("overlapping REFER accepted")
	}
	event := fmt.Sprintf("refer;id=%d", refer.request.CSeq().SeqNo)
	for _, tc := range []struct {
		event, mime, state, body string
		want                     int
	}{
		{"refer;id=9999999", "message/sipfrag", "terminated", "SIP/2.0 200 OK\r\n", 481},
		{event, "text/plain", "terminated", "SIP/2.0 200 OK\r\n", 415},
		{event, "message/sipfrag", "terminated", "untrusted 200 OK\r\n", 400},
		{event, "message/sipfrag", "active", "SIP/2.0 200 OK\r\n", 400},
	} {
		if got := transferNotify(t, peer, id, tc.event, tc.state, tc.mime, tc.body); got != tc.want {
			t.Fatalf("NOTIFY %q/%q got %d, want %d", tc.event, tc.body, got, tc.want)
		}
	}
	noTransferEvent(t, events)
	if code := transferNotify(t, peer, id, event, "active;expires=120", "message/sipfrag;version=2.0", "SIP/2.0 100 Trying\r\n"); code != 200 {
		t.Fatal("valid transfer progress rejected")
	}
	if progress := nextEvent(t, events, "transfer"); progress.State != "pending" || progress.StatusCode != 100 {
		t.Fatalf("incorrect progress: %+v", progress)
	}
	if code := transferNotify(t, peer, id, event, "terminated;reason=noresource", "message/sipfrag", "SIP/2.0 200 OK\r\n"); code != 200 {
		t.Fatal("early terminal transfer notification rejected")
	}
	noTransferEvent(t, events)
	respond(refer.request, refer.tx, 202, "Accepted")
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if final := nextEvent(t, events, "transfer"); final.State != "success" || final.StatusCode != 200 {
		t.Fatalf("incorrect transfer result: %+v", final)
	}
	if code := transferNotify(t, peer, id, event, "terminated", "message/sipfrag", "SIP/2.0 200 OK\r\n"); code != 200 {
		t.Fatal("completed NOTIFY retransmission was not acknowledged")
	}
	noTransferEvent(t, events)
	caller.Hangup(ctx, id)
}

func TestRejectedReferDiscardsEarlySuccessAndCorrelatesRetry(t *testing.T) {
	caller, events := newTestClient(t, "alice")
	peer, peerEvents, refers := transferPeer(t)
	id := connectTestCall(t, caller, peer, events, peerEvents)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- caller.Transfer(ctx, id, "sip:carol@127.0.0.1") }()
	first := <-refers
	firstEvent := fmt.Sprintf("refer;id=%d", first.request.CSeq().SeqNo)
	if code := transferNotify(t, peer, id, firstEvent, "terminated", "message/sipfrag", "SIP/2.0 200 OK\r\n"); code != 200 {
		t.Fatal("early result was not acknowledged")
	}
	respond(first.request, first.tx, 486, "Busy Here")
	if err := <-result; err == nil {
		t.Fatal("rejected REFER reported success")
	}
	noTransferEvent(t, events)
	go func() { result <- caller.Transfer(ctx, id, "sip:carol@127.0.0.1") }()
	second := <-refers
	respond(second.request, second.tx, 202, "Accepted")
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	for _, oldEvent := range []string{firstEvent, "refer"} {
		if code := transferNotify(t, peer, id, oldEvent, "terminated", "message/sipfrag", "SIP/2.0 200 OK\r\n"); code != 481 {
			t.Fatal("old or ambiguous transfer notification completed a new REFER")
		}
	}
	secondEvent := fmt.Sprintf("refer;id=%d", second.request.CSeq().SeqNo)
	if code := transferNotify(t, peer, id, secondEvent, "terminated", "message/sipfrag", "SIP/2.0 486 Busy Here\r\n"); code != 200 {
		t.Fatal("correlated failure rejected")
	}
	if final := nextEvent(t, events, "transfer"); final.State != "failed" || final.StatusCode != 486 {
		t.Fatalf("failure was not normalized: %+v", final)
	}
	if _, err := caller.getCall(id); err != nil {
		t.Fatal("failed transfer terminated original call")
	}
	caller.Hangup(ctx, id)
}
