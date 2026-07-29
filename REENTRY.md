# Re-entry guide

Written so that anyone – a person or an AI assistant – can pick this project up
without reading the whole codebase first. Start here, then jump to the files this
document points at.

For the user-facing description (features, installation, configuration) see
[README.md](README.md). Open work is in [TODO.md](TODO.md).

## In three sentences

TDrive Sync is a Google Drive client for Linux, modelled on Google's Windows Drive
client: a background daemon with a tray icon, a native settings window
(WebKitGTK), shipped as a single AppImage. The sync engine is a bundled
[rclone](https://rclone.org) binary, driven as a child process – either
`rclone mount` (stream mode, files on demand) or `rclone bisync` (mirror mode,
full local copy). The Go code owns configuration, state, the local HTTP control
API, the desktop integration and everything the user sees.

## Quick check before changing anything

```bash
./scripts/check.sh            # build, vet, gofmt, tests + live system state
./scripts/check.sh --plugin   # also compile the Dolphin plugin (needs KF6 headers)
```

`check.sh` never changes anything. It ends with a summary of what is installed and
running on this machine, which is usually the context you need.

## Architecture

### Processes

```
  ┌─────────────────────────────────────────────────────────────┐
  │ tdrive-sync run              the daemon, one per user       │
  │                                                             │
  │  manager ──── owns status, starts/stops the active mode      │
  │     │                                                       │
  │     ├── rclone mount   (stream)   ── child process          │
  │     ├── rclone bisync  (mirror)   ── child process          │
  │     ├── inotify watcher (mirror)                            │
  │     └── publishes: status.json, file-manager.json           │
  │                                                             │
  │  webui  ── HTTP on 127.0.0.1:45677 (JSON API + settings page)│
  │  tray   ── DBus StatusNotifierItem                          │
  │  updater ── GitHub releases, replaces the AppImage          │
  └─────────────────────────────────────────────────────────────┘
        │                              │
        │ spawns                       │ reads
        ▼                              ▼
  tdrive-sync window            Dolphin + KIO plugins
  (WebKitGTK, own process)      (overlay icons, context menu)
```

- **Single instance:** `run` first probes `127.0.0.1:<web_port>/api/status`. If
  something answers, it just opens the settings window and exits.
- **The settings window is a separate process** (`tdrive-sync window`) so GTK stays
  on its own main thread and a crash there cannot take the daemon down. It only
  renders the page served by `webui`.
- **rclone is never linked in**, only executed. RC credentials are random per run
  and passed through the environment, never argv.

### Ports and locations

| What | Where |
|---|---|
| Settings API + page | `127.0.0.1:<web_port>` (default 45677) |
| rclone RC API | `127.0.0.1:<web_port>+1`, basic auth, random per run |
| Config | `~/.config/tdrive-sync/config.yaml`, `rclone.conf` |
| State | `~/.local/state/tdrive-sync/`: `status.json`, `file-manager.json`, `logs/` |
| Cache | `~/.cache/tdrive-sync/vfs/` → `vfs/<remote>/…` (data), `vfsMeta/<remote>/…` (metadata), `bisync/` |
| Dolphin plugins | `~/.local/lib64/qt6/plugins/kf6/{overlayicon,kfileitemaction}/` |

### Packages

| Package | Responsibility |
|---|---|
| `cmd/tdrive-sync` | entry point; subcommand dispatch, daemon wiring, CLI commands |
| `internal/config` | YAML config load/save, XDG paths, offline-pin list |
| `internal/rclone` | locate the binary, OAuth login, build mount/bisync arguments, RC client, `lsjson` listing |
| `internal/manager` | the controller: status, mode start/stop, pinning, warming, conflicts, auto-recovery, inotify watcher |
| `internal/fmstate` | per-file state for file managers: published snapshot, VFS cache inspection, cache eviction |
| `internal/dolphin` | KDE integration: embedded KIO plugin sources (C++) plus the installer |
| `internal/webui` | loopback HTTP server, JSON API, embedded single-page frontend |
| `internal/window` | native window via WebKitGTK (dlopen, no dev headers), desktop entry, autostart, URL opening |
| `internal/tray` | tray icon over DBus StatusNotifierItem (no GTK, no cgo) |
| `internal/notify` | desktop notifications over DBus |
| `internal/i18n` | German/English message catalogs plus locale detection |
| `internal/updater` | self-update from GitHub releases |
| `internal/logbuf`, `internal/logfile` | in-memory ring buffer for the UI, day-rotating log file |

### How the file-manager indicator works

This is the least obvious part of the codebase, so here is the whole chain:

1. `manager.broadcast()` → `publishFM()` writes
   `~/.local/state/tdrive-sync/file-manager.json` (`internal/fmstate`): mode,
   sync folder, cache location, remote name, pinned paths, daemon binary. Writes
   that would not change the content are skipped, so watchers stay quiet.
2. The **Dolphin overlay plugin** reads that file once, watches it, and then
   resolves every item it is asked about **entirely from rclone's cache on disk**.
   It never calls the daemon and never touches the FUSE mount:
   `KOverlayIconPlugin::getOverlays()` runs for every visible item and must not
   block, and a stat on a stalled mount would freeze the file manager.
3. A 4-second timer re-checks the items currently on screen and emits
   `overlaysChanged()` for those whose state moved (download finished, folder
   pinned, upload done).
4. The **context-menu plugin** turns clicks into
   `tdrive-sync offline on|off <paths>`, which POSTs to the daemon's local API.

The same logic exists twice, deliberately: in Go
([internal/fmstate/fmstate.go](internal/fmstate/fmstate.go), used by the CLI and
covered by tests) and in C++
([internal/dolphin/plugin/tdrivestate.cpp](internal/dolphin/plugin/tdrivestate.cpp),
used by Dolphin). **Change one, change the other**, and keep
`fmstate_test.go` as the specification of the expected behaviour.

## Hard-won facts – do not re-derive these

- **rclone's `Rs` field is useless for "is it complete?"** The VFS metadata leaves
  `Rs: null` both for a file that was fully downloaded and for one where only
  4 KiB were read. Completeness is therefore decided by how much of the sparse
  cache file is actually allocated (`st_blocks`), with `SEEK_HOLE` as the
  tie-breaker – that also survives filesystems with transparent compression.
- **rclone cannot free cached data over its RC API.** `vfs/forget` only drops
  cached directory listings. Freeing space means deleting
  `vfs/<remote>/<path>` and `vfsMeta/<remote>/<path>` directly. That is safe:
  rclone notices the missing data and downloads it again (verified – checksum
  matches afterwards). Files whose metadata says `Dirty` are never deleted; their
  cache copy is the only copy of those bytes.
- **A KIO plugin cannot ship prebuilt.** It has to match the KDE Frameworks build
  it is loaded into, so the sources travel embedded in the Go binary
  (`//go:embed plugin`) and are compiled on the user's machine into their home
  directory. Needs cmake, a C++ compiler, `extra-cmake-modules`, `kf6-kio-devel`,
  `qt6-qtbase-devel`.
- **Qt only searches `QT_PLUGIN_PATH`.** There is no user-local plugin directory
  that Qt looks into by default, so the installer writes
  `~/.config/environment.d/50-tdrive-sync.conf` plus a Plasma startup script and
  calls `systemctl --user set-environment`. Programs started before that keep the
  old environment – hence "log out and back in once".
- **`extra-cmake-modules` is required**, not optional: `FindKF6.cmake` lives there,
  and KIO's own CMake config pulls in find modules from it.
- **Browsing a folder downloads files.** File managers generate thumbnails, which
  reads files, which caches them. Items turn green just from being looked at.
  That is how an rclone mount behaves, not something the indicator does.
- **The mount's RC API needs auth.** Credentials are random per daemon run and only
  exist in that process's environment, so an outside tool cannot drive it.

## Conventions in this codebase

- Comments and identifiers are English; user-visible strings go through
  `internal/i18n` (`catalog_de.go` / `catalog_en.go` – keep the keys in sync; a
  missing German key falls back to English).
- No cgo. GTK is reached via `dlopen`, the tray via DBus, so the AppImage stays
  portable.
- Everything that can fail on an exotic desktop is best-effort: the daemon keeps
  running without a tray host, without WebKitGTK, without a browser.
- Comments explain *why*, not *what*. Match the density of the surrounding code.
- `gofmt` before committing. `internal/notify/notify.go` and
  `internal/tray/icon.go` are unformatted in the current tree – pre-existing, do
  not fold unrelated reformatting into a feature commit.

## Verifying by hand

```bash
# State of individual files, exactly what the indicator shows
tdrive-sync file-state ~/GoogleDrive/some/file.pdf

# Pin / release from the console (what the context menu calls)
tdrive-sync offline on  ~/GoogleDrive/Documents
tdrive-sync offline off ~/GoogleDrive/Documents

# Dolphin integration
tdrive-sync dolphin status
tdrive-sync dolphin install
QT_PLUGIN_PATH=~/.local/lib64/qt6/plugins dolphin ~/GoogleDrive   # try without touching the session

# A daemon in isolation, leaving the running one alone
XDG_CONFIG_HOME=/tmp/t/config XDG_STATE_HOME=/tmp/t/state XDG_CACHE_HOME=/tmp/t/cache \
  TDRIVE_RCLONE=$(command -v rclone) ./tdrive-sync run
```

The context-menu plugin can be checked headless (no clicking) by loading it the
way Dolphin does – see the probe program described in [TODO.md](TODO.md) if that
is needed again.

## Where to start for common tasks

| Task | Start at |
|---|---|
| Change what the tray or window shows | `internal/tray/menu.go`, `internal/webui/index.html` |
| Add a setting | `internal/config/config.go` → `internal/webui/webui.go` → `index.html` → both i18n catalogs |
| Change sync behaviour / rclone flags | `internal/rclone/rclone.go` (`MountArgs`, `BisyncArgs`) |
| Change mode control, recovery, pinning | `internal/manager/runners.go`, `manager.go` |
| Change the indicator or its states | `internal/fmstate/` **and** `internal/dolphin/plugin/` |
| Add another file manager | `internal/fmstate` is desktop-agnostic; add a sibling of `internal/dolphin` |
| AppImage packaging | `build-appimage.sh`, `packaging/` |
