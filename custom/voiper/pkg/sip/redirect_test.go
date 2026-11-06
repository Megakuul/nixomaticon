package sip

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func TestRedirectStripsCredentialsAndPreservesCall(t *testing.T) {
	targetEvents := make(chan Event, 16)
	forwarded := make(chan *wire.Request, 1)
	target, err := newClient(Config{Server: "127.0.0.1", Username: "target", LocalAddress: "127.0.0.1:0"}, func(e Event) { targetEvents <- e }, func(c *Client) {
		c.server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) { forwarded <- req.Clone(); c.onInvite(req, tx) })
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	redirector, err := newClient(Config{Server: "127.0.0.1", Username: "redirector", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
		c.server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
			if req.GetHeader("Authorization") == nil {
				response := wire.NewResponseFromRequest(req, 401, "Unauthorized", nil)
				response.AppendHeader(wire.NewHeader("WWW-Authenticate", `Digest realm="original", nonce="nonce", algorithm=SHA-256, qop="auth"`))
				_ = tx.Respond(response)
				return
			}
			response := wire.NewResponseFromRequest(req, 302, "Moved Temporarily", nil)
			response.AppendHeader(wire.HeaderClone(&target.contact))
			_ = tx.Respond(response)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer redirector.Close()
	callerEvents := make(chan Event, 16)
	caller, err := NewClient(Config{Server: "127.0.0.1", Port: redirector.contact.Address.Port, Username: "alice", Password: "secret", LocalAddress: "127.0.0.1:0", MaxRedirects: 1}, func(e Event) { callerEvents <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := caller.Dial(ctx, redirector.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	redirect := nextEvent(t, callerEvents, "redirect")
	if redirect.CallID != id || redirect.RemoteURI != target.contact.Address.String() {
		t.Fatalf("redirect event: %+v", redirect)
	}
	incoming := nextEvent(t, targetEvents, "incoming")
	if incoming.CallID != id {
		t.Fatal("redirect changed application call ID")
	}
	request := <-forwarded
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Authentication-Info", "Proxy-Authentication-Info"} {
		if request.GetHeader(name) != nil {
			t.Fatalf("forwarded %s to new destination", name)
		}
	}
	if request.To().Address.String() != target.contact.Address.String() {
		t.Fatalf("redirect retained old To destination: %s", request.To())
	}
	if err := target.Answer(ctx, id, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, callerEvents, "connected")
	if err := caller.Hangup(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectDestinationPolicy(t *testing.T) {
	client, _ := newTestClient(t, "alice")
	current, _ := client.recipient("sip:alice@example.test")
	for _, target := range []string{"https://example.test/path", "sip:alice@example.test", "sip:bob@example.test?Authorization=secret"} {
		response := wire.NewResponse(302, "Moved Temporarily")
		response.AppendHeader(wire.NewHeader("Contact", "<"+target+">"))
		if _, err := redirectTarget(current, response, map[string]bool{current.String(): true}); err == nil {
			t.Errorf("accepted redirect %q", target)
		}
	}
	secure := current
	secure.Scheme = "sips"
	response := wire.NewResponse(302, "Moved Temporarily")
	response.AppendHeader(wire.NewHeader("Contact", "<sip:bob@example.test>"))
	if _, err := redirectTarget(secure, response, nil); err == nil {
		t.Fatal("accepted secure signaling downgrade")
	}
}

func TestRedirectRefusesNewAuthorityAuthentication(t *testing.T) {
	for _, status := range []int{401, 407} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			attempts := make(chan *wire.Request, 4)
			target, err := newClient(Config{Server: "127.0.0.1", Username: "target", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
				c.server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
					attempts <- req.Clone()
					response := wire.NewResponseFromRequest(req, status, "Authentication Required", nil)
					name := "WWW-Authenticate"
					if status == 407 {
						name = "Proxy-Authenticate"
					}
					response.AppendHeader(wire.NewHeader(name, `Digest realm="original", nonce="new-host-nonce", algorithm=SHA-256, qop="auth"`))
					_ = tx.Respond(response)
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			redirector, err := newClient(Config{Server: "127.0.0.1", Username: "redirector", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
				c.server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
					response := wire.NewResponseFromRequest(req, 302, "Moved Temporarily", nil)
					response.AppendHeader(wire.HeaderClone(&target.contact))
					_ = tx.Respond(response)
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			defer redirector.Close()
			events := make(chan Event, 16)
			caller, err := NewClient(Config{Server: "127.0.0.1", Port: redirector.contact.Address.Port, Username: "alice", Password: "secret", LocalAddress: "127.0.0.1:0", MaxRedirects: 1}, func(e Event) { events <- e })
			if err != nil {
				t.Fatal(err)
			}
			defer caller.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := caller.Dial(ctx, redirector.contact.Address.String(), []byte(offerSDP)); err != nil {
				t.Fatal(err)
			}
			ended := nextEvent(t, events, "ended")
			if !strings.Contains(ended.Message, "authentication refused for redirected authority") {
				t.Fatalf("unexpected result: %+v", ended)
			}
			if len(attempts) != 1 {
				t.Fatalf("redirected authentication was retried: %d attempts", len(attempts))
			}
			request := <-attempts
			if request.GetHeader("Authorization") != nil || request.GetHeader("Proxy-Authorization") != nil {
				t.Fatal("redirected request disclosed credentials")
			}
		})
	}
}
