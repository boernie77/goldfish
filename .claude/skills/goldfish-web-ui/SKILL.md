---
name: goldfish-web-ui
description: "Use when changing the Goldfish web frontend: views, filter UI, tiles/overlays, toolbar, settings/admin UI, playlists pages, bulk selection, architecture and module layout."
metadata:
  project: Goldfish (boernie77/goldfish)
  source: "CLAUDE.md-Aufteilung 2026-09-20"
---

# goldfish-web-ui

Aus der früheren Sammel-CLAUDE.md des Goldfish-Repos ausgelagerter Themenbereich (Zeichen: 66370, Sektionen: 22). Volltext des Originals: Skill `goldfish-full-archive`.

## Harte Regeln (zuerst lesen)

- Playlists sind strikt pro Benutzer getrennt — Kuratierung darf sich nie vermischen.
- Vor neuen Kachel-Badges/Buttons die dokumentierten Overlay-Positionen lesen.

---

## Zweck

Privatgebrauch auf einem Unraid-Server. Zielwerte: **Direct Play wenn möglich**,
**On-the-fly Transcode via Intel VAAPI** sonst. Funktional an Jellyfin angelehnt, aber
deutlich schlanker — eine ausführbare Datei, ein Container, keine externen
Abhängigkeiten außer ffmpeg.

## Tech-Stack

