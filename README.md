# TDrive Sync

Ein Google-Drive-Synchronisationsclient für Linux – funktional angelehnt an den
Windows-Client von Google Drive. Er läuft als Hintergrunddienst mit **Tray-Icon**,
bietet ein **natives Einstellungs-Fenster** (WebKitGTK, kein Browser) und wird als
**AppImage** ausgeliefert (eine Datei, keine Installation nötig).

Als Sync-Engine dient das bewährte [rclone](https://rclone.org), das im AppImage
mitgeliefert wird.

## Funktionen

- **Zwei Sync-Modi, jederzeit umschaltbar:**
  - **Stream (virtuelles Laufwerk):** Das gesamte Drive erscheint als Ordner,
    Dateien werden bei Bedarf geladen. Spart Speicherplatz.
  - **Mirror (lokale Kopie):** Vollständige Zwei-Wege-Synchronisation eines
    Ordners – alles offline verfügbar.
- **Bidirektionale Synchronisation:** Änderungen fließen in beide Richtungen –
  lokal → Drive und Drive → lokal (Mirror-Modus, `rclone bisync`).
- **Echtzeit-Überwachung lokaler Änderungen:** Ein nativer inotify-Watcher
  erkennt lokale Änderungen sofort und stößt den Abgleich unmittelbar an
  (entprellt), statt nur auf den Intervall-Timer zu warten.
- **Periodisches Remote-Polling:** Ein Timer gleicht regelmäßig ab und erfasst so
  auch Änderungen, die direkt in Google Drive gemacht wurden (Standard 5 min,
  einstellbar).
- **Konfliktbehandlung – automatisch oder manuell (in der Oberfläche wählbar):**
  - **Automatisch:** Die neuere Datei gewinnt; im Zweifel gewinnt die Cloud. Die
    unterlegene Kopie bleibt als **datierte Sicherung** erhalten.
  - **Manuell** (Standard): Beide Versionen bleiben erhalten; die Oberfläche
    listet alle offenen Konflikte, du entscheidest pro Datei
    („Diese behalten" / „Löschen"). Ein Zähler-Badge an der Statusanzeige zeigt
    offene Konflikte.
- **Automatische Wiederherstellung (Auto-Recovery):** Nach mehreren
  aufeinanderfolgenden Fehlversuchen wird automatisch ein vollständiger
  Neuabgleich (`--resync`) ausgelöst.
- **Erkennung verwaister Sperren:** Sperrdateien abgestürzter/abgebrochener Läufe
  werden automatisch erkannt und entfernt.
- **Offline verfügbar machen:** Im Stream-Modus lassen sich einzelne Ordner
  auswählen, die dauerhaft lokal vorgehalten und offline nutzbar bleiben –
  wie „Offline verfügbar machen" beim Windows-Client.
- **JSON-Status-API:** Eine stets aktuelle `status.json` erlaubt einfaches
  Monitoring von außen (zusätzlich zur HTTP-API auf 127.0.0.1).
- **Protokoll mit Rotation:** Tägliche Logdateien mit 7-Tage-Aufbewahrung; alte
  Dateien werden automatisch aufgeräumt.
- **Autostart beim Anmelden:** Standardmäßig aktiv (XDG-Autostart), in der
  Oberfläche abschaltbar.
- **Automatische Updates:** Prüft beim Start und periodisch die GitHub-Releases,
  meldet neue Versionen und aktualisiert die AppImage auf Knopfdruck. Optional
  werden auch **Vorabversionen (Prereleases)** berücksichtigt. Die aktuelle
  Version wird im Einstellungs-Fenster angezeigt.
- **Tray-Icon** mit Statusfarbe (grün = aktuell, blau = synchronisiert,
  rot = Fehler) und Kontextmenü.
- **Natives Einstellungs-Fenster** mit Datei-Browser zum Auswählen der
  Offline-Ordner (nutzt das im System vorhandene WebKitGTK, kein Web-Browser).
- **Desktop-Benachrichtigungen** bei wichtigen Ereignissen.
- **Automatischer Neustart** des Mounts / erneuter Abgleich bei Verbindungs­abbruch.

## AppImage bauen

Voraussetzungen: Go ≥ 1.23, `curl`, `unzip`. rclone und appimagetool werden
automatisch heruntergeladen.

```bash
./build-appimage.sh
```

Ergebnis: `dist/Google_Drive_Sync-x86_64.AppImage`

Umgebungsvariablen (optional):

| Variable        | Zweck                                             |
|-----------------|---------------------------------------------------|
| `GO`            | Pfad zu einer bestimmten Go-Installation          |
| `RCLONE_BIN`    | vorhandene rclone-Binary statt Download verwenden |
| `APPIMAGETOOL`  | vorhandenes appimagetool verwenden                |

## Benutzung

```bash
chmod +x Google_Drive_Sync-x86_64.AppImage
./Google_Drive_Sync-x86_64.AppImage
```

Beim ersten Start öffnet sich das Einstellungs-Fenster. Dort auf
**„Bei Google anmelden"** klicken – für die Google-Anmeldung selbst öffnet sich
einmalig der Standardbrowser (OAuth). Danach Modus wählen und im Stream-Modus
optional Ordner als „offline" markieren.

Weitere Kommandos:

```bash
./Google_Drive_Sync-x86_64.AppImage login    # Anmeldung in der Konsole (headless)
./Google_Drive_Sync-x86_64.AppImage open     # Einstellungs-Fenster öffnen
./Google_Drive_Sync-x86_64.AppImage status   # Status ausgeben
```

Ein erneuter Start bei bereits laufendem Dienst öffnet einfach die Einstellungen.

### Autostart

Der Dienst richtet sich **automatisch** für den Start beim Anmelden ein: Beim
ersten Start wird ein XDG-Autostart-Eintrag unter
`~/.config/autostart/tdrive-sync.desktop` angelegt (verweist auf den AppImage-/
Programm-Pfad). Abschalten oder wieder aktivieren lässt sich das jederzeit über
den Schalter **„Beim Anmelden automatisch starten"** im Einstellungs-Fenster.

## Automatische Updates

Die AppImage kann sich selbst aktualisieren. Der Dienst prüft **beim Start** und
danach in Abständen die
[GitHub-Releases](https://github.com/tokajer/tdrive-sync/releases) des Projekts:

- Ist eine neuere Version verfügbar, erscheint im Einstellungs-Fenster ein
  Hinweis mit Button **„Jetzt aktualisieren"** und eine Desktop-Benachrichtigung.
- Beim Aktualisieren wird das passende `*.AppImage`-Asset heruntergeladen und die
  laufende Datei **atomar ersetzt**; anschließend genügt ein Klick auf
  **„Neu starten"**.
- Mit der Option **„Vorabversionen"** werden auch als *Prerelease* markierte
  Releases einbezogen.

Die **angezeigte Version** wird beim Bau aus dem Git-Tag gesetzt
(`-ldflags -X main.version=…`; im GitHub-Build der getaggte Release). Lokale
Builds ohne Tag melden sich als **`local-dev-build`** und bieten kein
Selbstupdate (nur die AppImage-Version kann sich ersetzen).

Abschalten: `update_check_disabled: true` in der `config.yaml`.

## Voraussetzungen zur Laufzeit

- **FUSE 3** (`fusermount3`) für den Stream-Modus – auf den meisten Distributionen
  vorinstalliert.
- **WebKitGTK** (`libwebkit2gtk-4.1`) für das Einstellungs-Fenster – auf den
  meisten Desktops vorhanden. Fehlt es, läuft der Dienst weiter; das Fenster kann
  dann nicht geöffnet werden (die HTTP-Steuerung auf 127.0.0.1 bleibt aber aktiv).
- **Tray-Icon:** Es wird ein *StatusNotifierItem*-Host benötigt. KDE Plasma, XFCE,
  Cinnamon u. a. bringen das mit. Unter **GNOME** ist die Erweiterung
  *AppIndicator and KStatusNotifierItem Support* nötig. Ohne Tray-Host läuft der
  Dienst trotzdem – das Einstellungs-Fenster erreichst du dann über
  `tdrive-sync open`.

## Eigene Google-OAuth-Zugangsdaten (später)

Standardmäßig werden die in rclone eingebauten Google-Zugangsdaten verwendet –
sofort startklar für den privaten Gebrauch. Wenn das Projekt wächst und höhere
Limits / eigenes Branding gewünscht sind, genügt es, in der Konfigurationsdatei
`~/.config/tdrive-sync/config.yaml` die eigenen Werte einzutragen:

```yaml
google:
  client_id: "DEINE_CLIENT_ID"
  client_secret: "DEIN_CLIENT_SECRET"
```

Danach einmal neu anmelden. Eine eigene Client-ID legt man in der
[Google Cloud Console](https://console.cloud.google.com) an (OAuth-Client,
Typ „Desktop", Drive-API aktivieren).

## Konfiguration

Alle Einstellungen liegen unter `~/.config/tdrive-sync/`:

- `config.yaml` – App-Einstellungen (Modus, Ordner, Offline-Pfade, Intervall,
  Konfliktmodus, Autostart, Port, OAuth)
- `rclone.conf` – rclone-Remote inkl. OAuth-Token

Wichtige Felder in `config.yaml`:

```yaml
sync_mode: stream            # "stream" oder "mirror"
conflict_mode: manual        # "manual" (Standard) oder "auto"
mirror_interval_sec: 300     # Polling-Intervall im Mirror-Modus (Sekunden)
autostart_disabled: false    # true = kein Autostart beim Anmelden
update_prerelease: false     # true = auch Vorabversionen (Prereleases) anbieten
update_check_disabled: false # true = keine automatische Update-Prüfung
```

Laufzeitdaten liegen unter `~/.local/state/tdrive-sync/` (bzw. `$XDG_STATE_HOME`):

- `status.json` – aktueller Status für Monitoring (JSON-Status-API)
- `logs/tdrive-sync-JJJJ-MM-TT.log` – tägliche Logdateien, 7 Tage Aufbewahrung

Der VFS-/bisync-Cache liegt unter `~/.cache/tdrive-sync/` (u. a. `bisync/` mit
Arbeits- und Sperrdateien).

## Architektur

```
cmd/tdrive-sync      Einstiegspunkt (Daemon, CLI, Single-Instance)
internal/config      Laden/Speichern der YAML-Konfiguration
internal/rclone      rclone-Wrapper (Login, mount, bisync, RC-API, Listing)
internal/manager     Sync-Manager: Modus-Steuerung, Status, Offline-Pinning,
                     inotify-Watcher, Auto-Recovery, Konfliktauflösung
internal/webui       lokaler Steuer-Server (127.0.0.1) + eingebettete Oberfläche
internal/window      natives Einstellungs-Fenster (WebKitGTK via dlopen, ohne Dev-Header)
internal/tray        Tray-Icon über DBus StatusNotifierItem (ohne GTK/cgo)
internal/notify      Desktop-Benachrichtigungen über DBus
internal/updater     Selbstupdate über GitHub-Releases (Check, Download, Austausch)
internal/logbuf      In-Memory-Ringpuffer für das Protokoll in der Oberfläche
internal/logfile     Tages-rotierende Logdatei mit 7-Tage-Aufbewahrung
packaging/           AppRun, Desktop-File, Icon
build-appimage.sh    Build-Skript
```

**Sync-Modi im Detail:**

- *Stream* startet `rclone mount` mit vollem VFS-Cache. Als „offline" markierte
  Ordner werden vollständig durch den Mount gelesen, damit sie im Cache landen und
  ohne Verbindung verfügbar bleiben.
- *Mirror* nutzt `rclone bisync` für eine echte Zwei-Wege-Synchronisation. Der
  Abgleich läuft im einstellbaren Intervall (Standard 5 min), auf Knopfdruck und
  **sofort bei lokalen Änderungen** (inotify-Watcher). Konflikte werden je nach
  Einstellung automatisch (neuer gewinnt, im Zweifel Cloud, datierte Sicherung)
  oder manuell in der Oberfläche gelöst. Nach mehreren Fehlversuchen erzwingt der
  Dienst einen vollständigen `--resync` (Auto-Recovery); verwaiste Sperren werden
  vor jedem Lauf bereinigt.

## Hinweise / bekannte Grenzen

- **Offline-Pinning (Stream):** Garantiert offline verfügbar sind gepinnte Ordner,
  solange der VFS-Cache nicht manuell geleert wird. Wer *alles* garantiert offline
  will, nutzt den **Mirror-Modus**. Die Umschaltung ist jederzeit möglich.
- Beim **ersten** Mirror-Abgleich führt rclone einen vollständigen `--resync` durch;
  das kann bei großen Drives dauern.
- Google-Docs/Sheets/Slides erscheinen (wie bei rclone üblich) als Verknüpfungs­dateien.

## Lizenz

Der App-Code steht unter der MIT-Lizenz. Das mitgelieferte rclone steht unter der
MIT-Lizenz (© Nick Craig-Wood).
