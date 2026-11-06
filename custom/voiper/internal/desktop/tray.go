package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"slices"
	"sync/atomic"
	"time"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"
)

var trayStarted atomic.Bool

func (s *Service) StartTray(ctx context.Context, iconPNG []byte) error {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	if s.stopTray != nil {
		if s.Capabilities().Tray {
			return nil
		}
		return ErrUnavailable
	}
	if s.session == nil || s.ctx.Err() != nil {
		return ErrUnavailable
	}
	if len(iconPNG) > 1<<20 {
		return errors.New("tray icon exceeds 1 MiB")
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(iconPNG))
	if err != nil || dimensions.Width > 4096 || dimensions.Height > 4096 {
		return errors.New("invalid tray PNG")
	}
	source, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return err
	}
	icon := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			icon.Set(x, y, source.At(x*dimensions.Width/64, y*dimensions.Height/64))
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, icon); err != nil {
		return err
	}
	iconPNG = encoded.Bytes()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	watcher := s.session.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher")
	var host dbus.Variant
	if err := watcher.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.kde.StatusNotifierWatcher", "IsStatusNotifierHostRegistered").Store(&host); err != nil {
		return ErrUnavailable
	}
	if active, ok := host.Value().(bool); !ok || !active {
		return ErrUnavailable
	}
	if err := s.watch(s.session, "org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher", ""); err != nil {
		return err
	}
	if !trayStarted.CompareAndSwap(false, true) {
		return errors.New("tray has already been initialized")
	}
	ready := make(chan struct{})
	start, end := systray.RunWithExternalLoop(func() {
		systray.SetTitle("Voiper")
		systray.SetTooltip("Voiper SIP phone")
		systray.SetIcon(iconPNG)
		systray.SetOnTapped(func() { s.trayAction("show") })
		show := systray.AddMenuItem("Show Voiper", "Open the phone")
		quit := systray.AddMenuItem("Quit Voiper", "Close the phone")
		go func() {
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-show.ClickedCh:
					s.trayAction("show")
				case <-quit.ClickedCh:
					s.trayAction("quit")
				}
			}
		}()
		close(ready)
	}, nil)
	start()
	s.stopTray = end
	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	name := ""
	for _, candidate := range conn.Names() {
		if len(candidate) > 0 && candidate[0] == ':' {
			name = candidate
			break
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var registered dbus.Variant
		if err := watcher.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.kde.StatusNotifierWatcher", "RegisteredStatusNotifierItems").Store(&registered); err != nil {
			return err
		}
		items, _ := registered.Value().([]string)
		if slices.Contains(items, name+"/StatusNotifierItem") {
			s.mu.Lock()
			s.capabilities.Tray = true
			s.mu.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) trayAction(action string) {
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	select {
	case s.trayActions <- Event{Type: "tray-action", Action: action}:
	default:
	}
}
func (s *Service) closeTray() {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	if s.stopTray != nil {
		s.stopTray()
		s.stopTray = nil
	}
	s.mu.Lock()
	s.capabilities.Tray = false
	s.mu.Unlock()
}
