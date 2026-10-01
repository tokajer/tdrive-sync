// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package dolphin

import (
	"os"
	"strings"
)

// A very small INI editor.
//
// KDE config files are plain INI and we touch exactly one key in them, so
// editing the lines in place is both enough and the least invasive: everything
// else in the file, including comments and unknown groups, stays untouched.

// readLines reads a file into lines; a missing file reads as empty.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func writeLines(path string, lines []string) error {
	return writeFile(path, strings.Join(lines, "\n")+"\n", 0o644)
}

// sectionOf returns the group name of a section header line, if it is one.
func sectionOf(line string) (string, bool) {
	l := strings.TrimSpace(line)
	if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
		return l[1 : len(l)-1], true
	}
	return "", false
}

// keyOf returns the key of an assignment line, if it is one.
func keyOf(line string) (string, bool) {
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(key), true
}

// iniValue looks a key up inside a group.
func iniValue(lines []string, section, key string) (string, bool) {
	start, end := sectionRange(lines, section)
	if start < 0 {
		return "", false
	}
	i := keyIndex(lines[start:end], key)
	if i < 0 {
		return "", false
	}
	_, value, _ := strings.Cut(lines[start+i], "=")
	return value, true
}

// sectionRange returns the half-open line range holding a group's body, or
// (-1, -1) when the file has no such group. Locating the group first is what
// keeps iniSet and iniUnset free of state flags.
func sectionRange(lines []string, section string) (start, end int) {
	start = -1
	for i, line := range lines {
		s, ok := sectionOf(line)
		if !ok {
			continue
		}
		if start >= 0 {
			return start, i // the next header ends our group
		}
		if s == section {
			start = i + 1
		}
	}
	if start < 0 {
		return -1, -1
	}
	return start, len(lines) // our group is the last one in the file
}

// keyIndex returns the position of an assignment to key, or -1.
func keyIndex(lines []string, key string) int {
	for i, line := range lines {
		if k, ok := keyOf(line); ok && k == key {
			return i
		}
	}
	return -1
}

// iniSet sets a key inside a group, creating either as needed.
func iniSet(lines []string, section, key, value string) []string {
	assign := key + "=" + value
	start, end := sectionRange(lines, section)

	if start < 0 {
		// No such group yet: append it, separated from what is above.
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		return append(out, "["+section+"]", assign)
	}

	if i := keyIndex(lines[start:end], key); i >= 0 {
		out := append([]string{}, lines...)
		out[start+i] = assign
		return out
	}

	// The group is there but the key is not: insert it after the group's last
	// non-blank line, before the blank line separating it from the next group.
	at := start + contentEnd(lines[start:end])
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, assign)
	return append(out, lines[at:]...)
}

// contentEnd returns the index just after the last non-blank line.
func contentEnd(lines []string) int {
	i := len(lines)
	for i > 0 && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	return i
}

// iniUnset removes a key from a group, leaving the group itself in place.
func iniUnset(lines []string, section, key string) []string {
	start, end := sectionRange(lines, section)
	if start < 0 {
		return lines
	}
	i := keyIndex(lines[start:end], key)
	if i < 0 {
		return lines
	}
	out := make([]string, 0, len(lines)-1)
	out = append(out, lines[:start+i]...)
	return append(out, lines[start+i+1:]...)
}
