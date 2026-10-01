// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"tdrive-sync/internal/i18n"
)

// SetOffline pins a Drive-relative path for offline availability, or releases it
// again. The pin is always recorded; only a mode with a cache (a pinner) acts on
// it - mirror mode keeps a full local copy regardless.
//
// Releasing does more than drop the pin: it deletes the local copy, so the space
// it used comes back - "free up space" in the file manager's context menu ends up
// here too, for files that were merely downloaded rather than pinned.
func (m *Manager) SetOffline(path string, on bool) error {
	if err := m.cfg.SetOffline(path, on); err != nil {
		return err
	}
	m.status.Notify()

	pin, ok := m.currentMode().(pinner)
	if !ok {
		return nil
	}

	// Bounded by the daemon's own context, not the runner's: a pin or release
	// must outlive SetMode/SetLocalDir restarting the runner mid-warm.
	m.mu.Lock()
	parent := m.parent
	m.mu.Unlock()
	if parent == nil { // Start was never called (CLI, tests)
		parent = context.Background()
	}

	go func() {
		ctx, cancel := context.WithTimeout(parent, evictTimeout)
		defer cancel()
		pin.applyPin(ctx, path, on)
	}()
	return nil
}

// streamMode is the mounted Drive: files on demand, pinned paths kept in
// rclone's VFS cache.
type streamMode struct{ m *Manager }

func (s streamMode) newRunner() Runner { return newStreamRunner(s.m) }

// applyPin implements pinner.
func (s streamMode) applyPin(ctx context.Context, rel string, on bool) {
	m := s.m
	if on {
		s.warmPath(ctx, rel)
		return
	}
	if freed := s.freePath(ctx, rel); freed > 0 {
		m.log.Logf("freed %s by releasing %s", humanBytes(freed), rel)
		m.notifier.Notify(i18n.T("notify.space_freed", humanBytes(freed)))
	}
	// Safe after Shutdown too: a closed status store delivers nothing.
	m.status.Notify()
}

// warmOffline reads every pinned path through the mount.
func (s streamMode) warmOffline(ctx context.Context) {
	for _, p := range s.m.cfg.OfflinePaths() {
		if ctx.Err() != nil {
			return
		}
		s.warmPath(ctx, p)
	}
}

// warmPath reads every file under a Drive-relative path through the mount so it
// gets stored in the VFS cache and stays available offline.
func (s streamMode) warmPath(ctx context.Context, rel string) {
	root := filepath.Join(s.m.cfg.LocalDir(), rel)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		_, _ = io.Copy(io.Discard, f)
		_ = f.Close()
		return nil
	})
}

// freePath drops the local copy of a Drive-relative path and reports how much
// disk space that freed.
func (s streamMode) freePath(ctx context.Context, rel string) int64 {
	m := s.m
	freed, kept, err := m.cache.Evict(rel)
	if err != nil {
		m.log.Errorf("could not free %s: %v", rel, err)
		return 0
	}
	if kept > 0 {
		m.log.Logf("kept %d file(s) below %s: their changes have not reached Drive yet", kept, rel)
	}
	// Let rclone re-read the listing so the freed items show up as online-only.
	// Files and directories go through different parameters.
	fi, statErr := os.Stat(filepath.Join(m.cfg.LocalDir(), rel))
	if statErr == nil && !fi.IsDir() {
		_ = m.ctl.ForgetFile(ctx, rel)
	} else {
		_ = m.ctl.Forget(ctx, rel)
	}
	return freed
}
