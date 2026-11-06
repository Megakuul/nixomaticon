package sip

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"golang.org/x/net/dns/dnsmessage"
)

func failoverDNS(t *testing.T, records []*net.SRV, addresses map[string][][4]byte) (*net.Resolver, <-chan dnsmessage.Question) {
	t.Helper()
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	queries := make(chan dnsmessage.Question, 128)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		for {
			n, peer, err := socket.ReadFrom(buffer)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buffer[:n]) != nil || len(request.Questions) != 1 {
				continue
			}
			question := request.Questions[0]
			select {
			case queries <- question:
			default:
			}
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true}, Questions: request.Questions}
			header := dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: 30}
			switch question.Type {
			case dnsmessage.TypeSRV:
				for _, record := range records {
					target, err := dnsmessage.NewName(record.Target)
					if err == nil {
						response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.SRVResource{Priority: record.Priority, Weight: record.Weight, Port: record.Port, Target: target}})
					}
				}
			case dnsmessage.TypeA:
				for _, address := range addresses[question.Name.String()] {
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AResource{A: address}})
				}
			}
			if len(response.Answers) == 0 {
				response.Header.RCode = dnsmessage.RCodeNameError
			}
			body, err := response.Pack()
			if err == nil {
				_, _ = socket.WriteTo(body, peer)
			}
		}
	}()
	t.Cleanup(func() { socket.Close(); <-done })
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", socket.LocalAddr().String())
	}}
	return resolver, queries
}

