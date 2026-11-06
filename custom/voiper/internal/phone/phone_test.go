package phone

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/internal/store"
	"github.com/megakuul/voiper/pkg/media"
	"github.com/megakuul/voiper/pkg/sip"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "phone.sqlite"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(context.Background(), database, nil)
	t.Cleanup(func() { manager.Close(); database.Close() })
	return manager
}

func testAccount(t *testing.T, m *Manager, name string) *account {
	t.Helper()
	ctx, cancel := context.WithCancel(m.ctx)
	client, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: name, LocalAddress: "127.0.0.1:0"}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); client.Close() })
	a := &account{ctx: ctx, client: client, cancel: cancel, state: AccountState{Name: name, State: "registered"}, config: config.Config{Server: "127.0.0.1", Username: name, LocalAddress: "127.0.0.1:0"}}
	m.mu.Lock()
	m.accounts[name] = a
	m.mu.Unlock()
	return a
}

func TestIgnoreEventsFromReplacedAccount(t *testing.T) {
	m := testManager(t)
	old := testAccount(t, m, "office")
	current := testAccount(t, m, "office")
	m.handle(event{account: "office", owner: old, sip: sip.Event{Type: "registration", State: "failed", Message: "old connection timed out"}})
	snapshot := m.Snapshot()
	if len(snapshot.Accounts) != 1 || snapshot.Accounts[0].State != "registered" || snapshot.Accounts[0].Error != "" {
		t.Fatalf("stale registration changed replacement: %+v", snapshot.Accounts)
	}
	m.handle(event{account: "office", owner: current, sip: sip.Event{Type: "registration", State: "failed", Message: "current error"}})
	if m.Snapshot().Accounts[0].Error != "current error" {
		t.Fatal("current account event ignored")
	}
}

func TestCallEventsCannotCrossAccounts(t *testing.T) {
	for _, kind := range []string{"ringing", "connected", "ended"} {
		t.Run(kind, func(t *testing.T) {
			m := testManager(t)
			first := testAccount(t, m, "first")
			second := testAccount(t, m, "second")
			m.mu.Lock()
			m.calls["shared-call-id"] = &call{state: CallState{ID: "shared-call-id", Account: "first", Direction: "outgoing", State: "calling", Started: time.Now()}, owner: first}
			m.mu.Unlock()
			m.handle(event{account: "second", owner: second, sip: sip.Event{Type: kind, CallID: "shared-call-id"}})
			snapshot := m.Snapshot()
			if len(snapshot.Calls) != 1 || snapshot.Calls[0].State != "calling" {
				t.Fatalf("%s crossed account boundary: %+v", kind, snapshot.Calls)
			}
		})
	}
}

func TestReplacingAccountClearsPresence(t *testing.T) {
	m := testManager(t)
	old := testAccount(t, m, "office")
	m.mu.Lock()
	m.presence["office\x00sip:bob@example.org"] = Presence{Account: "office", Remote: "sip:bob@example.org", State: "available"}
	m.mu.Unlock()
	if err := m.Enable("office", old.config); err != nil {
		t.Fatal(err)
	}
	if presence := m.Snapshot().Presence; len(presence) != 0 {
		t.Fatalf("presence from replaced account survived: %+v", presence)
	}
}

