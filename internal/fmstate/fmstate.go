// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fmstate publishes what a file-manager integration needs to show a
// per-file sync indicator ("streamed" vs "available offline"), and resolves that
// state for a local path.
//
// The published file is the entire contract with the Dolphin overlay plugin: the
// plugin reads it once, watches it for changes, and then resolves every file it
// is asked about on its own from rclone's VFS cache on disk. Keeping the file
// manager off any IPC path matters - an overlay lookup runs for every visible
// item and must not block.
//
// The cache itself is read (and freed) through Cache in cache.go; this file
// holds the published snapshot and the state rules on top of it.
package fmstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tdrive-sync/internal/fsutil"
	"tdrive-sync/internal/pins"
	"tdrive-sync/internal/xdg"
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

// ModeMirror is the value of Info.Mode in mirror mode. It is config.ModeMirror
// on the wire; spelled out here so this package, the plugin contract, depends
// on nothing but the rules it applies.
const ModeMirror = "mirror"

// Info is the snapshot handed to the file-manager integration. Its JSON form is
// the wire format the C++ plugin parses, so field names are part of the
// contract and change only together with Version.
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

// Cache returns the view of rclone's VFS cache this snapshot points at.
func (i Info) Cache() Cache { return Cache{Dir: i.CacheDir, Remote: i.Remote} }

// Path returns the default location of the published file.
func Path() (string, error) {
	dir, err := xdg.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "file-manager.json"), nil
}

// Publisher writes Info to disk, skipping writes that would not change the
// file. The integration watches that file, so needless rewrites would mean
// needless refreshes.
//
// It is safe for concurrent use: in the daemon every status change publishes,
// and those arrive from the stats poller, the sync runners and the HTTP
// handlers at the same time.
type Publisher struct {
	mu   sync.Mutex
	path string
	last []byte
}

// NewPublisher returns a publisher writing to path. An empty path resolves to
// the default location, which is what the daemon uses; tests pass their own so
// two publishers cannot fight over one file.
func NewPublisher(path string) (*Publisher, error) {
	if path == "" {
		p, err := Path()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return &Publisher{path: path}, nil
}

// Publish atomically writes i unless the identical content is already on disk.
func (p *Publisher) Publish(i Info) error {
	i.Version = Version
	if i.Pinned == nil {
		i.Pinned = []string{}
	}
	raw, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	p.mu.Lock()
	defer p.mu.Unlock()
	if string(raw) == string(p.last) {
		return nil
	}
	if err := fsutil.WriteAtomic(p.path, raw, 0o644); err != nil {
		return err
	}
	p.last = raw
	return nil
}

// Load reads the published file.
func Load() (Info, error) {
	path, err := Path()
	if err != nil {
		return Info{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	var i Info
	if err := json.Unmarshal(raw, &i); err != nil {
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

// IsPinned reports whether rel is marked "keep offline". Delegates to
// pins.Has so a pin written by an older version ("/Docs/") is normalised
// exactly as it is in the daemon.
func (i Info) IsPinned(rel string) bool {
	return pins.Has(i.Pinned, rel)
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
	if i.Mode == ModeMirror {
		return Local
	}
	cache := i.Cache()
	pinned := i.IsPinned(rel)
	if isDir {
		if pinned {
			return Pinned
		}
		if cache.DirHasData(rel) {
			return Partial
		}
		return Cloud
	}
	c := cache.Inspect(rel)
	switch {
	case !c.Found || c.Empty:
		if pinned {
			return Pinning
		}
		return Cloud
	case c.Dirty:
		return Uploading
	case !c.Complete:
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
