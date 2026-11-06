package media

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
)

func TestCloseRetainsStalledNativeCleanupAndFinalError(t *testing.T) {
	call := recoveryCall(t)
	call.settings.DisableAutoRecovery = true
	call.openAudio = syntheticAudio
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	failure := errors.New("native device close failed")
	var closes atomic.Int32
	call.closeAudio = func(stream *audio.Stream) error {
		closes.Add(1)
		<-release
		stream.Close()
		return failure
	}
	if err := call.Connect(call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if err := call.Close(); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("pending cleanup: %v", err)
	}
	if !call.Stats().CleanupPending || call.stream.Stats().Stopped {
		t.Fatal("native resources released before cleanup completed")
	}
	workersStopped := make(chan struct{})
	go func() { call.workers.Wait(); close(workersStopped) }()
	select {
	case <-workersStopped:
	case <-time.After(time.Second):
		t.Fatal("media workers waited for native teardown")
	}
	if err := call.Close(); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("repeated pending cleanup: %v", err)
	}
	finish()
	select {
	case <-call.closedDone:
	case <-time.After(time.Second):
		t.Fatal("late native cleanup did not finish")
	}
	if err := call.Close(); !errors.Is(err, failure) {
		t.Fatalf("lost late native error: %v", err)
	}
	if closes.Load() != 1 || call.Stats().CleanupPending || call.Stats().LastError != failure.Error() {
		t.Fatal("cleanup state or ownership was lost")
	}
}

func TestRecoveryWorkerStopsWhileNativeTeardownIsStalled(t *testing.T) {
	call := recoveryCall(t)
	call.openAudio = syntheticAudio
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	call.closeAudio = func(stream *audio.Stream) error {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return stream.Close()
	}
	if err := call.Connect(call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	call.devices.Load().close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native recovery teardown did not start")
	}
	if err := call.Close(); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("pending recovery cleanup: %v", err)
	}
	workersStopped := make(chan struct{})
	go func() { call.workers.Wait(); close(workersStopped) }()
	select {
	case <-workersStopped:
	case <-time.After(time.Second):
		t.Fatal("recovery worker waited for native teardown")
	}
	finish()
	select {
	case <-call.closedDone:
	case <-time.After(time.Second):
		t.Fatal("late recovery cleanup did not finish")
	}
	if err := call.Close(); err != nil {
		t.Fatal(err)
	}
}
