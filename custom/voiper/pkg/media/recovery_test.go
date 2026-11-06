package media

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
)

func recoveryCall(t *testing.T) *Call {
	t.Helper()
	call, err := NewCall(context.Background(), "recovery", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	call.closeTimeout = 20 * time.Millisecond
	call.recoveryInterval = 5 * time.Millisecond
	call.recoveryTimeout = time.Second
	t.Cleanup(func() { call.Close() })
	return call
}
func waitRecovery(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-deadline:
			t.Fatal("audio recovery timed out")
		case <-ticker.C:
		}
	}
}
func TestAudioRecoveryReopensStoppedDevices(t *testing.T) {
	call := recoveryCall(t)
	call.openAudio = syntheticAudio
	if err := call.Connect(call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	old := call.devices.Load()
	old.close()
	waitRecovery(t, func() bool { return call.devices.Load() != old })
	stats := call.Stats()
	if stats.AudioRecoveryAttempts != 1 || stats.AudioRecovering || stats.AudioStopped || stats.Codec != "PCMA" {
		t.Fatalf("recovered state: %+v", stats)
	}
}
func TestAudioRecoveryHasBoundedRetriesAndManualRetry(t *testing.T) {
	call := recoveryCall(t)
	var available atomic.Bool
	available.Store(true)
	call.openAudio = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		if !available.Load() {
			return nil, errors.New("audio server unavailable")
		}
		return syntheticAudio(settings, rate)
	}
	if err := call.Connect(call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	original := call.devices.Load()
	available.Store(false)
	original.close()
	waitRecovery(t, func() bool { return strings.HasPrefix(call.Stats().AudioRecoveryError, "automatic recovery stopped:") })
	if attempts := call.Stats().AudioRecoveryAttempts; attempts != 5 {
		t.Fatalf("attempted %d opens, want 5", attempts)
	}
	available.Store(true)
	if err := call.RetryAudio(); err != nil {
		t.Fatal(err)
	}
	waitRecovery(t, func() bool { return call.devices.Load() != original })
	if stats := call.Stats(); stats.AudioRecoveryAttempts != 6 || stats.AudioRecoveryError != "" {
		t.Fatalf("manual retry state: %+v", stats)
	}
}
func TestCancelDuringNativeRecoveryOpen(t *testing.T) {
	call := recoveryCall(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	late := make(chan *audio.Stream, 1)
	var opens atomic.Int32
	call.openAudio = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		if opens.Add(1) == 1 {
			return syntheticAudio(settings, rate)
		}
		close(entered)
		<-release
		stream, err := syntheticAudio(settings, rate)
		late <- stream
		return stream, err
	}
	if err := call.Connect(call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	call.devices.Load().close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	done := make(chan struct{})
	go func() { call.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("native open blocked call cancellation")
	}
	close(release)
	stream := <-late
	waitRecovery(t, func() bool { return stream.Stats().Stopped })
}

func TestInitialDeviceOpenCancellationAndLateCleanup(t *testing.T) {
	call := recoveryCall(t)
	entered, release := make(chan struct{}), make(chan struct{})
	late := make(chan *audio.Stream, 1)
	call.openAudio = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		close(entered)
		<-release
		stream, err := syntheticAudio(settings, rate)
		late <- stream
		return stream, err
	}
	finished := make(chan error, 1)
	offer := call.LocalSDP("127.0.0.1")
	go func() { finished <- call.Connect(offer) }()
	<-entered
	call.Close()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled open connected")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Connect ignored cancellation during native open")
	}
	close(release)
	stream := <-late
	waitRecovery(t, func() bool { return stream.Stats().Stopped && !call.nativeOpenPending.Load() })
}

func TestInitialDeviceOpenTimeoutPreventsConcurrentRetry(t *testing.T) {
	call := recoveryCall(t)
	call.recoveryTimeout = 20 * time.Millisecond
	release := make(chan struct{})
	call.openAudio = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		<-release
		return syntheticAudio(settings, rate)
	}
	offer := call.LocalSDP("127.0.0.1")
	if err := call.Connect(offer); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("open deadline: %v", err)
	}
	if err := call.Connect(offer); err == nil {
		close(release)
		t.Fatal("retry started during unfinished native open")
	}
	close(release)
	waitRecovery(t, func() bool { return !call.nativeOpenPending.Load() })
}
