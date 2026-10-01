// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

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
	"path/filepath"
	"strings"
	"time"

	"tdrive-sync/internal/dolphin"
	"tdrive-sync/internal/fmstate"
	"tdrive-sync/internal/manager"
	"tdrive-sync/internal/notify"
	"tdrive-sync/internal/window"
)

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
  tdrive-sync dolphin previews on|off
                                 previews in the sync folder (experimental; off keeps
                                 them from downloading every file looked at)
  tdrive-sync dolphin previews-default on|off
                                 same as a Dolphin-wide default, for folders that
                                 have no setting of their own

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
	if !instanceRunning(cfg.WebPort()) {
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
		if err := postJSON(cfg.WebPort(), "/api/offline", map[string]any{"path": rel, "on": on}); err != nil {
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
		if err := dolphin.Install(context.Background(), out); err != nil {
			log.Fatalf("installation failed: %v", err)
		}
	case "remove", "uninstall":
		if err := dolphin.Remove(context.Background(), out); err != nil {
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
		cfg := loadOrExit()
		if pv, err := dolphin.PreviewsStatus(cfg.LocalDir()); err == nil {
			out("previews in %s: %s", cfg.LocalDir(), map[bool]string{true: "off", false: "on"}[pv.Disabled])
		}
		if r.OverlayPresent && !r.OnPluginPath {
			out("")
			out("The plugin is installed but not on this process's QT_PLUGIN_PATH.")
			out("Log out and back in once, or start Dolphin with:")
			out("  QT_PLUGIN_PATH=%q dolphin", r.Paths.PluginDir)
		}
	case "previews-default":
		if len(os.Args) < 4 || (os.Args[3] != "on" && os.Args[3] != "off") {
			log.Fatal("usage: tdrive-sync dolphin previews-default on|off")
		}
		off := os.Args[3] == "off"
		if err := dolphin.SetPreviewsDefault(off); err != nil {
			log.Fatalf("could not change the default: %v", err)
		}
		if off {
			out("Dolphin's default is now “no previews” (folders with their own setting keep it).")
		} else {
			out("Dolphin's preview default is back to normal.")
		}
	case "previews":
		if len(os.Args) < 4 || (os.Args[3] != "on" && os.Args[3] != "off") {
			log.Fatal("usage: tdrive-sync dolphin previews on|off")
		}
		cfg := loadOrExit()
		off := os.Args[3] == "off"
		// Through the daemon when it runs: it also writes the marker for every
		// folder in the Drive, which needs the remote listing.
		if instanceRunning(cfg.WebPort()) {
			if err := postJSON(cfg.WebPort(), "/api/dolphin/previews", map[string]any{"disabled": off}); err != nil {
				log.Fatalf("could not change the preview setting: %v", err)
			}
		} else if err := dolphin.SetPreviews(cfg.LocalDir(), off); err != nil {
			log.Fatalf("could not change the preview setting: %v", err)
		} else if off {
			out("note: the daemon is not running – the folders inside the Drive get their")
			out("marker the next time it starts.")
		}
		if off {
			out("previews off for %s – restart Dolphin so it reads the setting.", cfg.LocalDir())
		} else {
			out("previews on for %s – looking at files downloads them again.", cfg.LocalDir())
		}
	default:
		log.Fatal("usage: tdrive-sync dolphin install|status|remove|previews on|off|previews-default on|off")
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

// cliLogin runs the OAuth flow in the terminal (for headless setups).
func cliLogin() {
	cfg := loadOrExit()
	mgr, err := manager.New(cfg, notify.Noop{}, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("starting the Google sign-in – follow the link in the browser…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// rclone opens the sign-in link via xdg-open; route that through our own
	// opener so the link reliably reaches the user's browser. Best-effort: if
	// the shim cannot be written, rclone opens the link itself as before.
	dir, err := window.InstallOpenShim()
	if err != nil {
		log.Printf("browser shim unavailable, letting rclone open the sign-in link: %v", err)
	}
	if err := mgr.Login(ctx, dir, func(line string) { fmt.Println(line) }); err != nil {
		log.Fatalf("sign-in failed: %v", err)
	}
	fmt.Println("signed in as", cfg.AccountEmail())
}

// openWindowCmd opens the settings UI in a native window (blocking).
func openWindowCmd() {
	cfg := loadOrExit()
	url := fmt.Sprintf("http://127.0.0.1:%d", cfg.WebPort())
	if err := window.Open(appName, url); err != nil {
		log.Printf("could not open the window: %v", err)
		os.Exit(1)
	}
}

func cliStatus() {
	cfg := loadOrExit()
	if !instanceRunning(cfg.WebPort()) {
		fmt.Println("the daemon is not running.")
		return
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", cfg.WebPort()))
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

// cliRestartWait waits for the old daemon to let go of the port and mount, then
// takes its place. It is the second half of restartFunc and not meant to be run
// by hand.
func cliRestartWait() {
	// This process was started from the freshly installed AppImage, so simply
	// becoming the daemon runs the new version. Waiting first, because the old
	// one still holds the settings port and the mount.
	time.Sleep(restartDelay)
	runDaemon()
}
