// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// startProc launches rclone with args, tagging its output in the log, and
// returns the command plus a channel that receives its exit error.
//
// A process whose output cannot be captured is not started at all: it would run
// with no progress and no way to say why it failed.
func (m *Manager) startProc(args []string, tag string) (*exec.Cmd, chan error, error) {
	return m.engine.Start(args, func(line string) { m.log.Logf("[%s] %s", tag, line) })
}

// terminate asks a sync process to stop, killing it if it does not. rclone needs
// the grace period to flush its cache and unmount.
func (m *Manager) terminate(cmd *exec.Cmd, done <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(terminateGrace)
	defer timer.Stop()
	select {
	case <-done:
		// Exited on its own; nothing to kill and no goroutine left behind.
	case <-timer.C:
		m.log.Logf("[%s] did not stop within %s, killing it", cmd.Path, terminateGrace)
		_ = cmd.Process.Kill()
	}
}

// unmount releases a FUSE mount point, lazily so a busy mount still goes away.
func (m *Manager) unmount(mp string) {
	for _, tool := range []string{"fusermount3", "fusermount"} {
		if _, err := exec.LookPath(tool); err == nil {
			_ = exec.Command(tool, "-uz", mp).Run()
			return
		}
	}
}

func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sanitize turns a path into something usable as a file name.
func sanitize(p string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(strings.TrimPrefix(p, "/"))
}
