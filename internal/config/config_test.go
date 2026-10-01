// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseGoogleCredsJSON(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantID     string
		wantSecret string
		wantErr    bool
	}{
		{
			name:       "desktop app installed wrapper",
			in:         `{"installed":{"client_id":"abc.apps.googleusercontent.com","project_id":"p","client_secret":"GOCSPX-xyz","redirect_uris":["http://localhost"]}}`,
			wantID:     "abc.apps.googleusercontent.com",
			wantSecret: "GOCSPX-xyz",
		},
		{
			name:       "web app wrapper",
			in:         `{"web":{"client_id":"web.apps.googleusercontent.com","client_secret":"secret-web"}}`,
			wantID:     "web.apps.googleusercontent.com",
			wantSecret: "secret-web",
		},
		{
			name:       "flat object",
			in:         `{"client_id":"flat-id","client_secret":"flat-secret"}`,
			wantID:     "flat-id",
			wantSecret: "flat-secret",
		},
		{
			name:    "missing secret",
			in:      `{"installed":{"client_id":"only-id"}}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			in:      `not json`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseGoogleCredsJSON([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ClientID != tc.wantID || got.ClientSecret != tc.wantSecret {
				t.Fatalf("got %+v, want id=%q secret=%q", got, tc.wantID, tc.wantSecret)
			}
			if !got.Configured() {
				t.Fatalf("expected Configured() true for %+v", got)
			}
		})
	}
}

// TestConcurrentAccess is the regression test for the data race that came from
// sharing one *Config between the HTTP handlers, the manager and both sync
// runners without any synchronisation. Run under -race, which check.sh does.
func TestConcurrentAccess(t *testing.T) {
	c := Default()
	c.path = filepath.Join(t.TempDir(), "config.yaml")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				if err := c.SetOffline("Docs", n%2 == 0); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = c.OfflinePaths()
				_ = c.LocalDir()
				_ = c.Mode()
				_ = c.Configured()
			}
		}()
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				if err := c.SetAccountEmail("a@example.com"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSetLocalDirRejectsRelative(t *testing.T) {
	c := Default()
	c.path = filepath.Join(t.TempDir(), "config.yaml")
	if err := c.SetLocalDir("GoogleDrive"); err == nil {
		t.Fatal("a relative sync folder must be rejected: it becomes a mount point")
	}
	if err := c.SetLocalDir("/srv/drive/"); err != nil {
		t.Fatalf("absolute path rejected: %v", err)
	}
	if got := c.LocalDir(); got != "/srv/drive" {
		t.Errorf("LocalDir = %q, want %q", got, "/srv/drive")
	}
}

func TestSetModeRejectsUnknown(t *testing.T) {
	c := Default()
	c.path = filepath.Join(t.TempDir(), "config.yaml")
	if err := c.SetMode("teleport"); err == nil {
		t.Fatal("an unknown sync mode must be rejected, not silently coerced")
	}
	if got := c.Mode(); got != ModeStream {
		t.Errorf("mode changed despite the error: %q", got)
	}
}

// TestSettersPersist checks that a setter really wrote the file: callers no
// longer call Save() themselves, so a setter that forgot to would lose the
// change on the next start without anything failing.
func TestSettersPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.path = path
	if err := c.SetConflictMode(ConflictAuto); err != nil {
		t.Fatal(err)
	}
	if err := c.SetOffline("Photos/2024", true); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back data
	if err := yaml.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ConflictMode != ConflictAuto {
		t.Errorf("conflict_mode on disk = %q, want %q", back.ConflictMode, ConflictAuto)
	}
	if strings.Join(back.OfflinePaths, "|") != "Photos/2024" {
		t.Errorf("offline_paths on disk = %v", back.OfflinePaths)
	}
}
