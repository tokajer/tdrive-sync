package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"tdrive-sync/internal/config"
)

// conflictMarker is the substring rclone bisync puts into the name of a
// conflicting file (from our --conflict-suffix). Both the manual-mode copies
// (…conflict1 / …conflict2) and the automatic-mode dated backups
// (…conflict-YYYY-MM-DD) contain it.
const conflictMarker = ".conflict"

// reConflict matches the conflict marker (optionally followed by a number or a
// date) plus an optional trailing extension, so the original name can be
// reconstructed whether rclone appends the suffix at the end or before the
// extension.
var reConflict = regexp.MustCompile(`\.conflict[-0-9]*(\.[^./]*)?$`)

// Conflict describes one conflicting file found in the local mirror.
type Conflict struct {
	Path  string    `json:"path"`  // path relative to the local dir
	Base  string    `json:"base"`  // original name without the conflict marker
	Side  string    `json:"side"`  // "cloud" | "local" | "backup"
	Size  int64     `json:"size"`  // file size in bytes
	MTime time.Time `json:"mtime"` // last-modified time
}

// Conflicts scans the local mirror for unresolved conflict files. It is only
// meaningful in mirror mode; in stream mode the local dir is a mount and holds
// no such files.
func (m *Manager) Conflicts() []Conflict {
	// Only mirror mode has real local files; walking a stream mount would pull
	// every file down on demand.
	if m.cfg.Mode != config.ModeMirror {
		return nil
	}
	root := m.cfg.LocalDir
	var out []Conflict
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.Contains(name, conflictMarker) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, Conflict{
			Path:  rel,
			Base:  markerBase(rel),
			Side:  conflictSide(name),
			Size:  info.Size(),
			MTime: info.ModTime(),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ResolveConflict resolves a single conflict file. action "keep" promotes the
// file to its original name and removes the sibling conflict copies; action
// "delete" just removes the copy. Either way a fresh sync is triggered so the
// decision propagates to Drive.
func (m *Manager) ResolveConflict(rel, action string) error {
	root := m.cfg.LocalDir
	// Clean against "/" so any ".." cannot escape the local dir.
	full := filepath.Join(root, filepath.Clean("/"+rel))
	if !strings.HasPrefix(full, filepath.Clean(root)+string(os.PathSeparator)) {
		return fmt.Errorf("ungültiger Pfad")
	}
	if !strings.Contains(filepath.Base(full), conflictMarker) {
		return fmt.Errorf("keine Konfliktdatei")
	}

	switch action {
	case "delete":
		if err := os.Remove(full); err != nil {
			return err
		}
		m.logf("Konflikt gelöst: %s gelöscht", rel)
	case "keep":
		dir := filepath.Dir(full)
		base := markerBase(filepath.Base(full))
		target := filepath.Join(dir, base)
		if err := os.Rename(full, target); err != nil {
			return err
		}
		removeSiblingConflicts(dir, base, target)
		m.logf("Konflikt gelöst: %s als %s behalten", rel, base)
	default:
		return fmt.Errorf("unbekannte Aktion: %s", action)
	}

	m.SyncNow()
	return nil
}

// markerBase reconstructs the original file name (or relative path) by removing
// the conflict marker segment.
func markerBase(name string) string {
	loc := reConflict.FindStringSubmatchIndex(name)
	if loc == nil {
		return name
	}
	ext := ""
	if loc[2] >= 0 {
		ext = name[loc[2]:loc[3]]
	}
	return name[:loc[0]] + ext
}

// conflictSide guesses which side a conflict copy came from. rclone numbers the
// copies by path, and our bisync uses Path1 = Drive, Path2 = local.
func conflictSide(name string) string {
	switch {
	case strings.Contains(name, ".conflict1"):
		return "cloud"
	case strings.Contains(name, ".conflict2"):
		return "local"
	default:
		return "backup"
	}
}

// removeSiblingConflicts deletes any other conflict copies in dir that share the
// same original base name, leaving only keep.
func removeSiblingConflicts(dir, base, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.Contains(name, conflictMarker) || markerBase(name) != base {
			continue
		}
		p := filepath.Join(dir, name)
		if p == keep {
			continue
		}
		_ = os.Remove(p)
	}
}
