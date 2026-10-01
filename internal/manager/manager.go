// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package manager orchestrates the sync backend: it owns the current status,
// starts/stops the active sync mode (stream mount or mirror bisync), handles
// login/logout, and manages offline-pinned paths.
//
// The modes themselves live behind the syncMode interface (stream.go,
// mirror.go, offline.go, conflicts.go), so the manager decides which one is
// active, and what it can do, without knowing how either works.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"tdrive-sync/internal/app"
	"tdrive-sync/internal/config"
	"tdrive-sync/internal/fmstate"
	"tdrive-sync/internal/i18n"
	"tdrive-sync/internal/notify"
	"tdrive-sync/internal/rclone"
)

// Logger receives the daemon's diagnostic output. *logbuf.Buffer implements it;
// the interface keeps this package independent of where the lines end up.
type Logger interface {
	// Logf records a line whose severity has to be guessed from its text.
	Logf(format string, args ...any)
	// Errorf records a line the caller knows to be an error.
	Errorf(format string, args ...any)
}

// nopLogger discards everything, for callers that do not want a log.
type nopLogger struct{}

func (nopLogger) Logf(string, ...any)   {}
func (nopLogger) Errorf(string, ...any) {}

// Engine starts the sync processes. The real one is *rclone.Client; naming it
// as an interface is what lets the mode runners - the auto-recovery path above
// all - be tested without an rclone binary or a FUSE mount.
type Engine interface {
	// MountArgs builds the argument list for a stream-mode mount.
	MountArgs(mountpoint, rcAddr, cacheDir string) []string
	// BisyncArgs builds the argument list for a mirror-mode reconcile.
	// resyncMode "" runs a normal bisync; any other value forces a full
	// `--resync --resync-mode <resyncMode>` reconciliation.
	BisyncArgs(localDir, workdir string, manualConflicts bool, resyncMode string) []string
	// Start launches a run, forwarding its output lines to onLine, and returns
	// the command plus a channel carrying its exit error.
	Start(args []string, onLine func(string)) (*exec.Cmd, chan error, error)
}

// Control is the mount's remote-control API, as the runners use it.
type Control interface {
	Ping(ctx context.Context) bool
	Refresh(ctx context.Context, dir string) error
	Forget(ctx context.Context, dir string) error
	ForgetFile(ctx context.Context, file string) error
	CoreStats(ctx context.Context) (rclone.Stats, error)
	ResetStats(ctx context.Context) error
}

// Account is the signed-in Google account and the Drive listing behind it. The
// real one is *rclone.Client; like Engine and Control it is an interface so
// sign-in, sign-out and start-up can be tested without rclone.
type Account interface {
	// Login runs the interactive OAuth flow, streaming its output to onLine.
	Login(ctx context.Context, opts rclone.LoginOptions, onLine func(string)) error
	// Logout deletes the stored remote.
	Logout(ctx context.Context) error
	// RemoteExists reports whether a remote is stored.
	RemoteExists() bool
	// UserEmail returns the account's address, "" when it cannot be read.
	UserEmail(ctx context.Context) string
	// List returns the immediate children of a Drive-relative directory.
	List(ctx context.Context, rel string) ([]rclone.Entry, error)
	// ListDirs returns every folder below a Drive-relative path.
	ListDirs(ctx context.Context, rel string) ([]string, error)
}

// Runner drives one sync mode.
type Runner interface {
	// Run blocks until ctx is cancelled, keeping the mode alive in between.
	Run(ctx context.Context)
	// SyncNow asks for an immediate reconciliation. It is called from UI
	// handlers and must not block.
	SyncNow()
}

// syncMode is one way of making the Drive available locally. It lives as long
// as the configuration selects it, not just while a runner runs: pinning a file
// or resolving a conflict has to work while syncing is paused, too.
//
// What a mode can do beyond running is expressed by the optional interfaces
// below; the manager asks for them instead of switching on the mode's name.
type syncMode interface {
	// newRunner builds the runner that keeps this mode alive.
	newRunner() Runner
}

// pinner is a mode in which "keep offline" means something: it keeps a cache
// that pinned paths are read into and released paths are freed from.
type pinner interface {
	// applyPin reads rel into the cache (on) or frees it again (off).
	applyPin(ctx context.Context, rel string, on bool)
}

// conflictKeeper is a mode that leaves conflicting copies for the user to
// resolve, and whose runner has to restart when the conflict mode changes.
type conflictKeeper interface {
	conflicts() []Conflict
	resolveConflict(rel, action string) error
}

