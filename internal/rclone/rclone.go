// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package rclone wraps the bundled rclone binary: locating it, authenticating
// a Google Drive remote, and building arguments for mount / bisync runs.
package rclone

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tdrive-sync/internal/config"
)

// Client drives a single rclone binary against our private config file.
type Client struct {
	bin       string
	conf      string
	remote    string
	creds     config.GoogleCreds
	openerDir string // PATH prefix for Login, see SetURLOpener
}

// FindBinary locates the rclone executable. Search order:
//  1. $TDRIVE_RCLONE
//  2. next to our own executable (how the AppImage ships it)
//  3. $PATH
func FindBinary() (string, error) {
	if p := os.Getenv("TDRIVE_RCLONE"); p != "" {
		if isExec(p) {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, "rclone"),
			filepath.Join(dir, "..", "lib", "tdrive-sync", "rclone"),
		} {
			if isExec(cand) {
				return cand, nil
			}
		}
	}
	if p, err := exec.LookPath("rclone"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("rclone binary not found (set $TDRIVE_RCLONE or install rclone)")
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// New creates a Client, locating the binary and resolving the config path.
func New(remote string, creds config.GoogleCreds) (*Client, error) {
	bin, err := FindBinary()
	if err != nil {
		return nil, err
	}
	conf, err := config.RcloneConfPath()
	if err != nil {
		return nil, err
	}
	return &Client{bin: bin, conf: conf, remote: remote, creds: creds}, nil
}

// SetCreds updates the custom OAuth client used for the next Login. It has no
// effect on a remote that already exists (the client is baked in at creation
// time), so callers should only change it while signed out.
func (c *Client) SetCreds(creds config.GoogleCreds) { c.creds = creds }

// SetURLOpener puts dir first on the PATH of the login command. rclone shows
// the OAuth link by shelling out to xdg-open, so a directory holding our own
// xdg-open shim routes that call into the app's opener (see
// window.InstallOpenShim). Passing "" leaves the environment alone, and rclone
// opens the link on its own.
func (c *Client) SetURLOpener(dir string) { c.openerDir = dir }

// Bin returns the resolved rclone binary path.
func (c *Client) Bin() string { return c.bin }

// Conf returns the rclone config file path.
func (c *Client) Conf() string { return c.conf }

// Remote returns the remote spec ("name:").
func (c *Client) Remote() string { return c.remote + ":" }

func (c *Client) base() []string { return []string{"--config", c.conf} }

// RemoteExists reports whether the remote is already defined in the config.
func (c *Client) RemoteExists() bool {
	data, err := os.ReadFile(c.conf)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "["+c.remote+"]")
}

