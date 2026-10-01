---
name: tdrive-sync
description: Orientation for the TDrive Sync codebase (Google Drive client for Linux - Go daemon, bundled rclone, tray icon, WebKitGTK settings window, AppImage, Dolphin file-manager indicator). Load this before exploring the tree, changing sync behaviour, touching the file-manager indicator or the KIO plugin, or debugging offline pinning and cache state. Saves re-deriving the architecture and the rclone quirks from scratch.
---

# TDrive Sync

Google Drive client for Linux, modelled on Google's Windows Drive client. A Go
daemon drives a **bundled rclone binary as a child process** — `rclone mount`
(stream mode, files on demand) or `rclone bisync` (mirror mode, full local copy).
Ships as one AppImage. GTK is `dlopen`ed at runtime (cgo, but no link-time GTK
dependency - a `CGO_ENABLED=0` build loses the window), tray via DBus.

**First command of any session:**

```bash
./scripts/check.sh          # build, vet, gofmt, tests + what is installed/running here
./scripts/check.sh --plugin # also compile the Dolphin KIO plugin
```

Read-only. Its final block tells you whether the daemon runs, whether the Drive is
mounted, and whether the Dolphin plugins are installed — usually all the context
you need.

Deeper walkthrough: **[REENTRY.md](../../../REENTRY.md)**. Open work:
**[TODO.md](../../../TODO.md)**. User-facing docs: `README.md`.

## Where things live

| Package | Responsibility |
|---|---|
| `cmd/tdrive-sync` | subcommand dispatch, daemon wiring, CLI commands |
| `internal/app` | app name, desktop id, `Exec()` (AppImage path first) |
| `internal/xdg` | the freedesktop base directories, resolved once for everyone |
| `internal/fsutil` | `WriteAtomic` - use it for every file another program reads |
| `internal/pins` | offline-pin rules (`Has`, `Add`, `Remove`); the C++ plugin mirrors `Has` |
| `internal/config` | YAML config, offline-pin list. Fields are unexported: accessors lock, setters persist in the same critical section |
| `internal/rclone` | binary lookup, OAuth login, mount/bisync arguments, RC client, `lsjson` |
| `internal/manager` | the controller: status store, mode start/stop, pinning, warming, conflicts, login. Modes are `syncMode`s (`streamMode`, `mirrorMode`) in the `modes` registry: each builds its `Runner` and offers optional features (`pinner`, `conflictKeeper`). rclone sits behind `Engine`/`Control`/`Account`, so tests need no binary. Status observers run asynchronously (newest snapshot wins); tests call `status.Flush()`. Timings in `tuning.go` |
| `internal/fmstate` | per-file state for file managers: published snapshot, VFS cache inspection, cache eviction |
| `internal/dolphin` | KDE integration: embedded C++ KIO plugin sources, installer, preview-marker keeper |
| `internal/webui` | loopback HTTP API + embedded single-page frontend (`index.html`); depends on the `Backend` interface, handler tests in `handlers_test.go` |
| `internal/window` | WebKitGTK window via dlopen, desktop entry, autostart, URL opening |
| `internal/tray` | tray icon over DBus StatusNotifierItem |
| `internal/i18n` | German/English catalogs; keep `catalog_de.go` and `catalog_en.go` in sync |

Runtime locations: config `~/.config/tdrive-sync/`, state
`~/.local/state/tdrive-sync/` (`status.json`, `file-manager.json`, `logs/`), cache
`~/.cache/tdrive-sync/vfs/` - note that this directory is itself what rclone gets
as `--cache-dir`, so the data really sits at `~/.cache/tdrive-sync/vfs/vfs/<remote>/…`
with metadata at `…/vfs/vfsMeta/<remote>/…`. Settings API on `127.0.0.1:45677`,
rclone RC one port higher with random per-run credentials; the settings socket
doubles as the single-instance lock and is bound before anything else starts.

## Facts that are expensive to rediscover

- **rclone's `Rs` metadata field cannot tell you whether a file is complete.** It
  is `null` both for a fully downloaded file and for one where 4 KiB were read.
  Completeness comes from the sparse cache file's allocated blocks, with
  `SEEK_HOLE` as tie-breaker (survives filesystem compression).
- **rclone cannot free cached data over its RC API.** `vfs/forget` only drops
  directory listings. Freeing space means deleting the two cache files directly —
  safe, rclone re-downloads on next access. Never delete a file whose metadata
  says `Dirty`: that cache copy is the only copy of those bytes.
- **A KIO plugin cannot ship prebuilt** (must match the loaded KDE Frameworks), so
  its sources are `//go:embed`-ed and compiled on the user's machine. Needs cmake,
  a C++ compiler, `extra-cmake-modules` (provides `FindKF6.cmake` — not optional),
  `kf6-kio-devel`, `qt6-qtbase-devel`.
- **Qt has no default user-local plugin directory**, so the installer extends
  `QT_PLUGIN_PATH` via `~/.config/environment.d/` plus a Plasma startup script;
  already-running programs keep the old environment.
- **The state logic exists twice on purpose**: Go
  (`internal/fmstate/fmstate.go`, CLI + daemon) and C++
  (`internal/dolphin/plugin/tdrivestate.cpp`, Dolphin). Change one, change the
  other, and add a case to `internal/fmstate/testdata/state_cases.json` - that
  one fixture is run against both sides (`go test ./internal/fmstate` and
  `scripts/check.sh --plugin`), so drift fails the check.
- `getOverlays()` must never block and never stat the FUSE mount — a stalled mount
  would freeze the file manager. Resolve from the on-disk cache only.
- Browsing a folder downloads files (thumbnailing reads them), so items turn
  "available offline" just from being looked at. Inherent to rclone mounts.

## Working rules here

- Comments and identifiers English; user-visible strings via `internal/i18n`.
- Everything desktop-related is best-effort: the daemon must keep running without
  a tray host, without WebKitGTK, without a browser.
- `gofmt` before committing. `internal/notify/notify.go` and
  `internal/tray/icon.go` are unformatted in the current tree — pre-existing, do
  not reformat them as a side effect.
- Tests run under `-race` in `scripts/check.sh`. One `*config.Config` and one
  status store are shared by the HTTP handlers, the sync runners and the stats
  poller, so a new unsynchronised field is a real race, not a theoretical one.
- Do not restart the user's running daemon to test something. Use an isolated
  instance (own `XDG_*` directories and `web_port`) or write the state file by
  hand — see REENTRY.md.

## Manual checks

```bash
tdrive-sync file-state <path>          # the state the indicator shows
tdrive-sync offline on|off <path>      # pin / free up space (what the context menu calls)
tdrive-sync dolphin status|install|remove
QT_PLUGIN_PATH=~/.local/lib64/qt6/plugins dolphin ~/GoogleDrive
```
