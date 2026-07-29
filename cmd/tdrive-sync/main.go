// Command tdrive-sync is a Google Drive sync client with a tray icon and a local
// settings UI, modelled on the Windows Google Drive client. It uses a bundled
// rclone binary as its sync engine.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tdrive-sync/internal/config"
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
	case "version", "--version", "-v":
		fmt.Println("tdrive-sync", version)
	default:
		usage()
	}
}

func usage() {
	fmt.Print(`tdrive-sync – Google-Drive-Synchronisation

Verwendung:
  tdrive-sync [run]     Daemon mit Tray-Icon und Einstellungs-Fenster starten (Standard)
  tdrive-sync login     Google-Konto in der Konsole verbinden (headless)
  tdrive-sync open      Einstellungs-Fenster öffnen
  tdrive-sync status    Aktuellen Status anzeigen
  tdrive-sync version   Version anzeigen
`)
}

func loadOrExit() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Konfiguration konnte nicht geladen werden: %v", err)
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
		log.Printf("Desktop-Integration nicht möglich: %v", err)
	}

	// Start on boot: register (or remove) the XDG autostart entry per config.
	if err := window.InstallAutostart(cfg.AutostartEnabled()); err != nil {
		log.Printf("Autostart-Eintrag nicht möglich: %v", err)
	}

	// Single-instance: if a daemon already answers on the web port, just open
	// its settings UI and exit (mimics clicking the app icon again).
	if instanceRunning(cfg.WebPort) {
		log.Println("Läuft bereits – öffne Einstellungen.")
		spawnWindow()
		return
	}

	logs := logbuf.New(1000)
	logf := logs.Logf
	notifier := notify.NewDBus("TDrive Sync", "tdrive-sync")

	mgr, err := manager.New(cfg, notifier, logf)
	if err != nil {
		log.Fatalf("Start fehlgeschlagen: %v", err)
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
			OpenFolder:   func() { openURL(cfg.LocalDir) },
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
			log.Printf("Kein Tray-Icon: %v (Daemon läuft weiter, Steuerung über %s)", err, web.URL())
		}
	}()

	// On first launch, open the settings window so the user can sign in.
	if !cfg.Configured() {
		log.Println("Noch nicht angemeldet – öffne Einstellungs-Fenster")
		go func() { time.Sleep(900 * time.Millisecond); spawnWindow() }()
	} else {
		log.Printf("Bereit. Einstellungen über das Tray-Icon oder: %s open", exeName())
	}

	if err := web.ListenAndServe(ctx); err != nil {
		log.Printf("Web-UI-Fehler: %v", err)
	}
	mgr.Shutdown()
	log.Println("Beendet.")
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
			logf("Update-Prüfung fehlgeschlagen: %v", err)
		} else if rel != nil && rel.Version != lastNotified {
			lastNotified = rel.Version
			notifier.Notify("TDrive Sync", "Update verfügbar: "+rel.Tag+" – im Einstellungs-Fenster installieren")
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
	fmt.Println("Starte Google-Anmeldung – folge dem Link im Browser…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := mgr.Login(ctx, func(line string) { fmt.Println(line) }); err != nil {
		log.Fatalf("Anmeldung fehlgeschlagen: %v", err)
	}
	fmt.Println("Angemeldet als", cfg.AccountEmail)
}

// openWindowCmd opens the settings UI in a native window (blocking).
func openWindowCmd() {
	cfg := loadOrExit()
	url := fmt.Sprintf("http://127.0.0.1:%d", cfg.WebPort)
	if err := window.Open("TDrive Sync", url); err != nil {
		log.Printf("Fenster konnte nicht geöffnet werden: %v", err)
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
		log.Printf("Fenster-Start fehlgeschlagen: %v", err)
		return
	}
	cmd := exec.Command(exe, "window")
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		log.Printf("Fenster-Start fehlgeschlagen: %v", err)
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
		fmt.Println("Daemon läuft nicht.")
		return
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", cfg.WebPort))
	if err != nil {
		fmt.Println("Status nicht abrufbar:", err)
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

func openURL(target string) {
	if err := exec.Command("xdg-open", target).Start(); err != nil {
		log.Printf("xdg-open fehlgeschlagen: %v", err)
	}
}
