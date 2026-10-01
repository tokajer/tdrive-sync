// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// startMirror creates a mirror runner for m, starts it in the background and
// returns it together with a cancel func and a channel closed once Run
// returns. Callers that need to seed the local dir before the first run must
// do so before calling this.
func startMirror(t *testing.T, m *Manager) (r *mirrorRunner, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	r = newMirrorRunner(m)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	go func() { defer close(ch); r.Run(ctx) }()
	return r, cancel, ch
}

// markInitialized writes the bisync init marker for m's local dir, so Run
// treats the folder as an already-established mirror rather than a first sync.
func markInitialized(t *testing.T, m *Manager) {
	t.Helper()
	if err := os.MkdirAll(m.cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(m.cacheDir, "bisync-init-"+sanitize(m.cfg.LocalDir()))
	if err := os.WriteFile(marker, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMirrorAutoRecovery asserts that after maxMirrorFailures failed runs, the
// next run forces a full resync.
func TestMirrorAutoRecovery(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	if err := os.MkdirAll(m.cfg.LocalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	markInitialized(t, m)

	r, cancel, done := startMirror(t, m)
	defer cancel()

	// Three failures in a row, each followed by an explicit trigger so the test
	// does not have to wait out the retry interval.
	boom := errors.New("bisync failed")
	for i := 0; i < maxMirrorFailures; i++ {
		engine.runOnce(t, boom)
		r.SyncNow()
	}
	// The fourth run is the recovery.
	engine.runOnce(t, nil)

	cancel()
	<-done

	got := engine.resyncModes()
	if len(got) < maxMirrorFailures+1 {
		t.Fatalf("expected at least %d runs, got %d: %v", maxMirrorFailures+1, len(got), got)
	}
	for i := 0; i < maxMirrorFailures; i++ {
		if got[i] != "" {
			t.Errorf("run %d asked for a resync before the failure threshold: %v", i+1, got)
		}
	}
	if got[maxMirrorFailures] != recoveryResyncMode {
		t.Errorf("run %d resync mode = %q, want the auto-recovery mode %q: %v",
			maxMirrorFailures+1, got[maxMirrorFailures], recoveryResyncMode, got)
	}
}

// TestMirrorSuccessResetsFailures checks that a good run clears the counter, so
// three failures spread over a week do not eventually force a resync.
func TestMirrorSuccessResetsFailures(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	if err := os.MkdirAll(m.cfg.LocalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	markInitialized(t, m)

	r, cancel, done := startMirror(t, m)
	defer cancel()

	boom := errors.New("bisync failed")
	// fail, fail, succeed, fail, fail - never three in a row.
	for _, err := range []error{boom, boom, nil, boom, boom} {
		engine.runOnce(t, err)
		r.SyncNow()
	}
	engine.runOnce(t, nil)
	cancel()
	<-done

	for i, resync := range engine.resyncModes() {
		if resync != "" {
			t.Errorf("run %d forced a resync although the failures never ran three deep: %v",
				i+1, engine.resyncModes())
		}
	}
}

// TestMirrorReportsFailure covers what the user sees while a sync keeps failing.
func TestMirrorReportsFailure(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	if err := os.MkdirAll(m.cfg.LocalDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	_, cancel, done := startMirror(t, m)
	defer cancel()

	engine.runOnce(t, errors.New("bisync failed"))
	st := waitForState(t, m, StateError)
	if st.Message == "" {
		t.Error("an error state must carry a message the UI can show")
	}

	cancel()
	<-done
}

// TestMirrorSuccessGoesIdle asserts that a good run leaves the status "up to
// date" with a fresh timestamp, and that the first run over an empty local dir
// (no init marker) resyncs with the cloud (path1) authoritative.
func TestMirrorSuccessGoesIdle(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	if err := os.MkdirAll(m.cfg.LocalDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	_, cancel, done := startMirror(t, m)
	defer cancel()

	engine.runOnce(t, nil)
	st := waitForState(t, m, StateIdle)
	if st.LastSync.IsZero() {
		t.Error("a successful sync must record when it happened")
	}

	cancel()
	<-done

	if got := engine.resyncModes(); len(got) != 1 || got[0] != firstSyncResyncMode {
		t.Errorf("first-run resync modes = %v, want [%q]", got, firstSyncResyncMode)
	}
}

// TestMirrorFirstSyncPopulatedLocalDirPrefersNewer asserts that a first sync
// over a local dir that already holds files (e.g. the init marker was lost
// from a cleared ~/.cache) reconciles as "newer" instead of letting the cloud
// silently overwrite them.
func TestMirrorFirstSyncPopulatedLocalDirPrefersNewer(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	local := m.cfg.LocalDir()
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cancel, done := startMirror(t, m)
	defer cancel()

	engine.runOnce(t, nil)
	cancel()
	<-done

	if got := engine.resyncModes(); len(got) != 1 || got[0] != recoveryResyncMode {
		t.Errorf("first-run resync mode with a populated local dir = %v, want [%q]", got, recoveryResyncMode)
	}
}

// TestMirrorMarkerWriteFailureDoesNotRepeatResync asserts that when the init
// marker cannot be written to disk, the daemon still remembers in memory that
// the folder was initialized, so the run right after stays a normal
// (non-resync) bisync instead of forcing one again.
func TestMirrorMarkerWriteFailureDoesNotRepeatResync(t *testing.T) {
	m := newTestManager(t)
	engine := newFakeEngine()
	m.engine = engine
	local := m.cfg.LocalDir()
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(m.cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory at the marker path makes it both "already initialized" (so
	// fileExists sees it) and unwritable (os.WriteFile cannot replace a
	// directory with a regular file).
	marker := filepath.Join(m.cacheDir, "bisync-init-"+sanitize(local))
	if err := os.MkdirAll(marker, 0o700); err != nil {
		t.Fatal(err)
	}

	r, cancel, done := startMirror(t, m)
	defer cancel()

	boom := errors.New("bisync failed")
	for i := 0; i < maxMirrorFailures; i++ {
		engine.runOnce(t, boom)
		r.SyncNow()
	}
	engine.runOnce(t, nil) // the recovery run: forces a resync, marker write fails
	r.SyncNow()
	engine.runOnce(t, nil) // the run right after must not force one again
	cancel()
	<-done

	got := engine.resyncModes()
	if len(got) < maxMirrorFailures+2 {
		t.Fatalf("expected at least %d runs, got %d: %v", maxMirrorFailures+2, len(got), got)
	}
	if last := got[len(got)-1]; last != "" {
		t.Errorf("run after a failed marker write forced a resync again: resync mode = %q, want \"\"", last)
	}
}
