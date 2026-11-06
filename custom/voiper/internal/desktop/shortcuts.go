package desktop

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const portalName = "org.freedesktop.portal.Desktop"
const portalPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
const shortcutsInterface = "org.freedesktop.portal.GlobalShortcuts"

type ShortcutBinding struct{ ID, Description, Trigger string }
type ShortcutSettings struct {
	Available, Enabled, Configuring bool
	CanConfigure                    bool
	Bindings                        []ShortcutBinding
	Error                           string
}
type portalShortcut struct {
	ID      string
	Options map[string]dbus.Variant
}
type portalShortcutSession struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	conn    *dbus.Conn
	owner   string
	path    dbus.ObjectPath
	signals chan *dbus.Signal
	held    map[string]bool
}

func shortcutDescription(id string) string {
	switch id {
	case "show-dialer":
		return "Show Voiper dialer"
	case "toggle-mute":
		return "Mute or unmute the only active call"
	}
	return ""
}

func shortcutVersion(ctx context.Context, conn *dbus.Conn) (uint32, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var value dbus.Variant
	if err := conn.Object(portalName, portalPath).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, shortcutsInterface, "version").Store(&value); err != nil {
		return 0, err
	}
	version, ok := value.Value().(uint32)
	if !ok || version < 1 {
		return 0, ErrUnavailable
	}
	return version, nil
}

func (s *Service) probeShortcuts() {
	version, err := shortcutVersion(s.ctx, s.session)
	s.shortcutState = ShortcutSettings{Available: err == nil, CanConfigure: version >= 2, Bindings: []ShortcutBinding{}}
	if err != nil {
		s.shortcutState.Error = "This desktop does not provide the Global Shortcuts portal"
	}
}

func (s *Service) Shortcuts() ShortcutSettings {
	s.shortcutMu.Lock()
	defer s.shortcutMu.Unlock()
	state := s.shortcutState
	state.Bindings = slices.Clone(state.Bindings)
	return state
}

func (s *Service) shortcutsChanged() {
	select {
	case s.shortcutEvents <- Event{Type: "shortcuts-changed"}:
	default:
	}
}

// EnableShortcuts must follow an explicit user action: BindShortcuts may show a
// desktop consent dialog. This session is not restored automatically at startup.
func (s *Service) EnableShortcuts(ctx context.Context) (err error) {
	s.shortcutMu.Lock()
	if s.shortcutSession != nil {
		s.shortcutMu.Unlock()
		return errors.New("shortcuts are already enabled or being configured")
	}
	activeContext, cancel := context.WithCancel(s.ctx)
	active := &portalShortcutSession{ctx: activeContext, cancel: cancel, done: make(chan struct{}), signals: make(chan *dbus.Signal, 32), held: map[string]bool{}}
	s.shortcutSession = active
	s.shortcutState.Configuring, s.shortcutState.Error = true, ""
	s.shortcutMu.Unlock()
	s.shortcutsChanged()
	started := false
	defer func() {
		if !started {
			s.finishShortcuts(active, err)
		}
	}()
	ctx, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	stopCancellation := context.AfterFunc(activeContext, stop)
	defer stopCancellation()
	if err = ctx.Err(); err != nil {
		return err
	}
	active.conn, err = dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	version, err := shortcutVersion(ctx, active.conn)
	if err != nil {
		return fmt.Errorf("Global Shortcuts portal unavailable: %w", err)
	}
	if err = active.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, portalName).Store(&active.owner); err != nil {
		return err
	}
	active.conn.Signal(active.signals)
	if err = active.conn.AddMatchSignalContext(ctx, dbus.WithMatchSender(active.owner), dbus.WithMatchPathNamespace(portalPath)); err != nil {
		return err
	}
	if err = active.conn.AddMatchSignalContext(ctx, dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchObjectPath("/org/freedesktop/DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, portalName)); err != nil {
		return err
	}
	result, err := active.request(ctx, "CreateSession", nil, map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant("voiper_" + rand.Text())})
	if err != nil {
		return err
	}
	// The portal specification deliberately encodes session_handle as a string.
	handle, ok := result["session_handle"].Value().(string)
	path := dbus.ObjectPath(handle)
	prefix := string(portalPath) + "/session/" + strings.ReplaceAll(strings.TrimPrefix(active.conn.Names()[0], ":"), ".", "_") + "/"
	if !ok || !path.IsValid() || !strings.HasPrefix(handle, prefix) {
		return errors.New("portal returned an invalid shortcut session")
	}
	active.path = path
	requested := []portalShortcut{}
	for _, id := range []string{"show-dialer", "toggle-mute"} {
		requested = append(requested, portalShortcut{ID: id, Options: map[string]dbus.Variant{"description": dbus.MakeVariant(shortcutDescription(id))}})
	}
	result, err = active.request(ctx, "BindShortcuts", []any{active.path, requested, ""}, map[string]dbus.Variant{})
	if err != nil {
		return err
	}
	bindings, err := shortcutBindings(result["shortcuts"])
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return errors.New("no global shortcuts were approved")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	s.shortcutMu.Lock()
	s.shortcutState = ShortcutSettings{Available: true, Enabled: true, CanConfigure: version >= 2, Bindings: bindings}
	s.shortcutMu.Unlock()
	started = true
	go s.runShortcuts(active)
	s.shortcutsChanged()
	return nil
}

