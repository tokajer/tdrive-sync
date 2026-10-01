// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package manager

import (
	"context"
	"sync/atomic"
)

// watchLocal is a no-op where inotify is unavailable; the interval-based sync
// covers local changes on its own.
func watchLocal(context.Context, string, Logger, chan<- struct{}, *atomic.Bool) {}
