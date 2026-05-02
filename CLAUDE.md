# Goldfish — Jellyfin-light für Unraid

Produktname: **Goldfish**. Das Go-Modul, Docker-Image, Volumes und Stack-Name bleiben aus
Kompatibilitätsgründen `videoplayer` / `simple-videoplayer` — nur das UI-Branding ist
„Goldfish".

---

# 🔒 OIDC SSO mit Authentik — LIVE seit 2026-04-27

> **An jede Claude-Session, die Goldfish anfasst:**
> Das Repo hat eine **vollständig deployte und produktiv laufende OIDC-Anbindung
> an Authentik**. Sie funktioniert. Sie ist getestet. Sie wird im Browser UND in
> der iOS/Android-App genutzt. **Bitte nicht „aufräumen", nicht „vereinfachen",
> nicht „den Auth-Code modernisieren"** ohne den ganzen Block hier verstanden zu
> haben. Wenn du etwas an Login-Code, Login-UI, User-Tabelle, Routern oder
> Dockerfile machst, lies erst diesen Abschnitt zu Ende.

## Was läuft

- **Authentik** auf `https://auth.<your-domain>` (separater VPS) ist der zentrale
  IdP für 9 Apps inkl. Goldfish.
- Goldfish ist ein **OIDC-Client** (Authorization Code Flow + PKCE + nonce).
- **Email/Passwort-Login bleibt bestehen** als Fallback. SSO ist additiv.
- **iOS/Android-App** funktioniert ebenfalls — die App nutzt den gleichen
  Web-Endpoint (Goldfish ist Web-only, kein Custom-URL-Scheme nötig wie bei Immich).

## Authentik-Provider (am Authentik-Server, nicht im Goldfish-Repo)

| Feld | Wert |
|---|---|
| App-Slug | `goldfish` |
| Provider-Name | `Goldfish` |
| Issuer | `https://auth.<your-domain>/application/o/goldfish/` |
| Redirect-URI | `https://goldfish.<your-domain>/api/auth/oidc/callback` (strict) |
| Sub-Mode | `user_email` (sub-Claim ist die Email) |
| Signing-Key | `authentik Self-signed Certificate` → **RS256** |
| Scopes | `openid email profile` |
| Group-Binding | `Familie` (Christian + Alex) |

**Niemals zurück auf HS256** stellen — go-oidc-Client lehnt das ab.
**Trailing-Slash am Issuer NICHT trimmen** — Authentik liefert ihn mit zurück,
strict-Match-Verifier vergleicht 1:1.

## Im Repo (alle Files NÖTIG, NICHT LÖSCHEN)

```
internal/api/oidc.go                — OIDCConfig, OIDCRuntime, oidcLogin/oidcCallback
internal/api/router.go              — Server.OIDC Feld + 2 Routes
internal/api/auth.go                — /api/auth/oidc/{login,callback} in isPublicPath
internal/store/sqlite.go            — addCol(users, oidc_subject) + partial-unique Index
internal/store/users.go             — GetUserByOIDCSubject, GetUserByNameCI, SetUserOIDCSubject
cmd/goldfish/main.go                — OIDCConfig aus 4 Env-Vars
internal/webassets/web/login.html   — #ssoBtn + sso_error-Reader
Dockerfile                          — golang:1.24-bookworm (oauth2 v0.24 braucht ≥1.23)
docker-compose.yml                  — OIDC_*-Env aus Stack durchgereicht
go.mod                              — coreos/go-oidc/v3 v3.11.0, golang.org/x/oauth2 v0.24.0
```

## Match-Logik (`oidcCallback` in `internal/api/oidc.go`)

1. `users.oidc_subject = sub` → Login direkt durch
2. Sonst: `users.username = preferred_username COLLATE NOCASE` → setzt
   `oidc_subject` und Login durch (one-time-link)
3. Sonst: Redirect zu `/login.html?sso_error=Kein Goldfish-Konto für …`

## Deployment (Stand 2026-04-27)

- **Image:** `simple-videoplayer:latest`, gebaut von Image-CI (self-hosted Runner
  `goldfish-ci` Stack 38) bei jedem Push auf `main`.
  Falls Runner offline (Reg-Token expired): direkt builden via
  `POST http://<UNRAID-LAN-IP>:9000/api/endpoints/3/docker/build?t=simple-videoplayer:latest`
  mit Tarball als Body (siehe `.github/workflows/deploy.yml`).
- **Stack:** Portainer-Stack-37 `videoplayer` auf Endpoint 3 (`<UNRAID-LAN-IP>`).
- **Volume:** Bind ist `videoplayer_config:/config`. **WICHTIG:** das Volume ist
  als `external: true, name: videoplayer_videoplayer_config` deklariert — der
  echte Datenbestand (User-DB, Posters, Trickplay-Cache) liegt im
  Volume `videoplayer_videoplayer_config` (101 MB). Wer das Compose neu
  schreibt und das `external`-Mapping vergisst, mountet ein leeres Volume
  und alle User-Daten sind „verschwunden" (sind nicht weg, aber nicht gemountet).
- **Stack-Env (in Portainer Stack-Editor → Environment variables, MUSS gesetzt sein):**
  ```
  OIDC_ISSUER_URL    = https://auth.<your-domain>/application/o/goldfish/
  OIDC_CLIENT_ID     = (aus Authentik Admin → Applications → Goldfish → Provider)
  OIDC_CLIENT_SECRET = (aus Authentik Admin → Applications → Goldfish → Provider)
  OIDC_REDIRECT_URL  = https://goldfish.<your-domain>/api/auth/oidc/callback
  ```
  Ohne diese 4 Vars → `/api/auth/oidc/login` antwortet 503 „SSO nicht konfiguriert".
- **User-Pre-Link** (einmalig per SQLite gegen `videoplayer_videoplayer_config`):
  ```sql
  UPDATE users SET oidc_subject='user1@example.com'   WHERE username='Christian';
  UPDATE users SET oidc_subject='user2@example.com'    WHERE username='Alex';
  ```
  (`Christian` mit großem C — case-sensitive in der DB.) `Familie`-User bleibt
  ohne SSO-Verknüpfung; loggt sich wie gewohnt mit Username/Passwort ein.

## Live-Health-Test

```bash
curl -i https://goldfish.<your-domain>/api/auth/oidc/login
# Erwartet: 302 zu auth.<your-domain>/application/o/authorize/?...
```

Wenn 503 zurückkommt → Env-Vars fehlen im Container.
Wenn 502 zurückkommt mit „issuer did not match" → jemand hat den Trailing-Slash
gekürzt, siehe `internal/api/oidc.go` Zeile mit `r.cfg.IssuerURL`.

## Was NICHT zu tun ist

- **NICHT** `internal/api/oidc.go` löschen oder „auf Standardbibliothek umstellen".
  go-oidc/v3 + oauth2 ist die Standardbibliothek für OIDC in Go.
