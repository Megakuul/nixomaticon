//go:build integration

package audio

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The null PCM exercises the actual ALSA backend, callbacks and shutdown. It
// has no hardware clock, so this deliberately makes no timing/duplex quality claim.
func TestALSANullLifecycle(t *testing.T) {
	if os.Getenv("VOIPER_TEST_NATIVE_AUDIO") != "1" {
		t.Skip("set VOIPER_TEST_NATIVE_AUDIO=1 for isolated ALSA acceptance")
	}
	root := t.TempDir()
	config := filepath.Join(root, "alsa.conf")
	if err := os.WriteFile(config, []byte("pcm.!default { type null }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALSA_CONFIG_PATH", config)
	t.Setenv("PULSE_SERVER", "unix:"+filepath.Join(root, "no-pulse.sock"))
	t.Setenv("PULSE_RUNTIME_PATH", root)
	for _, backend := range []string{"alsa", "auto"} {
		t.Run(backend, func(t *testing.T) {
			for range 3 {
				stream, err := Open(Settings{Backend: backend}, 8000)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				if stream.Backend != "alsa" {
					t.Fatalf("selected %s instead of ALSA", stream.Backend)
				}
				pcm := make([]byte, 320)
				stream.Playback.Write(pcm)
				captured := 0
				for deadline := time.Now().Add(time.Second); captured == 0 && time.Now().Before(deadline); {
					captured = stream.Capture.Read(pcm)
					time.Sleep(time.Millisecond)
				}
				if captured == 0 || stream.Stats().Stopped {
					t.Fatal("ALSA duplex callback did not provide capture")
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if !stream.Stats().Stopped {
					t.Fatal("closed ALSA stream still reports active")
				}
			}
		})
	}
}

func TestALSAUnavailablePCM(t *testing.T) {
	if os.Getenv("VOIPER_TEST_NATIVE_AUDIO") != "1" {
		t.Skip("set VOIPER_TEST_NATIVE_AUDIO=1 for isolated ALSA acceptance")
	}
	root := t.TempDir()
	config := filepath.Join(root, "alsa.conf")
	// With no includes, even miniaudio's fallback names cannot resolve to a
	// physical device. The unknown plugin must fail without opening anything.
	if err := os.WriteFile(config, []byte("pcm.!default { type voiper_nonexistent_plugin }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALSA_CONFIG_PATH", config)
	for range 3 {
		stream, err := Open(Settings{Backend: "alsa"}, 8000)
		if err == nil {
			stream.Close()
			t.Fatal("unavailable ALSA PCM was accepted")
		}
	}
}
