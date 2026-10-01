// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// watchMask is the set of events that mean "the local tree changed".
const watchMask = syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MODIFY |
	syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO | syscall.IN_CLOSE_WRITE

// watchLocal watches root recursively with Linux inotify and fires the
// (debounced) trigger whenever local files change, so edits reach Drive almost
// immediately instead of waiting for the next poll interval. It runs until ctx
// is cancelled and never returns an error: if inotify is unavailable the
// interval-based sync still covers everything.
//
// busy, when true, means a mirror run is in progress: its own writes (files
// bisync just downloaded) must not re-trigger itself.
func watchLocal(ctx context.Context, root string, log Logger, trigger chan<- struct{}, busy *atomic.Bool) {
	fd, err := syscall.InotifyInit1(syscall.IN_NONBLOCK | syscall.IN_CLOEXEC)
	if err != nil {
		log.Logf("real-time watching unavailable (%v) – interval sync stays active", err)
		return
	}
	// Wrapped in os.File so Go's runtime poller services the Read: a plain
	// blocking syscall.Read on this fd would not notice the fd being closed
	// from another goroutine and the reader would leak forever.
	file := os.NewFile(uintptr(fd), "inotify")
	go func() {
		<-ctx.Done()
		_ = file.Close()
	}()

	w := &watcher{fd: fd, file: file, dirs: map[int32]string{}, busy: busy}
	w.addRecursive(root)

	changed := make(chan struct{}, 1)
	go w.read(changed)
	debounce(ctx, changed, trigger)
}

// debounce collapses a burst of change signals into a single trigger a short
// moment after the last one. Editors and copies produce many events for what
// the user did once.
func debounce(ctx context.Context, changed <-chan struct{}, trigger chan<- struct{}) {
	timer := time.NewTimer(localChangeQuiet)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	var quiet <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			timer.Reset(localChangeQuiet)
			quiet = timer.C
		case <-quiet:
			quiet = nil
			select {
			case trigger <- struct{}{}:
			default:
			}
		}
	}
}

// watcher holds the inotify descriptor and the directories it covers. Only the
// reader goroutine touches dirs after setup, so it needs no locking.
type watcher struct {
	fd int
	// file is the fd wrapped for blocking, cancellable reads; see watchLocal.
	file *os.File
	dirs map[int32]string
	// busy suppresses signalling while a mirror run it would otherwise
	// re-trigger is in progress. See watchLocal.
	busy *atomic.Bool
}

// read parses inotify events, keeps the watch set in step with the tree, and
// signals changed (coalesced) for any relevant event. It returns once file is
// closed.
func (w *watcher) read(changed chan<- struct{}) {
	buf := make([]byte, 64*1024)
	for {
		n, err := w.file.Read(buf)
		if err != nil || n < syscall.SizeofInotifyEvent {
			return
		}
		hit := false
		var off uint32
		for off <= uint32(n)-uint32(syscall.SizeofInotifyEvent) {
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
			name := eventName(buf, off, raw.Len)
			w.apply(raw, name)
			hit = true
			off += uint32(syscall.SizeofInotifyEvent) + raw.Len
		}
		// SHORTCUT: events from a run's last writes that are read just after
		// busy clears can still slip through and cause one extra, redundant
		// run; revisit if that turns out to matter in practice (e.g. by
		// draining once more right after busy flips back to false).
		if hit && !w.busy.Load() {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
}

// apply keeps the watch set in step with one event.
func (w *watcher) apply(raw *syscall.InotifyEvent, name string) {
	// The kernel dropped this watch (the directory went away, or the watch was
	// removed). Forgetting it keeps the map from growing for the lifetime of a
	// long-running mirror, which would eventually exhaust max_user_watches.
	if raw.Mask&syscall.IN_IGNORED != 0 {
		delete(w.dirs, raw.Wd)
		return
	}
	// A new directory: start watching it too, so coverage stays complete.
	isNewDir := raw.Mask&syscall.IN_ISDIR != 0 &&
		raw.Mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0
	if !isNewDir || name == "" {
		return
	}
	if parent, ok := w.dirs[raw.Wd]; ok {
		w.addRecursive(filepath.Join(parent, name))
	}
}

// addRecursive adds a watch to root and every directory beneath it. Failures on
// individual directories are ignored (best-effort coverage).
func (w *watcher) addRecursive(root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		wd, werr := syscall.InotifyAddWatch(w.fd, path, watchMask)
		if werr != nil {
			return nil
		}
		w.dirs[int32(wd)] = path
		return nil
	})
}

// eventName extracts the (NUL-padded) name that follows an inotify event.
func eventName(buf []byte, off uint32, length uint32) string {
	if length == 0 {
		return ""
	}
	start := off + uint32(syscall.SizeofInotifyEvent)
	end := start + length
	if end > uint32(len(buf)) {
		return ""
	}
	return strings.TrimRight(string(buf[start:end]), "\x00")
}
