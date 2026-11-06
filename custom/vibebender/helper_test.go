package main

import (
	"bufio"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestReconnectDelay(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := reconnectDelay(i + 1); got != w*time.Second {
			t.Errorf("reconnectDelay(%d) = %v, want %v", i+1, got, w*time.Second)
		}
	}
	if got := reconnectDelay(0); got != time.Second {
		t.Errorf("reconnectDelay(0) = %v, want 1s", got)
	}
}

// helperSession runs the fake helper over pipes and returns its event stream.
type helperSession struct {
	t      *testing.T
	in     *io.PipeWriter
	events chan event
	exited chan struct{}
	code   int
}

func startHelper(t *testing.T) *helperSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &helperSession{t: t, in: inW, events: make(chan event, 256), exited: make(chan struct{})}
	go func() {
		h.code = serveHelper(inR, outW, true)
		outW.Close()
		close(h.exited)
	}()
	go func() {
		s := bufio.NewScanner(outR)
		for s.Scan() {
			var e event
			if json.Unmarshal(s.Bytes(), &e) == nil {
				h.events <- e
			}
		}
		close(h.events)
	}()
	// The event encoder is package-global: never let a helper outlive its test.
	t.Cleanup(func() {
		inW.Close()
		go func() {
			for range h.events {
			}
		}()
		<-h.exited
	})
	return h
}

func (h *helperSession) send(c command) {
	h.t.Helper()
	if err := json.NewEncoder(h.in).Encode(c); err != nil {
		h.t.Fatal(err)
	}
}

// waitFor returns the first event matching match, failing after a timeout.
func (h *helperSession) waitFor(desc string, match func(event) bool) event {
	h.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-h.events:
			if !ok {
				h.t.Fatalf("helper exited before %s", desc)
			}
			if match(e) {
				return e
			}
		case <-timeout:
			h.t.Fatalf("timed out waiting for %s", desc)
		}
	}
}

func state(s string) func(event) bool {
	return func(e event) bool { return e.Event == "state" && e.State == s }
}

func (h *helperSession) exitCode() int {
	h.t.Helper()
	select {
	case <-h.exited:
		return h.code
	case <-time.After(10 * time.Second):
		h.t.Fatal("helper did not exit")
		return -1
	}
}

func TestHelperConnectDisconnect(t *testing.T) {
	h := startHelper(t)
	h.send(command{Cmd: "connect", Config: &Config{Server: "vpn.test", Username: "u", Password: "p"}})
	up := h.waitFor("connected event", func(e event) bool { return e.Event == "connected" })
	if up.Device != "ppp0" || len(up.Routes) != 2 {
		t.Errorf("unexpected connected event: %+v", up)
	}
	h.waitFor("connected state", state("connected"))
	h.send(command{Cmd: "disconnect"})
	h.waitFor("disconnected", state("disconnected"))
	if code := h.exitCode(); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

func TestHelperPrompt(t *testing.T) {
	h := startHelper(t)
	h.send(command{Cmd: "connect", Config: &Config{Server: "vpn.test", Username: "otp-user", Password: "p"}})
	h.waitFor("prompt", func(e event) bool { return e.Event == "prompt" })
	h.send(command{Cmd: "respond", Value: "123456"})
	h.waitFor("connected", state("connected"))
}

func TestHelperStdinCloseDisconnects(t *testing.T) {
	h := startHelper(t)
	h.send(command{Cmd: "connect", Config: &Config{Server: "vpn.test", Username: "u", Password: "p"}})
	h.waitFor("connected", state("connected"))
	h.in.Close() // GUI went away
	h.waitFor("disconnected", state("disconnected"))
}

func TestHelperAuthErrorIsNotRetried(t *testing.T) {
	h := startHelper(t)
	h.send(command{Cmd: "connect", Config: &Config{Server: "vpn.test", Username: "u", Password: "wrong", AutoReconnect: true}})
	e := h.waitFor("error", func(e event) bool { return e.Event == "state" && (e.State == "error" || e.State == "reconnecting") })
	if e.State != "error" {
		t.Fatalf("rejected login must not reconnect, got state %q", e.State)
	}
	if code := h.exitCode(); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
}

func TestHelperReconnectsAfterDrop(t *testing.T) {
	t.Setenv("VIBEBENDER_FAKE_DROP", "1")
	h := startHelper(t)
	h.send(command{Cmd: "connect", Config: &Config{Server: "vpn.test", Username: "u", Password: "p", AutoReconnect: true}})
	h.waitFor("first connect", state("connected"))
	r := h.waitFor("reconnecting", state("reconnecting"))
	if r.Retry != 1 || r.Error == "" {
		t.Errorf("unexpected reconnecting event: %+v", r)
	}
	h.waitFor("second connect", state("connected"))
	h.send(command{Cmd: "disconnect"})
	h.waitFor("disconnected", state("disconnected"))
}

func TestHelperDropWithoutAutoReconnectFails(t *testing.T) {
	t.Setenv("VIBEBENDER_FAKE_DROP", "1")
	h := startHelper(t)
	h.send(command{Cmd: "connect", Config: &Config{Server: "vpn.test", Username: "u", Password: "p"}})
	h.waitFor("connected", state("connected"))
	h.waitFor("error", state("error"))
	if code := h.exitCode(); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
}
