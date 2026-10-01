# TODO

Open work, most useful first. Each item carries enough context to be picked up
cold — see [REENTRY.md](REENTRY.md) for the architecture behind them.

## 1. Nautilus / GNOME support

`internal/fmstate` is already desktop-agnostic — it publishes
`file-manager.json` and answers `tdrive-sync file-state`. A GNOME sibling of
`internal/dolphin` would be a `nautilus-python` extension implementing
`Nautilus.InfoProvider.update_file_info()` and calling `add_emblem()`, so no
compiler is needed, but it does need the `nautilus-python` package. Port the state
logic a third time or shell out to `tdrive-sync file-state` in batches.

Untested locally: this machine has no Nautilus installed.

## 2. Previews pull the files back in — experimental switch, not dependable

**Symptom:** "Free up space" on a folder frees it and the folder is orange again
seconds later. Reproduced and measured on 2026-07-30:

- `Arbeit` (nobody looking at it): freed → stays empty → `cloud`. Correct.
- `bsc` : freed → all files fully back within 2–10 s → `partial`.
- The reader is `kioworker … kf6/kio/thumbnail.so`, Dolphin's thumbnailer: it was
  holding files open all over the Drive (`Dark Age of Camelot/LotM/*.ini` while
  those folders were on screen). Baloo is *not* involved (indexing is disabled for
  those files), and it is not self-inflicted either: deleting the cache files by
  hand, without `vfs/forget` and without removing the cache directories, is undone
  just as fast.

So the indicator is telling the truth – the data really is local again. The cause
is that a preview reads the whole file, and reading through an rclone mount
downloads it. The Windows clients dodge this with a placeholder file attribute
that Explorer honours; Linux has no equivalent, every read hydrates.

**Mitigated, marked experimental in the UI**, by a switch in the settings card ("Switch previews off in the sync
folder", `tdrive-sync dolphin previews on|off`, `internal/dolphin/previews.go`).
Measured on the way there, so nobody repeats it: a `.directory` *inside* the folder
has no effect at all (Dolphin always reads its own store path), and view properties
are **per folder without inheritance** – silencing the sync folder leaves every
subfolder previewing. Hence `dolphin.Keeper` (`internal/dolphin/keeper.go`), which writes
the marker for every folder from `rclone lsjson --dirs-only --recursive` and
repeats every 30 minutes for folders that appear later. It is started from `cmd`
rather than by the manager, so the sync backend carries no dependency on a file
manager.

Why it stays experimental (all measured):

- Dolphin reads view properties when it opens a folder, and a new window reuses the
  running process, so only a full restart (`kquitapp6 dolphin`) applies it.
- A folder's own properties arrive after its view is up, so the first visit still
  previews once. Only the opt-in global default stops that – and that one reaches
  beyond the sync folder.
- Folders created between two refreshes (30 min) preview once before their marker
  exists.
- It all hinges on Dolphin's `GlobalViewProps` handling, which is a KDE
  implementation detail, not an API.

The dependable fix is a different placeholder model (see how Nextcloud does it on
Linux: a `name.ext.nextcloud` stub has no thumbnailer, so nothing hydrates) or an
OS-level placeholder attribute, which Linux does not have. Both are far outside
this switch.

## 3. States that were never seen on screen

`uploading` (local changes on their way up) and `pinning` (pin still downloading)
are implemented and unit-tested, but their overlays were never verified visually —
producing them needs a write into the user's real Drive. Check the icon choice
(`cloud-upload`, `emblem-synchronizing-symbolic`) the next time a large upload
happens naturally.

## 4. Smaller items

- **Tracked-item cap:** the overlay plugin follows at most 2000 items for live
  updates (`kMaxTracked` in `overlayplugin.cpp`). Beyond that, overlays are still
  correct when a folder is opened but do not refresh by themselves. Fine for now;
  revisit if someone browses huge folders.
- **Releasing inside a pinned folder does nothing:** "free up space" on an item
  below a pinned folder deletes its cache, but the folder's pin brings it back on
  the next warm run. `Manager.SetOffline` (`internal/manager/offline.go`) would
  have to refuse it (and say why) or split the parent pin into its siblings.
  `config.Config` already has what the check needs: compare `IsOffline(path)`
  against an exact match in `OfflinePaths()`.
- **Folder state is an approximation:** a folder counts as partially available as
  soon as one file below it holds data. A cheap "all of it is local" answer would
  need a cached per-folder tally; the bounded scan in `fmstate.Cache.DirHasData` only
  rules out folders that hold nothing at all.
- **README screenshot:** the overlay table describes the icons in words; a small
  screenshot of the four states in Dolphin would say it faster.
- **Headless check for the context menu:** verifying it currently means building a
  throwaway Qt program that loads the plugin the way `KFileItemActions` does
  (`KPluginMetaData::findPlugins("kf6/kfileitemaction")`, then
  `KPluginFactory::instantiatePlugin<KAbstractFileItemActionPlugin>` and calling
  `actions()` with a `KFileItemListProperties`). Worth keeping under
  `internal/dolphin/plugin/` as an optional target if it is needed a third time.
- **Config setters change memory before the save:** `config.Config.set` applies
  the change and then writes the file; when the write fails, memory and disk
  disagree (e.g. `SetLocalDir` returns an error, no restart happens, but status,
  `SyncFolder` and the UI already use the new folder). Fix: apply to a copy
  (deep-copy `OfflinePaths`, `addOffline`/`removeOffline` reuse the backing
  array), save, then commit.
- **Mirror edits during a run wait for the interval:** the inotify watcher drops
  every event while bisync runs (`mirrorRunner.busy`), so bisync's own downloads
  do not queue a redundant run. A user edit made during a long run (first sync)
  is picked up only at the next interval (`mirror_interval_sec`, default 300 s).
  Distinguishing our writes from the user's would need bisync's `-v` output.
- **Pause has two sources of truth in the UI:** `index.html` derives "paused"
  from `state === 'paused'`, the tray from `Manager.Paused()`. Consistent since
  both are set in one critical section, but `/api/status` could expose
  `Paused()` directly.
- **Doc mismatch on "active":** `Status.Active()` is true while paused, starting
  or in error, but `Manager.SyncFolder` and `fmstate.Info.Active` say
  otherwise in their comments. Harmless today (the preview keeper writes to
  Dolphin's own store, not into the mount); fix the docs or the predicate.
- **Mirror recovery needs a current rclone:** auto-recovery runs
  `--resync-mode newer`. The AppImage bundles a rclone that has it; a
  non-AppImage install falling back to an old rclone on `$PATH` would fail every
  recovery run.
