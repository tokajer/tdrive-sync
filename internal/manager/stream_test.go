// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestStreamReportsUnreachableControl covers the failure that used to be
// invisible: the mount process is alive but its control server never answers,
// so nothing can be refreshed and no pinned path can be warmed. The status used
// to stay "starting" forever, with no error and no log line - a spinner that
// never resolves. The usual cause is something else holding the control port.
func TestStreamReportsUnreachableControl(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	m.ctl = &fakeControl{reachable: false}

	r := newStreamRunner(m)
	r.readyTimeout = 50 * time.Millisecond
	r.retryDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()

	// Let the mount start, then leave it running while the control server stays
	// silent.
	select {
	case <-engine.started:
	case <-time.After(testTimeout):
		t.Fatal("the runner never started a mount")
	}

	st := waitForState(t, m, StateError)
	if st.Message == "" {
		t.Error("the error must name what went wrong, or the user has nothing to act on")
	}

	cancel()
	// Release the run so its goroutine finishes.
	select {
	case engine.results <- nil:
	case <-time.After(testTimeout):
	}
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("the runner did not stop when its context was cancelled")
	}
}

// TestStreamGoesIdleWhenReady is the good path: once the control server answers
// the Drive counts as up to date.
func TestStreamGoesIdleWhenReady(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	m.ctl = &fakeControl{reachable: true}

	r := newStreamRunner(m)
	r.readyTimeout = time.Second
	r.retryDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()

	select {
	case <-engine.started:
	case <-time.After(testTimeout):
		t.Fatal("the runner never started a mount")
	}
	waitForState(t, m, StateIdle)

	cancel()
	select {
	case engine.results <- nil:
	case <-time.After(testTimeout):
	}
	<-done
}

// TestStreamCreatesMountPoint checks the runner prepares the sync folder rather
// than failing on a fresh install where it does not exist yet.
func TestStreamCreatesMountPoint(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	m.ctl = &fakeControl{reachable: true}

	if _, err := os.Stat(m.cfg.LocalDir()); !os.IsNotExist(err) {
		t.Fatalf("the sync folder should not exist yet: %v", err)
	}

	r := newStreamRunner(m)
	r.readyTimeout = time.Second
	r.retryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()

	select {
	case <-engine.started:
	case <-time.After(testTimeout):
		t.Fatal("the runner never started a mount")
	}
	if fi, err := os.Stat(m.cfg.LocalDir()); err != nil || !fi.IsDir() {
		t.Errorf("the sync folder was not created: %v", err)
	}

	cancel()
	select {
	case engine.results <- nil:
	case <-time.After(testTimeout):
	}
	<-done
}

// TestApplyTransferState pins down which states follow the mount's transfer
// activity. Paused and error must survive a stats tick, or pausing would undo
// itself the moment the next poll came in.
func TestApplyTransferState(t *testing.T) {
	cases := []struct {
		name         string
		start        State
		transferring bool
		want         State
	}{
		{"idle stays idle", StateIdle, false, StateIdle},
		{"idle starts syncing", StateIdle, true, StateSyncing},
		{"syncing settles", StateSyncing, false, StateIdle},
		{"syncing keeps syncing", StateSyncing, true, StateSyncing},
		{"paused is untouched", StatePaused, true, StatePaused},
		{"error is untouched", StateError, false, StateError},
		{"starting is untouched", StateStarting, true, StateStarting},
		{"disconnected is untouched", StateDisconnected, true, StateDisconnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := Runtime{State: tc.start}
			applyTransferState(&rt, tc.transferring)
			if rt.State != tc.want {
				t.Errorf("state %q with transferring=%t became %q, want %q",
					tc.start, tc.transferring, rt.State, tc.want)
			}
		})
	}
}

// TestSyncingToIdleStampsLastSync covers the timestamp the UI shows as "last
// synchronised".
func TestSyncingToIdleStampsLastSync(t *testing.T) {
	rt := Runtime{State: StateSyncing}
	applyTransferState(&rt, false)
	if rt.LastSync.IsZero() {
		t.Error("finishing a transfer must record when it happened")
	}
}
