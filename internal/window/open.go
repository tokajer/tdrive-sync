// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// This file owns every hand-off to the desktop: opening a URL in the user's
// browser and opening a folder in the file manager. Both go through one runner
// so the environment fixes (see childEnv) and the fallbacks apply everywhere,
// and so a broken xdg-open never leaves the user without a browser.

const (
	// launcherTimeout bounds a launcher run. xdg-open and friends return as soon
	// as they have handed the target over, so this only catches a hang.
	launcherTimeout = 20 * time.Second
	// startupGrace is how long a browser started directly is watched for an
	// immediate failure before it counts as successfully launched. Such a
	// browser keeps running in the foreground, so we cannot wait for its exit.
	startupGrace = 700 * time.Millisecond
)

// launcher is one candidate command for opening a target.
type launcher struct {
	name string   // executable, looked up on the sanitised PATH
	args []string // arguments; "%s" is replaced with the target, else it is appended
	// wait reports that the command exits as soon as it has handed the target
	// over, so its exit status tells us whether it worked.
	wait bool
}

// OpenExternal opens an http(s) URL with the user's browser. Anything else is
// refused: the URL can come from a web page (a link clicked in the settings
// window), and only http(s) is safe to pass to a desktop handler.
func OpenExternal(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("refusing to open %q: only http and https URLs are opened", rawURL)
	}
	return run(u.String(), browserLaunchers())
}

// OpenPath opens a local file or directory in the desktop's file manager.
func OpenPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("no path given")
	}
	return run(path, fileLaunchers())
}

// browserLaunchers lists the commands tried for a URL: the user's own $BROWSER
// first, then the desktop's generic handlers, then browsers that may be
// installed. The long tail matters on systems where xdg-open is missing or
// misconfigured — the case that leaves a user staring at a dead sign-in button.
func browserLaunchers() []launcher {
	out := browserEnvLaunchers()
	out = append(out, genericLaunchers()...)
	for _, bin := range []string{
		"x-www-browser", "sensible-browser", "firefox", "chromium", "chromium-browser",
		"google-chrome", "google-chrome-stable", "microsoft-edge", "brave-browser", "epiphany",
	} {
		out = append(out, launcher{name: bin})
	}
	return out
}

// fileLaunchers lists the commands tried for a local path.
func fileLaunchers() []launcher {
	return append(genericLaunchers(), launcher{name: "nautilus"}, launcher{name: "dolphin"}, launcher{name: "thunar"})
}

// genericLaunchers are the desktop-provided "open this with whatever is
// registered" helpers, all of which return immediately.
func genericLaunchers() []launcher {
	return []launcher{
		{name: "xdg-open", wait: true},
		{name: "gio", args: []string{"open", "%s"}, wait: true},
		{name: "kde-open", wait: true},
		{name: "gnome-open", wait: true},
	}
}

// browserEnvLaunchers turns $BROWSER into launchers. It holds a colon-separated
// list of commands, each optionally using "%s" as the URL placeholder.
func browserEnvLaunchers() []launcher {
	var out []launcher
	for _, entry := range strings.Split(os.Getenv("BROWSER"), ":") {
		fields := strings.Fields(entry)
		if len(fields) == 0 {
			continue
		}
		out = append(out, launcher{name: fields[0], args: fields[1:]})
	}
	return out
}

// run tries each launcher in turn and returns nil as soon as one took the
// target. Every failure is collected so the log names what was tried.
func run(target string, cands []launcher) error {
	env := childEnv()
	path := envValue(env, "PATH")
	var failures []string
	for _, c := range cands {
		bin, ok := lookIn(path, c.name)
		if !ok {
			continue
		}
		if err := start(bin, withTarget(c.args, target), env, c.wait); err != nil {
			failures = append(failures, c.name+": "+err.Error())
			continue
		}
		return nil
	}
	if len(failures) > 0 {
		return fmt.Errorf("no desktop handler could open %q (tried %s)", target, strings.Join(failures, "; "))
	}
	return fmt.Errorf("found no desktop handler to open %q", target)
}

