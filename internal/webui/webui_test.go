package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tdrive-sync/internal/config"
)

func testServer() *Server {
	return &Server{
		cfg:  &config.Config{WebPort: 45677},
		addr: "127.0.0.1:45677",
	}
}

func TestGuard(t *testing.T) {
	s := testServer()
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

	cases := []struct {
		name     string
		mutating bool
		method   string
		host     string
		origin   string
		want     int
	}{
		{"read same-origin", false, http.MethodGet, "127.0.0.1:45677", "", http.StatusOK},
		{"read localhost host", false, http.MethodGet, "localhost:45677", "", http.StatusOK},
		{"read ui origin", false, http.MethodGet, "127.0.0.1:45677", "http://127.0.0.1:45677", http.StatusOK},
		{"dns rebinding", false, http.MethodGet, "evil.example:45677", "", http.StatusForbidden},
		{"cross-site origin", true, http.MethodPost, "127.0.0.1:45677", "https://evil.example", http.StatusForbidden},
		{"null origin", true, http.MethodPost, "127.0.0.1:45677", "null", http.StatusForbidden},
		{"csrf via GET", true, http.MethodGet, "127.0.0.1:45677", "", http.StatusMethodNotAllowed},
		{"mutating post ok", true, http.MethodPost, "127.0.0.1:45677", "http://127.0.0.1:45677", http.StatusOK},
		{"mutating post no origin", true, http.MethodPost, "127.0.0.1:45677", "", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, "http://"+c.host+"/api/x", nil)
			r.Host = c.host
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			w := httptest.NewRecorder()
			s.guard(c.mutating, ok)(w, r)
			if w.Code != c.want {
				t.Errorf("guard(%v) %s Host=%q Origin=%q: status %d, want %d",
					c.mutating, c.method, c.host, c.origin, w.Code, c.want)
			}
		})
	}
}
