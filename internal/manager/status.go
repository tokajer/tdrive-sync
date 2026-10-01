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
	State        State               `json:"state"`
	Mode         config.SyncMode     `json:"mode"`
	ConflictMode config.ConflictMode `json:"conflict_mode"`
	Message      string              `json:"message"`
	Account      string              `json:"account"`
	LocalDir     string              `json:"local_dir"`
	Bytes        int64               `json:"bytes"`
	Speed        float64             `json:"speed"`
	Errors       int64               `json:"errors"`
	LastSync     time.Time           `json:"last_sync"`
	Offline      []string            `json:"offline"`
}

// Active reports whether the daemon is syncing, as opposed to signed out or shut
// down. The file-manager integration shows indicators only while it is.
func (s Status) Active() bool {
	return s.State != "" && s.State != StateDisconnected
}

// statusStore owns the runtime status and the observers watching it.
// Config-derived fields are read via settings at snapshot time, never stored.
//
// Observers are called from a goroutine of their own, never from the caller of
// Update: they write files and talk to DBus, and a stalled disk or session bus
// must not hold up the sync runners that report their state through here.
type statusStore struct {
	// settings returns the configuration half of a snapshot, with the runtime
	// half left zero. Called under the lock, so it must not call back into the
	// store.
	settings func() Status

	mu          sync.Mutex
	rt          Runtime
	subscribers []*subscriber
	closed      bool
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

// Subscribe registers fn for every subsequent change, and hands it the current
// snapshot first so an observer never starts out blank. The returned function
// unregisters fn again.
//
// fn sees snapshots in order, but a slow fn may skip intermediate ones: it is
// always given the newest state, which is all an observer of a status needs.
func (s *statusStore) Subscribe(fn func(Status)) (unsubscribe func()) {
	sub := newSubscriber(fn)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return func() {}
	}
	s.subscribers = append(s.subscribers, sub)
	sub.offer(s.snapshotLocked())
	s.mu.Unlock()
	go sub.run()

	return func() {
		s.mu.Lock()
		for i, x := range s.subscribers {
			if x == sub {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		sub.close()
	}
}

// SetState records a coarse state with its message and notifies observers.
func (s *statusStore) SetState(state State, msg string) {
	s.Update(func(rt *Runtime) {
		rt.State = state
		rt.Message = msg
	})
}

// Update applies fn to the runtime status and notifies observers.
func (s *statusStore) Update(fn func(*Runtime)) {
	s.UpdateIf(func(rt *Runtime) bool {
		fn(rt)
		return true
	})
}

// UpdateIf applies fn and notifies observers only when fn reports a change.
// It never waits for an observer.
func (s *statusStore) UpdateIf(fn func(*Runtime) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !fn(&s.rt) || s.closed {
		return
	}
	// Offered under the lock that took the snapshot, so a newer snapshot can
	// never be overtaken by an older one.
	cur := s.snapshotLocked()
	for _, sub := range s.subscribers {
		sub.offer(cur)
	}
}

// Notify re-delivers the current snapshot without changing it. Used when a
// config change moved something the snapshot derives.
func (s *statusStore) Notify() {
	s.Update(func(*Runtime) {})
}

// Flush waits until every observer has handled the newest snapshot.
func (s *statusStore) Flush() {
	s.mu.Lock()
	subs := append([]*subscriber{}, s.subscribers...)
	s.mu.Unlock()
	for _, sub := range subs {
		sub.flush()
	}
}

// Close stops delivering: observers finish what is queued, and once Close
// returns none of them runs again. Later updates still change the status, they
// just reach nobody - shutdown relies on that to publish its final state
// without a late delivery overwriting it.
func (s *statusStore) Close() {
	s.mu.Lock()
	s.closed = true
	subs := s.subscribers
	s.subscribers = nil
	s.mu.Unlock()
	for _, sub := range subs {
		sub.close()
		sub.wait()
	}
}

// subscriber delivers snapshots to one observer from its own goroutine,
// keeping only the newest undelivered one.
type subscriber struct {
	fn func(Status)

	mu      sync.Mutex
	cond    *sync.Cond
	next    Status
	pending bool // next has not been handed to fn yet
	busy    bool // fn is running
	closed  bool
	exited  bool
}

func newSubscriber(fn func(Status)) *subscriber {
	sub := &subscriber{fn: fn}
	sub.cond = sync.NewCond(&sub.mu)
	return sub
}

// offer replaces whatever is still queued with st.
func (sub *subscriber) offer(st Status) {
	sub.mu.Lock()
	sub.next = st
	sub.pending = true
	sub.mu.Unlock()
	sub.cond.Broadcast()
}

// run is the delivery loop. It drains what is queued before it honours close.
func (sub *subscriber) run() {
	sub.mu.Lock()
	defer func() {
		sub.exited = true
		sub.mu.Unlock()
		sub.cond.Broadcast()
	}()
	for {
		for !sub.pending && !sub.closed {
			sub.cond.Wait()
		}
		if !sub.pending {
			return
		}
		st := sub.next
		sub.pending, sub.busy = false, true
		sub.mu.Unlock()

		sub.fn(st)

		sub.mu.Lock()
		sub.busy = false
		sub.cond.Broadcast()
	}
}

func (sub *subscriber) flush() {
	sub.mu.Lock()
	for (sub.pending || sub.busy) && !sub.exited {
		sub.cond.Wait()
	}
	sub.mu.Unlock()
}

func (sub *subscriber) close() {
	sub.mu.Lock()
	sub.closed = true
	sub.mu.Unlock()
	sub.cond.Broadcast()
}

func (sub *subscriber) wait() {
	sub.mu.Lock()
	for !sub.exited {
		sub.cond.Wait()
	}
	sub.mu.Unlock()
}
