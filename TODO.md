# TODO

Open work, most useful first. Each item carries enough context to be picked up
cold — see [REENTRY.md](REENTRY.md) for the architecture behind them.

## 1. "Install Dolphin integration" button in the settings window

**Why:** the file-manager indicator is currently only reachable through
`tdrive-sync dolphin install`. In an AppImage aimed at desktop users that is
effectively invisible — nobody opens a terminal to find it.

**Where:**

- `internal/webui/index.html` — a new card, next to the offline-folders card
  (`id="offlineCard"`). Follow the existing pattern: `class="card"`, an `<h2>`, a
  status line, buttons with `data-i18n` attributes, state filled in by a `load…()`
  function that fetches the API.
- `internal/webui/webui.go` — three endpoints in `ListenAndServe`:
  - `get("/api/dolphin", …)` → `dolphin.Status()` as JSON, plus whether the desktop
    is KDE at all (`XDG_CURRENT_DESKTOP` contains `KDE`) so the card can hide
    itself elsewhere.
  - `post("/api/dolphin/install", …)` → run `dolphin.Install` in a goroutine,
    collecting its `logf` output into a buffer on the `Server` (same shape as the
    existing `loginActive`/`loginLines`/`loginErr` fields, which the login flow
    already uses for exactly this "long job with streaming output" case).
  - `post("/api/dolphin/remove", …)` → `dolphin.Remove`.
- `internal/i18n/catalog_de.go` + `catalog_en.go` — keys for the card title, the
  three states (not installed / installed / build requirements missing), the
  buttons and the "restart Dolphin, or log out once" hint.

**Design notes:**

- The build takes a few seconds and can fail for a reason the user must act on
  (missing devel packages). So the card needs to show output, not just a spinner:
  render the collected lines in a `<pre>` like the login box (`class="loginbox"`).
- `dolphin.Install` already returns a helpful error containing the distribution's
  install command (`buildDepsHint`). Show it verbatim; do not swallow it.
- The card must also make clear that a Dolphin restart (or one re-login) is needed
  — `dolphin.Status().OnPluginPath` reports whether the environment is in place.
- Hide the whole card outside KDE rather than offering something that cannot work.

**Done when:** a fresh KDE user can go from "no integration" to visible overlay
icons without ever typing a command, and a user without the devel packages sees
what to install instead of a silent failure.

## 2. Nautilus / GNOME support

`internal/fmstate` is already desktop-agnostic — it publishes
`file-manager.json` and answers `tdrive-sync file-state`. A GNOME sibling of
`internal/dolphin` would be a `nautilus-python` extension implementing
`Nautilus.InfoProvider.update_file_info()` and calling `add_emblem()`, so no
compiler is needed, but it does need the `nautilus-python` package. Port the state
logic a third time or shell out to `tdrive-sync file-state` in batches.

Untested locally: this machine has no Nautilus installed.

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
- **Redundant pins:** pinning a file whose parent folder is already pinned adds a
  second entry to `offline_paths`. Harmless, but `config.AddOffline` could drop
  entries already covered by an ancestor.
- **Folder state is an approximation:** a folder counts as partially available as
  soon as anything below it is cached. A cheap "all of it is local" answer would
  need a cached per-folder tally.
- **README screenshot:** the overlay table describes the icons in words; a small
  screenshot of the four states in Dolphin would say it faster.
- **Headless check for the context menu:** verifying it currently means building a
  throwaway Qt program that loads the plugin the way `KFileItemActions` does
  (`KPluginMetaData::findPlugins("kf6/kfileitemaction")`, then
  `KPluginFactory::instantiatePlugin<KAbstractFileItemActionPlugin>` and calling
  `actions()` with a `KFileItemListProperties`). Worth keeping under
  `internal/dolphin/plugin/` as an optional target if it is needed a third time.
