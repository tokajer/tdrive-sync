// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("content = %q", got)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestWriteAtomicIfChangedSkipsSameContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := WriteAtomicIfChanged(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomicIfChanged(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if !fi.ModTime().Equal(past) {
		t.Fatal("unchanged content was rewritten")
	}
}