// modes maps each sync mode to its implementation. Adding a mode means adding a
// syncMode and one line here; nothing else in the package switches on the mode.
var modes = map[config.SyncMode]func(*Manager) syncMode{
	config.ModeStream: func(m *Manager) syncMode { return streamMode{m} },
	config.ModeMirror: func(m *Manager) syncMode { return mirrorMode{m} },
}

// currentMode returns the configured mode, nil if the configuration names one
// this build does not know.
func (m *Manager) currentMode() syncMode {
	build, ok := modes[m.cfg.Mode()]
	if !ok {
		return nil
	}
	return build(m)
}

// Manager is the central controller. All exported methods are safe for
// concurrent use.
type Manager struct {
	cfg      *config.Config
	account  Account
	engine   Engine
	ctl      Control
	rcAddr   string
	cacheDir string
	cache    fmstate.Cache
	notifier notify.Notifier
	log      Logger
	exe      string

	// status owns the runtime status and its observers.
	status *statusStore

	// fmPub publishes the per-file state the file-manager integration renders.
	fmPub *fmstate.Publisher

	// lifecycle serialises stop/start so two callers cannot each leave a runner
	// running; never take it from a runner goroutine (stopLocked waits for it).
	lifecycle sync.Mutex

	mu        sync.Mutex
	parent    context.Context
	runCancel context.CancelFunc
	runWG     sync.WaitGroup
	active    Runner
	// paused is true between Pause and Resume. startLocked is a no-op while it
	// holds, so configuration changes made during a pause (mode, local dir,
	// conflict mode) only take effect once Resume starts a runner again.
	paused bool
}

// New builds a Manager for the given config. log may be nil.
func New(cfg *config.Config, notifier notify.Notifier, log Logger) (*Manager, error) {
	conf, err := config.RcloneConfPath()
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = nopLogger{}
	}
	cacheDir, err := config.RcloneCacheDir()
	if err != nil {
		return nil, fmt.Errorf("could not prepare the cache directory: %w", err)
	}
	pub, err := fmstate.NewPublisher("")
	if err != nil {
		return nil, err
	}

	// Per-run random credentials for the mount's RC API: without them any local
	// process (or a web page via CSRF) could drive the rclone control server.
	rcPass, err := randomSecret()
	if err != nil {
		return nil, fmt.Errorf("could not generate RC credentials: %w", err)
	}
	const rcUser = "tdrive-sync"
	rcAddr := fmt.Sprintf("127.0.0.1:%d", cfg.WebPort()+1)
	rc, err := rclone.New(cfg.RemoteName(), conf, rcUser, rcPass)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		cfg:      cfg,
		account:  rc,
		engine:   rc,
		ctl:      rclone.NewRC(rcAddr, rcUser, rcPass),
		rcAddr:   rcAddr,
		cacheDir: cacheDir,
		cache:    fmstate.Cache{Dir: cacheDir, Remote: cfg.RemoteName()},
		notifier: notifier,
		log:      log,
		exe:      app.Exec(),
		fmPub:    pub,
	}
	m.status = newStatusStore(m.settings)
	m.status.SetState(StateDisconnected, i18n.T("status.signin_required"))
	return m, nil
}

// settings returns the configuration half of a status snapshot.
func (m *Manager) settings() Status {
	return Status{
		Mode:         m.cfg.Mode(),
		ConflictMode: m.cfg.ConflictMode(),
		Account:      m.cfg.AccountEmail(),
		LocalDir:     m.cfg.LocalDir(),
		Offline:      m.cfg.OfflinePaths(),
	}
}

// randomSecret returns 32 hex characters from a cryptographic source.
func randomSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Browse lists the immediate children of a Drive-relative directory.
func (m *Manager) Browse(ctx context.Context, rel string) ([]rclone.Entry, error) {
	return m.account.List(ctx, rel)
}

// LocalDir is the configured sync folder.
func (m *Manager) LocalDir() string { return m.cfg.LocalDir() }

// SyncFolder reports the sync folder while a stream mount is serving it, and ""
// otherwise. The Dolphin preview keeper uses it to decide whether there is
// anything to write markers for; see internal/dolphin.
func (m *Manager) SyncFolder() string {
	st := m.status.Get()
	if st.Mode != config.ModeStream || !st.Active() {
		return ""
	}
	return st.LocalDir
}

