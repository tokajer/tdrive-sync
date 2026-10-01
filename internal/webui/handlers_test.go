// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/logbuf"
	"tdrive-sync/internal/manager"
	"tdrive-sync/internal/rclone"
	"tdrive-sync/internal/updater"
)

// fakeBackend records what the handlers asked the sync side to do.
type fakeBackend struct {
	mu         sync.Mutex
	calls      []string
	mode       config.SyncMode
	offline    map[string]bool
	entries    []rclone.Entry
	resolveErr error
}

func (b *fakeBackend) record(call string) {
	b.mu.Lock()
	b.calls = append(b.calls, call)
	b.mu.Unlock()
}

func (b *fakeBackend) called(call string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (b *fakeBackend) Status() manager.Status { return manager.Status{State: manager.StateIdle} }
func (b *fakeBackend) SetMode(m config.SyncMode) error {
	b.record("SetMode")
	b.mode = m
	return nil
}
func (b *fakeBackend) SetConflictMode(config.ConflictMode) error {
	b.record("SetConflictMode")
	return nil
}
func (b *fakeBackend) Conflicts() []manager.Conflict { return nil }
func (b *fakeBackend) ResolveConflict(string, string) error {
	b.record("ResolveConflict")
	return b.resolveErr
}
func (b *fakeBackend) SetLocalDir(string) error { b.record("SetLocalDir"); return nil }
func (b *fakeBackend) SyncNow()                 { b.record("SyncNow") }
func (b *fakeBackend) Pause()                   { b.record("Pause") }
func (b *fakeBackend) Resume()                  { b.record("Resume") }
func (b *fakeBackend) Login(context.Context, string, func(string)) error {
	b.record("Login")
	return nil
}
func (b *fakeBackend) Logout(context.Context) error            { b.record("Logout"); return nil }
func (b *fakeBackend) GoogleCreds() config.GoogleCreds         { return config.GoogleCreds{} }
func (b *fakeBackend) SetGoogleCreds(config.GoogleCreds) error { return nil }
func (b *fakeBackend) Browse(context.Context, string) ([]rclone.Entry, error) {
	return b.entries, nil
}
func (b *fakeBackend) SetOffline(path string, on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline == nil {
		b.offline = map[string]bool{}
	}
	b.offline[path] = on
	return nil
}
func (b *fakeBackend) ResetErrors() { b.record("ResetErrors") }

// fakeUpdater never talks to GitHub.
type fakeUpdater struct{ checks int }

func (u *fakeUpdater) Status() updater.Status { return updater.Status{State: updater.StateIdle} }
func (u *fakeUpdater) Check(context.Context) (*updater.Release, error) {
	u.checks++
	return nil, nil
}
func (u *fakeUpdater) Apply(context.Context) error { return nil }

// newHandlerServer builds a Server on fakes, with the configuration kept in a
// throwaway directory so a setter can never touch the real one.
func newHandlerServer(t *testing.T) (*Server, *fakeBackend) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	be := &fakeBackend{}
	return New(be, config.Default(), logbuf.New(10), nil, nil, nil), be
}

// call sends one request through the full router, guard included.
func call(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = s.addr
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	return out
}

