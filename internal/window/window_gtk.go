//go:build linux && cgo

// Package window opens the settings UI in a native application window using the
// system's WebKitGTK. The GTK/WebKit libraries are loaded at runtime via dlopen,
// so building needs no GTK/WebKit development headers and the binary still runs
// on systems where WebKitGTK is present (as it is on most desktops).
package window

/*
#cgo LDFLAGS: -ldl
#include <stdlib.h>
#include <dlfcn.h>

typedef int   (*init_check_t)(int*, char***);
typedef void* (*new_void_t)(void);
typedef void* (*window_new_t)(int);
typedef void  (*set_title_t)(void*, const char*);
typedef void  (*set_size_t)(void*, int, int);
typedef void  (*add_t)(void*, void*);
typedef void  (*widget_t)(void*);
typedef void  (*load_uri_t)(void*, const char*);
typedef void  (*void_fn_t)(void);
typedef unsigned long (*connect_t)(void*, const char*, void*, void*, void*, int);
typedef void  (*set_prgname_t)(const char*);
typedef int   (*deficon_file_t)(const char*, void**);
typedef int   (*icon_file_t)(void*, const char*, void**);

static void *h_gtk, *h_gobj, *h_wk, *h_glib;

static init_check_t p_init_check;
static window_new_t p_window_new;
static set_title_t  p_set_title;
static set_size_t   p_set_size;
static add_t        p_add;
static widget_t     p_show_all;
static widget_t     p_grab_focus;
static connect_t    p_connect;
static void_fn_t    p_main;
static void_fn_t    p_main_quit;
static new_void_t   p_wk_new;
static load_uri_t   p_wk_load;
static set_prgname_t  p_set_prgname;
static deficon_file_t p_set_deficon;
static icon_file_t    p_set_icon;

// destroy handler: quit the GTK main loop so the process exits.
static void on_destroy(void* w, void* d) {
	if (p_main_quit) p_main_quit();
}

// close handler: JavaScript window.close() makes WebKit emit "close"; quit the
// GTK main loop so the settings window closes (used on an update restart).
static void on_close(void* w, void* d) {
	if (p_main_quit) p_main_quit();
}

static int load_syms(void) {
	h_gtk  = dlopen("libgtk-3.so.0", RTLD_NOW | RTLD_GLOBAL);
	h_gobj = dlopen("libgobject-2.0.so.0", RTLD_NOW | RTLD_GLOBAL);
	h_wk   = dlopen("libwebkit2gtk-4.1.so.0", RTLD_NOW | RTLD_GLOBAL);
	h_glib = dlopen("libglib-2.0.so.0", RTLD_NOW | RTLD_GLOBAL);
	if (!h_gtk)  return 1;
	if (!h_gobj) return 2;
	if (!h_wk)   return 3;

	p_init_check = (init_check_t) dlsym(h_gtk, "gtk_init_check");
	p_window_new = (window_new_t) dlsym(h_gtk, "gtk_window_new");
	p_set_title  = (set_title_t)  dlsym(h_gtk, "gtk_window_set_title");
	p_set_size   = (set_size_t)   dlsym(h_gtk, "gtk_window_set_default_size");
	p_add        = (add_t)        dlsym(h_gtk, "gtk_container_add");
	p_show_all   = (widget_t)     dlsym(h_gtk, "gtk_widget_show_all");
	p_grab_focus = (widget_t)     dlsym(h_gtk, "gtk_widget_grab_focus");
	p_main       = (void_fn_t)    dlsym(h_gtk, "gtk_main");
	p_main_quit  = (void_fn_t)    dlsym(h_gtk, "gtk_main_quit");
	p_connect    = (connect_t)    dlsym(h_gobj, "g_signal_connect_data");
	p_wk_new     = (new_void_t)   dlsym(h_wk, "webkit_web_view_new");
	p_wk_load    = (load_uri_t)   dlsym(h_wk, "webkit_web_view_load_uri");

	// Optional: setting the Wayland app_id and the window icon. Missing symbols
	// only mean a generic icon, so they must not fail initialisation.
	p_set_prgname = (set_prgname_t)  dlsym(h_glib ? h_glib : h_gobj, "g_set_prgname");
	p_set_deficon = (deficon_file_t) dlsym(h_gtk, "gtk_window_set_default_icon_from_file");
	p_set_icon    = (icon_file_t)    dlsym(h_gtk, "gtk_window_set_icon_from_file");

	if (!p_init_check || !p_window_new || !p_set_title || !p_set_size ||
	    !p_add || !p_show_all || !p_connect || !p_main || !p_main_quit ||
	    !p_wk_new || !p_wk_load) return 4;
	return 0;
}

// run opens the window and blocks in the GTK main loop until it is closed.
static int run_window(const char* title, const char* url, const char* icon) {
	int rc = load_syms();
	if (rc) return rc;

	// Set the Wayland app_id before the display connection is made so the
	// compositor can associate the window with the app (and its .desktop icon).
	if (p_set_prgname) p_set_prgname("tdrive-sync");

	if (!p_init_check(0, 0)) return 10; // no display / init failed

	// Set the window icon directly from the bundled logo. This works even when
	// no .desktop file is installed (e.g. a freshly launched AppImage), so the
	// window shows the app logo instead of a generic placeholder.
	if (icon && icon[0] && p_set_deficon) p_set_deficon(icon, 0);

	void* win = p_window_new(0); // GTK_WINDOW_TOPLEVEL
	p_set_title(win, title);
	p_set_size(win, 980, 720);
	if (icon && icon[0] && p_set_icon) p_set_icon(win, icon, 0);
	p_connect(win, "destroy", (void*)on_destroy, 0, 0, 0);

	void* wv = p_wk_new();
	p_add(win, wv);
	if (p_grab_focus) p_grab_focus(wv);
	// Let the page close its own window via JavaScript window.close().
	p_connect(wv, "close", (void*)on_close, 0, 0, 0);
	p_wk_load(wv, url);

	p_show_all(win);
	p_main();
	return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// Available reports that a native-window implementation is compiled in.
const Available = true

// Open shows the given URL in a native window and blocks until it is closed.
// It must be called from the program's main goroutine.
func Open(title, url string) error {
	// Avoid blank pages on some GPU/driver combinations.
	if os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
	runtime.LockOSThread()

	ct := C.CString(title)
	cu := C.CString(url)
	ci := C.CString(iconPath()) // "" if the icon could not be materialised
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cu))
	defer C.free(unsafe.Pointer(ci))

	rc := C.run_window(ct, cu, ci)
	switch int(rc) {
	case 0:
		return nil
	case 1, 2, 3:
		return fmt.Errorf("WebKitGTK/GTK-Bibliothek nicht gefunden (Code %d)", int(rc))
	case 10:
		return fmt.Errorf("keine grafische Sitzung (DISPLAY/Wayland) verfügbar")
	default:
		return fmt.Errorf("Fenster konnte nicht geöffnet werden (Code %d)", int(rc))
	}
}
