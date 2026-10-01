// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command tdrive-sync is a Google Drive sync client with a tray icon and a local
// settings UI, modelled on the Windows Google Drive client. It uses a bundled
// rclone binary as its sync engine.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tdrive-sync/internal/app"
	"tdrive-sync/internal/config"
	"tdrive-sync/internal/dolphin"
	"tdrive-sync/internal/i18n"
	"tdrive-sync/internal/logbuf"
	"tdrive-sync/internal/logfile"
	"tdrive-sync/internal/manager"
	"tdrive-sync/internal/notify"
	"tdrive-sync/internal/tray"
	"tdrive-sync/internal/updater"
	"tdrive-sync/internal/webui"
	"tdrive-sync/internal/window"
)

// version is injected at build time via -ldflags "-X main.version=<tag>".
// Local builds keep the default so they are clearly identifiable.
var version = "local-dev-build"

func main() {
	log.SetFlags(log.Ltime)
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "run", "":
		runDaemon()
	case "login":
		cliLogin()
	case "open", "ui", "window", "settings":
		openWindowCmd()
	case "status":
		cliStatus()
	case "open-url":
		// Used by the xdg-open shim we put on rclone's PATH during login, and
		// usable on its own for diagnosing a browser that will not open.
		cliOpenURL()
	case "offline":
		// Used by the Dolphin context menu, and handy on its own.
		cliOffline()
	case "file-state":
		cliFileState()
	case "dolphin":
		cliDolphin()
	case "restart-wait":
		// Internal: the second half of an update restart, see restartFunc.
		cliRestartWait()
	case "version", "--version", "-v":
		fmt.Println("tdrive-sync", version)
	default:
		usage()
	}
}

func loadOrExit() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("could not load the configuration: %v", err)
	}
	return cfg
}

// runDaemon starts the sync backend, the settings web server and the tray icon.
func runDaemon() {
	cfg := loadOrExit()
	persistLog()

	// Single instance, decided by the settings socket rather than by asking
	// over HTTP first. The probe cannot be trusted on its own: a daemon that is
	// still coming up does not answer yet, and the second launch would then
	// mount over the first one's mount point and unmount it again on its way
	// out. Binding before anything else starts makes that impossible.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.WebPort()))
	if err != nil {
		if instanceRunning(cfg.WebPort()) {
			log.Println("already running – opening the settings.")
			spawnWindow()
			return
		}
		log.Fatalf("settings port %d is not available: %v", cfg.WebPort(), err)
	}
	defer func() { _ = ln.Close() }()

	logs := logbuf.New(1000)
	installDesktopIntegration(cfg, logs)
	notifier := notify.NewDBus(app.Name, app.ID)

	mgr, err := manager.New(cfg, notifier, logs)
	if err != nil {
		log.Fatalf("start failed: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	status := &statusFile{errorf: logs.Errorf}
	mgr.Subscribe(status.write)
	mgr.Start(ctx)

	// Keep Dolphin's "no previews here" markers in step with the folders in the
	// Drive. It lives out here rather than inside the manager: the sync backend
	// has no business knowing about a file manager.
	previews := dolphin.NewKeeper(mgr, logs.Logf)
	go previews.Run(ctx)

	// Self-update (AppImage builds): check GitHub releases, and let the user
	// apply an update with one click from the settings window.
	upd := updater.New(version, cfg.UpdatePrerelease, logs.Logf)
	if !cfg.UpdateCheckDisabled() && upd.Status().CanSelfUpdate {
		go runUpdateChecks(ctx, upd, notifier, logs.Logf)
	}

	web := webui.New(mgr, cfg, logs, upd, previews, restartFunc(cancel, logs))
	go runTray(ctx, mgr, cancel, web.URL(), logs)

	// On first launch, open the settings window so the user can sign in.
	if !cfg.Configured() {
		logs.Logf("not signed in yet – opening the settings window")
		go func() {
			if !waitOrDone(ctx, firstWindowDelay) {
				spawnWindow()
			}
		}()
	} else {
		logs.Logf("ready. Settings via the tray icon or: %s open", app.Exec())
	}

	if err := web.Serve(ctx, ln); err != nil {
		logs.Errorf("web UI error: %v", err)
	}
	mgr.Shutdown()
	closeWindows()
	logs.Logf("stopped.")
}

// persistLog writes the daemon log to a day-rotating file with 7-day
// retention, in addition to stderr/journal. Best-effort: on failure we keep
// stderr only.
func persistLog() {
	dir, err := config.LogDir()
	if err != nil {
		return
	}
	if lw, err := logfile.New(dir, 7); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, lw))
	}
}

// installDesktopIntegration registers the desktop entry and icon - so the
// Wayland compositor can show the logo in the settings window's titlebar - and
// brings the autostart entry in line with the configuration. Best-effort.
func installDesktopIntegration(cfg *config.Config, logs *logbuf.Buffer) {
	if err := window.InstallDesktopEntry(); err != nil {
		logs.Errorf("desktop integration not possible: %v", err)
	}
	if err := window.InstallAutostart(cfg.AutostartEnabled()); err != nil {
		logs.Errorf("autostart entry not possible: %v", err)
	}
}

