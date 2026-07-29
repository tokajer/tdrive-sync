package window

import (
	"os"
	"path/filepath"

	"tdrive-sync/internal/i18n"
)

// localized renders one desktop-entry line per supported language: the plain
// key holds English, and a "[lang]" variant follows for every translation. The
// desktop environment then picks the line matching its own locale, which is not
// necessarily the one the daemon started with.
func localized(key, msgKey string) string {
	out := key + "=" + i18n.In(i18n.EN, msgKey) + "\n"
	for _, l := range []i18n.Lang{i18n.DE} {
		out += key + "[" + string(l) + "]=" + i18n.In(l, msgKey) + "\n"
	}
	return out
}

// InstallDesktopEntry writes a user-scope .desktop file and the app icon into
// ~/.local/share so the Wayland compositor can associate the settings window
// (whose app_id is "tdrive-sync") with its icon. On Wayland a GTK3 app cannot
// hand a per-window icon to the compositor, so this desktop-file match is the
// only reliable way to get the logo into the titlebar / taskbar / overview.
//
// It is idempotent (only writes when content changed) and best-effort: any
// error is returned for logging but is not fatal to the daemon.
func InstallDesktopEntry() error {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share")
	}

	iconDir := filepath.Join(data, "icons", "hicolor", "scalable", "apps")
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(iconDir, "tdrive-sync.svg"), iconSVG); err != nil {
		return err
	}

	exec := appExecPath()

	appDir := filepath.Join(data, "applications")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	// The file id ("tdrive-sync") must equal the window's Wayland app_id for the
	// compositor to match them; StartupWMClass covers X11 as well.
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=TDrive Sync\n" +
		localized("GenericName", "desktop.generic_name") +
		localized("Comment", "desktop.comment") +
		"Exec=\"" + exec + "\"\n" +
		"Icon=tdrive-sync\n" +
		"Terminal=false\n" +
		"Categories=Network;FileTransfer;\n" +
		"Keywords=google;drive;sync;cloud;backup;\n" +
		"StartupNotify=false\n" +
		"StartupWMClass=tdrive-sync\n"
	return writeIfChanged(filepath.Join(appDir, "tdrive-sync.desktop"), []byte(entry))
}

// InstallAutostart registers (or removes) an XDG autostart entry so the daemon
// starts automatically when the user logs in. When enabled is false any
// existing entry is removed. It is idempotent and best-effort.
func InstallAutostart(enabled bool) error {
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cfgHome = filepath.Join(home, ".config")
	}
	dir := filepath.Join(cfgHome, "autostart")
	path := filepath.Join(dir, "tdrive-sync.desktop")

	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=TDrive Sync\n" +
		localized("Comment", "desktop.autostart_comment") +
		"Exec=\"" + appExecPath() + "\" run\n" +
		"Icon=tdrive-sync\n" +
		"Terminal=false\n" +
		"Categories=Network;FileTransfer;\n" +
		"StartupNotify=false\n" +
		"StartupWMClass=tdrive-sync\n" +
		"X-GNOME-Autostart-enabled=true\n" +
		"X-GNOME-Autostart-Delay=5\n"
	return writeIfChanged(path, []byte(entry))
}

// appExecPath returns the command used to launch the app. It prefers the outer
// AppImage path (stable across runs) over the executable, which for an AppImage
// points into a temporary mount that vanishes on exit.
func appExecPath() string {
	if p := os.Getenv("APPIMAGE"); p != "" {
		return p
	}
	if e, err := os.Executable(); err == nil {
		return e
	}
	return "tdrive-sync"
}

// writeIfChanged writes data to path only when it differs from the current
// contents, so repeated daemon starts do not churn the files.
func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}
