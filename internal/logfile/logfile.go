// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logfile provides a simple day-rotating file writer with age-based
// retention, used to persist the daemon's log to disk while keeping it tidy.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	filePrefix = "tdrive-sync-"
	fileSuffix = ".log"
	dateLayout = "2006-01-02"
)

// Writer is an io.Writer that appends to a per-day log file, rolls over at
// midnight, and prunes files older than the retention window. It is safe for
// concurrent use.
type Writer struct {
	dir       string
	retention time.Duration

	mu  sync.Mutex
	day string
	f   *os.File
}

// New opens (or creates) today's log file in dir and prunes anything older than
// retentionDays. retentionDays <= 0 defaults to 7.
func New(dir string, retentionDays int) (*Writer, error) {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, retention: time.Duration(retentionDays) * 24 * time.Hour}
	if err := w.rotate(time.Now()); err != nil {
		return nil, err
	}
	return w, nil
}

// Write implements io.Writer, rotating to a new file when the day changes.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if now.Format(dateLayout) != w.day {
		if err := w.rotate(now); err != nil {
			return 0, err
		}
	}
	if w.f == nil {
		return len(p), nil
	}
	return w.f.Write(p)
}

// Close closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		return err
	}
	return nil
}

// rotate switches to the log file for the given day and prunes old files. The
// caller must hold w.mu (or be constructing the Writer).
func (w *Writer) rotate(now time.Time) error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	day := now.Format(dateLayout)
	path := filepath.Join(w.dir, filePrefix+day+fileSuffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.f = f
	w.day = day
	w.prune(now)
	return nil
}

// prune deletes log files whose day is older than the retention window.
func (w *Writer) prune(now time.Time) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-w.retention)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) < len(filePrefix)+len(fileSuffix) {
			continue
		}
		if name[:len(filePrefix)] != filePrefix || name[len(name)-len(fileSuffix):] != fileSuffix {
			continue
		}
		dayStr := name[len(filePrefix) : len(name)-len(fileSuffix)]
		day, perr := time.Parse(dateLayout, dayStr)
		if perr != nil {
			continue
		}
		if day.Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}

// Path returns today's log file path (informational).
func (w *Writer) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return filepath.Join(w.dir, fmt.Sprintf("%s%s%s", filePrefix, w.day, fileSuffix))
}