- **NICHT** das Email/Passwort-Login („authLogin") rauswerfen — ist Fallback.
- **NICHT** `oidc_subject`-Spalte aus dem User-Schema entfernen.
- **NICHT** den `#ssoBtn`-Block in `login.html` „aufräumen".
- **NICHT** das Volume von `external: true` auf einen lokalen Default umstellen
  ohne den Volume-Namen `videoplayer_videoplayer_config` zu erhalten — sonst
  leere User-DB.
- **NICHT** im Authentik-Provider `sub_mode` auf `hashed_user_id` zurücksetzen —
  würde alle bestehenden Pre-Links ungültig machen.
- **NICHT** den Authentik-Provider neu erstellen — die Client-ID/Secret in
  Portainer-Stack-37 müsste sonst auch aktualisiert werden.

---

Ein schlanker Video-Streaming-Server auf Intel-iGPU-Hardware. Einzelner Go-Binärcontainer,
eingebettetes Web-UI, SQLite, ffmpeg mit VAAPI.

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

```
cmd/videoplayer/main.go           — HTTP-Server, Startpunkt, Wiring
internal/api/                     — HTTP-Handler (router, libraries, items, stream, tmdb, watched, favorite, playlists, auth, users, trickplay, …)
internal/auth/                    — Session-Store, Middleware, ACL-Checks (requireLibAccess)
internal/enrich/worker.go         — TMDB/OMDb-Enrichment-Goroutine (Background-Queue)
internal/model/types.go           — Library, Item, Metadata, User, Playlist, LibraryKind
internal/nameparser/parser.go     — Dateiname → {Title, Year, Season, Episode}
internal/nameparser/parser_test.go — Tabellen-Tests, 88,8 % Coverage (siehe Frontend-Tests + Tooling)
internal/nameparser/variants.go   — De-leet + Longest-Token-Fallback für Kandidaten-Expansion
internal/playback/                — Decider (mit Quality-Cap), VAAPI-ffmpeg-Runner, HW-Detect
internal/playback/decider_test.go — Tabellen-Tests fuer Decide/DecideWithCap/IsInterlaced
internal/scanner/scanner.go       — Recursive Walk + ffprobe + Thumbnail + Metadata + Sample-Skip
internal/store/sqlite.go          — DB + Migrationen + alle Queries
internal/store/cast.go            — People, Metadata-Cast, Cast-Backfill-Tracking
internal/store/collections.go     — TMDB-Collections + Items-in-Collection
internal/tmdb/client.go           — TMDB API-Client mit Rate-Limiting
internal/trickplay/               — Background-Worker, VAAPI-HW-Decode, Hover-Thumbnail-Sprites
internal/webassets/               — //go:embed all:web (siehe „Frontend-Modul-Layout" unten)
scripts/check-frontend.sh         — Pre-Deploy: `node --check` ueber alle web/*.js
scripts/install-git-hooks.sh      — installiert pre-commit-Hook (blockt JS-Parse-Errors)
.github/workflows/deploy.yml      — Auto-Build via Portainer-API, mit Frontend-Syntax-Check
Dockerfile                        — Multi-Stage-Build
docker-compose.yml                — Template für Unraid
```

### Frontend-Modul-Layout (Stand 2026-04-30, ABGESCHLOSSEN)

`internal/webassets/web/` enthaelt mehrere kleine, fokussierte JS-Dateien
(plain `<script>`-Tags, keine ES-Modules — gemeinsamer window-Scope) plus
HTML/CSS. Die Lade-Reihenfolge in `index.html` ist relevant, weil spaetere
Module Funktionen aus frueheren nutzen.

```
internal/webassets/web/
  index.html                      — HTML-Skeleton + Dialog-Definitionen + Cast-SDK + Script-Tags
  login.html                      — Login + Setup-Form + OIDC-Button
  style.css                       — komplette App-Styles
  helpers.js          ~63 LOC     — fmtDuration/fmtSize/fmtDate/resLabel/escapeHTML
  dialogs.js         ~124 LOC     — appAlert/appConfirm/appPrompt/appDialog/showToast
  api.js              ~63 LOC     — api()/apiGetCached + 30s-LRU-Cache + 401-Redirect
  cast.js            ~142 LOC     — Google-Cast-SDK-Init + startCastSession (Token-Auth)
  player-components.js ~196 LOC   — Video.js-Custom-Buttons (Skip, Shuffle, Fav, Playlist, Delete, Cast, AirPlay)
  cards.js           ~593 LOC     — renderCard / renderFolderCard / renderCollectionCard / renderPlaylistCard / renderPersonShowCard / hidePartButton / openMissingMovieDialog
  views.js           ~878 LOC     — renderHomeView (Startseite) + Staffel-Ansicht + renderRangeContinuationCard
  grid.js            ~758 LOC     — loadItems + loadItemsBody (Branch-Logik je Anzeige-Modus)
  player.js         ~1661 LOC     — openDetail + applyPlayback + Buffer-Gate + Trickplay-Hover + Seek-Restart + syncTranscodeDisplays
  admin.js           ~540 LOC     — User-Menue + Admin-Panel + Manage-Libraries + Path-Browser + Settings
  playlists.js       ~250 LOC     — Playlist-Manager + Add-to-Playlist + Shuffle (shufflePrev/Next, playRandom)
  scan.js            ~404 LOC     — Scan-Aktionen + Status-Polling + Globale Trickplay-Statusleiste
  matching.js        ~792 LOC     — Manuelles Matching + Edit-Metadata + Refresh-All-Metadata + Missing-Movies-Export + Path-Search + Trickplay-Manager
  app.js            ~1371 LOC     — Rest: state-Objekt, Libraries-Loading, Bulk-Selection, Alphabet-Sidebar, Dialog-Drag, Filter-Modi-Helper, Topbar-Events, Boot-Wiring
```

**Lade-Reihenfolge in index.html:**
```
helpers → dialogs → api → cast → player-components → cards → views → grid → player → admin → playlists → scan → matching → app
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
  5 min Inaktivität per GC-Loop beendet; Cache unter `/config/cache/{sessionID}/`.

### Volumes

- `/media` (ro) → Unraid-Share `/mnt/user` (alle Shares als Unterordner)
- `/config` (rw) → SQLite-DB, Thumbnails, TMDB-Poster-Cache, Transcode-Cache

### DB-Schema (wichtigste Tabellen)

- `libraries(id, name, path, kind, created_at)` — kind ∈ {movies, tv, private}
- `library_paths(library_id, path)` — Multi-Path (mehrere Quellordner pro Lib)
- `items(id, library_id, path UNIQUE, rel_path, …, metadata_id, watched, watched_at)`
  — `watched`/`favorite` auf items-Ebene sind Legacy; echte Nutzerzustände liegen in `user_item_state`.
- `metadata(id, tmdb_type, tmdb_id, parent_id, title, year, release_date, overview,
             rating, genres, runtime_min, poster_path, season, episode, imdb_id, …)`
- `folder_metadata(library_id, folder, metadata_id)` — Show ↔ Serien-Ordner
- `folder_nav(library_id, folder, enabled)` — Drilldown-Flag pro Ordner (admin-togglebar)
- `users(id, username UNIQUE, password_hash, is_admin, created_at)`
- `sessions(token, user_id, created_at, expires_at)`
- `user_library_acl(user_id, library_id)` — Non-Admins sehen nur gelistete Libs
- `user_item_state(user_id, item_id, watched, watched_at, favorite, favorite_at, last_played_at)` — per-User
- `playlists(id, user_id, name, created_at)` + `playlist_items(playlist_id, item_id, position)`
- `trickplay(item_id, generated_at, interval_sec)` — Sprite-Generation-Status
- `settings(key, value)` — u. a. `tmdb_api_key`, `omdb_api_key`, `buffer_seconds`, `trickplay_interval_sec`
- `people(id, tmdb_id UNIQUE, name, profile_path, updated_at)` — TMDB-Schauspieler
- `metadata_cast(metadata_id, person_id, character, role, ord)` — Cast pro Metadata;
  `role ∈ {main, guest}`, Parent-Show liefert main bei Episoden
- `collections(id, tmdb_id UNIQUE, name, poster_path, backdrop_path, updated_at)` — TMDB-Sammlungen
- `metadata.collection_id` → verknüpft Filme mit ihrer TMDB-Collection (James Bond, Star Wars …)
- `hidden_collection_parts(user_id, collection_id, tmdb_movie_id)` — pro User ausgeblendete
  Sammlungs-Parts (z. B. Home Alone 3 in der Kevin-Sammlung, weil ohne Kevin)
- `items.metadata_confirmed INTEGER DEFAULT 0` — 1 = TMDB-Zuordnung vom User bestätigt.
  Wirkt als: (a) Filter für „⚠ Verdächtige Zuordnungen", (b) Schutz vor
  `UnmatchEpisodesInFolder`, (c) Trigger für Auto-NFO-Write
- `libraries.on_home INTEGER DEFAULT 1` — Toggle „auf der Startseite anzeigen".
  Gesteuert im Library-Manager via Checkbox pro Lib
- `settings.hwaccel_mode` — `auto` | `vaapi` | `nvenc` | `software`; wird beim
  App-Start in `hw.ApplySelection()` gelesen, wirkt live nach Settings-Save
- `metadata.cast_fetched_at` — markiert „Cast-Call bereits gemacht" auch ohne Treffer,
  verhindert Endlosschleifen im Backfill bei leeren TMDB-Credits
- `metadata.released_at` / `metadata.imdb_id` — TMDB-Felder gecacht
- `items.trickplay_status` + `items.trickplay_error` — Status: `"" | pending | done | failed` + letzte ffmpeg-Fehlermeldung
- `items.episode_end INTEGER DEFAULT 0` — Ende-Episode einer Doppelfolge (S07E23E24 →
  metadata_id=E23, episode_end=24). 0 = keine Range. Staffel-Ansicht zeigt alle
  abgedeckten Episoden als owned (gleiches Item).
- `rename_history(id, item_id, old_path, new_path, old_rel_path, new_rel_path,
  renamed_at, undone_at, triggered_by)` — Audit-Log für Auto-Rename. Jedes
  Rename schreibt einen Eintrag (auto/manual/bulk); Undo setzt `undone_at`
  und schreibt `items.path` zurück. Siehe „Auto-Rename bestätigter Filme".

## Features

### Bibliotheken
- Mehrere Bibliotheken mit Typ **Filme / Serien / Privat**.
- **Multi-Path**: pro Bibliothek beliebig viele Quellordner, werden beim Scan aggregiert.
- Pfad-Browser-Dialog nur unterhalb `/media` (Security-Check, kein Directory-Traversal).
- Inkrementelles Scannen (mtime-Vergleich), Items verwaister Dateien werden entfernt.
- **Folder-gescopter Scan:** `POST /api/scan/{libID}?folder=<rel>` beschränkt Walk +
  Orphan-Delete auf diesen Unterbaum — UI bietet das automatisch wenn man in einem
  Ordner steht (Scan-Button-Default + zwei zusätzliche Einträge im Dropdown).

### Scanner & Metadaten
- ffprobe liefert Container/Codec/Auflösung/Laufzeit/Bitrate.
- Thumbnail (480×270 JPEG) wird bei 10 % der Laufzeit mit ffmpeg extrahiert.
- Release-Date-Extraktion aus ffprobe-Tags: `creation_time` (mp4),
  `com.apple.quicktime.creationdate`, **`DATE`** (yt-dlp in MKV, Format `YYYYMMDD`),
  Fallback mtime.
- **Sample-Ordner** (`Sample`, `Samples`, case-insensitive) werden per `filepath.SkipDir`
  übersprungen — reduziert UI-Kacheln und Enrichment-Queue.
- **Auto-Backlog-Enrichment:** nach jedem Scan-Ende ruft der Scanner
  `enricher.EnrichAllFoldersNow()`. Das iteriert pro TV-Lib alle Top-Level-Folder
  mit ≥1 unmatched Item und stößt jeweils `enrichFolderSync` an. So wird die
  Queue-Reihenfolge des 5-Min-Tickers nicht zum Flaschenhals bei vielen tausend
  pending Items.

### Benutzer & Zugriff
- Erst-Setup bei leerer `users`-Tabelle: `/login.html` zeigt Setup-Formular; der erste
  Account wird automatisch Admin.
- Login per bcrypt; Session-Token in HttpOnly-Cookie (SameSite=Lax).
- **ACL:** Admins sehen alle Libraries. Non-Admins nur die in `user_library_acl` gelisteten.
  `requireLibAccess(w, r, libID)` wird in jedem Item-/Stream-/Transcode-Handler aufgerufen.
- Nutzerverwaltung (anlegen, Passwort ändern, Admin togglen, ACL bearbeiten) in der UI
  unter Settings → Benutzer (nur Admins).
- Watched + Favorite sind **pro User** (`user_item_state`), nicht pro Item.

### Trickplay (Hover-Vorschau)
- Background-Worker erzeugt Sprite-JPGs + WebVTT (10 s-Intervalle, konfigurierbar) pro Item.
- **VAAPI-Hardware-Decode** für die ffmpeg-Generierung (`-hwaccel vaapi
  -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi`, dann
  `scale_vaapi` + `hwdownload,format=nv12`). Wird in `main.go` via
  `trickplayWorker.SetHWAccelDevice(hw.Device)` konfiguriert. Ohne HW-Decode
  laufen 4K-60fps-Quellen regelmäßig in den Timeout.
- **Software-Fallback:** Wenn VAAPI zur Laufzeit scheitert (Fehler enthält
  „hwaccel initialisation", „Function not implemented" oder „No support for
  codec"), wird derselbe Befehl automatisch ohne `-hwaccel`-Header erneut
  ausgeführt. Erkennbar im Log: `[trickplay] item X: VAAPI-Init fehlgeschlagen,
  fallback auf Software`.
- **Timeout**: `duration/10 + 120s`, max 30 min.
- **Filter-Chain VAAPI**: `fps=1/N,scale_vaapi=w=160:h=90:force_original_aspect_ratio=decrease,hwdownload,format=nv12,pad=…color=black,tile=XxY`
- **Filter-Chain Software-Fallback**: `fps=1/N,scale=160:90:force_original_aspect_ratio=decrease,pad=…color=black,tile=XxY`
- Asset-Endpoints: `/api/trickplay/{id}/thumbs.vtt`, `/api/trickplay/{id}/sprite.jpg`.
- UI: Eigenes kompaktes Hover-Plugin **direkt in `app.js`** (`attachTrickplayHover`,
  `parseThumbVTT`) — parst VTT, hängt Mousemove auf `progressControl`, zeigt
  Sprite-Ausschnitt via `background-position`. Kein externes JS-Plugin.
- Worker läuft non-blocking, Status über `/api/trickplay/status`.
- Trigger-Gate: `item.trickplayStatus === "done"` aus der DB — HEAD-Probing
  würde gegen 405 laufen (chi registriert HEAD nicht automatisch für GET-Routen).
- **Admin-Dialog „Trickplay verwalten"** (Settings-Menü, admin-only):
  - Tabs mit Listen der done/failed/pending Items inkl. Fehlermeldung
  - „↻ Fehler erneut versuchen" setzt alle `failed` → `pending`, startet neu
  - „🗑 Alle Trickplay-Dateien löschen" (cancel-and-wipe)
- **Cancel-Button** in der laufenden Status-Bar (rotes ✕).
- **Ordner-Toggle ändert nie Dateien auf Disk** — Deaktivierung entfernt nur
  den DB-Marker, Dateien bleiben bis zum expliziten „Alle löschen".

### Schauspieler (Cast)
- TMDB-Credits (`/movie/{id}/credits`, `/tv/{id}/credits`, Episoden-Gäste aus
  `/tv/{id}/season/{s}/episode/{e}/credits`) werden beim Match geladen + bei
  jedem Enrich-Run im `backfillCast`-Pass für alle Metadata-Einträge nachgezogen.
- `metadata.cast_fetched_at` markiert „Aufruf gemacht" — verhindert Endlos-Retries
  bei TMDB-Einträgen ohne Cast-Daten.
- Max 15 Main-Cast pro Film/Show; Episoden-Gäste bis 30.
- Foto-Cache in `/config/people/{hash}.jpg` (w185-Größe).
- Detail-Dialog zeigt horizontalen Scroll-Strip mit runden Foto-Karten.
- Klick auf einen Schauspieler öffnet Person-Filter-Modus: Grid zeigt alle
  Videos quer über alle Libraries, in denen die Person im Cast listet.
- Endpoints: `GET /api/metadata/{id}/cast`, `GET /api/person/{tmdbId}/profile`,
  `GET /api/items?personId=<tmdbId>`.

### Sammlungen (TMDB-Collections)
- TMDB liefert bei Film-Details `belongs_to_collection` (James Bond, Star Wars, …).
- Wird beim Enrichment automatisch in `collections` upsertet und via
  `metadata.collection_id` verknüpft.
- Auto-Library-Eintrag **„Sammlungen"** im Library-Dropdown (unterhalb der
  echten Libraries, gleiche Optgroup — keine Extra-Formatierung).
- Root-Ansicht: Kacheln aller Sammlungen mit Film-Anzahl. Klick öffnet die
  Sammlung flach, sortiert chronologisch nach Release-Jahr.
- Poster-Priorität: eigenes Collection-Poster (TMDB) → Fallback Poster des
  ältesten Films (`fallbackMetaId` im API-Response) → Placeholder.
- Collection-Poster werden mit negativer ID (`-cid`) im bestehenden poster-Cache
  abgelegt; Endpoint `GET /api/poster/collection/{id}` servt sie.
- **movieCount** zählt `DISTINCT metadata_id`, nicht Files — Merge-Duplikate
  verfälschen den Counter nicht. Einzel-Film-Franchises werden angezeigt, aber
  die User möchte sie sehen.
- **Parts ausblenden (per User):** Hover auf Part-Kachel → ✕ → `POST
  /api/collections/{id}/parts/{tmdbMovieId}/hide`. Footer-Link „N ausgeblendet
  · alle anzeigen" toggelt Einblenden, ausgeblendete Parts werden dimmer
  gerendert + grüner ↺-Button zum Wiederherstellen (Button NICHT von der
  Parent-Opacity beeinflusst, sonst nicht klickbar).
- **Missing Parts sind klickbar** → öffnet TMDB-Detail-Dialog (`#missingMovieDialog`,
  Klassen `modal detail-modal` damit Layout identisch zum echten Detail-Dialog).
  Serverseitig via `GET /api/tmdb/movie/{tmdbId}` (Movie-Details + Cast).

### Merge-Duplikate
- Identische Items (gleicher `metadataId`) werden im Grid **client-seitig**
  zu einer Kachel zusammengefasst; `×N`-Badge zeigt Varianten-Anzahl.
- Repräsentant = höchste Auflösung, sonst größte Bitrate (in `groupVariants`).
- Detail-Dialog bietet Varianten-Dropdown. Auswahl setzt `state.currentItem`
  um → Play/Download/Favorit/Watched greifen auf die gewählte Datei. Dropdown-
  Label: `<Dateiname>  —  <CONTAINER> · <Auflösung> · <Größe> · <Bitrate> · …`
- **⇔ Merge-Button in der Topbar**: löst `POST /api/libraries/{id}/auto-merge-duplicates`
  aus. Server iteriert pro Ordner: wenn *genau eine* TMDB-Zuordnung existiert,
  werden Geschwister-Items ohne/mit abweichender Zuordnung angeglichen. Ordner
  mit mehreren konkurrierenden Zuordnungen (echte Trilogien) bleiben unberührt
  und werden als `skippedConflicts` zurückgegeben. Nur für Movies-Libs.
- **Manueller Bulk-Merge (API)**: `POST /api/items/merge` mit `{ids:[…]}` —
  erstes Item mit metadata_id ist canonical, andere übernehmen. Endpoint
  existiert, aber UI-Button dafür ist nicht mehr in der Bulk-Bar.

### Flat-View & Bulk-Selection
- **Flat-View-Toggle** im Toolbar (📂) — blendet die Ordner-Navigation aus und
  zeigt alle Videos der Library flach. Persistiert in `localStorage`.
- **Bulk-Auswahl** via `☑ Auswählen`-Button: aktiviert `selection-mode`
  (CSS-Klasse auf `body`), blendet Checkboxen auf jeder Kachel ein. Click im
  Mode togglet Auswahl statt Detail zu öffnen.
- Sticky Action-Bar: `Alle · Keine · ♡ Favorit · ✓ Gesehen · 📋 Playlist ·
  ⬇ Download · 🗑 Löschen`. Bulk-Download triggert `<a download>`-Clicks
  mit 400 ms Abstand (Browser-Popup-Blocker).
- `state.lastRenderedItems` speichert die gerade gerenderte Liste — „Alle
  auswählen" arbeitet darauf.

### UI
- Grid-Ansicht mit Kachel-Thumbnails oder TMDB-Poster. Movies/TV-Libraries nutzen
  **2:3-Poster-Kacheln** (`card--poster`), Private-Libs weiterhin 16:9-Thumbnail.
- **Private-Libs (YouTube, Urlaubsvideos, …)**: Kachel zeigt den **Dateinamen**
  (ohne Extension) als Titel. Der Top-Ordnername wird NICHT auf der Kachel
  angezeigt — er ist im Breadcrumb und im Detail-Dialog (rel_path) ohnehin
  sichtbar. Default-Sort in privaten Libs ist „Veröffentlicht" **aufsteigend**
  (älteste zuerst) — in `restoreSortForContext()` anhand `lib.kind === "private"`
  gesetzt. Manuell geänderter Sort wird weiterhin pro Lib+Ordner in
  `localStorage` unter `sort:lib:<libID>:<folder>` persistiert.
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
- **Alphabet-Sidebar rechts** (`#alphaSidebar`): zeigt A-Z + `#` bei Sort=title
  und ≥10 Items, in Collections-Root unabhängig vom Sort. `jumpToLetter()`
  scrollt zur ersten Kachel mit passendem Anfangsbuchstaben (Cards haben
  `data-item-id`). Body bekommt Klasse `has-alpha-sidebar`, dann nimmt
  das Grid 36px Rand rechts frei.
