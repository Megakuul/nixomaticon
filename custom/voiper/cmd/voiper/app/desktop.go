package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	assets "github.com/megakuul/voiper/build/package"
	"github.com/megakuul/voiper/internal/desktop"
	"github.com/megakuul/voiper/internal/phone"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type appEvent struct {
	name  string
	value any
}

func (a *App) emitPhoneEvent(name string, value any) {
	runtime.EventsEmit(a.ctx, name, value)
	select {
	case a.desktopEvents <- appEvent{name, value}:
	default:
	}
}
func (a *App) runDesktop() {
	defer a.workers.Done()
	select {
	case <-a.initialized:
	case <-a.ctx.Done():
		return
	}
	service := desktop.New(a.ctx)
	defer service.Close()
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return
	}
	a.desktop = service
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	service.StartTray(ctx, assets.Icon)
	cancel()
	runtime.EventsEmit(a.ctx, "desktop-ready", nil)
	a.directory.Refresh()
	a.startWork(a.enableStartupAccounts)
	a.startWork(a.watchScreenLock)
	notifications := map[string]bool{}
	for {
		select {
		case <-a.ctx.Done():
			return
		case event, ok := <-service.Events():
			if !ok {
				return
			}
			switch event.Type {
			case "shortcuts-changed":
				runtime.EventsEmit(a.ctx, "shortcuts-changed", nil)
			case "global-shortcut":
				if event.Action == "show-dialer" {
					runtime.WindowShow(a.ctx)
					runtime.WindowUnminimise(a.ctx)
					runtime.EventsEmit(a.ctx, "show-dialer", nil)
				} else if event.Action == "toggle-mute" {
					if err := a.phone.ToggleActiveMute(); err != nil {
						runtime.WindowShow(a.ctx)
						runtime.EventsEmit(a.ctx, "show-dialer", nil)
						runtime.EventsEmit(a.ctx, "desktop-warning", err.Error())
					}
				}
			case "tray-action":
				if event.Action == "show" {
					runtime.WindowShow(a.ctx)
					runtime.EventsEmit(a.ctx, "desktop-ready", nil)
				} else if event.Action == "quit" {
					a.RequestQuit()
				}
			case "notification-action":
				a.runDesktopAction(event)
			case "suspend":
				a.phone.Suspend()
			case "resume", "network-online":
				a.startWork(func() { a.phone.RecoverRegistrations() })
			}
		case event := <-a.desktopEvents:
			a.mu.Lock()
			enabled := a.preferences.Notifications
			a.mu.Unlock()
			if event.name == "phone-changed" {
				ringing := map[string]bool{}
				for _, call := range a.phone.Snapshot().Calls {
					if call.State == "ringing" {
						ringing["call:"+call.ID] = true
					}
				}
				for key := range notifications {
					if !ringing[key] {
						ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
						service.Dismiss(ctx, key)
						cancel()
						delete(notifications, key)
					}
				}
				continue
			}
			if !enabled {
				continue
			}
			notice, ok := event.value.(phone.Notice)
			if !ok {
				continue
			}
			notification := desktop.Notification{Title: "Voiper", Body: notice.Remote, Timeout: 10 * time.Second}
			switch event.name {
			case "incoming-call":
				notification.Key = "call:" + notice.ID
				notification.Title = "Incoming call · " + notice.Account
				notification.Urgent = true
				notification.Timeout = 0
				notification.Actions = []desktop.Action{{Key: "answer", Label: "Answer"}, {Key: "decline", Label: "Decline"}, {Key: "show", Label: "Show"}}
				notifications[notification.Key] = true
			case "incoming-message":
				notification.Key = "message:" + notice.Account + ":" + notice.Remote
				notification.Title = "New message · " + notice.Account
				notification.Actions = []desktop.Action{{Key: "show", Label: "Open Voiper"}}
			default:
				continue
			}
			ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
			err := service.Notify(ctx, notification)
			cancel()
			if err != nil && !errors.Is(err, desktop.ErrUnavailable) && a.ctx.Err() == nil {
				runtime.EventsEmit(a.ctx, "desktop-warning", "Desktop notification could not be shown")
			}
		}
	}
}

// watchScreenLock reports the screen lock to the phone, which shows the user
// as away while locked. A failing custom command is logged once per message.
func (a *App) watchScreenLock() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	lastError := ""
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
		}
		a.mu.Lock()
		keep, command := a.preferences.KeepStatusWhenLocked, a.preferences.ScreenLockCommand
		a.mu.Unlock()
		locked := false
		if !keep {
			ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
			var err error
			locked, err = desktop.ScreenLocked(ctx, command)
			cancel()
			if message := fmt.Sprint(err); err != nil && message != lastError {
				slog.Warn("Could not check the screen lock", "error", err)
				lastError = message
			} else if err == nil {
				lastError = ""
			}
		}
		a.phone.SetScreenLocked(locked)
	}
}
func (a *App) startWork(fn func()) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return
	}
	a.workers.Add(1)
	a.mu.Unlock()
	go func() { defer a.workers.Done(); fn() }()
}
func (a *App) runDesktopAction(event desktop.Event) {
	runtime.WindowShow(a.ctx)
	if !strings.HasPrefix(event.Key, "call:") {
		return
	}
	id := strings.TrimPrefix(event.Key, "call:")
	a.startWork(func() {
		var err error
		switch event.Action {
		case "answer":
			err = a.phone.Answer(id)
		case "decline":
			err = a.phone.Hangup(id)
		}
		if err != nil {
			runtime.EventsEmit(a.ctx, "phone-error", err.Error())
		}
	})
}
