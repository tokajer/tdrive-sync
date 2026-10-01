// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads and persists the application configuration.
//
// One *Config is shared by every goroutine in the daemon: the HTTP handlers,
// the manager and both sync runners. Its fields are therefore unexported and
// reachable only through accessors that hold the mutex, and every setter
// persists the file while still holding it. Callers cannot forget to save, and
// a reader can never observe a half-applied change.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"tdrive-sync/internal/i18n"
	"tdrive-sync/internal/xdg"
)

// SyncMode selects how the Drive is made available locally.
type SyncMode string

const (
	// ModeStream mounts the whole Drive as a virtual filesystem; files are
	// downloaded on demand. Individual paths can be pinned for offline use.
	ModeStream SyncMode = "stream"
	// ModeMirror keeps a full two-way-synced local copy of the Drive.
	ModeMirror SyncMode = "mirror"
)

// Modes lists every selectable sync mode. Callers validating user input go
// through ParseMode rather than comparing against the constants themselves.
var Modes = []SyncMode{ModeStream, ModeMirror}

// ParseMode maps a string to a sync mode, reporting whether it names one.
func ParseMode(s string) (SyncMode, bool) {
	for _, m := range Modes {
		if string(m) == s {
			return m, true
		}
	}
	return "", false
}

// Conflict resolution strategies for mirror mode.
const (
	// ConflictAuto resolves conflicts automatically: the newest file wins, the
	// cloud wins when the two sides cannot be reconciled, and the losing copy is
	// kept as a dated backup.
	ConflictAuto = "auto"
	// ConflictManual keeps both versions of a conflicting file so the user can
	// decide in the UI which one wins.
	ConflictManual = "manual"
)

// GoogleCreds holds an optional custom OAuth client. When both fields are
// empty rclone's built-in Drive credentials are used. Filling these in is the
// single change needed to move to a dedicated Google Cloud OAuth client later.
type GoogleCreds struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

// Configured reports whether a custom OAuth client is set (both fields present).
func (g GoogleCreds) Configured() bool {
	return g.ClientID != "" && g.ClientSecret != ""
}

// ParseGoogleCredsJSON extracts the OAuth client_id and client_secret from a
// credentials file downloaded from the Google Cloud console. It accepts the
// "Desktop app" / "Web app" wrapper ({"installed": {…}} / {"web": {…}}) as well
// as a flat {"client_id": …, "client_secret": …} object.
func ParseGoogleCredsJSON(data []byte) (GoogleCreds, error) {
	type oauthClient struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	var raw struct {
		Installed *oauthClient `json:"installed"`
		Web       *oauthClient `json:"web"`
		oauthClient
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return GoogleCreds{}, fmt.Errorf("%s: %w", i18n.T("err.creds_json_read"), err)
	}
	pick := raw.oauthClient
	if raw.Installed != nil {
		pick = *raw.Installed
	} else if raw.Web != nil {
		pick = *raw.Web
	}
	if pick.ClientID == "" || pick.ClientSecret == "" {
		return GoogleCreds{}, errors.New(i18n.T("err.creds_json_fields"))
	}
	return GoogleCreds{ClientID: pick.ClientID, ClientSecret: pick.ClientSecret}, nil
}

// data is the persisted part of the configuration, separated from the lock and
// the file path so it can be marshalled as a whole.
type data struct {
	// AccountEmail is informational, filled after a successful login.
	AccountEmail string `yaml:"account_email"`
	// RemoteName is the rclone remote name used internally.
	RemoteName string `yaml:"remote_name"`
	// Mode is the active sync mode.
	Mode SyncMode `yaml:"sync_mode"`
	// LocalDir is the mount point (stream) or the mirror root (mirror).
	LocalDir string `yaml:"local_dir"`
	// OfflinePaths are Drive-relative paths kept available offline in stream
	// mode (e.g. "Documents", "Photos/2024").
	OfflinePaths []string `yaml:"offline_paths"`
	// MirrorIntervalSec is how often mirror mode reconciles, in seconds.
	MirrorIntervalSec int `yaml:"mirror_interval_sec"`
	// ConflictMode is how mirror-mode sync conflicts are handled
	// ("auto" or "manual"). Empty is treated as "auto".
	ConflictMode string `yaml:"conflict_mode"`
	// AutostartDisabled turns off the "start on login" autostart entry when set.
	AutostartDisabled bool `yaml:"autostart_disabled"`
	// UpdatePrerelease includes prereleases when checking for updates.
	UpdatePrerelease bool `yaml:"update_prerelease"`
	// UpdateCheckDisabled turns off automatic update checks (on start and
	// periodically) when set.
	UpdateCheckDisabled bool `yaml:"update_check_disabled"`
	// WebPort is the local settings-UI port (127.0.0.1 only).
	WebPort int `yaml:"web_port"`
	// Google holds optional custom OAuth credentials.
	Google GoogleCreds `yaml:"google"`
}

