package app

import (
	"context"
	"testing"
	"time"

	"github.com/megakuul/voiper/internal/phone"
)

func TestCallCommandReturnsImmediatelyAndDoesNotQueueDuplicates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := NewApp()
	app.ctx, app.cancel = ctx, cancel
	app.phone = &phone.Manager{}
	close(app.initialized)
	started := make(chan struct{})
	if err := app.startCall(func() error { close(started); <-ctx.Done(); return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("call command did not start")
	}
	if err := app.startCall(func() error { t.Error("duplicate call was queued"); return nil }); err == nil {
		t.Fatal("duplicate activation accepted")
	}
	cancel()
	finished := make(chan struct{})
	go func() { app.workers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled command retained worker")
	}
	app.mu.Lock()
	app.closing = true
	app.mu.Unlock()
	if err := app.startCall(func() error { t.Error("call started during shutdown"); return nil }); err == nil {
		t.Fatal("call accepted during shutdown")
	}
}