func (active *portalShortcutSession) request(ctx context.Context, method string, args []any, options map[string]dbus.Variant) (map[string]dbus.Variant, error) {
	options["handle_token"] = dbus.MakeVariant("voiper_" + rand.Text())
	var handle dbus.ObjectPath
	if err := active.conn.Object(portalName, portalPath).CallWithContext(ctx, shortcutsInterface+"."+method, 0, append(args, options)...).Store(&handle); err != nil {
		return nil, err
	}
	if !handle.IsValid() || !strings.HasPrefix(string(handle), string(portalPath)+"/request/") {
		return nil, errors.New("portal returned an invalid request handle")
	}
	for {
		select {
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			active.conn.Object(active.owner, handle).CallWithContext(cleanup, "org.freedesktop.portal.Request.Close", 0)
			cancel()
			return nil, ctx.Err()
		case signal := <-active.signals:
			if signal == nil {
				return nil, errors.New("portal connection closed")
			}
			if signal.Sender != active.owner || signal.Path != handle || signal.Name != "org.freedesktop.portal.Request.Response" || len(signal.Body) != 2 {
				continue
			}
			code, valid := signal.Body[0].(uint32)
			result, ok := signal.Body[1].(map[string]dbus.Variant)
			if !valid || !ok {
				return nil, errors.New("invalid portal response")
			}
			if code == 1 {
				return nil, errors.New("shortcut setup was cancelled")
			}
			if code != 0 {
				return nil, errors.New("desktop declined shortcut setup")
			}
			return result, nil
		}
	}
}

func shortcutBindings(value dbus.Variant) ([]ShortcutBinding, error) {
	var shortcuts []portalShortcut
	if err := dbus.Store([]any{value.Value()}, &shortcuts); err != nil {
		return nil, errors.New("invalid shortcut bindings from portal")
	}
	if len(shortcuts) > 16 {
		return nil, errors.New("too many shortcut bindings from portal")
	}
	bindings := []ShortcutBinding{}
	seen := map[string]bool{}
	for _, shortcut := range shortcuts {
		description := shortcutDescription(shortcut.ID)
		if description == "" || seen[shortcut.ID] {
			continue
		}
		trigger, _ := shortcut.Options["trigger_description"].Value().(string)
		if len(trigger) > 256 {
			return nil, errors.New("shortcut description is too long")
		}
		bindings = append(bindings, ShortcutBinding{ID: shortcut.ID, Description: description, Trigger: trigger})
		seen[shortcut.ID] = true
	}
	return bindings, nil
}