- Favoriten-Filter zeigt flach innerhalb der aktuellen Library (ohne Ordner-Ebenen).
- **Library-Wechsel** setzt alle Filter/Suche/Sortierung zurück.
- **Request-Sequencing** in `loadItems` (`state.loadSeq`): verhindert, dass bei
  schneller Live-Suche ein älterer Response das Grid mit stale Daten überschreibt.
- Auto-Sort bei TV-Subfolder: `episode` (nach Staffel+Episode aus TMDB-Metadata).
- Detail-Dialog mit Poster, Plot, Rating, Genres, Episode-Info + Buttons:
  „Abspielen" / „Als gesehen markieren" / „♡ Favorit" / „Zu Playlist" / „Download" /
  „Löschen" (Admin-only) / „Manuell zuordnen" (Movies/TV).
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

### Startseite (Home-View)
- Default-Ansicht beim ersten Öffnen (`state.homeView = true`) + 🏠-Button in
  der Topbar. Zeigt pro Library einen eigenen Block mit drei Streifen:
  **▶ Fortsetzen** (Items mit Resume-Position), **📺 Als nächstes** (nächste
  ungesehene Episode je Serie, nur TV-Libs), **🆕 Zuletzt hinzugefügt**.
- Libraries mit `on_home=0` (Checkbox im Library-Manager) werden komplett
  ausgeblendet.
- Library-Name-Überschrift klickbar → öffnet die Lib in Standard-Ansicht.
- Suchfeld in der Topbar ist auf Home-View **library-übergreifend**:
  matcht gegen Titel + Schauspieler über alle Libraries mit ACL-Zugriff.
- API: `GET /api/home` liefert `{sections: [{library, continue, nextUp,
  recent}, …]}`, `PUT /api/libraries/{id}/home-visibility` togglet `on_home`.

### Doppelfolgen (Episoden-Range)
- Dateinamen wie `S07E23E24.mkv`, `S07E23-E24.mkv`, `S07E23 E24 Finale.mkv` werden
  vom Parser erkannt: `Episode=23`, `EpisodeEnd=24`. Bei 3+ E-Blöcken (`S02E10E11E12`)
  wird nur das erste Zusatz-E gecaptured, weitere non-capturing konsumiert (Regex
  matcht weiterhin). Sanity-Limit: `EpisodeEnd-Episode ≤ 10`.
- Enricher matcht das Item auf die TMDB-Metadata der ERSTEN Episode (`E23`) und
  schreibt `items.episode_end = 24`. Kein zweiter TMDB-Call.
- `SeriesOwnedEpisodes` liefert `EpisodeEnd` mit; der Seasons-API-Handler baut
  einen `ownedLookup[season][episode] → {ItemID, EpisodeEnd}` auf, der jeden
  abgedeckten Slot belegt. `maxEpisode` wird auf das Ende der Range gekappt.
- Frontend: Im normalen Grid zeigt die Kachel `S07E23-24` wenn `it.episodeEnd`
  gesetzt ist. In der Staffel-Ansicht bekommt der primäre Slot (E23) eine
  volle `renderCard`, weitere Slots (E24, …) einen schmalen
  `renderRangeContinuationCard`-Stub mit „Teil von S07E23-24"-Badge, beide
  öffnen dasselbe Item.
- NFO-Writer schreibt `<episodenumberend>24</episodenumberend>` wenn `episode_end > episode`
  (Kodi/Jellyfin/Plex-kompatibel).
- **Startup-Backfill:** Bei leerem `settings.episode_range_backfill_v1` läuft
  `backfillEpisodeRanges` einmal durch, parsed Dateinamen aller Episoden-Items
  mit mindestens zwei 'E' im Pfad und schreibt erkannte Ranges zurück.
  Setting-Flag verhindert Re-Run nach Restart.

### Staffel-Ansicht für Serien
- Toggle-Button `📺 Staffeln` in der Topbar, nur in TV-Libs sichtbar.
  Zwei Ebenen:
  - **Library-Default**: localStorage `seasonView:lib:<libID>` = "1"/"0"
  - **Pro-Serie-Override**: localStorage `seasonView:<libID>:<folder>`
  - Effective = per-Folder if set, else library-Default
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

### Metadaten-Bestätigung + Verdächtige Zuordnungen
- **`✅ Zuordnung bestätigen`**-Button im Detail-Dialog togglet
  `items.metadata_confirmed`. Grün umrandet wenn bestätigt.
- Manuelle Zuordnung via `POST /api/items/{id}/metadata` setzt
  `metadata_confirmed=1` implizit (ConfirmItemMatch).
