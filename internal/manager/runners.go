package manager

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// runStream mounts the whole Drive and keeps offline-pinned paths warm. It
// restarts the mount if it dies, until ctx is cancelled.
func (m *Manager) runStream(ctx context.Context) {
	mp := m.cfg.LocalDir
	if err := ensureDir(mp); err != nil {
		m.setState(StateError, "Mount-Ordner nicht nutzbar: "+err.Error())
		return
	}
	go m.pollStats(ctx)

	firstReady := false
	for ctx.Err() == nil {
		m.setState(StateStarting, "Google Drive wird eingebunden…")
		cmd, done := m.startProc(m.rc.MountArgs(mp, m.rcAddr, m.cacheDir), "mount")

		if m.waitMountReady(ctx, 40*time.Second) {
			m.setState(StateIdle, "Auf dem neuesten Stand")
			if !firstReady {
				m.notifier.Notify("TDrive Sync", "Laufwerk bereit unter "+mp)
				firstReady = true
			}
			go m.warmOffline(ctx)
		}

		select {
		case <-ctx.Done():
			m.terminate(cmd)
			m.unmount(mp)
			<-done
			return
		case <-done:
			m.unmount(mp)
			if ctx.Err() != nil {
				return
			}
			m.setState(StateError, "Verbindung unterbrochen – Neustart in 5 s…")
		}
		if sleepCtx(ctx, 5*time.Second) {
			return
		}
	}
}

// maxMirrorFailures is how many consecutive bisync failures trigger an
// automatic full resync (auto-recovery).
const maxMirrorFailures = 3

// runMirror keeps a two-way-synced local copy using rclone bisync. It reconciles
//   - on a timer (periodic remote polling),
//   - on demand (SyncNow), and
//   - immediately after local changes detected via inotify (watchMirror).
//
// After several consecutive failures it forces a full resync to recover, and it
// cleans up stale locks left behind by crashed runs before every attempt.
func (m *Manager) runMirror(ctx context.Context) {
	local := m.cfg.LocalDir
	if err := ensureDir(local); err != nil {
		m.setState(StateError, "Sync-Ordner nicht nutzbar: "+err.Error())
		return
	}
	workdir := filepath.Join(m.cacheDir, "bisync")
	_ = os.MkdirAll(workdir, 0o700)
	marker := filepath.Join(m.cacheDir, "bisync-init-"+sanitize(local))

	// Real-time local watching: nudge the sync trigger on local changes.
	go m.watchMirror(ctx, local)

	failures := 0
	for ctx.Err() == nil {
		// Stale lock detection: drop locks older than the bisync --max-lock
		// window, which can only come from a crashed/killed run.
		m.cleanStaleLocks(workdir, 5*time.Minute)

		recovering := failures >= maxMirrorFailures
		first := !fileExists(marker)
		resync := first || recovering

		switch {
		case recovering:
			m.logf("Auto-Wiederherstellung: vollständiger Neuabgleich nach %d Fehlversuchen", failures)
			m.setState(StateSyncing, "Auto-Wiederherstellung: vollständiger Neuabgleich…")
		case first:
			m.setState(StateSyncing, "Erstabgleich läuft (kann dauern)…")
		default:
			m.setState(StateSyncing, "Synchronisiere…")
		}

		cmd, done := m.startProc(m.rc.BisyncArgs(local, workdir, m.cfg.ConflictMode, resync), "bisync")
		var runErr error
		select {
		case <-ctx.Done():
			m.terminate(cmd)
			<-done
			return
		case runErr = <-done:
		}

		if runErr == nil {
			if resync {
				_ = os.WriteFile(marker, []byte("ok\n"), 0o644)
			}
			failures = 0
			m.mu.Lock()
			m.status.LastSync = time.Now()
			m.mu.Unlock()
			m.setState(StateIdle, "Auf dem neuesten Stand")
		} else {
			failures++
			if failures >= maxMirrorFailures {
				m.setState(StateError, "Wiederholte Fehler – Auto-Wiederherstellung beim nächsten Lauf")
				m.notifier.Notify("TDrive Sync", "Wiederholte Synchronisierungsfehler – automatische Wiederherstellung folgt")
			} else {
				m.setState(StateError, "Synchronisierungsfehler – neuer Versuch folgt")
				m.notifier.Notify("TDrive Sync", "Synchronisierungsfehler – wird erneut versucht")
			}
		}

		// Periodic remote polling. After a failure, retry sooner than the full
		// interval so recovery does not stall.
		wait := time.Duration(m.cfg.MirrorIntervalSec) * time.Second
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		if runErr != nil && wait > 20*time.Second {
			wait = 20 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-m.syncTrigger:
		}
	}
}

