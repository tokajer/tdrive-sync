// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package notify sends desktop notifications over the freedesktop DBus service,
// degrading to a no-op when no session bus / notification daemon is available.
package notify

import (
	"sync"

	"github.com/godbus/dbus/v5"
)

// Notifier shows a desktop notification.
type Notifier interface {
	Notify(title, body string)
}

// Noop is a Notifier that does nothing.
type Noop struct{}

// Notify implements Notifier.
func (Noop) Notify(string, string) {}

// DBus posts notifications via org.freedesktop.Notifications.
type DBus struct {
	mu      sync.Mutex
	conn    *dbus.Conn
	appName string
	appIcon string
	lastID  uint32
}

// NewDBus connects to the session bus. It returns a Noop-backed notifier
// (never nil) so callers can use it unconditionally even without a bus.
func NewDBus(appName, appIcon string) Notifier {
	conn, err := dbus.SessionBus()
	if err != nil || conn == nil {
		return Noop{}
	}
	return &DBus{conn: conn, appName: appName, appIcon: appIcon}
}

// Notify implements Notifier, replacing the previous notification in place.
func (d *DBus) Notify(title, body string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	obj := d.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		d.appName,           // app_name
		d.lastID,            // replaces_id
		d.appIcon,           // app_icon
		title,               // summary
		body,                // body
		[]string{},          // actions
		map[string]dbus.Variant{}, // hints
		int32(5000),         // expire_timeout ms
	)
	if call.Err == nil {
		_ = call.Store(&d.lastID)
	}
}
