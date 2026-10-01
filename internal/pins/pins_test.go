// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package pins

import (
	"strings"
	"testing"
)

// TestOfflinePins pins down the hierarchy rules. The case that matters in
// practice is the last one: a leftover pin below a released folder used to make
// the release pointless, because the daemon downloaded that file again right
// afterwards and the folder went back to "partially offline".
func TestOfflinePins(t *testing.T) {
	cases := []struct {
		name string
		ops  []string // "+path" pins, "-path" releases
		want []string
	}{
		{"pin one", []string{"+USA"}, []string{"USA"}},
		{"pinning twice keeps one entry", []string{"+USA", "+USA"}, []string{"USA"}},
		{"a pinned ancestor already covers the file", []string{"+USA", "+USA/pass.pdf"}, []string{"USA"}},
		{"pinning the folder drops the pins below it", []string{"+USA/pass.pdf", "+USA"}, []string{"USA"}},
		{"siblings stay", []string{"+USA/a", "+USA2/b"}, []string{"USA/a", "USA2/b"}},
		{"a shared prefix is not a parent", []string{"+USA", "-USAX"}, []string{"USA"}},
		{"releasing a folder drops the pins below it", []string{"+USA/pass.pdf", "+Docs", "-USA"}, []string{"Docs"}},
		{"trailing slashes name the same path", []string{"+USA/", "-/USA"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			for _, op := range tc.ops {
				path := Clean(op[1:])
				if op[0] == '+' {
					paths = Add(paths, path)
				} else {
					paths = Remove(paths, path)
				}
			}
			if got := strings.Join(paths, "|"); got != strings.Join(tc.want, "|") {
				t.Errorf("after %v: offline_paths = %v, want %v", tc.ops, paths, tc.want)
			}
		})
	}
}

func TestIsOffline(t *testing.T) {
	pins := []string{"USA", "Docs/tax/2024.pdf"}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"USA", true},
		{"USA/pass.pdf", true}, // covered by the pinned folder
		{"USAX", false},        // a shared prefix is not a parent
		{"Docs", false},        // the parent of a pinned file is not itself pinned
		{"Docs/tax/2024.pdf", true},
		{"", false},
	} {
		if got := Has(pins, tc.path); got != tc.want {
			t.Errorf("Has(%q) = %t, want %t", tc.path, got, tc.want)
		}
	}
}

// TestNormalizeOffline covers pins that reach us from an older version or a
// hand-edited file: they have to end up in the form the indicator matches
// against, or a pin would be honoured here and ignored on screen.
func TestNormalizeOffline(t *testing.T) {
	got := Normalize([]string{"/USA/", "USA", "", " Docs ", ".", "Docs"})
	if want := "USA|Docs"; strings.Join(got, "|") != want {
		t.Errorf("Normalize = %v, want %s", got, want)
	}
}
