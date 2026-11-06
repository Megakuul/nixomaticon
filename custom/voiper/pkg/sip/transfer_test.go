package sip

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReceivedTransferPlacesReplacementCall(t *testing.T) {
	alice, aliceEvents := newTestClient(t, "alice")
	bob, bobEvents := newTestClient(t, "bob")
	carol, carolEvents := newTestClient(t, "carol")
	original := connectTestCall(t, alice, bob, aliceEvents, bobEvents)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	replacement := make(chan string, 1)
	bob.SetTransferHandler(func(transferCtx context.Context, id, target string) error {
		if id != original {
			return errors.New("incorrect original dialog")
		}
		next, err := bob.Dial(ctx, target, []byte(offerSDP))
		if err != nil {
			return err
		}
		for {
			select {
			case event := <-bobEvents:
				if event.CallID != next {
					continue
				}
				if event.Type == "connected" {
					replacement <- next
					return nil
				}
				if event.Type == "ended" {
					return errors.New(event.Message)
				}
			case <-transferCtx.Done():
				_ = bob.Hangup(context.Background(), next)
				return transferCtx.Err()
			}
		}
	})
	if err := alice.Transfer(ctx, original, carol.contact.Address.String()); err != nil {
		t.Fatal(err)
	}
	incoming := nextEvent(t, carolEvents, "incoming")
	if err := carol.Answer(ctx, incoming.CallID, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-replacement:
		if id != incoming.CallID {
			t.Fatal("replacement mismatch")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	completed := nextEvent(t, bobEvents, "transfer-complete")
	if completed.State != "success" {
		t.Fatalf("transfer failed: %+v", completed)
	}
	first := nextEvent(t, aliceEvents, "transfer")
	final := nextEvent(t, aliceEvents, "transfer")
	if !strings.Contains(string(first.Body), "100 Trying") || !strings.Contains(string(final.Body), "200 OK") {
		t.Fatalf("missing transfer progress: %s / %s", first.Body, final.Body)
	}
	if _, err := bob.getCall(original); err != nil {
		t.Fatal("original ended before application confirmed replacement")
	}
	if err := bob.Hangup(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := bob.Hangup(ctx, incoming.CallID); err != nil {
		t.Fatal(err)
	}
}

func TestReplacesRequiresExactDialogTags(t *testing.T) {
	alice, aliceEvents := newTestClient(t, "alice")
	bob, bobEvents := newTestClient(t, "bob")
	carol, carolEvents := newTestClient(t, "carol")
	consultation := connectTestCall(t, alice, carol, aliceEvents, carolEvents)
	cl, _ := alice.getCall(consultation)
	local, _ := cl.outgoing.InviteRequest.From().Params.Get("tag")
	remote, _ := cl.outgoing.InviteResponse.To().Params.Get("tag")
	target := carol.contact.Address.String() + "?Replaces=" + url.QueryEscape(consultation+";to-tag="+remote+";from-tag="+local)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	badTarget := strings.Replace(target, remote, "unknown-tag", 1)
	if _, err := bob.Dial(ctx, badTarget, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	failed := nextEvent(t, bobEvents, "ended")
	if !strings.Contains(failed.Message, "481") {
		t.Fatalf("unknown tags accepted: %+v", failed)
	}
	id, err := bob.Dial(ctx, target, []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	incoming := nextEvent(t, carolEvents, "incoming")
	if incoming.ReplacesCallID != consultation || incoming.CallID != id {
		t.Fatalf("incorrect Replaces correlation: %+v", incoming)
	}
	if err := carol.Answer(ctx, id, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, bobEvents, "connected")
	if _, err := carol.getCall(consultation); err != nil {
		t.Fatal("replacement ended original without application decision")
	}
}

func TestTransferURIRejectsHeaderInjection(t *testing.T) {
	client, _ := newTestClient(t, "alice")
	for _, target := range []string{"sip:bob@localhost?From=mallory", "sip:bob@localhost?Replaces=id%3Bto-tag%3Da%3Bfrom-tag%3Db%0d%0aInjected%3Ayes", "sip:bob@localhost?Replaces=id"} {
		if _, _, err := client.inviteTarget(target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
}