func (s *Service) runShortcuts(active *portalShortcutSession) {
	var failure error
	defer func() { s.finishShortcuts(active, failure) }()
	for {
		select {
		case <-active.ctx.Done():
			return
		case signal := <-active.signals:
			if signal == nil {
				failure = errors.New("portal connection closed")
				return
			}
			if signal.Sender == "org.freedesktop.DBus" && signal.Path == "/org/freedesktop/DBus" && signal.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(signal.Body) == 3 && signal.Body[0] == portalName && signal.Body[1] == active.owner {
				failure = errors.New("shortcut portal restarted; enable shortcuts again")
				return
			}
			if signal.Sender == active.owner && signal.Path == active.path && signal.Name == "org.freedesktop.portal.Session.Closed" {
				return
			}
			if event := s.shortcutSignal(active, signal); event.Type != "" {
				select {
				case s.shortcutEvents <- event:
				default:
				}
			}
		}
	}
}
func (s *Service) shortcutSignal(active *portalShortcutSession, signal *dbus.Signal) Event {
	if signal.Sender != active.owner || signal.Path != portalPath || len(signal.Body) < 2 || signal.Body[0] != active.path {
		return Event{}
	}
	s.shortcutMu.Lock()
	defer s.shortcutMu.Unlock()
	if s.shortcutSession != active || !s.shortcutState.Enabled {
		return Event{}
	}
	if signal.Name == shortcutsInterface+".ShortcutsChanged" && len(signal.Body) == 2 {
		if bindings, err := shortcutBindings(dbus.MakeVariant(signal.Body[1])); err == nil {
			s.shortcutState.Bindings = bindings
			clear(active.held)
			return Event{Type: "shortcuts-changed"}
		}
		return Event{}
	}
	if len(signal.Body) != 4 {
		return Event{}
	}
	id, ok := signal.Body[1].(string)
	if !ok || !slices.ContainsFunc(s.shortcutState.Bindings, func(binding ShortcutBinding) bool { return binding.ID == id }) {
		return Event{}
	}
	switch signal.Name {
	case shortcutsInterface + ".Deactivated":
		delete(active.held, id)
	case shortcutsInterface + ".Activated":
		if !active.held[id] {
			active.held[id] = true
			return Event{Type: "global-shortcut", Action: id}
		}
	}
	return Event{}
}

func (s *Service) finishShortcuts(active *portalShortcutSession, failure error) {
	active.cancel()
	if active.conn != nil {
		if active.path != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			active.conn.Object(active.owner, active.path).CallWithContext(ctx, "org.freedesktop.portal.Session.Close", 0)
			cancel()
		}
		active.conn.Close()
	}
	s.shortcutMu.Lock()
	if s.shortcutSession == active {
		s.shortcutSession = nil
		s.shortcutState.Enabled, s.shortcutState.Configuring = false, false
		s.shortcutState.Bindings = []ShortcutBinding{}
		if failure != nil {
			s.shortcutState.Error = failure.Error()
		}
	}
	s.shortcutMu.Unlock()
	close(active.done)
	s.shortcutsChanged()
}

func (s *Service) DisableShortcuts() error {
	s.shortcutMu.Lock()
	active := s.shortcutSession
	if active != nil {
		active.cancel()
	}
	s.shortcutMu.Unlock()
	if active == nil {
		return nil
	}
	select {
	case <-active.done:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("shortcut portal is still closing")
	}
}

func (s *Service) ConfigureShortcuts(ctx context.Context) error {
	s.shortcutMu.Lock()
	active, state := s.shortcutSession, s.shortcutState
	s.shortcutMu.Unlock()
	if active == nil || !state.Enabled {
		return errors.New("enable shortcuts first")
	}
	if !state.CanConfigure {
		return errors.New("this portal cannot reconfigure shortcuts; disable and enable them again")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return active.conn.Object(active.owner, portalPath).CallWithContext(ctx, shortcutsInterface+".ConfigureShortcuts", 0, active.path, "", map[string]dbus.Variant{}).Err
}
