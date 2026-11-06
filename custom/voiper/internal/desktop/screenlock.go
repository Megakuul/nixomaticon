package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// lockerProcesses run only while the screen is locked. hyprlock comes first as
// the primary target; swaylock, gtklock and waylock cover niri, sway and other
// wlroots compositors; kscreenlocker_greet is KDE's lock screen. Long-running
// daemons such as xscreensaver are left out because their presence says nothing.
var lockerProcesses = []string{"hyprlock", "swaylock", "gtklock", "waylock", "i3lock", "slock", "xsecurelock", "physlock", "kscreenlocker_g"}

// ScreenLocked reports whether the desktop is locked. A non-empty command runs
// with sh -c and decides alone: exit status 0 means locked, 1 unlocked. Without
// one, locker processes, the GNOME and freedesktop screensaver interfaces and
// logind's LockedHint are checked; any of them reporting a lock counts.
func ScreenLocked(ctx context.Context, command string) (bool, error) {
	if command != "" {
		err := exec.CommandContext(ctx, "sh", "-c", command).Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return true, nil
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return false, nil
		default:
			return false, fmt.Errorf("screen lock command: %w", err)
		}
	}
	if lockerRunning() {
		return true, nil
	}
	if session, err := dbus.SessionBus(); err == nil {
		for _, name := range []string{"org.gnome.ScreenSaver", "org.freedesktop.ScreenSaver"} {
			var active bool
			path := dbus.ObjectPath("/" + strings.ReplaceAll(name, ".", "/"))
			if session.Object(name, path).CallWithContext(ctx, name+".GetActive", 0).Store(&active) == nil && active {
				return true, nil
			}
		}
	}
	if system, err := dbus.SystemBus(); err == nil {
		// "auto" is the logind session this process belongs to.
		variant, err := system.Object("org.freedesktop.login1", "/org/freedesktop/login1/session/auto").GetProperty("org.freedesktop.login1.Session.LockedHint")
		if locked, ok := variant.Value().(bool); err == nil && ok && locked {
			return true, nil
		}
	}
	return false, nil
}

// lockerRunning looks for a locker process owned by the current user.
func lockerRunning() bool {
	entries, err := filepath.Glob("/proc/[0-9]*/comm")
	if err != nil {
		return false
	}
	uid := uint32(os.Getuid())
	for _, entry := range entries {
		name, err := os.ReadFile(entry)
		if err != nil || !slices.Contains(lockerProcesses, strings.TrimSpace(string(name))) {
			continue
		}
		info, err := os.Stat(filepath.Dir(entry))
		if err != nil {
			continue
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid == uid {
			return true
		}
	}
	return false
}