func TestDisableCancelsPendingRegistration(t *testing.T) {
	m := testManager(t)
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg := config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0"}
	if err := m.Enable("office", cfg); err != nil {
		t.Fatal(err)
	}
	listener.SetReadDeadline(time.Now().Add(3 * time.Second))
	packet := make([]byte, 4096)
	if _, _, err := listener.ReadFrom(packet); err != nil {
		t.Fatalf("registration never started: %v", err)
	}
	done := make(chan struct{})
	go func() { m.Disable("office"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disable blocked waiting for registrar")
	}
	if snapshot := m.Snapshot(); len(snapshot.Accounts) != 0 || len(snapshot.Calls) != 0 {
		t.Fatalf("disabled account retained: %+v", snapshot)
	}
}

func TestDuplicateEndedEventsWriteHistoryOnce(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	m.mu.Lock()
	m.calls["call-id"] = &call{state: CallState{ID: "call-id", Account: "office", Remote: "sip:bob@example.org", Direction: "outgoing", State: "connected", Started: time.Now(), Connected: time.Now()}, owner: owner}
	m.mu.Unlock()
	ended := event{account: "office", owner: owner, sip: sip.Event{Type: "ended", CallID: "call-id", Message: "remote hangup"}}
	m.handle(ended)
	m.handle(ended)
	history, err := m.store.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != "remote hangup" {
		t.Fatalf("unexpected history after duplicate event: %+v", history)
	}
}

func TestInvalidActivationDoesNotHoldActiveCall(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	stream, err := media.NewCall(m.ctx, "active", media.Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMU"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.calls["active"] = &call{state: CallState{ID: "active", Account: "office", Direction: "outgoing", State: "connected", Started: time.Now()}, owner: owner, media: stream}
	m.mu.Unlock()
	// This media session has no SIP dialog. Attempting to hold it would fail with
	// "hold existing call", exposing validation performed after a side effect.
	if err := m.Answer("missing"); err == nil || strings.Contains(err.Error(), "hold existing call") {
		t.Fatalf("stale Answer attempted to hold an active call: %v", err)
	}
	if err := m.Dial("office", "sip:bad\r\nheader@example.org"); err == nil || strings.Contains(err.Error(), "hold existing call") {
		t.Fatalf("invalid Dial attempted to hold an active call: %v", err)
	}
}

func TestPendingCallPreventsAnotherActivation(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	m.mu.Lock()
	m.calls["pending"] = &call{state: CallState{ID: "pending", Account: "office", Direction: "outgoing", State: "calling", Started: time.Now()}, owner: owner}
	m.mu.Unlock()
	if err := m.Dial("office", "bob"); err == nil {
		t.Fatal("allowed another call while the first is still connecting")
	}
	if len(m.Snapshot().Calls) != 1 {
		t.Fatal("pending activation created another media session")
	}
}

func TestGlobalMuteSelectsOnlyUnambiguousActiveCall(t *testing.T) {
	m := testManager(t)
	if err := m.ToggleActiveMute(); err == nil {
		t.Fatal("no-call shortcut reported success")
	}
	m.mu.Lock()
	active := &call{state: CallState{ID: "active", State: "connected"}}
	held := &call{state: CallState{ID: "held", State: "connected", Held: true}}
	m.calls["active"], m.calls["held"] = active, held
	m.mu.Unlock()
	if err := m.ToggleActiveMute(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	if !active.state.Muted || held.state.Muted {
		t.Error("shortcut did not isolate active call")
	}
	held.state.Held = false
	m.mu.Unlock()
	if err := m.ToggleActiveMute(); err == nil {
		t.Fatal("ambiguous shortcut changed a call")
	}
	m.mu.Lock()
	if !active.state.Muted || held.state.Muted {
		t.Error("ambiguous shortcut changed mute state")
	}
	m.calls = map[string]*call{}
	m.mu.Unlock()
}

func TestAnsweredElsewhereIsNotSavedAsMissed(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	m.mu.Lock()
	m.calls["elsewhere"] = &call{state: CallState{ID: "elsewhere", Account: "office", Remote: "sip:bob@example.org", Direction: "incoming", State: "ringing", Started: time.Now()}, owner: owner}
	m.mu.Unlock()
	m.handle(event{account: "office", owner: owner, sip: sip.Event{Type: "ended", CallID: "elsewhere", Message: "call canceled", TerminationReason: "answered-elsewhere"}})
	history, err := m.store.History()
	if err != nil || len(history) != 1 || history[0].Status != "answered elsewhere" {
		t.Fatalf("history: %+v %v", history, err)
	}
}
