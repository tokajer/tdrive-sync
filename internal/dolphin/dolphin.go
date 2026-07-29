// Package dolphin installs the KDE/Dolphin file-manager integration: an
// overlay-icon plugin that marks every file in the sync folder as streamed or
// available offline, plus a context-menu plugin for pinning files offline.
//
// The plugin is a KIO plugin, so it has to be compiled against the KDE
// Frameworks build it will be loaded into — a prebuilt binary inside the
// AppImage would only work by accident. The sources therefore ship embedded and
// are compiled on the user's machine, into their home directory only.
package dolphin

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed plugin
var sources embed.FS

// pluginRoot is the user-local Qt plugin directory we install into. Qt only
// searches it when QT_PLUGIN_PATH says so, which is what the environment files
// below are for.
const pluginRoot = ".local/lib64/qt6/plugins"

// envFileName is our drop-in for the systemd user environment (applies to
// everything the desktop session starts afterwards).
const envFileName = "50-tdrive-sync.conf"

// Paths bundles every location the integration touches.
type Paths struct {
	// Source is where the embedded plugin sources are unpacked.
	Source string
	// Build is the CMake build directory.
	Build string
	// PluginDir is the user-local Qt plugin root.
	PluginDir string
	// Overlay and Action are the installed plugin files.
	Overlay string
	Action  string
	// EnvFile is the systemd user environment drop-in.
	EnvFile string
	// PlasmaEnvFile is the Plasma startup script (for sessions started without
	// systemd, where the drop-in is not read).
	PlasmaEnvFile string
}

// resolvePaths computes every path from the user's home directory.
func resolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(home, ".cache")
	}
	confDir := os.Getenv("XDG_CONFIG_HOME")
	if confDir == "" {
		confDir = filepath.Join(home, ".config")
	}
	work := filepath.Join(cache, "tdrive-sync", "dolphin-plugin")
	pluginDir := filepath.Join(home, pluginRoot)
	return Paths{
		Source:        filepath.Join(work, "src"),
		Build:         filepath.Join(work, "build"),
		PluginDir:     pluginDir,
		Overlay:       filepath.Join(pluginDir, "kf6", "overlayicon", "libtdrivesyncoverlay.so"),
		Action:        filepath.Join(pluginDir, "kf6", "kfileitemaction", "libtdrivesyncaction.so"),
		EnvFile:       filepath.Join(confDir, "environment.d", envFileName),
		PlasmaEnvFile: filepath.Join(confDir, "plasma-workspace", "env", "tdrive-sync.sh"),
	}, nil
}

// Install compiles the plugins and puts them where Dolphin can find them.
// Progress and hints go to logf.
func Install(logf func(string, ...any)) error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("dolphin"); err != nil {
		logf("note: dolphin was not found in PATH – installing anyway.")
	}
	if err := missingTools(); err != nil {
		return err
	}

	logf("unpacking the plugin sources to %s", p.Source)
	if err := writeSources(p.Source); err != nil {
		return err
	}

	logf("configuring (cmake)…")
	if out, err := run(p.Build, "cmake", "-S", p.Source, "-B", p.Build, "-DCMAKE_BUILD_TYPE=Release"); err != nil {
		logf("%s", out)
		return fmt.Errorf("cmake could not configure the build: %w\n\n%s", err, buildDepsHint())
	}
	logf("compiling…")
	if out, err := run(p.Build, "cmake", "--build", p.Build, "--parallel"); err != nil {
		logf("%s", out)
		return fmt.Errorf("the plugin did not compile: %w", err)
	}

	for _, c := range []struct{ from, to string }{
		{filepath.Join(p.Build, "libtdrivesyncoverlay.so"), p.Overlay},
		{filepath.Join(p.Build, "libtdrivesyncaction.so"), p.Action},
	} {
		if err := installFile(c.from, c.to); err != nil {
			return err
		}
		logf("installed %s", c.to)
	}

	if err := writeEnvFiles(p); err != nil {
		return err
	}
	logf("registered %s in %s", p.PluginDir, p.EnvFile)

	// Make it work for programs started from now on, without a re-login.
	if err := setSessionEnv(p.PluginDir); err != nil {
		logf("note: could not update the running session's environment: %v", err)
	}

	logf("")
	logf("Done. Restart Dolphin to pick the plugin up (close every window, or run")
	logf("`kquitapp6 dolphin`). To try it right away without touching the session:")
	logf("  QT_PLUGIN_PATH=%q dolphin", p.PluginDir)
	logf("If the indicators stay missing, log out and back in once – the environment")
	logf("variable only reaches programs the session starts after it is set.")
	return nil
}

// Remove deletes the plugins and the environment entries again.
func Remove(logf func(string, ...any)) error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	for _, f := range []string{p.Overlay, p.Action, p.EnvFile, p.PlasmaEnvFile} {
		err := os.Remove(f)
		switch {
		case err == nil:
			logf("removed %s", f)
		case os.IsNotExist(err):
		default:
			return err
		}
	}
	_ = os.RemoveAll(p.Build)
	_ = os.RemoveAll(p.Source)
	logf("")
	logf("Done. Restart Dolphin; the environment variable disappears on the next login.")
	return nil
}

