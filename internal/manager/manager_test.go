// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/fmstate"
	"tdrive-sync/internal/notify"
	"tdrive-sync/internal/rclone"
)

// newTestManager builds a Manager wired to fakes, with every XDG directory
// pointed at a throwaway location so a test can never touch the real
// configuration, cache or published state.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))

	cfg := config.Default()
	if err := cfg.SetLocalDir(filepath.Join(dir, "drive")); err != nil {
		t.Fatal(err)
	}
	cacheDir, err := config.RcloneCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := fmstate.NewPublisher(filepath.Join(dir, "file-manager.json"))
	if err != nil {
		t.Fatal(err)
	}

	m := &Manager{
		cfg:      cfg,
		cacheDir: cacheDir,
		cache:    fmstate.Cache{Dir: cacheDir, Remote: cfg.RemoteName()},
		account:  &fakeAccount{remote: true},
		notifier: notify.Noop{},
		log:      nopLogger{},
		fmPub:    pub,
		rcAddr:   "127.0.0.1:45678",
	}
	m.status = newStatusStore(m.settings)
	return m
}

// fakeEngine stands in for the rclone binary. Every run blocks until the test
// says how it ended, so a runner's loop can be driven one iteration at a time.
type fakeEngine struct {
	// started is signalled (and blocks) on every launch, giving the test the
	// lockstep it needs.
	started chan struct{}
	// results carries the exit status of each run, in order.
	results chan error

	mu     sync.Mutex
	resync []string // the resyncMode recorded per bisync call ("" = no resync)
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{started: make(chan struct{}), results: make(chan error)}
}

func (f *fakeEngine) MountArgs(string, string, string) []string { return []string{"mount"} }

func (f *fakeEngine) BisyncArgs(_, _ string, _ bool, resyncMode string) []string {
	f.mu.Lock()
	f.resync = append(f.resync, resyncMode)
	f.mu.Unlock()
	return []string{"bisync"}
}

func (f *fakeEngine) Start([]string, func(string)) (*exec.Cmd, chan error, error) {
	done := make(chan error, 1)
	go func() { done <- <-f.results }()
	f.started <- struct{}{}
	// A command without a Process: terminate() leaves it alone, which is what a
	// test wants.
	return &exec.Cmd{}, done, nil
}

// resyncModes returns the recorded bisync resyncMode arguments.
func (f *fakeEngine) resyncModes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.resync...)
}

// runOnce waits for the next launch and ends it with err.
func (f *fakeEngine) runOnce(t *testing.T, err error) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(testTimeout):
		t.Fatal("the runner did not start a sync process")
	}
	select {
	case f.results <- err:
	case <-time.After(testTimeout):
		t.Fatal("the runner did not collect the exit status")
	}
}

// fakeControl stands in for the mount's control server.
type fakeControl struct {
	mu        sync.Mutex
	reachable bool
	stats     rclone.Stats
}

func (c *fakeControl) Ping(context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reachable
}
func (c *fakeControl) Refresh(context.Context, string) error    { return nil }
func (c *fakeControl) Forget(context.Context, string) error     { return nil }
func (c *fakeControl) ForgetFile(context.Context, string) error { return nil }
func (c *fakeControl) ResetStats(context.Context) error         { return nil }
func (c *fakeControl) CoreStats(context.Context) (rclone.Stats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats, nil
}

// fakeAccount stands in for the stored rclone remote.
type fakeAccount struct {
	mu        sync.Mutex
	remote    bool  // a remote is stored
	loginErr  error // what Login returns
	logoutErr error // what Logout returns
	email     string
	logins    int
	logouts   int
}

func (a *fakeAccount) Login(context.Context, rclone.LoginOptions, func(string)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logins++
	if a.loginErr != nil {
		return a.loginErr
	}
	a.remote = true
	return nil
}

func (a *fakeAccount) Logout(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logouts++
	a.remote = false
	return a.logoutErr
}

func (a *fakeAccount) RemoteExists() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.remote
}

func (a *fakeAccount) UserEmail(context.Context) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.email
}

func (a *fakeAccount) List(context.Context, string) ([]rclone.Entry, error) { return nil, nil }
func (a *fakeAccount) ListDirs(context.Context, string) ([]string, error)   { return nil, nil }

// testTimeout bounds every wait in these tests, so a broken runner fails the
// test instead of hanging the suite.
const testTimeout = 5 * time.Second

// waitForState blocks until the status reaches want, or fails the test.
func waitForState(t *testing.T, m *Manager, want State) Status {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if st := m.Status(); st.State == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status never reached %q (stuck at %q: %s)", want, m.Status().State, m.Status().Message)
	return Status{}
}

// TestFileManagerModeMatchesConfig: fmstate spells the mirror mode out instead
// of importing config; the two must stay the same string.
func TestFileManagerModeMatchesConfig(t *testing.T) {
	if string(config.ModeMirror) != fmstate.ModeMirror {
		t.Fatalf("config.ModeMirror = %q, fmstate.ModeMirror = %q", config.ModeMirror, fmstate.ModeMirror)
	}
}
