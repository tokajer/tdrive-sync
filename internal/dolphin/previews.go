package dolphin

// Experimental (and labelled as such in the UI): what follows depends on KDE
// implementation details, and Dolphin applies the setting only when it opens a
// folder – so a restart is needed and the first visit previews anyway.
//
// Previews are the reason a sync folder fills up by itself: Dolphin renders them
// by reading the file, and reading through an rclone mount downloads it. Browsing
// the Drive therefore pulls it onto the disk, and "free up space" is undone within
// seconds while a folder is on screen. Dolphin can be told to skip previews for a
// single folder, which is what this file does.
//
// Two pieces are needed, and only both together have an effect:
//
//  1. dolphinrc must let folders keep their own view properties
//     (`GlobalViewProps=false`). With Dolphin's "common properties for all
//     folders" every per-folder setting is ignored – verified by experiment.
//  2. a view-properties file for the sync folder saying `PreviewsShown=false`.
//     It goes into Dolphin's own store below ~/.local/share/dolphin, never into
//     the sync folder: a file there would be uploaded to Drive and show up on
//     every other device.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// previewsStateFile remembers what dolphinrc said before we touched it, so
// switching previews back on can restore exactly that.
const previewsStateFile = "dolphin-previews.state"

// defaultStateFile does the same for Dolphin's global view-properties default.
const defaultStateFile = "dolphin-previews-default.state"

// PreviewReport describes whether previews are switched off for the sync folder.
type PreviewReport struct {
	// Disabled is the state the UI shows: the override exists *and* Dolphin
	// honours per-folder properties.
	Disabled bool
	// Override reports that our view-properties file says PreviewsShown=false.
	Override bool
	// PerFolder reports that dolphinrc allows per-folder view properties.
	PerFolder bool
	// File is the view-properties file we own.
	File string
}

// PreviewsStatus reports the current state for a sync folder.
func PreviewsStatus(syncDir string) (PreviewReport, error) {
	p, err := previewPaths(syncDir)
	if err != nil {
		return PreviewReport{}, err
	}
	r := PreviewReport{File: p.viewProps}

	lines, err := readLines(p.viewProps)
	if err != nil {
		return r, err
	}
	if v, ok := iniValue(lines, "Dolphin", "PreviewsShown"); ok {
		r.Override = strings.EqualFold(strings.TrimSpace(v), "false")
	}

	rc, err := readLines(p.dolphinrc)
	if err != nil {
		return r, err
	}
	// Absent means Dolphin's default, which is the common-properties mode.
	if v, ok := iniValue(rc, "General", "GlobalViewProps"); ok {
		r.PerFolder = strings.EqualFold(strings.TrimSpace(v), "false")
	}

	r.Disabled = r.Override && r.PerFolder
	return r, nil
}

