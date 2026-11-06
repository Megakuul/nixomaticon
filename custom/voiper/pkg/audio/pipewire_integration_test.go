//go:build integration

package audio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This private graph has only a virtual sink. WirePlumber's policy profile links
// ordinary Pulse clients without starting hardware, Bluetooth or video monitors.
func TestPipeWireSharedDuplex(t *testing.T) {
	if os.Getenv("VOIPER_TEST_NATIVE_AUDIO") != "1" {
		t.Skip("set VOIPER_TEST_NATIVE_AUDIO=1 for isolated PipeWire-Pulse acceptance")
	}
	for _, tool := range []string{"pipewire", "pipewire-pulse", "wireplumber", "pactl", "pacat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required: %v", tool, err)
		}
	}
	root := t.TempDir()
	socket := filepath.Join(root, "pulse.sock")
	for key, value := range map[string]string{
		"XDG_RUNTIME_DIR": root, "PIPEWIRE_RUNTIME_DIR": root,
		"PIPEWIRE_REMOTE": "voiper-pipewire", "PIPEWIRE_CONFIG_DIR": root,
		"PULSE_SERVER": "unix:" + socket, "PULSE_RUNTIME_PATH": root,
		"WIREPLUMBER_CONFIG_DIR": root, "XDG_STATE_HOME": root,
		"XDG_CONFIG_HOME": root, "XDG_CONFIG_DIRS": root,
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=" + filepath.Join(root, "no-session-bus"),
		"DBUS_SYSTEM_BUS_ADDRESS":  "unix:path=" + filepath.Join(root, "no-system-bus"),
	} {
		t.Setenv(key, value)
	}
	core := `context.properties = {
  core.daemon = true
  core.name = voiper-pipewire
  default.clock.rate = 8000
  default.clock.quantum = 160
  default.clock.min-quantum = 80
  default.clock.max-quantum = 320
}
context.spa-libs = {
  audio.convert.* = audioconvert/libspa-audioconvert
  support.* = support/libspa-support
}
context.modules = [
  { name = libpipewire-module-protocol-native }
  { name = libpipewire-module-metadata }
  { name = libpipewire-module-spa-node-factory }
  { name = libpipewire-module-client-node }
  { name = libpipewire-module-access args = {
      access.socket = { voiper-pipewire = unrestricted }
  } }
  { name = libpipewire-module-adapter }
  { name = libpipewire-module-link-factory }
]
context.objects = [
  { factory = spa-node-factory args = {
      factory.name = support.node.driver
      node.name = Dummy-Driver
      priority.driver = 200000
  } }
]
`
	pulse := fmt.Sprintf(`context.spa-libs = {
  audio.convert.* = audioconvert/libspa-audioconvert
  support.* = support/libspa-support
}
context.modules = [
  { name = libpipewire-module-protocol-native }
  { name = libpipewire-module-client-node }
  { name = libpipewire-module-adapter }
  { name = libpipewire-module-metadata }
  { name = libpipewire-module-protocol-pulse }
]
pulse.properties = { server.address = [ "unix:%s" ] }
pulse.cmd = [
  { cmd = load-module args = "module-null-sink sink_name=voiper_fixture rate=8000 channels=1 channel_map=mono" }
]
`, socket)
	// Copy the installed policy components, while selecting an explicit profile
	// with no hardware features and no persistent host state or DBus services.
	wp, _ := exec.LookPath("wireplumber")
	wp, err := filepath.EvalSymlinks(wp)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile(filepath.Join(filepath.Dir(wp), "..", "share", "wireplumber", "wireplumber.conf"))
	if err != nil {
		t.Fatal(err)
	}
	policy = append(policy, []byte(`
wireplumber.profiles = {
  voiper-fixture = {
    inherits = [ policy, mixin.systemwide-session, mixin.stateless ]
    support.dbus = disabled
    hardware.audio = disabled
    hardware.bluetooth = disabled
    hardware.video-capture = disabled
  }
}
`)...)
	for name, data := range map[string][]byte{"core.conf": []byte(core), "pulse.conf": []byte(pulse), "wireplumber.conf": policy} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	log, err := os.Create(filepath.Join(root, "pipewire.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	defer func() {
		if t.Failed() {
			raw, _ := os.ReadFile(log.Name())
			t.Logf("private PipeWire log:\n%s", raw)
		}
	}()
	var pulseServer *exec.Cmd
	for _, argv := range [][]string{
		{"pipewire", "-c", "core.conf"},
		{"wireplumber", "--profile=voiper-fixture"},
		{"pipewire-pulse", "-c", "pulse.conf"},
	} {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cmd.Process.Kill(); cmd.Wait() }()
		if argv[0] == "pipewire-pulse" {
			pulseServer = cmd
		}
		if argv[0] == "pipewire" {
			for deadline := time.Now().Add(3 * time.Second); ; {
				if _, err := os.Stat(filepath.Join(root, "voiper-pipewire")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("private PipeWire socket did not start")
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		if err := exec.CommandContext(ctx, "pactl", "set-default-sink", "voiper_fixture").Run(); err == nil {
			if err := exec.CommandContext(ctx, "pactl", "set-default-source", "voiper_fixture.monitor").Run(); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("private PipeWire-Pulse sink did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	checkSharedDuplex(t, ctx, log, "pipewire", func() { pulseServer.Process.Kill() })
}
