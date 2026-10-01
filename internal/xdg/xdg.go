// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package xdg resolves the freedesktop base directories. Every other package
// goes through it instead of re-deriving "$XDG_… or the default below $HOME",
// so an isolated instance (own XDG_* environment) really is isolated and the
// permissions of the directories we create stay consistent.
package xdg

import (
	"os"
	"path/filepath"
)

// appDir is the per-application subdirectory inside every base directory.
const appDir = "tdrive-sync"

// base resolves one base directory: the environment variable when it names an
// absolute path, otherwise the given fallback below the home directory.
//
// A relative value is ignored on purpose. The spec requires absolute paths, and
// honouring a relative one would put user data wherever the process happens to
// be running.
func base(env string, fallback ...string) (string, error) {
	if v := os.Getenv(env); filepath.IsAbs(v) {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}

// ConfigHome is $XDG_CONFIG_HOME (default ~/.config).
func ConfigHome() (string, error) { return base("XDG_CONFIG_HOME", ".config") }

// DataHome is $XDG_DATA_HOME (default ~/.local/share).
func DataHome() (string, error) { return base("XDG_DATA_HOME", ".local", "share") }

// StateHome is $XDG_STATE_HOME (default ~/.local/state).
func StateHome() (string, error) { return base("XDG_STATE_HOME", ".local", "state") }

// CacheHome is $XDG_CACHE_HOME (default ~/.cache).
func CacheHome() (string, error) { return base("XDG_CACHE_HOME", ".cache") }

// ConfigDir returns our configuration directory, creating it. It holds the
// account credentials, so it is private to the user.
func ConfigDir() (string, error) { return ensure(ConfigHome, 0o700) }

// StateDir returns our runtime-state directory (status file, logs), creating it.
func StateDir() (string, error) { return ensure(StateHome, 0o700) }

// CacheDir returns our cache directory, creating it.
func CacheDir() (string, error) { return ensure(CacheHome, 0o700) }

// DataDir returns our data directory, creating it. Unlike the others this one
// is world-readable: the desktop entry and the icon below it have to be
// readable by the session's own tooling.
func DataDir() (string, error) { return ensure(DataHome, 0o755) }

// ensure resolves a base directory, appends the application subdirectory and
// creates it.
func ensure(home func() (string, error), mode os.FileMode) (string, error) {
	b, err := home()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(b, appDir)
	if err := os.MkdirAll(dir, mode); err != nil {
		return "", err
	}
	return dir, nil
}
