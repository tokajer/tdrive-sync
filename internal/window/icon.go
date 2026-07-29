package window

import (
	_ "embed"
	"os"
	"path/filepath"
)

// iconSVG is the application logo, embedded so the settings window can set its
// own icon without relying on a system-installed .desktop file / icon theme
// (which an AppImage does not register on startup).
//
//go:embed icon.svg
var iconSVG []byte

// iconPath writes the embedded logo to a stable cache location and returns its
// path. It returns "" if the file could not be written, in which case the
// window simply opens without an explicit icon.
func iconPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "tdrive-sync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	p := filepath.Join(dir, "icon.svg")
	if err := os.WriteFile(p, iconSVG, 0o644); err != nil {
		return ""
	}
	return p
}
