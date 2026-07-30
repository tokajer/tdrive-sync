// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import "testing"

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
