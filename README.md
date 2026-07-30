# TDrive Sync

A Google Drive sync client for Linux – functionally modelled on Google's Windows
Drive client. It runs as a background service with a **tray icon**, provides a
**native settings window** (WebKitGTK, no browser) and ships as an **AppImage**
(a single file, no installation needed).

The sync engine is the proven [rclone](https://rclone.org), which is bundled
inside the AppImage.

The interface follows the **system locale**: German for a German locale, English
for everything else (including an unset locale).

## Features

- **Two sync modes, switchable at any time:**
  - **Stream (virtual drive):** the whole Drive shows up as a folder, files are
    downloaded on demand. Saves disk space.
  - **Mirror (local copy):** complete two-way synchronisation of one folder –
    everything available offline.
- **Bidirectional synchronisation:** changes flow both ways – local → Drive and
  Drive → local (mirror mode, `rclone bisync`).
- **Real-time watching of local changes:** a native inotify watcher notices
  local changes immediately and kicks off the reconciliation right away
  (debounced) instead of waiting for the interval timer.
- **Periodic remote polling:** a timer reconciles regularly and so also picks up
  changes made directly in Google Drive (5 min by default, configurable).
- **Conflict handling – automatic or manual (selectable in the UI):**
  - **Automatic:** the newer file wins; the cloud wins in case of doubt. The
    losing copy is kept as a **dated backup**.
  - **Manual** (default): both versions are kept; the UI lists every open
    conflict and you decide per file (“Keep this one” / “Delete”). A counter
    badge on the status line shows open conflicts.
- **Automatic recovery (auto-recovery):** after several consecutive failed
  attempts a full resync (`--resync`) is triggered automatically.
- **Stale lock detection:** lock files left behind by crashed or killed runs are
  detected and removed automatically.
- **Make available offline:** in stream mode individual folders can be selected
  to be kept locally for good and stay usable offline – like “Available offline”
  in the Windows client.
- **Indicator in the file manager (KDE/Dolphin):** every item in the sync folder
  shows whether it is online only, downloaded, or pinned offline – plus context
  menu entries for “Always keep offline” and “Free up space”, as in the Windows
  OneDrive client. See [File manager indicator](#file-manager-indicator-kdedolphin).
- **JSON status API:** an always-current `status.json` makes external monitoring
  easy (in addition to the HTTP API on 127.0.0.1).
- **Log with rotation:** daily log files with 7-day retention; old files are
  cleaned up automatically.
- **Autostart at login:** on by default (XDG autostart), switchable in the UI.
- **Automatic updates:** checks the GitHub releases on start and periodically,
  reports new versions and updates the AppImage at the press of a button.
  Optionally **prereleases** are considered as well. The current version is
  shown in the settings window.
- **Tray icon** – the app logo itself, which shows the state by how it looks:
  full colour when up to date, turning while syncing or starting, blinking red on
  an error, grey while paused or signed out. Plus a context menu.
- **Native settings window** with a file browser for picking the offline folders
  (uses the system's WebKitGTK, no web browser).
- **Links open in your own browser:** the sign-in link and the setup guide's
  links are handed to the system browser (`$BROWSER`, then `xdg-open` / `gio`,
  then the installed browsers), never loaded inside the settings window.
- **Desktop notifications** on important events.
- **Automatic restart** of the mount / renewed reconciliation when the
  connection drops.
- **Localised interface:** German and English, chosen from the system locale.

## Building the AppImage

Requirements: Go ≥ 1.23, `curl`, `unzip`. rclone and appimagetool are downloaded
automatically.

```bash
./build-appimage.sh
```

Result: `dist/TDrive_Sync-x86_64.AppImage`

Environment variables (optional):

| Variable       | Purpose                                          |
|----------------|--------------------------------------------------|
| `GO`           | path to a specific Go installation               |
| `RCLONE_BIN`   | use an existing rclone binary instead of downloading |
| `APPIMAGETOOL` | use an existing appimagetool                     |

## Usage

```bash
chmod +x TDrive_Sync-x86_64.AppImage
./TDrive_Sync-x86_64.AppImage
```

The settings window opens on the first start. Click **“Sign in with Google”** –
the default browser opens once for the Google sign-in itself (OAuth). Then pick a
mode and, in stream mode, optionally mark folders as “offline”.

If no browser appears, the login panel still shows the sign-in link to open by
hand, and `tdrive-sync open-url https://example.com` tells you which handler the
app tried and why it failed.

Further commands:

```bash
./TDrive_Sync-x86_64.AppImage login    # sign in from the console (headless)
./TDrive_Sync-x86_64.AppImage open     # open the settings window
./TDrive_Sync-x86_64.AppImage status   # print the status
```

Offline availability can also be steered from the console (the same paths the
file manager's context menu uses):

```bash
tdrive-sync file-state ~/GoogleDrive/Documents/report.pdf   # print the state
tdrive-sync offline on  ~/GoogleDrive/Documents             # keep offline
tdrive-sync offline off ~/GoogleDrive/Documents             # free up space
```

Starting the app again while the service is already running simply opens the
settings.

### Autostart

The service registers itself for start at login **automatically**: on the first
start an XDG autostart entry is created at
`~/.config/autostart/tdrive-sync.desktop` (pointing at the AppImage / program
path). It can be switched off and on again at any time via the
**“Start automatically at login”** toggle in the settings window.

## File manager indicator (KDE/Dolphin)

Just like the Windows OneDrive client, Dolphin can mark every item in the sync
folder with its availability:

| Overlay                      | Meaning                                                           |
|------------------------------|-------------------------------------------------------------------|
| cloud with a download arrow  | **Online only** – opening it fetches it from Drive                 |
| orange circle                | **Partly** downloaded (folder: something inside it is local)       |
| green check mark             | **Available offline** – there is a complete local copy             |
| star                         | **Always kept offline** – pinned, so it is kept local for good     |
| sync arrows                  | pinned download running, or local changes still going up           |

The context menu of items inside the sync folder gains up to two entries:
**“Always keep offline”** and **“Free up space (keep online only)”**. Freeing
space really deletes the local copy – the file stays visible and is downloaded
again when it is next opened.

In mirror mode everything is a real local copy anyway, so every item simply shows
the green check mark and the context menu stays out of the way.

### Installing it

Dolphin's overlay icons come from a KIO plugin, and such a plugin has to be
compiled against the KDE Frameworks build it is loaded into – a prebuilt binary
inside the AppImage would break on the next Plasma update. It is therefore
compiled once, on your machine, into your home directory.

On KDE the settings window has a **“File manager (Dolphin)”** card for this:
“Install integration” compiles the plugin and shows what it is doing, including
what is still missing if the build cannot run. The same thing from a terminal:

```bash
tdrive-sync dolphin install   # compile and install
tdrive-sync dolphin status    # is everything in place?
tdrive-sync dolphin remove    # uninstall again
```

The build needs cmake, a C++ compiler and the Qt 6 / KDE Frameworks 6 headers.
If something is missing, the matching command for your distribution is printed;
on Fedora/Nobara it is

```bash
sudo dnf install cmake gcc-c++ extra-cmake-modules kf6-kio-devel qt6-qtbase-devel
```

The plugins land in `~/.local/lib64/qt6/plugins/kf6/{overlayicon,kfileitemaction}`
and `QT_PLUGIN_PATH` is extended via `~/.config/environment.d/50-tdrive-sync.conf`
(plus a Plasma startup script). Nothing outside your home directory is touched,
so no root password is needed. Dolphin picks the plugin up the next time it
starts; if the indicators stay missing, log out and back in once – or try it
straight away without touching the session:

```bash
QT_PLUGIN_PATH=~/.local/lib64/qt6/plugins dolphin ~/GoogleDrive
```

### Previews in the sync folder (experimental)

This part is **experimental**: it works, but not dependably. Dolphin reads the
setting only when it opens a folder, so it has to be restarted completely (a new
window reuses the running process and its old configuration), the first look into a
folder still runs one round of previews unless the second switch below is on, and a
folder created after the last refresh previews once before its marker exists.

A preview is made by reading the file, and reading through the mount downloads it
– so browsing with previews on slowly pulls the whole Drive onto the disk, and
“free up space” is undone within seconds while the folder is on screen. The
settings card therefore has a switch, **“Switch previews off in the sync
folder”**, or from a terminal:

```bash
tdrive-sync dolphin previews off   # and `on` to allow them again
```

It affects the sync folder only – everywhere else Dolphin keeps its previews.
Three things happen for it:

- `GlobalViewProps=false` in `dolphinrc`, because Dolphin ignores per-folder
  settings while it uses common properties for all folders. The previous value is
  remembered and restored when the switch goes off again.
- `PreviewsShown=false` in Dolphin's own view-properties store
  (`~/.local/share/dolphin/view_properties/local/…`) – never a file inside the
  Drive, so nothing is uploaded.
- the same marker for **every folder in the Drive**, refreshed by the service
  every 30 minutes. Dolphin keeps view properties per folder and does not inherit
  them, so without this a subfolder would preview – and download – its content
  again.

Dolphin reads the setting when it opens a folder, so restart it once (`kquitapp6
dolphin`) after switching.

A folder's own setting is applied only once its view is up, so the **first** look
into a folder still runs one round of previews. The second switch,
**“Also make ‘no previews’ Dolphin's default”**, stops that too – it writes
`PreviewsShown=false` into Dolphin's global view properties, which covers every
folder that has no setting of its own, outside the sync folder as well. It is
therefore off by default and separate:

```bash
tdrive-sync dolphin previews-default off   # `on` restores the previous default
```

Individual folders can still switch previews back on; taking the default back
restores exactly what Dolphin had before (including removing the file again if it
did not exist).

### How the state is worked out

An overlay is looked up for *every* visible item and must not block, and a stat on
a stalled FUSE mount would freeze the file manager. So the plugin asks nobody:
the daemon publishes `~/.local/state/tdrive-sync/file-manager.json` (mode, sync
folder, cache location, pinned paths) and the plugin reads each item's state
straight from rclone's VFS cache on disk:

- no cache file at all → online only,
- sparse cache file with only parts allocated → partly downloaded,
- fully allocated, or no hole before the end of the file → available offline,
- `Dirty` in rclone's metadata → local changes are still on their way up.

Folders are approximated: they count as partly available as soon as anything
below them is cached, because deciding “all of it is local” would mean walking
the whole subtree on every single lookup. Changes that happen behind the file
manager's back – a download finishing, a folder being pinned – are noticed within
a few seconds and the overlay is refreshed.

## Automatic updates

The AppImage can update itself. The service checks the project's
[GitHub releases](https://github.com/tokajer/tdrive-sync/releases) **on start**
and periodically afterwards:

- If a newer version is available, the settings window shows a notice with an
  **“Update now”** button, plus a desktop notification.
- Updating downloads the matching `*.AppImage` asset and **atomically replaces**
  the running file; a click on **“Restart”** afterwards is all it takes.
- The **“Prereleases”** option includes releases marked as *prerelease*.

The **displayed version** is set at build time from the git tag
(`-ldflags -X main.version=…`; the tagged release in the GitHub build). Local
builds without a tag report **`local-dev-build`** and offer no self-update (only
the AppImage build can replace itself).

Switching it off: `update_check_disabled: true` in `config.yaml`.

## Runtime requirements

- **FUSE 3** (`fusermount3`) for stream mode – preinstalled on most
  distributions.
- **WebKitGTK** (`libwebkit2gtk-4.1`) for the settings window – present on most
  desktops. Without it the service keeps running; the window cannot be opened
  (but the HTTP control on 127.0.0.1 stays available).
- **Tray icon:** a *StatusNotifierItem* host is required. KDE Plasma, XFCE,
  Cinnamon and others ship one. On **GNOME** the *AppIndicator and
  KStatusNotifierItem Support* extension is needed. Without a tray host the
  service still runs – reach the settings window via `tdrive-sync open`.

## Your own Google OAuth credentials

Google is switching off rclone's shared Drive credentials at the end of 2026, so
add your own OAuth client once — the settings window walks through it (step 1 of
the setup: import the JSON from the Google Cloud console, or paste client ID and
client secret). Alternatively, put the values into
`~/.config/tdrive-sync/config.yaml` directly:

```yaml
google:
  client_id: "YOUR_CLIENT_ID"
  client_secret: "YOUR_CLIENT_SECRET"
```

Sign in once afterwards. Own credentials are created in the
[Google Cloud Console](https://console.cloud.google.com) (OAuth client of type
“Desktop app”, with the Drive API enabled).

## Configuration

All settings live under `~/.config/tdrive-sync/`:

- `config.yaml` – app settings (mode, folder, offline paths, interval, conflict
  mode, autostart, port, OAuth)
- `rclone.conf` – the rclone remote including the OAuth token

Important fields in `config.yaml`:

```yaml
sync_mode: stream            # "stream" or "mirror"
conflict_mode: manual        # "manual" (default) or "auto"
mirror_interval_sec: 300     # polling interval in mirror mode (seconds)
autostart_disabled: false    # true = no autostart at login
update_prerelease: false     # true = offer prereleases as well
update_check_disabled: false # true = no automatic update check
```

Runtime data lives under `~/.local/state/tdrive-sync/` (or `$XDG_STATE_HOME`):

- `status.json` – current status for monitoring (JSON status API)
- `logs/tdrive-sync-YYYY-MM-DD.log` – daily log files, 7-day retention

The VFS / bisync cache lives under `~/.cache/tdrive-sync/` (including `bisync/`
with the work and lock files).

## Architecture

```
cmd/tdrive-sync      entry point (daemon, CLI, single instance)
internal/config      loading/saving the YAML configuration
internal/rclone      rclone wrapper (login, mount, bisync, RC API, listing)
internal/manager     sync manager: mode control, status, offline pinning,
                     inotify watcher, auto-recovery, conflict resolution
internal/fmstate     per-file state for the file manager (published snapshot,
                     VFS cache inspection, freeing cached copies)
internal/dolphin     KDE integration: embedded KIO plugin sources + installer
internal/i18n        message catalogs (German/English) + locale detection
internal/webui       local control server (127.0.0.1) + embedded interface
internal/window      native settings window (WebKitGTK via dlopen, no dev headers)
internal/tray        tray icon via DBus StatusNotifierItem (no GTK/cgo)
internal/notify      desktop notifications via DBus
internal/updater     self-update via GitHub releases (check, download, replace)
internal/logbuf      in-memory ring buffer for the log shown in the UI
internal/logfile     day-rotating log file with 7-day retention
packaging/           AppRun, desktop file, icon
build-appimage.sh    build script
scripts/check.sh     build + vet + tests, then report the state of this machine
```

Working on the code? [REENTRY.md](REENTRY.md) walks through the process model, the
data flow behind the file-manager indicator and the rclone behaviour worth knowing
in advance; [TODO.md](TODO.md) lists the open work.

**The sync modes in detail:**

- *Stream* starts `rclone mount` with a full VFS cache. Folders marked as
  “offline” are read completely through the mount so they land in the cache and
  stay available without a connection.
- *Mirror* uses `rclone bisync` for true two-way synchronisation. It reconciles
  on the configurable interval (5 min by default), at the press of a button and
  **immediately on local changes** (inotify watcher). Conflicts are resolved
  either automatically (newer wins, cloud in case of doubt, dated backup) or
  manually in the UI, depending on the setting. After several failed attempts
  the service forces a full `--resync` (auto-recovery); stale locks are cleaned
  up before every run.

**Language selection:** the language is resolved once at start from
`LC_ALL` / `LC_MESSAGES` / `LANG` / `LANGUAGE`. Anything that is not German
falls back to English. Log files and CLI output stay English regardless, since
they are diagnostic.

## Notes / known limits

- **Offline pinning (stream):** pinned folders are guaranteed to be available
  offline as long as the VFS cache is not cleared manually. If you want
  *everything* guaranteed offline, use **mirror mode**. Switching is possible at
  any time.
- On the **first** mirror reconciliation rclone performs a full `--resync`; that
  can take a while on large Drives.
- Google Docs/Sheets/Slides show up as link files (as usual with rclone).
- **Browsing downloads files:** with previews switched on, the file manager reads
  files to make thumbnails, so items turn “available offline” simply from being
  looked at. That is how an rclone mount works, not something the indicator does –
  switch previews off for the sync folder (see above) to stop it.
- **Freeing space** removes rclone's cached copy directly, because rclone has no
  remote-control command for it (`vfs/forget` only drops directory listings).
  rclone notices the missing data and downloads it again on the next access.
- The file-manager indicator currently covers **Dolphin/KDE** only. GNOME's
  Nautilus would need its own extension; the state itself is desktop-agnostic
  (`file-manager.json` plus `tdrive-sync file-state`), so adding one is
  self-contained work.

## License

Copyright © 2026 tokajer

The app code is free software under the **GNU General Public License, version 3
or (at your option) any later version** – see [LICENSE](LICENSE). It is
distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY;
without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR
PURPOSE.

The complete source code is at <https://github.com/tokajer/tdrive-sync>.

The AppImage additionally bundles, unmodified and as a separate work:

- [rclone](https://rclone.org) – MIT license, © Nick Craig-Wood.
