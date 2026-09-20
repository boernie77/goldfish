---
name: goldfish-users
description: "Use when changing Goldfish users, ACL/FSK, playlists per user, statistics, notifications, activity log and backup/restore."
metadata:
  project: Goldfish (boernie77/goldfish)
  source: "CLAUDE.md-Aufteilung 2026-09-20"
---

# goldfish-users

Aus der früheren Sammel-CLAUDE.md des Goldfish-Repos ausgelagerter Themenbereich (Zeichen: 23295, Sektionen: 6). Volltext des Originals: Skill `goldfish-full-archive`.

## Harte Regeln (zuerst lesen)

- Strikte Datentrennung pro Benutzer — jede Änderung an Filtern/Abfragen darauf prüfen.
- JWT trägt den Admin-Status vom Login-Zeitpunkt; neue Admins müssen sich neu einloggen.

---

### Benutzer & Zugriff
- Erst-Setup bei leerer `users`-Tabelle: `/login.html` zeigt Setup-Formular; der erste
  Account wird automatisch Admin.
- Login per bcrypt; Session-Token in HttpOnly-Cookie (SameSite=Lax).
- **ACL:** Non-Admins sehen nur die in `user_library_access` gelisteten Libs.
  **Admins sehen IMMER alle Bibliotheken**, unabhängig von etwaigen eigenen
  ACL-Zeilen. **Historie:** 2026-08-31 gab es kurzzeitig eine
  Admin-Selbsteinschränkung (eigene ACL-Zeile → auch für den Admin nur diese
  Liste) — am 2026-09-02 zurückgenommen, weil eine frisch angelegte
  Bibliothek für einen Admin mit eigener ACL-Liste dann NIRGENDS mehr
  auftauchte, auch nicht im „Bibliotheken verwalten"-Dialog (real erlebt:
  neue "Demo"-Bibliothek erschien nur noch über den ungefilterten
  `/api/libraries?all=1`-Diagnose-Endpoint, nicht im normalen
  `/api/libraries`, das der Verwaltungsdialog nutzt — wirkte wie ein Bug, war
  aber die ACL-Einschränkung). Sichtbarkeits-Personalisierung für den eigenen
  täglichen Gebrauch läuft stattdessen ausschließlich über die dafür gebaute
  „🏠 Startseite anpassen"-Funktion (`user_home_prefs`/`user_nav_prefs`,
  siehe „Startseite (Home-View)" weiter unten) — ACL ist wieder rein ein
  Werkzeug, um Nicht-Admin-Usern Zugriff zu entziehen.
  Entscheider: `Store.UserHasLibraryAccess`/`ListLibrariesForUser` geben für
  `isAdmin=true` immer sofort `true`/alle zurück; der zentrale `ListItems`-
  ACL-Block überspringt die Einschränkung komplett bei `f.IsAdmin`.
  `requireLibAccess(w, r, libID)` wird in jedem Item-/Stream-/Transcode-Handler
  aufgerufen. Der ACL-Editor holt die Gesamtliste über `/api/libraries?all=1`
  (admin-only) — dort lässt sich für einen Admin-Account zwar weiterhin eine
  ACL-Liste speichern, sie hat aber keine Wirkung mehr. Test:
  `internal/store/acl_test.go`.
- Nutzerverwaltung (anlegen, Passwort *anderer* User zurücksetzen, Admin togglen,
  ACL bearbeiten) in der UI unter Settings → Benutzer (nur Admins).
- **Zahnrad-Menü ist seit 2026-09-01 für JEDEN User sichtbar** (vorher komplett
  admin-only versteckt). Sektion „Mein Konto" (🏠 Startseite anpassen, 🔐
  eigenes Passwort ändern via `PUT /api/auth/password`) ist für alle da; alle
  übrigen Drawer-Sektionen tragen die Klasse `.drawer-admin` und werden für
  Non-Admins per `renderUserMenu()` (admin.js) ausgeblendet. Drawer-Titel
  wechselt „⚙ Menü" (User) / „⚙ Administration" (Admin).
