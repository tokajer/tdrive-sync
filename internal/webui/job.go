// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package webui

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// maxJobLines caps the output kept for snapshot to return.
const maxJobLines = 200

// job runs one long-lived background task at a time and captures its output
// for a status endpoint to poll. Login and the Dolphin plugin install/remove
// are its two users, both "start it, then poll for progress" from the UI's
// point of view.
type job struct {
	mu       sync.Mutex
	name     string // "" while nothing runs
	lines    []string
	err      string
	cancelFn context.CancelFunc
}

// start runs fn in the background under a context with the given timeout,
// unless a job is already running, in which case it does nothing and returns
// false. Every line fn reports through logf is kept (capped to maxJobLines)
// for snapshot to return.
func (j *job) start(name string, timeout time.Duration, fn func(ctx context.Context, logf func(string, ...any)) error) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	j.mu.Lock()
	if j.name != "" {
		j.mu.Unlock()
		cancel()
		return false
	}
	j.name = name
	j.lines = nil
	j.err = ""
	j.cancelFn = cancel
	j.mu.Unlock()

	go func() {
		defer cancel()
		err := fn(ctx, func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			j.mu.Lock()
			j.lines = append(j.lines, line)
			if len(j.lines) > maxJobLines {
				j.lines = j.lines[len(j.lines)-maxJobLines:]
			}
			j.mu.Unlock()
		})
		j.mu.Lock()
		j.name = ""
		j.cancelFn = nil
		if err != nil {
			j.err = err.Error()
		}
		j.mu.Unlock()
	}()
	return true
}

// snapshot returns the job's current name ("" = not running), its captured
// output and its last error.
func (j *job) snapshot() (name string, lines []string, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.name, append([]string{}, j.lines...), j.err
}

// cancel stops the running job, if any. Used to give up on a build that
// stalls instead of leaving the job slot taken until the daemon restarts.
func (j *job) cancel() {
	j.mu.Lock()
	fn := j.cancelFn
	j.mu.Unlock()
	if fn != nil {
		fn()
	}
}
