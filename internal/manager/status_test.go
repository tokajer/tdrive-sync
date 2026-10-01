// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"sync"
	"testing"

	"tdrive-sync/internal/config"
)

// TestStatusFollowsConfig asserts that mode, account, sync folder and offline
// pins in a status snapshot always match the configuration.
func TestStatusFollowsConfig(t *testing.T) {
	m := newTestManager(t)

	if got := m.Status().LocalDir; got != m.cfg.LocalDir() {
		t.Fatalf("initial LocalDir = %q, want %q", got, m.cfg.LocalDir())
	}

	moved := t.TempDir()
	if err := m.cfg.SetLocalDir(moved); err != nil {
		t.Fatal(err)
	}
	if got := m.Status().LocalDir; got != moved {
		t.Errorf("after moving the sync folder, status says %q, want %q", got, moved)
	}

	if err := m.cfg.SetAccountEmail("someone@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := m.Status().Account; got != "someone@example.com" {
		t.Errorf("account = %q, want someone@example.com", got)
	}

	if err := m.cfg.SetMode(config.ModeMirror); err != nil {
		t.Fatal(err)
	}
	if got := m.Status().Mode; got != config.ModeMirror {
		t.Errorf("mode = %q, want mirror", got)
	}

	if err := m.cfg.SetOffline("Documents", true); err != nil {
		t.Fatal(err)
	}
	if got := m.Status().Offline; len(got) != 1 || got[0] != "Documents" {
		t.Errorf("offline = %v, want [Documents]", got)
	}
}

// TestSubscribeDeliversCurrent checks an observer is not left blank until the
// next change: the tray icon and the status file both need a first value.
func TestSubscribeDeliversCurrent(t *testing.T) {
	m := newTestManager(t)
	m.status.SetState(StateIdle, "up to date")

	var got Status
	m.Subscribe(func(s Status) { got = s })
	if got.State != StateIdle {
		t.Errorf("a new observer got %q, want the current state %q", got.State, StateIdle)
	}
}

// TestUpdateNotifiesObservers covers the path every status change takes.
func TestUpdateNotifiesObservers(t *testing.T) {
	m := newTestManager(t)

	var mu sync.Mutex
	var seen []State
	m.Subscribe(func(s Status) {
		mu.Lock()
		seen = append(seen, s.State)
		mu.Unlock()
	})

	m.status.SetState(StateStarting, "mounting")
	m.status.SetState(StateIdle, "up to date")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 { // the initial delivery plus two changes
		t.Fatalf("observer saw %v, want three deliveries", seen)
	}
	if seen[1] != StateStarting || seen[2] != StateIdle {
		t.Errorf("observer saw %v, want [… starting idle]", seen)
	}
}

// TestActiveExcludesDisconnected pins down when the file-manager integration
// shows indicators at all.
func TestActiveExcludesDisconnected(t *testing.T) {
	for _, tc := range []struct {
		state State
		want  bool
	}{
		{StateIdle, true},
		{StateSyncing, true},
		{StateStarting, true},
		{StateError, true},
		{StatePaused, true},
		{StateDisconnected, false},
		{"", false},
	} {
		if got := (Status{State: tc.state}).Active(); got != tc.want {
			t.Errorf("Active() for %q = %t, want %t", tc.state, got, tc.want)
		}
	}
}

// TestConcurrentStatusAccess drives the store the way the daemon does: the
// stats poller, the runners and the HTTP handlers all touch it at once.
func TestConcurrentStatusAccess(t *testing.T) {
	m := newTestManager(t)
	m.Subscribe(func(Status) {})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				m.status.Update(func(rt *Runtime) { rt.Bytes += 1 })
			}
		}()
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = m.Status()
			}
		}()
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				m.status.SetState(StateSyncing, "syncing")
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentUpdatesDeliverFinalStateLast asserts that a listener's
// last-seen snapshot under concurrent updates always matches the final Get().
func TestConcurrentUpdatesDeliverFinalStateLast(t *testing.T) {
	m := newTestManager(t)

	var mu sync.Mutex
	var lastSeen Status
	m.Subscribe(func(s Status) {
		mu.Lock()
		lastSeen = s
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				m.status.Update(func(rt *Runtime) { rt.Bytes++ })
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if want := m.Status().Bytes; lastSeen.Bytes != want {
		t.Errorf("listener's last-seen Bytes = %d, want %d (Get())", lastSeen.Bytes, want)
	}
}

// TestHumanBytes covers the sizes the "space freed" notification reports.
func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0 B",
		512:           "512 B",
		1024:          "1.0 KiB",
		1536:          "1.5 KiB",
		1024 * 1024:   "1.0 MiB",
		3 * 1 << 30:   "3.0 GiB",
		2 * 1 << 40:   "2.0 TiB",
		1024*1024 - 1: "1024.0 KiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
