// Package manager orchestrates the sync backend: it owns the current status,
// starts/stops the active sync mode (stream mount or mirror bisync), handles
// login/logout, and manages offline-pinned paths.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/i18n"
	"tdrive-sync/internal/notify"
	"tdrive-sync/internal/rclone"
	"tdrive-sync/internal/window"
)

// appName is the title shown on desktop notifications.
const appName = "TDrive Sync"

// State is a coarse sync state used for the tray icon and UI.
type State string

const (
	StateDisconnected State = "disconnected" // not signed in
	StateStarting     State = "starting"     // mount/bisync coming up
	StateSyncing      State = "syncing"      // transfers in progress
	StateIdle         State = "idle"         // up to date
	StatePaused       State = "paused"       // user paused
	StateError        State = "error"        // needs attention
)

// Status is an immutable snapshot handed to observers.
type Status struct {
	State        State           `json:"state"`
	Mode         config.SyncMode `json:"mode"`
	ConflictMode string          `json:"conflict_mode"`
	Message      string          `json:"message"`
	Account      string          `json:"account"`
	LocalDir     string          `json:"local_dir"`
	Bytes        int64           `json:"bytes"`
	Speed        float64         `json:"speed"`
	Errors       int64           `json:"errors"`
	LastSync     time.Time       `json:"last_sync"`
	Offline      []string        `json:"offline"`
}

// Manager is the central controller. All exported methods are safe for
// concurrent use.
type Manager struct {
	cfg      *config.Config
	rc       *rclone.Client
	rcAddr   string
	rcUser   string
	rcPass   string
	ctl      *rclone.RC
	cacheDir string
	notifier notify.Notifier
	logf     func(string, ...any)

	mu        sync.Mutex
	status    Status
	listeners []func(Status)

	runCancel   context.CancelFunc
	runWG       sync.WaitGroup
	parent      context.Context
	syncTrigger chan struct{}
}

// New builds a Manager for the given config.
func New(cfg *config.Config, notifier notify.Notifier, logf func(string, ...any)) (*Manager, error) {
	rc, err := rclone.New(cfg.RemoteName, cfg.Google)
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	rcAddr := fmt.Sprintf("127.0.0.1:%d", cfg.WebPort+1)
	// Per-run random credentials for the mount's RC API: without them any local
	// process (or a web page via CSRF) could drive the rclone control server.
	rcPass, err := randomSecret()
	if err != nil {
		return nil, fmt.Errorf("could not generate RC credentials: %w", err)
	}
	rcUser := "tdrive-sync"
	m := &Manager{
		cfg:         cfg,
		rc:          rc,
		rcAddr:      rcAddr,
		rcUser:      rcUser,
		rcPass:      rcPass,
		ctl:         rclone.NewRC(rcAddr, rcUser, rcPass),
		cacheDir:    cacheDir(),
		notifier:    notifier,
		logf:        logf,
		syncTrigger: make(chan struct{}, 1),
		status: Status{
			State:        StateDisconnected,
			Mode:         cfg.Mode,
			ConflictMode: cfg.ConflictMode,
			Account:      cfg.AccountEmail,
			LocalDir:     cfg.LocalDir,
			Offline:      append([]string{}, cfg.OfflinePaths...),
		},
	}
	return m, nil
}

// randomSecret returns 32 hex characters from a cryptographic source.
func randomSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func cacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "tdrive-sync", "vfs")
	_ = os.MkdirAll(d, 0o700)
	return d
}

// Rclone exposes the underlying rclone client (used by CLI login).
func (m *Manager) Rclone() *rclone.Client { return m.rc }

// Start begins syncing according to the current config. It returns immediately;
// work happens in background goroutines until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	m.parent = ctx
	if !m.cfg.Configured() || !m.rc.RemoteExists() {
		m.setState(StateDisconnected, i18n.T("status.signin_required"))
		return
	}
	m.startMode()
}

// startMode (re)launches the goroutine for the currently configured mode.
func (m *Manager) startMode() {
	m.stopMode()
	if m.parent == nil {
		return
	}
	ctx, cancel := context.WithCancel(m.parent)
	m.mu.Lock()
	m.runCancel = cancel
	mode := m.cfg.Mode
	m.mu.Unlock()

	m.runWG.Add(1)
	go func() {
		defer m.runWG.Done()
		switch mode {
		case config.ModeMirror:
			m.runMirror(ctx)
		default:
			m.runStream(ctx)
		}
	}()
}

// stopMode cancels and waits for the active mode goroutine. Never call from
// within a runner goroutine.
func (m *Manager) stopMode() {
	m.mu.Lock()
	c := m.runCancel
	m.runCancel = nil
	m.mu.Unlock()
	if c != nil {
		c()
	}
	m.runWG.Wait()
}

// Shutdown stops all activity and cleans up the mount.
func (m *Manager) Shutdown() {
	m.stopMode()
}

// -------- user actions --------

// SetMode switches the sync mode and restarts syncing.
func (m *Manager) SetMode(mode config.SyncMode) error {
	m.cfg.Mode = mode
	if err := m.cfg.Save(); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Mode = mode
	m.mu.Unlock()
	m.broadcast()
	if m.cfg.Configured() {
		m.startMode()
	}
	return nil
}