// SetPreviews switches Dolphin's previews for the sync folder off or on again.
//
// Switching off also flips dolphinrc to per-folder view properties, remembering
// the previous value; switching on restores it and removes our override. Dolphin
// reads the properties when it enters a folder, so an open window needs a reload.
func SetPreviews(syncDir string, disable bool) error {
	p, err := previewPaths(syncDir)
	if err != nil {
		return err
	}
	if disable {
		if err := writeViewProps(p.viewProps); err != nil {
			return err
		}
		return enablePerFolderProps(p)
	}
	// Everything below our own store directory belongs to folders inside the sync
	// folder, so the whole subtree goes.
	if err := os.RemoveAll(filepath.Dir(p.viewProps)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return restorePerFolderProps(p)
}

// ApplyPreviewFolders extends the switch to the folders inside the sync folder
// and reports how many markers it had to create.
//
// Necessary because Dolphin keeps view properties per folder and does *not*
// inherit them: switching previews off for the sync folder silences its own view
// only, while every subfolder would still preview – and thus download – its
// content. rels are Drive-relative folder paths; a folder that already has our
// marker is left alone, so this is cheap to call repeatedly.
func ApplyPreviewFolders(syncDir string, rels []string) (int, error) {
	p, err := previewPaths(syncDir)
	if err != nil {
		return 0, err
	}
	base := filepath.Dir(p.viewProps)
	written := 0
	for _, rel := range rels {
		rel = strings.Trim(strings.TrimSpace(rel), "/")
		if rel == "" || strings.HasPrefix(rel, "../") {
			continue
		}
		file := filepath.Join(base, filepath.FromSlash(rel), ".directory")
		if previewsOff(file) {
			continue
		}
		if err := writeViewProps(file); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// PreviewsDefaultOff reports whether "no previews" is Dolphin's default for every
// folder that has no view properties of its own.
func PreviewsDefaultOff() (bool, error) {
	props, _, err := globalPreviewFiles()
	if err != nil {
		return false, err
	}
	lines, err := readLines(props)
	if err != nil {
		return false, err
	}
	v, ok := iniValue(lines, "Dolphin", "PreviewsShown")
	return ok && strings.EqualFold(strings.TrimSpace(v), "false"), nil
}

// SetPreviewsDefault makes "no previews" Dolphin's default, or takes that back.
//
// This is the only way to keep the *first* look into a folder from generating
// previews: a folder's own properties are applied after its view is already up, so
// one round of previews has run by then. Verified by experiment that Dolphin uses
// this file as the fallback even with per-folder properties enabled – a single
// folder can therefore still switch its previews back on.
//
// It reaches beyond the sync folder, which is why it is a separate, opt-in switch.
func SetPreviewsDefault(disable bool) error {
	props, state, err := globalPreviewFiles()
	if err != nil {
		return err
	}
	lines, err := readLines(props)
	if err != nil {
		return err
	}
	if disable {
		previous, had := iniValue(lines, "Dolphin", "PreviewsShown")
		if had && strings.EqualFold(strings.TrimSpace(previous), "false") {
			return nil // already the default, nothing of ours to remember
		}
		// What to put back later: the previous value, nothing (the key was absent),
		// or the whole file if it did not exist at all.
		note := ""
		switch {
		case len(lines) == 0:
			note = newFileNote
		case had:
			note = previous
		}
		if err := writeFile(state, note+"\n", 0o644); err != nil {
			return err
		}
		lines = iniSet(lines, "Dolphin", "PreviewsShown", "false")
		if note == newFileNote {
			// A file Dolphin never wrote needs the two keys it expects.
			lines = iniSet(lines, "Dolphin", "Version", "4")
			lines = iniSet(lines, "Dolphin", "Timestamp", timestamp())
		}
		return writeLines(props, lines)
	}

	previous, err := os.ReadFile(state)
	if os.IsNotExist(err) {
		return nil // we never changed it
	}
	if err != nil {
		return err
	}
	switch v := strings.TrimSpace(string(previous)); v {
	case newFileNote:
		if err := os.Remove(props); err != nil && !os.IsNotExist(err) {
			return err
		}
	case "":
		if err := writeLines(props, iniUnset(lines, "Dolphin", "PreviewsShown")); err != nil {
			return err
		}
	default:
		if err := writeLines(props, iniSet(lines, "Dolphin", "PreviewsShown", v)); err != nil {
			return err
		}
	}
	return os.Remove(state)
}

// newFileNote marks that Dolphin had no global view properties before, so taking
// the default back means removing the file again rather than editing it.
const newFileNote = "!new"

// globalPreviewFiles resolves Dolphin's global view properties and where the
// previous value of the previews key is remembered.
func globalPreviewFiles() (props, state string, err error) {
	dataDir, err := xdgDir("XDG_DATA_HOME", ".local", "share")
	if err != nil {
		return "", "", err
	}
	stateDir, err := xdgDir("XDG_STATE_HOME", ".local", "state")
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dataDir, "dolphin", "view_properties", "global", ".directory"),
		filepath.Join(stateDir, "tdrive-sync", defaultStateFile), nil
}

// xdgDir returns an XDG base directory, falling back to its usual place in home.
func xdgDir(env string, fallback ...string) (string, error) {
	if v := os.Getenv(env); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}

// previewsOff reports whether a view-properties file already switches previews
// off, so an existing marker is not rewritten (its timestamp would change and
// Dolphin would reload for nothing).
func previewsOff(file string) bool {
	lines, err := readLines(file)
	if err != nil {
		return false
	}
	v, ok := iniValue(lines, "Dolphin", "PreviewsShown")
	return ok && strings.EqualFold(strings.TrimSpace(v), "false")
}

// previewFiles are the files the switch touches.
type previewFiles struct {
	viewProps string
	dolphinrc string
	state     string
}

// previewPaths resolves every file involved for one sync folder.
func previewPaths(syncDir string) (previewFiles, error) {
	syncDir = filepath.Clean(syncDir)
	if !filepath.IsAbs(syncDir) {
		return previewFiles{}, fmt.Errorf("sync folder %q is not an absolute path", syncDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return previewFiles{}, err
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share")
	}
	confDir := os.Getenv("XDG_CONFIG_HOME")
	if confDir == "" {
		confDir = filepath.Join(home, ".config")
	}
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		stateDir = filepath.Join(home, ".local", "state")
	}
	// Dolphin's layout: view_properties/local/<absolute path>/.directory
	return previewFiles{
		viewProps: filepath.Join(dataDir, "dolphin", "view_properties", "local"+syncDir, ".directory"),
		dolphinrc: filepath.Join(confDir, "dolphinrc"),
		state:     filepath.Join(stateDir, "tdrive-sync", previewsStateFile),
	}, nil
}

// writeViewProps writes the per-folder properties that switch previews off.
//
// The timestamp matters: Dolphin discards per-folder properties that are older
// than `ViewPropsTimestamp` in dolphinrc, so ours has to be the current time
// rather than something fixed.
func writeViewProps(path string) error {
	content := "[Dolphin]\nPreviewsShown=false\nTimestamp=" + timestamp() + "\nVersion=4\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// timestamp is "now" in the form Dolphin writes into view properties.
func timestamp() string {
	n := time.Now()
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d", n.Year(), int(n.Month()), n.Day(), n.Hour(), n.Minute(), n.Second())
}

// enablePerFolderProps makes Dolphin honour per-folder properties, remembering
// what was configured before.
func enablePerFolderProps(p previewFiles) error {
	lines, err := readLines(p.dolphinrc)
	if err != nil {
		return err
	}
	previous, had := iniValue(lines, "General", "GlobalViewProps")
	if had && strings.EqualFold(strings.TrimSpace(previous), "false") {
		return nil // already per-folder, nothing of ours to remember
	}
	if !had {
		previous = "" // absent; restoring means removing the key again
	}
	if err := writeFile(p.state, previous+"\n", 0o644); err != nil {
		return err
	}
	return writeLines(p.dolphinrc, iniSet(lines, "General", "GlobalViewProps", "false"))
}

// restorePerFolderProps undoes enablePerFolderProps.
func restorePerFolderProps(p previewFiles) error {
	previous, err := os.ReadFile(p.state)
	if os.IsNotExist(err) {
		return nil // we never changed it
	}
	if err != nil {
		return err
	}
	lines, err := readLines(p.dolphinrc)
	if err != nil {
		return err
	}
	if v := strings.TrimSpace(string(previous)); v == "" {
		lines = iniUnset(lines, "General", "GlobalViewProps")
	} else {
		lines = iniSet(lines, "General", "GlobalViewProps", v)
	}
	if err := writeLines(p.dolphinrc, lines); err != nil {
		return err
	}
	return os.Remove(p.state)
}

// -------- a very small INI editor --------
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
	in := false
	for _, line := range lines {
		if s, ok := sectionOf(line); ok {
			in = s == section
			continue
		}
		if !in {
			continue
		}
		if k, ok := keyOf(line); ok && k == key {
			_, value, _ := strings.Cut(line, "=")
			return value, true
		}
	}
	return "", false
}

// iniSet sets a key inside a group, creating either as needed.
func iniSet(lines []string, section, key, value string) []string {
	out := make([]string, 0, len(lines)+3)
	in, done, insertAt := false, false, -1
	for _, line := range lines {
		if s, ok := sectionOf(line); ok {
			if in && !done {
				// The group ended without the key: remember where its content
				// stops, before the blank line separating it from the next group.
				insertAt = contentEnd(out)
			}
			in = s == section
		} else if in && !done {
			if k, ok := keyOf(line); ok && k == key {
				out = append(out, key+"="+value)
				done = true
				continue
			}
		}
		out = append(out, line)
	}
	switch {
	case done:
		return out
	case in: // the group is the last one in the file
		insertAt = contentEnd(out)
	case insertAt < 0: // no such group yet
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		return append(out, "["+section+"]", key+"="+value)
	}
	out = append(out, "")
	copy(out[insertAt+1:], out[insertAt:])
	out[insertAt] = key + "=" + value
	return out
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
	out := make([]string, 0, len(lines))
	in := false
	for _, line := range lines {
		if s, ok := sectionOf(line); ok {
			in = s == section
		} else if in {
			if k, ok := keyOf(line); ok && k == key {
				continue
			}
		}
		out = append(out, line)
	}
	return out
}
