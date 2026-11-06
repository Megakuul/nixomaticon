package sip

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type subscriptionExchange struct {
	request  *wire.Request
	response *wire.Response
}

type subscriptionPeer struct {
	client      *Client
	requests    chan subscriptionExchange
	failNext    atomic.Int32
	holdNext    atomic.Bool
	earlyNotify atomic.Bool
}

func newSubscriptionPeer(t *testing.T) *subscriptionPeer {
	t.Helper()
	peer := &subscriptionPeer{requests: make(chan subscriptionExchange, 16)}
	client, err := newClient(Config{Server: "127.0.0.1", Username: "notifier", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
		c.server.OnSubscribe(func(req *wire.Request, tx wire.ServerTransaction) {
			status := int(peer.failNext.Swap(0))
			if status == 0 {
				status = 200
			}
			response := wire.NewResponseFromRequest(req, status, "Subscription", nil)
			response.To().Params.Add("tag", "notifier-"+string(*req.CallID()))
			response.AppendHeader(wire.HeaderClone(&c.contact))
			response.AppendHeader(wire.NewHeader("Expires", req.GetHeader("Expires").Value()))
			if status == 503 {
				response.AppendHeader(wire.NewHeader("Retry-After", "1"))
			}
			if peer.earlyNotify.Swap(false) {
				if status := peer.notify(t, subscriptionExchange{req, response}, 1, "active"); status != 200 {
					t.Errorf("early NOTIFY rejected: %d", status)
				}
			}
			if !peer.holdNext.Swap(false) {
				_ = tx.Respond(response)
			}
			peer.requests <- subscriptionExchange{req.Clone(), response.Clone()}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	peer.client = client
	t.Cleanup(func() { client.Close() })
	return peer
}

func (p *subscriptionPeer) next(t *testing.T) subscriptionExchange {
	t.Helper()
	select {
	case request := <-p.requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("subscription request did not arrive")
		return subscriptionExchange{}
	}
}

func (p *subscriptionPeer) notify(t *testing.T, exchange subscriptionExchange, sequence uint32, state string) int {
	t.Helper()
	request := wire.NewRequest(wire.NOTIFY, exchange.request.Contact().Address)
	from := exchange.response.To().AsFrom()
	to := exchange.request.From().AsTo()
	request.AppendHeader(&from)
	request.AppendHeader(&to)
	request.AppendHeader(wire.HeaderClone(exchange.request.CallID()))
	request.AppendHeader(wire.HeaderClone(&p.client.contact))
	request.AppendHeader(&wire.CSeqHeader{SeqNo: sequence, MethodName: wire.NOTIFY})
	request.AppendHeader(wire.NewHeader("Event", "presence"))
	request.AppendHeader(wire.NewHeader("Subscription-State", state))
	request.AppendHeader(wire.NewHeader("Content-Type", "application/pidf+xml"))
	request.SetBody([]byte("<presence/>"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := p.client.client.Do(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode
}

func subscribeTestPeer(t *testing.T, peer *subscriptionPeer) (*Client, chan Event, string, subscriptionExchange) {
	t.Helper()
	client, events := newTestClient(t, "alice")
	id, err := client.Subscribe(context.Background(), peer.client.contact.Address.String(), "presence", "application/pidf+xml")
	if err != nil {
		t.Fatal(err)
	}
	return client, events, id, peer.next(t)
}

func TestSubscriptionRecoveryKeepsLogicalIDAndRejectsOldDialog(t *testing.T) {
	peer := newSubscriptionPeer(t)
	client, events, id, initial := subscribeTestPeer(t, peer)
	if status := peer.notify(t, initial, 1, "active;expires=3600"); status != 200 {
		t.Fatal(status)
	}
	nextEvent(t, events, "presence")
	if status := peer.notify(t, initial, 2, "terminated;reason=Probation;retry-after=1"); status != 200 {
		t.Fatal(status)
	}
	if err := client.RefreshSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-peer.requests:
		t.Fatal("resume bypassed probation Retry-After")
	case <-time.After(200 * time.Millisecond):
	}
	recovered := peer.next(t)
	if *initial.request.CallID() == *recovered.request.CallID() || initial.request.From().Params.ToString(';') == recovered.request.From().Params.ToString(';') {
		t.Fatal("recovery reused old dialog identifiers")
	}
	if status := peer.notify(t, initial, 3, "active;expires=3600"); status != 481 {
		t.Fatalf("obsolete dialog notification accepted: %d", status)
	}
	if status := peer.notify(t, recovered, 1, "active;expires=3600"); status != 200 {
		t.Fatal(status)
	}
	for {
		event := nextEvent(t, events, "presence")
		if event.CallID != id {
			t.Fatalf("logical watch identity changed: %+v", event)
		}
		if event.State == "unknown" {
			break
		}
	}
	if event := nextEvent(t, events, "presence"); event.State != "presence" || event.CallID != id {
		t.Fatalf("recovered notification: %+v", event)
	}
	if err := client.Unsubscribe(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	removed := peer.next(t)
	if *removed.request.CallID() != *recovered.request.CallID() || removed.request.GetHeader("Expires").Value() != "0" {
		t.Fatal("unsubscribe did not use recovered wire dialog")
	}
}

func TestSubscriptionPermanentTerminationDoesNotRetry(t *testing.T) {
	for _, reason := range []string{"rejected", "noresource", "giveup", "invariant"} {
		t.Run(reason, func(t *testing.T) {
			peer := newSubscriptionPeer(t)
			client, _, id, initial := subscribeTestPeer(t, peer)
			if status := peer.notify(t, initial, 1, "terminated;reason="+reason); status != 200 {
				t.Fatal(status)
			}
			if err := client.RefreshSubscriptions(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-peer.requests:
				t.Fatal("server's terminal policy was retried")
			case <-time.After(50 * time.Millisecond):
			}
			if err := client.Unsubscribe(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSubscriptionRefreshRecovery(t *testing.T) {
	for _, status := range []int{408, 481, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			peer := newSubscriptionPeer(t)
			client, events, _, initial := subscribeTestPeer(t, peer)
			peer.notify(t, initial, 1, "active;expires=3600")
			nextEvent(t, events, "presence")
			peer.failNext.Store(int32(status))
			if err := client.RefreshSubscriptions(context.Background()); err != nil {
				t.Fatal(err)
			}
			failed := peer.next(t)
			if *failed.request.CallID() != *initial.request.CallID() {
				t.Fatal("ordinary resume unnecessarily replaced live dialog")
			}
			nextEvent(t, events, "presence")
			client.RefreshSubscriptions(context.Background())
			if status == 503 {
				select {
				case <-peer.requests:
					t.Fatal("resume bypassed server Retry-After")
				case <-time.After(100 * time.Millisecond):
				}
			}
			recovered := peer.next(t)
			if fresh := *initial.request.CallID() != *recovered.request.CallID(); fresh != (status != 503) {
				t.Fatalf("wrong dialog recovery for %d: fresh=%v", status, fresh)
			}
			if status := peer.notify(t, recovered, 2, "active;expires=3600"); status != 200 {
				t.Fatal(status)
			}
		})
	}
}

func TestMissingSubscriptionNOTIFYRestartsDialog(t *testing.T) {
	peer := newSubscriptionPeer(t)
	client, events, id, initial := subscribeTestPeer(t, peer)
	client.mu.Lock()
	sub := client.subscriptions[id]
	client.mu.Unlock()
	sub.mu.Lock()
	sub.notifyBy = time.Now().Add(20 * time.Millisecond)
	sub.mu.Unlock()
	sub.signal()
	if event := nextEvent(t, events, "presence"); event.State != "unknown" {
		t.Fatalf("unconfirmed state remained valid: %+v", event)
	}
	recovered := peer.next(t)
	if *initial.request.CallID() == *recovered.request.CallID() {
		t.Fatal("missing NOTIFY did not cause new dialog")
	}
}

func TestUnsubscribeCancelsInFlightRefresh(t *testing.T) {
	peer := newSubscriptionPeer(t)
	client, _, id, initial := subscribeTestPeer(t, peer)
	peer.notify(t, initial, 1, "active;expires=3600")
	peer.holdNext.Store(true)
	client.RefreshSubscriptions(context.Background())
	peer.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := client.Unsubscribe(ctx, id); err != nil {
		t.Fatalf("unsubscribe blocked behind canceled refresh: %v", err)
	}
	if request := peer.next(t); request.request.GetHeader("Expires").Value() != "0" {
		t.Fatal("subscription removal was not sent")
	}
}

func TestNotifyShortensSubscriptionLease(t *testing.T) {
	peer := newSubscriptionPeer(t)
	_, _, _, initial := subscribeTestPeer(t, peer)
	if status := peer.notify(t, initial, 1, "active;expires=1"); status != 200 {
		t.Fatal(status)
	}
	refresh := peer.next(t)
	if *refresh.request.CallID() != *initial.request.CallID() {
		t.Fatal("shortened lease did not refresh existing dialog")
	}
}

func TestEarlyNOTIFYBindsDialogBeforeSubscribeResponse(t *testing.T) {
	peer := newSubscriptionPeer(t)
	peer.earlyNotify.Store(true)
	client, events, id, initial := subscribeTestPeer(t, peer)
	if event := nextEvent(t, events, "presence"); event.CallID != id {
		t.Fatalf("early NOTIFY lost logical identity: %+v", event)
	}
	foreign := subscriptionExchange{initial.request, initial.response.Clone()}
	foreign.response.To().Params.Add("tag", "different-notifier")
	if status := peer.notify(t, foreign, 2, "active;expires=3600"); status != 481 {
		t.Fatalf("unrelated notifier accepted: %d", status)
	}
	client.mu.Lock()
	sub := client.subscriptions[id]
	client.mu.Unlock()
	sub.mu.Lock()
	expires, next := sub.expiresAt, sub.refreshAt
	sub.mu.Unlock()
	if expires.IsZero() || time.Until(next) < time.Minute {
		t.Fatal("early NOTIFY without expires discarded response lease")
	}
}

func TestSubscriptionExpiryHonorsExistingRetryAfter(t *testing.T) {
	peer := newSubscriptionPeer(t)
	client, events, id, initial := subscribeTestPeer(t, peer)
	client.mu.Lock()
	sub := client.subscriptions[id]
	client.mu.Unlock()
	sub.mu.Lock()
	sub.notifyBy = time.Now().Add(10 * time.Millisecond)
	sub.retryAfter = time.Now().Add(400 * time.Millisecond)
	sub.backoff = 10 * time.Millisecond
	sub.mu.Unlock()
	sub.signal()
	if event := nextEvent(t, events, "presence"); event.State != "unknown" {
		t.Fatal("missing NOTIFY did not invalidate presence")
	}
	select {
	case <-peer.requests:
		t.Fatal("Timer N bypassed outstanding Retry-After")
	case <-time.After(100 * time.Millisecond):
	}
	if recovered := peer.next(t); *recovered.request.CallID() == *initial.request.CallID() {
		t.Fatal("expired dialog was reused")
	}
}
