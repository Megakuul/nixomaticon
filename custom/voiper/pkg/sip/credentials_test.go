package sip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func TestForeignCallCannotChallengeAccountCredentials(t *testing.T) {
	for _, viaRefer := range []bool{false, true} {
		for _, status := range []int{401, 407} {
			t.Run(fmt.Sprintf("refer=%t/status=%d", viaRefer, status), func(t *testing.T) {
				requests := make(chan *wire.Request, 4)
				foreign, err := newClient(Config{Server: "127.0.0.1", Username: "foreign", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
					c.server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
						requests <- req.Clone()
						response := wire.NewResponseFromRequest(req, status, "Authentication Required", nil)
						header := "WWW-Authenticate"
						if status == 407 {
							header = "Proxy-Authenticate"
						}
						response.AppendHeader(wire.NewHeader(header, `Digest realm="office",nonce="untrusted",algorithm=SHA-256,qop="auth"`))
						_ = tx.Respond(response)
					})
				})
				if err != nil {
					t.Fatal(err)
				}
				defer foreign.Close()
				trusted, trustedEvents := newTestClient(t, "office")
				events := make(chan Event, 32)
				client, err := NewClient(Config{Server: "127.0.0.1", Port: trusted.contact.Address.Port, Username: "alice", Password: "secret", LocalAddress: "127.0.0.1:0"}, func(e Event) { events <- e })
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if viaRefer {
					original := connectTestCall(t, trusted, client, trustedEvents, events)
					handled := make(chan struct{})
					client.SetTransferHandler(func(ctx context.Context, _ string, target string) error {
						defer close(handled)
						id, err := client.Dial(ctx, target, []byte(offerSDP))
						if err != nil {
							return err
						}
						for {
							select {
							case event := <-events:
								if event.CallID == id && event.Type == "ended" {
									return errors.New(event.Message)
								}
							case <-ctx.Done():
								return ctx.Err()
							}
						}
					})
					if err := trusted.Transfer(ctx, original, foreign.contact.Address.String()); err != nil {
						t.Fatal(err)
					}
					select {
					case <-handled:
					case <-ctx.Done():
						t.Fatal("transfer callback did not finish")
					}
					if event := nextEvent(t, events, "transfer-complete"); event.State != "failed" || !strings.Contains(event.Message, "outside account scope") {
						t.Fatalf("foreign REFER authentication result: %+v", event)
					}
				} else {
					if _, err := client.Dial(ctx, foreign.contact.Address.String(), []byte(offerSDP)); err != nil {
						t.Fatal(err)
					}
					if event := nextEvent(t, events, "ended"); !strings.Contains(event.Message, "outside account scope") {
						t.Fatalf("foreign dial authentication result: %+v", event)
					}
				}
				if len(requests) != 1 {
					t.Fatalf("authentication retried against foreign target: %d", len(requests))
				}
				request := <-requests
				if request.GetHeader("Authorization") != nil || request.GetHeader("Proxy-Authorization") != nil {
					t.Fatal("credentials sent to foreign target")
				}
			})
		}
	}
}

func TestCredentialScopeIncludesPortsAndProxyRole(t *testing.T) {
	client, _ := newTestClient(t, "alice")
	account := wire.Uri{Scheme: "sip", Host: "127.0.0.1", Port: 5060}
	foreign := wire.Uri{Scheme: "sip", Host: "127.0.0.1", Port: 5090}
	challenge := wire.NewResponse(401, "Unauthorized")
	request := client.request(wire.INVITE, foreign, nil)
	if client.credentialsAllowed(request, challenge, foreign) {
		t.Fatal("foreign port considered the account authority")
	}
	request.SetDestination("127.0.0.1:5060")
	if client.credentialsAllowed(request, challenge, foreign) {
		t.Fatal("trusted proxy allowed foreign origin credentials")
	}
	challenge.StatusCode = 407
	if !client.credentialsAllowed(request, challenge, foreign) {
		t.Fatal("configured registrar route cannot request proxy authentication")
	}
	request = client.request(wire.INVITE, account, nil)
	challenge.StatusCode = 401
	if !client.credentialsAllowed(request, challenge, account) {
		t.Fatal("configured account authentication rejected")
	}
}

func TestOutboundProxyWithoutPortUsesTransportDefault(t *testing.T) {
	for _, transport := range []string{"udp", "tcp", "tls"} {
		t.Run(transport, func(t *testing.T) {
			client, err := NewClient(Config{Server: "office.invalid", Username: "alice", OutboundProxy: "proxy.invalid", Transport: transport, LocalAddress: "127.0.0.1:0"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			foreign := wire.Uri{Scheme: "sip", User: "bob", Host: "foreign.invalid"}
			request := client.request(wire.MESSAGE, foreign, nil)
			if request.Destination() != fmt.Sprintf("proxy.invalid:%d", wire.DefaultPort(transport)) {
				t.Fatalf("proxy destination lacks transport port: %s", request.Destination())
			}
			if !client.credentialsAllowed(request, wire.NewResponse(407, "Proxy Authentication Required"), foreign) {
				t.Fatal("explicit proxy cannot request proxy credentials")
			}
			if client.credentialsAllowed(request, wire.NewResponse(401, "Unauthorized"), foreign) {
				t.Fatal("explicit proxy allowed foreign origin credentials")
			}
			request.SetDestination(fmt.Sprintf("foreign.invalid:%d", wire.DefaultPort(transport)))
			if client.credentialsAllowed(request, wire.NewResponse(407, "Proxy Authentication Required"), foreign) {
				t.Fatal("unconfigured proxy obtained account credentials")
			}
		})
	}
}
