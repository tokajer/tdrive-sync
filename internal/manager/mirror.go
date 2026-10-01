// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/i18n"
)

// Resync modes used for the situations that force a full bisync
// reconciliation. Both are valid --resync-mode values in the bundled rclone.
const (
	// firstSyncResyncMode is used the very first time this folder is bisynced
	// when the local dir is empty: nothing to reconcile against yet, so the
	// cloud is authoritative. See firstSyncMode for the non-empty case.
	firstSyncResyncMode = "path1"
	// recoveryResyncMode is used for auto-recovery after repeated failures,
	// and for a first sync whose local dir is already populated. Preferring
	// the newer file - rather than always the cloud - means local edits are
	// not silently overwritten by a stale or absent cloud copy.
	recoveryResyncMode = "newer"
)

// firstSyncMode picks the resync mode for the very first bisync of a folder.
// The cloud is only treated as authoritative when there is nothing local to
// lose: if the init marker is missing (e.g. a cleared ~/.cache) but the local
// dir already holds files, forcing "path1" would silently overwrite them, so
// that case reconciles as "newer" instead.
func firstSyncMode(local string) string {
	entries, err := os.ReadDir(local)
	if err != nil || len(entries) == 0 {
		return firstSyncResyncMode
	}
	return recoveryResyncMode
}

// mirrorRunner keeps a two-way-synced local copy using rclone bisync. It
// reconciles
//   - on a timer (periodic remote polling),
//   - on demand (SyncNow), and
//   - immediately after local changes detected via inotify (watchLocal).
//
// After several consecutive failures it forces a full resync to recover, and it
// cleans up stale locks left behind by crashed runs before every attempt.
type mirrorRunner struct {
	m *Manager
	// trigger asks for a reconcile now. Buffered, so a signal never blocks and
	// several requests collapse into one run.
	trigger chan struct{}
	// busy is true for the duration of a bisync run, so the local-change
	// watcher can ignore the file events that run's own writes produce.
	// Without it, every run that downloaded anything would immediately queue a
	// redundant one.
	busy atomic.Bool
}

// mirrorMode is the full two-way-synced local copy.
type mirrorMode struct{ m *Manager }

func (mm mirrorMode) newRunner() Runner { return newMirrorRunner(mm.m) }

func newMirrorRunner(m *Manager) *mirrorRunner {
	return &mirrorRunner{m: m, trigger: make(chan struct{}, 1)}
}

// SyncNow asks for an immediate reconciliation.
func (r *mirrorRunner) SyncNow() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

func (r *mirrorRunner) Run(ctx context.Context) {
	m := r.m
	local := m.cfg.LocalDir()
	if err := ensureDir(local); err != nil {
		m.setState(StateError, i18n.T("status.sync_dir_error", err))
		return
	}
	workdir := filepath.Join(m.cacheDir, "bisync")
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		m.setState(StateError, i18n.T("status.sync_dir_error", err))
		return
	}
	marker := filepath.Join(m.cacheDir, "bisync-init-"+sanitize(local))

	// Real-time local watching: nudge the trigger on local changes. Waited for
	// below so Run never returns while it is still running.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchLocal(ctx, local, m.log, r.trigger, &r.busy)
	}()
	defer wg.Wait()

	// Tracked in memory as well as on disk: a marker write failure must not
	// make every single run look like the first one again.
	initialized := fileExists(marker)
	failures := 0
	for ctx.Err() == nil {
		// Stale lock detection: drop locks older than the bisync --max-lock
		// window, which can only come from a crashed/killed run.
		r.cleanStaleLocks(workdir, mirrorLockMaxAge)

		resyncMode := ""
		switch {
		case failures >= maxMirrorFailures:
			resyncMode = recoveryResyncMode
			m.log.Logf("auto-recovery: full resync after %d failed attempts", failures)
			m.setState(StateSyncing, i18n.T("status.auto_recovery"))
		case !initialized:
			resyncMode = firstSyncMode(local)
			m.setState(StateSyncing, i18n.T("status.first_sync"))
		default:
			m.setState(StateSyncing, i18n.T("status.syncing"))
		}

		r.busy.Store(true)
		cmd, done, err := m.startProc(
			m.engine.BisyncArgs(local, workdir, m.cfg.ConflictMode() == config.ConflictManual, resyncMode), "bisync")
		if err != nil {
			r.busy.Store(false)
			m.setState(StateError, i18n.T("status.sync_error_retry"))
			if sleepCtx(ctx, mirrorRetryInterval) {
				return
			}
			continue
		}

		var runErr error
		select {
		case <-ctx.Done():
			m.terminate(cmd, done)
			return
		case runErr = <-done:
		}
		r.busy.Store(false)

		if runErr == nil {
			if resyncMode != "" {
				initialized = true
				r.writeMarker(marker)
			}
			failures = 0
			m.status.Update(func(rt *Runtime) {
				rt.LastSync = time.Now()
				rt.State = StateIdle
				rt.Message = i18n.T("status.up_to_date")
			})
		} else {
			failures++
			r.reportFailure(failures)
		}

		if r.waitForNextRun(ctx, runErr != nil) {
			return
		}
	}
}

// writeMarker records that the local dir has completed at least one full
// resync, so later runs are treated as an established mirror rather than a
// first sync.
func (r *mirrorRunner) writeMarker(marker string) {
	if err := os.WriteFile(marker, []byte("ok\n"), 0o644); err != nil {
		r.m.log.Errorf("could not write the bisync init marker: %v", err)
	}
}

// reportFailure sets the error state and tells the user, distinguishing a single
// hiccup from the run of failures that triggers a full resync.
func (r *mirrorRunner) reportFailure(failures int) {
	if failures >= maxMirrorFailures {
		r.m.setState(StateError, i18n.T("status.repeated_errors"))
		r.m.notifier.Notify(i18n.T("notify.repeated_errors"))
		return
	}
	r.m.setState(StateError, i18n.T("status.sync_error_retry"))
	r.m.notifier.Notify(i18n.T("notify.sync_error_retry"))
}

// waitForNextRun sleeps until the next reconcile is due, an explicit sync is
// requested, or the context ends. It reports true when the runner should stop.
//
// After a failure the wait is shortened, so recovery does not have to sit out a
// full polling interval.
func (r *mirrorRunner) waitForNextRun(ctx context.Context, afterFailure bool) bool {
	wait := time.Duration(r.m.cfg.MirrorIntervalSec()) * time.Second
	if wait < mirrorMinInterval {
		wait = mirrorMinInterval
	}
	if afterFailure && wait > mirrorRetryInterval {
		wait = mirrorRetryInterval
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
	case <-r.trigger:
	}
	return false
}

// cleanStaleLocks removes bisync lock files in dir older than maxAge. rclone
// renews its lock within --max-lock, so anything older is a leftover from a
// process that crashed or was killed.
func (r *mirrorRunner) cleanStaleLocks(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), ".lck") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			r.m.log.Logf("removed stale lock file: %s", e.Name())
		}
	}
}
