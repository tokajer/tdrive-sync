// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package pins holds the rules for "keep offline" pins: Drive-relative paths,
// where pinning a folder pins everything below it.
//
// The daemon's configuration stores the pins and the file-manager integration
// matches against them; both go through this package so they can never
// disagree about what is pinned. The C++ plugin carries a copy of Has (see
// internal/dolphin/plugin/tdrivestate.cpp), checked by the shared fixture in
// internal/fmstate/testdata.
package pins

import "strings"

// Clean normalises a Drive-relative path so pins compare reliably: "Docs",
// "Docs/" and "/Docs" all name the same folder. The Drive root cleans to "".
func Clean(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "." {
		return ""
	}
	return p
}

// Has reports whether p is pinned directly or through a parent folder.
func Has(pins []string, p string) bool {
	p = Clean(p)
	if p == "" {
		return false
	}
	for _, e := range pins {
		if e = Clean(e); e == p || covers(e, p) {
			return true
		}
	}
	return false
}

// Add returns pins with p pinned, dropping pins it now covers: leaving them
// would make a later release of the folder ineffective, as the leftover pin
// pulls its file straight back into the cache. It may reuse pins' storage.
func Add(pins []string, p string) []string {
	if Has(pins, p) {
		return pins
	}
	out := pins[:0]
	for _, e := range pins {
		if !covers(p, e) {
			out = append(out, e)
		}
	}
	return append(out, p)
}

// Remove returns pins without p and without any pin below it, so releasing a
// folder really releases its contents. It may reuse pins' storage.
func Remove(pins []string, p string) []string {
	out := pins[:0]
	for _, e := range pins {
		if Clean(e) != p && !covers(p, e) {
			out = append(out, e)
		}
	}
	return out
}

// Normalize cleans every pin and drops empty and duplicate entries.
func Normalize(pins []string) []string {
	out := make([]string, 0, len(pins))
	seen := map[string]bool{}
	for _, e := range pins {
		e = Clean(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// covers reports whether the pinned path parent contains child.
func covers(parent, child string) bool {
	parent, child = Clean(parent), Clean(child)
	if parent == "" || child == "" {
		return false
	}
	return strings.HasPrefix(child, parent+"/")
}
