// Package config loads and persists the application configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
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

// Config is the persisted application state.
type Config struct {
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

	mu   sync.Mutex `yaml:"-"`
	path string     `yaml:"-"`
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		RemoteName:        "gdrive",
		Mode:              ModeStream,
		LocalDir:          filepath.Join(home, "GoogleDrive"),
		OfflinePaths:      []string{},
		MirrorIntervalSec: 300,
		ConflictMode:      ConflictManual,
		WebPort:           45677,
	}
}

// Dir returns the configuration directory, creating it if necessary.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "tdrive-sync")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

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

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.path == "" {
		cfg.path = path
	}
	if cfg.RemoteName == "" {
		cfg.RemoteName = "gdrive"
	}
	if cfg.ConflictMode == "" {
		cfg.ConflictMode = ConflictManual
	}
	return cfg, nil
}

// AutostartEnabled reports whether the app should register itself to start on
// login.
func (c *Config) AutostartEnabled() bool { return !c.AutostartDisabled }

// Save atomically writes the config to disk.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Configured reports whether a login has been completed.
func (c *Config) Configured() bool {
	return c.AccountEmail != ""
}

// AddOffline registers a Drive-relative path as offline-available (dedup).
func (c *Config) AddOffline(p string) {
	for _, e := range c.OfflinePaths {
		if e == p {
			return
		}
	}
	c.OfflinePaths = append(c.OfflinePaths, p)
}

// RemoveOffline removes a previously pinned path.
func (c *Config) RemoveOffline(p string) {
	out := c.OfflinePaths[:0]
	for _, e := range c.OfflinePaths {
		if e != p {
			out = append(out, e)
		}
	}
	c.OfflinePaths = out
}

// RcloneConfPath is where the rclone remote definition lives.
func RcloneConfPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rclone.conf"), nil
}

// StateDir returns the runtime-state directory (status file, logs), creating it
// if necessary. Follows $XDG_STATE_HOME, defaulting to ~/.local/state.
func StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "tdrive-sync")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

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