func TestRegistrationSRVFailoverAndSelectedRouting(t *testing.T) {
	var primaryRequests atomic.Int32
	primary, err := newClient(Config{Server: "127.0.0.1", Username: "primary", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
		c.server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
			primaryRequests.Add(1)
			respond(req, tx, 503, "Unavailable")
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	requests := make(chan *wire.Request, 16)
	backup, err := newClient(Config{Server: "127.0.0.1", Username: "backup", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
		handle := func(req *wire.Request, tx wire.ServerTransaction) {
			if req.GetHeader("Authorization") == nil {
				response := wire.NewResponseFromRequest(req, 401, "Unauthorized", nil)
				response.AppendHeader(wire.NewHeader("WWW-Authenticate", `Digest realm="office", nonce="backup", algorithm=SHA-256, qop="auth"`))
				_ = tx.Respond(response)
				return
			}
			requests <- req.Clone()
			response := wire.NewResponseFromRequest(req, 200, "OK", nil)
			response.AppendHeader(wire.NewHeader("Expires", "3600"))
			_ = tx.Respond(response)
		}
		c.server.OnRegister(handle)
		c.server.OnMessage(handle)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	resolver, queries := failoverDNS(t, []*net.SRV{{Target: "primary.test.", Port: uint16(primary.contact.Address.Port)}, {Target: "backup.test.", Port: uint16(backup.contact.Address.Port), Priority: 1}}, map[string][][4]byte{"primary.test.": {{127, 0, 0, 1}}, "backup.test.": {{127, 0, 0, 1}}, "registrar.test.": {{127, 0, 0, 1}}})
	client, err := NewClient(Config{Server: "registrar.test", Domain: "office.test", Username: "alice", Password: "secret", Resolver: resolver, LocalAddress: "127.0.0.1:0", KeepAliveInterval: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(ctx, "bob", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshRegistration(ctx); err != nil {
		t.Fatal(err)
	}
	for index, method := range []wire.RequestMethod{wire.REGISTER, wire.MESSAGE, wire.REGISTER} {
		select {
		case request := <-requests:
			if request.Method != method || request.Recipient.Port != 0 || request.From().Address.Host != "office.test" {
				t.Fatalf("logical SIP authority changed on request %d: %s", index, request.String())
			}
		case <-ctx.Done():
			t.Fatal("backup did not receive authenticated request")
		}
	}
	if primaryRequests.Load() != 1 {
		t.Fatal("healthy selected backup was not retained for refresh")
	}
	question := <-queries
	if question.Type != dnsmessage.TypeSRV || question.Name.String() != "_sip._udp.registrar.test." {
		t.Fatalf("did not prefer the SIP SRV service: %+v", question)
	}
	foreign := client.request(wire.MESSAGE, wire.Uri{Scheme: "sip", User: "eve", Host: "foreign.test"}, nil)
	foreign.SetDestination(backup.LocalAddr())
	if client.credentialsAllowed(foreign, wire.NewResponseFromRequest(foreign, 401, "Unauthorized", nil), foreign.Recipient) {
		t.Fatal("DNS-selected hop widened origin credential authority")
	}
}

func TestExplicitRegistrarPortSkipsSRVAndCancellation(t *testing.T) {
	resolver, queries := failoverDNS(t, []*net.SRV{{Target: "unused.test.", Port: 9999}}, map[string][][4]byte{"registrar.test.": {{127, 0, 0, 1}, {127, 0, 0, 2}}})
	config := Config{Server: "registrar.test", Port: 5077, Resolver: resolver, Transport: "tcp"}
	targets, err := resolveRegistrarTargets(context.Background(), config, false)
	if err != nil || len(targets) != 2 {
		t.Fatalf("address resolution: %v %v", targets, err)
	}
	for _, target := range targets {
		_, port, _ := net.SplitHostPort(target)
		if port != strconv.Itoa(config.Port) {
			t.Fatal("explicit port changed")
		}
	}
	for len(queries) > 0 {
		if question := <-queries; question.Type == dnsmessage.TypeSRV {
			t.Fatal("explicit registrar port performed SRV discovery")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolveRegistrarTargets(ctx, config, false); err == nil {
		t.Fatal("canceled DNS resolution succeeded")
	}
}

type observedTCPListener struct {
	net.Listener
	accepted chan net.Conn
}

func (l observedTCPListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err == nil {
		l.accepted <- connection
	}
	return connection, err
}

func TestTCPFlowRecoveryUpdatesContactAndReceivesRequests(t *testing.T) {
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 8)
	requests := make(chan *wire.Request, 8)
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.Clone()
		response := wire.NewResponseFromRequest(req, 200, "OK", nil)
		response.AppendHeader(wire.NewHeader("Expires", "3600"))
		_ = tx.Respond(response)
	})
	server.OnOptions(func(req *wire.Request, tx wire.ServerTransaction) { respond(req, tx, 200, "OK") })
	go server.ServeTCP(observedTCPListener{Listener: listener, accepted: accepted})
	events := make(chan Event, 32)
	client, err := NewClient(Config{Server: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "alice", Transport: "tcp", LocalAddress: "127.0.0.1:0", KeepAliveInterval: time.Second}, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	first := <-requests
	if first.Contact().Address.HostPort() != first.Source() {
		t.Fatal("TCP registration did not advertise its outbound flow")
	}
	connection := <-accepted
	connection.Close()
	var renewed *wire.Request
	select {
	case renewed = <-requests:
	case <-ctx.Done():
		t.Fatal("keepalive did not restore registration after TCP flow loss")
	}
	if renewed.Source() == first.Source() || renewed.Contact().Address.HostPort() != renewed.Source() {
		t.Fatalf("reconnected Contact does not identify the new flow: %s", renewed.String())
	}
	peer, err := sipgo.NewClient(ua)
	if err != nil {
		t.Fatal(err)
	}
	message := wire.NewRequest(wire.MESSAGE, renewed.Contact().Address)
	message.SetTransport("tcp")
	message.SetDestination(renewed.Source())
	message.AppendHeader(wire.NewHeader("Content-Type", "text/plain"))
	message.SetBody([]byte("incoming on restored flow"))
	response, err := peer.Do(ctx, message)
	if err != nil || !response.IsSuccess() {
		t.Fatalf("incoming request on restored flow: %v %v", response, err)
	}
	if event := nextEvent(t, events, "message"); event.Message != "incoming on restored flow" {
		t.Fatalf("wrong incoming flow event: %+v", event)
	}
}
