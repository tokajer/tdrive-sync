// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package app holds what identifies this program to the desktop: its name and
// the command that starts it again.
package app

import "os"

// Name is the display name: window title, tray title, notification sender and
// desktop entries.
const Name = "TDrive Sync"

// ID is the identifier the desktop matches us by: the desktop-file id, the
// Wayland app_id and the icon name.
const ID = "tdrive-sync"

// AppImage is the path of the AppImage file we run from, "" when we do not.
func AppImage() string { return os.Getenv("APPIMAGE") }

// Exec is the command that starts this program again - for desktop entries,
// the file-manager context menu and the restart after an update.
//
// The outer AppImage path is preferred: it survives a restart, while the
// executable inside points into a mount that vanishes on exit.
func Exec() string {
	if p := AppImage(); p != "" {
		return p
	}
	if e, err := os.Executable(); err == nil {
		return e
	}
	return ID
}
