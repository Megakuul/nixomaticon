package sip

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

const offerSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=call\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 8000 RTP/AVP 0\r\na=sendrecv\r\n"

func newTestClient(t *testing.T, name string) (*Client, chan Event) {
	t.Helper()
	events := make(chan Event, 32)
	client, err := NewClient(Config{Server: "127.0.0.1", Username: name, LocalAddress: "127.0.0.1:0"}, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, events
}

func nextEvent(t *testing.T, events <-chan Event, kind string) Event {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e := <-events:
			if e.Type == kind {
				return e
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", kind)
			return Event{}
		}
	}
}

func TestLoopbackCall(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) { return offer, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := caller.Dial(ctx, receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	incoming := nextEvent(t, receiverEvents, "incoming")
	if incoming.CallID != id || string(incoming.SDP) != offerSDP {
		t.Fatalf("unexpected incoming call: %+v", incoming)
	}
	if err := receiver.Answer(ctx, id, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	connected := nextEvent(t, callerEvents, "connected")
	if string(connected.SDP) != offerSDP {
		t.Fatal("SDP answer not delivered")
	}
	nextEvent(t, receiverEvents, "connected")
	if err := caller.SendDTMF(ctx, id, "5"); err != nil {
		t.Fatal(err)
	}
	if e := nextEvent(t, receiverEvents, "dtmf"); !strings.Contains(string(e.Body), "Signal=5") {
		t.Fatal("DTMF digit not delivered")
	}
	if err := caller.Reinvite(ctx, id, []byte(strings.ReplaceAll(offerSDP, "sendrecv", "sendonly"))); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, callerEvents, "updated")
	if err := caller.Hangup(ctx, id); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, receiverEvents, "ended")
	nextEvent(t, callerEvents, "ended")
	if _, err := caller.getCall(id); err == nil {
		t.Fatal("ended call retained")
	}
}

func TestLoopbackCancel(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := caller.Dial(ctx, receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, receiverEvents, "incoming")
	nextEvent(t, callerEvents, "ringing")
	if err := caller.Hangup(ctx, id); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, receiverEvents, "ended")
	nextEvent(t, callerEvents, "ended")
}

func TestLoopbackMessage(t *testing.T) {
	caller, _ := newTestClient(t, "alice")
	receiver, events := newTestClient(t, "bob")
	if err := caller.SendMessage(context.Background(), receiver.contact.Address.String(), "Hello ☎"); err != nil {
		t.Fatal(err)
	}
	event := nextEvent(t, events, "message")
	if event.Message != "Hello ☎" || !strings.Contains(event.RemoteURI, "alice") {
		t.Fatalf("unexpected message: %+v", event)
	}
}

func TestRegistrationRefreshAndCancellation(t *testing.T) {
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	registrations := make(chan *wire.Request, 10)
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		registrations <- req.Clone()
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "1"))
		res.AppendHeader(wire.NewHeader("Server", "Loopback PBX"))
		tx.Respond(res)
	})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ready := make(chan struct{})
	go server.ServeUDP(&readyPacketConn{PacketConn: conn, ready: ready})
	<-ready
	port := conn.LocalAddr().(*net.UDPAddr).Port
	events := make(chan Event, 20)
	client, err := NewClient(Config{Server: "127.0.0.1", Port: port, Username: "alice", LocalAddress: "127.0.0.1:0", Expires: time.Second}, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	var first, second *wire.Request
	select {
	case first = <-registrations:
	case <-time.After(3 * time.Second):
		t.Fatal("no initial registration")
	}
	select {
	case second = <-registrations:
	case <-time.After(3 * time.Second):
		t.Fatal("no registration refresh")
	}
	if *first.CallID() != *second.CallID() || second.CSeq().SeqNo <= first.CSeq().SeqNo {
		t.Fatal("refresh must retain Call-ID and increase CSeq")
	}
	cancel()
	select {
	case <-client.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("registration context did not close client")
	}
	done := make(chan struct{})
	go func() { client.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked")
	}
}

func TestInvalidInput(t *testing.T) {
	client, _ := newTestClient(t, "alice")
	for _, target := range []string{"", "sip:a@host\r\nInjected: true", "https://example.com"} {
		if _, err := client.recipient(target); err == nil {
			t.Errorf("accepted target %q", target)
		}
	}
	if err := client.SendDTMF(context.Background(), "missing", "55"); err == nil {
		t.Fatal("accepted invalid DTMF")
	}
	if err := client.SendMessage(context.Background(), "bob", strings.Repeat("x", 65537)); err == nil {
		t.Fatal("accepted oversized message")
	}
	if _, err := NewClient(Config{Server: "127.0.0.1", Username: "alice", Transport: "invalid"}, nil); err == nil {
		t.Fatal("accepted invalid transport")
	}
}

