// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package i18n holds the message catalogs for everything the user sees: the
// settings UI, the tray menu, desktop notifications and the status texts they
// display. The language is resolved once from the system locale — German when
// the locale asks for German, English for anything else — so an unsupported or
// unset locale always lands on English.
//
// Log output and CLI messages are deliberately not translated: they are
// diagnostic and stay English.
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Lang is a supported UI language.
type Lang string

const (
	// EN is English, also the fallback for unsupported locales.
	EN Lang = "en"
	// DE is German.
	DE Lang = "de"
)

var (
	mu       sync.RWMutex
	lang     = Detect()
	catalogs = map[Lang]map[string]string{EN: catalogEN, DE: catalogDE}
)

// localeEnv lists the environment variables that decide the locale, in the
// order POSIX gives them precedence.
var localeEnv = []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"}

// Detect resolves the UI language from the locale environment, returning EN
// when nothing names a supported language.
func Detect() Lang {
	for _, env := range localeEnv {
		// $LANGUAGE may hold a priority list ("de:en"); the others hold one tag.
		for _, tag := range strings.Split(os.Getenv(env), ":") {
			if l, ok := matchTag(tag); ok {
				return l
			}
		}
	}
	return EN
}

// matchTag maps a locale tag ("de_DE.UTF-8@euro", "en-GB", "de") to a supported
// language, reporting whether one was found. "C"/"POSIX" match nothing.
func matchTag(tag string) (Lang, bool) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "._@-"); i >= 0 {
		tag = tag[:i]
	}
	switch Lang(tag) {
	case DE:
		return DE, true
	case EN:
		return EN, true
	}
	return "", false
}

// Current returns the active language.
func Current() Lang {
	mu.RLock()
	defer mu.RUnlock()
	return lang
}

// Set overrides the detected language. Unsupported values select EN.
func Set(l Lang) {
	if _, ok := catalogs[l]; !ok {
		l = EN
	}
	mu.Lock()
	lang = l
	mu.Unlock()
}

// T returns the message for key in the active language, formatted with args
// when any are given.
func T(key string, args ...any) string { return In(Current(), key, args...) }

// In returns the message for key in a specific language. A key missing from
// that catalog falls back to English and finally to the key itself, so a gap
// shows up as a visible placeholder instead of an empty string.
func In(l Lang, key string, args ...any) string {
	msg, ok := catalogs[l][key]
	if !ok {
		if msg, ok = catalogEN[key]; !ok {
			msg = key
		}
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Catalog returns a copy of the active language's messages. The settings server
// embeds it into the page so the frontend renders translated right away.
func Catalog() map[string]string {
	src := catalogs[Current()]
	out := make(map[string]string, len(catalogEN))
	// Start from English so a key missing in the active catalog still resolves.
	for k, v := range catalogEN {
		out[k] = v
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}
