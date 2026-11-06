package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/megakuul/voiper/internal/desktop"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Preferences struct {
	DefaultAccount       string
	Notifications        bool
	CloseToBackground    bool
	Autostart            bool
	MessageRetentionDays int
	// KeepStatusWhenLocked stops Voiper from showing you as away on a locked screen.
	KeepStatusWhenLocked bool
	// ScreenLockCommand overrides lock detection; exit status 0 means locked.
	ScreenLockCommand string
}
type DesktopSettings struct {
	Preferences  Preferences
	Capabilities desktop.Capabilities
}

func (a *App) DesktopSettings() (DesktopSettings, error) {
	if err := a.ready(); err != nil {
		return DesktopSettings{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	result := DesktopSettings{Preferences: a.preferences}
	if a.desktop != nil {
		result.Capabilities = a.desktop.Capabilities()
	}
	return result, nil
}
func (a *App) SetPreferences(p Preferences) error {
	if err := a.ready(); err != nil {
		return err
	}
	if p.MessageRetentionDays < 0 || p.MessageRetentionDays > 3650 {
		return errors.New("message retention must be between 0 and 3650 days")
	}
	if len(p.DefaultAccount) > 256 {
		return errors.New("account name is too long")
	}
	p.ScreenLockCommand = strings.TrimSpace(p.ScreenLockCommand)
	if len(p.ScreenLockCommand) > 1024 || strings.ContainsAny(p.ScreenLockCommand, "\r\n\x00") {
		return errors.New("screen lock command must be a single line")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p.Autostart != a.preferences.Autostart {
		if err = setAutostart(root, p.Autostart); err != nil {
			return err
		}
	}
	if err = writeJSON(filepath.Join(root, "voiper", "preferences.json"), p); err != nil {
		return err
	}
	a.preferences = p
	return a.store.PruneMessages(p.MessageRetentionDays)
}
func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func setAutostart(root string, enabled bool) error {
	path := filepath.Join(root, "autostart", "voiper.desktop")
	if !enabled {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// Nix's GTK wrapper supplies the runtime environment needed at login.
	if filepath.Base(executable) == ".voiper-wrapped" {
		wrapper := filepath.Join(filepath.Dir(executable), "voiper")
		if info, err := os.Stat(wrapper); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			executable = wrapper
		}
	}
	// Profile launchers survive Nix upgrades and garbage collection.
	if current, err := os.Stat(executable); err == nil {
		for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
			if !filepath.IsAbs(directory) {
				continue
			}
			candidate := filepath.Join(directory, "voiper")
			if strings.HasPrefix(candidate, "/nix/store/") {
				continue
			}
			if info, err := os.Stat(candidate); err == nil && os.SameFile(current, info) {
				executable = candidate
				break
			}
		}
	}
	executable = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$", "%", "%%").Replace(executable)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("[Desktop Entry]\nType=Application\nName=Voiper\nExec=\""+executable+"\"\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"), 0600)
}
func (a *App) beforeClose(ctx context.Context) bool {
	select {
	case <-a.initialized:
	default:
		return false
	}
	a.mu.Lock()
	background, quitting := a.preferences.CloseToBackground, a.quitting
	if a.desktop == nil || !a.desktop.Capabilities().Tray {
		background = false
	}
	a.mu.Unlock()
	if quitting {
		return false
	}
	if background {
		runtime.WindowHide(ctx)
		return true
	}
	if a.phone != nil && len(a.phone.Snapshot().Calls) > 0 {
		runtime.EventsEmit(ctx, "confirm-quit", nil)
		return true
	}
	return false
}
func (a *App) Quit() { a.mu.Lock(); a.quitting = true; a.mu.Unlock(); runtime.Quit(a.ctx) }
func (a *App) RequestQuit() {
	if a.ready() != nil {
		return
	}
	runtime.WindowShow(a.ctx)
	if a.phone != nil && len(a.phone.Snapshot().Calls) > 0 {
		runtime.EventsEmit(a.ctx, "confirm-quit", nil)
		return
	}
	a.Quit()
}
