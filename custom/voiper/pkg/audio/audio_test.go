package audio

import (
	"bytes"
	"sync"
	"testing"
)

func TestRingWrapAndOverflow(t *testing.T) {
	ring := NewRing(4)
	if n := ring.Write([]byte{1, 2, 3, 4, 5}); n != 4 {
		t.Fatalf("wrote %d", n)
	}
	first := make([]byte, 2)
	ring.Read(first)
	ring.Write([]byte{5, 6})
	out := make([]byte, 4)
	if n := ring.Read(out); n != 4 || !bytes.Equal(out, []byte{3, 4, 5, 6}) {
		t.Fatalf("read %d: %v", n, out)
	}
	if ring.Read(out) != 0 {
		t.Fatal("empty ring returned data")
	}
}
func TestRingConcurrentPCM(t *testing.T) {
	ring := NewRing(64)
	const count = 10000
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < count; i++ {
			for ring.Write([]byte{byte(i)}) == 0 {
			}
		}
	}()
	sample := make([]byte, 1)
	for i := 0; i < count; i++ {
		for ring.Read(sample) == 0 {
		}
		if sample[0] != byte(i) {
			t.Fatalf("sample %d: got %d", i, sample[0])
		}
	}
	workers.Wait()
}
func TestMixMinusAndClipping(t *testing.T) {
	out := make([]int16, 2)
	Mix(out, [][]int16{{30000, -30000}, {30000, -30000}, {1, 2}}, 2)
	if out[0] != 32767 || out[1] != -32768 {
		t.Fatalf("clipping: %v", out)
	}
	Mix(out, [][]int16{{100, 200}, {300, 400}}, 0)
	if out[0] != 300 || out[1] != 400 {
		t.Fatalf("mix-minus: %v", out)
	}
}
func TestBackendPreference(t *testing.T) {
	available, err := backends("auto")
	if err != nil || backendName(available[0]) != "pulse" || backendName(available[1]) != "alsa" {
		t.Fatalf("backends %v: %v", available, err)
	}
	if _, err := backends("unknown"); err == nil {
		t.Fatal("accepted unknown backend")
	}
}

func TestPlaybackReferenceIncludesRenderedSilence(t *testing.T) {
	stream := &Stream{Capture: NewRing(16), Playback: NewRing(16), Reference: NewRing(16)}
	tap, stop, err := stream.TapPlayback(16)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	stream.Playback.Write([]byte{1, 2, 3, 4})
	rendered := make([]byte, 8)
	stream.processFrames(rendered, []byte{7, 8})
	want := []byte{1, 2, 3, 4, 0, 0, 0, 0}
	for _, queue := range []*Ring{stream.Reference, tap} {
		got := make([]byte, 8)
		if queue.Read(got) != 8 || !bytes.Equal(got, want) {
			t.Fatalf("reference %v, want %v", got, want)
		}
	}
	stop()
	stream.processFrames(rendered, nil)
	if tap.Read(rendered) != 0 {
		t.Fatal("removed tap still received playback")
	}
}
