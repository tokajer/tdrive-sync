// Command tdrive-sync is a Google Drive sync client with a tray icon and a local
// settings UI, modelled on the Windows Google Drive client. It uses a bundled
// rclone binary as its sync engine.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/dolphin"
	"tdrive-sync/internal/fmstate"
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

// appName is the window title and the sender shown on desktop notifications.
const appName = "TDrive Sync"

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
	case "version", "--version", "-v":
		fmt.Println("tdrive-sync", version)
	default:
		usage()
	}
}

func usage() {
	fmt.Print(`tdrive-sync – Google Drive synchronisation

Usage:
  tdrive-sync [run]              start the daemon with tray icon and settings window (default)
  tdrive-sync login              connect a Google account from the console (headless)
  tdrive-sync open               open the settings window
  tdrive-sync status             print the current status
  tdrive-sync version            print the version

File manager integration (KDE/Dolphin):
  tdrive-sync dolphin install    build and install the overlay-icon plugin
  tdrive-sync dolphin status     show whether the plugin is in place
  tdrive-sync dolphin remove     uninstall it again

  tdrive-sync offline on|off <path>…   keep paths offline, or release them
  tdrive-sync file-state <path>…       print the sync state of paths
`)
}

// cliOpenURL opens a URL in the user's browser.
func cliOpenURL() {
	if len(os.Args) < 3 {
		log.Fatal("usage: tdrive-sync open-url <url>")
	}
	if err := window.OpenExternal(os.Args[2]); err != nil {
		log.Fatalf("could not open %s: %v", os.Args[2], err)
	}
}

// cliOffline pins paths for offline use or releases them again. The Dolphin
// context menu calls this with absolute paths inside the sync folder.
func cliOffline() {
	if len(os.Args) < 4 || (os.Args[2] != "on" && os.Args[2] != "off") {
		log.Fatal("usage: tdrive-sync offline on|off <path>…")
	}
	on := os.Args[2] == "on"
	cfg := loadOrExit()
	if !instanceRunning(cfg.WebPort) {
		log.Fatal("the daemon is not running.")
	}
	info, err := fmstate.Load()
	if err != nil {
		log.Fatalf("could not read the sync state: %v", err)
	}
	for _, arg := range os.Args[3:] {
		abs, err := filepath.Abs(arg)
		if err != nil {
			log.Printf("skipping %s: %v", arg, err)
			continue
		}
		rel, ok := info.Rel(abs)
		if !ok {
			log.Printf("skipping %s: not inside %s", abs, info.Root)
			continue
		}
		if err := postJSON(cfg.WebPort, "/api/offline", map[string]any{"path": rel, "on": on}); err != nil {
			log.Printf("%s: %v", rel, err)
			continue
		}
		if on {
			fmt.Printf("keeping offline: %s\n", rel)
		} else {
			fmt.Printf("online only: %s\n", rel)
		}
	}
}

// cliFileState prints the sync state of paths (the same states the file-manager
// indicator shows).
func cliFileState() {
	if len(os.Args) < 3 {
		log.Fatal("usage: tdrive-sync file-state <path>…")
	}
	info, err := fmstate.Load()
	if err != nil {
		log.Fatalf("could not read the sync state: %v", err)
	}
	for _, arg := range os.Args[2:] {
		abs, err := filepath.Abs(arg)
		if err != nil {
			log.Printf("skipping %s: %v", arg, err)
			continue
		}
		state := string(info.Resolve(abs))
		if state == "" {
			state = "-"
		}
		fmt.Printf("%-9s %s\n", state, abs)
	}
}

// cliDolphin installs, inspects or removes the Dolphin integration.
func cliDolphin() {
	sub := "install"
	if len(os.Args) > 2 {
		sub = os.Args[2]
	}
	out := func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	switch sub {
	case "install":
		if err := dolphin.Install(out); err != nil {
			log.Fatalf("installation failed: %v", err)
		}
	case "remove", "uninstall":
		if err := dolphin.Remove(out); err != nil {
			log.Fatalf("removal failed: %v", err)
		}
	case "status":
		r, err := dolphin.Status()
		if err != nil {
			log.Fatal(err)
		}
		out("overlay plugin:      %s", present(r.OverlayPresent, r.Paths.Overlay))
		out("context menu plugin: %s", present(r.ActionPresent, r.Paths.Action))
		out("environment entry:   %s", present(r.EnvFilePresent, r.Paths.EnvFile))
		out("on QT_PLUGIN_PATH:   %t", r.OnPluginPath)
		if r.OverlayPresent && !r.OnPluginPath {
			out("")
			out("The plugin is installed but not on this process's QT_PLUGIN_PATH.")
			out("Log out and back in once, or start Dolphin with:")
			out("  QT_PLUGIN_PATH=%q dolphin", r.Paths.PluginDir)
		}
	default:
		log.Fatal("usage: tdrive-sync dolphin install|status|remove")
	}
}

func present(ok bool, path string) string {
	if ok {
		return "installed – " + path
	}
	return "missing"
}

