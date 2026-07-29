package config

import "testing"

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
