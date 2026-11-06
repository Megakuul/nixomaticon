//go:build linux && cgo && webkit2_41

package desktop

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static gboolean voiper_prepare_gtk(void) {
    if (!gtk_init_check(NULL, NULL))
        return FALSE;
    GdkScreen *screen = gdk_screen_get_default();
    if (!screen)
        return FALSE;
    // WebKitGTK uses this resolution for zoom; GDK's unset sentinel is negative.
    if (gdk_screen_get_resolution(screen) <= 0) {
        gint dpi = -1;
        g_object_get(gtk_settings_get_for_screen(screen), "gtk-xft-dpi", &dpi, NULL);
        gdk_screen_set_resolution(screen, dpi > 0 ? dpi / 1024.0 : 96.0);
    }
    return TRUE;
}
*/
import "C"

import (
	"errors"
	"os"
)

// PrepareGTK must run on the locked OS thread that subsequently runs Wails.
func PrepareGTK() error {
	// Match Wails' backend selection before initializing GTK on its behalf.
	if os.Getenv("GDK_BACKEND") == "" {
		switch os.Getenv("XDG_SESSION_TYPE") {
		case "", "unspecified", "x11":
			if err := os.Setenv("GDK_BACKEND", "x11"); err != nil {
				return err
			}
		}
	}
	if C.voiper_prepare_gtk() == 0 {
		return errors.New("could not initialize GTK display")
	}
	return nil
}
