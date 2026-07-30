// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// watchMirror watches root recursively with Linux inotify and fires the
// (debounced) sync trigger whenever local files change, so edits reach Drive
// almost immediately instead of waiting for the next poll interval. It runs
// until ctx is cancelled and never returns an error: if inotify is unavailable
// the interval-based sync still covers everything.
func (m *Manager) watchMirror(ctx context.Context, root string) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		m.logf("real-time watching unavailable (%v) – interval sync stays active", err)
		return
	}
	// Closing the fd unblocks the Read in readInotify so the goroutine exits.
	go func() {
		<-ctx.Done()
		_ = syscall.Close(fd)
	}()

	const mask = syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MODIFY |
		syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO | syscall.IN_CLOSE_WRITE

	// watches maps a watch descriptor back to its directory so we can extend the
	// watch set when new subdirectories appear. Only the reader goroutine
	// touches it, so no locking is needed.
	watches := map[int32]string{}
	addWatchRecursive(fd, root, mask, watches)

	changed := make(chan struct{}, 1)
	go m.readInotify(fd, mask, watches, changed)

	// Debounce: editors and copies produce bursts of events; collapse them into
	// a single trigger a short moment after the last one.
	const quiet = 2 * time.Second
	var timerC <-chan time.Time
	timer := time.NewTimer(quiet)
	if !timer.Stop() {
		<-timer.C
	}
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Reset(quiet)
			timerC = timer.C
		case <-timerC:
			timerC = nil
			select {
			case m.syncTrigger <- struct{}{}:
			default:
			}
		}
	}
}

// readInotify parses inotify events, adds watches for newly created
// directories, and signals changed (coalesced) for any relevant event. It
// returns when the fd is closed.
func (m *Manager) readInotify(fd int, mask uint32, watches map[int32]string, changed chan<- struct{}) {
	buf := make([]byte, 64*1024)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil || n < syscall.SizeofInotifyEvent {
			return
		}
		hit := false
		var off uint32
		for off <= uint32(n)-uint32(syscall.SizeofInotifyEvent) {
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameLen := raw.Len
			name := ""
			if nameLen > 0 {
				start := off + uint32(syscall.SizeofInotifyEvent)
				name = strings.TrimRight(string(buf[start:start+nameLen]), "\x00")
			}
			// A new directory: start watching it too, so coverage stays complete.
			if raw.Mask&syscall.IN_ISDIR != 0 &&
				raw.Mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
				if parent, ok := watches[raw.Wd]; ok && name != "" {
					addWatchRecursive(fd, filepath.Join(parent, name), mask, watches)
				}
			}
			hit = true
			off += uint32(syscall.SizeofInotifyEvent) + nameLen
		}
		if hit {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
}

// addWatchRecursive adds an inotify watch to root and every directory beneath
// it. Failures on individual directories are ignored (best-effort coverage).
func addWatchRecursive(fd int, root string, mask uint32, watches map[int32]string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		wd, werr := syscall.InotifyAddWatch(fd, path, mask)
		if werr != nil {
			return nil
		}
		watches[int32(wd)] = path
		return nil
	})
}