// ListDirs returns every folder below a Drive-relative path.
func (m *Manager) ListDirs(ctx context.Context, rel string) ([]string, error) {
	return m.account.ListDirs(ctx, rel)
}

// Start begins syncing according to the current config. It returns immediately;
// work happens in background goroutines until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.parent = ctx
	m.mu.Unlock()
	// Subscribed here rather than in New: the CLI's one-shot `login` command
	// builds a Manager that never starts, and must not overwrite the running
	// daemon's file-manager.json with an inactive snapshot.
	m.status.Subscribe(m.publishFM)
	if !m.cfg.Configured() || !m.account.RemoteExists() {
		m.status.SetState(StateDisconnected, i18n.T("status.signin_required"))
		return
	}
	m.restart()
}

// restart stops and restarts the active runner so a configuration change
// takes effect immediately.
func (m *Manager) restart() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.stopLocked()
	m.startLocked()
}

// stopLocked cancels and waits for the active runner. Caller must hold
// m.lifecycle. Never call it from within a runner goroutine: it waits for that
// goroutine to finish.
func (m *Manager) stopLocked() {
	m.mu.Lock()
	cancel := m.runCancel
	m.runCancel = nil
	m.active = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.runWG.Wait()
}

// startLocked (re)launches the runner for the currently configured mode.
// Caller must hold m.lifecycle.
func (m *Manager) startLocked() {
	m.mu.Lock()
	if m.paused {
		m.mu.Unlock()
		return
	}
	if m.active != nil {
		// guard against a second runner
		m.mu.Unlock()
		return
	}
	parent := m.parent
	if parent == nil {
		m.mu.Unlock()
		return
	}
	mode := m.currentMode()
	if mode == nil {
		m.mu.Unlock()
		m.status.SetState(StateError, i18n.T("status.unknown_mode", m.cfg.Mode()))
		return
	}
	runner := mode.newRunner()
	ctx, cancel := context.WithCancel(parent)
	m.runCancel = cancel
	m.active = runner
	m.mu.Unlock()

	m.runWG.Add(1)
	go func() {
		defer m.runWG.Done()
		runner.Run(ctx)
	}()
}

// Shutdown stops all activity and cleans up the mount.
func (m *Manager) Shutdown() {
	m.lifecycle.Lock()
	m.stopLocked()
	m.lifecycle.Unlock()
	// Closed first, so no delivery still in flight can overwrite what follows.
	m.status.Close()
	// Tell the file-manager integration to stop showing indicators: without a
	// mount there is nothing left to indicate.
	m.publishFM(Status{})
}

// -------- user actions --------

// SetMode switches the sync mode and restarts syncing.
func (m *Manager) SetMode(mode config.SyncMode) error {
	if err := m.cfg.SetMode(mode); err != nil {
		return err
	}
	m.status.Notify()
	if m.cfg.Configured() {
		m.restart()
	}
	return nil
}

// SetLocalDir moves the sync folder and restarts syncing so the new location
// takes effect. The old mount point is released by the runner being stopped.
func (m *Manager) SetLocalDir(path string) error {
	if err := m.cfg.SetLocalDir(path); err != nil {
		return err
	}
	m.status.Notify()
	if m.cfg.Configured() {
		m.restart()
	}
	return nil
}

// SetConflictMode switches how mirror-mode conflicts are resolved and restarts
// mirror syncing so the new bisync flags take effect.
func (m *Manager) SetConflictMode(mode config.ConflictMode) error {
	if err := m.cfg.SetConflictMode(mode); err != nil {
		return err
	}
	m.status.Notify()
	if _, ok := m.currentMode().(conflictKeeper); ok && m.cfg.Configured() {
		m.restart()
	}
	return nil
}

// Pause stops syncing until Resume is called.
func (m *Manager) Pause() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	m.paused = true
	m.mu.Unlock()
	m.stopLocked()
	m.status.SetState(StatePaused, i18n.T("status.paused"))
}

// Resume restarts syncing after a pause. A no-op when not currently paused,
// so a double call (a double click, a tray race, an empty POST body) cannot
// start a second runner alongside the one already running.
func (m *Manager) Resume() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	if !m.paused {
		m.mu.Unlock()
		return
	}
	m.paused = false
	m.mu.Unlock()
	if !m.cfg.Configured() {
		return
	}
	m.stopLocked()
	m.startLocked()
}

// Paused reports whether syncing is currently paused.
func (m *Manager) Paused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