// runTray shows the tray icon until ctx ends. Best-effort: the daemon runs
// fine without one, so a missing tray host is only logged.
func runTray(ctx context.Context, mgr *manager.Manager, quit context.CancelFunc, webURL string, logs *logbuf.Buffer) {
	act := tray.Actions{
		OpenFolder:   func() { openFolder(mgr.LocalDir(), logs) },
		SyncNow:      mgr.SyncNow,
		TogglePause:  func() { togglePause(mgr) },
		OpenSettings: spawnWindow,
		Logout: func() {
			c, cl := context.WithTimeout(ctx, 30*time.Second)
			defer cl()
			if err := mgr.Logout(c); err != nil {
				logs.Errorf("sign-out failed: %v", err)
			}
		},
		Quit: quit,
	}
	if err := tray.Run(ctx, mgr.Subscribe, act, logs.Logf); err != nil {
		logs.Logf("no tray icon: %v (the daemon keeps running, control it via %s)", err, webURL)
	}
}

// firstWindowDelay lets the web server come up before the first-launch window
// points at it, so the user does not meet a connection error.
const firstWindowDelay = 900 * time.Millisecond

// restartFunc returns the callback the settings UI uses to restart the daemon
// after an update was installed.
func restartFunc(cancel context.CancelFunc, logs *logbuf.Buffer) func() {
	return func() {
		// Close any open settings window so the update restart is clean and no
		// stale window lingers against the old daemon.
		closeWindows()
		// Relaunch through a detached copy of ourselves, which waits for the old
		// daemon to release the port and unmount before starting.
		//
		// Not through a shell: the path would have to be quoted for sh, and Go's
		// %q is not shell quoting - an AppImage stored under a path containing
		// "$" or a backtick would be expanded rather than run.
		cmd := exec.Command(app.Exec(), "restart-wait")
		cmd.Env = os.Environ()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			logs.Errorf("could not schedule the restart: %v", err)
		}
		cancel()
	}
}

// restartDelay is how long the replacement waits for the old daemon to shut
// down: it has to release the settings port and unmount the Drive first.
const restartDelay = 2 * time.Second

// runUpdateChecks checks for updates shortly after start and then periodically,
// notifying the user once per newly discovered version.
func runUpdateChecks(ctx context.Context, upd *updater.Updater, notifier notify.Notifier, logf func(string, ...any)) {
	if waitOrDone(ctx, 4*time.Second) {
		return
	}
	var lastNotified string
	for {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		rel, err := upd.Check(cctx)
		cancel()
		if err != nil {
			logf("update check failed: %v", err)
		} else if rel != nil && rel.Version != lastNotified {
			lastNotified = rel.Version
			notifier.Notify(i18n.T("notify.update_available", rel.Tag))
		}
		if waitOrDone(ctx, 6*time.Hour) {
			return
		}
	}
}

// waitOrDone sleeps for d, returning true if ctx was cancelled first.
func waitOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

func togglePause(mgr *manager.Manager) {
	if mgr.Paused() {
		mgr.Resume()
	} else {
		mgr.Pause()
	}
}

// windowProcs tracks settings-window child processes so the daemon can close
// them (e.g. on an update restart).
var windowProcs struct {
	mu   sync.Mutex
	cmds []*exec.Cmd
}

// spawnWindow launches the settings window as a separate process so the daemon
// keeps running and GTK stays isolated on its own main thread.
//
// Every window is waited for in the background. Without that each one the user
// closes would stay a zombie for the daemon's lifetime, and the list below
// would grow with it.
func spawnWindow() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("could not start the window: %v", err)
		return
	}
	cmd := exec.Command(exe, "window")
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		log.Printf("could not start the window: %v", err)
		return
	}
	windowProcs.mu.Lock()
	windowProcs.cmds = append(windowProcs.cmds, cmd)
	windowProcs.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		forgetWindow(cmd)
	}()
}

// forgetWindow drops a finished window from the list.
func forgetWindow(cmd *exec.Cmd) {
	windowProcs.mu.Lock()
	defer windowProcs.mu.Unlock()
	for i, c := range windowProcs.cmds {
		if c == cmd {
			windowProcs.cmds = append(windowProcs.cmds[:i], windowProcs.cmds[i+1:]...)
			return
		}
	}
}

// closeWindows asks every settings window this daemon started to close. The
// goroutine spawnWindow left behind reaps each one.
func closeWindows() {
	windowProcs.mu.Lock()
	cmds := append([]*exec.Cmd{}, windowProcs.cmds...)
	windowProcs.mu.Unlock()
	for _, cmd := range cmds {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}

// openFolder opens a local folder in the file manager (tray action).
func openFolder(path string, logs *logbuf.Buffer) {
	if err := window.OpenPath(path); err != nil {
		logs.Errorf("could not open %s: %v", path, err)
	}
}
