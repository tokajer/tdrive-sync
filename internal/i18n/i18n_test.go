// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

import "testing"

// clearLocale empties every locale variable so a test starts from a known,
// locale-free environment.
func clearLocale(t *testing.T) {
	t.Helper()
	for _, env := range localeEnv {
		t.Setenv(env, "")
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Lang
	}{
		{"no locale set", nil, EN},
		{"german", map[string]string{"LANG": "de_DE.UTF-8"}, DE},
		{"german with modifier", map[string]string{"LANG": "de_AT.UTF-8@euro"}, DE},
		{"german dash form", map[string]string{"LANG": "de-DE"}, DE},
		{"english", map[string]string{"LANG": "en_GB.UTF-8"}, EN},
		{"unsupported falls back", map[string]string{"LANG": "fr_FR.UTF-8"}, EN},
		{"posix locale", map[string]string{"LANG": "C.UTF-8"}, EN},
		{"lc_all wins over lang", map[string]string{"LC_ALL": "de_DE.UTF-8", "LANG": "fr_FR.UTF-8"}, DE},
		{"lc_messages wins over lang", map[string]string{"LC_MESSAGES": "de_DE.UTF-8", "LANG": "en_US.UTF-8"}, DE},
		{"language list first supported", map[string]string{"LANGUAGE": "fr:de:en"}, DE},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearLocale(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if got := Detect(); got != c.want {
				t.Errorf("Detect() with %v = %q, want %q", c.env, got, c.want)
			}
		})
	}
}

// TestCatalogsMatch keeps the translations in sync: a key present in one
// language but not the other would silently fall back to English at runtime.
func TestCatalogsMatch(t *testing.T) {
	for key := range catalogEN {
		if _, ok := catalogDE[key]; !ok {
			t.Errorf("key %q missing from the German catalog", key)
		}
	}
	for key := range catalogDE {
		if _, ok := catalogEN[key]; !ok {
			t.Errorf("key %q missing from the English catalog", key)
		}
	}
}

func TestT(t *testing.T) {
	Set(DE)
	defer Set(EN)

	if got, want := T("tray.sync_now"), "Jetzt synchronisieren"; got != want {
		t.Errorf("T(tray.sync_now) = %q, want %q", got, want)
	}
	if got, want := T("notify.signed_in_as", "a@b.c"), "Angemeldet als a@b.c"; got != want {
		t.Errorf("T(notify.signed_in_as) = %q, want %q", got, want)
	}
	if got, want := In(EN, "tray.sync_now"), "Sync now"; got != want {
		t.Errorf("In(EN, tray.sync_now) = %q, want %q", got, want)
	}
	// An unknown key must surface as itself rather than as an empty string.
	if got := T("nope.missing"); got != "nope.missing" {
		t.Errorf("T(nope.missing) = %q, want the key itself", got)
	}
}

func TestSetRejectsUnsupported(t *testing.T) {
	Set(Lang("fr"))
	defer Set(EN)
	if got := Current(); got != EN {
		t.Errorf("Current() after Set(fr) = %q, want %q", got, EN)
	}
}

func TestCatalogIsCompleteCopy(t *testing.T) {
	Set(DE)
	defer Set(EN)
	c := Catalog()
	if len(c) != len(catalogEN) {
		t.Errorf("Catalog() has %d entries, want %d", len(c), len(catalogEN))
	}
	if c["ui.save"] != catalogDE["ui.save"] {
		t.Errorf("Catalog()[ui.save] = %q, want the German message", c["ui.save"])
	}
	c["ui.save"] = "mutated"
	if T("ui.save") == "mutated" {
		t.Error("Catalog() handed out the live map")
	}
}