// Report describes the current installation, for troubleshooting.
type Report struct {
	Paths          Paths
	OverlayPresent bool
	ActionPresent  bool
	EnvFilePresent bool
	// OnPluginPath reports whether QT_PLUGIN_PATH in *this* process already
	// contains the plugin directory. Since the daemon and Dolphin are started
	// by the same session, this is a good proxy for what Dolphin sees.
	OnPluginPath bool
}

// Status collects the state of the integration.
func Status() (Report, error) {
	p, err := resolvePaths()
	if err != nil {
		return Report{}, err
	}
	r := Report{Paths: p}
	r.OverlayPresent = exists(p.Overlay)
	r.ActionPresent = exists(p.Action)
	r.EnvFilePresent = exists(p.EnvFile)
	for _, dir := range filepath.SplitList(os.Getenv("QT_PLUGIN_PATH")) {
		if dir != "" && filepath.Clean(dir) == filepath.Clean(p.PluginDir) {
			r.OnPluginPath = true
		}
	}
	return r, nil
}

// writeSources unpacks the embedded plugin sources, replacing what is there.
func writeSources(dir string) error {
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(sources, "plugin", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, "plugin"), "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := sources.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// missingTools reports the build tools that are not installed.
func missingTools() error {
	var missing []string
	if _, err := exec.LookPath("cmake"); err != nil {
		missing = append(missing, "cmake")
	}
	if !anyInPath("g++", "c++", "clang++") {
		missing = append(missing, "a C++ compiler")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s missing.\n\n%s", strings.Join(missing, " and "), buildDepsHint())
}

func anyInPath(names ...string) bool {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

// buildDepsHint returns the install command for the packages the build needs,
// matching the running distribution where we recognise it.
func buildDepsHint() string {
	const generic = "Needed: cmake, a C++ compiler, and the development packages of Qt 6 and KDE Frameworks 6 (KIO)."
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return generic
	}
	ids := osReleaseIDs(string(data))
	for _, id := range ids {
		switch id {
		case "fedora", "nobara", "rhel", "centos":
			return "Install the build requirements once:\n  sudo dnf install cmake gcc-c++ extra-cmake-modules kf6-kio-devel qt6-qtbase-devel"
		case "debian", "ubuntu", "linuxmint", "pop":
			return "Install the build requirements once:\n  sudo apt install cmake g++ extra-cmake-modules libkf6kio-dev qt6-base-dev"
		case "arch", "manjaro", "endeavouros":
			return "Install the build requirements once:\n  sudo pacman -S --needed cmake gcc extra-cmake-modules kio qt6-base"
		case "opensuse", "opensuse-tumbleweed", "opensuse-leap", "suse":
			return "Install the build requirements once:\n  sudo zypper install cmake gcc-c++ extra-cmake-modules kf6-kio-devel qt6-base-devel"
		}
	}
	return generic
}

// osReleaseIDs returns ID plus ID_LIKE from an os-release file.
func osReleaseIDs(content string) []string {
	var ids []string
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		value = strings.Trim(value, `"'`)
		ids = append(ids, strings.Fields(strings.ToLower(value))...)
	}
	return ids
}

// run executes a command in dir, returning its combined output.
func run(dir, name string, args ...string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// installFile copies a built plugin into place, replacing an older copy.
func installFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return fmt.Errorf("the build produced no %s: %w", filepath.Base(from), err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp := to + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

// writeEnvFiles puts the plugin directory on QT_PLUGIN_PATH for future logins:
// once through the systemd user environment (Wayland sessions and everything
// else started as a user unit) and once through Plasma's startup scripts.
func writeEnvFiles(p Paths) error {
	envContent := fmt.Sprintf(`# Written by tdrive-sync (`+"`tdrive-sync dolphin install`"+`).
# Lets Qt programs – Dolphin above all – find the file-manager integration
# plugin in the user's home directory.
QT_PLUGIN_PATH=%s:${QT_PLUGIN_PATH}
`, p.PluginDir)
	if err := writeFile(p.EnvFile, envContent, 0o644); err != nil {
		return err
	}

	plasmaContent := `#!/bin/sh
# Written by tdrive-sync (` + "`tdrive-sync dolphin install`" + `).
QT_PLUGIN_PATH="$HOME/` + pluginRoot + `${QT_PLUGIN_PATH:+:$QT_PLUGIN_PATH}"
export QT_PLUGIN_PATH
`
	return writeFile(p.PlasmaEnvFile, plasmaContent, 0o755)
}

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

// setSessionEnv adds the plugin directory to the running session's environment,
// so a newly started Dolphin sees the plugin without a re-login. Only affects
// programs the session manager starts from now on.
func setSessionEnv(pluginDir string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return err
	}
	current := ""
	if out, err := exec.Command("systemctl", "--user", "show-environment").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(line, "QT_PLUGIN_PATH="); ok {
				current = v
			}
		}
	}
	for _, dir := range filepath.SplitList(current) {
		if dir != "" && filepath.Clean(dir) == filepath.Clean(pluginDir) {
			return nil // already there
		}
	}
	value := pluginDir
	if current != "" {
		value += string(filepath.ListSeparator) + current
	}
	return exec.Command("systemctl", "--user", "set-environment", "QT_PLUGIN_PATH="+value).Run()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
