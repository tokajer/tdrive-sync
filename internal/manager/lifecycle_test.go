// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tdrive-sync/internal/config"
)

// trackingRunner stands in for a real mode runner (stream/mirror) and counts
// how many instances are simultaneously inside Run, so a lifecycle bug that
// starts a second runner before the first one is actually stopped shows up
// directly as more than one concurrently active instance - the "two mounts on
// one folder" failure the lifecycle lock exists to prevent.
type trackingRunner struct {
	active *int32
	max    *int32
}

func (r *trackingRunner) Run(ctx context.Context) {
	n := atomic.AddInt32(r.active, 1)
	defer atomic.AddInt32(r.active, -1)
	for {
		old := atomic.LoadInt32(r.max)
		if n <= old || atomic.CompareAndSwapInt32(r.max, old, n) {
			break
		}
	}
	<-ctx.Done()
}

func (r *trackingRunner) SyncNow() {}

// withTrackingRunner swaps the stream-mode runner builder for one that counts
// concurrently active instances, restoring the original on cleanup. Tests in
// this package run sequentially (none opts into t.Parallel), so mutating the
// package-level runners map for the duration of one test is safe.
func withTrackingRunner(t *testing.T) (active, max *int32) {
	t.Helper()
	active, max = new(int32), new(int32)
	orig := runners[config.ModeStream]
	runners[config.ModeStream] = func(*Manager) Runner {
		return &trackingRunner{active: active, max: max}
	}
	t.Cleanup(func() { runners[config.ModeStream] = orig })
	return active, max
}

// TestLifecycleNeverRunsTwoRunners asserts that hammering SetMode/SetLocalDir
// concurrently never runs more than one runner at once, and Shutdown leaves
// none running.
func TestLifecycleNeverRunsTwoRunners(t *testing.T) {
	m := newTestManager(t)
	if err := m.cfg.SetAccountEmail("test@example.com"); err != nil {
		t.Fatal(err)
	}
	active, max := withTrackingRunner(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.mu.Lock()
	m.parent = ctx
	m.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = m.SetLocalDir(filepath.Join(t.TempDir(), "drive"))
		}()
		go func() {
			defer wg.Done()
			_ = m.SetMode(config.ModeStream)
		}()
	}
	wg.Wait()

	m.Shutdown()

	deadline := time.Now().Add(testTimeout)
	for atomic.LoadInt32(active) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(active); got != 0 {
		t.Fatalf("Shutdown left %d runner(s) active", got)
	}
	if got := atomic.LoadInt32(max); got > 1 {
		t.Errorf("%d runners were active at the same time, want at most 1", got)
	}
}

// TestPauseBlocksStartUntilResume asserts that a config change made while
// paused does not start a runner, and takes effect only once Resume starts one.
func TestPauseBlocksStartUntilResume(t *testing.T) {
	m := newTestManager(t)
	if err := m.cfg.SetAccountEmail("test@example.com"); err != nil {
		t.Fatal(err)
	}
	engine := newFakeEngine()
	m.engine = engine
	m.ctl = &fakeControl{reachable: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// newTestManager leaves m.rc nil (these tests never touch the rclone
	// binary), so the production entry point - Start, which calls
	// m.rc.RemoteExists() - cannot be used here. restart() needs no rclone
	// client and starts a runner exactly the same way, so it doubles as
	// "first start".
	m.mu.Lock()
	m.parent = ctx
	m.mu.Unlock()
	m.restart()
	engine.runOnce(t, nil)

	m.Pause()
	if !m.Paused() {
		t.Fatal("Pause() did not mark the manager paused")
	}
	waitForState(t, m, StatePaused)

	newDir := filepath.Join(t.TempDir(), "drive2")
	if err := m.SetLocalDir(newDir); err != nil {
		t.Fatal(err)
	}
	if err := m.SetMode(config.ModeStream); err != nil {
		t.Fatal(err)
	}

	select {
	case <-engine.started:
		t.Fatal("a config change while paused started a runner")
	case <-time.After(50 * time.Millisecond):
	}
	if !m.Paused() {
		t.Error("SetMode/SetLocalDir must not silently clear the pause")
	}

	m.Resume()
	if m.Paused() {
		t.Error("Resume() did not clear paused")
	}
	select {
	case <-engine.started:
	case <-time.After(testTimeout):
		t.Fatal("Resume() did not start a runner")
	}

	select {
	case engine.results <- nil:
	case <-time.After(testTimeout):
	}
	m.Shutdown()
}

// TestResumeIdempotentWhileRunning asserts that Resume is a no-op once a
// runner is already active: repeated and concurrent calls must never start a
// second runner, and Pause afterwards must not hang.
func TestResumeIdempotentWhileRunning(t *testing.T) {
	m := newTestManager(t)
	if err := m.cfg.SetAccountEmail("test@example.com"); err != nil {
		t.Fatal(err)
	}
	active, max := withTrackingRunner(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.mu.Lock()
	m.parent = ctx
	m.mu.Unlock()
	m.restart()

	deadline := time.Now().Add(testTimeout)
	for atomic.LoadInt32(active) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(active); got != 1 {
		t.Fatalf("expected one runner active after the initial start, got %d", got)
	}

	m.Resume()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Resume()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(max); got > 1 {
		t.Errorf("%d runners were active at the same time, want at most 1", got)
	}

	done := make(chan struct{})
	go func() { defer close(done); m.Pause() }()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("Pause() hung after repeated Resume calls")
	}

	m.Shutdown()
}
