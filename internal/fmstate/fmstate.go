// Package fmstate publishes what a file-manager integration needs to show a
// per-file sync indicator ("streamed" vs "available offline"), and resolves that
// state for a local path.
//
// The published file is the entire contract with the Dolphin overlay plugin: the
// plugin reads it once, watches it for changes, and then resolves every file it
// is asked about on its own from rclone's VFS cache on disk. Keeping the file
// manager off any IPC path matters — an overlay lookup runs for every visible
// item and must not block.
//
// rclone's cache layout below --cache-dir is
//
//	vfs/<remote>/<drive-relative path>      the (possibly sparse) data file
//	vfsMeta/<remote>/<drive-relative path>  JSON: size, cached byte ranges, dirty
//
// so a file's state is one stat plus one small JSON read.
package fmstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tdrive-sync/internal/config"
)

// Version is the format version of the published file. The plugin refuses
// anything it does not know.
const Version = 1

// State is the per-file sync state an indicator renders.
type State string

const (
	// Unknown means the path is not inside the sync folder (no indicator).
	Unknown State = ""
	// Cloud means nothing is cached locally: opening the file downloads it.
	Cloud State = "cloud"
	// Partial means some but not all of the file is cached.
	Partial State = "partial"
	// Cached means the whole file is in the local cache and usable offline,
	// without having been explicitly pinned.
	Cached State = "cached"
	// Pinned means the file (or a parent folder) is marked "keep offline" and
	// the local copy is complete.
	Pinned State = "pinned"
	// Pinning means it is marked "keep offline" but the download is not finished.
	Pinning State = "pinning"
	// Uploading means the local copy has changes not yet written back to Drive.
	Uploading State = "uploading"
	// Local means mirror mode: everything is a real local copy anyway.
	Local State = "local"
)

// Info is the snapshot handed to the file-manager integration.
type Info struct {
	// Version is the format version (see Version).
	Version int `json:"version"`
	// Active reports whether the daemon is running and syncing (false while
	// signed out, paused or shut down).
	Active bool `json:"active"`
	// Mode is the sync mode ("stream" or "mirror").
	Mode string `json:"mode"`
	// State mirrors the coarse daemon state ("idle", "syncing", "error", …).
	State string `json:"state"`
	// Root is the absolute mount point (stream) or mirror root.
	Root string `json:"root"`
	// CacheDir is rclone's --cache-dir, holding vfs/ and vfsMeta/.
	CacheDir string `json:"cache_dir"`
	// Remote is the rclone remote name (the first path element inside the cache).
	Remote string `json:"remote"`
	// Exec is the path of the running binary, so the integration can invoke the
	// CLI (e.g. to pin a folder from a context menu).
	Exec string `json:"exec"`
	// Pinned are the Drive-relative paths marked "keep offline".
	Pinned []string `json:"pinned"`
}

// Path returns the location of the published file.
func Path() (string, error) {
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "file-manager.json"), nil
}

// Publisher writes Info to disk, skipping writes that would not change the
// file. The integration watches that file, so needless rewrites would mean
// needless refreshes.
type Publisher struct {
	last []byte
}

// Publish atomically writes i unless the identical content is already on disk.
func (p *Publisher) Publish(i Info) error {
	i.Version = Version
	if i.Pinned == nil {
		i.Pinned = []string{}
	}
	data, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if string(data) == string(p.last) {
		return nil
	}
	path, err := Path()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	p.last = data
	return nil
}