- Watched + Favorite sind **pro User** (`user_item_state`), nicht pro Item.
- **⚠ `GetSession` muss JEDES neue `users`-Feld mitladen** — `currentUser(r)`
  wird bei jedem Request daraus befüllt, NICHT aus `GetUserByName`/`GetUser`.
  War 2026-09-02 ein echter Bug: `max_age_rating` fehlte in der Query, die
  FSK-Grenze hatte dadurch bei KEINER eingeloggten Session je eine Wirkung.
  Test: `users_test.go TestGetSessionCarriesMaxAgeRatingAndCanDownload`.
- **⬇ Downloads pro User steuerbar (seit 2026-09-02):** `users.can_download`
  (Default 1 = erlaubt). Admin togglet es in der Benutzerverwaltung
  (`PUT /api/users/{id}/can-download`, `Store.SetUserCanDownload`). Admins
  ignorieren den Wert (gleiche Konvention wie `MaxAgeRating`). Durchsetzung
  serverseitig in `downloadItem` (`internal/api/delete_download.go`,
  `requireDownloadAllowed`-Helper in `helpers.go`) — 403 bei `can_download=0`
  und Nicht-Admin. Frontend blendet `#detailDownload`/`#bulkDownload` bei
  fehlender Erlaubnis zusätzlich aus (reiner UI-Komfort, kein Ersatz für die
  Server-Prüfung). `GET /api/auth/status` liefert `canDownload` mit, damit
  `state.me.canDownload` im Client verfügbar ist.
- **🔍 „Manuell zuordnen" + ✅ „Zuordnung bestätigen" sind admin-only** (seit
  2026-09-02) — `PUT /items/{id}/confirm` hatte vorher GAR KEINEN Admin-Schutz
  (jeder eingeloggte User konnte `metadata_confirmed` per API togglen); beide
  Buttons in `player.js` prüfen jetzt zusätzlich `state.me.isAdmin`.
### Glocke / Benachrichtigungen (seit 2026-05-05)

- 🔔-Button in der Topbar (`.bell-btn`) neben dem Zahnrad.
- Rotes Badge mit ungelesener Anzahl; Klick öffnet Dropdown, markiert alle als gelesen.
- Einträge in `localStorage` unter `gf_notifications` (max 50), persistent über Reload.
- Aktuell befüllt von Whisper-Job-Completions (✅ fertig / ❌ fehlgeschlagen).
- `bellAdd(icon, title, sub)` ist global — weitere Features können es nutzen.
- `initBell()` wird aus `boot()` in `app.js` aufgerufen.

### Statistik (seit 2026-07-10)
- Menüpunkt „📊 Statistik" im Zahnrad-Drawer (`data-action="statistik"`, admin-only
  wie der Rest des Drawers). Scope ist immer der Kontext, aus dem der Dialog
  geöffnet wird — `state.currentLibrary` + `state.currentFolder`, gleiche
  Konvention wie der Folder-gescopte Scan-Button: Library-Root = ganze Bibliothek,
  in einem Unterordner nur dessen Inhalt rekursiv.
- Zeigt Gesamtzahl Dateien, Gesamtgröße, Gesamtlaufzeit + drei Balken-Verteilungen
  (Auflösung, Filetyp/Container, Länge-Buckets).
- **Performance:** rein aggregierende SQL (`COUNT`/`SUM`/`CASE WHEN`), keine
  Item-Rows werden nach Go geladen. Auflösungs-Bucket-Grenzen identisch zum
  bestehenden `ResBuckets`-Filter (`MAX(height, width*9/16)`). Zwei indexierte
  Scans (`items_library_idx` bzw. `items_lib_relpath_idx` bei Folder-Scope),
  läuft nur on-demand beim Öffnen des Dialogs — keine Zusatzlast im normalen
  Grid-Betrieb.
