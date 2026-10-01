// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"sync"
	"time"

	"tdrive-sync/internal/i18n"
)

// streamRunner mounts the whole Drive and keeps offline-pinned paths warm. It
// restarts the mount if it dies, until its context is cancelled.
type streamRunner struct {
	m *Manager
	// warm carries requests to read the pinned paths through the mount (served by warmLoop).
	warm chan struct{}
	// refresh carries requests for a full re-read of the Drive listing before warming (SyncNow).
	refresh chan struct{}

	// Timing, taken from the package constants. They are fields so a test can
	// shorten them; nothing else changes them.
	readyTimeout time.Duration
	retryDelay   time.Duration
}

func newStreamRunner(m *Manager) *streamRunner {
	return &streamRunner{
		m:            m,
		warm:         make(chan struct{}, 1),
		refresh:      make(chan struct{}, 1),
		readyTimeout: mountReadyTimeout,
		retryDelay:   remountDelay,
	}
}

// SyncNow re-reads the Drive listing and refreshes the pinned paths. The
// refresh case in warmLoop already warms afterwards, so no separate nudge is
// needed here.
func (r *streamRunner) SyncNow() {
	select {
	case r.refresh <- struct{}{}:
	default:
	}
}

// nudgeWarm asks the warming loop for a pass, dropping the request if one is
// already queued.
func (r *streamRunner) nudgeWarm() {
	select {
	case r.warm <- struct{}{}:
	default:
	}
}

func (r *streamRunner) Run(ctx context.Context) {
	m := r.m
	mp := m.cfg.LocalDir()
	if err := ensureDir(mp); err != nil {
		m.setState(StateError, i18n.T("status.mount_dir_error", err))
		return
	}

	// Both helpers live for the whole runner, not for one mount; waited for
	// below so Run never returns while either is still running.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); m.pollStats(ctx) }()
	go func() { defer wg.Done(); r.warmLoop(ctx) }()
	defer wg.Wait()

	firstReady := false
	for ctx.Err() == nil {
		m.setState(StateStarting, i18n.T("status.mounting"))
		cmd, done, err := m.startProc(m.engine.MountArgs(mp, m.rcAddr, m.cacheDir), "mount")
		if err != nil {
			m.setState(StateError, i18n.T("status.mount_start_error", err))
			if sleepCtx(ctx, r.retryDelay) {
				return
			}
			continue
		}

		if m.waitMountReady(ctx, r.readyTimeout) {
			m.setState(StateIdle, i18n.T("status.up_to_date"))
			if !firstReady {
				m.notifier.Notify(i18n.T("notify.drive_ready", mp))
				firstReady = true
			}
			r.nudgeWarm()
		} else if ctx.Err() == nil {
			// The mount process is alive but its control server never answered,
			// so we cannot tell whether the Drive is usable and cannot warm the
			// pinned paths. Saying so beats a spinner that never resolves: the
			// usual cause is something else occupying the control port.
			m.setState(StateError, i18n.T("status.mount_no_control", m.rcAddr))
		}

		select {
		case <-ctx.Done():
			m.terminate(cmd, done)
			m.unmount(mp)
			return
		case <-done:
			m.unmount(mp)
			if ctx.Err() != nil {
				return
			}
			m.setState(StateError, i18n.T("status.connection_lost"))
		}
		if sleepCtx(ctx, r.retryDelay) {
			return
		}
	}
}

// warmLoop reads the offline-pinned paths through the mount whenever asked, and
// re-reads the Drive listing first when that was explicitly requested (see
// SyncNow). ctx bounds both: nothing it starts can outlive the runner.
func (r *streamRunner) warmLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.warm:
			r.warmNow(ctx)
		case <-r.refresh:
			rctx, cancel := context.WithTimeout(ctx, warmTimeout)
			if err := r.m.ctl.Refresh(rctx, ""); err != nil {
				r.m.log.Logf("could not refresh the Drive listing: %v", err)
			}
			cancel()
			r.warmNow(ctx)
		}
	}
}

// warmNow runs one warming pass, bounded by warmTimeout.
func (r *streamRunner) warmNow(ctx context.Context) {
	wctx, cancel := context.WithTimeout(ctx, warmTimeout)
	defer cancel()
	r.m.warmOffline(wctx)
}

// waitMountReady polls the mount's control server until it answers or timeout.
func (m *Manager) waitMountReady(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithTimeout(ctx, rcCallTimeout)
		ok := m.ctl.Ping(cctx)
		cancel()
		if ok {
			return true
		}
		if sleepCtx(ctx, mountReadyPoll) {
			return false
		}
	}
	return false
}

// pollStats mirrors the mount's transfer statistics into the status.
func (m *Manager) pollStats(ctx context.Context) {
	t := time.NewTicker(statsInterval)
	defer t.Stop()
	var lastReported string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, rcCallTimeout)
		s, err := m.ctl.CoreStats(cctx)
		cancel()
		if err != nil {
			continue
		}
		if s.LastError != "" && s.LastError != lastReported {
			lastReported = s.LastError
			m.log.Errorf("%s", s.LastError)
		}
		// UpdateIf, not Update: an idle mount polls unchanged stats every tick,
		// and delivering those to every listener would be pure noise.
		m.status.UpdateIf(func(rt *Runtime) bool {
			before := *rt
			rt.Bytes = s.Bytes
			rt.Speed = s.Speed
			rt.Errors = s.Errors
			applyTransferState(rt, s.Transferring > 0)
			return rt.Bytes != before.Bytes || rt.Speed != before.Speed ||
				rt.Errors != before.Errors || rt.State != before.State || rt.Message != before.Message
		})
	}
}

// applyTransferState moves the coarse state between idle and syncing to match
// what the mount is doing.
//
// Only those two states are touched: paused, starting, disconnected and error
// are decided elsewhere, and a transfer finishing must not quietly clear them.
func applyTransferState(rt *Runtime, transferring bool) {
	if !settledState(rt.State) {
		return
	}
	if transferring {
		rt.State = StateSyncing
		rt.Message = i18n.T("status.syncing")
		return
	}
	if rt.State == StateSyncing {
		rt.LastSync = time.Now()
	}
	rt.State = StateIdle
	rt.Message = i18n.T("status.up_to_date")
}

// settledState reports whether the mount is up and its state is free to follow
// the transfer activity.
func settledState(s State) bool { return s == StateIdle || s == StateSyncing }
