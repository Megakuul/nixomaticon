//go:build integration

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run explicitly: VOIPER_TEST_NATIVE_AUDIO=1 go test -tags integration
// -run TestPulseSharedDuplex ./pkg/audio. No host devices or audio service are used.
func TestPulseSharedDuplex(t *testing.T) {
	if os.Getenv("VOIPER_TEST_NATIVE_AUDIO") != "1" {
		t.Skip("set VOIPER_TEST_NATIVE_AUDIO=1 for isolated native PulseAudio acceptance")
	}
	for _, tool := range []string{"pulseaudio", "pactl", "pacat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required: %v", tool, err)
		}
	}
	root := t.TempDir()
	socket := filepath.Join(root, "pulse.sock")
	config := filepath.Join(root, "pulse.pa")
	content := fmt.Sprintf("load-module module-native-protocol-unix socket=%s auth-anonymous=1\nload-module module-null-sink sink_name=voiper_fixture rate=8000 channels=1\nset-default-sink voiper_fixture\nset-default-source voiper_fixture.monitor\n", socket)
	if err := os.WriteFile(config, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_SERVER", "unix:"+socket)
	t.Setenv("PULSE_RUNTIME_PATH", root)
	t.Setenv("XDG_RUNTIME_DIR", root)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(root, "no-session-bus"))
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+filepath.Join(root, "no-system-bus"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	log, err := os.Create(filepath.Join(root, "pulse.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server := exec.CommandContext(ctx, "pulseaudio", "-n", "--daemonize=no", "--use-pid-file=no", "--exit-idle-time=-1", "--log-target=stderr", "--file="+config)
	server.Stderr = log
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if err = exec.CommandContext(ctx, "pactl", "info").Run(); err == nil {
			ready = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		raw, _ := os.ReadFile(filepath.Join(root, "pulse.log"))
		t.Fatalf("private PulseAudio did not start: %s", raw)
	}
	checkSharedDuplex(t, ctx, log, "pulse", func() { server.Process.Kill() })
}

func checkSharedDuplex(t *testing.T, ctx context.Context, log *os.File, backend string, stopServer func()) {
	t.Helper()
	for _, openStream := range []func(Settings, int) (*Stream, error){OpenPlayback, OpenCapture} {
		probe, err := openStream(Settings{Backend: backend}, 8000)
		if err != nil {
			t.Fatal(err)
		}
		if err := probe.Close(); err != nil {
			t.Fatal(err)
		}
	}
	stream, err := Open(Settings{Backend: backend}, 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	other := exec.CommandContext(ctx, "pacat", "--playback", "--raw", "--rate=8000", "--channels=1", "--format=s16le", "--latency-msec=40", "--device=voiper_fixture")
	input, err := other.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	other.Stderr = log
	if err = other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); other.Process.Kill(); other.Wait() }()
	// Separate process is an ordinary second audio client. Both tones must reach
	// the duplex stream's monitor input, proving that playback is shared.
	external := make([]byte, 8000*2*6)
	for i := 0; i < len(external)/2; i++ {
		binary.LittleEndian.PutUint16(external[i*2:], uint16(int16(4000*math.Sin(2*math.Pi*400*float64(i)/8000))))
	}
	written := make(chan struct{})
	go func() { _, _ = bytes.NewReader(external).WriteTo(input); close(written) }()
	defer func() {
		input.Close()
		other.Process.Kill()
		select {
		case <-written:
		case <-time.After(time.Second):
			t.Error("other audio writer did not stop")
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	captured := []int16{}
	frame := make([]byte, 320)
	read := make([]byte, 3200)
	sample := 0
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
		for i := 0; i < 160; i++ {
			binary.LittleEndian.PutUint16(frame[i*2:], uint16(int16(4000*math.Sin(2*math.Pi*700*float64(sample+i)/8000))))
		}
		sample += 160
		stream.Playback.Write(frame)
		n := stream.Capture.Read(read)
		for i := 0; i+1 < n; i += 2 {
			captured = append(captured, int16(binary.LittleEndian.Uint16(read[i:])))
		}
	}
	input.Close()
	other.Process.Kill()
	if len(captured) < 16000 {
		t.Fatalf("insufficient duplex capture: %d samples", len(captured))
	}
	captured = captured[len(captured)-16000:]
	amplitude := func(hz float64) float64 {
		var re, im float64
		for i, value := range captured {
			phase := 2 * math.Pi * hz * float64(i) / 8000
			re += float64(value) * math.Cos(phase)
			im += float64(value) * math.Sin(phase)
		}
		return 2 * math.Hypot(re, im) / float64(len(captured))
	}
	for _, hz := range []float64{400, 700} {
		if level := amplitude(hz); level < 600 {
			t.Fatalf("shared %g Hz tone missing (amplitude %.1f)", hz, level)
		} else {
			t.Logf("shared %g Hz amplitude %.1f", hz, level)
		}
	}
	if stream.Stats().Stopped {
		t.Fatal("native duplex callback stopped")
	}
	stopServer()
	for deadline := time.Now().Add(3 * time.Second); !stream.Stats().Stopped; {
		if time.Now().After(deadline) {
			t.Fatal("lost audio server was not reflected in stream health")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