func TestTCPCallAndReject(t *testing.T) {
	events := make(chan Event, 20)
	receiver, err := NewClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0", Transport: "tcp"}, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	callerEvents := make(chan Event, 20)
	caller, err := NewClient(Config{Server: "127.0.0.1", Username: "alice", LocalAddress: "127.0.0.1:0", Transport: "tcp"}, func(e Event) { callerEvents <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := caller.Dial(ctx, receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "incoming")
	if err := receiver.Reject(ctx, id); err != nil {
		t.Fatal(err)
	}
	ended := nextEvent(t, callerEvents, "ended")
	if !strings.Contains(ended.Message, "486") {
		t.Fatalf("unexpected rejection: %+v", ended)
	}
}

func TestClosePendingCall(t *testing.T) {
	receiver, events := newTestClient(t, "bob")
	caller, _ := newTestClient(t, "alice")
	if _, err := caller.Dial(context.Background(), receiver.contact.Address.String(), []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "incoming")
	done := make(chan struct{})
	go func() { caller.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on pending INVITE")
	}
}

func TestDigestRegistration(t *testing.T) {
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	authenticated := make(chan bool, 1)
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		if req.GetHeader("Authorization") == nil {
			res := wire.NewResponseFromRequest(req, 401, "Unauthorized", nil)
			res.AppendHeader(wire.NewHeader("WWW-Authenticate", `Digest realm="loopback", nonce="test-nonce", algorithm=SHA-256, qop="auth"`))
			tx.Respond(res)
			return
		}
		credentials, err := digest.ParseCredentials(req.GetHeader("Authorization").Value())
		if err != nil {
			respond(req, tx, 403, "Bad Credentials")
			return
		}
		challenge := &digest.Challenge{Realm: "loopback", Nonce: "test-nonce", Algorithm: "SHA-256", QOP: []string{"auth"}}
		expected, err := digest.Digest(challenge, digest.Options{Method: "REGISTER", URI: req.Recipient.String(), Username: "auth-alice", Password: "secret", Cnonce: credentials.Cnonce, Count: credentials.Nc})
		valid := err == nil && credentials.Response == expected.Response && credentials.Username == "auth-alice"
		authenticated <- valid
		if !valid {
			respond(req, tx, 403, "Bad Credentials")
			return
		}
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "60"))
		tx.Respond(res)
	})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ready := make(chan struct{})
	go server.ServeUDP(&readyPacketConn{PacketConn: conn, ready: ready})
	<-ready
	client, err := NewClient(Config{Server: "127.0.0.1", Port: conn.LocalAddr().(*net.UDPAddr).Port, Username: "alice", AuthUsername: "auth-alice", Password: "secret", LocalAddress: "127.0.0.1:0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if !<-authenticated {
		t.Fatal("digest response did not verify")
	}
}

func TestRedirectRingingCall(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, events := newTestClient(t, "bob")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := caller.Dial(ctx, receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "incoming")
	if err := receiver.Redirect(ctx, id, "sip:reception@example.org"); err != nil {
		t.Fatal(err)
	}
	ended := nextEvent(t, callerEvents, "ended")
	if !strings.Contains(ended.Message, "302") {
		t.Fatalf("expected redirect response, got %+v", ended)
	}
}

func TestRejectUnsupportedRequiredExtension(t *testing.T) {
	caller, _ := newTestClient(t, "alice")
	receiver, _ := newTestClient(t, "bob")
	req := caller.request(wire.INVITE, receiver.contact.Address, []byte(offerSDP))
	req.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	req.AppendHeader(wire.NewHeader("Require", "unsupported-test-extension"))
	response, err := caller.do(context.Background(), req)
	if err == nil || response == nil || response.StatusCode != 420 || response.GetHeader("Unsupported") == nil {
		t.Fatalf("required extension was not rejected: response=%v error=%v", response, err)
	}
}

func TestFailedRefreshRetainsRequestedExpiry(t *testing.T) {
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 8)
	var count atomic.Int32
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.GetHeader("Expires").Value()
		if count.Add(1) == 2 {
			respond(req, tx, 503, "Temporarily Unavailable")
			return
		}
		response := wire.NewResponseFromRequest(req, 200, "OK", nil)
		response.AppendHeader(wire.NewHeader("Expires", "1"))
		tx.Respond(response)
	})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ready := make(chan struct{})
	go server.ServeUDP(&readyPacketConn{PacketConn: conn, ready: ready})
	<-ready
	client, err := NewClient(Config{Server: "127.0.0.1", Port: conn.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0", Expires: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"60", "1", "1"} {
		select {
		case actual := <-requests:
			if actual != expected {
				t.Fatalf("requested expiry=%s, want %s", actual, expected)
			}
		case <-ctx.Done():
			t.Fatal("refresh/retry did not arrive")
		}
	}
}

func TestLocalAddressCurrentFollowsRoute(t *testing.T) {
	client, err := NewClient(Config{Server: "127.0.0.1", Username: "alice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if !client.LocalAddressCurrent() {
		t.Fatal("fresh client reports a stale local address")
	}
	// The address a network change left behind.
	client.mu.Lock()
	client.contact.Address.Host = "192.0.2.1"
	client.mu.Unlock()
	if client.LocalAddressCurrent() {
		t.Fatal("client bound to a former address reports it as current")
	}
}
