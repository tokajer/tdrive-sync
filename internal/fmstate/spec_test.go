// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package fmstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The state logic exists twice on purpose: here in Go, and in C++ inside the
// Dolphin overlay plugin, which must resolve a state without any IPC. testdata/
// state_cases.json is the specification both have to satisfy. This test runs it
// against the Go side; scripts/check.sh --plugin runs the same file against the
// C++ side.

// specCase is one entry of the shared fixture.
type specCase struct {
	Name     string     `json:"name"`
	Rel      string     `json:"rel"`
	Present  *bool      `json:"present"`
	Size     int64      `json:"size"`
	Filled   int64      `json:"filled"`
	Dirty    bool       `json:"dirty"`
	Ranges   [][2]int64 `json:"ranges"`
	Meta     *bool      `json:"meta"`
	Dir      bool       `json:"dir"`
	Children []specCase `json:"children"`
	Pinned   []string   `json:"pinned"`
	Mode     string     `json:"mode"`
	Want     State      `json:"want"`
}

// exists reports whether the cache entry should be created at all.
func (c specCase) exists() bool { return c.Present == nil || *c.Present }

// hasMeta reports whether the metadata file should be written.
func (c specCase) hasMeta() bool { return c.Meta == nil || *c.Meta }

// SpecPath is where the shared fixture lives, so the C++ side can be pointed at
// the same file.
const SpecPath = "testdata/state_cases.json"

func loadSpec(t *testing.T) []specCase {
	t.Helper()
	raw, err := os.ReadFile(SpecPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []specCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("the shared specification is empty")
	}
	return doc.Cases
}

// materialise builds the cache layout one case describes.
func materialise(t *testing.T, c Cache, tc specCase) {
	t.Helper()
	if tc.Dir {
		if err := os.MkdirAll(c.DataPath(tc.Rel), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, child := range tc.Children {
			materialise(t, c, child)
		}
		return
	}
	if !tc.exists() {
		return
	}

	data := c.DataPath(tc.Rel)
	if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(data)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Filled > 0 {
		if _, err := f.Write(make([]byte, tc.Filled)); err != nil {
			t.Fatal(err)
		}
	}
	// Sparse, the way rclone leaves it: the file always has the full size, only
	// the downloaded part is allocated.
	if err := f.Truncate(tc.Size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if !tc.hasMeta() {
		return
	}
	ranges := make([]cachedRange, 0, len(tc.Ranges))
	for _, r := range tc.Ranges {
		ranges = append(ranges, cachedRange{Pos: r[0], Size: r[1]})
	}
	meta := c.MetaPath(tc.Rel)
	if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(struct {
		Size  int64
		Dirty bool
		Rs    []cachedRange
	}{Size: tc.Size, Dirty: tc.Dirty, Rs: ranges})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSharedSpec runs the fixture the C++ plugin is held to as well. A failure
// here means the two implementations would show different things for the same
// file - the CLI saying one state, the overlay in Dolphin another.
func TestSharedSpec(t *testing.T) {
	for _, tc := range loadSpec(t) {
		t.Run(tc.Name, func(t *testing.T) {
			mode := tc.Mode
			if mode == "" {
				mode = "stream"
			}
			i := Info{
				Active:   true,
				Mode:     mode,
				State:    "idle",
				Root:     "/home/u/GoogleDrive",
				CacheDir: t.TempDir(),
				Remote:   "gdrive",
				Pinned:   tc.Pinned,
			}
			materialise(t, i.Cache(), tc)
			if got := i.ResolveRel(tc.Rel, tc.Dir); got != tc.Want {
				t.Errorf("state = %q, want %q", got, tc.Want)
			}
		})
	}
}
