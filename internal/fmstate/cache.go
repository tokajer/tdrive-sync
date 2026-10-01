// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package fmstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rclone's cache layout below --cache-dir is
//
//	vfs/<remote>/<drive-relative path>      the (possibly sparse) data file
//	vfsMeta/<remote>/<drive-relative path>  JSON: size, cached byte ranges, dirty
//
// so a file's state is one stat plus one small JSON read.

// Cache is a read/evict view of rclone's VFS cache for one remote. It is a
// value, cheap to construct, and holds no state of its own.
//
// Everything that deletes cached bytes lives here rather than on Info: Info is
// the snapshot published to the file manager, and the destructive operations
// have no business travelling with it.
type Cache struct {
	// Dir is rclone's --cache-dir.
	Dir string
	// Remote is the rclone remote name, the first element inside the cache.
	Remote string
}

// Usable reports whether the cache location is known.
func (c Cache) Usable() bool { return c.Dir != "" && c.Remote != "" }

// DataPath is where the cached content of a Drive-relative path lives.
func (c Cache) DataPath(rel string) string {
	return filepath.Join(c.Dir, "vfs", c.Remote, rel)
}

// MetaPath is where rclone keeps the cache metadata of a Drive-relative path.
func (c Cache) MetaPath(rel string) string {
	return filepath.Join(c.Dir, "vfsMeta", c.Remote, rel)
}

// CacheInfo is what the local cache says about one file.
type CacheInfo struct {
	Found    bool // there is a cache file at all
	Empty    bool // it exists but holds no data yet
	Dirty    bool // it holds local changes not yet sent to Drive
	Complete bool // the whole file is there
}

// Inspect looks at the cached copy of a Drive-relative file.
//
// Completeness cannot be read off rclone's recorded ranges alone: rclone leaves
// "Rs" empty both for a file it has fully downloaded and for one it has barely
// touched. What is reliable is how much of the sparse cache file is actually
// allocated, with the first hole as the tie-breaker.
func (c Cache) Inspect(rel string) CacheInfo {
	var out CacheInfo
	dataPath := c.DataPath(rel)
	fi, err := os.Stat(dataPath)
	if err != nil || fi.IsDir() {
		return out
	}
	out.Found = true

	size := fi.Size()
	m, haveMeta := readMeta(c.MetaPath(rel))
	if haveMeta {
		out.Dirty = m.Dirty
		if m.Size > 0 {
			size = m.Size
		}
	}
	if size <= 0 {
		out.Complete = true
		return out
	}

	alloc := allocatedBytes(fi)
	switch {
	case rangesCoverAll(m.Rs, size):
		// rclone did record the ranges: they are authoritative.
		out.Complete = true
	case alloc >= size:
		out.Complete = true
	case alloc == 0:
		out.Empty = true
	default:
		hole, err := firstHole(dataPath, size)
		switch {
		case err != nil:
		case hole >= size:
			out.Complete = true
		case hole == 0:
			out.Empty = true
		}
	}
	return out
}

// dirScanBudget caps how many cache entries DirHasData looks at. It runs from a
// file manager's overlay lookup, which happens for every visible item, so the
// answer has to stay cheap; a folder that really holds data hits the first one
// immediately anyway.
const dirScanBudget = 64

// DirHasData reports whether anything below a Drive-relative folder is cached.
//
// The directory tree outlives the data in it: rclone creates the folders when it
// first touches a file below them, freeing a single file leaves its parents
// behind, and a file that was only opened stays a fully sparse placeholder.
// Taking "the cache folder exists" for "something is local" would therefore leave
// folders marked as partially offline long after the last byte was freed.
func (c Cache) DirHasData(rel string) bool {
	budget := dirScanBudget
	queue := []string{c.DataPath(rel)}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(cur)
		if err != nil {
			// A missing cache directory means nothing is local. Anything else is
			// unreadable, and there the cautious answer is "there is data".
			if os.IsNotExist(err) {
				continue
			}
			return true
		}
		for _, e := range entries {
			if budget <= 0 {
				return true // out of budget: keep the answer we gave before
			}
			budget--
			if e.IsDir() {
				queue = append(queue, filepath.Join(cur, e.Name()))
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if fi.Size() > 0 && allocatedBytes(fi) == 0 {
				continue // a placeholder rclone has not downloaded into yet
			}
			return true
		}
	}
	return false
}

