package desktop

import (
	"github.com/godbus/dbus/v5"
	"testing"
)

func TestNotificationActionsRequireMatchingServiceAndCall(t *testing.T) {
	s := &Service{pending: map[uint32]notificationRecord{7: {key: "call-a", actions: []string{"answer", "decline"}}}, origins: map[string]signalOrigin{notifications: {sender: ":1.20", path: notificationPath}}}
	signal := &dbus.Signal{Sender: ":1.20", Path: notificationPath, Name: notifications + ".ActionInvoked", Body: []any{uint32(7), "answer"}}
	if got := s.handleSignal(signal, false); got != (Event{Type: "notification-action", Key: "call-a", Action: "answer"}) {
		t.Fatalf("action: %+v", got)
	}
	signal.Sender = ":1.999"
	if got := s.handleSignal(signal, false); got.Type != "" {
		t.Fatal("accepted action from another bus peer")
	}
	signal.Sender = ":1.20"
	signal.Body = []any{uint32(7), "unexpected"}
	if got := s.handleSignal(signal, false); got.Type != "" {
		t.Fatal("accepted unrequested action")
	}
	signal.Name = notifications + ".NotificationClosed"
	signal.Body = []any{uint32(7), uint32(2)}
	s.handleSignal(signal, false)
	signal.Name = notifications + ".ActionInvoked"
	signal.Body = []any{uint32(7), "answer"}
	if got := s.handleSignal(signal, false); got.Type != "" {
		t.Fatal("accepted expired call notification")
	}
}

func TestSystemSignalsCannotBeSpoofedOnSessionBus(t *testing.T) {
	s := &Service{origins: map[string]signalOrigin{"org.freedesktop.login1.Manager": {sender: ":1.20", path: "/org/freedesktop/login1", system: true}}}
	signal := &dbus.Signal{Sender: ":1.20", Path: "/org/freedesktop/login1", Name: "org.freedesktop.login1.Manager.PrepareForSleep", Body: []any{false}}
	if event := s.handleSignal(signal, false); event.Type != "" {
		t.Fatal("accepted session-bus impersonation of system service")
	}
	if event := s.handleSignal(signal, true); event.Type != "resume" {
		t.Fatalf("valid resume: %+v", event)
	}
}
