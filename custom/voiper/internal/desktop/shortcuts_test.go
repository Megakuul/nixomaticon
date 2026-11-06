package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type shortcutPortalMock struct {
	conn            *dbus.Conn
	waitForApproval bool
	closed          chan string
}
type portalCloseMock struct {
	kind   string
	closed chan string
}

func (p *portalCloseMock) Close() *dbus.Error {
	p.closed <- p.kind
	return nil
}
func (p *shortcutPortalMock) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != shortcutsInterface || property != "version" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unknown property"))
	}
	return dbus.MakeVariant(uint32(2)), nil
}
func portalMockPath(sender dbus.Sender, kind string, token dbus.Variant) dbus.ObjectPath {
	return dbus.ObjectPath(string(portalPath) + "/" + kind + "/" + strings.ReplaceAll(strings.TrimPrefix(string(sender), ":"), ".", "_") + "/" + token.Value().(string))
}
func (p *shortcutPortalMock) CreateSession(sender dbus.Sender, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	request := portalMockPath(sender, "request", options["handle_token"])
	session := portalMockPath(sender, "session", options["session_handle_token"])
	p.conn.Export(&portalCloseMock{kind: "session", closed: p.closed}, session, "org.freedesktop.portal.Session")
	// Emit before returning the method reply to exercise response subscription order.
	p.conn.Emit(request, "org.freedesktop.portal.Request.Response", uint32(0), map[string]dbus.Variant{"session_handle": dbus.MakeVariant(string(session))})
	return request, nil
}
func (p *shortcutPortalMock) BindShortcuts(sender dbus.Sender, session dbus.ObjectPath, shortcuts []portalShortcut, parent string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	request := portalMockPath(sender, "request", options["handle_token"])
	p.conn.Export(&portalCloseMock{kind: "request", closed: p.closed}, request, "org.freedesktop.portal.Request")
	if !p.waitForApproval {
		for i := range shortcuts {
			shortcuts[i].Options["trigger_description"] = dbus.MakeVariant("Configured test key")
		}
		p.conn.Emit(request, "org.freedesktop.portal.Request.Response", uint32(0), map[string]dbus.Variant{"shortcuts": dbus.MakeVariant(shortcuts)})
	}
	return request, nil
}

func newShortcutPortal(t *testing.T, waiting bool) (*Service, *shortcutPortalMock) {
	t.Helper()
	address := privateSessionBus(t)
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	portal := &shortcutPortalMock{conn: conn, waitForApproval: waiting, closed: make(chan string, 8)}
	if err = conn.Export(portal, portalPath, shortcutsInterface); err != nil {
		t.Fatal(err)
	}
	if err = conn.Export(portal, portalPath, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.RequestName(portalName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{ctx: ctx, shortcutEvents: make(chan Event, 16)}
	t.Cleanup(func() { cancel(); s.DisableShortcuts() })
	return s, portal
}

func TestShortcutSessionConsentAndCleanup(t *testing.T) {
	s, portal := newShortcutPortal(t, false)
	if err := s.EnableShortcuts(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := s.Shortcuts()
	if !state.Enabled || !state.Available || !state.CanConfigure || len(state.Bindings) != 2 || state.Bindings[0].Trigger != "Configured test key" {
		t.Fatalf("bindings: %+v", state)
	}
	s.shortcutMu.Lock()
	active := s.shortcutSession
	s.shortcutMu.Unlock()
	if err := portal.conn.Emit(portalPath, shortcutsInterface+".Activated", active.path, "show-dialer", uint64(1), map[string]dbus.Variant{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-s.shortcutEvents:
			if event.Type == "global-shortcut" {
				if event.Action != "show-dialer" {
					t.Fatalf("wrong action: %+v", event)
				}
				if err := s.DisableShortcuts(); err != nil {
					t.Fatal(err)
				}
				if s.Shortcuts().Enabled {
					t.Fatal("shortcuts survived disable")
				}
				select {
				case kind := <-portal.closed:
					if kind != "session" {
						t.Fatal(kind)
					}
				case <-time.After(time.Second):
					t.Fatal("portal session not closed")
				}
				return
			}
		case <-deadline:
			t.Fatal("approved shortcut was not delivered")
		}
	}
}

func TestShortcutConsentDeadlineClosesRequestAndSession(t *testing.T) {
	s, portal := newShortcutPortal(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := s.EnableShortcuts(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	state := s.Shortcuts()
	if state.Enabled || state.Configuring {
		t.Fatalf("canceled setup remains active: %+v", state)
	}
	closed := map[string]bool{}
	for len(closed) < 2 {
		select {
		case kind := <-portal.closed:
			closed[kind] = true
		case <-time.After(time.Second):
			t.Fatalf("missing cleanup: %v", closed)
		}
	}
}

func TestPortalRevocationClearsShortcutBindings(t *testing.T) {
	s, portal := newShortcutPortal(t, false)
	if err := s.EnableShortcuts(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.shortcutMu.Lock()
	active := s.shortcutSession
	s.shortcutMu.Unlock()
	if err := portal.conn.Emit(active.path, "org.freedesktop.portal.Session.Closed", map[string]dbus.Variant{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.done:
	case <-time.After(time.Second):
		t.Fatal("revoked session remains active")
	}
	if state := s.Shortcuts(); state.Enabled || len(state.Bindings) != 0 {
		t.Fatalf("stale bindings: %+v", state)
	}
}

func TestShortcutSignalsRequireOwnerSessionAndApprovedID(t *testing.T) {
	active := &portalShortcutSession{owner: ":1.20", path: "/org/freedesktop/portal/desktop/session/1_3/test", held: map[string]bool{}}
	s := &Service{shortcutSession: active, shortcutState: ShortcutSettings{Enabled: true, Bindings: []ShortcutBinding{{ID: "toggle-mute"}}}}
	signal := &dbus.Signal{Sender: active.owner, Path: portalPath, Name: shortcutsInterface + ".Activated", Body: []any{active.path, "toggle-mute", uint64(1), map[string]dbus.Variant{}}}
	signal.Sender = ":1.99"
	if event := s.shortcutSignal(active, signal); event.Type != "" {
		t.Fatal("accepted forged portal sender")
	}
	signal.Sender = active.owner
	signal.Body[0] = dbus.ObjectPath("/other/session")
	if event := s.shortcutSignal(active, signal); event.Type != "" {
		t.Fatal("accepted another session")
	}
	signal.Body[0] = active.path
	signal.Body[1] = "hangup"
	if event := s.shortcutSignal(active, signal); event.Type != "" {
		t.Fatal("accepted unrequested action")
	}
	signal.Body[1] = "toggle-mute"
	if event := s.shortcutSignal(active, signal); event.Action != "toggle-mute" {
		t.Fatal("approved action rejected")
	}
	if event := s.shortcutSignal(active, signal); event.Type != "" {
		t.Fatal("key repeat toggled mute twice")
	}
	signal.Name = shortcutsInterface + ".Deactivated"
	s.shortcutSignal(active, signal)
	signal.Name = shortcutsInterface + ".Activated"
	if event := s.shortcutSignal(active, signal); event.Action != "toggle-mute" {
		t.Fatal("next key press rejected")
	}
}