// postJSON sends a JSON body to the daemon's local API.
func postJSON(port int, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
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

	// Persist the daemon log to a day-rotating file with 7-day retention, in
	// addition to stderr/journal. Best-effort: on failure we keep stderr only.
	if dir, err := config.LogDir(); err == nil {
		if lw, err := logfile.New(dir, 7); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, lw))
		}
	}

	// Register a user-scope .desktop file + icon so the Wayland compositor can
	// show the app logo in the settings window's titlebar/taskbar (best-effort).
	if err := window.InstallDesktopEntry(); err != nil {
		log.Printf("desktop integration not possible: %v", err)
	}

	// Start on boot: register (or remove) the XDG autostart entry per config.
	if err := window.InstallAutostart(cfg.AutostartEnabled()); err != nil {
		log.Printf("autostart entry not possible: %v", err)
	}

	// Single-instance: if a daemon already answers on the web port, just open
	// its settings UI and exit (mimics clicking the app icon again).
	if instanceRunning(cfg.WebPort) {
		log.Println("already running – opening the settings.")
		spawnWindow()
		return
	}

	logs := logbuf.New(1000)
	logf := logs.Logf
	notifier := notify.NewDBus(appName, "tdrive-sync")

	mgr, err := manager.New(cfg, notifier, logf)
	if err != nil {
		log.Fatalf("start failed: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// JSON status API: mirror every status change to status.json for monitoring.
	mgr.Subscribe(func(s manager.Status) { writeStatusFile(s) })

	mgr.Start(ctx)

	// Self-update (AppImage builds): check GitHub releases, and let the user
	// apply an update with one click from the settings window.
	upd := updater.New(version, cfg.UpdatePrerelease, logf)
	restart := func() {
		// Close any open settings window so the update restart is clean and no
		// stale window lingers against the old daemon.
		closeWindows()
		exe := os.Getenv("APPIMAGE")
		if exe == "" {
			if e, err := os.Executable(); err == nil {
				exe = e
			}
		}
		if exe != "" {
			// Relaunch after a short delay so the old daemon releases the port
			// and unmounts first.
			_ = exec.Command("sh", "-c", fmt.Sprintf("sleep 2; exec %q run", exe)).Start()
		}
		cancel()
	}
	if !cfg.UpdateCheckDisabled && upd.Status().CanSelfUpdate {
		go runUpdateChecks(ctx, upd, notifier, logf)
	}

	web := webui.New(mgr, cfg, logs, upd, restart)

	// Tray icon (best-effort; the daemon runs fine without it).
	go func() {
		act := tray.Actions{
			OpenFolder:   func() { openFolder(cfg.LocalDir) },
			SyncNow:      func() { mgr.SyncNow() },
			TogglePause:  func() { togglePause(mgr) },
			OpenSettings: func() { spawnWindow() },
			Logout: func() {
				c, cl := context.WithTimeout(context.Background(), 30*time.Second)
				defer cl()
				_ = mgr.Logout(c)
			},
			Quit: cancel,
		}
		if err := tray.Run(ctx, mgr, act, logf); err != nil {
			log.Printf("no tray icon: %v (the daemon keeps running, control it via %s)", err, web.URL())
		}
	}()

	// On first launch, open the settings window so the user can sign in.
	if !cfg.Configured() {
		log.Println("not signed in yet – opening the settings window")
		go func() { time.Sleep(900 * time.Millisecond); spawnWindow() }()
	} else {
		log.Printf("ready. Settings via the tray icon or: %s open", exeName())
	}

	if err := web.ListenAndServe(ctx); err != nil {
		log.Printf("web UI error: %v", err)
	}
	mgr.Shutdown()
	log.Println("stopped.")
}

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
			notifier.Notify(appName, i18n.T("notify.update_available", rel.Tag))
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

// writeStatusFile atomically writes the current status to status.json so
// external tooling can monitor the sync without talking to the HTTP API.
func writeStatusFile(s manager.Status) {
	path, err := config.StatusPath()
	if err != nil {
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func togglePause(mgr *manager.Manager) {
	if mgr.Status().State == manager.StatePaused {
		mgr.Resume()
	} else {
		mgr.Pause()
	}
}

// cliLogin runs the OAuth flow in the terminal (for headless setups).
func cliLogin() {
	cfg := loadOrExit()
	mgr, err := manager.New(cfg, notify.Noop{}, func(f string, a ...any) {})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("starting the Google sign-in – follow the link in the browser…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := mgr.Login(ctx, func(line string) { fmt.Println(line) }); err != nil {
		log.Fatalf("sign-in failed: %v", err)
	}
	fmt.Println("signed in as", cfg.AccountEmail)
}

// openWindowCmd opens the settings UI in a native window (blocking).
func openWindowCmd() {
	cfg := loadOrExit()
	url := fmt.Sprintf("http://127.0.0.1:%d", cfg.WebPort)
	if err := window.Open(appName, url); err != nil {
		log.Printf("could not open the window: %v", err)
		os.Exit(1)
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
}

// closeWindows asks every settings window this daemon started to close.
func closeWindows() {
	windowProcs.mu.Lock()
	cmds := windowProcs.cmds
	windowProcs.cmds = nil
	windowProcs.mu.Unlock()
	for _, cmd := range cmds {
		if cmd.Process == nil {
			continue
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		go func(c *exec.Cmd) { _, _ = c.Process.Wait() }(cmd)
	}
}

func exeName() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "tdrive-sync"
}

func cliStatus() {
	cfg := loadOrExit()
	if !instanceRunning(cfg.WebPort) {
		fmt.Println("the daemon is not running.")
		return
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", cfg.WebPort))
	if err != nil {
		fmt.Println("status not available:", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func instanceRunning(port int) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// openFolder opens a local folder in the file manager (tray action).
func openFolder(path string) {
	if err := window.OpenPath(path); err != nil {
		log.Printf("could not open %s: %v", path, err)
	}
}
