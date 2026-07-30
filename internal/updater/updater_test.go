// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "1.0.0", 0},
		{"1.2.10", "1.2.9", 1},
		{"1.0.0", "1.0.0-beta.1", 1},  // release > prerelease
		{"1.0.0-beta.1", "1.0.0", -1}, // prerelease < release
		{"1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"1.0.0-beta", "1.0.0-alpha", 1},
		{"2.0", "1.9.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPick(t *testing.T) {
	app := asset{Name: "TDrive_Sync-x86_64.AppImage", URL: "u", Size: 1}
	rels := []ghRelease{
		{TagName: "v0.1.0", Assets: []asset{app}},
		{TagName: "v0.3.0-beta.1", Prerelease: true, Assets: []asset{app}},
		{TagName: "v0.2.0", Assets: []asset{app}},
		{TagName: "v0.4.0", Draft: true, Assets: []asset{app}},    // drafts ignored
		{TagName: "v0.5.0", Assets: []asset{{Name: "notes.txt"}}}, // no AppImage asset
	}

	// Stable only: highest non-prerelease with an AppImage is 0.2.0.
	if got := pick(rels, false); got == nil || got.Version != "0.2.0" {
		t.Fatalf("pick(stable) = %v, want 0.2.0", got)
	}
	// Including prereleases: 0.3.0-beta.1 wins.
	if got := pick(rels, true); got == nil || got.Version != "0.3.0-beta.1" {
		t.Fatalf("pick(pre) = %v, want 0.3.0-beta.1", got)
	}
}

func TestCheckAndApply(t *testing.T) {
	payload := []byte("NEW-APPIMAGE-BINARY-CONTENT")

	mux := http.NewServeMux()
	mux.HandleFunc("/download/app", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	var srv *httptest.Server
	mux.HandleFunc("/repos/tokajer/tdrive-sync/releases", func(w http.ResponseWriter, r *http.Request) {
		rels := []ghRelease{{
			TagName: "v0.2.0",
			Name:    "Release 0.2.0",
			HTMLURL: "https://example/release",
			Assets: []asset{{
				Name: "TDrive_Sync-x86_64.AppImage",
				URL:  srv.URL + "/download/app",
				Size: int64(len(payload)),
			}},
		}}
		_ = json.NewEncoder(w).Encode(rels)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	// Target AppImage to be replaced.
	dir := t.TempDir()
	target := filepath.Join(dir, "app.AppImage")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("0.1.0", false, nil)
	u.apiBase = srv.URL
	u.appImage = target
	u.client = srv.Client()

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel == nil || rel.Version != "0.2.0" {
		t.Fatalf("Check returned %v, want 0.2.0", rel)
	}
	if st := u.Status(); st.State != StateAvailable || st.Available != "0.2.0" {
		t.Fatalf("status after check = %+v", st)
	}

	if err := u.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("target content = %q, want %q", got, payload)
	}
	fi, _ := os.Stat(target)
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("target not executable: %v", fi.Mode())
	}
	if st := u.Status(); st.State != StateReady {
		t.Fatalf("state after apply = %q, want ready", st.State)
	}
	// No stray temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "app.AppImage" {
			t.Fatalf("unexpected leftover file: %s", e.Name())
		}
	}
}

func TestCheckUpToDate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/tokajer/tdrive-sync/releases", func(w http.ResponseWriter, r *http.Request) {
		rels := []ghRelease{{TagName: "v0.1.0", Assets: []asset{{Name: "x-x86_64.AppImage", URL: "u", Size: 1}}}}
		_ = json.NewEncoder(w).Encode(rels)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	u := New("0.1.0", false, nil)
	u.apiBase = srv.URL
	u.appImage = "/tmp/whatever"
	u.client = srv.Client()

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel != nil {
		t.Fatalf("expected up-to-date, got %v", rel)
	}
	if st := u.Status(); st.State != StateUpToDate {
		t.Fatalf("state = %q, want uptodate", st.State)
	}
}
