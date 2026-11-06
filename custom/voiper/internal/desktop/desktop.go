package desktop

import (
	"context"
	"errors"
	"html"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

var ErrUnavailable = errors.New("desktop service unavailable")
var ErrSecretNotFound = errors.New("account password not found in Secret Service")

const notifications = "org.freedesktop.Notifications"
const notificationPath = dbus.ObjectPath("/org/freedesktop/Notifications")

type Capabilities struct {
	Tray                bool
	Notifications       bool
	NotificationActions bool
	SuspendMonitor      bool
	NetworkMonitor      bool
	Secrets             bool
	Warnings            []string
}
type Event struct{ Type, Key, Action string }
type Action struct{ Key, Label string }
type Notification struct {
	Key, Title, Body string
	Actions          []Action
	Urgent           bool
	Timeout          time.Duration
}
type notificationRecord struct {
	key     string
	actions []string
}

type signalOrigin struct {
	sender string
	path   dbus.ObjectPath
	system bool
}

type Service struct {
	ctx             context.Context
	cancel          context.CancelFunc
	session, system *dbus.Conn
	capabilities    Capabilities
	markup          bool
	signals         chan *dbus.Signal
	systemSignals   chan *dbus.Signal
	events          chan Event
	trayActions     chan Event
	shortcutEvents  chan Event
	shortcutMu      sync.Mutex
	shortcutState   ShortcutSettings
	shortcutSession *portalShortcutSession
	trayMu          sync.Mutex
	stopTray        func()
	done            chan struct{}
	mu              sync.Mutex
	pending         map[uint32]notificationRecord
	origins         map[string]signalOrigin
	originMu        sync.Mutex
	serviceNames    map[string]string
	notifyMu        sync.Mutex
}

func New(ctx context.Context) *Service {
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{ctx: ctx, cancel: cancel, signals: make(chan *dbus.Signal, 64), systemSignals: make(chan *dbus.Signal, 64), events: make(chan Event, 64), trayActions: make(chan Event, 8), shortcutEvents: make(chan Event, 8), done: make(chan struct{}), pending: make(map[uint32]notificationRecord), origins: make(map[string]signalOrigin), serviceNames: make(map[string]string)}
	var err error
	s.session, err = dbus.ConnectSessionBus()
	if err != nil {
		s.capabilities.Warnings = append(s.capabilities.Warnings, "Session bus unavailable; notifications and wallet disabled")
	} else {
		s.session.Signal(s.signals)
		probe, stop := context.WithTimeout(ctx, 3*time.Second)
		var caps []string
		err = s.session.Object(notifications, notificationPath).CallWithContext(probe, notifications+".GetCapabilities", 0).Store(&caps)
		stop()
		if err == nil && s.watch(s.session, notifications, notificationPath, notifications, "") == nil {
			s.capabilities.Notifications = true
			s.capabilities.NotificationActions = slices.Contains(caps, "actions")
			s.markup = slices.Contains(caps, "body-markup")
		} else {
			s.capabilities.Warnings = append(s.capabilities.Warnings, "Desktop notifications unavailable")
		}
		s.capabilities.Secrets = secretServiceBuilt && s.hasOwner(s.session, "org.freedesktop.secrets")
		s.probeShortcuts()
	}
	s.system, err = dbus.ConnectSystemBus()
	if err == nil {
		s.system.Signal(s.systemSignals)
		if s.hasOwner(s.system, "org.freedesktop.login1") {
			s.capabilities.SuspendMonitor = s.watch(s.system, "org.freedesktop.login1", "/org/freedesktop/login1", "org.freedesktop.login1.Manager", "PrepareForSleep") == nil
		}
		if s.hasOwner(s.system, "org.freedesktop.NetworkManager") {
			s.capabilities.NetworkMonitor = s.watch(s.system, "org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager", "org.freedesktop.NetworkManager", "StateChanged") == nil
		}
		if !s.capabilities.NetworkMonitor && s.hasOwner(s.system, "org.freedesktop.network1") {
			s.capabilities.NetworkMonitor = s.watch(s.system, "org.freedesktop.network1", "/org/freedesktop/network1", "org.freedesktop.DBus.Properties", "PropertiesChanged") == nil
		}
	}
	if !s.capabilities.NetworkMonitor {
		s.capabilities.Warnings = append(s.capabilities.Warnings, "NetworkManager/systemd-networkd monitoring unavailable; SIP retry and suspend recovery remain active")
	}
	go s.run()
	return s
}

func (s *Service) hasOwner(conn *dbus.Conn, name string) bool {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	var owner bool
	return conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, name).Store(&owner) == nil && owner
}
func (s *Service) watch(conn *dbus.Conn, sender string, path dbus.ObjectPath, iface, member string) error {
	options := []dbus.MatchOption{dbus.WithMatchSender(sender), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(iface)}
	if member != "" {
		options = append(options, dbus.WithMatchMember(member))
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, sender).Store(&owner); err != nil {
		return err
	}
	if err := conn.AddMatchSignalContext(ctx, options...); err != nil {
		return err
	}
	s.originMu.Lock()
	s.origins[iface] = signalOrigin{sender: owner, path: path, system: conn == s.system}
	s.serviceNames[sender] = iface
	s.originMu.Unlock()
	if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchObjectPath("/org/freedesktop/DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, sender)); err != nil {
		return err
	}
	return nil
}
func (s *Service) Capabilities() Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.capabilities
	c.Warnings = slices.Clone(c.Warnings)
	return c
}
func (s *Service) Events() <-chan Event { return s.events }
func (s *Service) Close() error         { s.cancel(); <-s.done; return nil }