- Bestätigte Items:
  - Erscheinen nicht in „⚠ Verdächtige Zuordnungen"
  - Werden von `UnmatchEpisodesInFolder` nicht angetastet (bei Show-Re-Match
    bleibt die individuell bestätigte Episode erhalten)
  - Lösen Auto-NFO-Write aus (siehe „NFO-Sidecars")
- Unmatch (`SetItemMetadata(id, 0)`) setzt confirmed ebenfalls auf 0 zurück.

### NFO-Sidecars (Plex/Jellyfin-Kompatibilität)
- Kodi-kompatibles XML-Format in `<Dateiname>.nfo` neben der Videodatei,
  plus `tvshow.nfo` im Top-Level-Serien-Ordner.
- Package: `internal/nfo/writer.go` (WriteMovie, WriteEpisode, WriteTVShow).
- Unique-ID per `<uniqueid type="tmdb" default="true">`; zusätzlich IMDb-ID
  wenn vorhanden. Genres aus dem Genres-JSON-String der Metadata.
- Trigger: automatisch beim ✅-Confirm (via `confirmItemMetadata`), beim
  manuellen Assign (`setItemMetadata`/`setFolderMetadata`), manuell via 💾-
  Button im Detail-Dialog, bulk-retroaktiv via Zahnrad-Menü → „💾 NFO für
  alle bestätigten" (`POST /api/items/write-all-nfos`, Admin).
- Schreibfehler werden in den Aufrufsites still geloggt, der Match-Call
  bricht nicht ab — NFO ist Komfort-Feature, nicht Blocker.

### Hardware-Beschleunigung (generisch)
- `HWAccel`-Struct mit `Selected | VAAPIAvailable | NVENCAvailable` +
  Driver-Infos für beide. `Detect()` prüft VAAPI (`/dev/dri` + vainfo) und
  NVENC (`/dev/nvidia0` + ffmpeg-Encoder). Auto-Default: VAAPI > NVENC >
  Software.
- Settings → Hardware-Beschleunigung: Dropdown `Auto`/`Intel/AMD VAAPI`/
  `NVIDIA NVENC`/`Software`. Settings-Save ruft `hw.ApplySelection()` auf
  Server + pusht live in `Playback.SetHWAccel` + `Trickplay.SetBackend`.
- Trickplay-Pfade pro Backend (siehe Trickplay-Abschnitt). Transcode-Pfade
  analog in `playback/ffmpeg.go` mit switch `m.hw.Selected`.
- **Unraid-Voraussetzung für NVENC**: NVIDIA-Plugin installiert + geladen;
  Compose mit `runtime: nvidia` + `NVIDIA_VISIBLE_DEVICES=all` +
  `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility`. Ohne geladenen
  Treiber → Container-Start crasht mit „driver not loaded".
- Benchmark (96-min-1080p, Intel iGPU + Quadro P400): VAAPI 60 s,
  NVENC 219 s, Software 703 s. Auf dieser Hardware VAAPI-Default richtig.

### Playback
- **Direct Play**: mp4/mov mit h264/aac → Originaldatei per HTTP-Range.
- **Transcode** (auto bei inkompatiblen Formaten): HLS, H.264/AAC.
- **HLS-Playlist: `#EXT-X-PLAYLIST-TYPE:EVENT`** (via ffmpeg-Flag
  `-hls_playlist_type event`). Verhindert, dass Video.js/VHS die wachsende
  Playlist als Live-Stream erkennt und bei Play nach Pause zur Live-Edge
  springt. `-hls_flags` steht auf `independent_segments` allein — kein
  `append_list`, weil ffmpeg dann `#EXT-X-DISCONTINUITY` vor das erste
  Segment schreibt und VHS daraufhin keine Segmente bei time=0 lädt. Dank
  `cleanDir`-Reset vor jedem Session-Start ist `append_list` entbehrlich.
- **Playlist-Rewriter** (`api/stream.go`) hängt Query-Parameter an jede
  Segment-URI UND strippt eine führende `#EXT-X-DISCONTINUITY` vor dem ersten
  Segment — belt-and-suspenders-Schutz gegen VHS-Start-Gap.
- **Profile**: Original / 1080p @ 5 Mbps / 720p @ 2.5 Mbps / 480p @ 1 Mbps / 360p @ 700 kbps.
- **Quality-Cap im Auto-Modus**: `DecideWithCap(it, profile)` erzwingt Transcode, wenn
  Itemhöhe/-bitrate das Profil überschreitet — auch bei browser-kompatiblen Dateien.
  UI-Label im Player: „Maximum" (Auto) vs. „Profil" (Transcode), ausgeblendet bei Direct Play.
- **Modus-Override**: Im Player-Dialog Auto/Direct/Transcode manuell wählbar.
- **Audio-/Subtitle-Auswahl**: Nur bei Transcode (Dropdown im Player-Dialog).
  Subs als WebVTT-Remote-Text-Track, Server konvertiert on-the-fly.
- **Hardware-Accel**: Intel VAAPI (out-of-the-box via `/dev/dri`-Passthrough +
  `group_add 107`). NVIDIA NVENC optional wenn `runtime: nvidia` gesetzt ist.
  Settings-Dropdown „Auto / VAAPI / NVENC / Software" (siehe
  „Hardware-Beschleunigung"-Abschnitt). Software-Fallback bei Runtime-Fehlern.
- Konfigurierbarer **Client-Puffer** (5–180 s) über `hls.config.maxBufferLength`.

### Player-UI (Video.js Custom Components)
- `ensurePlayerComponents()` registriert Subklassen von `videojs.getComponent("Button")`
  einmal pro Session; `applyPlayback` fügt sie in die `controlBar` ein:
  **ShufflePrev** (⏮), **ShuffleNext** (⏭), **FavoriteButton** (♡/♥), **PlaylistButton** (📋).
- Vorteil gegenüber externen Buttons im Dialog-Header: bleiben im Fullscreen-Modus
  sichtbar, werden von Video.js-UX (Hover-Autohide) mitgesteuert.
- `FavoriteButton`-Klick togglet `/api/items/:id/favorite` und setzt `.vjs-favorite--on`
  (rotes Herz). `PlaylistButton` verlässt ggf. Fullscreen und öffnet den
  „Zu Playlist hinzufügen"-Dialog.
- **DeleteButton** (admin-only, 🗑) sitzt ebenfalls in der Control-Bar neben
  PlaylistButton. Idle dezent grau (opacity 0.65), Hover rötlich. Doppelte
  `appConfirm`-Bestätigung → `DELETE /api/items/:id?deleteFile=true` →
  Player-Close + Grid-Reload. Wird in `applyPlayback` nur hinzugefügt wenn
  `state.me.isAdmin`.
- **Custom-Buttons sitzen am Ende der ControlBar** (vor `FullscreenToggle`) —
  früher Einfügen bei Index 1-4 hat den Progress-Control-Flex ausgequetscht, die
  Progress-Bar war weg.
- **Video.js-Instanz wird bei Source-Wechsel wiederverwendet** (z. B. beim Shuffle-
  Next/Prev): `vjs.src({...})` + `vjs.play()`, statt `dispose()` + `new videojs()`.
  So bleibt Fullscreen erhalten. Bei Reuse werden alte Remote-Text-Tracks entfernt.
- **Dialog VOR Player-Init öffnen:** `applyPlayback` misst Breite direkt nach
  `showModal()`; sonst greift `vjs-layout-tiny` und blendet Controls aus.
- **`liveui: false` + `responsive: false`** in den Player-Optionen: unsere
  progressive HLS-Playlist hat kein ENDLIST → Video.js hält sie für live. CSS
  erzwingt zusätzlich `.vjs-progress-control { display: flex !important; }`.
- **`forcePlayerDuration(vjs, durationSec)`** schreibt die ffprobe-Filmlänge in
  den `duration`-Cache (persistiert bei jedem `durationchange` via Re-Apply).
  Ohne das wäre die Progress-Bar im Transcode-Mode nie voll und Trickplay-Hover
  rechnet Maus-X auf falsche Zeiten um.
- **Startpuffer-Gate** (`settings.start_buffer_seconds`, 0-120 s): pausiert den
  Player zu Beginn, bis genug Vorlauf da ist. Zentriertes Overlay mit
  Fortschrittsbalken + „Jetzt starten"-Button über dem Videobild.
  - **HLS-Transcode**: misst die **ffmpeg-Server-Position** via
    `/api/transcode/:id/progress` (alle 800 ms). VHS bufffert im Pause-Zustand
    pro Design nur 1 Segment — der echte Puffer-Indikator ist daher, wieviel
    ffmpeg-seitig schon transcodiert wurde. Sobald genug Vorlauf gibt es,
    Release + `play()`; Client holt die Segmente dann bei Bedarf rapide nach.
  - **Direct Play (mp4)**: misst Client-`buffered()`-Range am `wantedPos`,
    funktioniert weil progressives mp4 auch im Pause-Zustand buffert.
    Effective Target = `min(startBuffer, bufferSeconds - 2)`, da VHS' Goal-
    Buffer auch hier das Cap ist.
  - Sicheres Pause-Halten via `onPlayGate`-Listener; `onSeeked`-Snap wäre ein
    Trap (erzeugt Seek-Loop mit VHS' Segment-Loader → Buffer bleibt bei 5 s).
  - Kick-off: `vjs.play()` einmalig, dann warten auf `canplay`/`progress`-
    Event (echtes Signal „erstes Segment geladen"); erst danach pausieren +
    auf `wantedPos` seeken. Safety-Timeout 5 s falls kein Event kommt.
  - `state.playback.startWantedSec` trägt die Ziel-Position (0 bei Transcode
    + Von-Anfang, Resume-Position bei Fortsetzen + Direct Play).
- **Buffer-Overlay** zeigt `<Auflösung> · [Server +N s ·] Buffer +M s`
  (Server-Offset nur beim Transcode). Auflösung aus `video.videoWidth/Height`
  (tatsächliche Render-Auflösung).
- **Zwei Darstellungsmodi** (Toggle via `positionBufferOverlay(vjs)` beim
  Player-Open und bei `fullscreenchange`):
  - **Docked** (eingebetteter Player, Default): Element sitzt außerhalb der
    `.video-stage` als Geschwister in `.player-wrap`, direkt über dem Footer.
    Schmaler Streifen (dunkles Grau, blauer Text), Titel ausgeblendet (steht
    im Dialog-Header). Klasse: `transcode-ahead--docked`.
  - **Floating** (Fullscreen): Element wird in `vjs.el()` verschoben,
    `position: absolute` oben rechts mit Titel-Zeile. Fadet über CSS-Regel
    `.vjs-user-inactive.vjs-playing .transcode-ahead { opacity: 0 }` synchron
    mit der Progress-Bar aus (1 s Transition).
- **Gotcha:** Der Polling-Loop (`setClass` in `startBufferDisplay`) darf NICHT
  `el.className = "transcode-ahead"` machen — das würde `--docked` bei jedem
  Tick wegräumen. Nur die Status-Marker (`behind`/`low`) via
  `classList.add/remove` toggeln.
- `hideBufferOverlay` fügt die `hidden`-Klasse beim Player-Close; `disposePlayer`
  schiebt das Overlay zurück in `.player-wrap` (falls es im Fullscreen im
  vjs-Root saß und der Root beim Dispose verschwindet).
- **`resLabel(it)`** rechnet `max(height, width*9/16)` — Cinemascope-Filme
  (1920×800) landen korrekt im 1080p-Bucket statt 720p. Server-Bucket-Filter
  nutzt dieselbe Formel (`MAX(i.height, i.width*9/16)`).

### Shuffle-Play
- Globale Zufallswiedergabe mit **History-Navigation**: `state.shuffleHistory` +
  `state.shuffleIdx` — ⏮ geht zurück, ⏭ spielt neues Zufallsitem (oder aus History
  weiterblättern, wenn schon zurückgesprungen wurde).
- Zufallspool berücksichtigt aktuelle Library/Folder/Search/Watched-Filter.
- `openPlayer(item, {fromShuffle: true})` erhält den Shuffle-State beim Item-Wechsel.

### Playlists (per User)
- Jede Playlist gehört genau einem User; Items werden in `playlist_items` mit
  `position`-Reihenfolge gehalten.
- Auto-Next: Beim `ended`-Event des Players spielt das nächste Queue-Item automatisch.
- UI: Topbar zeigt Playlist-Auswahl; Detail-Dialog und Player-Control-Bar haben
  „Zu Playlist hinzufügen"-Button (neu-erstellen direkt aus dem Dialog möglich).

### Transcode-Seek (Capture-Handler + Session-Restart)
- Die HLS-Session-Seekable-Range wächst nur bis zum aktuell produzierten Segment.
  Klickt der User dahinter, würde Video.js auf Seekable-Ende clampen
  („nur ein paar Sekunden vorwärts").
- Fix: Capture-Phase-Handler auf `progressControl` fängt den Klick ab **bevor**
  Video.js clampt, rechnet das absolute Ziel aus (`ratio * item.durationSec`),
  und startet eine **neue ffmpeg-Session mit `start=<target>`**.
- State `state.playback.virtualOffset` trackt die aktuelle Source-Start-Zeit.
- `forcePlayerDuration(vjs, total)` hält die volle Filmlänge im `duration`-Cache.
- `syncTranscodeDisplays` läuft auf `requestAnimationFrame` und überschreibt
  die Zeit-/Progress-Anzeigen mit `techTime + virtualOffset`, damit die
  Oberfläche absolute Positionen zeigt. Video.js' eigene TimeDisplay-Updates
  werden im Transcode-Modus als No-Op gepatched, um Flicker zu verhindern.
- **Wichtig — HLS-Segment-URLs:** Der Playlist-Handler schreibt die m3u8
  on-the-fly um und hängt die Query-Parameter (`profile`/`start`/`audio`) an
  jede `seg*.ts`-Zeile. Ohne das verlieren Segment-Requests ihre Parameter
  und landen auf der Default-Session mit startSec=0 → Video spielt von vorn
  statt am Seek-Ziel.

### Performance
- **gzip-Kompression** aller Text/JSON/JS/CSS/VTT/M3U8-Responses
  (`middleware.Compress(5, ...)` in chi).
- **Cache-Control** auf Assets: Fonts/SVG lange (7 Tage), JS/CSS kurz
  (`max-age=60, must-revalidate` + ETag via http.ServeContent), HTML
  `no-cache`.
- **content-visibility: auto** + `contain-intrinsic-size` auf `.card`
  → Browser rendert off-screen Kacheln nicht.
- **`<img loading="lazy">`** für Poster/Thumbs (statt CSS-background).
- **Client-Items-Cache**: letzte 5 Items-List-Responses in-memory (TTL 30s),
  invalidiert bei Mutation. `apiGetCached(path)` wrapt die üblichen Fetches.
- **Request-Sequencing** in `loadItems` (`state.loadSeq`) — stale responses
  beim Tippen ins Suchfeld können das Grid nicht mehr überschreiben.
- **DB-Indexe** auf `items(library_id, added_at|duration_sec|height|rel_path)`
  und `user_item_state(user_id, last_played_at)`.

### Filter-UI
- **Auflösungs-Filter**: kompaktes Dropdown mit Checkboxen (Multi-Select).
  Server-seitig werden mehrere `bucket`-Werte per OR geORd.
  Buckets: 4K / 2K / 1080p / 720p / 576p / 540p / 480p / ≤360p.
- **Sortierung** mit Default-Richtung je Feld (Title/Episode asc, Rest desc);
  ⬆/⬇-Button neben dem Dropdown flippt die Richtung.
- **Duplikate** ist ein **Eintrag im Sort-Dropdown** (nicht eigenes Filterfeld).
  Aktiv → alle Items mit mehrfach vergebener `metadata_id` flach ohne Merge.
- **Favoriten-Filter** auf „Nur" stellt sofort eine flache Library-weite
  Ansicht (wie Duplikate, aber eigener Pfad in loadItems).
- **„Zuletzt abgespielt"-Sort** zeigt ebenfalls eine flache Library-Ansicht
  (gleiche Struktur wie Favoriten). Server-seitig filtert ListItems bei
  `Sort=="played"` zusätzlich `AND us.last_played_at IS NOT NULL`, damit
  nur tatsächlich abgespielte Items erscheinen.
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

### Person-Filter (Schauspieler-Klick)
- Klick auf einen Schauspieler-Cast öffnet einen library-übergreifenden
  Filter (`state.personFilter`). Server-Endpoint `GET /api/items?personId=<tmdb>`.
- **Split-Rendering** in zwei Sektionen:
  - **🎬 Filme**: normale `renderCard`-Kacheln, client-seitig chronologisch
    sortiert (neueste zuerst, via `metadata.releaseDate`/`releasedAt`).
  - **📺 Serien**: Pro (libraryId + rel_path[0]) genau EINE Sammelkachel
    (`renderPersonShowCard`). Poster via `/api/poster/metadata/<parentId>`
    (Parent-Show-Metadata), Badge zeigt Anzahl gefundener Episoden. Klick
    navigiert zum Show-Ordner wie der `data-show-link`-Klick auf einer
    Episoden-Kachel.
- Sektion-Headings via `.person-section-title` (grid-column: 1/-1).

### Bulk-Selection
- Toolbar-Button „☑ Auswählen" aktiviert `body.selection-mode`.
- Sticky Aktionsleiste: „N ausgewählt" · Alle · Keine · ♡ Favorit · ✓ Gesehen
  · 📋 Playlist · ⬇ Download · 🗑 Löschen.
- Download-Bulk triggert sequentielle `<a download>`-Clicks mit 400ms Abstand.

### Topbar-Navigation
- Drei gleich große Icon-Buttons links in der Topbar: **🏠 Home**,
  **📚 Sammlungen**, **📋 Playlists**. Alle mit Klasse `.nav-icon-btn`
  (feste Breite 38 px, font-size 16 px) — unterschiedliche Emoji-Breiten
  führen sonst zu optisch ungleichen Kästchen.
- Sammlungen NICHT mehr im `#librarySelect`-Dropdown (früher als `col:`-Entry).
  Handler: `#collectionsBtn` setzt `state.collectionsView=true` und ruft
  `loadItems()`.

### Sammlungs-Komplett-Badge
- `store.Collection` enthält `PartCount` (alle TMDB-Parts der Sammlung) und
  `HiddenCount` (davon vom aktuellen User ausgeblendet). `ListCollections`
  nimmt jetzt `userID` als Parameter.
- Frontend (`renderCollectionCard`): `complete = movieCount >= partCount - hiddenCount`.
  Wenn true, grünes „✓ komplett"-Badge oben links (`.collection-complete`).
- Der Zähler unten rechts zeigt standardmäßig `N/Total Filme` (statt nur `N Filme`).
  Bei `partCount=0` (Parts noch nicht gefetcht) Fallback auf den alten Zähler.

### Einstellungen / Admin-UI
- **Zahnrad-Button** (`#settingsBtn`) ist für Non-Admin-User komplett
  ausgeblendet (`hidden`-Klasse in `renderUserMenu`). Das Menü enthält
  ausschließlich administrative Einträge.
- **Entfernt aus dem Menü**: „Duplikate zusammenführen" (Auto-Merge passiert
  eh bei gleicher TMDB-ID) und „NFO für alle bestätigten" (läuft automatisch
  beim Bestätigen). Die Server-Endpoints bleiben erhalten, nur die UI-Einträge
  + zugehörige Helfer (`autoMergeLibrary`, `writeAllConfirmedNFOs`) sind raus.
- **Toast-Helper** `showToast(msg, {kind:"info"|"success"|"error", duration?})`:
  unaufdringliches, nicht-modales Feedback rechts unten. Container
  `#toastRoot` wird bei Bedarf lazy angelegt.

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

### Download & Löschen
- **Download** (`GET /api/items/:id/download`): Original-Datei mit `Content-Disposition:
  attachment` ausliefern. Kein Transcode.
- **Löschen** (Admin-only, `DELETE /api/items/:id?deleteFile=true|false`): Item aus DB
  und optional auch die Datei von Disk entfernen.

### Auto-Rename bestätigter Filme (seit 2026-04-30)
- Setting `auto_rename_confirmed_movies` (Toggle in Settings → „Datei-Umbenennung",
  iOS-Style Slider rechts in der Zeile). Wenn an: jede ✅-Bestätigung eines
  Films **mit Library-`kind=movies`** (auch wenn die Lib „Bluray", „4K-Filme"
  etc. heißt) löst eine Umbenennung der Datei zu `<Title> (<Year>).<ext>` aus.
- Greift NICHT auf TV/Private-Libs und auch nicht auf Episoden — Filter über
  `library.kind = "movies"` UND `metadata.tmdb_type = "movie"`.
- Sanitize: `<>:"/\|?*` und Steuerzeichen werden aus dem Title entfernt;
  trailing dots+spaces gestrippt. Bei Year=0 nur `Title.ext`.
- Konflikt: existiert die Zieldatei → Suffix ` (2)`, ` (3)` … bis 99.
- **rename_history-Tabelle** protokolliert jede Aktion (id, item_id, old_path,
  new_path, old_rel_path, new_rel_path, renamed_at, undone_at, triggered_by ∈
  {auto, manual, bulk}). Wird via DB-Transaktion atomar mit dem
  `items.path`-Update geschrieben.
- **Manueller 🏷-Button** im Detail-Dialog — admin-only, sichtbar bei
  bestätigten Filmen mit Movies-Lib. Tooltip zeigt Ziel-Dateiname (Preview-
  API ohne Side-Effect). Funktioniert auch mit Setting=AUS — User kann so
  vor Aktivierung einzelne Files testen.
- **Umbenennungen-Manager** im Zahnrad-Menü (`📝 Umbenennungen verwalten`):
  Tabelle mit allen Renames inkl. ↩-Undo pro Eintrag, „⬇ CSV exportieren"
  (Browser-Download), „Alle bestätigten Filme jetzt umbenennen" (Bulk).
- **Lautloser Card-Refresh:** nach Confirm/Manual-Rename wird die einzelne
  Kachel im Grid via `silentlyRefreshItem(id)` in-place ersetzt — KEIN
  `loadItems()`-Reload, Scroll-Position bleibt erhalten.
- **Kachel-Indikator:** kleiner grüner ✓ in der Meta-Zeile (10px) bei allen
  Items mit `metadata_confirmed = 1`. Klasse `.confirmed-tick`.
- Code-Pfade:
  - `internal/rename/rename.go` — SanitizeFilename, TargetFilename,
    ResolveConflict, PreviewTarget, RenameOnDisk (+ rename_test.go).
  - `internal/store/rename_history.go` — RecordRename (TX), MarkRenameUndone
    (TX), GetRenameHistory, ListRenameHistory, ListConfirmedMovies.
  - `internal/api/admin_rename.go` — 6 Endpoints + computeRenameTargetForItem
    + executeRename. Gemeinsamer Code-Pfad für manual/auto/bulk.
  - Hook in `confirmItemMetadata` (api/items.go) — wenn
    `s.settingAutoRenameOn()`. Fehler nur ins Log, Confirm bleibt erfolgreich.
- **NICHT entfernen** ohne Verständnis: Library-Kind-Filter (`lib.Kind ==
  "movies"`) ist explizit gewünscht. Bei Refactor-Versuchen, „warum prüft
  ihr das doppelt (movie-metadata + movie-lib)" — der Lib-Filter ist die
  *primäre* Schutzmaßnahme; metadata.tmdb_type ist redundant aber harmlos.

### TMDB-Integration
- Suche & Detail für Filme, Serien, Episoden (deutsche Sprache).
- Match-Strategie: Name-Parser → TMDB-Search → Jahres-Score → Auto-Match; Fallback manuell.
- Poster werden nach `/config/posters/` gecacht (einmalig pro Metadata-ID).
- **Manuelles Matching**: pro Item oder pro Serien-Ordner; Folder-Match triggert
  sofortiges Episode-Matching für genau diesen Ordner. Item-Match (TMDB-Search
  ODER IMDb-ID) bestätigt die Zuordnung implizit (`ConfirmItemMatch` setzt
  `metadata_confirmed=1`) und öffnet **nach dem Submit den Detail-Dialog** mit
  frisch gelesenem Item — der User sieht sofort Poster, Plot, Cast und kann
  verifizieren oder abspielen. Folder-Match öffnet keinen Dialog (kein einzelnes
  Item im Fokus).
- Privatvideos: `kind=private` → keine TMDB-Calls.
- Enrichment-Queue: max. 35 req/10 s, läuft non-blocking im Hintergrund.

### TMDB-Client-Cache
- In-Memory-TTL-Cache (15 min) für `GetTV`, `GetTVCredits`, `GetSeason` im
  `tmdb.Client`. Reduziert Round-Trips massiv wenn die Staffel-Ansicht mehrfach
  geöffnet wird UND entkoppelt User-Requests vom Enricher-Backlog, der denselben
  Rate-Limiter (35 req/10 s) teilt.
- Cache-Keys: `tv:<id>:<lang>`, `tvcredits:<id>:<lang>`, `season:<showID>:<n>:<lang>`.
- `InvalidateShow(showID)` löscht alle drei Präfixe auf einmal — wird vom
  „↻ TMDB neu laden"-Button via `?refresh=true` getriggert.
- Lazy Cleanup bei >500 Entries. Kein Persistenz-Layer, Restart leert den Cache.

### Gesehen-Markierung
- `items.watched` + `watched_at`.
- Auto-Markierung bei 90 % Laufzeit (einmal pro Player-Session).
- Manuell togglebar im Detail-Dialog.
- Filter in Topbar: Alle / Nur ungesehen / Nur gesehen.
- Visuell: grünes ✓-Badge, abgedunkelte Kachel, gedimmter Titel.

### Name-Parser (internal/nameparser)
- `ParseFile` — für Filme, nur SxxExx/NxN erkennen.
- `ParseEpisodeFile` — TV-Kontext, zusätzlich 3–4-stellige Episode-Codes:
  `104` → S1E04, `1004` → S10E04. Jahre (1900–2099) explizit ausgeschlossen,
  Plausibilität: Season 1–29, Episode 1–99.
- `ParseFolder` — Show-Name + Jahr aus Ordnernamen (z. B. `Banshee (2013)`).
- Release-Trash wird rausgestrippt: `1080p`, `x264`, `BluRay`, `GERMAN`, `DL`, `-GROUP`, …
- **`variants.go`**: `ExpandCandidates(title)` erzeugt zusätzliche Such-Varianten:
  - **De-leet** (`Deleetify`): 7→t, 4→a, 3→e, 0→o, 5→s, 1→i — nur auf Tokens mit
    Buchstaben-Mehrheit, damit reine Zahlen (Jahre, Staffelnummern) unangetastet bleiben.
  - **Longest-Word-Fallback**: aus Obfuskations-Dateinamen wie `Sitrb.Langsam.1988`
    wird der längste Token (`Langsam`) plus Jahr extrahiert — TMDB findet „Stirb langsam".
- **Enrichment-Kandidatenliste**: Worker probiert Datei → alle rel_path-Segmente rückwärts,
  jeweils plus Deleet-/Longest-Variante. Dedup via Lowercase-Title+Year-Key.

## Deployment

### Portainer-Stack (live)
- **Server:** <UNRAID-LAN-IP>:9000 (Portainer CE 2.39.1)
- **Endpoint-ID:** 3 (`local`, Docker standalone)
- **Stack-ID:** 37 (Name `videoplayer`)
- **URL:** http://<UNRAID-LAN-IP>:8098

### Build & Redeploy-Flow (vom Entwicklerrechner ohne Go-Installation)

1. **Tar** des Source-Trees (ohne macOS-xattrs):
   ```
   COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata \
     --exclude='.DS_Store' --exclude='config' --exclude='cache' \
     -cf /tmp/videoplayer-src.tar -C /Users/christian/Projekte/Videoplayer .
   ```
2. **Build** via Portainer Docker-Proxy:
   ```
   POST /api/endpoints/3/docker/build?t=simple-videoplayer:latest
   Content-Type: application/x-tar
   Body: <tarball>
   ```
3. **Redeploy** des Stacks:
   ```
   PUT /api/stacks/37?endpointId=3
   Body: {"stackFileContent": "<compose-yml>", "prune": false, "pullImage": false}
   ```
4. Named Volume `videoplayer_config` bleibt erhalten — DB + Poster-Cache überleben
   Redeploys.

### docker-compose.yml (gekürzt)

```yaml
services:
  videoplayer:
    image: simple-videoplayer:latest
    ports: ["8098:8096"]
    devices: ["/dev/dri:/dev/dri"]   # VAAPI-Passthrough
    group_add: ["107"]                 # render-group
    volumes:
      - videoplayer_config:/config
      - /mnt/user:/media:ro
    environment:
      - VP_LISTEN=:8096
      - VP_CONFIG_DIR=/config
      - TZ=Europe/Berlin
volumes:
  videoplayer_config:
```

## Bekannte Probleme & Lösungen (Decision Log)

### ✅ Server-Buffer springt zyklisch alle ~60 s auf 0, Wiedergabe stallt (2026-05-02)
- **Symptom:** Bei Transcode-Wiedergabe alle ~60 s Sprung der
  Server-Buffer-Anzeige auf 0, Browser-Buffer waechst nicht weiter,
  Video stoppt. Sehr wiederkehrend, betrifft jede Wiedergabe ueber 1 min.
- **Ursache:** `Manager.ConsumeFresh` hatte als Idempotenz ein 60-s-
  Wallclock-Fenster. VHS laedt aber EVENT-Playlists **kontinuierlich**
  mit `fresh=1` in der URL. Nach 60 s lief das Fenster ab, die naechste
  Reload kam durch, ConsumeFresh sagte „OK", `StopSession` killte die
  laufende ffmpeg-Session, `StartOrGet` startete frisch von vorn →
  Browser-Buffer leer, Stall. Loop alle 60 s.
- **Fix:** Discriminator von Wallclock auf den `_t`-URL-Token umstellen.
  Das Frontend setzt `_t=Date.now()` einmalig pro `applyPlayback`-Aufruf.
  VHS-Reloads behalten denselben Token (= no-op), ein echter Player-Open
  generiert einen neuen Token (= killen+neu starten ist erlaubt).
  Manager-Field `freshHandledAt map[string]time.Time` →
  `freshTokens map[string]string`. Caller in `transcodePlaylist`
  uebergibt `r.URL.Query().Get("_t")` als Token.
- **Niemals zurueck auf Wallclock-Fenster** — das ist der Bug, den wir
  gerade beseitigt haben. Der Token ist die einzige zuverlaessige
  Discrimination zwischen VHS-Reload und echtem User-Open.

### ✅ Buffer-Counter steht 4–5 s, dann springt er hoch (HLS-Segment-Time + Keyframe-Intervall)
- **Symptom:** Beim Klick auf „Abspielen" zeigt der Buffer-Counter im
  Player ~4–5 s lang Null, dann springt er auf 4 oder 5. Manchmal
  kurzer Hänger direkt nach diesen Sekunden.
- **Ursache (zwei Schichten):**
  1. Wir hatten `-hls_time 4` → ffmpeg liefert Segmente mit ~4 s Dauer.
     Bis das erste fertig ist, gibt's nichts zum Abspielen.
  2. Selbst nach dem Senken auf `-hls_time 2` blieben die Segmente
     4–5 s lang. Grund: `-hls_time` schneidet **nur an Keyframes**.
     Wenn die Quelle Keyframes alle 4–5 s hat (typisch fuer Releases),
     ist das Segment-Minimum eben dieser Abstand. Ohne erzwungene
     Keyframes greift `-hls_time` nicht wirklich.
- **Fix:**
  1. `-hls_time 2`.
  2. **Plus** `-force_key_frames "expr:gte(t,n_forced*2)"` —
     erzwingt beim Re-Encode einen Keyframe alle 2 s. Frame-Rate-
     unabhaengig, funktioniert auf VAAPI / NVENC / libx264. Damit
     greift `-hls_time 2` tatsaechlich.
- **NICHT entfernen** ohne den Counter-Verzoegerungs-Bug zu kennen —
  beides zusammen ist das Programm.

### ✅ Transcode-Playback hängt nach genau 4 Sekunden (fresh=1 vs. VHS-Reloads)
- **Symptom:** Beim Start eines Videos im Transcode-Modus läuft das Bild ~4 s
  und stoppt dann. Skip-nach-vorn macht es wieder lauffähig. Sehr
  reproduzierbar, betrifft fast jeden Initial-Play.
- **Ursache:** Frontend hängt an die Initial-Playlist-URL `&fresh=1` an,
  damit der Server eine evtl. stehengebliebene ffmpeg-Session beim
  „Von Anfang"-Pfad zwangsstop'd. **Aber:** Video.js/VHS lädt eine HLS-
  EVENT-Playlist periodisch neu — mit derselben URL inklusive `fresh=1`.
  Der Playlist-Handler hat damit bei jedem Reload erneut `StopSession +
  StartOrGet` ausgeführt → ffmpeg-Prozess wurde gekillt, neue Session
  startete bei Null, hatte erst seg00000 in der Playlist (4 s Material)
  → VHS spielte seg0, wollte seg1, das gab's nie weil die Session schon
  wieder weg war. Skip-Nach-Vorn (`restartTranscodeAt` in `app.js`) baut
  eine neue URL **ohne** `fresh=1` → idempotent, läuft sauber.
- **Lösung:** `Session.StartedAt` als Wallclock-Timestamp eingeführt,
  `Manager.SessionAge(...)` exposed die Lebensdauer. Im Playlist-Handler
  (`internal/api/stream.go`, `transcodePlaylist`) wird `fresh=1` nur noch
  honoriert, wenn keine Session läuft ODER die laufende ≥ 4 s alt ist
  (= ein Segment, garantiert nicht aus dem aktuellen VHS-Reload-Zyklus).
- **Was NICHT zu tun ist:**
  - **NICHT** den `fresh=1`-Mechanismus „aufräumen" oder durch ein
    One-Time-Token ersetzen, ohne den Decision-Log-Eintrag „Von Anfang
    startet mitten im Film" weiter unten zu kennen — `fresh=1` ist
    fundamental für korrektes „Von Anfang"-Verhalten.
  - **NICHT** die 4-Sekunden-Schwelle für Idempotenz tiefer setzen
    (z. B. 1 s) — VHS-Reload-Frequenz für EVENT-Playlists liegt bei
    `target_duration` (= unsere `-hls_time 4`), tiefere Werte würden
    Reloads als „neuer Play" missinterpretieren und das Symptom
    zurückbringen.
  - **NICHT** `fresh=1` clientseitig nach dem ersten Load aus der URL
    strippen — die URL ist VHS' interne Quelle, ein nachträglicher
    `vjs.src({...})` würde den Player komplett neu initialisieren und
    den ggf. aktiven Fullscreen-Modus verlieren.
  - **NICHT** das `StartedAt`-Feld aus `Session` entfernen oder
    `SessionAge` aus `Manager` löschen — beides ist genau für diese
    Idempotenz-Prüfung da.

### ✅ „Ohne TMDB-Zuordnung"-Filter zeigt Serien, in denen man nichts findet
- **Symptom:** Im TV-Library-Root mit Sort=„Ohne TMDB-Zuordnung" erscheinen
  Serien wie Blacklist oder Alias als Folder-Kacheln, obwohl sie aus User-
  Sicht vollständig gemappt sind. Beim Reinklicken sind alle Episoden mit
  Poster da, kein Hinweis auf eine unmatched Datei. User reportet „ich finde
  nichts".
- **Ursachen (zwei kompoundiert):**
  1. Der Filter sitzt im Sort-Dropdown (`unmatched` ist eine Pseudo-
     Sortierung, semantisch aber globaler Filter). `loadItems()` ruft beim
     Folder-Wechsel `restoreSortForContext()` auf, das pro-Folder gespeicherte
     Sort-Einstellungen aus localStorage lädt — und überschreibt damit die
     `unmatched`-Auswahl. Folge: Filter ist beim Eintritt weg, Grid zeigt
     ALLE Episoden inkl. der gemappten.
  2. Falls Staffel-Ansicht aktiv ist (per-Library-Default oder per-Folder-
     Override), springt `loadItems()` früh in den Seasons-API-Branch und
     ignoriert `match=unmatched` komplett. `series.go` zieht zwar unmatched
     Items in die Owned-Map, aber NUR wenn der Parser ein `SxxExx` aus dem
     rel_path bekommt (`p.IsEpisode && p.Season > 0 && p.Episode > 0`). Eine
     Datei wie `Blacklist/Behind The Scenes.mkv` oder `Blacklist/Extras/
     Trailer.mkv` hat keine Episoden-Nummerierung und taucht in der Staffel-
     Ansicht nirgends auf.
- **Lösung (`internal/webassets/web/app.js`):**
  1. Neuer Set `PSEUDO_FILTER_MODES = {unmatched, favorites, duplicates,
     suspicious, interlaced}`. `persistSortForContext` speichert diese Modi
     nicht mehr per-Folder (sind globale Filter, keine Sortierung).
     `restoreSortForContext` returnt früh, wenn aktuell ein Pseudo-Modus
     gewählt ist — der Filter bleibt beim Folder-Wechsel aktiv.
  2. Vor dem Staffel-Ansicht-Branch wird `currentMatchMode()` einmal
     berechnet (`matchMode`). Bei `matchMode === "unmatched"` wird der
     Branch übersprungen, das normale flache Items-Grid rendert mit dem
     Filter — unmatched Bonus-/Extras-Dateien sind sichtbar.
- **Generelles Pattern:** UI-States, die im selben Widget wohnen aber
  unterschiedliche Semantik haben (Sortierung vs. Filter), brauchen
  getrennte Persistenz-Strategien. „Per-Folder gespeichert" ist nur für
  echte Sortier-Vorlieben sinnvoll, nicht für Filter, die der User global
  aktiviert hat.

### ✅ „Von Anfang" startet mitten im Film (HLS-Live-Edge + Server-Session-Carryover)
- **Symptom:** User wählt „⟲ Von Anfang" im Resume-Dialog, Film startet aber
  mitten drin (manchmal Minuten voraus). Erster Versuch nach Container-Start
  klappt oft, jeder weitere springt immer weiter rein. Bei Direct Play
  gelegentlich ähnlich.
- **Ursachen (mehrere Schichten, alle aufgesetzt):**
  1. HLS-Playlist ohne `#EXT-X-PLAYLIST-TYPE` → VHS erkennt sie als Live-
     Stream und snappt beim `play()` zur Live-Edge.
  2. ffmpeg `-hls_flags append_list` schreibt `#EXT-X-DISCONTINUITY` vor das
     erste Segment → VHS sieht das als Gap am Start und lädt bei time=0 nichts.
  3. Video.js reuse path: `vjs.src({...})` reset currentTime zwar theoretisch,
     aber bei manchen Szenarien bleibt die alte Position hängen.
  4. Kein expliziter `currentTime(0)`-Call bei Direct-Play-„Von Anfang" —
     browser startet bei undefined position.
  5. **Root-Cause für die zweiten/dritten Versuche (2026-04-27):**
     `Manager.StartOrGet` cached Sessions per `(itemID, profile, audio,
     startSec, deinterlace)`. Eine Session bei `startSec=0` lebt nach
     Player-Close noch 5 Min weiter (Idle-GC) und transkodiert in der Zeit
     ungefragt weiter — Material akkumuliert. Beim zweiten „Von Anfang"-
     Klick matched StartOrGet die alte Session mit der bereits langen
     Playlist. Browser bekommt eine Playlist mit z. B. 530 s Material,
     VHS springt da rein.
- **Lösung (alle 5 Punkte zusammen, jeder einzelne reicht NICHT):**
  1. ffmpeg-Args: `-hls_playlist_type event` + `-hls_flags independent_segments`
     (append_list raus, EVENT-Type rein).
  2. Playlist-Rewriter strippt eine leading `#EXT-X-DISCONTINUITY`.
  3. Direct Play UND Transcode: `vjs.one("loadedmetadata", () => currentTime(localStart))`
     wird IMMER registriert in beiden Branches (Reuse + Neu) — `localStart=0`
     bei Transcode (Resume-Offset steckt in der URL), bei Direct Play =
     resumeForDirectPlay.
  4. Beim „Von Anfang"-Klick im Resume-Dialog: sofort `PUT /api/items/:id/resume`
     mit `{positionSec: 0}` um die alte DB-Position zu clearen.
  5. **Server-Session-Reset bei „Von Anfang":** Frontend hängt `&fresh=1`
     an die Transcode-URL wenn `resumeSec===0`. Server liest den Param,
     ruft `Manager.StopSession(...)` BEVOR `StartOrGet` — alte Session wird
     beendet, `cleanDir` löscht den Cache-Ordner, ffmpeg startet komplett
     neu mit leerer Playlist. Plus `_t=<timestamp>` Cache-Bust an der URL
     verhindert dass VHS bei identischer URL eine alte Source-Position aus
     dem Browser-Memory wiederverwendet.
- **NICHT-Funktioniert (vermeiden, schon probiert):**
  - Nur `currentTime(0)` im Frontend setzen — half nicht, weil Server immer noch
    fortgeschrittene Playlist liefert und VHS dort irgendwo landet.
  - Nur `_t=<timestamp>` Cache-Bust — half nicht, weil Server-Side bei
    `start=0` die exakt gleiche Session matched (`_t` ist nur URL-Cosmetics).
  - `currentTime(0)` aggressiv im Buffer-Gate-Polling setzen — VHS' Segment-
    Loader gerät dadurch in einen Seek-Loop, Buffer wächst nicht.
  - Den Reuse-Pfad komplett vermeiden (`disposePlayer` immer) — kostet
    Vollbild-Modus beim Shuffle-Next, der User hat das beim Test gemerkt.
  - Lösung steht und fällt mit Punkt 5: ohne Server-Side-Reset gibt es
    keinen verlässlichen Weg, die alte Session-Position auf 0 zu zwingen.

### ✅ Duplikate-Filter zeigte falsche / fehlende Filme (2026-04-28)
- **Symptom:** „Sort = Duplikate" in einer Library zeigte nicht alle Filme,
  von denen der User wusste, dass er 2 Versionen hat. Im Standard-Grid
  hatten dieselben Filme aber `×2`-Badges. Plus: nach späterem Fix wurden
  auch Episoden + Privatvideos eingemischt, wenn man in einer Movies-Lib
  war (Wildes Kanada in Bluray-Duplikaten o. ä.).
- **Ursachen (in der Entdeckungs-Reihenfolge):**
  1. **Frontend mischt eigene Filter rein.** Der Duplikate-Branch schickte
     bisher zusätzlich `watched`, `favorite` und `resolution`-Buckets an den
     Server. Bei aktivem „nur ungesehen"-Watched-Filter UND zwei gesehenen
     Versionen verschwand der Film komplett. Bei „nur 1080p"-Filter +
     einem Film mit 1080p+4K war nur eine Version sichtbar.
  2. **Inkonsistenz Server-Filter vs. Variant-Count.** Eine andere
     Session hat heute Mittag `attachVariantCounts` eingebaut, das
     library-übergreifend zählt — daher zeigte das Badge `×2` auch für
     Filme, deren zweite Version in einer anderen Library liegt (Bluray-
     Variante + Filme-Variante). Der DupesOnly-SQL-Filter prüfte aber
     nur library-spezifisch (`WHERE library_id = i.library_id` in der
     HAVING-Subquery) → Filme mit cross-library-Duplikaten wurden NICHT
     gefunden.
  3. **Library-Type-Mismatch nach erstem Fix.** Sobald der DupesOnly-
     Subquery-Library-Filter raus war, kamen Episoden-Duplikate aus
     Serien-Lib und Doku-Duplikate aus Privat-Libs in den Bluray-View
     mit rein.
- **Lösung (drei zusammenwirkende Schritte):**
  1. **Frontend (loadItems duplicates-Branch):** keine `libraryId`,
     keine `watched`, keine Resolution-Buckets mitsenden — nur
     `duplicates=yes` + Suche. Damit kommen alle Versionen aller
     Duplikate vom Server zurück, ohne dass User-Filter Versionen aus
     dem Vergleichs-View kicken.
  2. **Server (DupesOnly-Filter):** HAVING-Subquery ohne `library_id`-
     Bedingung → Duplikat = `metadata_id` taucht global ≥2× auf,
     konsistent zu `attachVariantCounts`.
  3. **Frontend (kind-aware Filterung nach Empfang):**
     `currentLib.kind` bestimmt, welche Libraries einbezogen sind:
     `kind=movies` (Bluray + Filme zusammen), `tv` (alle Serien-Libs),
     `private` (alle Privat-Libs). Nach diesem Filter wird die
     `metadata_id`-Häufigkeit nochmal client-seitig nachgezählt — sonst
     bleiben „Geister-Singletons" zurück, deren Geschwister durch den
     Kind-Filter rausgefallen ist. Breadcrumb: „⧉ Duplikate (alle
     Filme/Serien/Privatvideos)".
- **NICHT-Funktioniert (vermeiden, schon probiert):**
  - Nur Frontend-Filter weglassen → `watched`/`resolution`-Probleme
    behoben, aber library-übergreifende Duplikate fehlen weiter.
  - Nur Server-SQL global machen → cross-library erkannt, aber Episoden
    + Privatvideos kontaminieren den Movies-Duplikate-View.
  - Server-side neuen `library_kind`-Filter einführen → mehr Schema/API-
    Fläche; client-seitige Kind-Filterung ist einfacher und reicht.

### ✅ Buffer-Overlay ignoriert Docked-Position
- **Symptom:** Nach Einführung der Docked-Darstellung (Streifen unter Bild im
  eingebetteten Modus) saß das Overlay weiterhin oben rechts ÜBER dem Video,
  als wäre `--docked` nie gesetzt worden. DevTools zeigte das Element im
  richtigen DOM-Parent (`.player-wrap`), aber ohne die Klasse.
- **Ursache:** `startBufferDisplay.setClass` machte `el.className = "transcode-ahead"`
  bei jedem Poll-Tick (mehrmals pro Sekunde). Das löschte `transcode-ahead--docked`
  sofort wieder, direkt nach dem Setzen durch `positionBufferOverlay`.
- **Lösung:** `setClass` toggelt nur noch die Status-Marker (`behind`/`low`)
  via `classList.add/remove`. Generell: wenn mehrere Codepfade Klassen auf
  demselben Element pflegen, `className =` niemals benutzen.

### ✅ Billions Staffel 2 komplett um eins verschoben
- **Symptom:** In einer TV-Show (konkret Billions S2) waren alle Dateien um
  eine Episode verschoben gematcht — File E7 zeigte auf TMDB-E6, File E6 auf
  E5, usw. „↻ TMDB neu laden" half nicht (Problem lag in den persistierten
  `items.metadata_id`, nicht im TMDB-Cache). Manuelles Umzuordnen einzelner
  Files erzeugte Kaskaden-Probleme, weil dann die Nachbarposition frei wurde.
- **Ursache:** Systematischer Enricher-Fehler für diese Show — vermutlich
  Parser-Missinterpretation eines Release-Tokens in den Dateinamen, der
  einen anderen Episoden-Code suggerierte.
- **Lösung:** Neuer Admin-Endpoint + Button „⚠ Episoden neu zuordnen" im
  Show-Header. Setzt für ALLE Episoden-Items im Ordner (auch bestätigte!)
  `metadata_id=NULL`, `metadata_confirmed=0`, `episode_end=0` und triggert
  `EnrichFolderNow`. Show-Folder-Zuordnung bleibt unangetastet. Nach 4 s lädt
  die Ansicht mit `?refresh=true` neu. Für Billions sofort gelöst.

### ✅ DOM-Builder async gemacht → komplett schwarze Seite
- **Symptom:** Nach einem Bulk-Replace von `alert/confirm/prompt` auf die
  neuen App-Dialoge rendert das Grid nichts mehr. Die Kacheln erscheinen
  nicht, obwohl die Items geladen sind.
- **Ursache:** Mein Sed-Skript hat alle `function foo()`, deren Body
  textuell `await` enthält, auf `async function` umgestellt. Das hat auch
  `renderCard`, `renderFolderCard`, `resLabel`, `hidePartButton` erwischt —
  deren inner event-handler haben await, aber die äußere Funktion selbst
  nicht. Async-gemachte Builder geben `Promise<Element>` statt `Element`
  zurück, `frag.appendChild(promise)` produziert einen leeren Node.
- **Lösung:** Die vier Builder explizit zurück auf `function` setzen. Für
  zukünftige Refactors: Body-Scope korrekt parsen (Klammer-Matching), nicht
  textuelle Suche. DOM-Builder DÜRFEN NICHT async werden, egal was intern
  passiert.

### ✅ NVENC-Trickplay crasht mit „Failed to inject frame into filter network"
- **Symptom:** Mit `-hwaccel cuda -hwaccel_output_format cuda` und der
  normalen Trickplay-Filter-Chain bricht ffmpeg mit
  „Impossible to convert between the formats supported by the filter
  'Parsed_fps_0' and the filter 'auto_scale_0' Error reinitializing
  filters! Failed to inject frame into filter network: Function not
  implemented".
- **Ursache:** `-hwaccel_output_format cuda` hält die Frames nach dem Decode
  auf der GPU. Unsere Software-Filter (fps/scale/pad/tile) erwarten
  CPU-Frames und können das cuda-Frame-Format nicht lesen.
- **Lösung:** `-hwaccel_output_format cuda` weglassen → Frames werden
  automatisch ins CPU-RAM kopiert (was bei fps=1/10 vernachlässigbar ist,
  weil nur wenige Frames anfallen). Die Software-Filter-Chain funktioniert
  unverändert. Gleiches Pattern beim Transcode: `-hwaccel cuda -i … -vf
  <scale> -c:v h264_nvenc` — ffmpeg lädt automatisch zum Encoder hoch.

### ✅ Container crasht mit „driver not loaded" beim ersten NVIDIA-Deploy
- **Symptom:** Nach Aktivierung von `runtime: nvidia` in der Compose schlägt
  Stack-Update mit `nvidia-container-cli: initialization error: nvml error:
  driver not loaded` fehl. Container startet gar nicht.
- **Ursache:** NVIDIA-Plugin auf dem Unraid-Host war installiert, aber der
  Kernel-Treiber noch nicht geladen (z. B. weil das Plugin nach einem
  Kernel-Update noch auf Install-State stand).
- **Lösung:** Auf Unraid-WebUI den Treiber-Status prüfen, ggf. neu laden
  (oder Host neustarten). `nvidia-smi` auf dem Host muss funktionieren,
  bevor `runtime: nvidia` im Compose gesetzt werden kann. Für Default-
  Template: `runtime: nvidia`-Zeile als Kommentar drin lassen, damit Nutzer
  ohne NVIDIA das Template ohne Änderungen starten können.

### ✅ Episoden ohne TMDB-Still-Image zeigen Release-Filename als Titel
- **Symptom:** NCIS Sydney S03E16 „Folge 16" (TMDB-Platzhalter ohne
  still_path) rendert im Grid mit `NCIS.Sydney.S03E16.GERMAN.DL.720p.WEB.
  h264-SAUERKRAUT` als Titel, statt „NCIS Sydney" + S03E16 wie die
  anderen Folgen.
