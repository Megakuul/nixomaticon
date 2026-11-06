//go:build speex && opus

package media

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/megakuul/voiper/pkg/audio"
	"time"
)

func TestCodecSampleAndClockRateChangesKeepNativeRate(t *testing.T) {
	a, b := renegotiationPair(t, true)
	originalA, originalB := a.stream, b.stream
	for _, name := range []string{"G722", "opus", "PCMU"} {
		answer, err := b.AnswerOffer(codecOffer(t, a, name), "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Connect(answer); err != nil {
			t.Fatal(err)
		}
		exchangePCM(t, a, b)
		if a.stream != originalA || b.stream != originalB || a.deviceRate != 8000 || b.deviceRate != 8000 {
			t.Fatal("codec rate change reopened native devices")
		}
		if name == "opus" && (a.Stats().ClockRate != 48000 || a.Stats().SampleRate != 48000) {
			t.Fatal("Opus clock was not applied")
		}
	}
}

func TestRateChangesKeepConferenceTonesAndRecording(t *testing.T) {
	a, remoteA := renegotiationPair(t, false)
	b, remoteB := renegotiationPair(t, false)
	if err := JoinConference(a, b); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "conference.wav")
	if err := a.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	recording, legA, legB := a.recording.Load(), a.conference.Load(), b.conference.Load()
	for _, names := range [][2]string{{"G722", "PCMA"}, {"opus", "G722"}, {"PCMU", "opus"}, {"PCMA", "PCMU"}} {
		for i, pair := range [][2]*Call{{a, remoteA}, {b, remoteB}} {
			answer, err := pair[0].AnswerOffer(codecOffer(t, pair[1], names[i]), "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if err = pair[1].Connect(answer); err != nil {
				t.Fatal(err)
			}
		}
		checkConferenceTones(t, remoteA, remoteB)
		if a.recording.Load() != recording || a.conference.Load() != legA || b.conference.Load() != legB {
			t.Fatal("codec change replaced recording or conference")
		}
	}
	if err := a.StopRecording(); err != nil {
		t.Fatal(err)
	}
	wav, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) <= 44 || binary.LittleEndian.Uint32(wav[24:]) != 8000 || binary.LittleEndian.Uint16(wav[22:]) != 2 || int(binary.LittleEndian.Uint32(wav[40:])) != len(wav)-44 {
		t.Fatal("recording changed format or did not finalize its WAV header")
	}
	var audible [2]int
	for offset := 44; offset+3 < len(wav); offset += 4 {
		for channel := range audible {
			if math.Abs(float64(int16(binary.LittleEndian.Uint16(wav[offset+channel*2:])))) > 300 {
				audible[channel]++
			}
		}
	}
	if audible[0] < 1000 || audible[1] < 1000 {
		t.Fatalf("recording lost local conference mix or remote speech: %v", audible)
	}
}

func checkConferenceTones(t *testing.T, a, b *Call) {
	t.Helper()
	peers := []*Call{a, b}
	frequencies := []float64{400, 700}
	frames := make([][]byte, 2)
	for i, peer := range peers {
		frames[i] = make([]byte, peer.deviceRate/50*2)
		for sample := 0; sample < len(frames[i])/2; sample++ {
			value := int16(2500 * math.Sin(2*math.Pi*frequencies[i]*float64(sample)/float64(peer.deviceRate)))
			binary.LittleEndian.PutUint16(frames[i][sample*2:], uint16(value))
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	heard := [2]bool{}
	for frame := 0; frame < 75; frame++ {
		<-ticker.C
		for i, peer := range peers {
			peer.captureStream.Capture.Write(frames[i])
			output := make([]byte, len(frames[i]))
			n := peer.stream.Playback.Read(output)
			if frame < 12 || n != len(output) {
				continue
			}
			var ownSine, ownCosine, otherSine, otherCosine float64
			for sample := 0; sample < n/2; sample++ {
				value := float64(int16(binary.LittleEndian.Uint16(output[sample*2:])))
				phase := 2 * math.Pi * float64(sample) / float64(peer.deviceRate)
				ownSine += value * math.Sin(phase*frequencies[i])
				ownCosine += value * math.Cos(phase*frequencies[i])
				otherSine += value * math.Sin(phase*frequencies[1-i])
				otherCosine += value * math.Cos(phase*frequencies[1-i])
			}
			own := math.Hypot(ownSine, ownCosine) / float64(n/2)
			other := math.Hypot(otherSine, otherCosine) / float64(n/2)
			if other > 300 && own < other/4 {
				heard[i] = true
			}
		}
		if heard[0] && heard[1] {
			return
		}
	}
	t.Fatalf("conference lost remote tone or returned self echo: %v; %+v; %+v", heard, a.Stats(), b.Stats())
}

func TestRecoveryAndCloseAfterRecordedConferenceRateChange(t *testing.T) {
	a, remoteA := renegotiationPair(t, false)
	b, remoteB := renegotiationPair(t, false)
	if err := JoinConference(a, b); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recovered.wav")
	if err := a.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	answer, err := a.AnswerOffer(codecOffer(t, remoteA, "opus"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = remoteA.Connect(answer); err != nil {
		t.Fatal(err)
	}
	recording, leg := a.recording.Load(), a.conference.Load()
	rates := make(chan int, 2)
	release := make(chan struct{})
	var opens atomic.Int32
	a.mu.Lock()
	a.recoveryInterval = 5 * time.Millisecond
	a.recoveryTimeout = time.Second
	a.closeTimeout = 20 * time.Millisecond
	a.openAudio = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		rates <- rate
		if opens.Add(1) == 2 {
			<-release
		}
		return syntheticAudio(settings, rate)
	}
	a.mu.Unlock()
	defer close(release)
	old := a.devices.Load()
	old.close()
	if err = a.RetryAudio(); err != nil {
		t.Fatal(err)
	}
	waitRecovery(t, func() bool { return a.devices.Load() != old })
	if rate := <-rates; rate != 8000 {
		t.Fatalf("recovery used codec rate %d instead of device rate", rate)
	}
	checkConferenceTones(t, remoteA, remoteB)
	if a.recording.Load() != recording || a.conference.Load() != leg {
		t.Fatal("recovery replaced recording or conference")
	}
	a.devices.Load().close()
	select {
	case rate := <-rates:
		if rate != 8000 {
			t.Fatal("second recovery changed device rate")
		}
	case <-time.After(time.Second):
		t.Fatal("second recovery did not start")
	}
	if _, err = a.AnswerOffer(codecOffer(t, remoteA, "G722"), "127.0.0.1"); err == nil {
		t.Fatal("codec update raced an unfinished device open")
	}
	if err = a.Close(); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("close with stalled recovery: %v", err)
	}
	// Release the native-open seam before waiting for owned cleanup.
	release <- struct{}{}
	select {
	case <-a.closedDone:
	case <-time.After(time.Second):
		t.Fatal("late device open prevented cleanup")
	}
	wav, err := os.ReadFile(path)
	if err != nil || len(wav) < 44 || binary.LittleEndian.Uint32(wav[24:]) != 8000 || int(binary.LittleEndian.Uint32(wav[40:])) != len(wav)-44 {
		t.Fatalf("shutdown did not finalize fixed-rate recording: %v", err)
	}
}
