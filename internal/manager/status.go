// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"sync"
	"time"

	"tdrive-sync/internal/config"
)

// State is a coarse sync state used for the tray icon and UI.
type State string

const (
	StateDisconnected State = "disconnected" // not signed in
	StateStarting     State = "starting"     // mount/bisync coming up
	StateSyncing      State = "syncing"      // transfers in progress
	StateIdle         State = "idle"         // up to date
	StatePaused       State = "paused"       // user paused
	StateError        State = "error"        // needs attention
)

// Runtime is the half of the status that comes from the running sync. It is the
// only part the manager stores; everything else in a Status is read from the
// configuration when the snapshot is taken.
type Runtime struct {
	State    State
	Message  string
	Bytes    int64
	Speed    float64
	Errors   int64
	LastSync time.Time
}

// Status is an immutable snapshot handed to observers. Its JSON form is written
// to status.json and served by the settings API, so the field names are a
// contract with anything monitoring the daemon.
type Status struct {
	State        State           `json:"state"`
	Mode         config.SyncMode `json:"mode"`
	ConflictMode string          `json:"conflict_mode"`
	Message      string          `json:"message"`
	Account      string          `json:"account"`
	LocalDir     string          `json:"local_dir"`
	Bytes        int64           `json:"bytes"`
	Speed        float64         `json:"speed"`
	Errors       int64           `json:"errors"`
	LastSync     time.Time       `json:"last_sync"`
	Offline      []string        `json:"offline"`
}

// Active reports whether the daemon is syncing, as opposed to signed out or shut
// down. The file-manager integration shows indicators only while it is.
func (s Status) Active() bool {
	return s.State != "" && s.State != StateDisconnected
}

// statusStore owns the runtime status and the observers watching it.
// Config-derived fields are read via settings at snapshot time, never stored.
type statusStore struct {
	// settings returns the configuration half of a snapshot, with the runtime
	// half left zero. Called under the lock, so it must not call back into the
	// store.
	settings func() Status

	mu        sync.Mutex
	rt        Runtime
	listeners []func(Status)

	// deliver serialises snapshot+delivery so listeners see updates in order;
	// listeners must not call back into the store.
	deliver sync.Mutex
}

func newStatusStore(settings func() Status) *statusStore {
	return &statusStore{settings: settings}
}

// Get returns the current snapshot.
func (s *statusStore) Get() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// snapshotLocked merges the configuration and runtime halves. Caller holds s.mu.
func (s *statusStore) snapshotLocked() Status {
	out := s.settings()
	out.State = s.rt.State
	out.Message = s.rt.Message
	out.Bytes = s.rt.Bytes
	out.Speed = s.rt.Speed
	out.Errors = s.rt.Errors
	out.LastSync = s.rt.LastSync
	return out
}

// Subscribe registers fn for every subsequent change, and delivers the current
// snapshot right away so an observer never starts out blank. fn must not call
// back into the store (see deliver) - it would deadlock.
func (s *statusStore) Subscribe(fn func(Status)) {
	s.deliver.Lock()
	defer s.deliver.Unlock()
	s.mu.Lock()
	s.listeners = append(s.listeners, fn)
	cur := s.snapshotLocked()
	s.mu.Unlock()
	fn(cur)
}

// SetState records a coarse state with its message and notifies observers.
func (s *statusStore) SetState(state State, msg string) {
	s.Update(func(rt *Runtime) {
		rt.State = state
		rt.Message = msg
	})
}

// Update applies fn to the runtime status and notifies observers. Returning
// false from fn skips the notification, for a change that turned out to be no
// change at all.
func (s *statusStore) Update(fn func(*Runtime)) {
	s.UpdateIf(func(rt *Runtime) bool {
		fn(rt)
		return true
	})
}

// UpdateIf applies fn and notifies observers only when fn reports a change.
func (s *statusStore) UpdateIf(fn func(*Runtime) bool) {
	s.mu.Lock()
	changed := fn(&s.rt)
	s.mu.Unlock()
	if !changed {
		return
	}

	s.deliver.Lock()
	defer s.deliver.Unlock()
	s.mu.Lock()
	cur := s.snapshotLocked()
	ls := append([]func(Status){}, s.listeners...)
	s.mu.Unlock()

	for _, fn := range ls {
		fn(cur)
	}
}

// Notify re-delivers the current snapshot without changing it. Used when a
// config change moved something the snapshot derives.
func (s *statusStore) Notify() {
	s.Update(func(*Runtime) {})
}
