package audio

import (
	"context"
	"testing"
	"time"
)

func TestDiagnosticCancellationRetainsNativeOpenGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	stream := &Stream{}
	finished := make(chan error, 1)
	go func() {
		_, _, err := openDiagnostic(ctx, func() (*Stream, error) { close(entered); <-release; return stream, nil })
		finished <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled open succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled audio open blocked")
	}
	if _, _, err := openDiagnostic(context.Background(), func() (*Stream, error) { t.Fatal("second native open started"); return nil, nil }); err == nil {
		t.Fatal("stuck native open was not gated")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(diagnosticDevice) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(diagnosticDevice) != 0 || !stream.Stats().Stopped {
		t.Fatal("late audio resource was not closed")
	}
}
