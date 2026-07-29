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
- **JSON status API:** an always-current `status.json` makes external monitoring
  easy (in addition to the HTTP API on 127.0.0.1).
- **Log with rotation:** daily log files with 7-day retention; old files are
  cleaned up automatically.
- **Autostart at login:** on by default (XDG autostart), switchable in the UI.
- **Automatic updates:** checks the GitHub releases on start and periodically,
  reports new versions and updates the AppImage at the press of a button.
  Optionally **prereleases** are considered as well. The current version is
  shown in the settings window.
- **Tray icon** with a status colour (green = up to date, blue = syncing,
  red = error) and a context menu.
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

Starting the app again while the service is already running simply opens the
settings.

### Autostart

The service registers itself for start at login **automatically**: on the first
start an XDG autostart entry is created at
`~/.config/autostart/tdrive-sync.desktop` (pointing at the AppImage / program
path). It can be switched off and on again at any time via the
**“Start automatically at login”** toggle in the settings window.

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
```

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

## License

The app code is under the MIT license. The bundled rclone is under the MIT
license (© Nick Craig-Wood).