- Code: `internal/store/stats.go` (`GetLibraryStatDetail`), Handler
  `internal/api/libraries.go` (`libraryStatDetail`), Route
  `GET /api/libraries/{id}/stats-detail?folder=`. **Nicht** verwechseln mit dem
  bestehenden `GET /api/libraries/{id}/stats` (liefert nur `totalItems`/
  `folderCount` für Folder-Kachel-Badges — separater, unveränderter Endpoint).
  Frontend: `openStatistikDialog()` in `admin.js`.
- **🎵 Metadaten-Vollständigkeit für Musik-Bibliotheken (seit 2026-09-06,
  User-Wunsch: "einen Punkt, wo ich sehen kann, wieviel der Titel komplett
  mit Metadaten versehen sind und wie sich das entwickelt"):** nur bei
  `kind=music` gesetzt — der `libraryStatDetail`-Handler lädt zusätzlich
  `Store.GetMusicMetadataStat(libID, folder)` und hängt sie als
  `musicMetadata`-Feld an (`nil`/omitted bei Filme/Serien/Privat). Zeigt vier
  Balken: Künstler/Genre pro Track, Genre/Jahr pro Album (jeweils `X/Y
  (Z%)`). Titel/Dauer bewusst NICHT gezeigt — die sind praktisch nie leer
  (Dateiname- bzw. ffprobe-Fallback) und wären als Vollständigkeits-Kennzahl
  irreführend. Dialog einfach erneut öffnen, um den Fortschritt des
  MusicBrainz-Backfills (siehe „Musik-Bibliotheken" → „Genre/Jahr-Backfill")
  zu sehen — kein Live-Update, reiner Snapshot bei Öffnen. Code:
  `Store.GetMusicMetadataStat` (`internal/store/stats.go`),
  `renderMusicMetadataSection` (`admin.js`). Test:
  `internal/store/music_metadata_backfill_test.go
  TestGetMusicMetadataStat`.

### Aktivitäts-Protokoll & Backup/Restore (seit 2026-09-02)

- **Zweck (User-Wunsch):** nachvollziehen, wer sich an-/abgemeldet hat, was
  manuell bearbeitet/ausgelöst wurde und wer was schaut — plus die Möglichkeit,
  die komplette Datenbank zu sichern und im Bedarfsfall wiedereinzuspielen.
- **Neue Tabelle `activity_log`** (`internal/store/sqlite.go`):
  `id, at, user_id, username, category, action, detail`. `category` ∈
  `auth|playback|download|admin|job`. `LogActivity`/`ListActivityLog` in
  `internal/store/activity_log.go` — Pagination über `beforeId` (id-basierter
  Cursor), Retention: bei ~1 von 200 Inserts werden Einträge `> 180 Tage`
  gelöscht (kein eigener Hintergrund-Worker nötig).
- **Bewusst EIN Eintrag pro Lauf, nicht pro Datei** (User-Vorgabe: „Trickplay
  oder OCR, aber dann nicht jede Datei extra"): ein Scan-Start, ein
  Trickplay-„Fehler erneut versuchen", ein OCR-„alle jetzt erzeugen" erzeugen
  jeweils genau eine Zeile mit einer Zusammenfassung (z. B. „N zurückgesetzt").
  Einzel-Item-Retries (z. B. `retryItemTrickplay`, `ocrSubRetryItem`,
  `retryIntroSkipFolder`) sind bewusst NICHT geloggt — das wäre exakt die
  Pro-Datei-Granularität, die der User ausdrücklich nicht wollte.
- **Protokollierte Aktionen** (siehe `LogActivity`-Aufrufstellen, verteilt über
  `internal/api/*.go`): Login/Logout (`auth.go`, inkl. `oidc.go` für SSO),
  fehlgeschlagene Logins (mit versuchtem Usernamen, ohne Passwort), Wiedergabe-
  Start (`stream.go playbackInfo` — „wer schaut was", ein Eintrag pro
  Player-Open, kann bei mehreren Audio/Sub-Wechseln währenddessen mehrfach
  feuern, bewusst in Kauf genommen statt Overengineering), Einstellungen
  speichern (`settings.go`, ohne Klartext-Keys), Bibliothek anlegen/löschen,
  Benutzer anlegen/löschen/Passwort-Reset/Admin-Toggle/ACL-Änderung
  (`users.go`), Datei löschen/umbenennen/verschieben (einzeln + Bulk,
  `delete_download.go`/`admin_rename.go`), manuelle TMDB-Zuordnung + Bestätigen
  (`tmdb.go`/`items.go`), Scan-Start (`scan.go`, deckt sowohl manuellen
  ⟳-Button als auch ausgelöste Aufgaben aus Auto-Scan ab, da beide über
  denselben Handler laufen), Trickplay-Retry/Alles-löschen, OCR-Run-All/
  Retry-Failed/Ordner- und Global-Toggle, Intro-Erkennung-Ordner-Toggle,
  Backup-Download/-Restore.
- **Downloads (Kategorie `download`, seit 2026-09-14, User-Wunsch „Downloads
  sollen bitte auch protokolliert werden"):** Anlass war eine über eine Stunde
  laufende `?compat=1`-Formatanpassung eines 4K-Remux, die den Server sichtbar
  ausbremste und im Protokoll **nirgends** auftauchte — Benutzer und Gerät
  ließen sich hinterher nicht mehr feststellen. Zwei getrennte Aktionen, beide
  über den gemeinsamen Helper `Server.logDownload`
  (`internal/api/delete_download.go`):
  - `download_start` (`downloadItem`) — der eigentliche Transfer. Bewusst NUR
    beim ERSTEN Request: ein Resume schickt `Range: bytes=<offset>-`, sonst
    entstünde pro Fortsetzung eine weitere Zeile (gleiche „ein Eintrag pro
    Lauf"-Konvention wie bei Scan/OCR).
  - `download_prepare` (`downloadCompatStatus`, nur im `p.State == "idle"`-Zweig,
    also genau dann, wenn hier wirklich ein `StartPrep` ausgelöst wird) — die
    teure Formatanpassung. **Eigener Eintrag, weil sie detached weiterläuft**
    (`context.Background()`, siehe „Download & Löschen"): sie erzeugt auch dann
    stundenlang Last, wenn der Client die fertige Datei nie abholt, und wäre
    ohne diese Zeile weiterhin unzuordenbar.
  Detail-Text ist `"Titel" (Original)` bzw. `"Titel" (angepasst[, <profil>])`.
  **Der Browser fordert nie `compat=1` an** (kein Treffer in
  `internal/webassets/web/`), GoldfishLinux nur bei explizit gewähltem Profil
  (`downloads.py`: `compat=bool(profile)`) — ein `download_prepare` OHNE
  Profil-Zusatz stammt daher praktisch immer von einer Apple-App.
- **`[transcode] neue session …`-Logzeile mit Benutzer + Gerät** (seit
  2026-09-14, `transcodePlaylist` in `internal/api/stream.go`): die bestehende
  `[transcode] start`-Zeile in `internal/playback/ffmpeg.go` kann das nicht
  leisten — das Manager-Paket sieht keinen `*http.Request`. Ein Client, der
  `POST /playback/{id}/start` nicht ruft (ältere App-Stände), hinterließ dadurch
  **gar keine** Spur, wer eine Session gestartet hat. Die Zeile läuft nur, wenn
  `LookupSession` nil liefert, also wirklich eine neue Session entsteht — sonst
  würde jeder VHS-Playlist-Reload (alle ~4 s) das Log fluten. Bewusst NUR ins
  Server-Log, nicht ins `activity_log`: dort steht der Wiedergabe-Start bereits
  als `play`, eine zweite Zeile pro Seek wäre genau das Pro-Datei-Rauschen, das
  der User für dieses Protokoll ausdrücklich nicht wollte.
- **API:** `GET /api/admin/activity-log?category=&username=&beforeId=&limit=`
  — admin-only. `username` ist exakter Match (kein LIKE), gespeist aus dem
  bestehenden `GET /api/users` (voller Admin-Endpoint, bewusst nicht das
  Self-Exclusion-`listOtherUsernames`).
- **Frontend:** `#activityLogDialog` (`admin.js openActivityLogDialog`/
  `refreshActivityLog`/`populateActivityLogUserFilter`), Zahnrad-Menü →
  „Daten & Sicherheit" → „📜 Protokoll". Zwei Filter-Dropdowns (Kategorie +
  Benutzer, User-Wunsch 2026-09-02 nachträglich ergänzt). `ACTIVITY_LOG_LABELS`
  übersetzt die internen Action-Codes in lesbare deutsche Labels für die Tabelle.
- **Gerät + Wiedergabe-Ende/-Fehler (seit 2026-09-11, User-Wunsch: "ich
  würde gerne sehen, auf welchem Gerät etwas passiert ist, und nicht nur
  Wiedergabe gestartet, sondern auch beendet. Kann man auch Fehlermeldungen
  ... ins Protokoll nehmen"):**
  - Neue Spalte `activity_log.device` (Migration, `addCol`) + `ActivityEntry.
    Device`, in JEDEN der 41 `LogActivity`-Aufrufstellen mit durchgereicht.
    `deviceLabel(r *http.Request)` (`internal/api/helpers.go`): erst der
    eigene `X-Goldfish-Client`-Header (freiwillig, von den nativen Apps
    gesetzt, z. B. "Goldfish-Mac/209" — zuverlässig, weil selbst gesetzt,
    anders als der praktisch nicht unterscheidbare CFNetwork/Darwin-
    Default-User-Agent von URLSession/OkHttp auf Mac/iOS/tvOS), sonst eine
    einfache `User-Agent`-Heuristik für Browser (Chrome/Firefox/Safari/
    Edge/Opera × Windows/macOS/iOS/Android/Linux, z. B. "Chrome · macOS").
    Bei den beiden Auto-Backup-Aufrufstellen (`autobackup.go`, kein
    `*http.Request` vorhanden, Ticker-getriggert) bleibt `device=""`.
  - **⚠ Wiedergabe-Start wird NICHT im `GET /api/playback/{id}`-Handler
    geloggt** — der dient ZWEI Zwecken (echter Start UND reines Vorab-Laden
    der Stream-Liste fürs Detail-Dialog-Dropdown), ein Auto-Log dort erzeugte
    schon beim bloßen Öffnen des Detail-Dialogs einen Eintrag und beim
    tatsächlichen Abspielen einen zweiten. Stattdessen client-getriggertes
    `POST /api/playback/{id}/start` (symmetrisch zu `stop`/`error`), das nur
    die echten Play-Auslöser rufen (`player.js applyPlayback`, `music.js`
    Track-Start, `PlayerView.setUp`).
  - **Wiedergabe-ENDE**: `POST /api/playback/{id}/stop` (`stream.go
    playbackStop`, Body `{reason: "ended"|"closed", positionSec,
    durationSec}`) — Gegenstück zum "play"-Log beim Start.
    Der Server kann ein Wiedergabe-Ende nicht selbst erkennen (HTTP ist
    zustandslos, ein Transcode-Session-Timeout heißt nur "5 Minuten kein
    Request", nicht "User hat bewusst gestoppt") — der Client meldet es
    aktiv. Browser: `reportPlaybackStop(reason)` in `player.js`, aufgerufen
    aus `vjs.on("ended")` (reason "ended") UND `closePlayer()` (reason
    "closed", VOR `disposePlayer()`) — `state.playback.stopReported`-Flag
    (gesetzt in `applyPlayback`, pro Session zurückgesetzt) verhindert einen
    doppelten Report, wenn beide Pfade in derselben Session greifen.
    Bewusste Grenze: ein abstürzender/offline gehender Client meldet nie
    einen Stop (kein Ersatz für eine Heartbeat-Architektur).
  - **Wiedergabe-FEHLER**: `POST /api/playback/{id}/error` (`stream.go
    playbackError`, Body `{message}`, serverseitig auf 300 Zeichen
    gekappt). Browser: `player.js` hatte bisher **gar keinen**
    `vjs.on("error", ...)`-Handler — neu ergänzt, liest `vjs.error()`
    (MediaError-artiges Objekt) aus und meldet `.message`/Code.
  - **Der Server hängt seinen eigenen Zustand an jede Fehlermeldung**
    (seit 2026-09-13): `playbackError` ruft `Playback.DiagnoseItem(itemID)`
    auf und schreibt das Ergebnis sowohl ins Server-Log als auch in
    `activity_log.detail` (`… [server: session=… alter=… ffmpeg_laeuft=…
    playlist=… segmente=…]`). Der Client meldet nur eine generische
    Meldung — ob dahinter eine tote ffmpeg-Session, eine leere Playlist
    oder gar keine Session steckt, ist wenige Minuten später nicht mehr
    feststellbar, weil der GC die Session abräumt. **Deshalb im
    Fehlerpfad erheben, nicht erst bei der Auswertung.** Die
    Protokollzeile bleibt 180 Tage und ist damit verlässlicher als die
    rotierenden Container-Logs. `DiagnoseItem` liest nur und schluckt
    jeden eigenen Fehler — eine Diagnose darf den Fehlerpfad nie
    zusätzlich zum Scheitern bringen.
  - **`scripts/diag-playback.sh`** holt Server-Log (ohne
    Enrichment-Rauschen) + Protokoll für ein Zeitfenster in einem Rutsch:
    `./scripts/diag-playback.sh 18:37` (Uhrzeit lokal, optional zweites
    Argument = Fenster in Minuten). Braucht
    `~/.config/portainer/credentials.env`; demultiplext die
    8-Byte-Frame-Header der Docker-Log-API, die sonst als Steuerzeichen
    im Text landen.
  - Neue Protokoll-Zeilen: `stop` → "Wiedergabe beendet", `error` →
    "Wiedergabe-Fehler" (`ACTIVITY_LOG_LABELS`). Detail-Text bei `stop`
    z. B. "Titel (12:34 von 45:00, zu Ende)"/"…, geschlossen"
    (`fmtClock()`-Helper in stream.go).
  - Protokoll-Tabelle hat eine neue "Gerät"-Spalte.
  - **✅ GoldfishApple nachgezogen (Build 210, 2026-09-11):**
    `GoldfishClient` setzt `X-Goldfish-Client` (z. B. "Goldfish-Mac/210")
    per `httpAdditionalHeaders` auf JEDEM Request; `PlayerView.swift`
    (plattformübergreifend geteilt, deckt Mac/iOS/tvOS in einem Rutsch ab)
    meldet Stop bei `didPlayToEndTime` UND manuellem Schließen
    (`playbackStopReported`-Flag verhindert Doppel-Report) sowie Error bei
    allen vier bestehenden Fehlerquellen (Stream-URL fehlt, `playback()`-
    Fetch schlägt fehl, `AVPlayerItem.status == .failed`,
    `AVPlayerItemNewErrorLogEntry`).
  - **Noch offen:** GoldfishAndroid sendet bisher keinen
    `X-Goldfish-Client`-Header und ruft die neuen Stop-/Error-Endpoints
    noch nicht auf — läuft dort vorerst nur mit generischem OkHttp-
    User-Agent-Fallback und ohne Stop/Error-Logging.
  - Tests: `internal/api/helpers_test.go` (`TestDeviceLabel`),
    `internal/store/activity_log_test.go` (Device-Feld-Roundtrip).
- **Backup:** `Store.BackupToFile` (`internal/store/backup.go`) nutzt SQLites
  eingebautes `VACUUM INTO` — checkpointed den WAL automatisch, liefert eine
  einzelne konsistente Datei OHNE die riskante manuelle
  `.db`+`.db-wal`+`.db-shm`-Kopie im laufenden Betrieb. `GET /api/admin/backup`
  erzeugt sie in einen Temp-Pfad und liefert sie als Download
  (`goldfish-backup-<Datum>.db`), löscht die Temp-Datei danach. Enthält NICHT
  die Mediendateien oder Poster-/Trickplay-Caches (jederzeit neu erzeugbar) —
  nur die eigentliche SQLite-DB.
- **Restore ist bewusst restart-basiert, kein Live-Hot-Swap:** mehrere
  Hintergrund-Worker (Enrich, Trickplay, Introskip, OCR, Whisper, Auto-Scan)
  halten langlebige `*Store`-Referenzen — ein `*sql.DB`-Austausch unter allen
  laufenden Zugriffen wäre fehleranfällig. `Store.RestoreFromFile`
  (1) sichert die AKTUELLE DB nach `<config>/backups/pre-restore-<Zeitstempel>.db`
  (Schutz vor Fehlgriffen), (2) schließt die eigene DB-Verbindung, (3) entfernt
  `.db-wal`/`.db-shm` der alten Datei, (4) verschiebt die hochgeladene Datei an
  ihre Stelle. Der Handler (`internal/api/backup.go uploadRestore`) validiert
  die Upload-Datei VORHER (`store.ValidateBackupFile` — `PRAGMA
  integrity_check` + Kern-Tabellen-Check `users/items/settings/libraries` in
  einer eigenen readonly-Verbindung, rührt nicht an der aktiven DB) und ruft
  danach bewusst **`os.Exit(0)`** nach einer kurzen Verzögerung (Response muss
  erst beim Client ankommen) — **Docker `restart: unless-stopped` startet den
  Container automatisch mit der wiederhergestellten Datenbank neu.** Ohne
  diesen Neustart bliebe die alte, geschlossene `*sql.DB`-Verbindung im
  Prozess hängen.
  **NICHT** versuchen, das auf einen Live-Hot-Swap umzubauen, ohne die
  Hintergrund-Worker-Referenzen mitzudenken.
- **`POST /api/admin/backup/restore`** (multipart, Feld `file`, bis 2 GiB).
  Upload landet zunächst im selben Verzeichnis wie die aktive DB
  (`s.ConfigDir`) — `os.Rename` kann sonst nicht dateisystemübergreifend
  verschieben.
- **Frontend:** `#backupDialog` (`admin.js downloadBackup`/`uploadRestoreFile`),
  Download via `window.location.href` (Cookie-Auth, gleiches Muster wie der
  bestehende CSV-Export bei Umbenennungen), Restore via `fetch()` +
  `FormData` + doppelter `appConfirm`-Bestätigung (danger-Style) + Redirect
  nach 15 s (Wartezeit für den Container-Neustart).
- Tests: `internal/store/activity_log_test.go` (Insert/Filter/Pagination),
  `internal/store/backup_test.go` (Backup→Validate→Restore-Rundlauf inkl.
  Ablehnung einer kaputten Datei).

### Automatisches Backup (seit 2026-09-05, LIVE 1.0.70)

- Zeitgesteuerte Ergänzung zum manuellen Backup-Download oben — nutzt
  dieselbe `Store.BackupToFile` (VACUUM INTO). **Bewusst EIN einzelner
  Zeitplan, kein Array wie bei Auto-Scan** — für "die ganze DB sichern" gibt
  es keinen Anwendungsfall für mehrere parallele Zeitpläne.
- Zeitplan-Format **identisch zu Auto-Scan** (`daily:HH:MM` | `every:Nh` |
  `weekly:DOW:HH:MM`), `matchSchedule()` aus `autoscan.go` wird 1:1
  wiederverwendet (`internal/api/autobackup.go`, gleiches Package).
- Settings-Keys: `auto_backup_enabled`, `auto_backup_schedule`,
  `auto_backup_retention` (Default 7, Range 1–60).
- `Server.RunAutoBackup(ctx)` — Ticker alle 60 s, analog `RunAutoScan` aber
  mit einem einzelnen `lastFired time.Time` statt einer Map. Gestartet in
  `cmd/goldfish/main.go` neben `RunAutoScan`.
- Erzeugte Dateien: `<ConfigDir>/backups/auto-<YYYY-MM-DD_HHMMSS>.db` —
  **derselbe Ordner**, den `Store.RestoreFromFile` für die
  pre-restore-Sicherheitskopie nutzt (beide Dateiarten liegen nebeneinander,
  unterscheidbar am Präfix `auto-` vs. `pre-restore-`). Nach jedem Lauf
  räumt `pruneAutoBackups` überzählige (älter als die letzten `retention`)
  automatisch weg — der `YYYY-MM-DD_HHMMSS`-Zeitstempel im Dateinamen
  sortiert lexikografisch korrekt chronologisch, kein zusätzliches `Stat`
  für die Reihenfolge nötig.
- **Endpoints (alle admin-only):**
  ```
  GET/PUT /api/admin/auto-backup/settings          {enabled, schedule, retention}
  GET     /api/admin/auto-backup/list              [{name, sizeBytes, modTime}]
  GET     /api/admin/auto-backup/download/{name}
  DELETE  /api/admin/auto-backup/{name}
  ```
  `isValidAutoBackupFilename` schützt Download/Delete gegen Directory-
  Traversal — akzeptiert nur exakt das `auto-*.db`-Namensmuster ohne
  Pfadanteile.
- **Protokolliert** (`activity_log`, category `job`, EIN Eintrag pro Lauf):
  `backup_auto` mit Dateiname+Größe (userID/username leer = System-Event,
  siehe `LogActivity`-Konvention). Einstellungsänderung selbst wird als
  `admin`/`auto_backup_settings` durch den auslösenden Admin geloggt.
- **Frontend:** Zahnrad-Menü → „Automatisierung" → „🗄 Automatisches
  Backup" (`#autoBackupDialog`, `admin.js openAutoBackup`/
  `autoBackupRenderSchedule`/`saveAutoBackup`/`autoBackupRefreshList`) —
  Zeitplan-Picker-UI ist eine 1:1-Kopie des Musters aus `autoScanModeSelect`,
  nur gegen ein einzelnes `autoBackupCfg`-Objekt statt gegen ein
  Array-Element. Liste der vorhandenen automatischen Sicherungen mit
  ⬇-Download/🗑-Löschen pro Zeile, direkt im selben Dialog (kein separater
  „Backup"-Dialog nötig — die manuelle Sofort-Sicherung bleibt aber weiterhin
  im bestehenden `#backupDialog`, keine Vermischung der beiden Flows).
- **Restore bleibt ausschließlich manuell** (siehe oben) — automatische
  Backups sind reine Sicherungen zum Herunterladen/Aufbewahren, kein
  automatisierter Restore-Pfad.

### Gesehen-Markierung
- `items.watched` + `watched_at`.
- Auto-Markierung bei 90 % Laufzeit (einmal pro Player-Session).
- Manuell togglebar im Detail-Dialog.
- Filter in Topbar: Alle / Nur ungesehen / Nur gesehen.
- Visuell: grünes ✓-Badge, abgedunkelte Kachel, gedimmter Titel.

