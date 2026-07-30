// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package manager

import "context"

// watchMirror is a no-op on platforms without inotify; mirror mode then relies
// on interval-based polling alone.
func (m *Manager) watchMirror(ctx context.Context, root string) {}