// TestStatusContract pins the field names the page reads.
func TestStatusContract(t *testing.T) {
	s, _ := newHandlerServer(t)
	rec := call(t, s, http.MethodGet, "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got := decodeBody(t, rec)
	for _, key := range []string{"status", "configured", "web_url", "autostart"} {
		if _, ok := got[key]; !ok {
			t.Errorf("/api/status lacks %q", key)
		}
	}
}

func TestModeValidation(t *testing.T) {
	s, be := newHandlerServer(t)
	if rec := call(t, s, http.MethodPost, "/api/mode", `{"mode":"bogus"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown mode: status %d, want 400", rec.Code)
	}
	if rec := call(t, s, http.MethodPost, "/api/mode", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("broken body: status %d, want 400", rec.Code)
	}
	if be.called("SetMode") {
		t.Fatal("an invalid request reached the backend")
	}
	if rec := call(t, s, http.MethodPost, "/api/mode", `{"mode":"mirror"}`); rec.Code != http.StatusOK {
		t.Fatalf("valid mode: status %d", rec.Code)
	}
	if be.mode != config.ModeMirror {
		t.Errorf("backend mode = %q, want mirror", be.mode)
	}
}

func TestPauseAndResume(t *testing.T) {
	s, be := newHandlerServer(t)
	call(t, s, http.MethodPost, "/api/pause", `{"paused":true}`)
	if !be.called("Pause") {
		t.Error("paused=true did not pause")
	}
	call(t, s, http.MethodPost, "/api/pause", ``)
	if !be.called("Resume") {
		t.Error("an empty body did not resume")
	}
}

func TestOfflineRequiresPath(t *testing.T) {
	s, be := newHandlerServer(t)
	if rec := call(t, s, http.MethodPost, "/api/offline", `{"on":true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing path: status %d, want 400", rec.Code)
	}
	if rec := call(t, s, http.MethodPost, "/api/offline", `{"path":"Docs","on":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !be.offline["Docs"] {
		t.Error("pin did not reach the backend")
	}
}

func TestConflictResolveError(t *testing.T) {
	s, be := newHandlerServer(t)
	be.resolveErr = errors.New("gone")
	rec := call(t, s, http.MethodPost, "/api/conflict-resolve", `{"path":"a.conflict1","action":"keep"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}

// TestBrowseMarksInheritedPins: an entry below a pinned folder is offline, but
// cannot be released on its own.
func TestBrowseMarksInheritedPins(t *testing.T) {
	s, be := newHandlerServer(t)
	if err := s.cfg.SetOffline("Docs", true); err != nil {
		t.Fatal(err)
	}
	be.entries = []rclone.Entry{{Name: "a.txt", Path: "a.txt"}}
	rec := call(t, s, http.MethodGet, "/api/browse?path=Docs", "")
	var got struct {
		Entries []struct {
			Path      string `json:"path"`
			Offline   bool   `json:"offline"`
			Inherited bool   `json:"inherited"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %+v", got.Entries)
	}
	e := got.Entries[0]
	if e.Path != "Docs/a.txt" || !e.Offline || !e.Inherited {
		t.Errorf("entry = %+v, want Docs/a.txt offline and inherited", e)
	}
}

// TestPrereleaseIsStoredOnce: the setting lives in the configuration only, and
// a change triggers a fresh check.
func TestPrereleaseIsStoredOnce(t *testing.T) {
	s, _ := newHandlerServer(t)
	upd := &fakeUpdater{}
	s.upd = upd
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.ctx = ctx

	if rec := call(t, s, http.MethodPost, "/api/update/prerelease", `{"on":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !s.cfg.UpdatePrerelease() {
		t.Error("the setting was not saved")
	}
}

func TestUpdateWithoutUpdater(t *testing.T) {
	s, _ := newHandlerServer(t)
	for _, path := range []string{"/api/update/check", "/api/update/apply", "/api/update/restart"} {
		if rec := call(t, s, http.MethodPost, path, ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, rec.Code)
		}
	}
	got := decodeBody(t, call(t, s, http.MethodGet, "/api/update", ""))
	if got["state"] != "unsupported" {
		t.Errorf("/api/update state = %v, want unsupported", got["state"])
	}
}

func TestConflictModeValidation(t *testing.T) {
	s, be := newHandlerServer(t)
	if rec := call(t, s, http.MethodPost, "/api/conflict-mode", `{"mode":"manaul"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("typo: status %d, want 400", rec.Code)
	}
	if be.called("SetConflictMode") {
		t.Fatal("a typo reached the backend")
	}
	if rec := call(t, s, http.MethodPost, "/api/conflict-mode", `{"mode":"auto"}`); rec.Code != http.StatusOK {
		t.Errorf("valid mode: status %d", rec.Code)
	}
}