// Load reads the published file.
func Load() (Info, error) {
	path, err := Path()
	if err != nil {
		return Info{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	var i Info
	if err := json.Unmarshal(data, &i); err != nil {
		return Info{}, err
	}
	return i, nil
}

// Rel converts an absolute local path into its Drive-relative form. It reports
// false for anything outside the sync folder, and for the sync folder itself
// (which has no meaningful indicator of its own).
func (i Info) Rel(abs string) (string, bool) {
	if i.Root == "" {
		return "", false
	}
	root := filepath.Clean(i.Root)
	p := filepath.Clean(abs)
	if p == root {
		return "", false
	}
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", false
	}
	return strings.TrimPrefix(p, root+string(filepath.Separator)), true
}

// IsPinned reports whether a Drive-relative path is marked "keep offline",
// either directly or through one of its parent folders.
func (i Info) IsPinned(rel string) bool {
	for _, p := range i.Pinned {
		if p == "" {
			continue
		}
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// Resolve returns the state of an absolute local path.
func (i Info) Resolve(abs string) State {
	rel, ok := i.Rel(abs)
	if !ok {
		return Unknown
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return Unknown
	}
	return i.ResolveRel(rel, fi.IsDir())
}

// ResolveRel returns the state of a Drive-relative path.
//
// Folders are only approximated: a folder counts as Partial as soon as anything
// below it has been cached, because deciding "everything inside is local" would
// mean walking the whole subtree on every lookup.
func (i Info) ResolveRel(rel string, isDir bool) State {
	if rel == "" {
		return Unknown
	}
	if i.Mode == string(config.ModeMirror) {
		return Local
	}
	pinned := i.IsPinned(rel)
	if isDir {
		if pinned {
			return Pinned
		}
		if _, err := os.Stat(i.DataPath(rel)); err == nil {
			return Partial
		}
		return Cloud
	}
	c := i.inspect(rel)
	switch {
	case !c.found || c.empty:
		if pinned {
			return Pinning
		}
		return Cloud
	case c.dirty:
		return Uploading
	case !c.complete:
		if pinned {
			return Pinning
		}
		return Partial
	case pinned:
		return Pinned
	default:
		return Cached
	}
}

// DataPath is where the cached content of a Drive-relative path lives.
func (i Info) DataPath(rel string) string {
	return filepath.Join(i.CacheDir, "vfs", i.Remote, rel)
}

// MetaPath is where rclone keeps the cache metadata of a Drive-relative path.
func (i Info) MetaPath(rel string) string {
	return filepath.Join(i.CacheDir, "vfsMeta", i.Remote, rel)
}

// Evict deletes the local copy of a Drive-relative path (a single file or a whole
// folder). It returns how much disk space that freed and how many files were
// deliberately kept.
//
// Files holding changes that have not reached Drive yet are always kept: the
// cache copy is the only copy of those bytes. Everything else is safe to delete –
// rclone has no remote-control command for freeing cached data (vfs/forget only
// drops directory listings), and it simply downloads the data again on the next
// access. Since this really deletes files, the target is checked to name
// something inside the cache.
func (i Info) Evict(rel string) (freed int64, kept int, err error) {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" || rel == "." {
		return 0, 0, errors.New("refusing to evict the whole cache")
	}
	if i.CacheDir == "" || i.Remote == "" {
		return 0, 0, errors.New("cache location unknown")
	}
	data, meta := i.DataPath(rel), i.MetaPath(rel)
	if !within(filepath.Join(i.CacheDir, "vfs", i.Remote), data) ||
		!within(filepath.Join(i.CacheDir, "vfsMeta", i.Remote), meta) {
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
		return i.evictFile(rel, fi)
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
		n, k, err := i.evictFile(filepath.Join(rel, child), info)
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
func (i Info) evictFile(rel string, fi os.FileInfo) (freed int64, kept int, err error) {
	if m, ok := readMeta(i.MetaPath(rel)); ok && m.Dirty {
		return 0, 1, nil
	}
	freed = allocatedBytes(fi)
	if err := os.Remove(i.DataPath(rel)); err != nil && !os.IsNotExist(err) {
		return 0, 0, err
	}
	if err := os.Remove(i.MetaPath(rel)); err != nil && !os.IsNotExist(err) {
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

// cacheInfo is what the local cache says about one file.
type cacheInfo struct {
	found    bool // there is a cache file at all
	empty    bool // it exists but holds no data yet
	dirty    bool // it holds local changes not yet sent to Drive
	complete bool // the whole file is there
}

// inspect looks at the cached copy of a Drive-relative file.
//
// Completeness cannot be read off rclone's recorded ranges alone: rclone leaves
// "Rs" empty both for a file it has fully downloaded and for one it has barely
// touched. What is reliable is how much of the sparse cache file is actually
// allocated, with the first hole as the tie-breaker.
func (i Info) inspect(rel string) cacheInfo {
	var c cacheInfo
	dataPath := i.DataPath(rel)
	fi, err := os.Stat(dataPath)
	if err != nil || fi.IsDir() {
		return c
	}
	c.found = true

	size := fi.Size()
	m, haveMeta := readMeta(i.MetaPath(rel))
	if haveMeta {
		c.dirty = m.Dirty
		if m.Size > 0 {
			size = m.Size
		}
	}
	if size <= 0 {
		c.complete = true
		return c
	}

	alloc := allocatedBytes(fi)
	switch {
	case covers(m.Rs, size):
		// rclone did record the ranges: they are authoritative.
		c.complete = true
	case alloc >= size:
		c.complete = true
	case alloc == 0:
		c.empty = true
	default:
		hole, err := firstHole(dataPath, size)
		switch {
		case err != nil:
		case hole >= size:
			c.complete = true
		case hole == 0:
			c.empty = true
		}
	}
	return c
}

// readMeta reads rclone's cache metadata for one file.
func readMeta(path string) (meta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return meta{}, false
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return meta{}, false
	}
	return m, true
}

// covers reports whether the recorded byte ranges span the whole file.
func covers(rs []cachedRange, size int64) bool {
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