// Config is the persisted application state. It is safe for concurrent use.
type Config struct {
	mu   sync.Mutex
	d    data
	path string
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{d: data{
		RemoteName:        "gdrive",
		Mode:              ModeStream,
		LocalDir:          filepath.Join(home, "GoogleDrive"),
		OfflinePaths:      []string{},
		MirrorIntervalSec: 300,
		ConflictMode:      ConflictManual,
		WebPort:           45677,
	}}
}

// Dir returns the configuration directory, creating it if necessary.
func Dir() (string, error) { return xdg.ConfigDir() }

// Path returns the config file path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Load reads the config from disk, falling back to defaults for a missing file.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	cfg := Default()
	cfg.path = path

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(raw, &cfg.d); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.d.RemoteName == "" {
		cfg.d.RemoteName = "gdrive"
	}
	if cfg.d.ConflictMode == "" {
		cfg.d.ConflictMode = ConflictManual
	}
	if _, ok := ParseMode(string(cfg.d.Mode)); !ok {
		cfg.d.Mode = ModeStream
	}
	// Normalise pins written by an older version or edited by hand, so the
	// indicator's prefix matching and our own agree on what is pinned.
	cfg.d.OfflinePaths = normalizeOffline(cfg.d.OfflinePaths)
	return cfg, nil
}

// -------- readers --------

// AccountEmail returns the signed-in account, empty when signed out.
func (c *Config) AccountEmail() string { return get(c, func(d *data) string { return d.AccountEmail }) }

// RemoteName returns the internal rclone remote name.
func (c *Config) RemoteName() string { return get(c, func(d *data) string { return d.RemoteName }) }

// Mode returns the active sync mode.
func (c *Config) Mode() SyncMode { return get(c, func(d *data) SyncMode { return d.Mode }) }

// LocalDir returns the mount point (stream) or mirror root (mirror).
func (c *Config) LocalDir() string { return get(c, func(d *data) string { return d.LocalDir }) }

// MirrorIntervalSec returns the mirror-mode reconcile interval in seconds.
func (c *Config) MirrorIntervalSec() int {
	return get(c, func(d *data) int { return d.MirrorIntervalSec })
}

// ConflictMode returns how mirror-mode conflicts are handled.
func (c *Config) ConflictMode() string { return get(c, func(d *data) string { return d.ConflictMode }) }

// AutostartEnabled reports whether the app should start on login.
func (c *Config) AutostartEnabled() bool {
	return get(c, func(d *data) bool { return !d.AutostartDisabled })
}

// UpdatePrerelease reports whether prereleases are considered for updates.
func (c *Config) UpdatePrerelease() bool {
	return get(c, func(d *data) bool { return d.UpdatePrerelease })
}

// UpdateCheckDisabled reports whether automatic update checks are switched off.
func (c *Config) UpdateCheckDisabled() bool {
	return get(c, func(d *data) bool { return d.UpdateCheckDisabled })
}

// WebPort returns the settings-UI port.
func (c *Config) WebPort() int { return get(c, func(d *data) int { return d.WebPort }) }

// Google returns the configured custom OAuth client.
func (c *Config) Google() GoogleCreds { return get(c, func(d *data) GoogleCreds { return d.Google }) }

// Configured reports whether a login has been completed.
func (c *Config) Configured() bool {
	return get(c, func(d *data) bool { return d.AccountEmail != "" })
}

// OfflinePaths returns a copy of the Drive-relative paths kept offline.
func (c *Config) OfflinePaths() []string {
	return get(c, func(d *data) []string { return append([]string{}, d.OfflinePaths...) })
}

// IsOffline reports whether a Drive-relative path is kept offline, either by its
// own pin or through a pinned parent folder.
func (c *Config) IsOffline(p string) bool {
	return get(c, func(d *data) bool { return IsOfflinePath(d.OfflinePaths, p) })
}

// get reads one value under the lock. A generic helper keeps every accessor a
// single line, so adding a field cannot accidentally add an unlocked read.
func get[T any](c *Config, fn func(*data) T) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fn(&c.d)
}

// -------- writers --------
//
// Each one applies its change and persists the file in a single critical
// section, so a reader never sees a value that is not on disk yet.

// SetAccountEmail records the signed-in account ("" when signed out).
func (c *Config) SetAccountEmail(email string) error {
	return c.set(func(d *data) { d.AccountEmail = email })
}

// SetMode switches the sync mode. An unknown mode is rejected.
func (c *Config) SetMode(m SyncMode) error {
	if _, ok := ParseMode(string(m)); !ok {
		return fmt.Errorf("unknown sync mode %q", m)
	}
	return c.set(func(d *data) { d.Mode = m })
}

// SetLocalDir changes the mount point / mirror root. The path must be absolute:
// it becomes a mount point, and a relative one would resolve against whatever
// directory the daemon happens to run in.
func (c *Config) SetLocalDir(p string) error {
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		return errors.New(i18n.T("err.invalid_path"))
	}
	return c.set(func(d *data) { d.LocalDir = filepath.Clean(p) })
}

