// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package fmstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// cache builds an Info pointing at a throwaway cache directory.
func cache(t *testing.T) Info {
	t.Helper()
	return Info{
		Active:   true,
		Mode:     "stream",
		State:    "idle",
		Root:     "/home/u/GoogleDrive",
		CacheDir: t.TempDir(),
		Remote:   "gdrive",
	}
}

// writeCached puts a cache entry in place: a sparse data file of the given size
// with the first filled bytes written, plus rclone's metadata.
func writeCached(t *testing.T, i Info, rel string, size, filled int64, dirty bool, ranges []cachedRange) {
	t.Helper()
	data := i.DataPath(rel)
	if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(data)
	if err != nil {
		t.Fatal(err)
	}
	if filled > 0 {
		if _, err := f.Write(make([]byte, filled)); err != nil {
			t.Fatal(err)
		}
	}
	// Sparse: the cache file always has the full size, only parts are allocated.
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	meta := i.MetaPath(rel)
	if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(struct {
		Size  int64
		Dirty bool
		Rs    []cachedRange
	}{Size: size, Dirty: dirty, Rs: ranges})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRel(t *testing.T) {
	i := cache(t)
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"/home/u/GoogleDrive/a/b.txt", "a/b.txt", true},
		{"/home/u/GoogleDrive/x", "x", true},
		{"/home/u/GoogleDrive/", "", false},   // the sync folder itself
		{"/home/u/GoogleDrive", "", false},    // ditto
		{"/home/u/GoogleDriveX/a", "", false}, // a sibling that merely shares the prefix
		{"/home/u/other", "", false},
	} {
		got, ok := i.Rel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Rel(%q) = (%q, %t), want (%q, %t)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIsPinned(t *testing.T) {
	i := cache(t)
	i.Pinned = []string{"Documents", "Photos/2024"}
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"Documents", true},
		{"Documents/tax/2023.pdf", true},
		{"Photos/2024/a.jpg", true},
		{"Photos/2023/a.jpg", false},
		{"Documents2", false}, // prefix without a path separator must not match
		{"Photos", false},     // a parent of a pinned path is not itself pinned
	} {
		if got := i.IsPinned(tc.rel); got != tc.want {
			t.Errorf("IsPinned(%q) = %t, want %t", tc.rel, got, tc.want)
		}
	}
}