// start launches one command. A launcher is waited for and judged by its exit
// status; a browser started directly is only watched for an immediate failure,
// since it stays in the foreground for as long as the user keeps it open.
func start(bin string, args []string, env []string, wait bool) error {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	// Own process group: the browser must survive the daemon (or the settings
	// window) exiting.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timeout := startupGrace
	if wait {
		timeout = launcherTimeout
	}
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		if wait {
			_ = cmd.Process.Kill()
			return fmt.Errorf("timed out after %s", launcherTimeout)
		}
		// Still running after the grace period: it launched.
		return nil
	}
}

// withTarget fills the target into a launcher's arguments, replacing the first
// "%s" placeholder or appending it when there is none.
func withTarget(args []string, target string) []string {
	out := make([]string, 0, len(args)+1)
	filled := false
	for _, a := range args {
		if !filled && strings.Contains(a, "%s") {
			a = strings.Replace(a, "%s", target, 1)
			filled = true
		}
		out = append(out, a)
	}
	if !filled {
		out = append(out, target)
	}
	return out
}

// childEnv is the environment handed to a desktop handler. It drops the
// variables an AppImage (or our own WebKit setup) injects, which otherwise make
// a system browser start against bundled libraries — a classic reason for
// "nothing happens" — and takes our own xdg-open shim off the PATH so the shim
// cannot call itself.
func childEnv() []string {
	drop := map[string]bool{
		"LD_LIBRARY_PATH":                true,
		"LD_PRELOAD":                     true,
		"GTK_PATH":                       true,
		"GDK_PIXBUF_MODULE_FILE":         true,
		"GIO_MODULE_DIR":                 true,
		"GSETTINGS_SCHEMA_DIR":           true,
		"PYTHONHOME":                     true,
		"WEBKIT_DISABLE_DMABUF_RENDERER": true,
	}
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || drop[k] {
			continue
		}
		if k == "PATH" {
			v = cleanPath(v)
		}
		out = append(out, k+"="+v)
	}
	return out
}

// cleanPath removes our shim directory and anything inside a mounted AppImage
// from a PATH value, falling back to a sane default if nothing is left.
func cleanPath(path string) string {
	appDir := os.Getenv("APPDIR")
	shim := shimDir()
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || (shim != "" && dir == shim) {
			continue
		}
		if appDir != "" && (dir == appDir || strings.HasPrefix(dir, appDir+string(os.PathSeparator))) {
			continue
		}
		keep = append(keep, dir)
	}
	if len(keep) == 0 {
		return "/usr/local/bin:/usr/bin:/bin"
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// envValue reads a variable out of an environment slice.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// lookIn resolves name to an executable on the given PATH value. exec.LookPath
// cannot be used here: it searches our own PATH, which still contains the shim.
func lookIn(path, name string) (string, bool) {
	if strings.ContainsRune(name, os.PathSeparator) {
		if isExecutable(name) {
			return name, true
		}
		return "", false
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name)
		if isExecutable(cand) {
			return cand, true
		}
	}
	return "", false
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// shimDir is the directory holding our xdg-open shim (see InstallOpenShim).
func shimDir() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "tdrive-sync", "shim")
}

// InstallOpenShim writes an "xdg-open" shim into the app's cache directory and
// returns that directory, for use as the first PATH entry of a child process.
//
// rclone opens the OAuth link by shelling out to xdg-open, and its
// --auth-no-open-browser flag does not exist on `config create`, so this is the
// only way to make the sign-in link go through our own opener: the shim hands
// the URL back to us. That keeps it at exactly one browser window and means a
// failure is reported in our log instead of vanishing inside rclone.
func InstallOpenShim() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := shimDir()
	if dir == "" {
		return "", errors.New("no cache directory available")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	script := "#!/bin/sh\n" +
		"# Generated by tdrive-sync: hands a URL to the app's own opener.\n" +
		"exec " + shellQuote(exe) + " open-url \"$@\"\n"
	path := filepath.Join(dir, "xdg-open")
	if old, err := os.ReadFile(path); err != nil || string(old) != script {
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// shellQuote wraps s in single quotes for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