// cleanStaleLocks removes bisync lock files in dir older than maxAge. rclone
// renews its lock within --max-lock, so anything older is a leftover from a
// process that crashed or was killed.
func (m *Manager) cleanStaleLocks(dir string, maxAge time.Duration) {
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
			m.logf("Verwaiste Sperrdatei entfernt: %s", e.Name())
		}
	}
}

// -------- offline warming (stream mode) --------

func (m *Manager) warmOffline(ctx context.Context) {
	for _, p := range append([]string{}, m.cfg.OfflinePaths...) {
		if ctx.Err() != nil {
			return
		}
		m.warmPath(ctx, p)
	}
}

// warmPath reads every file under a Drive-relative path through the mount so it
// gets stored in the VFS cache and stays available offline.
func (m *Manager) warmPath(ctx context.Context, rel string) {
	root := filepath.Join(m.cfg.LocalDir, rel)
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

func (m *Manager) forgetPath(ctx context.Context, rel string) {
	_ = m.ctl.Forget(ctx, rel)
}

// -------- helpers --------

func (m *Manager) pollStats(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	var lastReported string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		s, err := m.ctl.CoreStats(cctx)
		cancel()
		if err != nil {
			continue
		}
		if s.LastError != "" && s.LastError != lastReported {
			lastReported = s.LastError
			m.logf("ERROR: %s", s.LastError)
		}
		m.mu.Lock()
		m.status.Bytes = s.Bytes
		m.status.Speed = s.Speed
		m.status.Errors = s.Errors
		if m.status.State == StateIdle || m.status.State == StateSyncing {
			if s.Transferring > 0 {
				m.status.State = StateSyncing
				m.status.Message = "Synchronisiere…"
			} else {
				if m.status.State == StateSyncing {
					m.status.LastSync = time.Now()
				}
				m.status.State = StateIdle
				m.status.Message = "Auf dem neuesten Stand"
			}
		}
		m.mu.Unlock()
		m.broadcast()
	}
}

// waitMountReady polls the mount's control server until it answers or timeout.
func (m *Manager) waitMountReady(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ok := m.ctl.Ping(cctx)
		cancel()
		if ok {
			return true
		}
		if sleepCtx(ctx, time.Second) {
			return false
		}
	}
	return false
}

// startProc launches rclone with args, streaming its output to the log, and
// returns the command plus a channel that receives its exit error.
func (m *Manager) startProc(args []string, tag string) (*exec.Cmd, chan error) {
	cmd := exec.Command(m.rc.Bin(), args...)
	// RC credentials via environment (rclone reads RCLONE_<FLAG> for any flag)
	// instead of argv, so they are not visible in the process list. Harmless for
	// runs without --rc (bisync).
	cmd.Env = append(os.Environ(),
		"RCLONE_RC_USER="+m.rcUser,
		"RCLONE_RC_PASS="+m.rcPass,
	)
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		cmd.Stderr = cmd.Stdout
	}
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		done <- err
		return cmd, done
	}
	if stdout != nil {
		go func() {
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				m.logf("[%s] %s", tag, sc.Text())
			}
		}()
	}
	go func() { done <- cmd.Wait() }()
	return cmd, done
}

func (m *Manager) terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		time.Sleep(8 * time.Second)
		_ = cmd.Process.Kill()
	}()
}

func (m *Manager) unmount(mp string) {
	for _, tool := range []string{"fusermount3", "fusermount"} {
		if _, err := exec.LookPath(tool); err == nil {
			_ = exec.Command(tool, "-uz", mp).Run()
			return
		}
	}
}

func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func sanitize(p string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(strings.TrimPrefix(p, "/"))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}