- **Ursache:** In `renderCard` war das Episode-Handling gekoppelt an
  `if (it.metadata && it.metadata.posterPath)`. Wenn TMDB keinen
  still_path liefert, fällt der Code in den else-Zweig (thumb/placeholder)
  und überspringt das gesamte Episode-Styling.
- **Lösung:** Die Episode-Logik (showName aus rel_path[0] als Titel,
  SxxExx + Episodentitel als Meta) läuft jetzt unabhängig vom
  posterPath-Check. Poster/Thumb-URL wird separat bestimmt.

### ✅ Manueller Show-Re-Match zeigt alle Folgen als „Fehlt"
- **Symptom:** User ordnet einen TV-Folder via „Manuell zuordnen" einer
  anderen Show zu. `folder_metadata` wird korrekt aktualisiert, aber in
  der Staffel-Ansicht erscheinen alle Folgen als „Fehlt".
- **Ursache:** Die einzelnen Episoden-Items behalten ihre alten
  `metadata_id`-Verweise auf Episoden der **alten** Show. Die
  Staffel-API vergleicht parent_id mit der neuen showTMDBID → keine Treffer.
  Der Enricher würde sie neu matchen, skippt aber `metadata_id > 0`.
- **Lösung:** `UnmatchEpisodesInFolder(libID, folder, newShowTMDB)` setzt
  alle Items mit Episoden-Metadata, deren Parent-Show eine andere
  TMDB-ID hat, auf `metadata_id=NULL`. Respektiert
  `metadata_confirmed=1` (bestätigte Items bleiben). Wird automatisch
  aus `setFolderMetadata` aufgerufen, danach `EnrichFolderNow`.