// Login runs the interactive OAuth flow, streaming rclone's stdout/stderr line
// by line to onLine (used to surface the "open this URL" prompt to the user).
func (c *Client) Login(ctx context.Context, onLine func(string)) error {
	args := append(c.base(),
		"config", "create", c.remote, "drive",
		"scope", "drive",
		"config_is_local", "true",
	)
	if c.creds.ClientID != "" {
		args = append(args, "client_id", c.creds.ClientID)
	}
	if c.creds.ClientSecret != "" {
		args = append(args, "client_secret", c.creds.ClientSecret)
	}
	cmd := exec.CommandContext(ctx, c.bin, args...)
	if c.openerDir != "" {
		cmd.Env = append(os.Environ(),
			"PATH="+c.openerDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return streamRun(cmd, onLine)
}

// Logout deletes the remote definition.
func (c *Client) Logout(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.bin, append(c.base(), "config", "delete", c.remote)...)
	return cmd.Run()
}

// UserEmail attempts to read the signed-in account's email address. Best effort:
// returns "" without error if the backend does not expose it.
func (c *Client) UserEmail(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, c.bin, append(c.base(), "config", "userinfo", c.Remote(), "--json")...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var info map[string]any
	if err := json.Unmarshal(out, &info); err != nil {
		return ""
	}
	for _, k := range []string{"email", "emailAddress", "user"} {
		if v, ok := info[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// MountArgs builds the argument list for `rclone mount` in stream mode.
// rcAddr enables the remote-control API used to warm/refresh the VFS cache.
func (c *Client) MountArgs(mountpoint, rcAddr, cacheDir string) []string {
	return append(c.base(),
		"mount", c.Remote(), mountpoint,
		// Local read/write cache: edits land on disk instantly, uploads run in
		// the background. Nothing is evicted, so offline-pinned files persist.
		"--vfs-cache-mode", "full",
		"--vfs-cache-max-age", "9999h",
		"--vfs-cache-max-size", "off",
		"--vfs-write-back", "5s",
		"--vfs-read-ahead", "128M",
		"--cache-dir", cacheDir,
		// Cache directory listings for a long time and detect remote changes via
		// Drive's change-polling instead — this is the big win against lag when
		// browsing/copying, since file managers stat constantly.
		"--dir-cache-time", "1000h",
		"--poll-interval", "10s",
		"--attr-timeout", "3s",
		// Throughput.
		"--buffer-size", "32M",
		"--vfs-read-chunk-size", "32M",
		"--vfs-read-chunk-size-limit", "1G",
		"--transfers", "8",
		"--checkers", "16",
		"--vfs-fast-fingerprint",
		"--use-mmap",
		// Control API for status/refresh + POSIX perms. The RC credentials are
		// passed via RCLONE_RC_USER/RCLONE_RC_PASS in the process environment
		// (see manager.startProc) so they do not show up in the process list.
		"--rc", "--rc-addr", rcAddr,
		"--file-perms", "0644",
		"--dir-perms", "0755",
	)
}

// BisyncArgs builds the argument list for `rclone bisync` in mirror mode.
//
// Path1 is the Drive remote (the cloud), Path2 is the local directory, so any
// "cloud wins" behaviour maps to Path1.
//
//   - workdir holds bisync's state and lock files (so we can clean up stale
//     locks from crashed runs).
//   - conflictMode "manual" keeps both versions of a conflicting file for the
//     user to resolve; anything else means automatic resolution: newest wins,
//     the losing copy is kept as a dated backup.
//   - resync performs a full reconciliation (first run or auto-recovery); on an
//     undecidable difference the cloud (Path1) wins.
func (c *Client) BisyncArgs(localDir, workdir, conflictMode string, resync bool) []string {
	args := append(c.base(),
		"bisync", c.Remote(), localDir,
		"--workdir", workdir,
		"--create-empty-src-dirs",
		// Recover from interruptions / less-serious errors without needing a
		// manual --resync, and treat a stale lock as recoverable.
		"--resilient",
		"--recover",
		"--max-lock", "2m",
		"--transfers", "8",
		"-v",
	)
	if conflictMode == config.ConflictManual {
		// Keep both sides renamed (…conflict1 = Drive, …conflict2 = local); the
		// user picks a winner in the settings UI.
		args = append(args,
			"--conflict-resolve", "none",
			"--conflict-loser", "num",
			"--conflict-suffix", "conflict",
		)
	} else {
		// Newest file wins; the losing copy is preserved as a dated backup.
		args = append(args,
			"--conflict-resolve", "newer",
			"--conflict-loser", "pathname",
			"--conflict-suffix", "conflict-"+time.Now().Format("2006-01-02"),
		)
	}
	if resync {
		// Full reconciliation. Path1 (the cloud) is authoritative when a
		// difference cannot be decided otherwise.
		args = append(args, "--resync", "--resync-mode", "path1")
	}
	return args
}

// Entry is one item returned by List.
type Entry struct {
	Name  string `json:"Name"`
	Path  string `json:"Path"`
	IsDir bool   `json:"IsDir"`
	Size  int64  `json:"Size"`
}

// ListDirs returns every folder below a Drive-relative path, recursively and
// Drive-relative. One rclone call, listings only – no file content is touched.
func (c *Client) ListDirs(ctx context.Context, rel string) ([]string, error) {
	args := append(c.base(), "lsjson", "--dirs-only", "--recursive",
		"--no-modtime", "--no-mimetype", c.Remote()+rel)
	out, err := exec.CommandContext(ctx, c.bin, args...).Output()
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		p := e.Path
		if rel != "" {
			p = rel + "/" + p
		}
		dirs = append(dirs, p)
	}
	return dirs, nil
}

// List returns the immediate children of a Drive-relative directory ("" = root).
func (c *Client) List(ctx context.Context, rel string) ([]Entry, error) {
	remote := c.Remote() + rel
	args := append(c.base(), "lsjson", remote)
	out, err := exec.CommandContext(ctx, c.bin, args...).Output()
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}
	// Directories first, then alphabetical.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// streamRun executes cmd and forwards every combined output line to onLine.
func streamRun(cmd *exec.Cmd, onLine func(string)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // rclone prints the OAuth URL to stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if onLine != nil {
			onLine(scanner.Text())
		}
	}
	return cmd.Wait()
}
