// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package manager

import "time"

// Timing of the sync runners, in one place so the reconnect and retry behaviour
// can be read and tuned without hunting for literals across the package.
const (
	// mountReadyTimeout is how long the mount's control server may take to
	// answer before the mount counts as not coming up.
	mountReadyTimeout = 40 * time.Second
	// mountReadyPoll is the spacing between those attempts.
	mountReadyPoll = time.Second
	// remountDelay is the pause after a mount died before it is started again.
	remountDelay = 5 * time.Second
	// statsInterval is how often transfer statistics are polled from the mount.
	statsInterval = 3 * time.Second
	// rcCallTimeout bounds a single call to the mount's control server.
	rcCallTimeout = 5 * time.Second
	// terminateGrace is how long a sync process gets after SIGTERM before it is
	// killed. rclone needs it to flush the VFS cache and unmount cleanly.
	terminateGrace = 8 * time.Second

	// mirrorMinInterval is the floor under the configured reconcile interval.
	mirrorMinInterval = 30 * time.Second
	// mirrorRetryInterval replaces it after a failed run, so a recovery does
	// not have to wait out the full interval.
	mirrorRetryInterval = 20 * time.Second
	// mirrorLockMaxAge is the age past which a bisync lock can only come from a
	// run that crashed. It has to exceed rclone's --max-lock (2m).
	mirrorLockMaxAge = 5 * time.Minute
	// maxMirrorFailures is how many consecutive bisync failures trigger an
	// automatic full resync (auto-recovery).
	maxMirrorFailures = 3
	// localChangeQuiet collapses a burst of local file events into one sync.
	// Editors and copies produce many events for a single logical change.
	localChangeQuiet = 2 * time.Second

	// warmTimeout bounds one pass of warming the offline-pinned paths.
	warmTimeout = 30 * time.Minute
	// evictTimeout bounds releasing a path and freeing its cache.
	evictTimeout = 30 * time.Minute
)