func (s *Service) run() {
	defer close(s.done)
	defer close(s.events)
	defer s.closeTray()
	defer s.DisableShortcuts()
	defer func() {
		if s.session != nil {
			s.clearNotifications()
			s.session.Close()
		}
		if s.system != nil {
			s.system.Close()
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case event := <-s.trayActions:
			select {
			case s.events <- event:
			default:
			}
		case event := <-s.shortcutEvents:
			select {
			case s.events <- event:
			default:
			}
		case signal := <-s.systemSignals:
			if signal == nil {
				continue
			}
			if event := s.handleSignal(signal, true); event.Type != "" {
				select {
				case s.events <- event:
				default:
				}
			}
		case signal := <-s.signals:
			if signal == nil {
				continue
			}
			event := s.handleSignal(signal, false)
			if event.Type != "" {
				select {
				case s.events <- event:
				default:
				}
			}
		}
	}
}
func (s *Service) handleSignal(signal *dbus.Signal, system bool) Event {
	s.originMu.Lock()
	defer s.originMu.Unlock()
	if signal.Name == "org.freedesktop.DBus.NameOwnerChanged" && signal.Sender == "org.freedesktop.DBus" && signal.Path == "/org/freedesktop/DBus" && len(signal.Body) == 3 {
		name, nameOK := signal.Body[0].(string)
		owner, ownerOK := signal.Body[2].(string)
		if iface, exists := s.serviceNames[name]; exists && nameOK && ownerOK {
			origin := s.origins[iface]
			if origin.system != system {
				return Event{}
			}
			origin.sender = owner
			s.origins[iface] = origin
			if name == "org.kde.StatusNotifierWatcher" {
				s.mu.Lock()
				s.capabilities.Tray = false
				s.mu.Unlock()
				return Event{Type: "tray-action", Action: "show"}
			}
			if name == notifications {
				s.mu.Lock()
				clear(s.pending)
				s.mu.Unlock()
			}
		}
		return Event{}
	}
	separator := strings.LastIndexByte(signal.Name, '.')
	if separator < 0 {
		return Event{}
	}
	origin, exists := s.origins[signal.Name[:separator]]
	if !exists || origin.system != system || signal.Sender != origin.sender || signal.Path != origin.path {
		return Event{}
	}
	switch signal.Name {
	case "org.kde.StatusNotifierWatcher.StatusNotifierHostUnregistered":
		s.mu.Lock()
		s.capabilities.Tray = false
		s.mu.Unlock()
		return Event{Type: "tray-action", Action: "show"}
	case notifications + ".ActionInvoked":
		if len(signal.Body) != 2 {
			return Event{}
		}
		id, ok := signal.Body[0].(uint32)
		action, valid := signal.Body[1].(string)
		if !ok || !valid {
			return Event{}
		}
		s.mu.Lock()
		record, exists := s.pending[id]
		s.mu.Unlock()
		if exists && slices.Contains(record.actions, action) {
			return Event{Type: "notification-action", Key: record.key, Action: action}
		}
	case notifications + ".NotificationClosed":
		if len(signal.Body) == 2 {
			if id, ok := signal.Body[0].(uint32); ok {
				s.mu.Lock()
				delete(s.pending, id)
				s.mu.Unlock()
			}
		}
	case "org.freedesktop.login1.Manager.PrepareForSleep":
		if len(signal.Body) == 1 {
			if sleeping, ok := signal.Body[0].(bool); ok {
				if sleeping {
					return Event{Type: "suspend"}
				}
				return Event{Type: "resume"}
			}
		}
	case "org.freedesktop.DBus.Properties.PropertiesChanged":
		if len(signal.Body) != 3 || signal.Body[0] != "org.freedesktop.network1.Manager" {
			return Event{}
		}
		changed, ok := signal.Body[1].(map[string]dbus.Variant)
		if !ok {
			return Event{}
		}
		state, ok := changed["OperationalState"].Value().(string)
		if !ok {
			return Event{}
		}
		switch state {
		case "routable", "degraded":
			return Event{Type: "network-online"}
		case "off", "no-carrier", "dormant", "carrier", "degraded-carrier":
			return Event{Type: "network-offline"}
		}
	case "org.freedesktop.NetworkManager.StateChanged":
		if len(signal.Body) == 1 {
			if state, ok := signal.Body[0].(uint32); ok {
				if state >= 50 {
					return Event{Type: "network-online"}
				}
				return Event{Type: "network-offline"}
			}
		}
	}
	return Event{}
}