- **Backend:** Go 1.22, stdlib `net/http` + `chi/v5`
- **DB:** `modernc.org/sqlite` (pure Go, kein cgo)
- **Frontend:** Vanilla HTML/CSS/JS + **Video.js 8.x** (VHS für HLS, lokal gebundelt, kein CDN).
  Aufgeteilt in 10 fokussierte JS-Module (siehe „Frontend-Modul-Layout" unter Verzeichnisstruktur).
  Trickplay-Hover-Thumbnails sind als eigenes Mini-Plugin **inline in `player.js`** implementiert
  (das externe `videojs-vtt-thumbnails`-Plugin ist raus — war nie im Repo gebundelt,
  HEAD-Check lief gegen 405).
- **Video:** ffmpeg mit `intel-media-va-driver` (iHD) + `libx264`-Fallback
- **TMDB:** v3 API mit eigenem Rate-Limit-Wrapper (35 req/10 s), **OMDb** als Fallback
- **Auth:** `golang.org/x/crypto/bcrypt`, HttpOnly-Session-Cookies (SameSite=Lax)
- **Container:** Debian bookworm-slim, Multi-Stage-Build, CGO_ENABLED=0
- **Deploy:** Portainer-Stack auf Unraid (<UNRAID-LAN-IP>:9000 → Endpoint 3, Stack-ID 37)
- **Tests + Tooling:** Tabellen-Tests in `internal/nameparser/parser_test.go` (88,8 % Coverage)
  und `internal/playback/decider_test.go` (Decider 100 %). Linter: `golangci-lint` (Go) und
  `biome` (JS/CSS) — beides empfohlen vor jedem Refactor laufen lassen.
- **Pre-Deploy-Schutz:** `scripts/check-frontend.sh` ruft `node --check` ueber alle
  embedded JS-Files. Lokal als pre-commit-Hook (Installation: `./scripts/install-git-hooks.sh`)
  und in CI (`.github/workflows/deploy.yml`) zwingend vor dem Build. Hat zuletzt einen
  „deutsche Anfuehrungszeichen mit ASCII-` " ` mittendrin"-Bug abgefangen, der die
  komplette Frontend-App tot gemacht haette. **Niemals `node --check` ueberspringen
  bei JS-Aenderungen.**

## Verzeichnisstruktur

Standard-Go-Projektlayout (`cmd/`, `internal/<paket>/`, `scripts/`, `.github/workflows/`) — siehe `ls`/`find` fuer die aktuelle Struktur.


### Frontend-Modul-Layout (Stand 2026-04-30, ABGESCHLOSSEN)

`internal/webassets/web/` enthaelt mehrere kleine, fokussierte JS-Dateien
(plain `<script>`-Tags, keine ES-Modules — gemeinsamer window-Scope) plus
HTML/CSS. Die Lade-Reihenfolge in `index.html` ist relevant, weil spaetere
Module Funktionen aus frueheren nutzen.

Module unter `internal/webassets/web/` (helpers, dialogs, api, cast, player-components, cards, views, grid, player, player-trickplay, player-transcode-seek, player-buffer, admin, playlists, scan, matching, whisper, introskip, ocrsub, music) + app.js — Groessen per `wc -l`, Zweck per Dateikopf-Kommentar. player-trickplay/-transcode-seek/-buffer seit 2026-09-06 (Schritt 5 der Modularisierung, siehe „Code-Review 2026-09-06" unten) aus player.js ausgelagert.


**Lade-Reihenfolge in index.html:**
```
helpers → dialogs → api → cast → player-components → cards → views → grid → player → player-trickplay → player-transcode-seek → player-buffer → admin → playlists → scan → matching → whisper → introskip → ocrsub → music → app
```

Refactor-Verlauf: app.js startete bei 7531 Zeilen und endete bei **1371 Zeilen (−82 %)**. Jeder Modul-Schritt war ein eigener Commit auf einem `code-review/app-js-split-*`-Branch, danach in main gemerged + live deployed + im Browser getestet.

**Nicht ueber `<script type="module">` nutzen** — die Funktionen referenzieren sich global via window-Scope, plus der ES-Module-Loader hat Quirks bei `defer`-Reihenfolge. Plain `<script defer>` mit korrekter HTML-Reihenfolge ist hier die einfachste, korrekte Loesung.

**Bei weiteren Aenderungen an Modul-Boundaries:** awk-Trim mit Multi-Block-Extraktionen muss ALLE Phasen-Endbedingungen (`in_block=0`-Resets) VOR der generischen `if (in_block) next`-Skip-Aktion pruefen. Sonst feuert der Skip aus Phase A's Action-Block fuer Phase B's in_block-Periode und verschluckt alles bis Dateiende. War einmal passiert (admin.js-Trim, korrigiert).

## Architektur

### Laufzeit

- HTTP-Server auf `:8096` (im Container), gemappt auf **`8098`** am Host
  (8096 wird bereits von bestehender Jellyfin-Instanz im host-Netzwerkmodus belegt).
- Hintergrund-Goroutine `enrich.Worker` läuft alle 5 Minuten und nach jedem Scan-Ende
  (via Scanner.OnComplete-Callback) sowie on-demand per `Trigger()`.
- **Startup-Backfill**: `backfillEpisodeRanges` läuft einmalig beim Container-Start,
  wenn `settings.episode_range_backfill_v1 != "1"`. Parsed alle Episoden-Items mit
  zwei 'E' im Pfad auf Doppelfolgen-Ranges und schreibt `items.episode_end`. Flag
  wird am Ende gesetzt, Restart-idempotent.
- Transcode-Sessions werden pro `(itemID, profileID, startSec)` gehalten und nach
  5 min Inaktivität per GC-Loop beendet; Cache unter
  `/config/cache/{sessionID}-{suffix}/`.
- **🔴 Harte Obergrenze gleichzeitiger Video-Transcodes (seit 2026-09-17,
  v1.4.1) — der wichtigste Stabilitätsschutz des Servers.** Ein User-Test am
  2026-09-16 („wie viele 4K-Transcodes schafft die iGPU?") ergab: **vier
  liefen stabil, acht rissen den GESAMTEN Unraid-Host mit** — kompletter
  Reboot nötig, nicht nur ein Container-Neustart. Ursache: `StartOrGet`
  startete bis dahin **bedingungslos** für jede Anfrage einen weiteren
  ffmpeg-Prozess; es gab überhaupt kein Limit. Jetzt zwei unabhängige
  Verteidigungslinien, **beide sind nötig**:
  1. **App-Limit** (`Manager.maxSessions`, Default `playback.DefaultMaxSessions`
     = 4, einstellbar im Zahnrad-Menü → Einstellungen, persistiert als
     `settings.max_transcodes`). Am Limit liefert `StartOrGet`
     `ErrTooManySessions`, der API-Layer macht daraus **HTTP 503 +
     `Retry-After: 30`** mit einer für Endnutzer lesbaren Meldung (bewusst
     kein 500 — es ist ein temporärer Zustand). **Eine abgelehnte Wiedergabe
     ist immer besser als ein toter Server.**

     **🔢 Seit v1.4.2 GEWICHTET statt stur nach Anzahl** (`transcodeCost`):
     gezählt werden Kostenpunkte, nicht Sitzungen. Der eingestellte Wert ×
     `CostFullBudgetUnit` (100) ergibt das Budget. Grundlage ist eine
     **Messung auf der echten Hardware** (VAAPI, 60 s Material je Lauf,
     zweifach wiederholt, Werte stabil):

     | Quelle → Ziel | Dauer | relativ |
     |---|---|---|
     | 4K HEVC → 2160p | 15,0 s | 1,00 |
     | 4K HEVC → 1080p | 6,6 s | 0,44 |
     | 4K HEVC → 720p | 5,6 s | 0,37 |
     | 1080p → 1080p | 5,8 s | 0,39 |
     | 1080p → 720p | 2,7 s | 0,18 |
     | 1080p → 480p | 1,8 s | 0,12 |

     **⚠ Die QUELLE geht mit in die Kosten ein, nicht nur das Ziel** —
     dasselbe Ziel (720p) kostet aus einer 4K-Quelle 5,6 s, aus einer
     1080p-Quelle nur 2,7 s, weil das Dekodieren unabhängig vom Ziel
     anfällt. Eine reine Ziel-Gewichtung wäre falsch. Unterhalb von 1080p
     flacht die Kurve ab (dann dominiert der Decode). Punktwerte bewusst
     nach OBEN gerundet; unbekannte Quellhöhe (`srcHeight = 0`) gilt als 4K.
     `StartOrGet` bekommt `srcHeight` deshalb als zusätzlichen Parameter
     (aus `items.height`).

     **Zusätzlicher Anzahl-Deckel** (`maxSessionsHardCapFactor` = 3): lauter
     billige Sitzungen würden sonst rechnerisch über zwanzig ffmpeg-Prozesse
     erlauben — die belasten die Grafikeinheit kaum, kosten aber je Prozess
     Speicher und Dateihandles.
  2. **Container-Deckel** in `docker-compose.yml` (`mem_limit`, `cpus`,
     `cpu_shares`). Fängt alles ab, was die App nicht kennt (ffmpeg-Ausreißer,
     Speicherleck, Worker parallel zu Wiedergaben). Ohne das darf der
     Container beliebig RAM ziehen, bis der Host-OOM-Killer zuschlägt — und
     der trifft nicht zwingend nur ffmpeg.
     **LIVE eingetragen in Portainer-Stack 37 am 2026-09-17** (vorher
     nachgemessen: `Memory: 0`, `NanoCpus: 0` — der Container lief komplett
     ohne Grenzen). Gesetzte Werte am Referenz-Host (Tower, Unraid 7.2,
     **20 Kerne / 31,2 GB RAM**): `mem_limit: 12g`, `cpus: 14.0`,
     `cpu_shares: 512`. Verifiziert per Docker-Inspect nach dem Redeploy
     (`Memory: 12884901888`, `NanoCpus: 14000000000`).
     **⚠ KEIN `memswap_limit` auf Unraid** — `docker info` meldet dort
     `SwapLimit: false`, die Option erzeugt nur eine Warnung.
     **⚠ Beim Stack-Redeploy IMMER das bestehende `Env`-Array mitschicken**
     (GET `/api/stacks/37` → `s.Env` → in den PUT-Body), sonst sind die vier
     OIDC-Variablen weg und SSO antwortet mit 503. Portainer 2.39 verlangt
     zusätzlich einen **CSRF-Token**: aus dem `x-csrf-token`-Response-Header
     einer vorherigen GET-Anfrage lesen und als `X-CSRF-Token`-Header
     mitsenden, sonst kommt „403 Forbidden — CSRF token not found".

  **Bewusste Details, nicht „aufräumen":** der Limit-Check sitzt NACH dem
  Sibling-Cleanup (dort frei gewordene Plätze zählen mit, sonst scheitert
  simples Spulen fälschlich) und NACH dem `m.sessions[id]`-Treffer (eine
  BESTEHENDE Session weiterzubenutzen ist nie limitiert, sonst bricht ein
  laufender Film beim nächsten Playlist-Reload ab). **Reine Audio-Sessions
  (Musik) zählen nicht mit** (`activeVideoSessionsLocked`) — Musik darf
  keinen Filmplatz wegnehmen. Tests: `internal/playback/session_limit_test.go`.
- **⚠ Der eindeutige Suffix der Session-Verzeichnisse darf NICHT nur aus
  einem Zeitstempel bestehen** (gefixt 2026-09-17): er war
  `time.Now().UnixNano()` — und das ist NICHT kollisionsfrei, weil die
  Uhr-Auflösung plattformabhängig ist. `TestSessionDirsAreUniquePerStart`
  schlug deshalb reproduzierbar fehl (auf macOS liefern zwei unmittelbar
  aufeinanderfolgende Aufrufe denselben Wert). Bei einer Kollision entsteht
  exakt wieder der Zustand, gegen den der Suffix eingeführt wurde: altes und
  neues ffmpeg schreiben in DASSELBE Verzeichnis → korrupte Playlist. Jetzt
  zusätzlich ein monotoner `atomic.Uint64`-Zähler (`sessionDirName`). Der
  Test prüft seither die **echte Produktionsfunktion** — vorher hatte er eine
  eigene Kopie der Formel nachgebaut und dadurch den Bug verdeckt: **Tests
  nie gegen eine nachgebaute Kopie der zu prüfenden Logik schreiben.**
  Die Key-Bildung liegt jetzt zentral in `sessionKey()`.
- **⚠ Das Session-Verzeichnis trägt einen eindeutigen Suffix — nicht entfernen**
  (seit 2026-09-13): `Session.Stop()` wartet nur **3 s** auf das Ende von
  ffmpeg und löscht danach `s.Dir`. Ein langsam sterbender Prozess (HEVC-Decode
  via VAAPI braucht gelegentlich länger) schreibt darüber hinaus weiter. Ohne
  Suffix legt `StartOrGet` für denselben Session-Key sofort DENSELBEN Pfad neu
  an — altes und neues ffmpeg schreiben dann ins gleiche Verzeichnis,
  überschreiben wechselseitig `index.m3u8` und vergeben `seg*.ts`-Nummern
  doppelt. Der Client bekommt eine korrupte Playlist und meldet einen
  Datenstromfehler. Ausgelöst wurde das von `fresh=1` (Seek oder neuer
  Player-Open bei laufender Session), reproduziert mit HTTP 500 auf die
  Playlist-Anfrage. **`cleanStaleSessionDirs` beim Manager-Start räumt
  verwaiste Verzeichnisse weg** — sein `sessionDirPattern` ist bewusst eng
  gefasst, weil im selben `cacheDir` auch `downloads/` und `trailers/` liegen,
  die NIEMALS angefasst werden dürfen (beide beginnen nicht mit `<ziffern>-`
  und können daher gar nicht matchen). Tests:
  `internal/playback/session_dir_test.go`.
  **⚠ `-d<0|1>` und der Suffix sind im Muster OPTIONAL** (seit 2026-09-14) —
  beide kamen erst nachträglich dazu, der Aufräumer selbst zusammen mit dem
  Suffix. Ohne die optionalen Gruppen war er für JEDES vorher angelegte
  Verzeichnis blind: am 2026-09-14 lagen **268 von 269** Session-Ordnern im
  alten Schema (`183130-orig-a-1-378-d0`, noch älter `21819-orig-a-1-0`) und
  damit rund **119 GB** dauerhaft im Cache, die nie jemand gelöscht hätte.
  Regel daraus: **wer das Namensschema der Session-Verzeichnisse ändert, muss
  im selben Zug prüfen, ob `sessionDirPattern` den Altbestand noch matcht** —
  sonst wächst der Cache still und unbegrenzt. `TestSessionDirPatternMatchesLegacyDirs`
  hält die Alt-Schemata fest.
- **ffmpeg-stderr landet im Log** (seit 2026-09-13): der Transcode-Prozess
  läuft mit `-loglevel error`, sein stderr wurde vorher per `io.Discard`
  komplett verworfen — scheiterte eine Wiedergabe, stand NICHTS im Log und die
  Diagnose war blind. Jetzt hält ein `ringBuffer` die letzten 4 KB und gibt sie
  aus, wenn der Prozess mit Fehler endet. Ein per Kontext gekillter Prozess
  (`fresh=1`, GC) ist der Normalfall und schweigt weiterhin.

## Features

### Flat-View & Bulk-Selection
- **Flat-View-Toggle** im Toolbar (📂) — blendet die Ordner-Navigation aus und
  zeigt alle Videos der Library flach. Persistiert in `localStorage`.
- **Bulk-Auswahl** via `☑ Auswählen`-Button: aktiviert `selection-mode`
  (CSS-Klasse auf `body`), blendet Checkboxen auf jeder Kachel ein. Click im
  Mode togglet Auswahl statt Detail zu öffnen.
- Sticky Action-Bar: `Alle · Keine · ♡ Favorit · ✓ Gesehen · 📋 Playlist ·
  ⬇ Download · 🗑 Löschen`. Bulk-Download triggert `<a download>`-Clicks
  mit 400 ms Abstand (Browser-Popup-Blocker).
- **Gesamtgröße neben der Anzahl** (seit 1.3.1, User-Wunsch 2026-09-10):
  `#bulkCount` zeigt „N ausgewählt · 12,4 GB" statt nur „N ausgewählt" —
  Summe über `sizeBytes` der `selectedItems()` (dieselbe Quelle, die auch
  die Bulk-Aktionen selbst nutzen, also konsistent mit dem, was z. B.
  Bulk-Download tatsächlich überträgt), formatiert mit dem bestehenden
  `fmtSize()`-Helper. Kein Größen-Suffix, wenn Summe 0 ist (Musik-Items o.
  ä. ohne `sizeBytes`).
- `state.lastRenderedItems` speichert die gerade gerenderte Liste — „Alle
  auswählen" arbeitet darauf.
- **Shift-Klick-Bereichsauswahl** (seit 1.3.0, User-Wunsch 2026-09-10):
  im Auswahl-Modus eine Kachel normal anklicken, dann eine spätere Kachel
  mit gehaltener Shift-Taste anklicken → alle dazwischen liegenden Kacheln
  werden mit ausgewählt (Finder/Explorer-Konvention). Reihenfolge kommt aus
  `state.lastRenderedItems` — exakt die zeilenweise links-nach-rechts
  gerenderte Grid-Reihenfolge, dieselbe Quelle wie „Alle auswählen".
  `state.selectionAnchorId` merkt sich die letzte NORMAL (nicht per Shift)
  angeklickte Kachel als Ausgangspunkt; bleibt über mehrere Shift-Klicks
  hinweg stehen, wird nur von einem normalen Klick (`toggleSelection`) neu
  gesetzt und bei `setSelectionMode(false)`/„Keine" zurückgesetzt.
  `selectRange(anchorId, targetId)` (`app.js`) sucht beide Indizes in
  `lastRenderedItems`, markiert den gesamten Bereich dazwischen (inklusive,
  Richtung egal) als ausgewählt. Fallback auf normales Einzel-Toggle, falls
  der Anker nach einem Such-/Filterwechsel nicht mehr in der aktuellen
  Liste steckt. Click-Handler in `cards.js` (gemeinsamer `data-item-id`-Pfad
  wie der normale Bulk-Toggle) — bewusst nur für die Kachel-Grid-Ansicht,
  nicht für die Musik-Listenzeilen (`.track-row`) mitgebaut, da nicht
  angefragt.

### UI
- **Dialoge (`.modal`) haben seit 2026-09-01 einen fixen Kopf + Fuß**
  (`.app-dialog` = appConfirm/appPrompt ausgenommen, scrollt nie).
  **Aktuelle Lösung (seit 2026-09-02, `helpers.js normalizeModalLayout()`):
  echtes Flex-Layout statt `position: sticky`.** Ein Monkey-Patch auf
  `HTMLDialogElement.prototype.showModal` baut jeden `.modal`-Dialog beim
  ERSTEN Öffnen strukturell um: erkennt Kopf (führendes `.modal-close` +
  `<h2>`) und Fuß (letzte `.row`/`.modal-actions`, auch wenn sie in einem
  `<form>` steckt), löst beide aus dem scrollenden Bereich heraus in eigene
  nicht-scrollende Flex-Items, dazwischen ein `<div class="modal-scroll">`
  mit normalem `overflow-y:auto`. Rein strukturelle Erkennung — **kein
  einzelnes Dialog-Markup muss angefasst werden.** Formular-Buttons, die
  dabei optisch außerhalb ihres `<form>` landen, bekommen `form="<id>"`,
  damit `type=submit` weiter dasselbe Formular auslöst. Dialoge ohne
  erkennbaren Kopf (kein führendes `<h2>`, z. B. `detailDialog`) werden
  übersprungen und behalten die ALTE sticky-CSS als Fallback (`.modal h2`,
  `.modal .row:last-child`, `box-shadow`-Deckung der `.modal`-eigenen
  Padding-Zone — siehe style.css-Kommentare).
  **Warum kein `position: sticky` mehr:** funktionierte in Chrome/Safari
  (mit `box-shadow` statt riskantem negativem `margin`, s. Memory), war
  aber in **Firefox unzuverlässig** — `position: sticky` innerhalb eines
  `<dialog>`-Elements ist dort ein bekannter Browser-Bug (User-Report
  2026-09-02, auf Nachfrage als Firefox bestätigt). Das neue Flex-Layout
  verzichtet komplett auf sticky und funktioniert dadurch browserunabhängig.
  Details + Debugging-Verlauf: Memory `project_feature_user_home_and_sticky_dialogs`.
- Grid-Ansicht mit Kachel-Thumbnails oder TMDB-Poster. Movies/TV-Libraries nutzen
  **2:3-Poster-Kacheln** (`card--poster`), Private-Libs weiterhin 16:9-Thumbnail.
- **Private-Libs (YouTube, Urlaubsvideos, …)**: Kachel zeigt den **Dateinamen**
  (ohne Extension) als Titel. Der Top-Ordnername wird NICHT auf der Kachel
  angezeigt — er ist im Breadcrumb und im Detail-Dialog (rel_path) ohnehin
  sichtbar. Default-Sort in privaten Libs ist „Veröffentlicht" **aufsteigend**
  (älteste zuerst) — in `restoreSortForContext()` anhand `lib.kind === "private"`
  gesetzt. Manuell geänderter Sort wird weiterhin pro Lib+Ordner in
  `localStorage` unter `sort:lib:<libID>:<folder>` persistiert.
- **Zweite Kachel-Zeile (`.card-filename`) zeigt nie denselben Text doppelt**
  (seit 2026-09-05, `cards.js renderCard`): die Bedingung ist NICHT (mehr)
  `itLib.kind === "private"`, sondern generisch `title === rawTitle` —
  `rawTitle` ist die unveränderte Server-Vorgabe (Dateiname ohne Endung).
  Immer wenn `title` NIRGENDS überschrieben wurde (kein TMDB-Match, kein
  SxxExx-geparster Show-Name bei unmatched TV-Items, kein Custom-Match) und
  ein Ordnerpfad existiert, zeigt die zweite Zeile den **Ordnerpfad** statt
  den Dateinamen ein zweites Mal. Ursprünglich nur für Privat-Libs gefixt
  (Commit `a3d9f44`), dann exakt dasselbe Muster bei unmatched TV-Episoden
  gemeldet (z. B. Tatort-Folgen ohne SxxExx im Dateinamen) — daher generisch
  gemacht statt library-kind-spezifisch. Root-Level-Dateien ohne Unterordner
  bleiben eine bekannte Restlücke (kein Ordnerpfad zum Anzeigen vorhanden).
  **Der Titel-/Jahr-Override (`title = it.metadata.title`) läuft in BEIDEN
  Zweigen von `renderCard`** — Poster UND reiner Thumbnail. Saß er nur im
  `posterPath`-Zweig, zeigte jede Custom-Zuordnung ohne Poster-Upload (und
  jeder TMDB-Treffer ohne Poster-URL) weiterhin den rohen Dateinamen als
  Kachel-Titel.
- **Dateinamen-Zeile bei Filmen/Serien ein-/ausblendbar (seit 2026-09-08,
  User-Wunsch):** getrennte Schalter im Zahnrad-Menü → „🔤 Anzeige" (siehe
  „Anzeige-Einstellungen" unten), `state.showFilenameMovies`/
  `showFilenameTv` (localStorage, Default an). `renderCard` (`cards.js`)
  berechnet `showFilenameLine` daraus (nur für `kind=movies`/`kind=tv` —
  Privat/Musik bleiben unberührt, dort ist die Zeile Teil des normalen
  Titel-Layouts) und umschließt den `.card-filename`-Block damit. Die
  Inhalts-Logik selbst (Dateiname vs. Ordnerpfad bei `title === rawTitle`,
  siehe oben) ist unverändert — der Schalter blendet nur die ganze Zeile
  komplett aus.
- Breadcrumb-Navigation: Root zeigt Ordner-Kacheln + Root-Items,
  Klick auf Ordner zeigt alle Dateien **flach** (rekursiv, keine weitere Tiefe).
- **Drilldown-Toggle:** Pro Ordner kann per Hover-⚙-Icon (Admin-only) eingestellt werden,
  ob tiefere Unterordner als eigene Kacheln erscheinen (`folder_nav.enabled`).
- **Topbar ist `position: fixed`** — Body bekommt `padding-top: var(--topbar-h)`,
  das via ResizeObserver in `boot()` an die tatsächliche Topbar-Höhe gekoppelt
  wird (sonst verrutscht Content beim Umbruch auf 2 Zeilen).
- **`.topbar-corner`** (Username + Zahnrad-Menü) ist `position: absolute;
  top/right` innerhalb der Topbar — bleibt immer oben rechts, auch wenn die
  Controls wrap'en. Controls haben `padding-right: 220px`, damit sie nicht
  unter die Ecke laufen.
- Filter: Suche, Datum-Von/Bis (tagesgenau), Sortierung (Name/Veröffentlicht/Hinzugefügt/Laufzeit),
  Gesehen-Status (Alle/Nur ungesehen/Nur gesehen), **Auflösungs-Filter** (Multi-Select-Buckets).
- **Sort-Dropdown enthält Pseudo-Modi** `favorites`, `unmatched`, `duplicates`
  — bisher eigene Filter-Felder, absorbiert. `currentSortMode()` gibt nur
  gültige Sort-Werte weiter (fällt auf `title` zurück), `currentFavoriteMode()`
  + `currentMatchMode()` lesen die Pseudo-Modi aus.
- **Suchfeld** trifft Titel **und** Schauspielernamen. SQL joined
  `metadata_cast` via `people.name LIKE ?`, inkl. Parent-Show bei Episoden.
  In Collections-Views client-seitig zusätzlich auf Collection-Name / Part-Titel.
  **Episoden-Treffer werden pro Serie** (`libraryId` + erstes rel_path-Segment)
  **zu EINER Sammelkachel gebündelt** (`appendSearchResultCards` /
  `renderSearchShowCard` in `cards.js`), Klick öffnet den Serien-Ordner —
  Filme/Privatvideos bleiben Einzelkacheln. Gilt für Library-Suche UND
  Home-View-Global-Suche.
- **Akzent-/Diakritika-unempfindlich (seit 2026-09-13, LIVE 1.3.23,
  User-Wunsch: "senorita" soll auch "Señorita" finden, gilt für ALLE
  Plattformen/Server):** rein server-seitig gelöst, damit Browser/Android/
  Apple/Linux automatisch alle profitieren — kein Client ruft eine eigene
  lokale Textsuche auf, alle delegieren an `GET /api/items?search=`.
  `internal/store/unaccent.go` registriert `UNACCENT(x)` als eigene
  deterministische SQLite-Skalarfunktion (analog `registerNaturalCollation`/
  `COLLATE NATSORT`): NFD-Zerlegung → alle Unicode-`Mn`-Zeichen
  (Kombinationszeichen) entfernen → NFC. `ItemFilter.Search`s LIKE-Klausel
  (`internal/store/items.go`) wrapt `i.title`/`m.title`/`i.artist`/
  `i.album`/`p.name` (Cast-Suche) jeweils mit `UNACCENT(...)`, der Go-seitige
  Suchbegriff selbst läuft ebenfalls durch `unaccent()` — SQLite braucht so
  kein zusätzliches `LOWER()` (LIKE ist für den verbleibenden ASCII-Bereich
  ohnehin case-insensitive). Test: `internal/store/unaccent_test.go`.
  **Nebenbei-Fund beim Umsetzen:** `go get golang.org/x/text@latest` hätte
  `go.mod`s `go`-Direktive von 1.24.0 auf 1.26.0 angehoben (bricht das
  `golang:1.24-bookworm`-Docker-Pin) — sofort per `git diff go.mod`
  bemerkt und stattdessen `golang.org/x/text v0.17.0` (schon länger
  transitive Abhängigkeit, braucht selbst nur `go 1.18`) als direkte
  Abhängigkeit gepinnt.
- **Alphabet-Sidebar rechts** (`#alphaSidebar`, seit 2026-07-12 immer sichtbar):
  zeigt A-Z + `#`, unabhängig vom Sort-Feld — wirkt als **Filter** (nicht
  Scroll-Sprung): `jumpToLetter()` → `setAlphaFilter()` blendet Kacheln, die
  nicht mit dem gewählten Buchstaben starten, per `.alpha-hidden`-Klasse aus
  (Toggle bei erneutem Klick, ✕-Chip im Breadcrumb-Banner hebt den Filter
  ebenfalls auf). Body bekommt Klasse `has-alpha-sidebar`, dann nimmt das
  Grid 36px Rand rechts frei.
  **Per-Ordner-Toggle** (seit 2026-09-08 im Zahnrad-Menü statt Topbar, siehe
  „Anzeige-Einstellungen" unten) blendet die Leiste NUR für den aktuell
  offenen Ordner aus/ein — localStorage `alphaSidebar:<libID>:<folder|"root">`
  = `"0"` (ausgeblendet) oder kein Key (Default an). Eigener Namespace,
  unabhängig von `sort:lib:…`/`seasonView:…`. In der Collections-Root-Ansicht
  (`state.currentLibrary === null` dort) gibt es keinen Toggle — die Leiste
  bleibt dort wie bisher unconditional an.
  **Scope-Verhalten (Endstand nach zwei Korrektur-Runden am 2026-09-06):** der
  WERT (`state.alphaFilter`) bleibt bibliotheksweit erhalten — der Reset in
  `loadItems()` vergleicht nur `navLibraryKey(key)` (reduziert
  `"lib:7:Billions:s1"` → `"lib:7"`, reicht Nicht-`lib:`-Keys unverändert
  durch), feuert also nur beim echten Wechsel der Bibliothek oder des
  Top-Level-Kontexts, nicht bei jedem Ordner-/Staffel-Wechsel darin.
  ANGEWENDET wird er dagegen nur an genau der Stelle, an der er gesetzt wurde:
  `state.alphaFilterScopeKey` merkt sich den `navKey()` des Sidebar-Klicks,
  `applyAlphaFilter()` blendet nur bei `scopeKey === navKey()` aus. So bleibt
  der Filter beim Zurücknavigieren aktiv, greift aber nicht in einer geöffneten
  Serie (deren Episodentitel selten mit demselben Buchstaben beginnen). Banner-
  und Sidebar-Markierung werden zentral in `applyAlphaFilter()` gepflegt, nicht
  verstreut in `setAlphaFilter()`.
- Favoriten-Filter zeigt flach ohne Ordner-Ebenen; Scope „nur nach unten flach"
  wie die Flat-Sorts — im Library-Root library-weit, in einem Unterordner nur
  dessen Favoriten (rekursiv). Breadcrumb hat dann einen Zurück-Pfeil.
- **Library-Wechsel** setzt alle Filter/Suche/Sortierung zurück.
- **Request-Sequencing** in `loadItems` (`state.loadSeq`): verhindert, dass bei
  schneller Live-Suche ein älterer Response das Grid mit stale Daten überschreibt.
- Auto-Sort bei TV-Subfolder: `episode` (nach Staffel+Episode aus TMDB-Metadata).
- Detail-Dialog mit Poster, Plot, Rating, Genres, Episode-Info + Buttons:
  „Abspielen" / „Als gesehen markieren" / „♡ Favorit" / „Zu Playlist" / „Download" /
  „Löschen" (Admin-only) / „Manuell zuordnen" (Movies/TV). Hat außerdem zwei
  Dropdowns **🔊 Tonspur / 💬 Untertitel** (`streamsInfoHTML` +
  `wireDetailAVSelects` in `player.js`). Datenquelle: `/api/playback/{id}`
  (kennt auch die erzeugten OCR/KI-Untertitel; `item.streams` allein nicht).
  Untertitel-Dropdown zeigt **nur einblendbare** Spuren (📝 OCR / 🎤 KI + echte
  Text-Subs) — Bild-Untertitel (PGS/VOBSUB) sind komplett raus, im Player-
  Untertitel-Dropdown ebenfalls. Die Auswahl landet in `state.detailPrefs`
  und `openPlayer` reicht sie an den Player durch: Tonspur → `&audio=` (nur
  wenn vom Standard abweichend), Untertitel → `#subSelect` wird vor
  `applySubtitleChoice` vorbelegt → Player startet direkt mit der Wahl.
  **Auflösung + Dateigröße in der Sub-Zeile** (seit 2026-09-07, User-Report:
  "In den Infofenstern steht nirgends die Auflösung und die Dateigröße"):
  eigenes `#detailResSize`-Element (`resSizeHTML()` in `player.js`) — vorher
  standen beide Werte NUR bei Items ohne TMDB-Zuordnung im Detail-Dialog
  (Auflösung war sonst nur außen auf der Kachel sichtbar, Dateigröße nur im
  Varianten-Dropdown ab 2 Varianten). Wird beim Wechsel im Varianten-Dropdown
  mit aktualisiert (die übrige Sub-Zeile — Jahr/Genres/Rating — bleibt
  unverändert, das sind Metadaten der Show/des Films, nicht der Einzeldatei).
- **Datei-/Pfad-Suche (admin)** im Zahnrad-Menü unter „🔍 Datei/Ordner suchen":
  Diagnose-Dialog (`#pathSearchDialog`), ruft `GET /api/items/search-path?q=`,
  matcht auf rel_path + path + title, zeigt aktuelle TMDB-Zuordnung. Klick
  öffnet den Standard-Detail-Dialog zum manuellen Umzuordnen.
- **Verdächtige Zuordnungen** im Sort-Dropdown: `⚠ Verdächtige Zuordnungen`
  zeigt Items, wo Token-Overlap zwischen Top-Folder und Metadata-Titel = 0
  UND keine Jahresübereinstimmung. Für Episoden wird gegen den **Parent-Show-
  Titel** verglichen (sonst würden alle Folgen „verdächtig" sein).
  Bestätigte Items (metadata_confirmed=1) werden ausgefiltert.
  Backend: `GET /api/items/suspicious`, Store: `store/suspicious.go`.
- **⧉ Datei in anderem Ordner (`namedupes`) am 2026-09-02 komplett entfernt**
  (Dropdown, Frontend-Branch, Backend-Endpoint `GET
  /api/libraries/{id}/name-dupes`, `store/namedupes.go`) — war eine echte
  Teilmenge von `simnames` (jedes exakte Größen-Duplikat hat zwangsläufig
  auch gleiche Auflösung+Länge) und brachte zuletzt immer 0 Treffer, weil
  `simnames` die verbliebenen Fälle längst gefunden+bereinigt hatte
  (User-Bestätigung). Für Doppel-Kopien jetzt **≈ Ähnliche Dateinamen**
  nutzen.
- **≈ Ähnliche Dateinamen** im Sort-Dropdown (`simnames`, seit 2026-08-31):
  Fast-Duplikate — Dateiname nach Normalisierung (klein, ohne Endung, ohne
  `" (N)"`-Kopiesuffix, `._-` → Leerzeichen) zu **≥ threshold** (Query-Param,
  Default 0.9, per normalisierter Levenshtein-Ähnlichkeit) identisch **UND**
  exakt gleiche Auflösung (width×height) **UND** Laufzeit ±1 s. Fängt
  `film.mp4` ↔ `film (2).mp4` und `film.wmv` ↔ `film.mp4`, die der strenge
  `duplicates`-Filter (gleiche `metadata_id`) nicht sieht. Folder-gescoped
  (rekursiv) wenn man in einem Ordner steht, sonst ganze Library — Matching
  läuft NUR innerhalb des Scopes. `item.dupeOtherPaths` + orangener
  ⧉-Badge (Overlay `top:66 right:6`); die „↳ auch: …"-Zeile in `renderCard`
  zeigt bei Zwillingen im GLEICHEN Ordner den Dateinamen statt des
  (identischen) Ordnerpfads. Backend: `GET /api/libraries/{id}/similar-names?folder=&threshold=`,
  `store/simnames.go` (`SimilarNameDupes` — Auflösungs-Bucket + Dauer-
  Sliding-Window ±1 s + Union-Find; `normalizeSimName`, `levenshtein`,
  `nameSimilarity`; Tests in `simnames_test.go`). Registriert in
  `currentSortMode`/`PSEUDO_FILTER_MODES`/`directionless` + `simNamesView`
  in `renderBreadcrumb`.
  - **`differsOnlyInDigits`-Guard (seit 1.0.19):** ein Paar wird verworfen,
    wenn die normalisierten Namen gleich lang sind und ALLE abweichenden
    Positionen beidseitig Ziffern sind — durchnummerierte Geschwister
    (FTV-Shoot-IDs `alana-7127-07` vs `alana-7128-01`, Episoden `s01e03`
    vs `s01e04`) sind keine Duplikate. `film (2)` / `.wmv` fallen NICHT
    darunter (Klammer/Endung vorher gestrippt → identisch). Hat FTV-
    Fehltreffer von 305 auf 4 gedrückt (echte `(1)`-Kopien).

### Startseite (Home-View)
- Default-Ansicht beim ersten Öffnen (`state.homeView = true`) + 🏠-Button in
  der Topbar. Zeigt pro Library einen eigenen Block mit drei Streifen:
  **▶ Fortsetzen** (Items mit Resume-Position), **📺 Als nächstes** (nächste
  ungesehene Episode je Serie, nur TV-Libs), **🆕 Zuletzt hinzugefügt**.
- Libraries mit `on_home=0` (`libraries.on_home`) werden komplett
  ausgeblendet — das ist der globale Default für User ohne eigene Auswahl.
  **Keine Admin-UI mehr dafür** (Checkbox seit 2026-09-02 aus dem
  Library-Manager entfernt, war doppelt gepflegt) — der Wert bleibt nur im
  Schema als Fallback, neue Libraries starten mit `on_home=1`.
- **Pro-User-Overrides, DREI unabhängige Achsen** (seit 2026-09-01/02, im
  „🏠 Startseite anpassen"-Dialog — Zahnrad-Menü ODER Button direkt auf der
  Startseite, `views.js openHomePrefsDialog`, für jeden User verfügbar, auch
  Admins). **Bewusst GETRENNTE Datenmodelle** — ein erster Versuch, alles
  über eine gemeinsame Tabelle zu steuern, hatte einen echten Nebeneffekt
  (Libraries, die nur im Reiter gewünscht waren, tauchten ungewollt im
  globalen „Fortsetzen"-Streifen auf); User-Anforderung danach explizit:
  „das muss alles separat gesteuert werden können":
  1. **Globale Streifen** „▶ Fortsetzen"/„📺 Als nächstes" — pro User
     ein-/ausblendbar. Generische Pro-User-KV-Tabelle
     `user_settings (user_id, key, value)`, Keys `home_show_continue`/
     `home_show_nextup`, Default an. `PUT /api/home/strips` togglet sie;
     `GET /api/home` UND `/api/home/preferences` liefern beide Flags mit.
     Serverseitige Berechnung von continue/nextUp bleibt unverändert (das
     Frontend entscheidet nur, ob es rendert), `renderHomeView` prüft
     `data.showContinue`/`data.showNextUp`.
  2. **Bibliotheks-Reiterleiste (Topbar)** — eigene Tabelle `user_nav_prefs
     (user_id, library_id, on_nav, sort_order)`, eigener Store
     (`internal/store/nav_prefs.go`), eigene API (`internal/api/nav.go`:
     `GET/PUT /api/nav/preferences[/{id}]`, `PUT /api/nav/order`). Frontend:
     `app.js visibleOrderedLibraries()` filtert/sortiert `state.libraries`
     (aus `state.navPrefs`, in `loadLibraries()` mitgeladen) für
     `renderLibNav()` — `state.libraries` selbst bleibt unangetastet
     (Library-Manager/ACL-Editor brauchen die volle Liste). Die **globalen
     ▲▼ im Library-Manager sind deshalb seit 2026-09-02 entfernt**
     (redundant, siehe „Bibliotheken & Medien" oben).
  3. **Startseite** (welche Libs + welche Reihenfolge der Streifen dort) —
     Tabelle `user_home_prefs (user_id, library_id, on_home, sort_order)`,
     wie zuvor, `GET/PUT /api/home/preferences[/{id}]` (liefert
     `{libraries, showContinue, showNextUp}`) + `PUT /api/home/order`.
  Alle drei Endpoint-Gruppen: authenticated, NICHT admin-only, ACL-gefiltert.
  `views.js buildLibPrefList()` ist der gemeinsame Rendering-Helper für die
  beiden Bibliotheks-Sektionen (Reiter + Startseite) im Dialog — je eigene
  Checkbox+▲▼-Liste, komplett unabhängig voneinander bedienbar.
- Library-Name-Überschrift klickbar → öffnet die Lib in Standard-Ansicht.
- Suchfeld in der Topbar ist auf Home-View **library-übergreifend**:
  matcht gegen Titel + Schauspieler über alle Libraries mit ACL-Zugriff.
- API: `GET /api/home` liefert `{sections: [{library, continue, nextUp,
  recent}, …]}`, bereits sortiert nach effektiver Reihenfolge (User-Override
  falls vorhanden, sonst `libraries.sort_order`). Sichtbarkeit analog:
  `user_home_prefs`-Zeile falls vorhanden, sonst `libraries.on_home`.

### Staffel-Ansicht für Serien
- **Auflösungsfilter weicht auf die normale flache Ordner-Liste zurück**
  (seit 2026-09-02, `grid.js`, gleiches Muster wie der bestehende „Ohne
  TMDB-Zuordnung"-Bypass): die Seasons-API kennt `bucket=` nicht UND mergt
  mehrere Dateien derselben Episode (z. B. 720p+360p) zu einem Owned-Slot
  mit Varianten-Dropdown — ein Filter hätte dort weder gewirkt noch die
  gezielte Auswahl „nur die 360p-Datei dieser Folge löschen, 720p behalten"
  ermöglicht. Mit `state.resBuckets.size > 0` fällt die Ansicht auf
  `/api/items?folder=<Show>&bucket=…` zurück (rekursiv wie jede normale
  Ordner-Navigation) — jede Auflösungs-Variante bleibt dort ein eigenes,
  einzeln löschbares Item.
- Toggle-Button `📺 Staffeln` in der Topbar, nur in TV-Libs sichtbar.
  Zwei Ebenen:
  - **Library-Default**: localStorage `seasonView:lib:<libID>` = "1"/"0"
  - **Pro-Serie-Override**: localStorage `seasonView:<libID>:<folder>`
  - Effective = per-Folder if set, else library-Default
  - **Default seit 2026-07-11: AN.** `seasonViewEffective()` in app.js gibt
    ohne gespeicherten Wert `true` zurück (`!== "0"` statt `=== "1"`) — Serien
    öffnen automatisch in der Staffel-Ansicht. Explizites Ausschalten pro
    Library (Toggle-Button in Library-Root) bleibt als "0" gespeichert und
    respektiert.
- **Automatischer Fallback bei fehlenden Staffel-Daten** (seit 2026-09-05,
  `grid.js`, User-Report: „Tatort" mit Kommissar-Unterordnern statt
  TMDB-Staffeln zeigte eine Sackgassen-Meldung „Keine Staffel-Daten
  verfügbar…" statt nutzbar zu bleiben — auf zwei verschiedenen Rechnern
  sogar unterschiedlich, weil `seasonView:*`-Overrides reines
  `localStorage` sind und nie zwischen Browsern synchronisieren). Liefert
  die Seasons-API auf oberster Ebene (kein Staffel-Klick,
  `state.currentSeason === null`) ein leeres `seasons`-Array — Ordner noch
  nicht TMDB-zugeordnet ODER die physische Struktur passt schlicht nicht zu
  TMDB-Staffeln (Tatort: Unterordner pro Ermittler-Duo, keine
  Sendejahr-Staffeln) — schaltet der Client die Staffel-Ansicht für GENAU
  diesen Ordner automatisch ab (persistiert wie ein manuelles Toggle unter
  `seasonView:<libID>:<folder>`, Toast „Keine Staffel-Struktur erkannt –
  zeige normale Ordner-Ansicht") und fällt in die normale, rekursive
  Ordner-/Dateiliste durch — dort funktionieren Sortierung/Filter
  unabhängig von jeder TMDB-Zuordnung. Der „Serie zuordnen…"-Button bleibt
  über `renderBreadcrumb` unverändert erreichbar (kommt für `kind=tv`
  automatisch, unabhängig vom Staffel-Modus).
  **Der Per-Ordner-Merker `seasonView:<libID>:<folder>="0"` wird an ZWEI Stellen
  aufgeräumt** (2026-09-10, LIVE 1.3.2 + 1.3.3): schreibseitig löschen beide
  Folder-Match-Zweige (`applyMatch`/`handleMatchImdb` in `matching.js`) ihn
  direkt nach erfolgreichem `POST .../folders/metadata`, bevor `loadItems()`
  läuft (kein neuer Scan nötig — Season/Episode werden ohnehin live aus dem
  Dateinamen geparst). Leseseitig verwirft `grid.js` ihn, wenn die Seasons-API
  tatsächlich Staffeln liefert — er wird an KEINER anderen Stelle je auf `"0"`
  gesetzt außer im Sackgassen-Fallback selbst, ist also zuverlässig als
  veraltet erkennbar; `state.seasonView` wird danach über `seasonViewEffective()`
  neu berechnet (fällt auf den Library-Default zurück, überschreibt also keine
  bewusst library-weit ausgeschaltete Staffel-Ansicht). Ohne den leseseitigen
  Teil bliebe jede schon VOR dem Fix in die Sackgasse gelaufene Serie für immer
  ungruppiert; so heilt sich jede Bestandsserie beim nächsten Öffnen selbst.
  **Bekannte Restlücke:** Dateien ohne SxxExx-Muster im Namen bleiben
  unabhängig davon ungruppiert (weder der synchrone `UnmatchedEpisodeFiles`-
  Fallback noch `matchItem` können ohne erkennbares Muster zuordnen) —
  Namensschema-Problem, kein Bug.
  **Bleibender Info-Header im Fallback (seit 2026-09-06, User-Wunsch: „bei
  nicht zugeordneten Serien soll auch so ein Infofenster aufgehen, mit den
  gleichen Buttons"):** der Toast allein verschwindet nach Sekunden ohne
  bleibenden Hinweis. `grid.js` merkt sich beim Fallback in
  `state.pendingShowInfoHeader` entweder `data.show` (Ordner IST zugeordnet,
  nur keine erkennbare Staffel-Struktur — Tatort/Terra-X-Fall) oder
  `{unmatched:true, folder}` (gar keine Zuordnung) und stellt danach — NACH dem
  `grid.innerHTML=""` des normalen Renderings, sonst sofort wieder gelöscht —
  `renderShowHeader(data.show, null)` (voller Header inkl. aller Buttons) bzw.
  `renderUnmatchedFolderHeader(folder)` (views.js, „🔍 Serie zuordnen…" +
  „🖼 Poster hochladen") voran. `showOut` trägt dafür zusätzlich `showTmdbId`
  (0 bei einem reinen Custom-Eintrag ohne echtes TMDB-Match) —
  `renderShowHeader` blendet dann „↻ TMDB neu laden"/„⚠ Episoden neu zuordnen"
  aus, „🖼 Poster ändern"/„🚫 Zuordnung entfernen" bleiben sichtbar (laufen
  generisch über `metadataId`).
  **Der Seasons-API-Call + die Header-Entscheidung laufen IMMER für TV-Ordner**,
  unabhängig von `state.seasonView` — nur ob Staffel-KACHELN oder die normale
  Liste gerendert werden, hängt am Toggle. Hing der ganze Block am Toggle,
  erschien der Header nur beim allerersten Öffnen (der Fallback hatte den
  Toggle ja gerade auf `"0"` gesetzt). Der Auto-Disable-Toast feuert weiterhin
  nur beim ÜBERGANG true→false.
  **🖼 Poster auch bei komplett unzugeordneten Serien (seit 2026-09-06,
  User-Wunsch):** der Button legt bei Klick zuerst per
  `POST /api/libraries/{id}/folders/metadata-manual` (`createCustomFolderMetadata`
  in `internal/api/tmdb.go`, Pendant zu `createCustomMetadata` — dort für ein
  Item, hier für den ganzen Ordner) einen `tmdb_type="custom"`-Eintrag an
  (`TMDBID = -time.Now().UnixNano()`, Titel = Ordnername), verknüpft ihn per
  `SetFolderMetadata` und öffnet darauf den bestehenden
  `openPosterPicker(metadataId, onApplied)`-Dialog (TMDB-Tab bleibt leer, der
  Upload-Teil funktioniert generisch). **`seriesSeasons` liefert auch im
  `showTmdbId===0`-Early-Return ein `show`-Objekt**, wenn
  `Store.GetFolderMetadataID` (bewusst OHNE `tmdb_type`-Filter, anders als
  `ShowTMDBForFolder`/`ShowMetadataIDForFolder`, die nur `tv` matchen) eine
  Zuordnung findet — sonst wäre der frisch hochgeladene Custom-Titel/Poster
  beim nächsten Öffnen des Ordners nicht mehr sichtbar (nur `metadataId`/
  `title`/`posterPath`, keine Seasons/Cast — die gibt's nur bei echtem Match).
  **Bekannte Einschränkung:** die wiederverwendeten Show-Header-Buttons rufen
  bei Erfolg weiterhin `renderSeasonFolders`/`renderSeasonEpisodes` direkt auf
  statt `loadItems()` — im Fallback-Kontext kann das kurzzeitig ein leeres
  Season-Grid statt der normalen Dateiliste zeigen, bis erneut navigiert wird.
  Kein Crash, nur ein UX-Rest.
- **Sort „Veröffentlicht" jetzt Teil von `FLAT_SORTS`/`FLAT_LIBRARY_SORTS`**
  (`grid.js`/`app.js`, seit 2026-09-05) — vorher fehlte `"released"` in
  beiden Sets, obwohl der Server (`ListItems`-SQL-Sort-Switch,
  `internal/store/sqlite.go`) rekursiv sortiertes `sort=released` schon
  lange unterstützt hat (Fallback-Kette `m.release_date → m.year →
  i.released_at → i.mod_time`, funktioniert auch OHNE TMDB-Zuordnung).
  Jetzt zeigt „Veröffentlicht" wie „Zuletzt hinzugefügt"/„Laufzeit" eine
  flache, chronologische Liste **auf der aktuellen Ordnerebene inkl. aller
  Unterordner** (Scope „nur nach unten flach", gleiche Konvention wie die
  anderen drei Flat-Sorts) — der eigentliche Weg, um z. B. alle
  Tatort-Folgen unabhängig von der Kommissar-Unterordner-Struktur
  chronologisch durchzublättern.
- In einem Show-Ordner mit aktivem Toggle: Staffeln als **Folder-Kacheln**
  (Poster + „x/y Folgen"-Badge). Klick öffnet Staffel → normales Grid mit
  owned + „Fehlt"-Kacheln.
- Fehlende Episoden werden **bis zur zuletzt vorhandenen** (`max(season,
  episode)`) geladen. Zukünftige/unveröffentlichte Folgen tauchen nicht
  als „Fehlt" auf.
- Serien-Info-Header oberhalb der Staffel-Kacheln: Poster, Titel, Jahres-
  Range, Status (deutsch: „Laufend"/„Beendet"/„Abgesetzt"/„In Produktion"/
  „Geplant"/„Pilot"), Staffel-/Folgen-Zählung (owned/total wenn abweichend),
  Genres, ★-Rating, Beschreibung, horizontale Cast-Strip.
- Backend: `GET /api/libraries/{id}/seasons?folder=<folder>` — lädt Show-
  Details + Cast + alle Staffeln inkl. Episoden-Owned-Flag. Fetch-Cap bei
  `max(S,E)` der owned Episoden. Store: `store/series.go`. `?refresh=true`
  invalidiert den TMDB-Cache für diese Show vor dem Fetch (für frische Daten).
- Show-Details, Credits und alle Seasons werden im Handler **parallel** (WaitGroup)
  geladen — bei Shows mit vielen Staffeln spart das mehrere Sekunden Latenz
  gegenüber seriellem Fetch. Der TMDB-Client cached die Antworten 15 min (siehe
  „TMDB-Client-Cache"), re-opens sind instant.
- **Show-Header-Buttons** unter der Beschreibung:
  - **„↻ TMDB neu laden"**: invalidiert den In-Memory-Cache (`InvalidateShow(showID)`)
    und refresht. Grünes Toast-Feedback `✓ Frisch geladen (N ms)` für 2,5 s.
  - **„⚠ Episoden neu zuordnen"** (orange): `POST /api/libraries/{id}/folders/re-enrich-episodes`
    setzt `items.metadata_id=NULL`, `metadata_confirmed=0`, `episode_end=0` für ALLE
    Episoden-Items im Ordner (inkl. bestätigter!) und triggert `EnrichFolderNow`.
    Nach 4 s lädt die Ansicht automatisch mit `?refresh=true` neu. Für Off-by-One-
    Fehler wie bei Billions Staffel 2, wo alle Episoden systematisch um eins
    verschoben gemappt waren.
  - **„🖼 Poster ändern" (seit 2026-09-06):** wiederverwendet denselben
    TMDB-Poster-Picker/Upload-Dialog (`#posterPickerDialog`) wie der
    ✏-Edit-Metadata-Dialog bei Filmen/Items — bisher nur für
    `state.currentItem` erreichbar, jetzt generalisiert
    (`openPosterPicker(explicitMetaID, onApplied)` in `matching.js`; der
    bestehende Item-Button ruft weiterhin ohne Argument auf, bekommt dabei
    das Click-Event als ersten Parameter, das ist kein `number` und fällt
    korrekt auf `state.currentItem.metadataId` zurück). Der Show-Header-
    Button übergibt explizit `show.metadataId` + einen Callback, der nach
    Anwenden die Staffel-Ansicht neu lädt (statt des sonst üblichen
    Item-Detail-Refreshs). **`show.metadataId` ist NEU im
    `GET /api/libraries/{id}/seasons`-Response** (`internal/api/series.go`,
    `showOut.MetadataID`) — kommt aus `Store.ShowMetadataIDForFolder`
    (`internal/store/series.go`, Pendant zu `ShowTMDBForFolder`, liefert
    aber die lokale `metadata.id` statt der TMDB-ID: erst `folder_metadata`,
    sonst der Parent-Metadata-Fallback über die Episoden im Ordner) — nötig,
    weil `POST /api/metadata/{id}/poster` auf `metadata.id` arbeitet, nicht
    auf `tmdb_id`. Ohne Zuordnung (`metadataId == 0`) zeigt der Button einen
    Hinweis statt den Dialog zu öffnen. **Show-Poster wird jetzt über den
    eigenen Proxy geladen** (`/api/poster/metadata/{id}` statt direkt TMDB-
    CDN) — sonst wäre ein per Upload gesetztes Poster (synthetischer
    `custom:…`-Pfad) nie sichtbar gewesen, das TMDB-CDN kann diesen Pfad
    nicht auflösen. Test: `internal/store/show_metadata_id_test.go`.

### Playlists (per User)
- Jede Playlist gehört genau einem User; Items werden in `playlist_items` mit
  `position`-Reihenfolge gehalten.
- **Strikt nach Video/Musik getrennt** (`playlists.kind`, seit 2026-09-04,
  User-Wunsch: "gemeinsame Playlists gefällt mir eigentlich nicht"). Neue
  Spalte `TEXT NOT NULL DEFAULT 'video'` — Migration setzt sie rückwirkend
  auf `'video'` für ALLE bestehenden Zeilen (SQLite wendet den ALTER-Default
  auch auf Bestandsdaten an, zutreffende Annahme da die Musik-Bibliothek erst
  seit wenigen Tagen existiert). `Store.CreatePlaylist(userID, name, kind)`
  verlangt `kind` explizit; `ListPlaylistsForUser(userID, isAdmin, kind)`
  filtert optional danach (`kind=""` = keine Einschränkung, von der
  Playlists-Root-Seite genutzt, die weiterhin ALLES zeigt). Der
  „Zu Playlist hinzufügen"-Dialog (`playlists.js openAddToPlaylist`)
  bestimmt `kind` aus der Bibliotheks-Art des Items
  (`playlistKindForItem`) und fragt/erstellt nur noch passende Playlists —
  ein Musiktitel kann so nicht mehr in eine Video-Playlist wandern und
  umgekehrt. Playlist-Manager-Dialog hat eine Art-Auswahl beim Neuanlegen
  (nur sichtbar wenn eine Musik-Bibliothek existiert), Playlist-Kacheln/
  -Liste zeigen ein 🎵/🎬-Icon.
- **Strikt user-isoliert, KEINE Admin-Ausnahme** (seit 2026-09-02 gefixt,
  Commit `3b2455b` — vorher echter Cross-User-Datenleck: Admin sah alle
  Playlists jedes Users). Anders als Library-ACL (wo „Admin sieht alles ohne
  explizite Einschränkung" bewusstes Design ist) gilt hier: Playlists sind
  private Kuratierung, Admin-Status gibt KEINEN Zugriff auf fremde
  Playlists. `Store.ListPlaylistsForUser` filtert immer `WHERE
  p.user_id = ?` (Admins sehen zusätzlich eigentümerlose Legacy-Playlists,
  `user_id IS NULL` — kein fremder Besitzer, also kein Cross-User-Fall).
  `api.requirePlaylistAccess` prüft den Besitzer ausnahmslos.
  `Store.PlaylistsForItem` (Checkmarks im „Zu Playlist hinzufügen"-Dialog)
  ist ebenfalls user-gescoped. Regressionstest:
  `internal/store/playlists_test.go TestPlaylistUserIsolation`. Siehe auch
  Memory `feedback_user_isolation_before_deploy` — **bei jedem neuen
  Feature mit privaten User-Daten explizit auf denselben Admin-Bypass-Reflex
  prüfen**, bevor deployed wird.
- Auto-Next: Beim `ended`-Event des Players spielt das nächste Queue-Item automatisch.
- UI: Topbar zeigt Playlist-Auswahl; Detail-Dialog und Player-Control-Bar haben
  „Zu Playlist hinzufügen"-Button (neu-erstellen direkt aus dem Dialog möglich).

### Filter-UI
- **Auflösungs-Filter**: kompaktes Dropdown mit Checkboxen (Multi-Select).
  Server-seitig werden mehrere `bucket`-Werte per OR geORd.
  Buckets: 4K / 2K / 1080p / 720p / 576p / 540p / 480p / ≤360p.
- **Genre-Filter (seit 2026-09-06, User-Wunsch)**: gleiches Dropdown-Muster
  wie der Auflösungs-Filter (Button `#genreDropdownBtn` öffnet ein Panel),
  aber mit dynamisch geladener Trefferliste statt fester Buckets — inkl.
  Suchfeld im Panel (`#genreDropdownSearch`), da Filme/Serien deutlich mehr
  Genres haben können als die Auflösungs-Buckets. **Global nutzbar**
  (Filme/Serien UND Musik, User-Vorgabe: "der Filter kann global wirken, da
  bei Filmen er auch sinnvoll ist") — anders als Auflösung/Gesehen/Bewertung
  wird das Genre-Label bei Musik-Bibliotheken NICHT ausgeblendet.
  `GET /api/libraries/{id}/genres` (`Store.ListGenresForLibrary`, gescoped
  auf genau diese Library — "im Ordner Musik sollen dort nur Musiktreffer
  stehen, bei Filmen oder Serien nur die dortigen") liefert die Trefferliste
  jeweils aus der passenden Quelle: `movies`/`tv` parsen alle distinkten
  Werte aus `metadata.genres` (TMDB-JSON-Array-String) in Go (bewusst ohne
  SQLite-JSON-Funktionen, robuster), `music` aus `items.genre` (Tag-Wert),
  `private` liefert immer `[]` (kein Genre-Konzept, Label bleibt
  ausgeblendet). Response gecacht pro Bibliotheks-ID im Frontend
  (`state.genreCache`), damit wiederholtes Öffnen des Pickers innerhalb
  derselben Library keinen neuen Request auslöst.
  **Server-Filter** `ItemFilter.Genres []string` (`internal/store/sqlite.go`,
  Multi-Select → OR) matcht IMMER beide Spalten gleichzeitig
  (`m.genres LIKE '%"<g>"%' OR i.genre = '<g>'`), unabhängig vom
  Bibliothekstyp — ein Musik-Genre wie "Rock" kommt praktisch nie in
  TMDB-Genres vor und umgekehrt, echte Kollisionen sind kein realistisches
  Risiko, und das erspart eine Kind-Fallunterscheidung im SQL. Query-Param
  `genre=` (mehrfach wie `bucket=`) an `/api/items` UND `/api/items/random`.
  Test: `internal/store/genres_test.go`.
  **⚠ Die Musik-Standardansicht ist NICHT `/api/items`**, sondern die
  Album-Kachel-Übersicht (`GET /api/libraries/{id}/albums`) — ein neuer
  Item-Filter läuft dort ins Leere, bis er zusätzlich in
  `Store.ListMusicAlbumsFiltered(libraryID, userID, genres)` landet (filtert per
  `a.genre IN (…)` auf der bereits aggregierten `music_albums.genre`-Spalte,
  kein LIKE nötig). `ListMusicAlbums` ist seither ein dünner Wrapper ohne
  Filter (bewahrt die alte 2-Arg-Signatur). Frontend: alle DREI
  Album-Fetch-Stellen in `grid.js` (Übersicht/Favoriten/„Alle Titel") hängen
  den Filter an (`musicGenreQS()`-Helper). Test:
  `music_albums_genre_filter_test.go`.
- **Musik-Genre in der Album-Übersicht** (seit 2026-09-06, User-Wunsch: "bei
  Musik möchte ich auch das Genre dabei stehen haben"): `music_albums.genre`
  war bisher nur im Album-Detail-Header sichtbar (seit 1.0.67) — jetzt auch
  in der Album-Kachel (`card-meta`, zweite Zeile neben Artist) und der
  Album-Übersichts-Listenzeile (`renderAlbumTiles` in views.js, neue Spalte
  `.track-row-genre`, `.track-row--album`-Grid-Template entsprechend
  erweitert). `ListMusicAlbums`/`model.MusicAlbum` lieferten `genre` schon
  vorher mit, reine Anzeige-Ergänzung ohne Server-Änderung.
- **Sortierung** mit Default-Richtung je Feld (Title/Episode asc, Rest desc);
  ⬆/⬇-Button neben dem Dropdown flippt die Richtung.
- **Duplikate** ist ein **Eintrag im Sort-Dropdown** (nicht eigenes Filterfeld).
  Aktiv → alle Items mit mehrfach vergebener `metadata_id` flach ohne Merge.
- **Favoriten-Filter** auf „Nur" stellt sofort eine flache Ansicht (wie
  Duplikate, aber eigener Pfad in loadItems). Scope = aktueller Ordner
  rekursiv, sonst library-weit (`folder`-Param an `/api/items`, Server
  kombiniert `favorite=yes` + `folder` bereits per AND).
- **Flache library-weite Sort-Modi** „Zuletzt abgespielt" (`played`), „Zuletzt
  hinzugefügt" (`added`), „Laufzeit" (`duration`), „Veröffentlicht" (`released`,
  seit 2026-09-05) und „Dateiname" (`filename`, seit 2026-09-05) zeigen die
  Top-N Videos der GANZEN Library, ignorieren die Ordner-/Staffel-Struktur
  (keine Folders). Ein gemeinsamer Branch in `grid.js` (`FLAT_SORTS`) holt sie
  flach, Breadcrumb via `renderBreadcrumb({flatSortView:<mode>})`. Server-seitig
  filtert ListItems bei `Sort=="played"` zusätzlich `AND us.last_played_at IS
  NOT NULL`. (App-Pendant: `isFlatSortMode()` in LibraryViewModel.)
  **„Dateiname" (`filename`) ist bewusst ein EIGENER Sort-Key, nicht einfach
  `title` (Name) mit ins `FLAT_SORTS`-Set aufgenommen:** `title` ist der
  App-weite Default-Sort — hätte er dieselbe Zwangs-Flach-Behandlung wie die
  anderen FLAT_SORTS, würde JEDE Ordner-Kachel-Ansicht (Library-Root,
  Drilldown-Ordner) permanent flach geschaltet, sobald irgendwo Default-Sort
  aktiv ist. `filename` sortiert IMMER nach dem physischen Dateinamen
  (`i.title COLLATE NATSORT`), NIE nach einem eventuellen TMDB-Titel (anders
  als `title`, das `m.title` bevorzugt) — User-Anfrage 2026-09-05: Serien wie
  Tatort, deren Dateinamen Jahr+Episodennummer tragen, deren einzelne Folgen
  aber oft nicht individuell TMDB-episode-gematcht sind, sollen sich
  zuverlässig nach genau diesem Namensschema sortieren lassen, unabhängig
  davon ob einzelne Dateien in derselben Liste zufällig doch einen TMDB-Titel
  haben (das würde bei `title` sonst die Reihenfolge durchbrechen). Generisch
  nutzbar für jede Bibliothek mit chronologisch/nummeriert benannten Dateien,
  nicht Tatort-spezifisch. `effectiveSortDir()` (app.js) behandelt `filename`
  wie `title`/`episode`: Default aufsteigend.
  **„Veröffentlicht" (`released`) — Datumsquelle, wichtig bei unmatched
  Items:** Fallback-Kette `m.release_date → m.year → i.released_at →
  i.mod_time` (siehe „Playback"/Sort-Switch in `internal/store/sqlite.go`).
  Für Episoden OHNE TMDB-Match kommt `i.released_at` ausschließlich aus
  ffprobe-Tags (`creation_time`, yt-dlp `DATE`-Tag) — der Scanner extrahiert
  **kein Datum aus dem Dateinamen selbst**. Fehlen diese Tags, sortiert
  `released` nach `mod_time` (Datei-Kopierdatum auf der Platte), NICHT nach
  echtem Sendedatum — bei Tatort-Rips ohne eingebettete Tags ist das der
  Datei-Downloaddatum, keine Chronologie der Ausstrahlung. In diesem Fall ist
  `filename` (sofern der Dateiname das Jahr/die Episodennummer trägt) die
  zuverlässigere Sortierung.
  **Persistenz (seit 2026-07-11):** Diese Sorts werden pro Library/Folder
  gespeichert (`FLAT_LIBRARY_SORTS` in app.js), aber NUR in einem Kontext, der
  ohnehin nie Unterordner-Kacheln zeigt — `currentContextShowsFolderTiles()`
  prüft `state.currentFolder === null` (Library-Root) oder
  `state.currentFolderDrilldown` (Drilldown-Ordner); dort würde eine
  gespeicherte flache Sortierung die Ordner-Kacheln beim Wiederbetreten
  dauerhaft verstecken, also wird dort NICHT persistiert (`persistSortForContext`
  überspringt, `restoreSortForContext` verwirft einen dort gespeicherten
  Flat-Sort als "kein Sort gespeichert"). In einem normalen Unterordner ohne
  Drilldown (die häufigste Situation — zeigt ohnehin immer flach) ist "flach
  sortiert" == "normal", wird also ganz normal gespeichert/wiederhergestellt
  wie jeder andere Sort.
  **Scope = nur nach unten flach (Browser + App vC 97):** im Library-Root
  library-weit, in einem Unterordner NUR dessen Inhalt (rekursiv) — nicht die
  ganze Library hochziehen. Browser: `grid.js` setzt `folder=state.currentFolder`
  wenn gesetzt (Breadcrumb zeigt den Ordner-Scope). App: `loadItems` folderParam
  = `if (st.flatView) null else currentFolder` (kein isFlatSortMode-null-Zwang
  mehr); offline `itemsSortedFlat(..., folder)`.
- **Bestätigungs-✅ auf der Kachel** bei Sort „Duplikate" oder „Verdächtige
  Zuordnungen": blauer Button unter dem Watched-Haken, Klick ruft
  `PUT /api/items/:id/confirm` mit `{confirmed:true}` und lädt das Grid neu.
- **Library-Wechsel** setzt Suche + alle Filter zurück (resetFilters).

### Klickbarer Watched-Haken auf der Kachel
- Runder ✓-Button oben-links auf jeder Kachel.
- Ungesehen: gedimmt, volle Sichtbarkeit bei Hover. Click togglet über
  `/api/items/:id/watched`, aktualisiert lokal ohne komplettes Re-Rendering.
- Öffnet nicht den Detail-Dialog (stopPropagation).

### Kachel-Overlay-Positionen (WICHTIG vor neuen Badges/Buttons)
Die `.card .thumb`-Fläche hat feste, bereits belegte Koordinaten. **VOR** jeder
neuen absolut positionierten Einblendung IMMER diese Tabelle prüfen und in der
Reihenfolge erweitern, sonst werden bestehende Elemente verdeckt:

| Position               | Element          | Größe  | Zweck                                |
|------------------------|------------------|--------|--------------------------------------|
| `top:6  left:6`        | `.watched-toggle`| 24×24  | Gesehen-Haken ✓                     |
| `top:8  left:8`        | `.card-select`   | 24×24  | Bulk-Select (überlagert, nur aktiv) |
| `top:36 left:6`        | `.confirm-toggle`| 24×24  | ✅ Zuordnung bestätigen (cond.)      |
| `top:6  left:38`       | `.thumb .badge`  | auto   | Container MKV/MP4                   |
| `top:6  right:6`       | `.rating`        | auto   | TMDB ★ 8.5 (kein Konflikt mit variant-badge) |
| `top:34 right:6`       | `.variant-badge` | auto   | ×N Varianten (UNTER dem Rating, nicht daneben) |
| `top:6  left:6`        | `.collection-complete`| auto | ✓ komplett (nur Sammlung-Kachel)   |
| `bottom:6 left:6`      | `.res-badge`     | auto   | 1080p / 720p etc.                   |
| `bottom:4 left:62`     | `.fav-toggle`    | 24×24  | ♡/♥ Favorit (rechts vom Res-Badge) |
| `bottom:6 right:6`     | `.duration`      | auto   | 1:42 h                               |
| `bottom:6 right:66`    | `.tp-badge`      | 24×24  | Trickplay-Status 🎞                  |
| `bottom:4 left:60`     | `.fav-badge`     | auto   | (Legacy, ungenutzt)                 |

**Regel**: Wenn du einen neuen Overlay-Button brauchst, prüfe zuerst welche
Koordinaten in obiger Tabelle schon belegt sind. Empfohlene Folgeplätze:
`top:66 left:6`, `top:66 right:6`, oder unter einem bestehenden Element mit
32 px Versatz (stacking). Bei Unsicherheit: beim User fragen.

### Klickbares Favoriten-Herz auf der Kachel
- Runder ♡/♥-Button oben-rechts (symmetrisch zum Watched-Haken). Immer
  gerendert, gedimmt wenn kein Favorit, rot gefüllt wenn Favorit.
- Click togglet über `/api/items/:id/favorite` + aktualisiert `item.favorite`
  lokal + toggelt die `is-on`-Klasse. Kein Re-Render, kein Detail-Dialog.
- Handler: `toggleFavoriteOnCard(item, btn)` analog zu `toggleWatchedOnCard`.

### Bulk-Selection
- Toolbar-Button „☑ Auswählen" aktiviert `body.selection-mode`.
- Sticky Aktionsleiste: „N ausgewählt" · Alle · Keine · ♡ Favorit · ✓ Gesehen
  · 📋 Playlist · ⬇ Download · 🗑 Löschen.
- Download-Bulk triggert sequentielle `<a download>`-Clicks mit 400ms Abstand.

### Icon-Buttons in der Werkzeugleiste (seit 2026-09-13, LIVE 1.3.31)

- User-Vorgabe: „nur als Icon vorhanden sein, das spart Platz". Betroffen:
  ☑ Auswählen, 🎲 Zufall, ⟳ Scan, 📚 Sammlungen, Playlists sowie in
  Musik-Bibliotheken 🎵 Alle Titel, der Kachel/Listen-Umschalter und die
  Spaltenwahl. Gemeinsame Klasse **`.icon-only`** (quadratisch, 34 px bzw.
  36 px in der Lib-Nav) — feste Breite, weil Emoji und SVG sonst
  unterschiedlich breit sitzen und die Reihe unruhig wirkt.
- **⚠ Jeder `.icon-only`-Button MUSS `title` UND `aria-label` tragen.** Ohne
  sichtbaren Text ist der Tooltip die einzige Erklärung für die Maus — und
  ohne `aria-label` ist der Button für Tastatur/Screenreader komplett
  namenlos.
- **Playlists nutzen ein eigenes SVG** (`ICON_PLAYLIST_SVG` in `helpers.js`):
  Liste mit Play-Pfeil — dasselbe Motiv wie in allen nativen Clients
  (Apple `music.note.list`, Android `Icons.Filled.PlaylistPlay`, Linux
  `playlist-symbolic`). Das frühere 📋 (Klemmbrett) passte zu keinem davon.
  Für „Liste + Play" gibt es kein Emoji, deshalb SVG — gleiches Muster wie
  `ICON_TRASH_SVG`/`ICON_FILM_SVG`. Ebenso `ICON_COLUMNS_SVG` (drei Spalten,
  analog Materials `view_column`) für die Spaltenwahl; das vorher genutzte
  ☰ meint eine Liste, nicht Spalten.
- **Der Album-Umschalter zeigt immer das Icon dessen, was ein Klick bewirkt**
  (User-Vorgabe): Kachelansicht → Listen-Icon, Listenansicht → Kachel-Icon.
  `syncMusicListViewBtn()` (`helpers.js`) hält Icon + Tooltip am State und
  MUSS von jeder Stelle gerufen werden, die `state.musicListView` ändert
  (aktuell: Klick-Handler + `boot()` für den aus localStorage
  wiederhergestellten Zustand).
- `renderLibNav`s `make()` nimmt ein `html`-Flag — Icon-Buttons brauchen
  `innerHTML` (SVG), Text-Buttons bleiben bei `textContent`.
- **`anleitung.html` wurde mitgepflegt** (kein automatischer Abgleich, siehe
  „📖 Anleitung"): Button-Überschriften tragen den Zweck jetzt in Klammern,
  dazu ein Hinweis auf die Tooltips und ein neuer Absatz zu den
  Musik-Schaltflächen.

### Topbar-Navigation
- Drei gleich große Icon-Buttons links in der Topbar: **🏠 Home**,
  **📚 Sammlungen**, **📋 Playlists**. Alle mit Klasse `.nav-icon-btn`
  (feste Breite 38 px, font-size 16 px) — unterschiedliche Emoji-Breiten
  führen sonst zu optisch ungleichen Kästchen.
- Sammlungen NICHT mehr im `#librarySelect`-Dropdown (früher als `col:`-Entry).
  Handler: `#collectionsBtn` setzt `state.collectionsView=true` und ruft
  `loadItems()`.

### Sammlungs-Komplett-Badge
- `store.Collection` enthält `PartCount` (alle TMDB-Parts der Sammlung),
  `HiddenCount` (davon vom aktuellen User ausgeblendet) und `UnreleasedCount`
  (davon noch nicht erschienen, s.u.). `ListCollections` nimmt `userID` als
  Parameter.
- Frontend (`cards.js` in `renderCollectionCard`):
  `complete = movieCount >= partCount - hiddenCount - unreleasedCount`.
  Wenn true, grünes „✓ komplett"-Badge oben links (`.collection-complete`).
- Der Zähler unten rechts zeigt standardmäßig `N/Total Filme` (statt nur `N Filme`,
  `Total` bereits abzüglich `hiddenCount`). Bei `partCount=0` (Parts noch nicht
  gefetcht) Fallback auf den alten Zähler.
- **Zukünftige/unangekündigte Filme zählen nicht gegen „komplett"** (User-
  Anforderung 2026-09-02: „Zukünftige Filme dürfen erscheinen, aber die
  Sammlung soll erst auf Unvollständig wechseln, wenn der Film tatsächlich
  erschienen ist" — Ziel: nicht jedes Mal manuell ausblenden müssen und dann
  vergessen wieder einzublenden). `UnreleasedCount` in `ListCollections`
  zählt Parts, deren `release_date` **leer ODER in der Zukunft** ist.
  Die Bedingung ist `release_date IS NULL OR release_date = '' OR
  release_date > date('now')` — ein **leeres** Datum (früh angekündigte
  Fortsetzung, die bei TMDB schon ein Poster, aber noch keinen Termin hat, real
  beobachtet: „Den of Thieves 3") muss mitzählen, sonst bleibt eine trotz
  vollständigem Bestand als unvollständig markiert. Das Enrichment überspringt
  einen Part nur, wenn `release_date` UND `poster_path` leer sind (reiner
  TMDB-Platzhalter). Frontend: `renderCollectionPartCard` zeigt für solche
  Teile ein blaues „Bald"-Badge (`.missing-badge--upcoming`) statt des roten
  „Fehlt" — rein kosmetisch, ohne Einfluss auf die Vollständigkeits-Logik.
  Test: `TestCollectionsUnreleasedParts`.

### Einstellungen / Admin-UI
- **Zahnrad-Button** (`#settingsBtn`) ist seit 2026-09-01 für JEDEN Benutzer
  sichtbar (siehe „Benutzer & Zugriff" oben — die vorherige Doku-Aussage
  „für Non-Admin komplett ausgeblendet" war veraltet, korrigiert 2026-09-02).
  Nur die Sektionen `.drawer-admin` (Allgemein, Automatisierung, Bibliothek
  & Medien, Diagnose) werden für Non-Admins per `renderUserMenu()`
  ausgeblendet — „Mein Konto" (inkl. „📖 Anleitung", s. u.) bleibt für alle da.
- **Entfernt aus dem Menü**: „Duplikate zusammenführen" (Auto-Merge passiert
  eh bei gleicher TMDB-ID) und „NFO für alle bestätigten" (läuft automatisch
  beim Bestätigen). Die Server-Endpoints bleiben erhalten, nur die UI-Einträge
  + zugehörige Helfer (`autoMergeLibrary`, `writeAllConfirmedNFOs`) sind raus.
- **Toast-Helper** `showToast(msg, {kind:"info"|"success"|"error", duration?})`:
  unaufdringliches, nicht-modales Feedback rechts unten. Container
  `#toastRoot` wird bei Bedarf lazy angelegt.
- **📖 Anleitung** (seit 2026-09-02, `data-action="anleitung"` in „Mein
  Konto", für alle sichtbar): öffnet `/anleitung.html` in einem neuen Tab
  (statische Seite, gleiches Muster wie `datenschutz.html` — kein Login
  nötig, `isPublicPath` lässt jedes Nicht-`/api/*`-Asset ohnehin durch).
  Laien-verständliche Erklärung jedes Topbar-Buttons, der Reiterleiste, der
  Startseiten-Streifen und **jedes** Menüpunkts inkl. seiner Optionen —
  Einstellungen (Puffer/Startpuffer/Trickplay-Intervall/TMDB-OMDb/
  Hardware-Beschleunigung/Direct-Play-vs-Transcode/Auto-Rename) besonders
  ausführlich. Muss bei größeren UI-Änderungen manuell mitgepflegt werden
  (kein automatischer Abgleich mit dem Code).
- **🔤 Anzeige** (seit 2026-09-08, `data-action="displayprefs"` in „Mein
  Konto", für alle sichtbar, `#displayPrefsDialog`,
  `views.js openDisplayPrefsDialog`): bündelt drei rein clientseitige,
  localStorage-basierte Anzeige-Schalter, die vorher entweder ein
  Toolbar-Button waren oder noch gar nicht existierten — User-Wunsch: „AZ
  Auswahl … ins Menü unter Einstellungen verschieben" + „ein Schalter …
  welcher die Dateinamen in den Kacheln ein bzw. ausblendet, bei Filmen und
  Serien, getrennt wählbar".
  1. **Buchstabenleiste in diesem Ordner** — ersetzt den bisherigen
     Topbar-Button `🔤 A-Z` (komplett aus `index.html`/`app.js` entfernt,
     kein Verhaltensunterschied: gleicher `alphaSidebar:<libID>:<folder>`-
     Storage-Key, siehe „Alphabet-Sidebar rechts" oben). Per-Ordner-Wert,
     die Checkbox liest ihn bei jedem Dialog-Öffnen frisch (kein Live-Sync
     nötig, da der Dialog beim Navigieren ohnehin geschlossen ist).
  2. **Dateinamen auf Film-Kacheln** / 3. **… auf Serien-Kacheln** — zwei
     neue, unabhängig voneinander schaltbare Toggles
     (`state.showFilenameMovies`/`showFilenameTv`, Default an), steuern die
     `.card-filename`-Zeile nur für `kind=movies`/`kind=tv` (siehe
     „Dateinamen-Zeile bei Filmen/Serien ein-/ausblendbar" oben). Musik/
     Privat-Bibliotheken haben keinen Schalter — dort ist die Zeile fester
     Teil des Layouts.
  Alle drei Werte sind rein lokal (kein Server-Roundtrip, anders als z. B.
  `openHomePrefsDialog`) — `#displayPrefsAlphaSidebar`/`#displayPrefsFilenameMovies`/
  `#displayPrefsFilenameTv` in `index.html`, Standard-`switch-row`-Markup.

### Playlists als eigene Seite
- **NICHT** mehr im Library-Dropdown. Der 📋-Button in der Topbar öffnet eine
  dedizierte Playlist-Ansicht mit Grid aus Playlist-Kacheln (blauer Verlauf,
  großes 📋-Icon, Video-Anzahl).
- Toolbar oberhalb des Grids: „+ Neue Playlist" + „Verwalten" (öffnen weiter
  den bestehenden Manager-Dialog).
- Klick auf eine Playlist-Kachel führt in die flache Item-Liste dieser
  Playlist — Breadcrumb-Zurück-Pfeil bringt dich zur Playlist-Root.
- **✕-Close-Button** im Breadcrumb: springt zurück zu der Ansicht, aus der
  der User den 📋-Button gedrückt hat. `state.playlistReturnNav` speichert
  den Snapshot beim Betreten (Library, Folder, Home-View, Collection, …).
  `exitPlaylist()` restoriert den Snapshot und setzt `state.playlistsView`
  zurück. Der ✕ erscheint in der Playlist-Root IMMER (wenn Snapshot da), in
  einer einzelnen Playlist nur wenn NICHT über Playlist-Root reingekommen
  (sonst hat man schon den `←`-Pfeil und ✕ wäre doppelt).
- **Duplikat-Hinweis** beim Add: `AddToPlaylist` gibt `{added: bool}` zurück
  (basierend auf `RowsAffected` der `INSERT OR IGNORE`). Client zeigt Toast
  „Zu X hinzugefügt" (grün) oder „Ist bereits in X" (blau). Bulk-Add sammelt
  die Counter und zeigt einen Sammel-Toast (z.B. „3 hinzugefügt, 2 bereits drin").
- **Bulk-Auswahl → Playlist**: öffnet den `addToPlaylistDialog` mit Liste
  aller Playlists + Quick-Create-Formular (gleicher Dialog wie im Detail).
  Aus Bulk-Auswahl heraus kann so auch direkt eine neue Playlist erstellt
  werden, die alle gewählten Videos enthält.



### Player-Dialog: Backdrop-Klick vs. Größe ändern (2026-09-25, v1.4.36)
Der Player-Dialog hat `resize: both`. Der Anfasser gehört zum `<dialog>` selbst, deshalb
feuert das Loslassen nach dem Größeändern einen `click` mit `target === #playerDialog`.
Der Backdrop-Schließen-Handler (seit 1.4.32) schloss daraufhin den Player. Regel:
**Backdrop nie nur über `e.target === dialog` erkennen.** Zusätzlich müssen
`pointerdown` UND `click` außerhalb von `getBoundingClientRect()` liegen (deckt auch
das Verschieben per Kopfleiste ab, wenn es außerhalb endet). `cards.js`/`matching.js`
nutzen noch das einfache Muster, sind aber nicht resizable.
