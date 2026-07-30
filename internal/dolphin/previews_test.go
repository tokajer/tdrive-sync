package dolphin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox points every XDG location and the home directory at a throwaway tree,
// so the tests never touch the real Dolphin configuration.
func sandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

func TestSetPreviewsRoundTrip(t *testing.T) {
	dir := sandbox(t)
	sync := filepath.Join(dir, "GoogleDrive")
	rc := filepath.Join(dir, "config", "dolphinrc")

	// A configuration that already holds unrelated settings: they must survive.
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "[General]\nVersion=202\nViewPropsTimestamp=2025,11,22,9,33,49.608\n\n[MainWindow]\nMenuBar=Disabled\n"
	if err := os.WriteFile(rc, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if r, err := PreviewsStatus(sync); err != nil || r.Disabled {
		t.Fatalf("fresh state: got %+v, err %v; want previews on", r, err)
	}

	if err := SetPreviews(sync, true); err != nil {
		t.Fatal(err)
	}
	r, err := PreviewsStatus(sync)
	if err != nil || !r.Disabled || !r.Override || !r.PerFolder {
		t.Fatalf("after switching off: got %+v, err %v", r, err)
	}
	// The view properties belong in Dolphin's store, never inside the sync folder:
	// a file there would be uploaded to Drive.
	if !strings.Contains(r.File, filepath.Join("data", "dolphin", "view_properties", "local")) {
		t.Errorf("view properties in an unexpected place: %s", r.File)
	}
	if _, err := os.Stat(filepath.Join(sync, ".directory")); !os.IsNotExist(err) {
		t.Error("a .directory file was written into the sync folder")
	}
	rcNow := readFileString(t, rc)
	for _, want := range []string{"GlobalViewProps=false", "Version=202", "MenuBar=Disabled"} {
		if !strings.Contains(rcNow, want) {
			t.Errorf("dolphinrc is missing %q:\n%s", want, rcNow)
		}
	}

	if err := SetPreviews(sync, false); err != nil {
		t.Fatal(err)
	}
	if r, err := PreviewsStatus(sync); err != nil || r.Disabled || r.Override {
		t.Fatalf("after switching on: got %+v, err %v", r, err)
	}
	if got := readFileString(t, rc); got != before {
		t.Errorf("dolphinrc was not restored:\n--- got\n%s\n--- want\n%s", got, before)
	}
}

// TestSetPreviewsKeepsForeignSetting guards the case where the user already had
// per-folder view properties: switching previews on again must not take that away.
func TestSetPreviewsKeepsForeignSetting(t *testing.T) {
	dir := sandbox(t)
	sync := filepath.Join(dir, "GoogleDrive")
	rc := filepath.Join(dir, "config", "dolphinrc")
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rc, []byte("[General]\nGlobalViewProps=false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetPreviews(sync, true); err != nil {
		t.Fatal(err)
	}
	if err := SetPreviews(sync, false); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, rc); !strings.Contains(got, "GlobalViewProps=false") {
		t.Errorf("the user's own setting was removed:\n%s", got)
	}
}

// TestApplyPreviewFolders covers the part that makes the switch work at all:
// Dolphin does not inherit view properties, so every folder needs its own marker.
func TestApplyPreviewFolders(t *testing.T) {
	dir := sandbox(t)
	sync := filepath.Join(dir, "GoogleDrive")
	if err := SetPreviews(sync, true); err != nil {
		t.Fatal(err)
	}

	dirs := []string{"bsc", "bsc/k8s", "with space/deep", "/leading", "trailing/", "", "."}
	n, err := ApplyPreviewFolders(sync, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("wrote %d markers, want 5 (empty and \".\" are skipped)", n)
	}
	store := filepath.Join(dir, "data", "dolphin", "view_properties", "local"+sync)
	for _, rel := range []string{"bsc", filepath.Join("bsc", "k8s"), "leading", "trailing"} {
		if !previewsOff(filepath.Join(store, rel, ".directory")) {
			t.Errorf("no marker for %s", rel)
		}
	}

	// Repeating must be a no-op: rewriting would change the timestamps and make
	// Dolphin reload for nothing.
	if n, err := ApplyPreviewFolders(sync, dirs); err != nil || n != 0 {
		t.Errorf("second run wrote %d markers (err %v), want 0", n, err)
	}

	// Switching previews back on takes the whole tree with it.
	if err := SetPreviews(sync, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Errorf("the marker tree survived: %v", err)
	}
}

// TestSetPreviewsDefault covers the opt-in default: it must not destroy other
// keys in Dolphin's global view properties, and it has to be reversible.
func TestSetPreviewsDefault(t *testing.T) {
	dir := sandbox(t)
	props := filepath.Join(dir, "data", "dolphin", "view_properties", "global", ".directory")
	if err := os.MkdirAll(filepath.Dir(props), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "[Dolphin]\nViewMode=1\nVersion=4\n"
	if err := os.WriteFile(props, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if off, err := PreviewsDefaultOff(); err != nil || off {
		t.Fatalf("fresh state: off=%t, err %v", off, err)
	}
	if err := SetPreviewsDefault(true); err != nil {
		t.Fatal(err)
	}
	if off, err := PreviewsDefaultOff(); err != nil || !off {
		t.Fatalf("after switching: off=%t, err %v", off, err)
	}
	if got := readFileString(t, props); !strings.Contains(got, "ViewMode=1") {
		t.Errorf("an unrelated key was lost:\n%s", got)
	}
	if err := SetPreviewsDefault(false); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, props); got != before {
		t.Errorf("not restored:\n--- got\n%s\n--- want\n%s", got, before)
	}
}

func TestIniEditing(t *testing.T) {
	t.Run("set into an existing group", func(t *testing.T) {
		in := []string{"[General]", "A=1", "", "[Other]", "B=2"}
		got := strings.Join(iniSet(in, "General", "C", "3"), "\n")
		want := "[General]\nA=1\nC=3\n\n[Other]\nB=2"
		if got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("replace in place", func(t *testing.T) {
		in := []string{"[General]", "A=1", "[Other]", "A=2"}
		got := strings.Join(iniSet(in, "Other", "A", "9"), "\n")
		if want := "[General]\nA=1\n[Other]\nA=9"; got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("create a missing group", func(t *testing.T) {
		got := strings.Join(iniSet([]string{"[Other]", "B=2"}, "General", "A", "1"), "\n")
		if want := "[Other]\nB=2\n\n[General]\nA=1"; got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("empty file", func(t *testing.T) {
		got := strings.Join(iniSet(nil, "General", "A", "1"), "\n")
		if want := "[General]\nA=1"; got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("unset keeps the group", func(t *testing.T) {
		in := []string{"[General]", "A=1", "B=2"}
		got := strings.Join(iniUnset(in, "General", "A"), "\n")
		if want := "[General]\nB=2"; got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("value lookup is group-scoped", func(t *testing.T) {
		in := []string{"[General]", "A=1", "[Other]", "A=2"}
		if v, ok := iniValue(in, "Other", "A"); !ok || v != "2" {
			t.Errorf("got (%q, %t), want (\"2\", true)", v, ok)
		}
		if _, ok := iniValue(in, "Missing", "A"); ok {
			t.Error("found a key in a group that does not exist")
		}
	})
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