func (s *Service) Notify(ctx context.Context, n Notification) error {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if !s.capabilities.Notifications {
		return ErrUnavailable
	}
	if n.Key == "" || len(n.Key) > 512 || len(n.Title) > 512 || len(n.Body) > 4096 || len(n.Actions) > 8 {
		return errors.New("invalid notification")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	actions := []string{}
	keys := []string{}
	if s.capabilities.NotificationActions {
		for _, a := range n.Actions {
			if a.Key == "" || strings.ContainsRune(a.Key, 0) || len(a.Key) > 64 || len(a.Label) > 128 {
				return errors.New("invalid notification action")
			}
			actions = append(actions, a.Key, a.Label)
			keys = append(keys, a.Key)
		}
	}
	body := n.Body
	if s.markup {
		body = html.EscapeString(body)
	}
	urgency := byte(1)
	if n.Urgent {
		urgency = 2
	}
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency), "desktop-entry": dbus.MakeVariant("voiper"), "suppress-sound": dbus.MakeVariant(true)}
	timeout := int32(-1)
	if n.Timeout > 0 {
		timeout = int32(min(n.Timeout.Milliseconds(), int64(2147483647)))
	} else if n.Urgent {
		timeout = 0
	}
	s.mu.Lock()
	var replaces uint32
	for id, record := range s.pending {
		if record.key == n.Key {
			replaces = id
			break
		}
	}
	if replaces == 0 && len(s.pending) >= 128 {
		s.mu.Unlock()
		return errors.New("too many desktop notifications")
	}
	s.mu.Unlock()
	var id uint32
	err := s.session.Object(notifications, notificationPath).CallWithContext(ctx, notifications+".Notify", 0, "Voiper", replaces, "voiper", n.Title, body, actions, hints, timeout).Store(&id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if replaces != 0 && replaces != id {
		delete(s.pending, replaces)
	}
	s.pending[id] = notificationRecord{key: n.Key, actions: keys}
	s.mu.Unlock()
	return nil
}
func (s *Service) Dismiss(ctx context.Context, key string) error {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if !s.capabilities.Notifications {
		return ErrUnavailable
	}
	s.mu.Lock()
	var id uint32
	for candidate, record := range s.pending {
		if record.key == key {
			id = candidate
			delete(s.pending, candidate)
			break
		}
	}
	s.mu.Unlock()
	if id == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.session.Object(notifications, notificationPath).CallWithContext(ctx, notifications+".CloseNotification", 0, id).Err
}

func (s *Service) SetSecret(ctx context.Context, account, password string) error {
	if !s.capabilities.Secrets {
		return ErrUnavailable
	}
	_, err := secretOperation(ctx, "set", account, password)
	return err
}
func (s *Service) GetSecret(ctx context.Context, account string) (string, error) {
	if !s.capabilities.Secrets {
		return "", ErrUnavailable
	}
	return secretOperation(ctx, "get", account, "")
}
func (s *Service) DeleteSecret(ctx context.Context, account string) error {
	if !s.capabilities.Secrets {
		return ErrUnavailable
	}
	_, err := secretOperation(ctx, "delete", account, "")
	return err
}

func (s *Service) clearNotifications() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	s.mu.Lock()
	ids := make([]uint32, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	clear(s.pending)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.session.Object(notifications, notificationPath).CallWithContext(ctx, notifications+".CloseNotification", 0, id)
	}
}
