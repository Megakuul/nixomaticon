package sip

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func publicationClient(t *testing.T, handler func(*wire.Request, wire.ServerTransaction)) *Client {
	t.Helper()
	server, err := newClient(Config{Server: "127.0.0.1", Username: "publisher", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) { c.server.OnPublish(handler) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	_, portText, _ := net.SplitHostPort(server.LocalAddr())
	port, _ := strconv.Atoi(portText)
	client, err := NewClient(Config{Server: "127.0.0.1", Port: port, Domain: "presence.invalid", Username: "alice", LocalAddress: "127.0.0.1:0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestPublicationLifetimeRefreshAndRemoval(t *testing.T) {
	requests := make(chan *wire.Request, 4)
	var sequence atomic.Int32
	client := publicationClient(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.Clone()
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "10"))
		res.AppendHeader(wire.NewHeader("SIP-ETag", "tag"+strconv.Itoa(int(sequence.Add(1)))))
		_ = tx.Respond(res)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, err := client.PublishPresenceLease(ctx, true, "Lunch & tea", "", time.Hour)
	if err != nil || lease.EntityTag != "tag1" || lease.Expires != 10*time.Second {
		t.Fatalf("publication: %+v, %v", lease, err)
	}
	req := <-requests
	if req.GetHeader("SIP-If-Match") != nil || !strings.Contains(string(req.Body()), "Lunch &amp; tea") {
		t.Fatalf("initial publication headers/body: %s", req.String())
	}
	lease, err = client.RefreshPresence(ctx, lease.EntityTag, time.Hour)
	if err != nil || lease.EntityTag != "tag2" {
		t.Fatalf("refresh: %+v, %v", lease, err)
	}
	req = <-requests
	if len(req.Body()) != 0 || req.ContentType() != nil || req.GetHeader("SIP-If-Match").Value() != "tag1" {
		t.Fatalf("refresh must be bodyless and conditional: %s", req.String())
	}
	if _, err = client.RefreshPresence(ctx, lease.EntityTag, 0); err != nil {
		t.Fatal(err)
	}
	req = <-requests
	if len(req.Body()) != 0 || req.GetHeader("Expires").Value() != "0" || req.GetHeader("SIP-If-Match").Value() != "tag2" {
		t.Fatalf("removal: %s", req.String())
	}
}

func TestPublicationMinimumExpiryRetry(t *testing.T) {
	var requests atomic.Int32
	client := publicationClient(t, func(req *wire.Request, tx wire.ServerTransaction) {
		if requests.Add(1) == 1 {
			res := wire.NewResponseFromRequest(req, 423, "Interval Too Brief", nil)
			res.AppendHeader(wire.NewHeader("Min-Expires", "7200"))
			_ = tx.Respond(res)
			return
		}
		if req.GetHeader("Expires").Value() != "7200" {
			t.Error("minimum expiry was not applied")
		}
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "7200"))
		res.AppendHeader(wire.NewHeader("SIP-ETag", "new"))
		_ = tx.Respond(res)
	})
	lease, err := client.PublishPresenceLease(context.Background(), true, "", "", time.Hour)
	if err != nil || lease.Expires != 2*time.Hour || requests.Load() != 2 {
		t.Fatalf("minimum expiry: %+v, %v, requests=%d", lease, err, requests.Load())
	}
}

func TestPublicationInvalidResponseDoesNotClaimLease(t *testing.T) {
	for _, test := range []struct{ name, expiry, tag string }{
		{"missing expiry", "", "valid"}, {"zero expiry", "0", "valid"},
		{"extended expiry", "7200", "valid"}, {"overflow expiry", "4294967296", "valid"},
		{"missing tag", "20", ""}, {"invalid tag", "20", "two tags"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := publicationClient(t, func(req *wire.Request, tx wire.ServerTransaction) {
				res := wire.NewResponseFromRequest(req, 200, "OK", nil)
				if test.expiry != "" {
					res.AppendHeader(wire.NewHeader("Expires", test.expiry))
				}
				if test.tag != "" {
					res.AppendHeader(wire.NewHeader("SIP-ETag", test.tag))
				}
				_ = tx.Respond(res)
			})
			if lease, err := client.PublishPresenceLease(context.Background(), true, "", "", time.Hour); err == nil || lease.EntityTag != "" {
				t.Fatalf("invalid response accepted: %+v, %v", lease, err)
			}
		})
	}
}

func TestPublicationLostTagRequiresNewPublication(t *testing.T) {
	var requests atomic.Int32
	client := publicationClient(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests.Add(1)
		_ = tx.Respond(wire.NewResponseFromRequest(req, 412, "Conditional Request Failed", nil))
	})
	_, err := client.RefreshPresence(context.Background(), "gone", time.Hour)
	var response *ResponseError
	if !errors.As(err, &response) || response.Status != 412 || requests.Load() != 1 {
		t.Fatalf("lost tag must be reported without retry: %v, requests=%d", err, requests.Load())
	}
}

func TestPublicationReportsServerRetryDelay(t *testing.T) {
	client := publicationClient(t, func(req *wire.Request, tx wire.ServerTransaction) {
		res := wire.NewResponseFromRequest(req, 503, "Unavailable", nil)
		res.AppendHeader(wire.NewHeader("Retry-After", "120 (maintenance);duration=30"))
		_ = tx.Respond(res)
	})
	_, err := client.PublishPresenceLease(context.Background(), true, "", "", time.Hour)
	var response *ResponseError
	if !errors.As(err, &response) || response.RetryAfter != 2*time.Minute {
		t.Fatalf("server retry delay was not preserved: %v", err)
	}
}
