// Package webui serves the local settings interface on 127.0.0.1. It exposes a
// small JSON API driven by the manager plus an embedded single-page frontend.
package webui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/i18n"
	"tdrive-sync/internal/logbuf"
	"tdrive-sync/internal/manager"
	"tdrive-sync/internal/updater"
	"tdrive-sync/internal/window"
)

//go:embed index.html
var indexHTML []byte

// i18nMarker is the placeholder in index.html that the message catalog for the
// active language is injected into, so the page renders translated on first
// paint instead of fetching its strings afterwards.
const i18nMarker = "<!--I18N-->"

// Server is the settings web server.
type Server struct {
	mgr     *manager.Manager
	cfg     *config.Config
	logs    *logbuf.Buffer
	upd     *updater.Updater
	restart func()
	addr    string
	log     func(string, ...any)
	index   []byte // index.html with the message catalog injected

	mu          sync.Mutex
	loginActive bool
	loginLines  []string
	loginErr    string
}

// New creates a settings server bound to 127.0.0.1 on the config's WebPort. upd
// and restart may be nil (self-update simply stays unavailable).
func New(mgr *manager.Manager, cfg *config.Config, logs *logbuf.Buffer, upd *updater.Updater, restart func()) *Server {
	return &Server{
		mgr:     mgr,
		cfg:     cfg,
		logs:    logs,
		upd:     upd,
		restart: restart,
		addr:    fmt.Sprintf("127.0.0.1:%d", cfg.WebPort),
		log:     logs.Logf,
		index:   renderIndex(indexHTML),
	}
}

// renderIndex injects the active language and its message catalog into the
// embedded page.
func renderIndex(page []byte) []byte {
	catalog, err := json.Marshal(i18n.Catalog())
	if err != nil {
		catalog = []byte("{}")
	}
	script := fmt.Sprintf("<script>window.APP_LANG=%q;window.I18N=%s;</script>",
		string(i18n.Current()), catalog)
	return bytes.Replace(page, []byte(i18nMarker), []byte(script), 1)
}

// URL returns the address the UI is reachable at.
func (s *Server) URL() string { return "http://" + s.addr }

// guard wraps a handler with the checks that keep the loopback-only API safe
// from the browser: the Host header must be our own loopback address (blocks
// DNS-rebinding pages whose scripts would otherwise be same-origin), a present
// Origin header must be the UI itself (blocks cross-site fetch/form POSTs, which
// always carry one), and mutating endpoints accept only POST (blocks CSRF via
// <img>/<script>/link GETs, which never carry an Origin header).
func (s *Server) guard(mutating bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "invalid Host header", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
			http.Error(w, "invalid Origin header", http.StatusForbidden)
			return
		}
		if mutating && r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func (s *Server) hostAllowed(host string) bool {
	return host == s.addr || host == fmt.Sprintf("localhost:%d", s.cfg.WebPort)
}

func (s *Server) originAllowed(origin string) bool {
	return origin == "http://"+s.addr || origin == fmt.Sprintf("http://localhost:%d", s.cfg.WebPort)
}

// ListenAndServe starts the HTTP server, blocking until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	// get: read-only endpoints; post: state-changing, POST-only.
	get := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.guard(false, h)) }
	post := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.guard(true, h)) }

	get("/", s.handleIndex)
	get("/api/status", s.handleStatus)
	post("/api/mode", s.handleMode)
	post("/api/conflict-mode", s.handleConflictMode)
	get("/api/conflicts", s.handleConflicts)
	post("/api/conflict-resolve", s.handleConflictResolve)
	post("/api/autostart", s.handleAutostart)
	post("/api/local-dir", s.handleLocalDir)
	post("/api/sync-now", s.handleSyncNow)
	post("/api/pause", s.handlePause)
	post("/api/login", s.handleLogin)
	get("/api/login-status", s.handleLoginStatus)
	post("/api/logout", s.handleLogout)
	get("/api/google-creds", s.handleGoogleCreds)
	post("/api/google-creds/save", s.handleGoogleCredsSave)
	post("/api/google-creds/import", s.handleGoogleCredsImport)
	get("/api/browse", s.handleBrowse)
	post("/api/offline", s.handleOffline)
	post("/api/open", s.handleOpen)
	post("/api/open-logs", s.handleOpenLogs)
	get("/api/logs", s.handleLogs)
	post("/api/logs/clear", s.handleLogsClear)
	get("/api/update", s.handleUpdate)
	post("/api/update/check", s.handleUpdateCheck)
	post("/api/update/apply", s.handleUpdateApply)
	post("/api/update/prerelease", s.handleUpdatePrerelease)
	post("/api/update/restart", s.handleUpdateRestart)

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.index)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := s.mgr.Status()
	writeJSON(w, map[string]any{
		"status":     st,
		"configured": s.cfg.Configured(),
		"web_url":    s.URL(),
		"autostart":  s.cfg.AutostartEnabled(),
	})
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mode := config.ModeStream
	if body.Mode == string(config.ModeMirror) {
		mode = config.ModeMirror
	}
	if err := s.mgr.SetMode(mode); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleConflictMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.SetConflictMode(body.Mode); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"conflicts": s.mgr.Conflicts()})
}