### ✅ VAAPI schlägt zur Laufzeit fehl trotz whitelisted Codec
- **Symptom:** Trickplay-Jobs für bestimmte h264-Files brechen mit
  `Failed setup for format vaapi: hwaccel initialisation returned error.`
  oder `Function not implemented` ab. Betrifft zufällig einzelne Dateien,
  obwohl Codec=h264 ist und `vaapiSupportsCodec()` true liefert.
- **Lösung:** Im `ffmpeg`-Runner nach VAAPI-Fehler automatisch ohne
  `-hwaccel`-Header erneut ausführen. Trigger-Substrings: „hwaccel
  initialisation", „Function not implemented", „No support for codec".
  Implementiert im Trickplay-Worker; gleiches Pattern sollte auf andere
  ffmpeg-Runner übertragen werden, falls sie auf HW setzen.

### ✅ `-vf` vor `-i` bricht ffmpeg sofort ab
- **Symptom:** Alle Trickplays scheitern mit „Option vf (set video filters)
  cannot be applied to input url … Move this option before the file it
  belongs to".
- **Ursache:** Beim Refactor wurde `-vf` vor `-i` gestellt. ffmpeg wertet
  Optionen relativ zu den Input-/Output-URLs — `-vf` muss IMMER **nach**
  `-i <input>` stehen, sonst ist es ein Input-Filter, der nicht implementiert
  ist.
