// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"context"
	"errors"
	"testing"

	"tdrive-sync/internal/config"
)

// TestStartWithoutRemoteStaysSignedOut: a config that names an account whose
// remote has gone (rclone.conf deleted by hand) must not start a runner.
func TestStartWithoutRemoteStaysSignedOut(t *testing.T) {
	m := newTestManager(t)
	if err := m.cfg.SetAccountEmail("test@example.com"); err != nil {
		t.Fatal(err)
	}
	m.account = &fakeAccount{remote: false}
	engine := newFakeEngine()
	m.engine = engine

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Shutdown()

	if got := m.Status().State; got != StateDisconnected {
		t.Fatalf("state = %q, want disconnected", got)
	}
	if m.active != nil {
		t.Fatal("a runner was started without a remote")
	}
}

// TestLoginStoresAccountAndStarts covers the first sign-in: the account is
// recorded and syncing begins without a further call.
func TestLoginStoresAccountAndStarts(t *testing.T) {
	m := newTestManager(t)
	if err := m.cfg.SetMode(config.ModeMirror); err != nil {
		t.Fatal(err)
	}
	m.account = &fakeAccount{email: "someone@example.com"}
	engine := newFakeEngine()
	m.engine = engine

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	if got := m.Status().State; got != StateDisconnected {
		t.Fatalf("before login: state = %q, want disconnected", got)
	}

	if err := m.Login(ctx, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := m.cfg.AccountEmail(); got != "someone@example.com" {
		t.Errorf("account = %q, want someone@example.com", got)
	}
	engine.runOnce(t, nil)
	waitForState(t, m, StateIdle)
	m.Shutdown()
}

// TestLoginFailureLeavesSignedOut: a cancelled or failed OAuth flow records no
// account and starts nothing.
func TestLoginFailureLeavesSignedOut(t *testing.T) {
	m := newTestManager(t)
	m.account = &fakeAccount{loginErr: errors.New("cancelled")}

	if err := m.Login(context.Background(), "", nil); err == nil {
		t.Fatal("Login succeeded although the OAuth flow failed")
	}
	if m.cfg.Configured() {
		t.Error("a failed login recorded an account")
	}
}

// TestLogoutStopsRunner: signing out stops syncing and clears the account,
// even when rclone could not delete the remote.
func TestLogoutStopsRunner(t *testing.T) {
	m := newTestManager(t)
	if err := m.cfg.SetAccountEmail("test@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.cfg.SetMode(config.ModeMirror); err != nil {
		t.Fatal(err)
	}
	acct := &fakeAccount{remote: true, logoutErr: errors.New("locked")}
	m.account = acct
	engine := newFakeEngine()
	m.engine = engine

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	engine.runOnce(t, nil)
	waitForState(t, m, StateIdle)

	if err := m.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if m.cfg.Configured() {
		t.Error("account still recorded after logout")
	}
	if got := m.Status().State; got != StateDisconnected {
		t.Errorf("state = %q, want disconnected", got)
	}
	if acct.logouts != 1 {
		t.Errorf("rclone logout called %d times, want 1", acct.logouts)
	}
	m.Shutdown()
}
