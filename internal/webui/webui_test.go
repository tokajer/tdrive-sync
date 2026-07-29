package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/i18n"
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

// TestRenderIndex checks that the page ships the active language's messages, so
// the frontend never has to fall back to raw keys.
func TestRenderIndex(t *testing.T) {
	if !bytes.Contains(indexHTML, []byte(i18nMarker)) {
		t.Fatalf("index.html no longer contains the %s placeholder", i18nMarker)
	}

	i18n.Set(i18n.DE)
	defer i18n.Set(i18n.EN)
	page := string(renderIndex(indexHTML))

	if strings.Contains(page, i18nMarker) {
		t.Error("the placeholder survived rendering")
	}
	for _, want := range []string{`window.APP_LANG="de"`, i18n.T("ui.save"), `"ui.sync_now":`} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
}

// TestIndexKeysAreTranslated guards against a data-i18n attribute referring to a
// key that no catalog defines — the UI would show the bare key.
func TestIndexKeysAreTranslated(t *testing.T) {
	catalog := i18n.Catalog()
	page := string(indexHTML)
	for _, attr := range []string{`data-i18n="`, `data-i18n-html="`} {
		rest := page
		for {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			key := rest[:strings.IndexByte(rest, '"')]
			if _, ok := catalog[key]; !ok {
				t.Errorf("index.html uses %s%s\" but no catalog defines it", attr, key)
			}
		}
	}
}
