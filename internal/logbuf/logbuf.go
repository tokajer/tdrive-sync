// Package logbuf is a small in-memory, thread-safe ring buffer for recent log
// lines. Every line is also mirrored to the standard logger (stderr/journal).
// The web UI reads from it to show an error/event log.
package logbuf

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Entry is one captured log line.
type Entry struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"` // "info" | "warn" | "error"
	Msg   string    `json:"msg"`
}

// Buffer holds the most recent log entries.
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	max     int
}

// New returns a buffer that keeps at most max entries.
func New(max int) *Buffer {
	if max <= 0 {
		max = 500
	}
	return &Buffer{max: max}
}

// Logf formats a message, prints it to the standard logger and stores it.
func (b *Buffer) Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	b.Add(classify(msg), msg)
}

// Add stores a pre-classified message.
func (b *Buffer) Add(level, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, Entry{Time: time.Now(), Level: level, Msg: msg})
	if len(b.entries) > b.max {
		b.entries = b.entries[len(b.entries)-b.max:]
	}
}

// Entries returns a copy of the stored entries, optionally errors/warnings only.
func (b *Buffer) Entries(errorsOnly bool) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Entry, 0, len(b.entries))
	for _, e := range b.entries {
		if errorsOnly && e.Level == "info" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Clear empties the buffer.
func (b *Buffer) Clear() {
	b.mu.Lock()
	b.entries = nil
	b.mu.Unlock()
}

// classify guesses a severity from the message text.
func classify(msg string) string {
	u := strings.ToUpper(msg)
	switch {
	case strings.Contains(u, "ERROR"), strings.Contains(u, "FAILED"),
		strings.Contains(u, "CRITICAL"), strings.Contains(u, "FATAL"):
		return "error"
	case strings.Contains(u, "WARNING"):
		return "warn"
	default:
		return "info"
	}
}
