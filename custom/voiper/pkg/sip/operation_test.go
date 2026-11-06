package sip

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHangupDeadlineWhileRenegotiating(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	entered, release := make(chan struct{}), make(chan struct{})
	receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) { close(entered); <-release; return offer, nil })
	renegotiated := make(chan error, 1)
	go func() { renegotiated <- caller.Reinvite(context.Background(), id, []byte(offerSDP)) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- caller.Hangup(ctx, id) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Hangup ignored deadline while waiting for renegotiation")
	}
	close(release)
	if err := <-renegotiated; err != nil {
		t.Fatal(err)
	}
	if err := caller.Hangup(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, receiverEvents, "ended")
}

func TestRejectDeadlineWhileAnswerOperationPending(t *testing.T) {
	caller, _ := newTestClient(t, "alice")
	receiver, events := newTestClient(t, "bob")
	id, err := caller.Dial(context.Background(), receiver.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "incoming")
	cl, _ := receiver.getCall(id)
	if err := cl.operation.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- receiver.Reject(ctx, id) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Reject ignored deadline while waiting for answer operation")
	}
	cl.operation.Unlock()
	if err := receiver.Reject(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

func TestAbortCancelsPendingRenegotiation(t *testing.T) {
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	entered, release := make(chan struct{}), make(chan struct{})
	receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) { close(entered); <-release; return offer, nil })
	result := make(chan error, 1)
	go func() { result <- caller.Reinvite(context.Background(), id, []byte(offerSDP)) }()
	<-entered
	if err := caller.AbortCall(id); err != nil {
		close(release)
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("pending negotiation returned %v", err)
		}
	case <-time.After(time.Second):
		t.Error("aborted call retained pending transaction")
	}
	close(release)
	ended := nextEvent(t, callerEvents, "ended")
	if ended.Message != "call aborted locally" {
		t.Fatalf("unexpected local termination: %+v", ended)
	}
	if _, err := caller.getCall(id); err == nil {
		t.Fatal("aborted call remains in client")
	}
}

func TestRegistrationRefreshDeadlineWhileAnotherExchangePending(t *testing.T) {
	client, _ := newTestClient(t, "alice")
	if err := client.registerMu.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- client.RefreshRegistration(ctx) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("registration refresh ignored its deadline")
	}
	client.registerMu.Unlock()
}