func (s *Server) handleConflictResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path   string `json:"path"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, i18n.T("err.invalid_request"), http.StatusBadRequest)
		return
	}
	if err := s.mgr.ResolveConflict(body.Path, body.Action); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleAutostart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.cfg.AutostartDisabled = !body.On
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := window.InstallAutostart(body.On); err != nil {
		s.log("could not update the autostart entry: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleLocalDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, i18n.T("err.invalid_path"), http.StatusBadRequest)
		return
	}
	s.cfg.LocalDir = body.Path
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Restart in the current mode to apply the new location.
	_ = s.mgr.SetMode(s.cfg.Mode)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	s.mgr.SyncNow()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused bool `json:"paused"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Paused {
		s.mgr.Pause()
	} else {
		s.mgr.Resume()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.loginActive {
		s.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "already": true})
		return
	}
	s.loginActive = true
	s.loginLines = nil
	s.loginErr = ""
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err := s.mgr.Login(ctx, func(line string) {
			s.mu.Lock()
			s.loginLines = append(s.loginLines, line)
			if len(s.loginLines) > 100 {
				s.loginLines = s.loginLines[len(s.loginLines)-100:]
			}
			s.mu.Unlock()
			s.log("[login] %s", line)
		})
		s.mu.Lock()
		s.loginActive = false
		if err != nil {
			s.loginErr = err.Error()
		}
		s.mu.Unlock()
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	resp := map[string]any{
		"active":     s.loginActive,
		"lines":      append([]string{}, s.loginLines...),
		"error":      s.loginErr,
		"configured": s.cfg.Configured(),
		"account":    s.cfg.AccountEmail,
	}
	s.mu.Unlock()
	writeJSON(w, resp)
}

// handleGoogleCreds reports the currently configured OAuth client. The secret is
// never returned; only the (non-sensitive) client ID and a configured flag.
func (s *Server) handleGoogleCreds(w http.ResponseWriter, r *http.Request) {
	creds := s.mgr.GoogleCreds()
	writeJSON(w, map[string]any{
		"client_id":  creds.ClientID,
		"configured": creds.Configured(),
	})
}

// handleGoogleCredsSave stores manually entered client_id/client_secret. Sending
// both empty reverts to rclone's built-in credentials.
func (s *Server) handleGoogleCredsSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(body.ClientID)
	secret := strings.TrimSpace(body.ClientSecret)
	if (id == "") != (secret == "") {
		http.Error(w, i18n.T("err.creds_incomplete"), http.StatusBadRequest)
		return
	}
	if err := s.mgr.SetGoogleCreds(config.GoogleCreds{ClientID: id, ClientSecret: secret}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "client_id": id, "configured": id != ""})
}

// handleGoogleCredsImport parses a credentials JSON downloaded from the Google
// Cloud console (or pasted in) and stores the extracted client_id/client_secret.
func (s *Server) handleGoogleCredsImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	creds, err := config.ParseGoogleCredsJSON([]byte(body.Data))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.SetGoogleCreds(creds); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "client_id": creds.ClientID, "configured": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.mgr.Logout(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	entries, err := s.mgr.Rclone().List(ctx, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	pinned := map[string]bool{}
	for _, p := range s.cfg.OfflinePaths {
		pinned[p] = true
	}
	type item struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		IsDir   bool   `json:"is_dir"`
		Size    int64  `json:"size"`
		Offline bool   `json:"offline"`
	}
	items := make([]item, 0, len(entries))
	for _, e := range entries {
		full := e.Path
		if rel != "" {
			full = rel + "/" + e.Path
		}
		items = append(items, item{
			Name:    e.Name,
			Path:    full,
			IsDir:   e.IsDir,
			Size:    e.Size,
			Offline: pinned[full],
		})
	}
	writeJSON(w, map[string]any{"path": rel, "entries": items})
}

func (s *Server) handleOffline(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, i18n.T("err.invalid_path"), http.StatusBadRequest)
		return
	}
	if err := s.mgr.SetOffline(body.Path, body.On); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := window.OpenPath(s.cfg.LocalDir); err != nil {
			s.log("could not open the sync folder: %v", err)
		}
	}()
	writeJSON(w, map[string]any{"ok": true})
}

// handleOpenLogs opens the log directory in the system file manager.
func (s *Server) handleOpenLogs(w http.ResponseWriter, r *http.Request) {
	dir, err := config.LogDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go func() {
		if err := window.OpenPath(dir); err != nil {
			s.log("could not open the log folder: %v", err)
		}
	}()
	writeJSON(w, map[string]any{"ok": true, "path": dir})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	errorsOnly := r.URL.Query().Get("level") == "error"
	writeJSON(w, map[string]any{"entries": s.logs.Entries(errorsOnly)})
}

func (s *Server) handleLogsClear(w http.ResponseWriter, r *http.Request) {
	s.logs.Clear()
	s.mgr.ResetErrors()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if s.upd == nil {
		writeJSON(w, map[string]any{"state": "unsupported", "can_self_update": false})
		return
	}
	writeJSON(w, s.upd.Status())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.upd == nil {
		http.Error(w, i18n.T("update.updater_missing"), http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, _ = s.upd.Check(ctx)
	writeJSON(w, s.upd.Status())
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.upd == nil {
		http.Error(w, i18n.T("update.updater_missing"), http.StatusServiceUnavailable)
		return
	}
	// Download + replace can take a while; run in the background and let the UI
	// poll /api/update for progress and the final state.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		_ = s.upd.Apply(ctx)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleUpdatePrerelease(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.cfg.UpdatePrerelease = body.On
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.upd != nil {
		s.upd.SetIncludePrerelease(body.On)
		// Re-check so the UI reflects the new selection immediately.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = s.upd.Check(ctx)
		}()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleUpdateRestart(w http.ResponseWriter, r *http.Request) {
	if s.restart == nil {
		http.Error(w, i18n.T("update.restart_missing"), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	go s.restart()
}