- **Lösung:** Strikte Reihenfolge: `[-hwaccel …] -i <input> -vf <filter>
  [output-opts] <output>`.

### ✅ Cinemascope-Filme (21:9) landen im 720p-Bucket
- **Symptom:** 1920×800-Filme wie Robin Hood 2010 werden als 720p angezeigt
  und vom 1080p-Filter ausgeschlossen.
- **Ursache:** `resLabel` und Bucket-SQL haben nur `height` betrachtet. Bei
  Cinemascope ist die Pixel-Höhe ~800, obwohl die horizontale Auflösung volles
  1080p-Niveau hat.
- **Lösung:** Effektive Höhe = `MAX(height, width * 9 / 16)`. Greift
  sowohl im Client (`resLabel`) als auch in der Server-Bucket-Filter-SQL.

### ✅ Buffer-Overlay verschwindet nach 2 s Inaktivität
- **Symptom:** Anzeige „Buffer +N s · Auflösung" nur kurz sichtbar, kommt
  nicht zuverlässig beim Mouse-Move wieder.
- **Ursache:** Overlay war an Video.js-Events `useractive`/`userinactive`
  gekoppelt — bei 2 s Idle entfernt Video.js die `is-active`-Klasse und CSS
  blendet auf Opacity 0. Bei wiederkehrendem Hover kam die Klasse nur kurz
  zurück.
- **Lösung:** `is-active` beim Player-Start permanent setzen, kein Binding
  an die Aktivitäts-Events mehr. Außerdem `stopTranscodeProgress` stoppt
  nur den Poll-Timer; die Klasse `hidden` wird ausschließlich in
  `hideBufferOverlay` (Player-Close) gesetzt.

### ✅ Sammlungen zeigen Pseudo-Collections mit nur 1 Film
- **Symptom:** 385 Sammlungen in der UI, viele davon mit movieCount=1 —
  TMDBs `belongs_to_collection` markiert jeden Franchise-Fortsetzungs-Film,
  auch ohne weitere Teile in der Lib. User möchte aber auch 1-Film-Sammlungen
  sehen (vielleicht kommt ja noch was dazu).
- **Lösung Iteration 1 (abgelehnt):** `HAVING COUNT(m.id) >= 2`.
- **Lösung Iteration 2 (final):** `EXISTS (≥1 Film)` — 1-Film-Sammlungen
  bleiben drin. **Aber** `movieCount = COUNT(DISTINCT m.id)` statt
  `COUNT(DISTINCT i.id)` — so zählt Merge-Duplikate nicht als 2 Filme.

### ✅ Missing Parts nicht klickbar
- **Symptom:** „Fehlt"-Kacheln in Sammlungen waren tote Placeholder — keine
  Plot/Cast-Info, obwohl TMDB diese Daten hätte.
- **Lösung:** Neuer Endpoint `GET /api/tmdb/movie/{tmdbId}` proxy't Movie +
  Credits. Frontend-Dialog `#missingMovieDialog` rendert Poster/Plot/Rating/
  Cast/IMDb-Link; Klassen `modal detail-modal` halten das Layout identisch
  mit dem echten Detail-Dialog.

### ✅ Intel Quick Sync (`h264_qsv`) schlägt fehl
- **Symptom:** `Error initializing an internal MFX session: unsupported (-3)` trotz
  vorhandenem iHD-Driver.
- **Ursache:** libmfx-Runtime und Intel Media Driver nicht kompatibel in diesem Container.
- **Lösung:** Stattdessen **VAAPI** (`h264_vaapi` mit `-vaapi_device /dev/dri/renderD128`
  und `-vf format=nv12,hwupload`) verwenden — gleiche iGPU-Hardware, robusterer Pfad.
- **UI-Hinweis:** Wird trotzdem als „Intel Quick Sync (VAAPI)" angezeigt, weil
  Marketing-Name = Hardware-Feature.

### ✅ Port 8096 blockiert durch Jellyfin im host-network-Mode
- **Symptom:** Deploy schlägt fehl mit „bind: address already in use" obwohl `docker ps`
  keinen 8096-Eintrag zeigt.
- **Ursache:** Jellyfin läuft im `host`-Netzwerkmodus → belegt Host-Ports ohne in der
  Docker-API-Ports-Liste zu erscheinen.
- **Lösung:** Videoplayer auf Port **8098** (Mapping `8098:8096`).
- **Mitigation:** Vor Deploys Container mit `HostConfig.NetworkMode == "host"`
  zusätzlich prüfen.

### ✅ TopLevelFolders liefert nur 1 falschen Ordner
- **Symptom:** Youtube-Bibliothek zeigte 1 Ordner „Wilma Hofleben" mit 257 Videos statt
  26 Kanal-Ordner.
- **Ursache:** `SELECT SUBSTR(...) AS folder ... GROUP BY folder` mit LEFT JOIN auf
  `folder_metadata.folder` — SQLite resolved den Alias zweideutig, gruppierte nach dem
  NULL-fm.folder statt dem Items-Alias.
- **Lösung:** Aggregation in Subquery, JOIN erst danach:
  ```sql
  SELECT f.folder, f.cnt, ... FROM (
    SELECT SUBSTR(rel_path, ...) AS folder, COUNT(*) AS cnt, ...
    FROM items WHERE ... GROUP BY folder
  ) f
  LEFT JOIN folder_metadata fm ON fm.library_id = f.library_id AND fm.folder = f.folder
  ```

### ✅ Migration scheitert bei Bestands-DB
- **Symptom:** Container-Restart-Loop mit `no such column: released_at` — Index auf
  noch-nicht-existierender Spalte.
- **Ursache:** `CREATE INDEX ON items(released_at)` wurde VOR `ALTER TABLE items
  ADD COLUMN released_at` ausgeführt.
- **Lösung:** Baseline-Statements (CREATE TABLE), dann idempotente `ALTER TABLE ADD
  COLUMN` (duplicate-column-Fehler wird ignoriert), dann erst die Indizes.

### ✅ YouTube-Upload-Datum wurde nicht erkannt
- **Symptom:** `releasedAt` war identisch mit File-mtime (Download-Datum).
- **Ursache:** yt-dlp schreibt das Upload-Datum als MKV-Tag **`DATE`** im Format
  `YYYYMMDD` (keine Bindestriche). Mein Parser suchte nur nach `creation_time` und
  bekannten RFC-Formaten.
- **Lösung:** Case-insensitive Tag-Lookup inkl. `DATE`, zusätzliches Layout `"20060102"`.

### ✅ Go-Embed-Pfad war falsch
- **Symptom:** `//go:embed all:web: no matching files found` beim Docker-Build.
- **Ursache:** `//go:embed all:web` in `cmd/videoplayer/main.go` suchte `cmd/videoplayer/web/`.
- **Lösung:** Eigenes Package `internal/webassets/` mit `web/`-Unterordner und Embed-Direktive.

### ✅ macOS-xattrs blockieren Docker-Build
- **Symptom:** Build bricht mit `lsetxattr com.apple.provenance ...: operation not supported`.
- **Lösung:** Tar mit `COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata …`.

### ✅ Manuelles Show-Matching triggert keine Episoden
- **Symptom:** Nach manuellem TMDB-Match einer Serie blieben die Episoden lange
  ungematcht.
- **Ursache:** `Trigger()` stieß den allgemeinen Worker-Loop an, der viele andere
  Items zuerst abarbeitete.
- **Lösung:** `Worker.EnrichFolderNow(libraryID, folder)` — dedizierte Goroutine nur
  für diesen Ordner, umgeht die 5-Minuten-Ticker-Latenz.

### ✅ chi registriert HEAD nicht automatisch für GET-Routen
- **Symptom:** Trickplay-Hover erschien nie, obwohl `sprite.jpg` + `thumbs.vtt`
  auf Disk vorhanden waren.
- **Ursache:** Client-seitiger `fetch(url, { method: "HEAD" })` gegen
  `/api/trickplay/{id}/thumbs.vtt` bekam 405 zurück (chi v5 matcht nur die
  registrierte Methode). `checkRes.ok` wurde false → Plugin-Init übersprungen.
- **Lösung:** HEAD-Check raus, stattdessen `item.trickplayStatus === "done"`
  aus der DB als Gate nutzen.

### ✅ HLS-Segment-URIs verlieren Query-Parameter
- **Symptom:** Beim Seek-Restart im Transcode startete das Video ab 0, obwohl
  die neue Playlist `start=<X>` an den Server übergab.
- **Ursache:** Die von ffmpeg geschriebene `index.m3u8` enthält relative
  Segment-Dateinamen (`seg00000.ts`). Der Browser löst diese gegen die
  Playlist-URL auf und lässt dabei die Query-Parameter weg → die
  Segment-Handler-Requests hatten kein `?start=X` → fielen auf die Default-Session
  mit startSec=0 zurück → Video spielte von vorne.
- **Lösung:** `transcodePlaylist`-Handler liest die Playlist ein, hängt an jede
  nicht-Kommentar-Zeile (Segment-URIs) die Query-Parameter an, und liefert
  die umgeschriebene Version aus.

### ✅ Go RE2 unterstützt keine Lookaheads
- **Symptom:** Container im Crash-Loop nach Deploy. Panic:
  `invalid or unsupported Perl syntax: '(?='`.
- **Ursache:** Ich hatte ein Regex mit Lookahead `(?=[a-zA-Z])` in
  `variants.go` verwendet — Go's `regexp`/RE2 unterstützt das nicht.
- **Lösung:** Capture-Group statt Lookahead:
  `^[a-z0-9]{2,7}-([a-zA-Z])` — der Rest beginnt an der Capture-Position.

### ✅ Endlos-Backfill bei Cast-Einträgen ohne TMDB-Credits
- **Symptom:** Filme bekamen nie Cast-Einträge, obwohl `backfillCast` lief.
- **Ursache:** `MetadataIDsMissingCast` lieferte Metadata ohne
  `metadata_cast`-Einträgen. Filme, bei denen TMDB leere Credits zurückgab,
  blieben daher für immer auf der Backfill-Liste — jeder Run machte den
  gleichen leeren Call.
- **Lösung:** Neue Spalte `metadata.cast_fetched_at`. Nach jedem
  `fetchMovieCast`/`fetchShowCast`/`fetchEpisodeGuests` wird sie gesetzt.
  Der Backfill-Filter schaut darauf, nicht auf tatsächliche Cast-Zeilen.

### ✅ Video.js Live-UI versteckt Progress-Bar bei progressiven HLS
- **Symptom:** Beim Transcode verschwand die Progress-Bar sobald Wiedergabe
  startete.
- **Ursache:** Unsere wachsende Playlist ohne `#EXT-X-ENDLIST` wird von
  Video.js als Live-Stream erkannt — `.vjs-live` wird gesetzt, und die
  Default-CSS blendet Progress-Bar + Zeit-Controls aus.
- **Lösung:** `liveui: false` in den Player-Optionen, zusätzlich CSS-Override
  (`.vjs-progress-control { display: flex !important; }` etc.).
  `forcePlayerDuration(vjs, total)` setzt zusätzlich eine stabile Dauer in
  den `duration`-Cache, damit SeekBar und Zeit-Anzeigen sinnvoll arbeiten.

### ✅ Jahr-als-Titel-Bug im Parser (1917, 1992)
- **Symptom:** Filme mit Jahr als Titel (z. B. `1917.2019.German…`) blieben
  unmatched; der Parser extrahierte „1917" als Release-Jahr und der Titel
  wurde leer.
- **Lösung:** Wenn das erste gefundene Jahr direkt am Anfang steht und ein
  zweites Jahr existiert, wird das zweite als Release-Jahr genutzt und das
  erste bleibt Titel-Teil.

