package sip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

func connectTestCall(t *testing.T, caller, receiver *Client, callerEvents, receiverEvents <-chan Event) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The account, rather than this setup deadline, owns the established call.
	id, err := caller.Dial(context.Background(), receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, receiverEvents, "incoming")
	if err := receiver.Answer(ctx, id, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, callerEvents, "connected")
	nextEvent(t, receiverEvents, "connected")
	return id
}

func TestSessionIntervalRetryAndRefresh(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	events := make(chan Event, 32)
	updates := make(chan *wire.Request, 4)
	receiver, err := newClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0", MinSessionExpires: time.Hour, SessionExpires: time.Hour}, func(e Event) { events <- e }, func(receiver *Client) {
		receiver.server.OnUpdate(func(req *wire.Request, tx wire.ServerTransaction) { updates <- req.Clone(); receiver.onUpdate(req, tx) })
	})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	id := connectTestCall(t, caller, receiver, callerEvents, events)
	cl, _ := caller.getCall(id)
	cl.mu.Lock()
	if cl.session.interval != time.Hour || !cl.session.localRefresher || !cl.session.update {
		t.Errorf("negotiated timer: %+v", cl.session)
	}
	cl.session.deadline = time.Now().Add(cl.session.interval/2 + 10*time.Millisecond)
	cl.mu.Unlock()
	select {
	case cl.session.wake <- struct{}{}:
	default:
	}
	select {
	case req := <-updates:
		if len(req.Body()) != 0 {
			t.Fatal("UPDATE refresh unexpectedly changed media")
		}
		if req.GetHeader("Session-Expires").Value() != "3600;refresher=uac" {
			t.Fatalf("refresh changed granted interval: %s", req)
		}
	case <-time.After(time.Second):
		t.Fatal("local refresher did not refresh")
	}
}

// The initial INVITE offer of an outgoing call lists every codec. A refresh must
// offer the negotiated session instead, or the answer can change the codec.
func TestReinviteRefreshOffersCurrentSession(t *testing.T) {
	const currentSDP = "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=call\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 8000 RTP/AVP 8\r\na=sendrecv\r\n"
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	offers := make(chan string, 1)
	caller.SetOfferHandler(func(id string, offer []byte) ([]byte, error) {
		if len(offer) != 0 {
			return nil, errors.New("unexpected remote offer")
		}
		return []byte(currentSDP), nil
	})
	receiver.SetOfferHandler(func(id string, offer []byte) ([]byte, error) {
		offers <- string(offer)
		return offer, nil
	})
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	cl, _ := caller.getCall(id)
	cl.mu.Lock()
	cl.session.update = false
	cl.session.deadline = time.Now().Add(cl.session.interval/2 + 10*time.Millisecond)
	cl.mu.Unlock()
	select {
	case cl.session.wake <- struct{}{}:
	default:
	}
	select {
	case offer := <-offers:
		if offer != currentSDP {
			t.Fatalf("refresh re-INVITE offered %q", offer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local refresher did not send a re-INVITE")
	}
}

func TestRemoteSessionExpiry(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	cl, _ := receiver.getCall(id)
	cl.mu.Lock()
	cl.session.deadline = time.Now()
	cl.mu.Unlock()
	select {
	case cl.session.wake <- struct{}{}:
	default:
	}
	ended := nextEvent(t, receiverEvents, "ended")
	if !strings.Contains(ended.Message, "session refresh timed out") {
		t.Fatalf("unexpected termination: %+v", ended)
	}
	nextEvent(t, callerEvents, "ended")
}

func TestUpdateAuthenticationAndIntervalRetry(t *testing.T) {
	events := make(chan Event, 32)
	var attempts atomic.Int32
	verified := make(chan bool, 1)
	receiverEvents := make(chan Event, 32)
	receiver, err := newClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(e Event) { receiverEvents <- e }, func(receiver *Client) {
		receiver.server.OnUpdate(func(req *wire.Request, tx wire.ServerTransaction) {
			attempt := attempts.Add(1)
			if attempt == 1 {
				res := wire.NewResponseFromRequest(req, 422, "Session Interval Too Small", nil)
				res.AppendHeader(wire.NewHeader("Min-SE", "7200"))
				_ = tx.Respond(res)
				return
			}
			if req.GetHeader("Authorization") == nil {
				res := wire.NewResponseFromRequest(req, 401, "Unauthorized", nil)
				res.AppendHeader(wire.NewHeader("WWW-Authenticate", `Digest realm="loopback", nonce="update-nonce", algorithm=SHA-256, qop="auth"`))
				_ = tx.Respond(res)
				return
			}
			credentials, err := digest.ParseCredentials(req.GetHeader("Authorization").Value())
			valid := false
			if err == nil {
				expected, err := digest.Digest(&digest.Challenge{Realm: "loopback", Nonce: "update-nonce", Algorithm: "SHA-256", QOP: []string{"auth"}}, digest.Options{Method: "UPDATE", URI: req.Recipient.Addr(), Username: "auth-alice", Password: "secret", Cnonce: credentials.Cnonce, Count: credentials.Nc})
				valid = err == nil && credentials.Response == expected.Response && credentials.Username == "auth-alice"
			}
			verified <- valid
			if !valid {
				respond(req, tx, 403, "Bad Credentials")
				return
			}
			receiver.onUpdate(req, tx)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	caller, err := NewClient(Config{Server: "127.0.0.1", Port: receiver.contact.Address.Port, Username: "alice", AuthUsername: "auth-alice", Password: "secret", LocalAddress: "127.0.0.1:0"}, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	id := connectTestCall(t, caller, receiver, events, receiverEvents)
	if err := caller.Update(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	if !<-verified {
		t.Fatal("digest validation failed")
	}
	if attempts.Load() != 3 {
		t.Fatalf("unexpected attempt count: %d", attempts.Load())
	}
	cl, _ := caller.getCall(id)
	cl.mu.Lock()
	interval := cl.session.interval
	cl.mu.Unlock()
	if interval != 2*time.Hour {
		t.Fatalf("retry interval=%v", interval)
	}
}

func TestGlareRetryCancellation(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	challenged := make(chan struct{}, 1)
	receiverEvents := make(chan Event, 32)
	receiver, err := newClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(e Event) { receiverEvents <- e }, func(receiver *Client) {
		receiver.server.OnUpdate(func(req *wire.Request, tx wire.ServerTransaction) {
			respond(req, tx, 491, "Request Pending")
			challenged <- struct{}{}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.Update(ctx, id, nil) }()
	<-challenged
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("glare backoff ignored cancellation")
	}
}

func TestSessionHeaderValidation(t *testing.T) {
	for _, value := range []string{"0", "-1", "86401", "abc", "90;refresher=other", "90;refresher=uac;refresher=uas"} {
		if _, _, err := parseSession(wire.NewHeader("Session-Expires", value)); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, role := range []string{"uac", "uas"} {
		duration, refresher, err := parseSession(wire.NewHeader("Session-Expires", fmt.Sprintf("90;refresher=%s", role)))
		if err != nil || duration != 90*time.Second || refresher != role {
			t.Fatalf("failed valid session header: %v", err)
		}
	}
}
