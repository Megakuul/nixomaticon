package desktop

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func privateSessionBus(t *testing.T) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Fatal("dbus-daemon is required for isolated desktop tests")
	}
	// Do not depend on host session configuration or activate real wallet services.
	config := filepath.Join(t.TempDir(), "bus.conf")
	if err = os.WriteFile(config, []byte(`<busconfig>
  <type>session</type>
  <listen>unix:tmpdir=/tmp</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*"/>
    <allow receive_sender="*"/>
    <allow own="*"/>
  </policy>
</busconfig>`), 0600); err != nil {
		t.Fatal(err)
	}
	ctxDaemon, stopDaemon := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(stopDaemon)
	cmd := exec.CommandContext(ctxDaemon, daemon, "--config-file="+config, "--nofork", "--print-address=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		cmd.Wait()
		t.Fatalf("private bus did not start: %s (stdout: %v)", stderr.String(), scanner.Err())
	}
	address := scanner.Text()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	return address
}
