// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

// Package tray shows a system-tray icon via the freedesktop StatusNotifierItem
// (SNI) protocol over DBus — no GTK/cgo required. It is best-effort: if no SNI
// host is running (e.g. GNOME without the AppIndicator extension) Run returns an
// error and the caller simply continues without a tray.
package tray

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"tdrive-sync/internal/i18n"
	"tdrive-sync/internal/manager"
)

// appName is the tray item title shown by the host.
const appName = "TDrive Sync"

const (
	sniPath  = "/StatusNotifierItem"
	menuPath = "/StatusNotifierItem/menu"
	sniIface = "org.kde.StatusNotifierItem"
	menuIf   = "com.canonical.dbusmenu"
)

// Actions holds the callbacks invoked from the tray menu.
type Actions struct {
	OpenFolder   func()
	SyncNow      func()
	TogglePause  func()
	OpenSettings func()
	Logout       func()
	Quit         func()
}

type iconPix struct {
	W, H  int32
	Bytes []byte
}

// Run installs the tray icon and blocks until ctx is cancelled. It returns an
// error if no tray host accepted the registration.
func Run(ctx context.Context, mgr *manager.Manager, act Actions, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("session bus: %w", err)
	}

	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	if _, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
		return fmt.Errorf("request name: %w", err)
	}

	menu := newMenu(act)
	menu.conn = conn
	item := &snItem{act: act, wake: make(chan struct{}, 1), desired: manager.StateDisconnected}

	if err := conn.Export(item, sniPath, sniIface); err != nil {
		return err
	}
	if err := conn.Export(menu, menuPath, menuIf); err != nil {
		return err
	}

	sniProps, err := prop.Export(conn, sniPath, item.propSpec())
	if err != nil {
		return err
	}
	if _, err := prop.Export(conn, menuPath, menu.propSpec()); err != nil {
		return err
	}
	item.props = sniProps
	item.menu = menu
	item.conn = conn

	// Register with the StatusNotifierWatcher.
	watcher := conn.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher")
	if call := watcher.Call("org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem", 0, name); call.Err != nil {
		return fmt.Errorf("no tray host: %w", call.Err)
	}
	logf("tray registered as %s", name)

	// Drive the icon animation (spin / blink) in its own goroutine.
	go item.animate(ctx)

	// Reflect manager status into the icon, tooltip and menu.
	mgr.Subscribe(func(st manager.Status) {
		item.update(st)
	})

	<-ctx.Done()
	return nil
}

// ---------------- StatusNotifierItem ----------------

type snItem struct {
	act   Actions
	conn  *dbus.Conn
	props *prop.Properties
	menu  *dbusMenu

	mu      sync.Mutex
	desired manager.State
	lastMsg string
	wake    chan struct{} // signals animate() that desired changed
}

func (s *snItem) propSpec() map[string]map[string]*prop.Prop {
	return map[string]map[string]*prop.Prop{
		sniIface: {
			"Category":   {Value: "ApplicationStatus", Writable: false},
			"Id":         {Value: "tdrive-sync", Writable: false},
			"Title":      {Value: appName, Writable: false},
			"Status":     {Value: "Active", Writable: false},
			"WindowId":   {Value: int32(0), Writable: false},
			"IconName":   {Value: "", Writable: false},
			"IconPixmap": {Value: greyFrame(), Writable: false},
			"ToolTip":    {Value: makeToolTip(appName, i18n.T("state.disconnected")), Writable: false},
			"ItemIsMenu": {Value: true, Writable: false},
			"Menu":       {Value: dbus.ObjectPath(menuPath), Writable: false},
		},
	}
}

// Activate is emitted on a primary (left) click.
func (s *snItem) Activate(x, y int32) *dbus.Error {
	if s.act.OpenSettings != nil {
		s.act.OpenSettings()
	}
	return nil
}

// SecondaryActivate is emitted on a middle click.
func (s *snItem) SecondaryActivate(x, y int32) *dbus.Error { return nil }

// ContextMenu is emitted on a right click (host usually shows Menu itself).
func (s *snItem) ContextMenu(x, y int32) *dbus.Error { return nil }

// Scroll is emitted on wheel scroll over the icon.
func (s *snItem) Scroll(delta int32, orientation string) *dbus.Error { return nil }

func (s *snItem) update(st manager.Status) {
	tip := st.Message
	if st.Account != "" {
		tip = st.Account + " — " + st.Message
	}

	s.mu.Lock()
	s.desired = st.State
	msgChanged := tip != s.lastMsg
	s.lastMsg = tip
	s.mu.Unlock()

	if msgChanged && s.props != nil {
		s.props.SetMust(sniIface, "ToolTip", makeToolTip(appName, tip))
		if s.conn != nil {
			_ = s.conn.Emit(sniPath, sniIface+".NewToolTip")
		}
	}
	// Wake the animator to re-evaluate the icon for the new state.
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if s.menu != nil {
		s.menu.updateFromStatus(st)
	}
}

// animate owns the tray IconPixmap: a static logo when idle, a grey logo when
// disconnected/paused, the spinning logo while syncing, and a red blink on
// error. It runs until ctx is cancelled.
func (s *snItem) animate(ctx context.Context) {
	cur := manager.State("") // force the first apply() to render

	var ticker *time.Ticker
	var tick <-chan time.Time
	phase := 0

	setIcon := func(px []iconPix) {
		if s.props == nil {
			return
		}
		s.props.SetMust(sniIface, "IconPixmap", px)
		if s.conn != nil {
			_ = s.conn.Emit(sniPath, sniIface+".NewIcon")
		}
	}
	render := func() {
		switch cur {
		case manager.StateSyncing, manager.StateStarting:
			f := spinFrames()
			setIcon(f[phase%len(f)])
		case manager.StateError:
			f := errorFrames()
			setIcon(f[phase%len(f)])
		case manager.StateIdle:
			setIcon(idleFrame())
		default: // disconnected, paused
			setIcon(greyFrame())
		}
	}
	apply := func() {
		s.mu.Lock()
		want := s.desired
		s.mu.Unlock()
		if want == cur {
			return
		}
		cur = want
		phase = 0
		if ticker != nil {
			ticker.Stop()
			ticker, tick = nil, nil
		}
		switch cur {
		case manager.StateSyncing, manager.StateStarting:
			ticker = time.NewTicker(70 * time.Millisecond)
		case manager.StateError:
			ticker = time.NewTicker(450 * time.Millisecond)
		}
		if ticker != nil {
			tick = ticker.C
		}
		render()
	}

	apply()
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			apply()
		case <-tick:
			phase++
			render()
		}
	}
}

func makeToolTip(title, sub string) []interface{} {
	// (s a(iiay) s s) => iconName, iconPixmaps, title, description
	return []interface{}{"", []iconPix{}, title, sub}
}
