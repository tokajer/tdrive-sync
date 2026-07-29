package window

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript puts an executable shell script into dir.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenExternalRefusesNonWeb(t *testing.T) {
	// A URL can come straight from a clicked link, so anything that is not
	// http(s) must be refused instead of handed to a desktop handler.
	for _, raw := range []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"data:text/html,<b>x</b>",
		"/etc/passwd",
		"",
		"http://", // no host
	} {
		if err := OpenExternal(raw); err == nil {
			t.Errorf("OpenExternal(%q) = nil, want an error", raw)
		}
	}
}

func TestOpenPathRejectsEmpty(t *testing.T) {
	if err := OpenPath("   "); err == nil {
		t.Error("OpenPath(blank) = nil, want an error")
	}
}

func TestWithTarget(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"appends when there is no placeholder", []string{"open"}, []string{"open", "URL"}},
		{"fills the placeholder", []string{"open", "%s"}, []string{"open", "URL"}},
		{"fills only the first placeholder", []string{"%s", "%s"}, []string{"URL", "%s"}},
		{"substitutes inside a word", []string{"--url=%s"}, []string{"--url=URL"}},
		{"no arguments", nil, []string{"URL"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withTarget(c.args, "URL")
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Errorf("withTarget(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestBrowserEnvLaunchers(t *testing.T) {
	t.Setenv("BROWSER", "my-browser --new-tab %s:other-browser:")
	got := browserEnvLaunchers()
	if len(got) != 2 {
		t.Fatalf("got %d launchers from $BROWSER, want 2: %+v", len(got), got)
	}
	if got[0].name != "my-browser" || strings.Join(got[0].args, " ") != "--new-tab %s" {
		t.Errorf("first launcher = %+v, want my-browser with --new-tab %%s", got[0])
	}
	if got[1].name != "other-browser" {
		t.Errorf("second launcher = %+v, want other-browser", got[1])
	}
	// $BROWSER is tried before the generic handlers, so a user's choice wins.
	if all := browserLaunchers(); all[0].name != "my-browser" {
		t.Errorf("browserLaunchers()[0] = %q, want the $BROWSER entry first", all[0].name)
	}
}

func TestCleanPathDropsShimAndAppDir(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("APPDIR", "/tmp/.mount_app")
	shim := shimDir()

	got := cleanPath(strings.Join([]string{
		shim, "/tmp/.mount_app/usr/bin", "/usr/bin", "", "/usr/local/bin",
	}, ":"))
	if want := "/usr/bin:/usr/local/bin"; got != want {
		t.Errorf("cleanPath() = %q, want %q", got, want)
	}
	// Nothing usable left: fall back rather than hand over an empty PATH.
	if got := cleanPath(shim); !strings.Contains(got, "/usr/bin") {
		t.Errorf("cleanPath(shim only) = %q, want a fallback containing /usr/bin", got)
	}
}

func TestChildEnvSanitises(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LD_LIBRARY_PATH", "/opt/appimage/lib")
	t.Setenv("LD_PRELOAD", "/opt/appimage/preload.so")
	t.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	t.Setenv("PATH", shimDir()+":/usr/bin")
	t.Setenv("HOME", "/home/someone")

	env := childEnv()
	for _, unwanted := range []string{"LD_LIBRARY_PATH", "LD_PRELOAD", "WEBKIT_DISABLE_DMABUF_RENDERER"} {
		if v := envValue(env, unwanted); v != "" {
			t.Errorf("childEnv() still carries %s=%q", unwanted, v)
		}
	}
	if got := envValue(env, "PATH"); got != "/usr/bin" {
		t.Errorf("childEnv() PATH = %q, want the shim directory removed", got)
	}
	if got := envValue(env, "HOME"); got != "/home/someone" {
		t.Errorf("childEnv() dropped HOME (got %q)", got)
	}
}

func TestLookIn(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "runnable", "exit 0\n")
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := lookIn(dir, "runnable"); !ok || got != filepath.Join(dir, "runnable") {
		t.Errorf("lookIn(runnable) = %q, %v", got, ok)
	}
	if _, ok := lookIn(dir, "plain"); ok {
		t.Error("lookIn found a non-executable file")
	}
	if _, ok := lookIn(dir, "missing"); ok {
		t.Error("lookIn found a file that does not exist")
	}
	if _, ok := lookIn("", "runnable"); ok {
		t.Error("lookIn searched an empty PATH successfully")
	}
}

func TestInstallOpenShim(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir, err := InstallOpenShim()
	if err != nil {
		t.Fatalf("InstallOpenShim() error: %v", err)
	}
	path := filepath.Join(dir, "xdg-open")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("shim not written: %v", err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("shim mode %v is not executable", fi.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if !strings.Contains(string(body), exe) || !strings.Contains(string(body), "open-url") {
		t.Errorf("shim does not call %q open-url:\n%s", exe, body)
	}
	// The shim must never be what our own opener finds, or it would call itself.
	// A real system xdg-open may well be found instead — just not this file.
	if found, ok := lookIn(cleanPath(dir+":/usr/bin"), "xdg-open"); ok && found == path {
		t.Error("the shim is still reachable on the sanitised PATH")
	}
	// Writing it again must be a no-op rather than an error.
	if _, err := InstallOpenShim(); err != nil {
		t.Errorf("second InstallOpenShim() error: %v", err)
	}
}

func TestRunPicksFirstWorkingLauncher(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "opened.txt")
	writeScript(t, dir, "broken-opener", "exit 3\n")
	writeScript(t, dir, "good-opener", "printf '%s' \"$1\" > "+log+"\n")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", dir)

	err := run("http://127.0.0.1/x", []launcher{
		{name: "not-installed-at-all"},
		{name: "broken-opener", wait: true},
		{name: "good-opener", wait: true},
	})
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the working launcher was not run: %v", err)
	}
	if string(got) != "http://127.0.0.1/x" {
		t.Errorf("launcher received %q", got)
	}
}

func TestRunReportsWhenNothingWorks(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "broken-opener", "exit 3\n")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", dir)

	err := run("http://127.0.0.1/x", []launcher{{name: "broken-opener", wait: true}})
	if err == nil || !strings.Contains(err.Error(), "broken-opener") {
		t.Errorf("run() error = %v, want it to name the failed launcher", err)
	}
	if err := run("http://127.0.0.1/x", []launcher{{name: "nothing-here"}}); err == nil ||
		!strings.Contains(err.Error(), "found no desktop handler") {
		t.Errorf("run() with no installed launcher = %v", err)
	}
}

// TestRunAcceptsForegroundBrowser covers a browser that keeps running instead of
// handing over and exiting: it counts as success once it survives the grace
// period.
func TestRunAcceptsForegroundBrowser(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "sleepy-browser", "sleep 30\n")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// /usr/bin stays on the PATH so the script itself finds sleep.
	t.Setenv("PATH", dir+":/usr/bin")

	if err := run("http://127.0.0.1/x", []launcher{{name: "sleepy-browser"}}); err != nil {
		t.Errorf("run() error: %v", err)
	}
}
