// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

// catalogDE is the German catalog. It mirrors the keys of catalogEN; anything
// missing here falls back to the English message.
var catalogDE = map[string]string{
	// -------- sync state (tray tooltip, status line) --------
	"state.disconnected": "Nicht angemeldet",
	"state.starting":     "Wird gestartet…",
	"state.syncing":      "Synchronisiere…",
	"state.idle":         "Auf dem neuesten Stand",
	"state.paused":       "Pausiert",
	"state.error":        "Fehler",

	// -------- status messages --------
	"status.signin_required":  "Nicht angemeldet – bitte anmelden",
	"status.paused":           "Synchronisierung pausiert",
	"status.signed_out":       "Abgemeldet",
	"status.up_to_date":       "Auf dem neuesten Stand",
	"status.syncing":          "Synchronisiere…",
	"status.mounting":         "Google Drive wird eingebunden…",
	"status.mount_dir_error":  "Mount-Ordner nicht nutzbar: %s",
	"status.sync_dir_error":   "Sync-Ordner nicht nutzbar: %s",
	"status.connection_lost":  "Verbindung unterbrochen – Neustart in 5 s…",
	"status.first_sync":       "Erstabgleich läuft (kann dauern)…",
	"status.auto_recovery":    "Auto-Wiederherstellung: vollständiger Neuabgleich…",
	"status.repeated_errors":  "Wiederholte Fehler – Auto-Wiederherstellung beim nächsten Lauf",
	"status.sync_error_retry": "Synchronisierungsfehler – neuer Versuch folgt",

	// -------- desktop notifications --------
	"notify.signed_in_as":     "Angemeldet als %s",
	"notify.drive_ready":      "Laufwerk bereit unter %s",
	"notify.repeated_errors":  "Wiederholte Synchronisierungsfehler – automatische Wiederherstellung folgt",
	"notify.sync_error_retry": "Synchronisierungsfehler – wird erneut versucht",
	"notify.update_available": "Update verfügbar: %s – im Einstellungs-Fenster installieren",
	"notify.space_freed":      "%s Speicherplatz freigegeben",

	// -------- tray menu --------
	"tray.not_signed_in": "Nicht angemeldet",
	"tray.open_folder":   "Ordner öffnen",
	"tray.sync_now":      "Jetzt synchronisieren",
	"tray.pause":         "Pausieren",
	"tray.resume":        "Fortsetzen",
	"tray.settings":      "Einstellungen…",
	"tray.sign_out":      "Abmelden",
	"tray.quit":          "Beenden",

	// -------- desktop entries (app launcher, autostart) --------
	"desktop.generic_name":      "Cloud-Synchronisation",
	"desktop.comment":           "Google Drive mit dem Rechner synchronisieren",
	"desktop.autostart_comment": "Google Drive beim Anmelden synchronisieren",

	// -------- updater --------
	"update.not_checked":      "Noch nicht geprüft",
	"update.appimage_only":    "Selbstupdate nur in der AppImage-Version verfügbar",
	"update.checking":         "Suche nach Updates…",
	"update.check_failed":     "Update-Prüfung fehlgeschlagen: %s",
	"update.up_to_date":       "Aktuell – keine neuere Version",
	"update.available":        "Update verfügbar: %s",
	"update.none_available":   "kein Update verfügbar",
	"update.downloading":      "Lade Update %s…",
	"update.download_failed":  "Download fehlgeschlagen: %s",
	"update.replace_failed":   "Ersetzen fehlgeschlagen: %s",
	"update.installed":        "Update %s installiert – bitte neu starten",
	"update.dir_not_writable": "Zielordner nicht beschreibbar",
	"update.download_partial": "unvollständiger Download (%d/%d Bytes)",
	"update.updater_missing":  "Updater nicht verfügbar",
	"update.restart_missing":  "Neustart nicht verfügbar",

	// -------- errors shown in the UI --------
	"err.invalid_path":      "ungültiger Pfad",
	"err.invalid_request":   "ungültige Anfrage",
	"err.not_conflict_file": "keine Konfliktdatei",
	"err.unknown_action":    "unbekannte Aktion: %s",
	"err.login_incomplete":  "die Anmeldung wurde nicht abgeschlossen",
	"err.sign_out_first":    "bitte zuerst abmelden, um den OAuth-Client zu ändern",
	"err.creds_incomplete":  "Client-ID und Client-Secret müssen beide angegeben werden",
	"err.creds_json_read":   "die JSON-Datei konnte nicht gelesen werden",
	"err.creds_json_fields": "client_id oder client_secret fehlen in der JSON-Datei",

	// -------- settings UI: header and status card --------
	"ui.subtitle":           "Synchronisiert Google Drive mit deinem Rechner – wie der Windows-Client.",
	"ui.loading":            "Wird geladen…",
	"ui.sync_now":           "Jetzt synchronisieren",
	"ui.pause":              "Pausieren",
	"ui.resume":             "Fortsetzen",
	"ui.open_folder":        "Ordner öffnen",
	"ui.sign_out":           "Abmelden",
	"ui.last_sync":          "Letzter Abgleich: %s",
	"ui.error_count":        "%d Fehler",
	"ui.daemon_unreachable": "Daemon nicht erreichbar",
	"ui.confirm_sign_out":   "Wirklich abmelden?",
	"ui.prompt_local_dir":   "Lokaler Ordner (voller Pfad):",

	// -------- settings UI: update card --------
	"ui.check_updates":          "Nach Updates suchen",
	"ui.update_now":             "Jetzt aktualisieren",
	"ui.restart":                "Neu starten",
	"ui.release_details":        "Release-Details ↗",
	"ui.prereleases":            "Vorabversionen",
	"ui.update_new":             "Neu: %s",
	"ui.update_appimage_only":   "Neue Version %s verfügbar – Selbstupdate nur in der AppImage-Version.",
	"ui.update_downloading":     "Lade Update… %d %%",
	"ui.update_searching":       "Suche nach Updates…",
	"ui.update_checking":        "Prüfe…",
	"ui.confirm_update":         "Update herunterladen und installieren?",
	"ui.confirm_update_restart": "Zum Abschließen des Updates jetzt neu starten?",
	"ui.restarting":             "Neustart…",

	// -------- settings UI: setup / OAuth client --------
	"ui.setup":              "Einrichtung",
	"ui.setup_notice_title": "Eigener Google-Zugang erforderlich",
	"ui.setup_notice_body":  "Google schaltet die gemeinsamen Zugangsdaten von rclone Ende 2026 ab. Damit die Anmeldung dauerhaft funktioniert, hinterlege einmalig deinen eigenen OAuth-Client. Das ist kostenlos und dauert wenige Minuten.",
	"ui.show_guide":         "Anleitung anzeigen",
	"ui.hide_guide":         "Anleitung ausblenden",
	"ui.guide_steps": `<li>Öffne die <a href="https://console.cloud.google.com/projectcreate" target="_blank" rel="noopener">Google Cloud Console</a> und lege ein neues Projekt an (oder wähle ein bestehendes).</li>
        <li>Aktiviere die <a href="https://console.cloud.google.com/apis/library/drive.googleapis.com" target="_blank" rel="noopener">Google Drive API</a> für das Projekt („Aktivieren“).</li>
        <li>Richte den <a href="https://console.cloud.google.com/apis/credentials/consent" target="_blank" rel="noopener">OAuth-Zustimmungsbildschirm</a> ein: Nutzertyp <b>Extern</b>, App-Name und deine E-Mail als Support-Kontakt eintragen.</li>
        <li>Setze den Veröffentlichungsstatus auf <b>„In Produktion“</b> (unter „Zielgruppe“). Sonst laufen die Anmeldungen alle 7&nbsp;Tage ab. Eine Google-Prüfung ist für den privaten Gebrauch nicht nötig.</li>
        <li>Erstelle unter <a href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener">Anmeldedaten</a> → „Anmeldedaten erstellen“ → „OAuth-Client-ID“ einen Client vom Typ <b>Desktop-App</b>.</li>
        <li>Lade die JSON-Datei herunter (Download-Symbol) oder kopiere <code>Client-ID</code> und <code>Client-Schlüssel</code>.</li>
        <li>Importiere die Datei unten – oder füge die Werte manuell ein.</li>`,
	"ui.setup_step1":         "1 · OAuth-Client hinterlegen",
	"ui.creds_active":        "✓ Eigener OAuth-Client aktiv",
	"ui.creds_active_id":     "✓ Eigener OAuth-Client aktiv (%s)",
	"ui.creds_missing":       "⚠ Noch kein eigener Client – es werden die Ende 2026 auslaufenden Standard-Zugangsdaten verwendet.",
	"ui.import_file":         "Datei importieren",
	"ui.paste_json":          "…oder JSON-Inhalt einfügen",
	"ui.import_json":         "JSON importieren",
	"ui.enter_manually":      "Manuell eingeben",
	"ui.hide_manual":         "Manuell ausblenden",
	"ui.client_id":           "Client-ID",
	"ui.client_secret":       "Client-Secret",
	"ui.save":                "Speichern",
	"ui.reset":               "Zurücksetzen",
	"ui.setup_step2":         "2 · Bei Google anmelden",
	"ui.setup_step2_hint":    "Danach mit deinem Google-Konto anmelden. Es öffnet sich ein Browserfenster.",
	"ui.sign_in":             "Bei Google anmelden",
	"ui.alert_pick_json":     "Bitte zuerst eine JSON-Datei wählen oder den Inhalt einfügen.",
	"ui.alert_import_ok":     "OAuth-Client importiert. Du kannst dich jetzt anmelden.",
	"ui.alert_import_failed": "Import fehlgeschlagen: %s",
	"ui.alert_creds_needed":  "Bitte Client-ID und Client-Secret angeben.",
	"ui.alert_save_ok":       "OAuth-Client gespeichert. Du kannst dich jetzt anmelden.",
	"ui.alert_save_failed":   "Speichern fehlgeschlagen: %s",
	"ui.confirm_clear_creds": "Eigenen OAuth-Client entfernen und wieder die Standard-Zugangsdaten verwenden?",
	"ui.error":               "Fehler: %s",

	// -------- settings UI: login progress --------
	"ui.login_starting":       "Starte Anmeldung… ein Browserfenster sollte sich öffnen.",
	"ui.login_open_link":      "Falls sich kein Browser öffnet, öffne diesen Link:",
	"ui.login_error_label":    "Fehler:",
	"ui.login_signed_in":      "Angemeldet als %s.",
	"ui.login_waiting":        "Warte auf Bestätigung im Browser…",
	"ui.login_starting_short": "Starte…",

	// -------- settings UI: sync mode --------
	"ui.sync_mode":        "Sync-Modus",
	"ui.mode_stream":      "Stream (virtuelles Laufwerk)",
	"ui.mode_stream_hint": "Alle Dateien sichtbar, werden bei Bedarf geladen. Einzelne Ordner offline verfügbar machen.",
	"ui.mode_mirror":      "Mirror (lokale Kopie)",
	"ui.mode_mirror_hint": "Vollständige Zwei-Wege-Kopie. Alles offline verfügbar, belegt Speicher.",
	"ui.folder":           "Ordner:",
	"ui.change":           "Ändern",
	"ui.autostart":        "Beim Anmelden automatisch starten",

	// -------- settings UI: conflicts --------
	"ui.conflict_handling":    "Konfliktbehandlung",
	"ui.conflict_auto":        "Automatisch",
	"ui.conflict_auto_hint":   "Neuere Datei gewinnt, im Zweifel die Cloud. Die unterlegene Kopie bleibt als datierte Sicherung erhalten.",
	"ui.conflict_manual":      "Manuell",
	"ui.conflict_manual_hint": "Beide Versionen bleiben erhalten. Du entscheidest unten pro Datei, welche gewinnt.",
	"ui.open_conflicts":       "Offene Konflikte",
	"ui.refresh":              "Aktualisieren",
	"ui.no_conflicts":         "Keine offenen Konflikte.",
	"ui.side_cloud":           "☁️ Cloud",
	"ui.side_local":           "💻 Lokal",
	"ui.side_backup":          "🗄 Sicherung",
	"ui.keep_this":            "Diese behalten",
	"ui.confirm_keep":         "Diese Version behalten und die anderen Konfliktkopien entfernen?",
	"ui.delete":               "Löschen",
	"ui.confirm_delete_copy":  "Diese Kopie löschen?",
	"ui.conflict_badge_one":   "1 Konflikt",
	"ui.conflict_badge_many":  "%d Konflikte",

	// -------- settings UI: log panel --------
	"ui.log":               "Protokoll",
	"ui.toggle_show":       "▸ anzeigen",
	"ui.toggle_hide":       "▾ ausblenden",
	"ui.errors_only":       "nur Fehler",
	"ui.open_log_files":    "Logdateien öffnen",
	"ui.clear":             "Löschen",
	"ui.no_entries":        "Keine Einträge",
	"ui.confirm_clear_log": "Protokoll und Fehlerzähler zurücksetzen?",

	// -------- settings UI: offline browser --------
	"ui.available_offline":      "Offline verfügbar",
	"ui.available_offline_hint": "Wähle Ordner, die dauerhaft lokal gespeichert und offline verfügbar bleiben sollen.",
	"ui.drive_root":             "🏠 Drive",
	"ui.empty_folder":           "Leerer Ordner",
	"ui.offline":                "offline",
	"ui.offline_parent":         "offline (übergeordneter Ordner)",

	// -------- settings UI: file-manager integration (KDE/Dolphin) --------
	"ui.dolphin":                "Dateimanager (Dolphin)",
	"ui.dolphin_hint":           "Markiert jedes Element im Sync-Ordner mit seinem Zustand und ergänzt „Offline behalten“ und „Speicherplatz freigeben“ im Kontextmenü. Das Plugin wird auf diesem Rechner kompiliert, weil es zur installierten KDE-Version passen muss.",
	"ui.dolphin_not_installed":  "Nicht installiert – Dolphin zeigt noch keine Sync-Symbole.",
	"ui.dolphin_deps_missing":   "⚠ Nicht installiert – die Build-Voraussetzungen fehlen (siehe unten).",
	"ui.dolphin_installed":      "✓ Installiert und aktiv.",
	"ui.dolphin_restart_needed": "✓ Installiert – Dolphin neu starten (alle Fenster schließen), damit das Plugin geladen wird. Fehlen die Symbole weiterhin, einmal ab- und wieder anmelden.",
	"ui.dolphin_install":        "Integration installieren",
	"ui.dolphin_reinstall":      "Neu kompilieren",
	"ui.dolphin_remove":         "Entfernen",
	"ui.dolphin_installing":     "Plugin wird kompiliert und installiert…",
	"ui.dolphin_removing":       "Wird entfernt…",
	"ui.dolphin_failed":         "Fehlgeschlagen:",
	"ui.confirm_dolphin_remove": "Dolphin-Integration entfernen? Die Sync-Symbole im Dateimanager verschwinden dann.",

	// -------- settings UI: Dolphin previews in the sync folder --------
	"ui.dolphin_previews":          "Vorschaubilder im Sync-Ordner abschalten (experimentell)",
	"ui.dolphin_previews_hint":     "Eine Vorschau liest die ganze Datei, und Lesen heißt Herunterladen – schon Blättern würde die Festplatte füllen. Gilt nur für den Sync-Ordner; überall sonst behält Dolphin seine Vorschauen (dafür wird es auf ordnerweise Ansichts-Eigenschaften umgestellt). Experimentell: Dolphin liest die Einstellung erst beim Öffnen eines Ordners, ein Neustart von Dolphin ist nötig, und der erste Blick in einen Ordner lädt trotzdem einmal.",
	"ui.dolphin_previews_off_done": "Vorschaubilder im Sync-Ordner sind aus. Ein offenes Dolphin-Fenster einmal neu laden (F5) oder den Ordner neu öffnen.",
	"ui.dolphin_previews_on_done":  "Vorschaubilder im Sync-Ordner sind wieder an. Achtung: Ansehen lädt die Dateien herunter.",

	"ui.dolphin_previews_default":        "Vorschau auch als Dolphin-Vorgabe abschalten (experimentell)",
	"ui.dolphin_previews_default_hint":   "Nötig, damit auch der erste Blick in einen Ordner nichts lädt: Dolphin wendet die Einstellung eines Ordners erst an, wenn die Ansicht schon steht. Diese Vorgabe gilt für alle Ordner ohne eigene Einstellung – also auch außerhalb des Sync-Ordners. Einzelne Ordner können die Vorschau weiterhin selbst einschalten.",
	"ui.dolphin_previews_default_done":   "Vorgabe gesetzt: keine Vorschau in Ordnern ohne eigene Einstellung. Dolphin einmal neu starten.",
	"ui.dolphin_previews_default_undone": "Vorgabe zurückgenommen – Dolphin zeigt wieder überall Vorschauen.",
}
