package media

import (
	"errors"
	"testing"
)

func TestDTMFFallbackOnlyForMissingNegotiation(t *testing.T) {
	call := &Call{connected: true, dtmf: make(chan byte, 1)}
	if err := call.SendDTMF("5"); !errors.Is(err, ErrTelephoneEventsNotNegotiated) {
		t.Fatalf("missing mapping: %v", err)
	}
	call.remote.hasTelephone = true
	call.remote.events = 1 << 5
	if err := call.SendDTMF("6"); !errors.Is(err, ErrTelephoneEventsNotNegotiated) {
		t.Fatalf("unsupported digit: %v", err)
	}
	if err := call.SendDTMF("5"); err != nil {
		t.Fatal(err)
	}
	if err := call.SendDTMF("5"); err == nil || errors.Is(err, ErrTelephoneEventsNotNegotiated) {
		t.Fatalf("full queue must not fall back: %v", err)
	}
	<-call.dtmf
	call.SetHeld(true)
	if err := call.SendDTMF("5"); err == nil || errors.Is(err, ErrTelephoneEventsNotNegotiated) {
		t.Fatalf("held call must not fall back: %v", err)
	}
	call.SetHeld(false)
	call.closed = true
	if err := call.SendDTMF("5"); err == nil || errors.Is(err, ErrTelephoneEventsNotNegotiated) {
		t.Fatalf("closed call must not fall back: %v", err)
	}
}