func TestResolveRelStates(t *testing.T) {
	const size = 1 << 20 // 1 MiB

	t.Run("no cache entry is cloud-only", func(t *testing.T) {
		i := cache(t)
		if got := i.ResolveRel("a.bin", false); got != Cloud {
			t.Errorf("got %q, want %q", got, Cloud)
		}
	})

	t.Run("fully downloaded is cached", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "a.bin", size, size, false, nil)
		if got := i.ResolveRel("a.bin", false); got != Cached {
			t.Errorf("got %q, want %q", got, Cached)
		}
	})

	t.Run("partly downloaded is partial", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "a.bin", size, 4096, false, nil)
		if got := i.ResolveRel("a.bin", false); got != Partial {
			t.Errorf("got %q, want %q", got, Partial)
		}
	})

	t.Run("allocated nothing yet is cloud-only", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "a.bin", size, 0, false, nil)
		if got := i.ResolveRel("a.bin", false); got != Cloud {
			t.Errorf("got %q, want %q", got, Cloud)
		}
	})

	t.Run("recorded ranges win over the allocation", func(t *testing.T) {
		i := cache(t)
		// Sparse on disk, but rclone says the whole file is there.
		writeCached(t, i, "a.bin", size, 4096, false, []cachedRange{{Pos: 0, Size: size}})
		if got := i.ResolveRel("a.bin", false); got != Cached {
			t.Errorf("got %q, want %q", got, Cached)
		}
	})

	t.Run("ranges with a gap are not complete", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "a.bin", size, 4096, false,
			[]cachedRange{{Pos: 0, Size: 1024}, {Pos: 2048, Size: size}})
		if got := i.ResolveRel("a.bin", false); got != Partial {
			t.Errorf("got %q, want %q", got, Partial)
		}
	})

	t.Run("local changes are uploading", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "a.bin", size, size, true, nil)
		if got := i.ResolveRel("a.bin", false); got != Uploading {
			t.Errorf("got %q, want %q", got, Uploading)
		}
	})

	t.Run("pinned and complete", func(t *testing.T) {
		i := cache(t)
		i.Pinned = []string{"dir"}
		writeCached(t, i, "dir/a.bin", size, size, false, nil)
		if got := i.ResolveRel("dir/a.bin", false); got != Pinned {
			t.Errorf("got %q, want %q", got, Pinned)
		}
	})

	t.Run("pinned but still downloading", func(t *testing.T) {
		i := cache(t)
		i.Pinned = []string{"a.bin"}
		writeCached(t, i, "a.bin", size, 4096, false, nil)
		if got := i.ResolveRel("a.bin", false); got != Pinning {
			t.Errorf("got %q, want %q", got, Pinning)
		}
	})

	t.Run("pinned with nothing downloaded yet", func(t *testing.T) {
		i := cache(t)
		i.Pinned = []string{"a.bin"}
		if got := i.ResolveRel("a.bin", false); got != Pinning {
			t.Errorf("got %q, want %q", got, Pinning)
		}
	})

	t.Run("empty file counts as complete", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "empty", 0, 0, false, nil)
		if got := i.ResolveRel("empty", false); got != Cached {
			t.Errorf("got %q, want %q", got, Cached)
		}
	})

	t.Run("folders", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "dir/a.bin", size, size, false, nil)
		if got := i.ResolveRel("dir", true); got != Partial {
			t.Errorf("touched folder: got %q, want %q", got, Partial)
		}
		if got := i.ResolveRel("untouched", true); got != Cloud {
			t.Errorf("untouched folder: got %q, want %q", got, Cloud)
		}
		i.Pinned = []string{"dir"}
		if got := i.ResolveRel("dir", true); got != Pinned {
			t.Errorf("pinned folder: got %q, want %q", got, Pinned)
		}
	})

	// A cache folder stays behind after its content is freed, and rclone leaves a
	// placeholder for every file it merely opened. Neither means the folder holds
	// anything locally, and the indicator must not claim it does.
	t.Run("folders without cached data are cloud-only", func(t *testing.T) {
		i := cache(t)

		if err := os.MkdirAll(i.DataPath("emptied/deep"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := i.ResolveRel("emptied", true); got != Cloud {
			t.Errorf("emptied folder: got %q, want %q", got, Cloud)
		}

		writeCached(t, i, "placeholders/a.bin", size, 0, false, nil)
		if got := i.ResolveRel("placeholders", true); got != Cloud {
			t.Errorf("folder of placeholders: got %q, want %q", got, Cloud)
		}

		writeCached(t, i, "placeholders/sub/b.bin", size, 4096, false, nil)
		if got := i.ResolveRel("placeholders", true); got != Partial {
			t.Errorf("data in a subfolder: got %q, want %q", got, Partial)
		}
	})

	t.Run("a huge folder answers without walking all of it", func(t *testing.T) {
		i := cache(t)
		for n := 0; n < dirScanBudget*2; n++ {
			writeCached(t, i, filepath.Join("many", fmt.Sprint(n)), size, 0, false, nil)
		}
		if got := i.ResolveRel("many", true); got != Partial {
			t.Errorf("got %q, want %q (the budget makes it assume data)", got, Partial)
		}
	})

	t.Run("mirror mode is always local", func(t *testing.T) {
		i := cache(t)
		i.Mode = "mirror"
		if got := i.ResolveRel("anything", false); got != Local {
			t.Errorf("got %q, want %q", got, Local)
		}
	})

	t.Run("the sync folder itself has no state", func(t *testing.T) {
		i := cache(t)
		if got := i.ResolveRel("", true); got != Unknown {
			t.Errorf("got %q, want %q", got, Unknown)
		}
	})
}

func TestEvict(t *testing.T) {
	const size = 1 << 20

	t.Run("removes the cache entry and reports the freed space", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "dir/a.bin", size, size, false, nil)
		writeCached(t, i, "dir/b.bin", size, 4096, false, nil)

		freed, kept, err := i.Evict("dir")
		if err != nil {
			t.Fatal(err)
		}
		if kept != 0 {
			t.Errorf("kept %d files, want 0", kept)
		}
		if freed < size {
			t.Errorf("freed %d bytes, want at least %d", freed, size)
		}
		if _, err := os.Stat(i.DataPath("dir")); !os.IsNotExist(err) {
			t.Errorf("cache data still present: %v", err)
		}
		if _, err := os.Stat(i.MetaPath("dir")); !os.IsNotExist(err) {
			t.Errorf("cache metadata still present: %v", err)
		}
		if got := i.ResolveRel("dir/a.bin", false); got != Cloud {
			t.Errorf("after evicting: got %q, want %q", got, Cloud)
		}
	})

	t.Run("keeps files whose changes have not reached Drive", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "dir/clean.bin", size, size, false, nil)
		writeCached(t, i, "dir/unsent.bin", size, size, true, nil)

		freed, kept, err := i.Evict("dir")
		if err != nil {
			t.Fatal(err)
		}
		if kept != 1 {
			t.Errorf("kept %d files, want 1", kept)
		}
		if freed < size || freed >= 2*size {
			t.Errorf("freed %d bytes, want about %d", freed, size)
		}
		if _, err := os.Stat(i.DataPath("dir/unsent.bin")); err != nil {
			t.Errorf("the file with unsent changes was deleted: %v", err)
		}
		if _, err := os.Stat(i.DataPath("dir/clean.bin")); !os.IsNotExist(err) {
			t.Errorf("the clean file survived: %v", err)
		}
	})

	t.Run("keeps a single file with unsent changes", func(t *testing.T) {
		i := cache(t)
		writeCached(t, i, "unsent.bin", size, size, true, nil)
		freed, kept, err := i.Evict("unsent.bin")
		if err != nil {
			t.Fatal(err)
		}
		if freed != 0 || kept != 1 {
			t.Errorf("freed %d bytes and kept %d, want 0 and 1", freed, kept)
		}
		if _, err := os.Stat(i.DataPath("unsent.bin")); err != nil {
			t.Errorf("the file with unsent changes was deleted: %v", err)
		}
	})

	t.Run("refuses to leave the cache", func(t *testing.T) {
		i := cache(t)
		for _, rel := range []string{"", " ", "/", ".", "..", "../..", "a/../../elsewhere"} {
			if _, _, err := i.Evict(rel); err == nil {
				t.Errorf("Evict(%q) was allowed", rel)
			}
		}
		// The cache root itself must survive those attempts.
		if _, err := os.Stat(i.CacheDir); err != nil {
			t.Errorf("cache directory gone: %v", err)
		}
	})

	t.Run("evicting something absent is not an error", func(t *testing.T) {
		i := cache(t)
		freed, kept, err := i.Evict("never/downloaded")
		if err != nil {
			t.Fatal(err)
		}
		if freed != 0 || kept != 0 {
			t.Errorf("freed %d bytes and kept %d, want 0 and 0", freed, kept)
		}
	})
}

func TestPublisherSkipsUnchangedWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	var p Publisher
	info := Info{Active: true, Mode: "stream", Root: "/home/u/GoogleDrive", Remote: "gdrive"}
	if err := p.Publish(info); err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != Version || loaded.Root != info.Root || !loaded.Active {
		t.Errorf("round trip lost data: %+v", loaded)
	}

	// Publishing the same picture again must not touch the file, so watchers do
	// not wake up for nothing.
	if err := p.Publish(info); err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Error("an unchanged snapshot was written again")
	}

	info.Pinned = []string{"Documents"}
	if err := p.Publish(info); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pinned) != 1 || loaded.Pinned[0] != "Documents" {
		t.Errorf("changed snapshot was not written: %+v", loaded)
	}
}