// SyncNow triggers an immediate reconciliation in the active mode.
func (m *Manager) SyncNow() {
	m.mu.Lock()
	runner := m.active
	m.mu.Unlock()
	if runner != nil {
		runner.SyncNow()
	}
}

// Login runs the OAuth flow, stores the account and starts syncing on success.
// openerDir routes rclone's sign-in link through our own opener shim (see
// window.InstallOpenShim, installed by the caller); "" lets rclone open the
// link itself.
func (m *Manager) Login(ctx context.Context, openerDir string, onLine func(string)) error {
	creds := m.cfg.Google()
	opts := rclone.LoginOptions{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		OpenerDir:    openerDir,
	}
	if err := m.account.Login(ctx, opts, onLine); err != nil {
		return err
	}
	if !m.account.RemoteExists() {
		return errors.New(i18n.T("err.login_incomplete"))
	}
	email := m.account.UserEmail(ctx)
	if email == "" {
		email = "Google Drive"
	}
	if err := m.cfg.SetAccountEmail(email); err != nil {
		return err
	}
	m.status.Notify()
	m.notifier.Notify(i18n.T("notify.signed_in_as", email))
	// Only the final start is serialised, not the OAuth flow above: that can
	// run for minutes and must not hold up an unrelated SetLocalDir/SetMode
	// call from the settings UI.
	m.restart()
	return nil
}

// Logout signs out and removes the remote. Held under one lifecycle critical
// section so a concurrent SetMode cannot restart against a remote this just
// deleted.
func (m *Manager) Logout(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	m.paused = false
	m.mu.Unlock()
	m.stopLocked()
	if err := m.account.Logout(ctx); err != nil {
		// Signed out here regardless: the account is unusable to us either way,
		// but the stale token left in rclone.conf is worth knowing about.
		m.log.Errorf("could not remove the stored account: %v", err)
	}
	if err := m.cfg.SetAccountEmail(""); err != nil {
		return err
	}
	m.status.SetState(StateDisconnected, i18n.T("status.signed_out"))
	return nil
}

// GoogleCreds returns the currently configured custom OAuth client credentials.
func (m *Manager) GoogleCreds() config.GoogleCreds { return m.cfg.Google() }

// SetGoogleCreds stores custom OAuth client credentials for login. Passing an
// empty GoogleCreds reverts to rclone's built-in credentials. The change only
// affects the next login, so it must be done while signed out.
func (m *Manager) SetGoogleCreds(creds config.GoogleCreds) error {
	if m.cfg.Configured() {
		return errors.New(i18n.T("err.sign_out_first"))
	}
	return m.cfg.SetGoogle(creds)
}

// -------- status plumbing --------

// Status returns the current snapshot.
func (m *Manager) Status() Status { return m.status.Get() }

// Subscribe registers fn to receive the current status and every later change,
// on a goroutine of its own; see statusStore.Subscribe. The returned function
// unregisters it.
func (m *Manager) Subscribe(fn func(Status)) (unsubscribe func()) { return m.status.Subscribe(fn) }

// setState is the runners' shortcut into the status store; an error state is
// also logged, since that is the one the user will come asking about.
func (m *Manager) setState(s State, msg string) {
	if s == StateError {
		m.log.Errorf("%s", msg)
	}
	m.status.SetState(s, msg)
}

// ResetErrors clears rclone's error counter and the current error state.
func (m *Manager) ResetErrors() {
	ctx, cancel := context.WithTimeout(context.Background(), rcCallTimeout)
	defer cancel()
	_ = m.ctl.ResetStats(ctx)
	m.status.Update(func(rt *Runtime) { rt.Errors = 0 })
}

// fmInfo builds the snapshot the file-manager integration works from.
func (m *Manager) fmInfo(s Status) fmstate.Info {
	return fmstate.Info{
		Active:   s.Active(),
		Mode:     string(s.Mode),
		State:    string(s.State),
		Root:     s.LocalDir,
		CacheDir: m.cacheDir,
		Remote:   m.cfg.RemoteName(),
		Exec:     m.exe,
		Pinned:   s.Offline,
	}
}

// publishFM hands the current picture to the file-manager integration.
func (m *Manager) publishFM(s Status) {
	if err := m.fmPub.Publish(m.fmInfo(s)); err != nil {
		m.log.Errorf("could not publish the file-manager state: %v", err)
	}
}

// humanBytes formats a byte count the way a file manager would.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	var suffix string
	for _, u := range units {
		value /= unit
		suffix = u
		if value < unit {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

// sleepCtx sleeps for d, returning true if ctx was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}