### ✅ Parser mismatch bei Release-Dateinamen wie `tvs-911-…-108.mkv`
- **Symptom:** 28 verschiedene Dateien in 9-1-1 landeten alle auf derselben
  Episode-Metadata (S9E11) → Grid zeigte eine Kachel mit `×28` statt
  individuelle Episoden.
- **Ursache:** Der Parser las aus der kryptischen Datei die „911" als 3-
  stelligen Episoden-Code und interpretierte sie als S9E11. Jede Datei hatte
  diese Zahl → alle wurden auf dieselbe Episode gematcht.
- **Lösung:** `matchItem` prüft TV-Kontext nun dreistufig —
  (1) strikter SxxExx/NxN im Dateinamen, (2) strikter SxxExx in Eltern-
  Ordnern (die haben fast immer den korrekten Code), (3) erst zuletzt der
  aggressive Parser mit numerischen Codes.
- Zusätzlich Cleanup-Endpoint `POST /api/enrich/unmatch-duplicates?threshold=3`
  → setzt `metadata_id=NULL` für TV-Items, deren `metadata_id` mehr als N mal
  vorkommt; Re-Enrich läuft dann mit der neuen Parser-Logik.

### ✅ SxxExx ohne Wort-Grenze davor (The MiddleS1E01.avi)
- **Symptom:** Dateinamen ohne Trenner zwischen Titel und Episode-Code blieben
  unmatched.
- **Lösung:** `reSxxExx` ohne Anfangs-`\b` — `(?i)S(\d{1,2})\s*E(\d{1,3})…`.
  `S+Digits+E+Digits` ist spezifisch genug, falsche Treffer in normalen
  Wörtern unwahrscheinlich.

### ✅ Fullscreen geht beim Shuffle-Next verloren
- **Symptom:** Shuffle-Weiterschalten (⏭) im Vollbildmodus bricht Fullscreen.
- **Ursache:** `applyPlayback` hat die Video.js-Instanz disposed und neu erzeugt →
  Fullscreen ist an das alte `<video>`-Element gebunden, das weg ist.
- **Lösung:** Wenn `state.vjs` existiert und nicht disposed ist, nur `vjs.src({...})` +
  `vjs.play()` aufrufen; alte Remote-Text-Tracks vorher entfernen. Nur beim erstmaligen
  Öffnen wird `new videojs()` aufgerufen (Events `timeupdate`/`ended` dort gebunden).

### ✅ Obfuskierte Release-Namen werden nicht erkannt
- **Symptom:** Filme wie `Sitrb.Langsam.1988.1080p.GERMAN.DL-group.mkv` blieben unmatched,
  auch `empire-weho`-Varianten scheiterten.
- **Ursachen:** (a) Datei-Parser lieferte nur Release-Group-Gibberish; (b) kein Fallback
  auf Ordner-Namen; (c) keine Deleet-/Typo-Variante.
- **Lösung:** `enrich.Worker` baut Kandidatenliste aus Datei + allen rel_path-Segmenten
  rückwärts; pro Kandidat zusätzlich `ExpandCandidates` mit Deleet + Longest-Token-
  Fallback. Dedup per Lowercase-Key.

### ✅ Sample-Ordner verschmutzen Library und Enrichment
- **Symptom:** Redundante „Sample"-Kacheln, zusätzliche Enrichment-Queue-Einträge.
- **Lösung:** Scanner skippt Ordner mit Namen `Sample`/`Samples` per
  `filepath.SkipDir` (case-insensitive).

### ✅ Numerische Episoden-Codes (104 = S1E04)
- **Symptom:** Dateien wie `Derrick 104.avi` wurden nicht als Episoden erkannt.
- **Ursache:** Parser kannte nur SxxExx und NxN.
- **Lösung:** `ParseEpisodeFile` für TV-Kontext mit zusätzlicher Regex für 3–4-stellige
  Zahlen; Jahres-Ausschluss (1900–2099) und Plausibilitäts-Check (S 1–29, E 1–99).
  Bei Filmen **nicht** aktiv (`Matrix 1999 1080.mkv` soll nicht S10E80 sein).

## API-Referenz (Kurzform)

```
# Bibliotheken
GET    /api/libraries
POST   /api/libraries                     {name, path, kind}
DELETE /api/libraries/{id}
PUT    /api/libraries/{id}/kind           {kind}
GET    /api/libraries/{id}/folders
GET    /api/libraries/{id}/paths
POST   /api/libraries/{id}/paths          {path}
DELETE /api/libraries/{id}/paths?path=…

# Items
GET    /api/items?libraryId=&folder=&search=&sort=&dateFrom=&dateTo=&watched=&favorite=&match=&duplicates=&bucket=…&personId=
GET    /api/items/years
GET    /api/items/random?libraryId=&folder=&search=&watched=
GET    /api/items/{id}
GET    /api/items/{id}/download           (Original-Datei als attachment)
GET    /api/items/search-path?q=          (Admin) Diagnose: rel_path/path/title-Suche
GET    /api/items/suspicious?libraryId=   „geratene" Zuordnungen: 0 Token-Overlap + Jahres-Mismatch
DELETE /api/items/{id}?deleteFile=…       (Admin)
PUT    /api/items/{id}/watched            {watched: bool}
PUT    /api/items/{id}/favorite           {favorite: bool}
PUT    /api/items/{id}/confirm            {confirmed: bool} — auto-NFO bei confirmed=true
POST   /api/items/{id}/write-nfo          (Admin) manueller NFO-Refresh
POST   /api/items/write-all-nfos          (Admin) alle bestätigten Items NFO-schreiben
GET    /api/items/{id}/playlists          (IDs der Playlists, in denen Item liegt)
POST   /api/items/merge                   (Admin) {ids:[…]} — Items unter erster metadata_id zusammenführen

# Playback
GET    /api/playback/{id}?mode=auto|direct|transcode&profile=orig|1080p|720p|480p|360p
GET    /api/stream/{id}                    (Direct Play mit Range)
GET    /api/transcode/{id}/index.m3u8?profile=…
GET    /api/transcode/{id}/{seg}?profile=…

# TMDB
GET    /api/metadata/search?q=&type=movie|tv&year=
POST   /api/items/{id}/metadata            {tmdbType, tmdbId, season, episode}
POST   /api/libraries/{id}/folders/metadata {folder, tmdbId}
POST   /api/libraries/{id}/auto-merge-duplicates  (Admin) Ordner mit eindeutiger Zuordnung → Geschwister angleichen
POST   /api/libraries/{id}/folders/re-enrich-episodes  (Admin) {folder} — alle Episoden im Ordner unmatchen (inkl. confirmed) + neu enrichen
GET    /api/poster/metadata/{id}
GET    /api/tmdb/movie/{tmdbId}            Detail+Cast eines Films, der NICHT in der Lib ist (Missing-Part-Dialog)
POST   /api/enrich/run
GET    /api/enrich/status

# Sammlungen
GET    /api/collections                    Liste aller Collections mit ≥1 Film, movieCount = DISTINCT metadata
GET    /api/collections/{id}/items         Parts inkl. owned/missing + per-User hidden-Flag
POST   /api/collections/{id}/parts/{tmdbMovieId}/hide      Part für aktuellen User ausblenden
DELETE /api/collections/{id}/parts/{tmdbMovieId}/hide      Part wieder einblenden
GET    /api/poster/collection/{id}

# Scan
POST   /api/scan/{libraryId}
GET    /api/scan/status
POST   /api/scan/cancel

# Playlists (per User)
GET    /api/playlists
POST   /api/playlists                      {name}
DELETE /api/playlists/{id}
POST   /api/playlists/{id}/items           {itemId}
DELETE /api/playlists/{id}/items/{itemId}

# Trickplay
GET    /api/trickplay/{id}/thumbs.vtt
GET    /api/trickplay/{id}/sprite-{n}.jpg
GET    /api/trickplay/status
POST   /api/trickplay/run

# Auth & Users
GET    /api/auth/status
POST   /api/auth/login                     {username, password}
POST   /api/auth/logout
POST   /api/auth/setup                     {username, password}   (nur wenn users leer)
GET    /api/users                          (Admin)
POST   /api/users                          {username, password, isAdmin}
PUT    /api/users/{id}/password            {password}
PUT    /api/users/{id}/admin               {isAdmin}
DELETE /api/users/{id}
GET    /api/users/{id}/acl                 (Admin)
PUT    /api/users/{id}/acl                 {libraryIds: [..]}     (Admin)

# Folder-Navigation (Drilldown-Toggle)
GET    /api/libraries/{id}/folders/nav     (Map folder → enabled)
PUT    /api/libraries/{id}/folders/nav     {folder, enabled}

# Settings
GET    /api/settings
PUT    /api/settings                       {bufferSeconds?, tmdbKey?, omdbKey?, trickplayIntervalSec?}
GET    /api/browse?path=/media/…

# Home & Serien-Strukturen
GET    /api/home                           Startseite-Daten: pro Library {continue, nextUp, recent}
GET    /api/libraries/{id}/seasons?folder=[&refresh=true]  Staffel-Übersicht inkl. Show-Infos, Cast, missing-Episoden (refresh=true invalidiert TMDB-Cache)
PUT    /api/libraries/{id}/home-visibility {onHome: bool}

# Health
GET    /api/health                         (hwaccel, tmdb.enabled)
```

## Refactor-Abschluss 2026-04-30 (Frontend-Modul-Split, fertig)

**Phase 1 — Linter-Findings (live):** kleine Bugs gefixt — poster-edit
ineffassign, scanner nilerr-Annotation, mp4probe int64-Overflow-Schutz,
3× ST1005-Errors klein, sqlite Close-Errcheck — plus echter Parser-Bug
(„Mad MAX" wurde zu „Mad Fury Road" weil `max` in reTrash stand;
`max`/`nf`/`dv` raus).

**Phase 2 — Tests (live):** erste Test-Suite des Projekts.
- `internal/nameparser/parser_test.go` — 88,8 % Coverage, 60+ Cases inkl.
  Decision-Log-Edge-Cases (Year-as-Title, numerische Episoden,
  Doppelfolgen, Sample-Skip, etc.).
- `internal/playback/decider_test.go` — Decider 100 % Coverage.

**Phase 3 — Frontend-Modul-Split (live, abgeschlossen):**
- 13 Module aus app.js extrahiert: helpers, dialogs, api, cast,
  player-components, cards, views, grid, player, admin, playlists,
  scan, matching. Siehe „Frontend-Modul-Layout" oben.
- app.js: **7531 → 1371 Zeilen (−82 %)**.
- Jeder Modul-Schritt: eigener Branch, einzeln gemerged + im Browser
  live getestet.

**Tools eingerichtet (bleiben):**
- `golangci-lint` und `biome` (homebrew) — vor groesseren Refactors laufen lassen.
- `scripts/check-frontend.sh` — `node --check` ueber alle web/*.js. Wird in
  pre-commit-Hook (`scripts/install-git-hooks.sh`) und in CI
  (`.github/workflows/deploy.yml`) ausgefuehrt. Hat bereits 2 Bugs gefangen
  („deutsche Anfuehrungszeichen mit ASCII-`"` mittendrin"). **Niemals
  ueberspringen bei JS-Aenderungen.**

**Pattern fuer Frontend-Modul-Aenderungen** (nicht mehr fuer geplante
Splits, aber falls man weitere Aufteilung braucht):
1. `git checkout -b code-review/<name>-<date>`
2. Block-Boundaries via `grep -n "^// --- "` finden
3. Datei via `awk` extrahieren + Header-Kommentar dazu
4. app.js trimmen mit `awk` (Multi-Block-Trims: ALLE in_block=0-Resets
   vor der generischen Skip-Aktion!) + Breadcrumb-Kommentar
5. `<script src="/<name>.js" defer>` in `index.html` an der richtigen
   Position der Lade-Reihenfolge
6. `./scripts/check-frontend.sh && go build ./... && go test ./...`
7. Commit mit `refactor(frontend): …` Prefix, Push branch
8. „merge" beim User abfragen → ff-only auf main → push → Auto-Deploy

**Backend-Roadmap (eigener Track, nicht teil des Refactors):**
- Native iOS/iPadOS/macOS-App fuer Offline-Wiedergabe (siehe
  project_roadmap_offline-Memory) — separat, eigene Session.

## TODO

- [ ] **Nach GitHub hochladen als Open-Source-Projekt.** Vorbereitung ist
  abgeschlossen: MIT-Lizenz, NOTICE.md mit allen Third-Party-Attributionen,
  README.md auf Englisch, CLAUDE.md via `.gitignore` ausgeschlossen,
  Go-Modulpfad anonymisiert (`github.com/goldfish-media/goldfish`),
  TMDB-Attribution im Settings-Dialog. Offene Schritte:
  1. `git init` im Projekt-Root + erstes Commit
  2. Repository auf GitHub erstellen (z. B. `github.com/<user>/goldfish`)
  3. Bei abweichendem User: Modulpfad anpassen
     (`go mod edit -module github.com/<user>/goldfish` + sed über alle `.go`)
  4. `git remote add origin` + `git push -u origin main`
  5. Topics setzen: `media-server`, `jellyfin-alternative`, `vaapi`, `go`

## Entwicklungsworkflow

- **Keine lokale Go-Toolchain erforderlich** — Docker-Build via Portainer-API übernimmt
  das komplett.
- **Datenbank-Migrationen** sind additiv und idempotent. Neue Spalten über
  `ALTER TABLE ADD COLUMN` im `addCol`-Helper, neue Indizes **nach** den ALTERs.
- **Vor Stack-Redeploy** keine destruktiven Aktionen nötig — Named Volume
  `videoplayer_config` bleibt erhalten, Schema wird hochmigriert.
- **TMDB-Key** wird in DB (`settings.tmdb_api_key`) gespeichert, nicht in Env-Vars.
  Änderbar über UI. Das Health-Endpoint zeigt `tmdb.enabled: true/false`.

## Bewusst NICHT implementiert

- Staffel-Ebene in der UI (alle Episoden eines Serien-Ordners sind flach)
- Untertitel-Burn-In (Subs werden als Track geliefert falls vorhanden)
- Adaptive Bitrate (Client wählt ein fixes Profil; Master-Playlist mit mehreren
  Renditions wäre ein nächster Schritt)
- Resume/Continue-Watching mit echten Position-Timestamps
- Live-TV / DVR
