// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

// catalogEN is the English catalog and the reference set of keys: every other
// catalog is looked up against it, and any key missing elsewhere falls back to
// the message here.
//
// Placeholders are fmt verbs (%s, %d) — the frontend understands the same ones,
// and %% renders a literal percent sign in both.
var catalogEN = map[string]string{
	// -------- sync state (tray tooltip, status line) --------
	"state.disconnected": "Not signed in",
	"state.starting":     "Starting…",
	"state.syncing":      "Syncing…",
	"state.idle":         "Up to date",
	"state.paused":       "Paused",
	"state.error":        "Error",

	// -------- status messages --------
	"status.signin_required":  "Not signed in – please sign in",
	"status.paused":           "Syncing paused",
	"status.signed_out":       "Signed out",
	"status.up_to_date":       "Up to date",
	"status.syncing":          "Syncing…",
	"status.mounting":         "Mounting Google Drive…",
	"status.mount_dir_error":  "Mount folder not usable: %s",
	"status.sync_dir_error":   "Sync folder not usable: %s",
	"status.connection_lost":  "Connection lost – restarting in 5 s…",
	"status.first_sync":       "Initial sync running (this may take a while)…",
	"status.auto_recovery":    "Auto-recovery: full resync…",
	"status.repeated_errors":  "Repeated errors – auto-recovery on the next run",
	"status.sync_error_retry": "Sync error – retrying",

	// -------- desktop notifications --------
	"notify.signed_in_as":     "Signed in as %s",
	"notify.drive_ready":      "Drive ready at %s",
	"notify.repeated_errors":  "Repeated sync errors – automatic recovery follows",
	"notify.sync_error_retry": "Sync error – retrying",
	"notify.update_available": "Update available: %s – install it in the settings window",
	"notify.space_freed":      "Freed %s of disk space",

	// -------- tray menu --------
	"tray.not_signed_in": "Not signed in",
	"tray.open_folder":   "Open folder",
	"tray.sync_now":      "Sync now",
	"tray.pause":         "Pause",
	"tray.resume":        "Resume",
	"tray.settings":      "Settings…",
	"tray.sign_out":      "Sign out",
	"tray.quit":          "Quit",

	// -------- desktop entries (app launcher, autostart) --------
	"desktop.generic_name":      "Cloud sync",
	"desktop.comment":           "Keep Google Drive in sync with your computer",
	"desktop.autostart_comment": "Sync Google Drive at login",

	// -------- updater --------
	"update.not_checked":      "Not checked yet",
	"update.appimage_only":    "Self-update is only available in the AppImage build",
	"update.checking":         "Checking for updates…",
	"update.check_failed":     "Update check failed: %s",
	"update.up_to_date":       "Up to date – no newer version",
	"update.available":        "Update available: %s",
	"update.none_available":   "no update available",
	"update.downloading":      "Downloading update %s…",
	"update.download_failed":  "Download failed: %s",
	"update.replace_failed":   "Replacing the file failed: %s",
	"update.installed":        "Update %s installed – please restart",
	"update.dir_not_writable": "target folder is not writable",
	"update.download_partial": "incomplete download (%d/%d bytes)",
	"update.updater_missing":  "updater not available",
	"update.restart_missing":  "restart not available",

	// -------- errors shown in the UI --------
	"err.invalid_path":      "invalid path",
	"err.invalid_request":   "invalid request",
	"err.not_conflict_file": "not a conflict file",
	"err.unknown_action":    "unknown action: %s",
	"err.login_incomplete":  "the login was not completed",
	"err.sign_out_first":    "please sign out first to change the OAuth client",
	"err.creds_incomplete":  "client ID and client secret must both be provided",
	"err.creds_json_read":   "the JSON could not be read",
	"err.creds_json_fields": "client_id or client_secret missing in the JSON file",

	// -------- settings UI: header and status card --------
	"ui.subtitle":           "Keeps Google Drive in sync with your computer – like the Windows client.",
	"ui.loading":            "Loading…",
	"ui.sync_now":           "Sync now",
	"ui.pause":              "Pause",
	"ui.resume":             "Resume",
	"ui.open_folder":        "Open folder",
	"ui.sign_out":           "Sign out",
	"ui.last_sync":          "Last sync: %s",
	"ui.error_count":        "%d errors",
	"ui.daemon_unreachable": "Daemon not reachable",
	"ui.confirm_sign_out":   "Really sign out?",
	"ui.prompt_local_dir":   "Local folder (full path):",

	// -------- settings UI: update card --------
	"ui.check_updates":          "Check for updates",
	"ui.update_now":             "Update now",
	"ui.restart":                "Restart",
	"ui.release_details":        "Release details ↗",
	"ui.prereleases":            "Prereleases",
	"ui.update_new":             "New: %s",
	"ui.update_appimage_only":   "New version %s available – self-update only in the AppImage build.",
	"ui.update_downloading":     "Downloading update… %d %%",
	"ui.update_searching":       "Searching for updates…",
	"ui.update_checking":        "Checking…",
	"ui.confirm_update":         "Download and install the update?",
	"ui.confirm_update_restart": "Restart now to finish the update?",
	"ui.restarting":             "Restarting…",

	// -------- settings UI: setup / OAuth client --------
	"ui.setup":              "Setup",
	"ui.setup_notice_title": "Your own Google credentials are required",
	"ui.setup_notice_body":  "Google is switching off rclone's shared credentials at the end of 2026. To keep signing in working for good, add your own OAuth client once. It is free and takes a few minutes.",
	"ui.show_guide":         "Show instructions",
	"ui.hide_guide":         "Hide instructions",
	"ui.guide_steps": `<li>Open the <a href="https://console.cloud.google.com/projectcreate" target="_blank" rel="noopener">Google Cloud Console</a> and create a new project (or pick an existing one).</li>
        <li>Enable the <a href="https://console.cloud.google.com/apis/library/drive.googleapis.com" target="_blank" rel="noopener">Google Drive API</a> for the project (“Enable”).</li>
        <li>Set up the <a href="https://console.cloud.google.com/apis/credentials/consent" target="_blank" rel="noopener">OAuth consent screen</a>: user type <b>External</b>, then enter an app name and your email as the support contact.</li>
        <li>Set the publishing status to <b>“In production”</b> (under “Audience”). Otherwise sign-ins expire every 7&nbsp;days. A Google review is not needed for private use.</li>
        <li>Under <a href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener">Credentials</a> → “Create credentials” → “OAuth client ID”, create a client of type <b>Desktop app</b>.</li>
        <li>Download the JSON file (download icon) or copy the <code>client ID</code> and <code>client secret</code>.</li>
        <li>Import the file below – or paste the values manually.</li>`,
	"ui.setup_step1":         "1 · Add your OAuth client",
	"ui.creds_active":        "✓ Your own OAuth client is active",
	"ui.creds_active_id":     "✓ Your own OAuth client is active (%s)",
	"ui.creds_missing":       "⚠ No own client yet – using the default credentials that expire at the end of 2026.",
	"ui.import_file":         "Import file",
	"ui.paste_json":          "…or paste the JSON contents",
	"ui.import_json":         "Import JSON",
	"ui.enter_manually":      "Enter manually",
	"ui.hide_manual":         "Hide manual entry",
	"ui.client_id":           "Client ID",
	"ui.client_secret":       "Client secret",
	"ui.save":                "Save",
	"ui.reset":               "Reset",
	"ui.setup_step2":         "2 · Sign in with Google",
	"ui.setup_step2_hint":    "Then sign in with your Google account. A browser window will open.",
	"ui.sign_in":             "Sign in with Google",
	"ui.alert_pick_json":     "Please choose a JSON file or paste its contents first.",
	"ui.alert_import_ok":     "OAuth client imported. You can sign in now.",
	"ui.alert_import_failed": "Import failed: %s",
	"ui.alert_creds_needed":  "Please provide both the client ID and the client secret.",
	"ui.alert_save_ok":       "OAuth client saved. You can sign in now.",
	"ui.alert_save_failed":   "Saving failed: %s",
	"ui.confirm_clear_creds": "Remove your own OAuth client and use the default credentials again?",
	"ui.error":               "Error: %s",

	// -------- settings UI: login progress --------
	"ui.login_starting":       "Starting sign-in… a browser window should open.",
	"ui.login_open_link":      "If no browser opens, open this link:",
	"ui.login_error_label":    "Error:",
	"ui.login_signed_in":      "Signed in as %s.",
	"ui.login_waiting":        "Waiting for confirmation in the browser…",
	"ui.login_starting_short": "Starting…",

	// -------- settings UI: sync mode --------
	"ui.sync_mode":        "Sync mode",
	"ui.mode_stream":      "Stream (virtual drive)",
	"ui.mode_stream_hint": "All files visible, downloaded on demand. Individual folders can be made available offline.",
	"ui.mode_mirror":      "Mirror (local copy)",
	"ui.mode_mirror_hint": "Complete two-way copy. Everything available offline, uses disk space.",
	"ui.folder":           "Folder:",
	"ui.change":           "Change",
	"ui.autostart":        "Start automatically at login",

	// -------- settings UI: conflicts --------
	"ui.conflict_handling":    "Conflict handling",
	"ui.conflict_auto":        "Automatic",
	"ui.conflict_auto_hint":   "The newer file wins, the cloud wins in case of doubt. The losing copy is kept as a dated backup.",
	"ui.conflict_manual":      "Manual",
	"ui.conflict_manual_hint": "Both versions are kept. You decide per file below which one wins.",
	"ui.open_conflicts":       "Open conflicts",
	"ui.refresh":              "Refresh",
	"ui.no_conflicts":         "No open conflicts.",
	"ui.side_cloud":           "☁️ Cloud",
	"ui.side_local":           "💻 Local",
	"ui.side_backup":          "🗄 Backup",
	"ui.keep_this":            "Keep this one",
	"ui.confirm_keep":         "Keep this version and remove the other conflict copies?",
	"ui.delete":               "Delete",
	"ui.confirm_delete_copy":  "Delete this copy?",
	"ui.conflict_badge_one":   "1 conflict",
	"ui.conflict_badge_many":  "%d conflicts",

	// -------- settings UI: log panel --------
	"ui.log":               "Log",
	"ui.toggle_show":       "▸ show",
	"ui.toggle_hide":       "▾ hide",
	"ui.errors_only":       "errors only",
	"ui.open_log_files":    "Open log files",
	"ui.clear":             "Clear",
	"ui.no_entries":        "No entries",
	"ui.confirm_clear_log": "Reset the log and the error counter?",

	// -------- settings UI: offline browser --------
	"ui.available_offline":      "Available offline",
	"ui.available_offline_hint": "Choose the folders that should be stored locally for good and stay available offline.",
	"ui.drive_root":             "🏠 Drive",
	"ui.empty_folder":           "Empty folder",
	"ui.offline":                "offline",
	"ui.offline_parent":         "offline (parent folder)",

	// -------- settings UI: file-manager integration (KDE/Dolphin) --------
	"ui.dolphin":                "File manager (Dolphin)",
	"ui.dolphin_hint":           "Marks every item in the sync folder with its state and adds “Keep offline” and “Free up space” to the context menu. The plugin is compiled on this computer because it has to match the installed KDE version.",
	"ui.dolphin_not_installed":  "Not installed – Dolphin shows no sync symbols yet.",
	"ui.dolphin_deps_missing":   "⚠ Not installed – the build requirements are missing (see below).",
	"ui.dolphin_installed":      "✓ Installed and active.",
	"ui.dolphin_restart_needed": "✓ Installed – restart Dolphin (close every window) so it loads the plugin. If the symbols stay missing, log out and back in once.",
	"ui.dolphin_install":        "Install integration",
	"ui.dolphin_reinstall":      "Rebuild",
	"ui.dolphin_remove":         "Remove",
	"ui.dolphin_installing":     "Compiling and installing the plugin…",
	"ui.dolphin_removing":       "Removing…",
	"ui.dolphin_failed":         "Failed:",
	"ui.confirm_dolphin_remove": "Remove the Dolphin integration? The sync symbols in the file manager disappear.",

	// -------- settings UI: Dolphin previews in the sync folder --------
	"ui.dolphin_previews":          "Switch previews off in the sync folder (experimental)",
	"ui.dolphin_previews_hint":     "A preview reads the whole file, and reading downloads it – browsing alone would fill your disk. This applies to the sync folder only; Dolphin keeps its previews everywhere else (it is switched to per-folder view properties for that). Experimental: Dolphin reads the setting when it opens a folder, it needs a restart, and the first look into a folder still downloads once.",
	"ui.dolphin_previews_off_done": "Previews in the sync folder are off. Reload an open Dolphin window (F5) or reopen the folder.",
	"ui.dolphin_previews_on_done":  "Previews in the sync folder are on again. Careful: looking at files downloads them.",

	"ui.dolphin_previews_default":        "Also make “no previews” Dolphin's default (experimental)",
	"ui.dolphin_previews_default_hint":   "Needed so that even the first look into a folder downloads nothing: Dolphin applies a folder's own setting only once its view is up. This default covers every folder without a setting of its own – outside the sync folder as well. Individual folders can still switch previews back on.",
	"ui.dolphin_previews_default_done":   "Default set: no previews in folders without a setting of their own. Restart Dolphin once.",
	"ui.dolphin_previews_default_undone": "Default taken back – Dolphin shows previews everywhere again.",
}
