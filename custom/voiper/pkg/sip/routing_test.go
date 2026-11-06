package sip

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

func TestAccountDomainRoutesThroughConfiguredDNSRegistrar(t *testing.T) {
	authorized := make(chan *wire.Request, 16)
	peer, err := newClient(Config{Server: "127.0.0.1", Username: "pbx", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
		handle := func(req *wire.Request, tx wire.ServerTransaction) {
			header := req.GetHeader("Authorization")
			challenge := &digest.Challenge{Realm: "office", Nonce: "routing", Algorithm: "SHA-256", QOP: []string{"auth"}}
			if header == nil {
				response := wire.NewResponseFromRequest(req, 401, "Unauthorized", nil)
				response.AppendHeader(wire.NewHeader("WWW-Authenticate", challenge.String()))
				_ = tx.Respond(response)
				return
			}
			credentials, err := digest.ParseCredentials(header.Value())
			if err == nil {
				expected, digestErr := digest.Digest(challenge, digest.Options{Method: string(req.Method), URI: req.Recipient.Addr(), Username: "alice", Password: "secret", Cnonce: credentials.Cnonce, Count: credentials.Nc})
				if digestErr != nil || expected.Response != credentials.Response {
					err = fmt.Errorf("incorrect digest: %v", digestErr)
				}
			}
			if err != nil {
				t.Error(err)
				respond(req, tx, 403, "Forbidden")
				return
			}
			authorized <- req.Clone()
			if req.Method == wire.INVITE {
				respond(req, tx, 486, "Busy Here")
				return
			}
			response := wire.NewResponseFromRequest(req, 200, "OK", nil)
			if req.Method == wire.SUBSCRIBE {
				response.AppendHeader(wire.NewHeader("Expires", req.GetHeader("Expires").Value()))
			}
			_ = tx.Respond(response)
		}
		c.server.OnInvite(handle)
		c.server.OnMessage(handle)
		c.server.OnSubscribe(handle)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	events := make(chan Event, 32)
	client, err := NewClient(Config{Server: "localhost", Domain: "office.invalid", Port: peer.contact.Address.Port, Username: "alice", Password: "secret", LocalAddress: "127.0.0.1:0"}, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, target := range []string{"bob", "sip:bob@office.invalid", "sip:bob@localhost"} {
		t.Run(target, func(t *testing.T) {
			if err := client.SendMessage(ctx, target, "hello"); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Dial(ctx, target, []byte(offerSDP)); err != nil {
				t.Fatal(err)
			}
			if event := nextEvent(t, events, "ended"); !strings.Contains(event.Message, "486") {
				t.Fatalf("call failed before authenticated PBX response: %+v", event)
			}
			id, err := client.Subscribe(ctx, target, "presence", "application/pidf+xml")
			if err != nil {
				t.Fatal(err)
			}
			// The test notifier has no Contact, so its established subscription
			// is deliberately discarded locally after proving initial routing.
			client.mu.Lock()
			sub := client.subscriptions[id]
			client.mu.Unlock()
			client.removeSubscription(sub)
			for range 3 {
				select {
				case request := <-authorized:
					if request.Recipient.Port != 0 || request.Recipient.User != "bob" {
						t.Fatalf("account URI rewritten: %s", request.Recipient.String())
					}
				case <-ctx.Done():
					t.Fatal("PBX did not authenticate all account requests")
				}
			}
		})
	}
}

func TestAccountRoutingPreservesExplicitForeignAuthority(t *testing.T) {
	client, _ := newTestClient(t, "alice")
	client.config.Domain = "office.invalid"
	client.config.Port = 5090
	client.registrar.Port = 5090
	for _, target := range []string{"sip:bob@office.invalid:5060", "sip:bob@foreign.invalid"} {
		uri, err := client.recipient(target)
		if err != nil {
			t.Fatal(err)
		}
		request := client.request(wire.MESSAGE, uri, nil)
		if strings.HasPrefix(request.Destination(), "127.0.0.1:") || client.accountAuthority(uri) {
			t.Fatalf("foreign authority routed as account: %s", target)
		}
	}
}