// SetConflictMode switches how mirror-mode conflicts are resolved and restarts
// mirror syncing so the new bisync flags take effect.
func (m *Manager) SetConflictMode(mode string) error {
	if mode != config.ConflictManual {
		mode = config.ConflictAuto
	}
	m.cfg.ConflictMode = mode
	if err := m.cfg.Save(); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.ConflictMode = mode
	m.mu.Unlock()
	m.broadcast()
	if m.cfg.Configured() && m.cfg.Mode == config.ModeMirror {
		m.startMode()
	}
	return nil
}

// Pause stops syncing until Resume is called.
func (m *Manager) Pause() {
	m.stopMode()
	m.setState(StatePaused, i18n.T("status.paused"))
}

// Resume restarts syncing after a pause.
func (m *Manager) Resume() {
	if !m.cfg.Configured() {
		return
	}
	m.startMode()
}

// SyncNow triggers an immediate reconciliation.
func (m *Manager) SyncNow() {
	switch m.cfg.Mode {
	case config.ModeMirror:
		select {
		case m.syncTrigger <- struct{}{}:
		default:
		}
	default:
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_ = m.ctl.Refresh(ctx, "")
			m.warmOffline(ctx)
		}()
	}
}

// Login runs the OAuth flow, stores the account and starts syncing on success.
func (m *Manager) Login(ctx context.Context, onLine func(string)) error {
	// rclone opens the sign-in link via xdg-open; route that through our own
	// opener so the link reliably reaches the user's browser. Best-effort: if the
	// shim cannot be written, rclone opens the link itself as before.
	if dir, err := window.InstallOpenShim(); err == nil {
		m.rc.SetURLOpener(dir)
	} else {
		m.logf("browser shim unavailable, letting rclone open the sign-in link: %v", err)
	}
	if err := m.rc.Login(ctx, onLine); err != nil {
		return err
	}
	if !m.rc.RemoteExists() {
		return errors.New(i18n.T("err.login_incomplete"))
	}
	email := m.rc.UserEmail(ctx)
	if email == "" {
		email = "Google Drive"
	}
	m.cfg.AccountEmail = email
	if err := m.cfg.Save(); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Account = email
	m.mu.Unlock()
	m.notifier.Notify(appName, i18n.T("notify.signed_in_as", email))
	m.startMode()
	return nil
}

// Logout signs out and removes the remote.
func (m *Manager) Logout(ctx context.Context) error {
	m.stopMode()
	_ = m.rc.Logout(ctx)
	m.cfg.AccountEmail = ""
	if err := m.cfg.Save(); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Account = ""
	m.mu.Unlock()
	m.setState(StateDisconnected, i18n.T("status.signed_out"))
	return nil
}

// GoogleCreds returns the currently configured custom OAuth client credentials.
func (m *Manager) GoogleCreds() config.GoogleCreds { return m.cfg.Google }

// SetGoogleCreds stores custom OAuth client credentials for login. Passing an
// empty GoogleCreds reverts to rclone's built-in credentials. The change only
// affects the next login, so it must be done while signed out.
func (m *Manager) SetGoogleCreds(creds config.GoogleCreds) error {
	if m.cfg.Configured() {
		return errors.New(i18n.T("err.sign_out_first"))
	}
	m.cfg.Google = creds
	if err := m.cfg.Save(); err != nil {
		return err
	}
	m.rc.SetCreds(creds)
	return nil
}

// SetOffline pins or unpins a Drive-relative path for offline availability
// (stream mode only).
func (m *Manager) SetOffline(path string, on bool) error {
	if on {
		m.cfg.AddOffline(path)
	} else {
		m.cfg.RemoveOffline(path)
	}
	if err := m.cfg.Save(); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Offline = append([]string{}, m.cfg.OfflinePaths...)
	m.mu.Unlock()
	m.broadcast()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if on {
			m.warmPath(ctx, path)
		} else {
			m.forgetPath(ctx, path)
		}
	}()
	return nil
}

// -------- status plumbing --------

// Status returns the current snapshot.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Subscribe registers fn to receive every subsequent status change.
func (m *Manager) Subscribe(fn func(Status)) {
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	cur := m.status
	m.mu.Unlock()
	fn(cur)
}

func (m *Manager) setState(s State, msg string) {
	if s == StateError {
		m.logf("ERROR: %s", msg)
	}
	m.mu.Lock()
	m.status.State = s
	m.status.Message = msg
	m.mu.Unlock()
	m.broadcast()
}

// ResetErrors clears rclone's error counter and the current error state.
func (m *Manager) ResetErrors() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.ctl.ResetStats(ctx)
	m.mu.Lock()
	m.status.Errors = 0
	m.mu.Unlock()
	m.broadcast()
}

func (m *Manager) broadcast() {
	m.mu.Lock()
	cur := m.status
	ls := append([]func(Status){}, m.listeners...)
	m.mu.Unlock()
	for _, fn := range ls {
		fn(cur)
	}
}