// Evict deletes the local copy of a Drive-relative path (a single file or a whole
// folder). It returns how much disk space that freed and how many files were
// deliberately kept.
//
// Files holding changes that have not reached Drive yet are always kept: the
// cache copy is the only copy of those bytes. Everything else is safe to delete -
// rclone has no remote-control command for freeing cached data (vfs/forget only
// drops directory listings), and it simply downloads the data again on the next
// access. Since this really deletes files, the target is checked to name
// something inside the cache.
func (c Cache) Evict(rel string) (freed int64, kept int, err error) {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" || rel == "." {
		return 0, 0, errors.New("refusing to evict the whole cache")
	}
	if !c.Usable() {
		return 0, 0, errors.New("cache location unknown")
	}
	data, meta := c.DataPath(rel), c.MetaPath(rel)
	if !within(filepath.Join(c.Dir, "vfs", c.Remote), data) ||
		!within(filepath.Join(c.Dir, "vfsMeta", c.Remote), meta) {
		return 0, 0, fmt.Errorf("%q points outside the cache", rel)
	}

	fi, err := os.Stat(data)
	if os.IsNotExist(err) {
		return 0, 0, nil // nothing downloaded, nothing to free
	}
	if err != nil {
		return 0, 0, err
	}
	if !fi.IsDir() {
		return c.evictFile(rel, fi)
	}

	err = filepath.WalkDir(data, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil // unreadable entries are left alone
		}
		child, err := filepath.Rel(data, path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		n, k, err := c.evictFile(filepath.Join(rel, child), info)
		freed += n
		kept += k
		return err
	})
	if err != nil {
		return freed, kept, err
	}
	if kept == 0 {
		// Nothing had to stay, so the now-empty directory tree can go too.
		if err := os.RemoveAll(data); err != nil {
			return freed, kept, err
		}
		if err := os.RemoveAll(meta); err != nil {
			return freed, kept, err
		}
	}
	return freed, kept, nil
}

// evictFile removes one cached file unless it holds unsent local changes.
func (c Cache) evictFile(rel string, fi os.FileInfo) (freed int64, kept int, err error) {
	if m, ok := readMeta(c.MetaPath(rel)); ok && m.Dirty {
		return 0, 1, nil
	}
	freed = allocatedBytes(fi)
	if err := os.Remove(c.DataPath(rel)); err != nil && !os.IsNotExist(err) {
		return 0, 0, err
	}
	if err := os.Remove(c.MetaPath(rel)); err != nil && !os.IsNotExist(err) {
		return freed, 0, err
	}
	return freed, 0, nil
}

// within reports whether path stays inside root.
func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cachedRange is one downloaded byte range of a cache file.
type cachedRange struct {
	Pos  int64 `json:"Pos"`
	Size int64 `json:"Size"`
}

// meta is the subset of rclone's VFS cache metadata we use.
type meta struct {
	Size  int64         `json:"Size"`
	Dirty bool          `json:"Dirty"`
	Rs    []cachedRange `json:"Rs"`
}

// readMeta reads rclone's cache metadata for one file.
func readMeta(path string) (meta, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta{}, false
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return meta{}, false
	}
	return m, true
}

// rangesCoverAll reports whether the recorded byte ranges span the whole file.
func rangesCoverAll(rs []cachedRange, size int64) bool {
	if len(rs) == 0 {
		return false
	}
	sorted := append(rs[:0:0], rs...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Pos < sorted[b].Pos })
	var reached int64
	for _, r := range sorted {
		if r.Pos > reached {
			return false // gap
		}
		if end := r.Pos + r.Size; end > reached {
			reached = end
		}
	}
	return reached >= size
}
