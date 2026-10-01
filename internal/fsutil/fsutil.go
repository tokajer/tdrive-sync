// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fsutil holds the one way this program replaces a file on disk.
//
// Every file we write is read by someone else at an arbitrary moment: the
// Dolphin plugin watches file-manager.json, monitoring tools read status.json,
// and Dolphin itself reads dolphinrc. A plain os.WriteFile truncates first, so a
// reader - or a crash - in between sees an empty file. Writing a sibling and
// renaming it over the target makes the switch atomic.
package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
)

// WriteAtomic replaces path with data. The parent directory must exist.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	// CreateTemp always uses 0600; the target's mode is part of the contract.
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// WriteAtomicIfChanged is WriteAtomic, skipped when path already holds data.
// Files that others watch stay untouched, so they do not reload for nothing.
func WriteAtomicIfChanged(path string, data []byte, mode os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return WriteAtomic(path, data, mode)
}
