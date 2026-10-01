// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/i18n"
)

// SetOffline pins a Drive-relative path for offline availability, or releases it
// again (stream mode only).
//
// Releasing does more than drop the pin: it deletes the local copy, so the space
// it used comes back - "free up space" in the file manager's context menu ends up
// here too, for files that were merely downloaded rather than pinned.
func (m *Manager) SetOffline(path string, on bool) error {
	if err := m.cfg.SetOffline(path, on); err != nil {
		return err
	}
	m.status.Notify()

	if m.cfg.Mode() != config.ModeStream {
		// Mirror mode keeps a full local copy regardless of pins: there is no
		// cache to warm or free.
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
		if on {
			m.warmPath(ctx, path)
			return
		}
		if freed := m.freePath(ctx, path); freed > 0 {
			m.log.Logf("freed %s by releasing %s", humanBytes(freed), path)
			m.notifier.Notify(i18n.T("notify.space_freed", humanBytes(freed)))
		}
		// A late notify after Shutdown cancelled the daemon context must not
		// re-publish an active file-manager.json.
		if ctx.Err() == nil {
			m.status.Notify()
		}
	}()
	return nil
}

// warmOffline reads every pinned path through the mount.
func (m *Manager) warmOffline(ctx context.Context) {
	for _, p := range m.cfg.OfflinePaths() {
		if ctx.Err() != nil {
			return
		}
		m.warmPath(ctx, p)
	}
}

// warmPath reads every file under a Drive-relative path through the mount so it
// gets stored in the VFS cache and stays available offline.
func (m *Manager) warmPath(ctx context.Context, rel string) {
	root := filepath.Join(m.cfg.LocalDir(), rel)
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
func (m *Manager) freePath(ctx context.Context, rel string) int64 {
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
