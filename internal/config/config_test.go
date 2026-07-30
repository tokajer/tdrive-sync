package config

import (
	"strings"
	"testing"
)

// TestOfflinePins pins down the hierarchy rules. The case that matters in
// practice is the last one: a leftover pin below a released folder used to make
// the release pointless, because the daemon downloaded that file again right
// afterwards and the folder went back to "partially offline".
func TestOfflinePins(t *testing.T) {
	cases := []struct {
		name string
		ops  []string // "+path" pins, "-path" releases
		want []string
	}{
		{"pin one", []string{"+USA"}, []string{"USA"}},
		{"pinning twice keeps one entry", []string{"+USA", "+USA"}, []string{"USA"}},
		{"a pinned ancestor already covers the file", []string{"+USA", "+USA/pass.pdf"}, []string{"USA"}},
		{"pinning the folder drops the pins below it", []string{"+USA/pass.pdf", "+USA"}, []string{"USA"}},
		{"siblings stay", []string{"+USA/a", "+USA2/b"}, []string{"USA/a", "USA2/b"}},
		{"a shared prefix is not a parent", []string{"+USA", "-USAX"}, []string{"USA"}},
		{"releasing a folder drops the pins below it", []string{"+USA/pass.pdf", "+Docs", "-USA"}, []string{"Docs"}},
		{"trailing slashes name the same path", []string{"+USA/", "-/USA"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{}
			for _, op := range tc.ops {
				if path := op[1:]; op[0] == '+' {
					c.AddOffline(path)
				} else {
					c.RemoveOffline(path)
				}
			}
			if got := strings.Join(c.OfflinePaths, "|"); got != strings.Join(tc.want, "|") {
				t.Errorf("after %v: offline_paths = %v, want %v", tc.ops, c.OfflinePaths, tc.want)
			}
		})
	}
}

func TestIsOffline(t *testing.T) {
	c := &Config{OfflinePaths: []string{"USA", "Docs/tax/2024.pdf"}}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"USA", true},
		{"USA/pass.pdf", true}, // covered by the pinned folder
		{"USAX", false},        // a shared prefix is not a parent
		{"Docs", false},        // the parent of a pinned file is not itself pinned
		{"Docs/tax/2024.pdf", true},
		{"", false},
	} {
		if got := c.IsOffline(tc.path); got != tc.want {
			t.Errorf("IsOffline(%q) = %t, want %t", tc.path, got, tc.want)
		}
	}
}

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
