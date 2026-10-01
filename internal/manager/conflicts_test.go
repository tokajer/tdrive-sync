// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"os"
	"path/filepath"
	"testing"

	"tdrive-sync/internal/config"
)

func TestMarkerBase(t *testing.T) {
	cases := map[string]string{
		// manual mode: rclone appends .conflictN at the end
		"report.txt.conflict1": "report.txt",
		"report.txt.conflict2": "report.txt",
		// suffix inserted before the extension
		"report.conflict1.txt": "report.txt",
		// auto mode: dated backup suffix
		"report.txt.conflict-2026-07-19": "report.txt",
		"report.conflict-2026-07-19.txt": "report.txt",
		// nested relative path is preserved
		"docs/report.txt.conflict1": "docs/report.txt",
		// no marker -> unchanged
		"report.txt": "report.txt",
	}
	for in, want := range cases {
		if got := markerBase(in); got != want {
			t.Errorf("markerBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConflictSide(t *testing.T) {
	cases := map[string]string{
		"report.txt.conflict1":           "cloud",
		"report.txt.conflict2":           "local",
		"report.txt.conflict-2026-07-19": "backup",
	}
	for in, want := range cases {
		if got := conflictSide(in); got != want {
			t.Errorf("conflictSide(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestConflictsOnlyInMirrorMode: stream mode has no conflict copies, and a
// resolve request must not rename files on the live mount.
func TestConflictsOnlyInMirrorMode(t *testing.T) {
	m := newTestManager(t)
	dir := m.cfg.LocalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyName := "a.conflict1.txt"
	if err := os.WriteFile(filepath.Join(dir, copyName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := m.Conflicts(); got != nil {
		t.Errorf("stream mode listed conflicts: %v", got)
	}
	if err := m.ResolveConflict(copyName, "keep"); err == nil {
		t.Error("stream mode resolved a conflict")
	}

	if err := m.cfg.SetMode(config.ModeMirror); err != nil {
		t.Fatal(err)
	}
	got := m.Conflicts()
	if len(got) != 1 || got[0].Path != copyName || got[0].Side != "cloud" {
		t.Fatalf("mirror conflicts = %+v", got)
	}
	if err := m.ResolveConflict(copyName, "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Errorf("kept copy not promoted: %v", err)
	}
}