// SetConflictMode switches how mirror-mode conflicts are resolved. Anything but
// ConflictManual means automatic resolution.
func (c *Config) SetConflictMode(mode string) error {
	if mode != ConflictManual {
		mode = ConflictAuto
	}
	return c.set(func(d *data) { d.ConflictMode = mode })
}

// SetAutostartEnabled records whether the app should start on login.
func (c *Config) SetAutostartEnabled(on bool) error {
	return c.set(func(d *data) { d.AutostartDisabled = !on })
}

// SetUpdatePrerelease records whether prereleases are considered.
func (c *Config) SetUpdatePrerelease(on bool) error {
	return c.set(func(d *data) { d.UpdatePrerelease = on })
}

// SetGoogle stores custom OAuth client credentials.
func (c *Config) SetGoogle(creds GoogleCreds) error {
	return c.set(func(d *data) { d.Google = creds })
}

// SetOffline pins a Drive-relative path for offline use, or releases it.
//
// Pins are hierarchical: pinning a folder pins everything below it, so a path
// already covered by a pinned ancestor is not added again, and pins below the
// new one are dropped. Leaving them would make a later release of the folder
// ineffective - the leftover pin pulls its file straight back into the cache.
// Releasing drops the pin together with every pin below it, so releasing a
// folder really releases its contents.
func (c *Config) SetOffline(p string, on bool) error {
	p = cleanOfflinePath(p)
	if p == "" {
		return errors.New(i18n.T("err.invalid_path"))
	}
	return c.set(func(d *data) {
		if on {
			d.OfflinePaths = addOffline(d.OfflinePaths, p)
			return
		}
		d.OfflinePaths = removeOffline(d.OfflinePaths, p)
	})
}

// set applies fn and writes the file, both under the lock.
func (c *Config) set(fn func(*data)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.d)
	return c.saveLocked()
}

// Save writes the config to disk. Setters do this themselves; this is for the
// rare caller that needs to force a write.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

// saveLocked atomically writes the config. The caller must hold c.mu.
func (c *Config) saveLocked() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	raw, err := yaml.Marshal(&c.d)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// -------- offline-pin rules (pure, so they are testable on their own) --------

// addOffline returns paths with p pinned, dropping pins it now covers.
func addOffline(paths []string, p string) []string {
	if IsOfflinePath(paths, p) {
		return paths
	}
	out := paths[:0]
	for _, e := range paths {
		if !covers(p, e) {
			out = append(out, e)
		}
	}
	return append(out, p)
}

// removeOffline returns paths without p and without any pin below it.
func removeOffline(paths []string, p string) []string {
	out := paths[:0]
	for _, e := range paths {
		if cleanOfflinePath(e) != p && !covers(p, e) {
			out = append(out, e)
		}
	}
	return out
}

// IsOfflinePath reports whether p is pinned directly or through a parent
// folder in pins. Exported so fmstate.Info.IsPinned (the C++ plugin's Go
// counterpart) can share this exact rule instead of carrying its own copy.
func IsOfflinePath(pins []string, p string) bool {
	p = cleanOfflinePath(p)
	if p == "" {
		return false
	}
	for _, e := range pins {
		if e = cleanOfflinePath(e); e == p || covers(e, p) {
			return true
		}
	}
	return false
}

// normalizeOffline cleans every pin and drops empty and duplicate entries.
func normalizeOffline(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, e := range paths {
		e = cleanOfflinePath(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// covers reports whether the pinned path parent contains child.
func covers(parent, child string) bool {
	parent, child = cleanOfflinePath(parent), cleanOfflinePath(child)
	if parent == "" || child == "" {
		return false
	}
	return strings.HasPrefix(child, parent+"/")
}

// cleanOfflinePath normalises a Drive-relative path so pins compare reliably:
// "Docs", "Docs/" and "/Docs" all name the same folder.
func cleanOfflinePath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "." {
		return ""
	}
	return p
}

// -------- derived paths --------

// RcloneConfPath is where the rclone remote definition lives.
func RcloneConfPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rclone.conf"), nil
}

// StateDir returns the runtime-state directory (status file, logs), creating it.
func StateDir() (string, error) { return xdg.StateDir() }

// StatusPath returns the path of the JSON status file used for monitoring.
func StatusPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "status.json"), nil
}

// LogDir returns the directory holding rotated log files, creating it.
func LogDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return "", err
	}
	return logs, nil
}

// RcloneCacheDir returns the directory handed to rclone as --cache-dir, creating
// it. rclone lays out "vfs/<remote>/…" (data) and "vfsMeta/<remote>/…"
// (metadata) below it; see package fmstate, which reads both back.
func RcloneCacheDir() (string, error) {
	dir, err := xdg.CacheDir()
	if err != nil {
		return "", err
	}
	// Historical layout: the directory is itself called "vfs", so the data files
	// end up under ".../vfs/vfs/<remote>/". Renaming it would strand the caches
	// of existing installations, so it stays until a release that migrates them.
	cache := filepath.Join(dir, "vfs")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	return cache, nil
}
