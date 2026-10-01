// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package dolphin

import (
	"context"
	"time"
)

const (
	// keeperInterval is how often the markers are reconciled with the folders
	// that exist in the Drive.
	keeperInterval = 30 * time.Minute
	// listTimeout bounds the recursive folder listing one pass needs.
	listTimeout = 3 * time.Minute
)

// Drive is what the keeper needs to know about the running sync. The manager
// implements it; stating it here is what keeps the sync backend free of any
// dependency on the file-manager integration.
type Drive interface {
	// SyncFolder is the folder to write markers for, or "" when there is
	// nothing mounted to write them for right now.
	SyncFolder() string
	// ListDirs returns every folder below a Drive-relative path.
	ListDirs(ctx context.Context, rel string) ([]string, error)
}

// Keeper keeps the "no previews here" markers in step with the folders that
// exist in the Drive, for as long as the setting is switched on.
//
// It has to be repeated because Dolphin stores view properties per folder
// without inheriting them: a folder created later would come with previews on
// again, and a preview downloads every file it renders.
type Keeper struct {
	drive Drive
	logf  func(string, ...any)
	now   chan struct{}
}

// NewKeeper returns a keeper for drive. logf may be nil.
func NewKeeper(drive Drive, logf func(string, ...any)) *Keeper {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Keeper{drive: drive, logf: logf, now: make(chan struct{}, 1)}
}

// Nudge asks for a pass right away, for instance just after the user switched
// previews off. It never blocks.
func (k *Keeper) Nudge() {
	select {
	case k.now <- struct{}{}:
	default:
	}
}

// Run reconciles the markers periodically until ctx is cancelled.
func (k *Keeper) Run(ctx context.Context) {
	ticker := time.NewTicker(keeperInterval)
	defer ticker.Stop()
	for {
		k.apply(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-k.now:
		}
	}
}

// apply writes the markers for every folder in the Drive, once.
func (k *Keeper) apply(ctx context.Context) {
	syncDir := k.drive.SyncFolder()
	if syncDir == "" {
		return // nothing mounted to write markers for
	}
	rep, err := PreviewsStatus(syncDir)
	if err != nil || !rep.Disabled {
		return // the user did not ask for this
	}

	lctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	dirs, err := k.drive.ListDirs(lctx, "")
	if err != nil {
		if ctx.Err() == nil {
			k.logf("could not list the folders for the preview setting: %v", err)
		}
		return
	}
	n, err := ApplyPreviewFolders(syncDir, dirs)
	if err != nil {
		k.logf("preview setting: %v", err)
	}
	if n > 0 {
		k.logf("previews switched off for %d new folder(s), %d in the Drive", n, len(dirs))
	}
}
