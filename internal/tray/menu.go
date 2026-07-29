//go:build linux

package tray

import (
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"tdrive-sync/internal/manager"
)

// menuEntry is one row in the tray menu.
type menuEntry struct {
	id      int32
	label   string
	enabled bool
	sep     bool
	cb      func()
}

// layout is the recursive (ia{sv}av) structure returned by GetLayout.
type layout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

type propEntry struct {
	ID    int32
	Props map[string]dbus.Variant
}

type dbusMenu struct {
	conn *dbus.Conn

	mu       sync.Mutex
	entries  []*menuEntry
	revision uint32
	header   *menuEntry
	pause    *menuEntry
}

func newMenu(act Actions) *dbusMenu {
	header := &menuEntry{id: 1, label: "Nicht angemeldet", enabled: false}
	pause := &menuEntry{id: 5, label: "Pausieren", enabled: true, cb: act.TogglePause}
	m := &dbusMenu{
		header: header,
		pause:  pause,
		entries: []*menuEntry{
			header,
			{id: 2, sep: true},
			{id: 3, label: "Ordner öffnen", enabled: true, cb: act.OpenFolder},
			{id: 4, label: "Jetzt synchronisieren", enabled: true, cb: act.SyncNow},
			pause,
			{id: 6, label: "Einstellungen…", enabled: true, cb: act.OpenSettings},
			{id: 7, sep: true},
			{id: 8, label: "Abmelden", enabled: true, cb: act.Logout},
			{id: 9, sep: true},
			{id: 10, label: "Beenden", enabled: true, cb: act.Quit},
		},
	}
	return m
}

func (m *dbusMenu) propSpec() map[string]map[string]*prop.Prop {
	return map[string]map[string]*prop.Prop{
		menuIf: {
			"Version":       {Value: uint32(3)},
			"Status":        {Value: "normal"},
			"TextDirection": {Value: "ltr"},
			"IconThemePath": {Value: []string{}},
		},
	}
}

func entryProps(e *menuEntry) map[string]dbus.Variant {
	if e.sep {
		return map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}
	}
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(e.label),
		"enabled": dbus.MakeVariant(e.enabled),
		"visible": dbus.MakeVariant(true),
	}
}

// GetLayout returns the full menu (parentID 0 = root).
func (m *dbusMenu) GetLayout(parentID, recursionDepth int32, propertyNames []string) (uint32, layout, *dbus.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	children := make([]dbus.Variant, 0, len(m.entries))
	for _, e := range m.entries {
		children = append(children, dbus.MakeVariant(layout{
			ID:       e.id,
			Props:    entryProps(e),
			Children: []dbus.Variant{},
		}))
	}
	root := layout{
		ID:       0,
		Props:    map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")},
		Children: children,
	}
	return m.revision, root, nil
}

// GetGroupProperties returns properties for the requested item ids (all if empty).
func (m *dbusMenu) GetGroupProperties(ids []int32, propertyNames []string) ([]propEntry, *dbus.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[int32]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := make([]propEntry, 0, len(m.entries))
	for _, e := range m.entries {
		if len(ids) == 0 || want[e.id] {
			out = append(out, propEntry{ID: e.id, Props: entryProps(e)})
		}
	}
	return out, nil
}

// GetProperty returns a single property value for an item.
func (m *dbusMenu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.id == id {
			if v, ok := entryProps(e)[name]; ok {
				return v, nil
			}
		}
	}
	return dbus.MakeVariant(""), nil
}

// Event handles menu interactions; "clicked" fires the item's callback.
func (m *dbusMenu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	m.mu.Lock()
	var cb func()
	for _, e := range m.entries {
		if e.id == id {
			cb = e.cb
			break
		}
	}
	m.mu.Unlock()
	if cb != nil {
		go cb()
	}
	return nil
}

// AboutToShow lets us refresh a submenu before it opens; nothing dynamic here.
func (m *dbusMenu) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

// updateFromStatus relabels the header and pause entries and, if anything
// changed, tells the host to re-read the layout.
func (m *dbusMenu) updateFromStatus(st manager.Status) {
	header := "Nicht angemeldet"
	if st.Account != "" {
		header = st.Account
	}
	pauseLabel := "Pausieren"
	if st.State == manager.StatePaused {
		pauseLabel = "Fortsetzen"
	}

	m.mu.Lock()
	changed := m.header.label != header || m.pause.label != pauseLabel
	m.header.label = header
	m.pause.label = pauseLabel
	if changed {
		m.revision++
	}
	rev := m.revision
	conn := m.conn
	m.mu.Unlock()

	if changed && conn != nil {
		_ = conn.Emit(menuPath, menuIf+".LayoutUpdated", rev, int32(0))
	}
}
