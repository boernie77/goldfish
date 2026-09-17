# Goldfish — Jellyfin-light für Unraid

Produktname: **Goldfish**. Das Go-Modul, Docker-Image, Volumes und Stack-Name bleiben aus
Kompatibilitätsgründen `videoplayer` / `simple-videoplayer` — nur das UI-Branding ist
„Goldfish".

**Server-Version:** seit 2026-08-30 semantisch versioniert, Start 1.0.0.
Konstante `appVersion` in `internal/api/router.go`. **Bei JEDEM Deploy die
Patch-Stelle um 1 erhöhen** (User-Vorgabe 2026-08-31) — also 1.0.1 → 1.0.2 →
… im selben Commit, der rausgeht. Ausgeliefert als `version` im `/api/health`,
angezeigt im Zahnrad-Menü-Fuß (`#drawerVersion`). (Die App-Repos zählen davon
unabhängig weiter, siehe `feedback_apple_versioning` / Android-Block.)
**Einmalige Ausnahme (User-Vorgabe 2026-09-06):** der Deploy nach 1.0.94
sprang bewusst auf **1.2.0** (explizit vom User so gewünscht, kein Tippfehler
und keine Fortsetzung der 1.0.x-Zählung) — ab da lief die normale
+0.0.1-Patch-Regel auf Basis von 1.2.0 weiter (1.2.1 → 1.2.2 → …).
**Zweite Ausnahme (User-Vorgabe 2026-09-10):** der Deploy nach 1.2.53
sprang bewusst auf **1.3.0** (explizit so gewünscht) — ab da läuft die
normale +0.0.1-Patch-Regel auf Basis von 1.3.0 weiter (1.3.1 → 1.3.2 → …).

**🌐 Das Repo ist seit 2026-09-05 ÖFFENTLICH** (`github.com/boernie77/goldfish`,
MIT-Lizenz). **Bei JEDER Änderung prüfen, ob committeter Code oder Kommentare
echte Namen, E-Mails, interne IPs oder Secrets enthalten** — das ist sofort für
jeden sichtbar, nicht mehr nur theoretisch. Der Modulpfad bleibt bewusst
`videoplayer` (eine Umbenennung wäre nur noch Churn).

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
  `goldfish-ci` Stack 38, Container `goldfish-runner`) bei jedem Push auf `main`.
  Falls Runner offline: **zuerst Container-Logs prüfen**
  (`GET .../containers/{id}/logs`), nicht direkt "Reg-Token expired" annehmen —
  am 2026-08-19 war die echte Ursache `Runner version vX.X.X is deprecated and
  cannot receive messages` (Restart-Loop, `myoung34/github-runner:latest`-Image
  hatte einen veralteten Layer lokal gecacht + `DISABLE_AUTO_UPDATE: "true"` im
  Compose verhinderte Selbst-Update). Fix: Stack-38-Compose holen,
  `DISABLE_AUTO_UPDATE` auf `"false"`, Stack mit `pullImage: true` redeployen
  (Env-Array `ACCESS_TOKEN` mitschicken, sonst geht der Token verloren, gleiches
  Muster wie Stack 37). Falls der Runner trotzdem nicht rechtzeitig wieder
  online kommt: direkt builden via
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

# 📱 Android-App (in Testphase, aktuell 1.2.67)

> **An jede Claude-Session, die Goldfish-Server-API anfasst:**
> Es gibt eine **Android-App** unter `/Users/christian/Projekte/GoldfishAndroid/`
> (eigenes Git-Repo `github.com/boernie77/goldfish-android`, privat),
> die aktuell im **Internal-Testing-Track** der Google Play Console verteilt wird
> (NICHT öffentlich im Play Store). Die App ist NICHT mitversioniert mit dem
> Server — wenn du eine API-Antwort änderst, kann die App stillschweigend
> brechen (Moshi-Parse-Error → leere Listen).
>
> **Die volle Architektur/Feature-Chronik/Build-Notizen stehen jetzt in der
> CLAUDE.md dieses App-Repos** (nicht mehr hier) — bei jeder Änderung, die
> diese App betreffen könnte, dort nachsehen bzw. das Repo direkt öffnen.
>
> **Die drei harten API-Kompatibilitäts-Regeln, die JEDE Session kennen muss:**
> 1. **`resumePosSec` ist NICHT in der `getItem`-Antwort** — separater
>    Endpoint `GET /api/items/{id}/resume`.
> 2. **Download-Endpoint heißt `/api/download/{id}`**, NICHT
>    `/api/items/{id}/download` (404).
> 3. **Cast-Endpoint via `metadata_id`, nicht `item_id`**:
>    `GET /api/metadata/{id}/cast`.
>
> **Wenn du etwas brichst:** versionCode in `app/build.gradle.kts` erhöhen
> (bei JEDER AAB), neue AAB bauen (`./gradlew bundleRelease`), in Play
> Console Internal-Testing-Track hochladen.
>
> Release-Signing-Credentials liegen NICHT im Repo — nur lokal in
> `keystore.properties` (git-ignoriert). **Diesen Keystore NIE committen.**

---

# 🐧 Linux-App (GoldfishLinux, seit 2026-09-12, läuft auf echtem Linux)

> **An jede Claude-Session, die Goldfish-Server-API anfasst:**
> Es gibt außer Android/Apple auch einen **nativen Linux-Desktop-Client**
> unter `github.com/boernie77/goldfish-linux` (öffentlich, lokal
> `~/Projekte/GoldfishLinux/`) — Python 3 + GTK4/libadwaita, als `.deb` für
> Debian 12+/Ubuntu 24.04+/Mint 22+ paketiert.
>
> **Die volle Architektur/aktueller-Stand/Bugfix-Historie steht jetzt in
> der CLAUDE.md dieses App-Repos** (nicht mehr hier).
>
> **Bei API-Änderungen prüfen:** `goldfish_linux/api.py` im dortigen Repo —
> nutzt `/api/auth/login`, `/api/libraries`, `/api/libraries/{id}/folders`,
> `/api/items`, `/api/playback/{id}` + den `?session=<token>`-Query-Fallback,
> `/api/download/{id}`, `/api/items/{id}/watched|favorite`.
> **Wird seit v0.1.7 auf einem echten Linux-Rechner entwickelt und geprüft**
> (Stand 2026-09-16: v0.1.48). Die frühere Warnung „nur auf macOS gebaut,
> nie auf echtem GTK4 gelaufen" gilt nicht mehr — sie hatte damals zu einer
> falschen Fehlerdiagnose geführt, siehe CLAUDE.md des Linux-Repos.

---

# 📺 Fire-TV/Android-TV-App (GoldfishFireTV, seit 2026-09-16, reines Grundgerüst)

> **An jede Claude-Session, die Goldfish-Server-API anfasst:**
> Es gibt seit 2026-09-16 zusätzlich einen **Fire-TV/Android-TV-Client**
> unter `github.com/boernie77/goldfish-firetv` (privat, lokal
> `~/Projekte/GoldfishFireTV/`) — Kotlin + Compose for TV, eigenständiges
> Repo (NICHT Teil von GoldfishAndroid, auch wenn der komplette data/di-
> Layer von dort übernommen und paket-umbenannt wurde).
>
> **Stand: Kern-Flow komplett, auf echtem Gerät verifiziert** (Fire TV
> Stick 4K Max, ADB-over-WiFi): Login, Startbildschirm, Bibliotheks-
> Browsing, Infoseite, Staffel-/Episodenansicht, Suche, Sammlungen/
> Playlists und echte HLS-Wiedergabe via ExoPlayer/media3 laufen. Vier
> Geräte-Feedback-Runden sind durchgearbeitet. Offen: Trailer-Wiedergabe,
> Downloads/Offline und Musik (bewusst Prio 3).
>
> **Die volle Architektur/aktueller-Stand steht in der CLAUDE.md dieses
> App-Repos** (nicht mehr hier) — bei jeder Änderung, die diese App
> betreffen könnte, dort nachsehen.
>
> **Bei API-Änderungen prüfen:** `data/api/GoldfishApi.kt` +
> `data/model/Models.kt` im App-Repo — identischer Client-Code wie
> GoldfishAndroid (1:1 kopiert), Änderungen müssen in BEIDEN Android-Repos
> nachgezogen werden, kein automatischer Sync.

---

# 🍎 Mac/iOS/tvOS-App (GoldfishApple, seit 2026-08-17)

> **An jede Claude-Session, die Goldfish-Server-API anfasst:**
> Es gibt außer Android/Linux auch eine **native Mac/iOS/tvOS-App** unter
> `/Users/christian/Projekte/GoldfishApple/` (SwiftUI, `GoldfishMac` +
> `GoldfishiOS` + `GoldfishTV`, eigenes Git-Repo
> `github.com/boernie77/goldfish-apple`, privat).
>
> **Die volle Architektur/Bugfix-Chronik/Build-Notizen stehen jetzt in der
> CLAUDE.md dieses App-Repos** (nicht mehr hier) — bei jeder Änderung, die
> diese App betreffen könnte, dort nachsehen bzw. das Repo direkt öffnen.
>
> **Die wichtigsten Dauer-Constraints, die JEDE Session kennen muss:**
> - SSO läuft über WKWebView (nicht wie Android komplett ohne OIDC) —
>   `WKWebsiteDataStore` ist persistent, ein Kontowechsel muss ihn explizit leeren.
> - **tvOS:** `Menu` in der Toolbar öffnet zuverlässig NICHTS — immer
>   `.sheet`/`.confirmationDialog` für neue tvOS-UI.
> - Eine persistente Leiste (Mini-Player) darf auf iOS NIE als
>   `.safeAreaInset` außen um eine `TabView` gelegt werden — blockiert die
>   native Tab-Leiste komplett. Muss pro Tab-Inhalt eingebunden werden.
> - Kein Windows/Linux-Target (nur macOS + iOS + tvOS).
>
> **Bei API-Änderungen prüfen:** `GoldfishCore/GoldfishClient.swift` +
> `GoldfishCore/Models/Models.swift` im App-Repo.

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

### Volumes

- `/media` → Unraid-Share `/mnt/user` (alle Shares als Unterordner).
  **Achtung:** Repo-Compose zeigt historisch `:ro`, der LIVE-Stack (Portainer
  Stack 37) mountet es aber **read-write** (`- /mnt/user:/media`, kein `:ro`) —
  verifiziert 2026-08-28. Deshalb kann Goldfish Media-Dateien löschen
  (Detail-Dialog 🗑, Dubletten-Aufräumen über „≈ Ähnliche Dateinamen"). Der Kommentar in
  `internal/api/delete_download.go` über „ist /media read-only gemountet?" ist
  entsprechend meist gegenstandslos.
- **`/media/UD-Disks` → `/mnt/disks`, `/media/UD-Remotes` → `/mnt/remotes`
  (seit 2026-09-09, read-write, User-Wunsch):** Unraid-Unassigned-Devices
  (externe Platten bzw. UD-Netzwerkfreigaben) — komplett andere Host-Pfade
  als `/mnt/user`, daher zwei zusätzliche Mounts, bewusst als Unterordner
  von `/media` (nicht als eigener Top-Level-Mount), damit sie ohne
  Code-Änderung im bestehenden Pfad-Browser + der `/media`-Security-Prüfung
  landen (Docker erlaubt verschachtelte Bind-Mounts problemlos). **Achtung:**
  Laufwerke hier sind typischerweise NICHT dauerhaft angeschlossen — für
  Bibliotheken darauf unbedingt „🚫 Ordner vom Auto-Scan ausschließen…" im
  Auto-Scan-Dialog nutzen (siehe „Scan-Ausschlüsse" unten), sonst löscht der
  ZEITGESTEUERTE Auto-Scan bei fehlender Platte die Einträge fälschlich als
  verwaist aus der DB. Ein manueller ⟳-Scan ist davon bewusst NICHT betroffen
  (User-Vorgabe) — nur unbeaufsichtigt laufende Scans respektieren die Liste.
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
- `user_item_state(user_id, item_id, watched, watched_at, favorite, favorite_at, last_played_at, resume_pos_sec, rating)` — per-User
  (`rating` = persönliche Sternebewertung 0–3, `SetItemRatingFor`, `PUT /api/items/{id}/rating`; UI: Sterne im Detail-Dialog + Kachel-Overlay, nur `kind=private`; Filter `?rating=unrated|min1|min2|exact3`)
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
- `generated_subtitles(id, item_id, language, status, error, generated_at)` — KI-generierte
  Untertitel-Jobs; `status ∈ {pending, running, done, failed}`, UNIQUE(item_id, language).
  Whisper transkribiert immer zuerst auf Englisch → `en.vtt`, dann optional Übersetzung.
  VTT-Dateien liegen unter `/config/generated-subs/{itemID}/{lang}.vtt`. Der
  Playback-Handler zeigt generierte Tracks im Sub-Dropdown (`🎤 Deutsch (KI)`) wenn
  die VTT-Datei auf Disk existiert (unabhängig vom DB-Status — Retry löscht alte Datei nicht).
- `settings.translation_backend` — `none` | `deepl` | `libretranslate`
- `settings.deepl_api_key` — DeepL Free-Keys enden auf `:fx` → `api-free.deepl.com`,
  sonst `api.deepl.com`. Wird auto-detekted in `translate.DeepLTranslator.endpoint()`.
- `settings.libretranslate_url` — z.B. `http://<UNRAID-LAN-IP>:5000`
- `settings.whisper_model` — z.B. `ggml-small`; Datei liegt in `/config/whisper-models/`
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
  old_library_id, new_library_id, renamed_at, undone_at, triggered_by)` —
  Audit-Log für Auto-Rename UND Verschieben (triggered_by ∈ {auto, manual,
  bulk, move}). `old_library_id`/`new_library_id` sind 0/0 wenn kein
  Bibliotheks-Wechsel stattfand (Normalfall + alle Einträge vor der
  2026-07-12-Migration). Undo setzt `undone_at` und schreibt `items.path`
  (+ ggf. `items.library_id`) zurück. Siehe „Auto-Rename bestätigter Filme"
  und „Verschieben in andere Ordner / Bibliotheken".

## Features

### Bibliotheken
- Mehrere Bibliotheken mit Typ **Filme / Serien / Privat**.
- **Multi-Path**: pro Bibliothek beliebig viele Quellordner, werden beim Scan aggregiert.
- Pfad-Browser-Dialog nur unterhalb `/media` (Security-Check, kein Directory-Traversal).
- Inkrementelles Scannen (mtime-Vergleich), Items verwaister Dateien werden entfernt.
- **Folder-gescopter Scan:** `POST /api/scan/{libID}?folder=<rel>` beschränkt Walk +
  Orphan-Delete auf diesen Unterbaum — UI bietet das automatisch wenn man in einem
  Ordner steht (Scan-Button-Default + zwei zusätzliche Einträge im Dropdown).
- **Card-Layout pro Privat-Lib togglebar** (`libraries.channel_label_on_top`,
  Default 1): bei aktivem Toggle (YouTube-Style) zeigt die Kachel-Top-Zeile
  den Top-Folder (Kanal-Name), der Dateiname kommt unten dicker. Bei OFF
  klassisches Layout (Titel oben). Checkbox „🏷 Ordner oben" im Library-
  Manager, nur bei `kind=private` sichtbar (bei Filme/Serien ist Titel
  sowieso oben).
- **Library-Manager-Row-Layout**: zweizeilig — Zeile 1 hat Name + Kind-
  Select + 🗑-Icon (Tooltip „Bibliothek löschen"), Zeile 2 (nur bei
  `kind=private`) hat die Toggle-Pille 🏷 „Ordner oben" (wirkt nur im Grid
  dieser Bibliothek, nicht im Dialog selbst — Tooltip präzisiert
  2026-09-02, User fragte danach). Beide Zeilen mit `flex-wrap` für narrow
  Modals (`.modal` hat `max-width: 520px`).
  **Kein „🏠 Startseite"-Toggle und kein ▲▼ mehr hier** (seit 2026-09-02
  entfernt) — beides ist jetzt pro Benutzer im „🏠 Startseite anpassen"-
  Dialog geregelt (steuert dort zusätzlich die Bibliotheks-Reiterleiste,
  siehe „Startseite (Home-View)") — war doppelt gepflegt.
  `PUT /api/libraries/order` bleibt als Endpoint bestehen, nur ungenutzt
  von der UI.

### Auto-Scan (zeitgesteuert, Browser-Admin-Menü)
- Mehrere unabhängige **Scan-Aufgaben**, jede mit eigenem Zeitplan, Bibliothek und
  Scan-Typ. Erreichbar über Zahnrad → „🕐 Auto-Scan".
- **Zeitplan-Formate:** `daily:HH:MM` | `every:Nh` (alle N Stunden, N 1–23) |
  `weekly:DOW:HH:MM` (Wochentag mon–sun).
- **Scan-Typ:** inkrementell (mtime-Vergleich, Default) oder vollständig (force=true).
- **Bibliothek:** einzelne Lib oder alle (libraryId=0).
- **Speicherung:** JSON-Array in `settings.auto_scan_tasks`. Legacy-Migration:
  alte Einzel-Settings (`auto_scan_enabled`/`schedule`/`library_id`) werden beim
  ersten Laden automatisch in eine Aufgabe konvertiert.
- **Server:** `RunAutoScan`-Goroutine prüft jede Minute alle aktiven Aufgaben
  unabhängig; feuert pro Aufgaben-ID max. einmal pro Minute.
- **Menü-Subtitle** zeigt „✓ N aktive Aufgabe(n)" wenn mindestens eine aktiv.

### Scan-Ausschlüsse — NUR Auto-Scan (seit 2026-09-09, LIVE 1.2.47, korrigiert 1.2.48)
- User-Anlass: Unassigned-Devices-Laufwerke (externe Platten, gemountet
  unter `/mnt/disks`/`/mnt/remotes` auf dem Unraid-Host, seit diesem Datum
  zusätzlich in den Live-Stack unter `/media/UD-Disks`/`/media/UD-Remotes`
  gemountet, siehe „Volumes" oben) sind nicht dauerhaft angeschlossen — ein
  unbeaufsichtigter Auto-Scan bei fehlender Platte würde die zugehörigen
  Items sonst als „verschwunden" werten und aus der DB löschen (Dateien
  selbst bleiben unangetastet, aber Goldfish „vergisst" sie).
- **Gilt NUR für den Auto-Scan, nicht für den manuellen ⟳-Scan** (User-Vorgabe
  2026-09-09, Korrektur der ersten Version 1.2.47): `Scanner.Start`/`run` nehmen
  `respectScanExcludes bool` — `RunAutoScan` übergibt `true`, `startScan`/
  `startScanAll` `false`. Bei `false` bleibt `excludedFolders` leer, wodurch
  Walk-Skip UND Orphan-Schutz automatisch zu No-Ops werden (keine eigene
  Verzweigung nötig).
- **Bekannte Grenze bei Multi-Path-Namenskollisionen:** der Ausschluss
  wirkt auf den aggregierten Ordner-NAMEN (`rel_path`-Top-Segment), nicht
  auf „welche physische Quelle". Wird ein Ordner als eigene zusätzliche
  Multi-Path-Wurzel zu einer Library hinzugefügt (statt als Unterordner
  einer bereits vorhandenen Wurzel), tragen Dateien DIREKT in dieser neuen
  Wurzel gar keinen Ordnernamen im `rel_path` (Multi-Path-Wurzeln liefern
  `filepath.Rel(root, path)`, der Wurzel-eigene Name verschwindet dabei) —
  ein Ausschluss kann sie strukturell nicht treffen. Trägt der neue Wurzel-
  Ordnername zusätzlich denselben Namen wie ein bereits existierender
  Unterordner der PRIMÄREN Library-Quelle, kollidieren beide beim
  Ausschließen. Sauberer Workaround: externe Laufwerke als **eigene,
  separate Bibliothek** anlegen (dann greift der `folder=""`-„gesamte
  Bibliothek ausschließen"-Fall kollisionsfrei) — war hier für den
  konkreten User-Fall keine Option (Ordnerinhalte sollen dauerhaft
  weiterhin unter der bestehenden Library "a" geführt werden), daher blieb
  es bei der Multi-Path-Konstruktion mit der oben beschriebenen Grenze.
- **Neue Tabelle `scan_excluded_folders(library_id, folder)`** — Zeilen-
  Existenz = ausgeschlossen (gleiche Konvention wie `trickplay_folders`/
  `intro_skip_folders`). `folder=""` schließt die GESAMTE Bibliothek aus
  (nur vom Auto-Scan, siehe oben).
  Store: `internal/store/scan_excludes.go` — `SetScanExcludedFolder`,
  `ListScanExcludedFolders`, `IsRelPathExcluded` (Präfix-Check mit `/`-Grenze,
  damit z.B. "Foo2" nicht fälschlich unter ausgeschlossenem "Foo" fällt),
  `ItemPathsUnderFolders` (liefert `items.path`, nicht `rel_path` — direkt
  fürs Scanner-`keep`-Set gedacht).
- **Scanner-Integration** (`internal/scanner/scanner.go run()`): lädt die
  Ausschlussliste nur wenn `respectScanExcludes=true` (Auto-Scan). (1) Im
  `filepath.WalkDir`-Callback wird ein ausgeschlossener Ordner komplett
  übersprungen (`filepath.SkipDir`, gleicher Mechanismus wie der bestehende
  Sample-Ordner-Skip). (2) VOR dem Orphan-Cleanup werden die Disk-Pfade
  aller bereits in der DB stehenden Items unter ausgeschlossenen Ordnern
  per `ItemPathsUnderFolders` ins `keep`-Set aufgenommen — **essentiell**,
  sonst würde Punkt (1) diese Items erst gar nicht ins `keep`-Set bringen
  und das Orphan-Cleanup direkt danach würde sie löschen, obwohl sie nur
  "ausgeschlossen" und nicht wirklich weg sind. Beide Schritte zusammen
  sind nötig, keiner reicht allein — beide laufen aber nur, wenn
  `respectScanExcludes=true` war.
- **API** (admin-only): `GET/PUT /api/libraries/{id}/scan-excludes`
  (`internal/api/scan_excludes.go`) — PUT nimmt `{folder, excluded}`, PUT
  wirkt sofort (kein „Übernehmen"-Schritt, wie beim Introskip-Dialog).
- **UI:** Button „🚫 Ordner vom Auto-Scan ausschließen…" im
  „🕐 Auto-Scan"-Dialog öffnet `#scanExcludeDialog` (`scan.js`, Code-Kopie
  des Baum-Musters aus `playlists.js` `openShuffleScopeDialog`/
  `renderShuffleScopeTree` — lazy pro Ebene über
  `GET /api/libraries/{id}/folders?parent=`, aber Single-Library statt
  library-übergreifend, da Ausschlüsse pro Bibliothek gespeichert werden).
  Checkbox-Klick ruft sofort PUT auf, kein Speichern-Button nötig. Dialog-
  Text weist explizit darauf hin, dass NUR Auto-Scan betroffen ist.
- **Kompakte Zusammenfassung im Auto-Scan-Dialog selbst** (seit 1.2.50,
  User-Wunsch "würde ich gerne sofort sehen"): `#autoScanExcludeSummary`
  (`scan.js renderAutoScanExcludeSummary`) — ein Call pro Bibliothek gegen
  denselben `GET .../scan-excludes`-Endpoint (kein neuer Server-Code, bei
  der überschaubaren Bibliotheks-Anzahl unproblematisch), zeigt
  "🚫 Ausgeschlossen: **Lib**: Ordner, Ordner · **Lib2**: …" oder einen
  "keine Ausschlüsse"-Hinweis. Wird beim Öffnen von `openAutoScan()` UND
  nach jedem Checkbox-Toggle im `#scanExcludeDialog` neu gerufen (der liegt
  beim Öffnen ÜBER dem Auto-Scan-Dialog, nicht als Ersatz dafür) — so bleibt
  die Summary live aktuell, auch während der Sub-Dialog noch offen ist.
- **🚫-Badge auf der Ordner-Kachel (seit 1.2.49, User-Feedback "sieht man auf
  der Übersichtsseite nicht"):** `store.Folder` bekam ein `Excluded bool`-
  Feld, gesetzt in `annotateDrilldown` (`internal/store/folder_nav.go`,
  gemeinsamer Endpunkt für BEIDE `SubfoldersAtFiltered`-Zweige — Library-
  Root über `topLevelFolders` UND tiefere Ebenen — daher der richtige Ort
  für eine Annotation, die überall gelten soll). `IsRelPathExcluded` prüft
  dabei auch Vorfahren (ein Unterordner eines ausgeschlossenen Ordners zeigt
  das Badge ebenfalls). **Derselbe Endpoint (`GET /api/libraries/{id}/folders`)
  füttert sowohl den Scan-Exclude-Dialog-Baum ALS AUCH die normale
  Bibliotheks-Übersicht** (`grid.js` lädt Ordner-Kacheln darüber) — die
  Annotation kam dadurch ohne separaten Endpoint automatisch in beide
  Ansichten. Frontend: `cards.js renderFolderCard` — `.folder-excluded`
  (Position `top:34 left:6`, unter `.folder-merged` gestapelt, beide sind
  seltene Fälle), nur für Admins sichtbar (`state.me.isAdmin`, reines Admin-
  Konzept), Tooltip stellt klar, dass ein manueller Scan davon NICHT
  betroffen ist (sonst missverständlich als "wird nie gescannt" lesbar).
- Tests: `internal/store/scan_excludes_test.go` (Store-Ebene: Toggle,
  `IsRelPathExcluded`-Matching, `ItemPathsUnderFolders`,
  `SubfoldersAtFilteredMarksExcluded` fürs Badge — die
  `respectScanExcludes`-Verzweigung selbst ist reines Scanner-Wiring ohne
  externe Abhängigkeit auf DB-Ebene, kein weiterer Test nötig).

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
### Trickplay (Hover-Vorschau)
- Background-Worker erzeugt Sprite-JPGs + WebVTT (10 s-Intervalle, konfigurierbar) pro Item.
- **VAAPI-Hardware-Decode** für die ffmpeg-Generierung (`-hwaccel vaapi
  -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi`, dann
  `scale_vaapi` + `hwdownload,format=nv12`). Wird in `main.go` via
  `trickplayWorker.SetHWAccelDevice(hw.Device)` konfiguriert. Ohne HW-Decode
  laufen 4K-60fps-Quellen regelmäßig in den Timeout.
- **Performance-Flags vor `-i`** (essentiell, NICHT entfernen):
  - `-skip_frame nokey` — Decoder gibt nur Keyframes raus. Hauptmedizin
    gegen 4K-60fps-Timeouts: statt hunderttausenden Frames werden nur die
    wenigen Sekunden-Keyframes verarbeitet (~50× schneller). Bei 10s-
    Sprite-Intervall und typischem Keyframe-Abstand ≤5s bleibt jeder Slot
    nah genug am Soll-Timestamp.
  - `-err_detect ignore_err` + `-fflags +discardcorrupt+genpts` — kaputte
    NAL-Units / Invalid-Stream-Daten in Release-Encodes brechen den
    Decoder nicht mehr ab.
- **Software-Fallback:** Wenn VAAPI zur Laufzeit scheitert (Fehler enthält
  „hwaccel initialisation", „Function not implemented", „No support for
  codec", „Could not find ref", „Failed to inject frame", „Failed to
  query surface", „hwdownload"), wird derselbe Befehl automatisch ohne
  `-hwaccel`-Header erneut ausgeführt. Erkennbar im Log:
  `[trickplay] item X: VAAPI-Init fehlgeschlagen, fallback auf Software`.
- **Timeout**: `duration/5 + 300s` proportional, Caps 30/60/180 min
  (default / 1080p+ / 4K+). Bei `tctx.Err() == DeadlineExceeded` schreibt
  der Handler eine klare Meldung in `items.trickplay_error`
  (`timeout nach 11m0s …`), statt nichtssagendem `signal: killed ()`.
- **Filter-Chain VAAPI**: `fps=1/N,scale_vaapi=w=160:h=90:force_original_aspect_ratio=decrease,hwdownload,format=nv12,pad=…color=black,tile=XxY`
- **Filter-Chain Software-Fallback**: `fps=1/N,scale=160:90:force_original_aspect_ratio=decrease,pad=…color=black,tile=XxY`
- **Pausiert automatisch während eines Library-Scans UND während gerade
  irgendetwas angesehen wird** (seit 2026-09-11, LIVE 1.3.6, User-Report
  "Mir ist die Last auf dem Server durch Goldfish zu hoch ... wenn ein Scan
  läuft, soll Trickbild kurz pausieren" + im selben Gespräch erweitert:
  "wenn etwas abgespielt wird, muss ein Scan und Trickbild auch pausieren
  oder reduziert werden, das hat immer Vorrang"). Trickplay ist wie
  Introskip/OCR sehr I/O-intensiv (ffmpeg pro Item) und kollidiert sonst
  mit einem gleichzeitig laufenden Scan auf demselben Netzwerk-Mount ODER
  mit dem, was der User gerade tatsächlich streamt. Mechanismus identisch
  zu Introskip (`SetPauseCheck`/`waitWhilePaused`, siehe dort) — neu ist
  der zweite Baustein **`internal/playback/activity.go`**: ein simpler,
  paketweiter „wird gerade etwas wiedergegeben"-Merker
  (`TouchActivity()`/`Active()`, 25s-Zeitfenster seit dem letzten
  beobachteten Wiedergabe-Request). Wird angestoßen aus `Session.Touch()`
  (Transcode — deckt sowohl Segment- als auch Progress-Poll-Requests ab,
  beide riefen das schon vorher für die Session-GC) UND direkt aus
  `streamDirect` (Direct Play hat kein Session-Konzept). `main.go` verdrahtet
  `trickplayWorker.SetPauseCheck(func() bool { return sc.Status().Running ||
  playback.Active() })` — **derselbe `playback.Active()`-Check wurde im
  selben Zug auch bei `introSkipWorker`/`ocrSubWorker` ergänzt** (vorher nur
  scan-pausiert, jetzt zusätzlich wiedergabe-pausiert) **und beim Scanner
  selbst** (`Scanner.SetPauseCheck`, neuer Pause-Punkt im Datei-Loop direkt
  vor dem teuren `probeItem`/ffprobe-Call, NICHT vor dem billigen
  mtime-Skip-Check) — der Scan selbst hat also ebenfalls Vorrang vor sich
  ausschließlich, aber weicht laufender Wiedergabe. **Bewusst kein
  Rate-Limiting/Drosseln statt Pausieren** — die vier Hintergrund-Worker sind
  ohnehin nicht latenzkritisch (Trickplay/Introskip/OCR laufen im Hintergrund
  über Stunden, ein Scan darf auch mal ein paar Minuten länger dauern),
  ein hartes Pausieren ist einfacher korrekt zu implementieren als ein
  quantitatives Throttling und liefert dem User exakt das gewünschte "hat
  immer Vorrang". Tests: `internal/playback/activity_test.go`.
- Asset-Endpoints: `/api/trickplay/{id}/thumbs.vtt`, `/api/trickplay/{id}/sprite.jpg`.
- UI: Eigenes kompaktes Hover-Plugin **direkt in `app.js`** (`attachTrickplayHover`,
  `parseThumbVTT`) — parst VTT, hängt Mousemove auf `progressControl`, zeigt
  Sprite-Ausschnitt via `background-position`. Kein externes JS-Plugin.
- Worker läuft non-blocking, Status über `/api/trickplay/status` — **admin-only**
  seit dem Fix unten (war vorher bewusst „Konsum für alle").
- Trigger-Gate: `item.trickplayStatus === "done"` aus der DB — HEAD-Probing
  würde gegen 405 laufen (chi registriert HEAD nicht automatisch für GET-Routen).
- **⚠ Worker-Status-Endpoints sind ALLE `requireAdmin`** — sie liefern
  `currentTitle`/`currentItemId`/`currentFolder` des gerade verarbeiteten
  Items, bibliotheksübergreifend und ohne ACL-Bezug zum Abfragenden:
  `/trickplay/status`, `/scan/status`, `/enrich/refresh-all-status`,
  `/whisper/status`, `/whisper/download-status`, `/introskip/status`.
  `/api/health` liefert nur noch aggregierte Zahlen, keinen Item-Bezug (stand
  dort vorher sogar komplett unauthentifiziert). Die Frontend-Polls laufen in
  `boot()` nur innerhalb eines `if (state.me.isAdmin)`-Blocks.
  **Beide Seiten separat prüfen:** ein ungated Endpoint ist der eigentliche
  Leak (URL direkt aufrufbar), ein ungated Frontend-Poll auf einen gated
  Endpoint nur eine Konsolen-Fehlermeldung. Zwei Leak-Runden nötig (LIVE
  1.2.16 + 1.2.51), beide aus dem Muster „Aktivierung admin-only, Konsum für
  alle" — siehe [[feedback_user_isolation_before_deploy]], Chronik in
  DECISIONS.md. Item-/Library-scoped Status-Endpoints
  (`/transcode/{id}/progress`, `/download/{id}/compat-status`,
  `/items/{id}/subtitle-jobs`, `/items/move/status`) sind bereits korrekt per
  `requireLibAccess`/`requireAdmin` abgesichert.
- **`.scan-group` (⟳-Scan-Button + Dropdown) ist admin-only** —
  `renderUserMenu()` (`admin.js`) blendet sie per `state.me.isAdmin` aus.
  Ebenso die Sort-Dropdown-Wartungsfilter „Duplikate", „🔀 Mehrere Versionen",
  „≈ Ähnliche Dateinamen", „Ohne TMDB-Zuordnung", „Alle Unbestätigten",
  „⚠ Verdächtige Zuordnungen", „🪤 Nur Interlaced" (`data-admin-only="1"` in
  `index.html`, Check in `grid.js` neben dem `data-kinds`-Filter) — reine
  Aufräum-/Zuordnungs-Werkzeuge. **„♡ Nur Favoriten" bleibt bewusst sichtbar**
  (User-Rückfrage explizit bestätigt) — normales Nutzer-Feature, liegt nur im
  selben Options-Block. Rein UI-seitig, kein Backend-ACL-Fix nötig (die
  Ansichten sind ohnehin auf `state.currentLibrary` gescoped).
- **Die nativen Apps sind davon nicht betroffen** — weder Apple noch Android
  rufen einen dieser Status-Endpoints auf (beide haben kein Admin-UI).
- **Admin-Dialog „Trickplay verwalten"** (Settings-Menü, admin-only):
  - Tabs mit Listen der done/failed/pending Items inkl. Fehlermeldung
  - „↻ Fehler erneut versuchen" setzt alle `failed` → `pending`, startet neu
  - „🗑 Alle Trickplay-Dateien löschen" (cancel-and-wipe)
- **Cancel-Button** in der laufenden Status-Bar (rotes ✕).
- **Ordner-Toggle ändert nie Dateien auf Disk** — Deaktivierung entfernt nur
  den DB-Marker, Dateien bleiben bis zum expliziten „Alle löschen".

### Intro-Erkennung ("Skip Intro", seit 2026-08-11, Algorithmus v2 seit 2026-08-13)
- Background-Worker (`internal/introskip`) erkennt den Vorspann/das Opening
  einer Serie automatisch durch **Audio- UND Bild-Fingerprint-Vergleich**
  zwischen den Episoden derselben Show, mit **echtem Paarweise-Vergleichen**
  (jede Episode gegen jede andere) — an Jellyfins „Intro Skipper"-Plugin
  angelehnt (`ConfusedPolarBear/intro-skipper` auf GitHub; Schwellenwerte in
  `correlate.go` von dort übernommen, Quellenangabe im Code-Kommentar).
  Kein manuelles Markieren nötig. Auslöser für das Redesign: User-Anforderung
  "Zuverlässigkeit ist mir sehr wichtig, Ton mit Bild kombinieren und auf
  Paarweise umsteigen" — längere Laufzeit bewusst gegen Verlässlichkeit
  eingetauscht. Ältere, mittlerweile abgelöste Algorithmus-Iterationen (reines
  Audio-Delta-Voting, verschiedene Ausreißer-Filter) siehe Git-Historie/Memory
  `project_feature_introskip`, nicht mehr im Code vorhanden.
- **Aktivierung ist strikt pro einzelnem Serien-Ordner** (Top-Level-Ordner
  einer TV-Library) — es gibt bewusst **keinen** „ganze Bibliothek
  aktivieren"-Schalter. `folder == ""` wird von Store UND API-Handler
  zurückgewiesen (`SetIntroSkipFolder`, `setIntroSkipFolder`).
- **Optionale Staffel-Beschränkung** (`intro_skip_folders.season`, `0` =
  alle Staffeln): ein aktivierter Ordner kann auf EINE Staffel eingeschränkt
  werden (`SetIntroSkipFolderSeason`), z.B. für kontrolliertes Testen. `PUT
  /api/libraries/{id}/introskip` nimmt optional `season` im Body an. UI:
  kleines Zahlenfeld neben jeder aktivierten Serie in `introskip.js`
  (`.introskip-season-input`, Platzhalter „alle"). **Wichtig:** `season` ist
  im API-Body ein **Zeiger** (`*int`), nicht ein normaler int — ein reiner
  Checkbox-Toggle sendet `{folder, enabled}` OHNE `season`-Feld; würde der
  Handler das als `season=0` lesen, würde JEDES An/Aus-Toggle die
  Staffel-Beschränkung heimlich löschen (real passiert, 2026-08-13). Mit dem
  Zeiger wird `SetIntroSkipFolderSeason` nur bei explizit mitgeschicktem Feld
  aufgerufen.
- **Voraussetzung: mindestens 2 Episoden** (nach Staffel-Filter, falls
  gesetzt) im Ordner — die Erkennung vergleicht immer mehrere Episoden
  gegeneinander. Serien mit nur einer Episode bekommen keinen Skip-Button;
  das ist eine inhärente Grenze der Methode, kein Bug.
- **Pipeline pro Job** (ein Job = ein ganzer Serien-Ordner, nicht pro
  Episode; `processShow` in `worker.go`):
  1. Pro Episode ZWEI Fingerprints: `fpcalc -raw -length 900` (Chromaprint,
     `fingerprint.go`) für Audio UND `ffmpeg … fps=1,scale=9x8,format=gray`
     (`videofingerprint.go`) für Bild — 1 dHash (64-Bit Differenz-Hash) pro
     Sekunde der ersten 15 Minuten. **Achtung Falle bei Audio:** fpcalcs
     `DURATION=`-Zeile ist die volle Datei-/Streamlänge, NICHT die
     tatsächlich fingerprintete Länge — die Sekunden-pro-Frame-Dauer wird
     deshalb über `min(DURATION, prefixSeconds) / Anzahl Fingerprint-Werte`
     berechnet, sonst verfälscht das die Umrechnung.
  2. **Echtes Paarweise-Vergleichen:** jede Kandidaten-Episode wird gegen
     JEDE andere Episode im Job einzeln korreliert (N·(N-1) Korrelationen
     bei N Episoden). Pro Paarung:
     - `correlateAudio` (Jellyfin-Algorithmus): Inverted-Index-Shift-Search
       (`buildInvertedIndex32`/`candidateShifts32`, Suchradius
       `invertedIndexShift=2`) statt Brute-Force über alle Zeitversätze.
       `maxHammingPerFrame=6` (von 32 Bit). Längster zusammenhängender Lauf
       gewinnt (`findLongestContiguous`, lückentolerant bis
       `maxTimeSkipSec=3.5s`). `maxIntroDurationSec=120` verhindert lange
       wiederverwendete Szenen-Musik als Fehltreffer.
     - **Bild-Gegenprüfung** (`verifyVideoMatch`): der audio-gefundene
       Zeitbereich wird per dHash-Vergleich (Hamming-Distanz ≤12/64 Bit,
       ≥60% Frame-Übereinstimmung) bestätigt — nutzt den von `correlateAudio`
       gelieferten Zeitversatz direkt, keine zweite unabhängige Bildsuche.
       Verwirft Fälle, wo Audio zufällig matcht, aber der Bildinhalt
       offensichtlich unterschiedlich ist (z.B. wiederverwendete Score-Musik).
     - Nur wenn BEIDE Signale übereinstimmen, zählt die Paarung als
       Beobachtung.
  3. **Konsens über mehrere Beobachtungen** (`aggregateObservations`,
     `minAgreeObservations=2`): mindestens 2 unabhängige Referenz-Episoden
     müssen für dieselbe Kandidaten-Episode ein zeitlich nahes Ergebnis
     liefern (`agreementToleranceSec=20`). Median von Start/Ende des größten
     Clusters gewinnt. **Bewusst PRO Kandidaten-Episode isoliert** (nicht
     global über die ganze Show) — ein früherer Cluster-Ansatz über alle
     Episoden hinweg bestrafte Serien mit legitim schwankender
     Cold-Open-Länge.
  4. Ergebnis pro Episode landet in `items.intro_start_sec`/
     `intro_end_sec` (NULL = nicht analysiert/kein Treffer).
     `items.intro_checked_at` markiert „Analyse-Versuch gemacht" auch ohne
     Treffer — verhindert Endlosschleifen im Rescan, analog
     `metadata.cast_fetched_at`.
  - **Backfills** (`cmd/goldfish/main.go`, je ein Settings-Key als
    Einmal-Gate): `backfillIntroSkipOutliers` (v3, nutzt
    `ForceRetryIntroSkipJob` — setzt IMMER auf `pending`; bei künftigen
    Reset-Backfills IMMER diese Funktion nutzen, NIE `UpsertIntroSkipJob`,
    das nur `failed`-Jobs zurücksetzt und `done`-Jobs still ignoriert).
    `backfillIntroSkipDisableAllExceptChuckS2` lief einmalig nach dem
    Jellyfin-Redesign: deaktivierte alle bisher aktivierten Serien-Ordner bis
    auf Chuck (Namensvergleich am letzten Pfadsegment), beschränkte Chuck auf
    Staffel 2 — kontrollierte Erstverifikation des neuen Algorithmus. Nach
    erfolgreichem Test hat der User alle 211 Serien wieder aktiviert (läuft
    im Hintergrund weiter).
- **Trigger:** Ordner-Aktivierung reiht sofort einen Job ein
  (`UpsertIntroSkipJob` + `Trigger()`). Nach jedem Scan werden bereits
  aktivierte Ordner mit neuen unanalysierten Episoden automatisch erneut
  eingereiht (`introSkipWorker.EnqueueStaleFolders()` im
  `sc.OnComplete`-Hook in `main.go`, analog `enricher.EnrichAllFoldersNow`;
  berücksichtigt die Staffel-Beschränkung via `IntroSkipFolderSeason`).
- **Worker läuft nur, wenn `introskip_enabled` (Settings-KV) `"true"` ist**
  — Ordner lassen sich trotzdem vorab konfigurieren, bevor der globale
  Schalter an ist (`runOnce()` prüft das Setting, no-opt sonst).
- Endpoints (alle außer `status` admin-only):
  ```
  GET/PUT /api/introskip/settings                  {enabled}
  GET/PUT /api/libraries/{id}/introskip             {folder, enabled, season?}
  GET     /api/introskip/status                     (Live-Worker-Status, offen)
  GET     /api/introskip/log?status=pending|running|done|failed
  GET     /api/libraries/{id}/introskip/episodes?folder=
  POST    /api/introskip/folders/{id}/retry         {folder}
  POST    /api/introskip/retry-failed
  ```
- **„Läuft"-Tab im Job-Status (seit 2026-09-06, User-Wunsch, analog OCR-
  Dialog)**: Admin-Dialog zeigte bisher nur Ausstehend/Fertig/Fehler — der
  `running`-Status existierte im Store schon lange
  (`Store.MarkIntroSkipJobRunning`), war nur nie über `introSkipLog`
  abfragbar (`allowed`-Map kannte nur `done|failed|pending`). Rein additiv:
  neuer Tab-Button + erweiterte `allowed`-Map, `ListIntroSkipJobsByStatus`
  selbst brauchte keine Änderung (reiner `WHERE status = ?`-Query).
- **Deaktivierte Serien werden vom Worker wirklich ignoriert** (seit
  2026-08-13): `Store.ListPendingIntroSkipJobs` joint gegen
  `intro_skip_folders`, sodass nur Jobs aktivierter Ordner gezogen werden —
  vorher hätte ein bereits `pending` stehender Job trotz Deaktivierung
  weitergelaufen.
- **„🆕 Neue Serien automatisch aktivieren" pro Bibliothek (seit 2026-09-06,
  User-Wunsch)**: bewusste, OPT-IN-Erweiterung des strikten Pro-Ordner-Opt-in
  oben — ändert NICHTS an der Aktivierungslogik selbst (weiterhin eine Zeile
  pro Serie in `intro_skip_folders`), sondern automatisiert nur das manuelle
  Anhaken für Serien, die NACH dem Aktivieren des Schalters neu gescannt
  werden. Auslöser: User hatte via „☑ Alle auswählen" alle vorhandenen
  Serien aktiviert und erwartete danach, dass neu hinzukommende automatisch
  mitlaufen. Neue Spalte `libraries.intro_skip_auto_new` (0/1) +
  `PUT/GET /api/libraries/{id}/introskip-auto-new` (admin). Neue Tabelle
  `intro_skip_seen_folders(library_id, folder)` — `SetIntroSkipFolder`
  trägt bei JEDEM bewussten Toggle (an ODER aus) eine Zeile ein, NIE
  gelöscht (anders als `intro_skip_folders` selbst, das seine Zeile beim
  Deaktivieren löscht — "Zeilen-Existenz = aktiviert"). Ohne diese zweite
  Tabelle könnte man "noch nie behandelt" nicht von "explizit deaktiviert"
  unterscheiden — eine bewusst ausgeschaltete Serie würde sonst beim
  nächsten Scan automatisch wieder aktiviert.
  `Store.NewIntroSkipCandidateFolders(libID)` liefert alle Top-Level-Ordner
  MINUS (aktiv ODER je gesehen). `Worker.EnqueueNewShowsForAutoLibraries()`
  (analog `EnqueueStaleFolders`) läuft im selben `sc.OnComplete`-Hook in
  `main.go` nach jedem Scan: für jede Library mit `intro_skip_auto_new=1`
  werden alle Kandidaten aktiviert + eingereiht. UI: eigene Checkbox im
  Introskip-Dialog unter dem Bibliotheks-Dropdown (`introSkipAutoNewToggle`,
  pro Library geladen/gesetzt, nicht global). **Bekannte Einschränkung:**
  ein Ordner, der VOR 2026-09-06 einmal aktiviert und wieder deaktiviert
  wurde, hinterließ keine `intro_skip_seen_folders`-Spur und könnte beim
  ersten Lauf nach diesem Update fälschlich als "neu" erneut aktiviert
  werden — bewusst kein rückwirkender Backfill, weil der sonst JEDEN
  bestehenden unaktivierten Ordner in JEDER Bibliothek pauschal als "schon
  gesehen" markieren und das Feature für Bestandsbibliotheken komplett
  wirkungslos machen würde. Tests:
  `internal/store/introskip_auto_new_test.go`.
- **`UpsertIntroSkipJob` läuft bei JEDEM `Enabled=true`**, unabhängig vom
  `season`-Feld — nur das season-spezifische `SetIntroSkipFolderSeason` bleibt
  an `Season != nil` gekoppelt (siehe Season-Zeiger oben). War bis 2026-09-06
  fälschlich an `Enabled && Season != nil` gehängt: ein reiner Checkbox-Toggle
  (und „☑ Alle auswählen") aktivierte den Ordner, legte aber NIE einen Job an —
  der Worker hatte nichts zu tun, ohne jede Fehlermeldung. Backfill
  `backfillIntroSkipMissingJobs` (Gate `intro_skip_missing_jobs_backfill_v1`)
  holt job-lose Ordner nach.
- **Pausiert automatisch während eines Library-Scans UND während gerade
  irgendetwas angesehen wird** (seit 2026-09-11 auch Letzteres, siehe
  „Trickplay" oben für `playback.Active()`) (`Worker.SetPauseCheck`
  in `cmd/goldfish/main.go`, gespeist aus `sc.Status().Running ||
  playback.Active()`): Introskip
  ist sehr I/O-intensiv (ffmpeg+fpcalc pro Episode) und kollidierte mit
  gleichzeitigen Scans auf demselben Netzwerk-Mount (real beobachtet:
  massenhaft `ffprobe: exit status 1` während eines Scans). Pause wirkt an
  zwei Stellen: `runOnce()` startet keinen neuen Job, UND die
  Episoden-Fingerprint-Schleife in `processShow` pausiert zwischen zwei
  Episoden (`waitWhilePaused`) — ein bereits laufender langer Job muss also
  nicht erst fertig werden, bevor ein dazwischen gestarteter Scan Vorrang
  bekommt. Kein Datenverlust beim Scan selbst (Orphan-Löschung betrifft nur
  Dateien, die vorher schon in der DB standen).
- **Episoden-Detailliste:** jede Serien-Zeile im Job-Tab (Fertig/Fehler/
  Ausstehend) lässt sich über ein ▸-Toggle aufklappen (`toggleIntroSkipEpisodeList`
  in `introskip.js`, lazy geladen bei erstem Klick) — zeigt pro Episode
  Titel + Status (✓ Start–Ende / kein Treffer / noch nicht geprüft). Der
  Endpoint ist status-unabhängig (`Store.IntroSkipEpisodeDetails`), liefert
  also in allen drei Tabs denselben aktuellen Stand.
- **Admin-Dialog „⏭️ Intro-Erkennung"** (Settings-Menü): globaler
  An/Aus-Toggle + flache (nicht-rekursive) Liste der Top-Level-Ordner
  einer TV-Bibliothek mit Checkbox pro Zeile (PUT **sofort** bei Klick,
  kein „Übernehmen"-Schritt — anders als der rekursive
  Shuffle-Scope-Dialog, weil eine Serie immer ein Top-Level-Ordner ist,
  kein Baum nötig) + Job-Tabs done/pending/failed (`.tp-tab`-Klassen vom
  Trickplay-Manager wiederverwendet). **↻-Retry gibt es sowohl im
  „Fehler"- als auch im „Fertig"-Tab** — ein Job kann technisch
  erfolgreich sein, aber inhaltlich unbrauchbar (0 Treffer, z. B. nach
  Threshold-Änderung); `Store.ForceRetryIntroSkipJob` setzt IMMER auf
  pending zurück (anders als `UpsertIntroSkipJob`, das beim bloßen
  Ordner-Toggle nur `failed`-Jobs zurücksetzt und bereits fertige Analysen
  nicht anfasst).
- **Player:** `#introSkipOverlayBtn` (statisches DOM-Element in
  `index.html`, in `.video-stage` neben `<video>`/`#prebufferOverlay` —
  **kein** Video.js-ControlBar-Component, war anfangs so gebaut, aber
  User-Feedback 2026-08-12 "übersehe ich" → Redesign als großer,
  auffälliger Pill-Button direkt im Videobild unten rechts, analog
  Jellyfins Skip-Intro-Button). `maybeToggleIntroSkip(vjs)` in `player.js`
  zeigt/versteckt ihn (CSS-Klasse `hidden`) im selben `timeupdate`-Handler
  wie `maybeMarkWatched` — inkl. derselben `virtualOffset`-Korrektur für
  den Transcode-Modus (dort zählt `vjs.currentTime()` nur lokal ab
  Segment-Start). Klick-Handler einmalig über `wireIntroSkipOverlayOnce()`
  gewired (statisches Element, kein Video.js-Kind, das pro Player-Open neu
  entstünde), berechnet das Delta zu `introEndSec` und ruft den
  bestehenden transcode-bewussten `skipPlayer(delta)`.
- **Docker:** Runtime-Stage installiert `libchromaprint-tools` (Debian-Paket,
  liefert `/usr/bin/fpcalc`) — kein eigener Cmake-Build-Stage nötig wie bei
  whisper.cpp, da Chromaprint als fertiges bookworm-Paket existiert.
- **Nicht** in `ListItems`/`playlists.go`/`home.go`/`collections.go`
  eingebaut — nur `GetItemFor` (der Player-Datenpfad beim Öffnen) liefert
  `introStartSec`/`introEndSec`. Absichtlich minimal gehalten (YAGNI), der
  Skip-Button braucht die Werte nur beim Player-Open.

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

### Trailer (Jellyfin-artig, seit 2026-09-04)
- Nur für echte Filme (`tmdb_type=movie`) im Detail-Dialog: 🎬-Button neben
  „Abspielen", öffnet einen öffentlich auf YouTube liegenden Trailer als
  eingebettetes iframe (`trailerDialog`). Kein eigener Video-Host/Download —
  reiner Embed, exakt wie Jellyfins Trailer-Funktion.
- `GET /api/metadata/{id}/trailer` → `tmdb.Client.GetMovieTrailer` holt
  `/movie/{tmdbId}/videos` (mit `include_video_language=<lang>,en,null`) und
  wählt den besten YouTube-Trailer/Teaser aus: bevorzugte Sprache > Englisch >
  alles andere, dabei offizielle Einträge und „Trailer" vor „Teaser".
  **Bevorzugte Sprache ist immer `c.language`** (aktuell fest `de-DE`, s.
  `tmdb.New()`) — **niemals hartkodiert „de"**, damit eine künftig
  umschaltbare Server-Sprache (User-Ankündigung 2026-09-04: geplant,
  mindestens Deutsch/Englisch) automatisch auch die Trailer-Sprache mitzieht,
  ohne diesen Code anzufassen. Cache: 15-min-TTL-Cache des TMDB-Clients
  (Key `movietrailer:<id>:<lang>`), inkl. „kein Trailer gefunden" (nil).
- 404 (kein Trailer, TMDB deaktiviert, falscher `tmdb_type`) ist der
  Normalfall bei den meisten Filmen — Frontend blendet den Button dann
  einfach aus, kein Fehler-Toast.
- Schließen des Trailer-Dialogs entfernt das `<iframe>` komplett aus dem DOM
  (nicht nur `src` leeren) — sonst spielt YouTube im Hintergrund weiter.
- **`GET /api/metadata/{id}/trailer-stream` + `GET /api/trailer-file/{key}`
  (seit 2026-09-04, für die nativen Apple-Apps):** der Browser braucht das
  NICHT (nutzt weiterhin das iframe-Embed direkt im Client), aber tvOS hat
  gar kein WebKit und kann daher kein `<iframe>` rendern — die Apps spielen
  stattdessen per `AVPlayer` ab, der eine EINZELNE Datei-URL braucht.
  **Wichtig: reine URL-Extraktion (`yt-dlp -g`) reicht NICHT** — YouTube
  liefert inzwischen für die meisten Videos KEIN kombiniertes Video+Audio-
  Format mehr, nur getrennte "video only"/"audio only"-Streams (verifiziert
  2026-09-04 direkt im Container: `--list-formats` zeigte keinerlei
  gemuxtes Format). `internal/ytdlp.Extractor` lädt daher mit
  `-f "bv*[ext=mp4][height<=1080]+ba[ext=m4a]/…" --merge-output-format mp4`
  herunter und lässt yt-dlp intern per `ffmpeg` (bereits im Image) zu EINER
  MP4 muxen, gecached unter `ConfigDir/cache/trailers/<youtubeKey>.mp4`
  (kein Ablaufdatum wie bei den signierten googlevideo-URLs, beliebig oft
  wiederverwendbar). `/trailer-stream` stößt den Download an (blockierend,
  aber Trailer sind kurz — ein paar Sekunden bei guter Bandbreite) und
  liefert `{url: "/api/trailer-file/<key>"}` (relativ, App löst es gegen
  `client.baseURL` auf); `/trailer-file/{key}` liefert die fertige Datei per
  `http.ServeContent` (Range-fähig, wichtig für AVPlayer-Seeking) und lädt
  bei Bedarf selbst nach, falls der Cache-Eintrag fehlt.
  Docker-Image installiert `yt-dlp` via `pip3 --break-system-packages`
  (gleiches Muster wie `pgsrip`). 3 Versuche mit 5s-Pause bei HTTP 403
  (laut `~/Projekte/Tatort_Fetcher`-Erfahrung gelegentlich transient,
  besonders bei mehreren gleichzeitigen Downloads — kein harter Dauer-Block).
  **Eigene Cipher-/PO-Token-Extraktion ist NICHT reimplementierbar** (seit
  YouTubes 2024er Anti-Bot-Härtung bräuchte das einen zusätzlichen
  JS-Challenge-Solver) — `yt-dlp` wird dagegen sehr aktiv dagegen gepflegt,
  ist aber selbst NICHT unfehlbar: bei fehlschlagendem Download liefert der
  Endpoint 502, die App fällt dann auf "Trailer extern öffnen" zurück statt
  hart zu scheitern.
  **Ein eigener Live-Test von `yt-dlp -g` (reine URL, kein Mux) aus einer
  Cloud-Sandbox heraus schlug mit HTTP 403 fehl** — das führte zunächst zur
  falschen Annahme, YouTube verlange inzwischen zwingend einen PO-Token-
  Sidecar. Tatsächliche Ursache: die Sandbox-IP wird von YouTube aggressiver
  gefiltert als die Heimnetz-IP des echten Servers, UND das fehlende Muxen
  war das eigentliche Kernproblem (siehe oben). **Bei ähnlichen Fragen: aus
  dieser Sandbox heraus getestetes yt-dlp/YouTube-Verhalten ist NICHT
  repräsentativ für das Verhalten vom echten Server aus** — im Zweifel
  direkt im laufenden Container testen (`docker exec videoplayer …`, SSH
  root@<UNRAID-LAN-IP>:2202, siehe `infra_unraid.md`-Memory).

### Musik-Bibliotheken (seit 2026-09-04)
- Neuer Bibliothekstyp `kind=music` neben movies/tv/private (Admin-UI:
  Bibliothek-anlegen-Dialog + Bibliotheks-Manager-Select).
- **Metadaten-Priorität (User-Vorgabe, WICHTIG bei künftigen Änderungen):**
  eingebettete Tags (ID3/FLAC/Vorbis, via ffprobe `format.tags` — bereits
  vorher nur für `extractReleaseTime` genutzt) sind IMMER die primäre Quelle.
  MusicBrainz+Cover-Art-Archive ist NUR Fallback, wenn Tags/Cover fehlen.
- **Gruppierung ist bewusst hybrid:** Ordnerstruktur bleibt die normale
  Browse-Navigation (Musik-Items sind ganz normale `items`-Zeilen, die
  bestehende `/api/items?folder=`-Navigation funktioniert unverändert). Die
  KANONISCHE Artist/Album-Identität für die Album-Kachel-Ansicht kommt separat
  aus den Tag-Werten (`items.artist`/`items.album`), mit dem übergeordneten
  Ordnernamen als Fallback, wenn beide Tags fehlen (Store:
  `GroupMusicAlbums`, läuft am Ende jedes Musik-Library-Scans).
- **Neue Tabelle `music_albums`** (NICHT die `metadata`-Tabelle
  wiederverwendet — die ist auf TMDB-int-IDs zugeschnitten,
  `UNIQUE(tmdb_type,tmdb_id,...)`, MusicBrainz-IDs sind UUIDs und passen da
  nicht sauber rein). `items.artist/album/track_no/music_album_id` additiv.
- **Scanner** (`internal/scanner/scanner.go`): neue Extensions
  (mp3/flac/m4a/ogg/opus/wav), Tag-Extraktion in `probeItem` (Artist/Album/
  Track/Titel-Fallback), Thumbnail-Generierung übersprungen (macht bei Audio
  keinen Sinn), stattdessen `extractAlbumCovers` — EINMAL pro Album (nicht
  pro Track!) das eingebettete Cover per `ffmpeg -an -vcodec copy` aus der
  ersten Track-Datei ziehen. Cache-Konvention identisch zu Postern
  (`Server.PosterDir`, Dateiname `album_<id>.jpg`).
- **Enrichment-Fallback** (`internal/enrich/music_worker.go` +
  `internal/musicbrainz/client.go`): bewusst ein KOMPLETT eigenständiger
  Worker, NICHT in `enrich.Worker` (TMDB/OMDb) integriert — der hält konkrete
  `*tmdb.Client`/`*omdb.Client`-Felder ohne Abstraktionsgrenze. MusicBrainz +
  Cover Art Archive sind beide kostenlos, kein API-Key nötig, aber
  MusicBrainz verlangt einen aussagekräftigen `User-Agent` + max. 1 req/s
  (eigener Rate-Limiter, NICHT den TMDB-Limiter mitbenutzen). Läuft nur für
  Alben, deren `cover_source` nach der Scanner-Extraktion noch leer ist.
- **`PendingMusicMetadataAlbums` trägt bewusst KEINEN `artist != album`-
  Ausschluss** (anders als `PendingMusicAlbums`/Cover-Suche, wo er sinnvoll
  ist) — selbstbetitelte Alben („Aerosmith" von Aerosmith) sind ein normaler,
  häufiger Fall und waren dadurch bis 2026-09-08 dauerhaft und lautlos von der
  MusicBrainz-Genre/Jahr-Suche ausgeschlossen (69 Alben betroffen).
- **Genre/Jahr-Backfill (seit 2026-09-06, LIVE 1.2.1, User-Wunsch: "Viele
  Titel haben zum Beispiel kein Genre"):** zweite, unabhängige Worker-Phase
  `runMetadataPhase` (`internal/enrich/music_worker.go`) NEBEN der
  bestehenden Cover-Phase — eigenes Gate (`music_albums.metadata_fetched_at`,
  analog `cover_source`), weil die meisten Alben ihr Cover schon lokal per
  eingebettetem Bild bekommen (Scanner `extractAlbumCovers`) und MusicBrainz
  für Cover dadurch oft NIE aufgerufen wird, obwohl Genre/Jahr trotzdem
  fehlen können. `Store.PendingMusicMetadataAlbums` liefert Alben mit
  `metadata_fetched_at IS NULL AND (genre='' OR year=0)` — Alben, die aus
  den Tags bereits beides haben, tauchen nie auf (**Tags bleiben primäre
  Quelle**, MusicBrainz überschreibt in `Store.ApplyMusicBrainzMetadata`
  NIE einen vorhandenen Wert). Ein gefundenes Genre wird zusätzlich auf
  Tracks OHNE eigenes Genre-Tag propagiert (`items.genre = ''` ist dort ein
  zuverlässiges "fehlt"-Signal). **Bewusst KEINE Propagation auf
  `items.released_at`** — das füllt der Scanner IMMER mit mindestens der
  Datei-mtime (`extractReleaseTime`-Fallback), ist also NIE wirklich leer;
  eine MB-Jahr-Schreibung dorthin würde echte Tag-Daten mit einer
  bedeutungslosen Kopierdatum-Fiktion verwechseln lassen. Jahr existiert
  stattdessen nur auf `music_albums.year` (Spalte war seit der ursprünglichen
  Musik-Feature-Runde im Schema, aber nie beschrieben — `GroupMusicAlbums`
  fasst nur Artist/Album/Genre zusammen, nie Jahr). `musicbrainz.Client
  .LookupReleaseGenres` (`inc=genres`-Lookup, eigener Call neben der Suche)
  liefert das Genre mit den meisten MusicBrainz-Stimmen, title-cased
  ("hard rock" → "Hard Rock", angeglichen an die Groß-/Kleinschreibung
  eingebetteter Tags). **Batch-Looping** (`musicPhaseBatchLimit=50`,
  `musicPhaseMaxBatches=200`): beide Phasen laufen pro Worker-Zyklus so oft
  in 50er-Batches durch, bis die Pending-Query leer ist (statt nur 50 Alben
  alle 30 Min) — bei tausenden Alben mit fehlendem Genre (Erstlauf nach
  diesem Feature: 2725 von 2742 Alben in der Musik-Bibliothek des Users)
  wäre die alte 30-Min-Kadenz untragbar langsam gewesen.
- **Playback** (`internal/playback/decider.go`+`ffmpeg.go`): eigener
  Audio-Only-Zweig VOR den Video-Codec-Checks (kein `VideoCodec` gesetzt) —
  mp3/aac/vorbis/opus spielt jeder Browser nativ (Direct Play), alles andere
  (flac/wav/…) wird zu AAC/HLS transcodiert, dabei komplett ohne
  Video-Filter/Hwaccel-Init.
- **API**: `GET /api/libraries/{id}/albums`, `GET /api/albums/{id}`
  (Album-Detail + Tracks sortiert nach `track_no`), `GET /api/poster/album/{id}`
  (Cover, gleiches Handler-Muster wie `getPoster`). Der bestehende
  `/api/items?libraryId=`-Endpoint liefert Tracks für die normale
  Ordner-Browse-Ansicht bereits generisch mit.
- **Frontend:** `.card--square` (quadratisches Cover, analog `.card--poster`)
  für Musik-Kacheln. Musik-Library-Root zeigt Album-Kacheln
  (`views.js renderAlbumTiles`/`renderAlbumTracks`, `state.currentAlbum`)
  statt der normalen Ordner-Ansicht — Unterordner-Navigation bleibt trotzdem
  unverändert nutzbar (Hybrid-Vorgabe).
- **Persistenter Mini-Player** (`music.js`, neues Modul, letzte Position vor
  `app.js` in der Lade-Reihenfolge): `#miniPlayer` sitzt als Geschwister von
  `#playerDialog` AUSSERHALB von `#grid` im DOM — übersteht dadurch jeden
  `loadItems()`/View-Wechsel unverändert (User-Vorgabe: "wie Spotify/YouTube
  Music", nicht nur ein angepasster Modal-Dialog). Eigener, unsichtbarer
  Video.js-Player (`musicState.vjs`, NICHT `state.vjs` — der gehört dem
  normalen Video-Modal und wird bei `disposePlayer()` verworfen) für
  HLS-Transcode-Support bei flac/wav, exakt wie der Hauptplayer. Klick auf
  eine Musik-Kachel ruft `musicPlayAlbum()` auf statt `openDetail()` zu
  öffnen — Queue kommt aus `state.playQueue`, das seit dem Fix unten in JEDEM
  Render-Pfad (Album-Ansicht, normale Ordner-Navigation, Suche) auf die
  gerade angezeigte Liste gesetzt wird, nicht nur in der Album-Ansicht.
- **`state.playQueue` muss in JEDEM Render-Pfad gesetzt werden**, nicht nur in
  der Album-Ansicht — `grid.js` setzt es bei jedem generischen Render
  (`searching ? items : merged`). Fehlte das, fiel `cards.js` auf die alte
  Queue des zuletzt geöffneten Albums zurück und spielte bei einem Suchtreffer
  den falschen Track (`indexOf` findet ihn nicht → -1 → Index 0).
- **Mini-Player: `musicState.playSeq`-Sequenz-Token + `vjs.ready()`-Wrapper** —
  ohne Token überschreiben sich zwei überlappende `musicPlayCurrent()`-Aufrufe
  (Doppelklick) gegenseitig mit `src()`/`play()`, Video.js bricht den laufenden
  Ladevorgang mit einem lautlos verschluckten `AbortError` ab. Ohne `ready()`
  laufen `src()`/`play()` auf einer frisch erzeugten Instanz ins Leere (Tech
  noch nicht initialisiert). Gleiches Muster wie `state.loadSeq` in `grid.js`.
- **Auflösungs-Badge unterdrückt** (`cards.js`, `isMusicLib` → `res = ""`) —
  ein Video-Konzept, für Audio-Dateien bedeutungslos/irreführend (zeigte z. B.
  "360p" auf Musik-Kacheln).
- **Bibliotheks-Zähler zeigt "N Titel · M Alben" statt "N Videos"** für
  `kind=music` (`views.js loadCount`, neben dem Bibliotheksnamen im
  Breadcrumb) — Album-Anzahl seit 2026-09-07 ergänzt (User-Wunsch: "nicht
  nur die Anzahl der Titel, auch der Alben"). Eigener Store-Query
  `Store.CountMusicAlbums` (gleiche WHERE-Klausel wie
  `ListMusicAlbumsFiltered`, nur ohne die Zeilen zu laden) statt
  `folderCount` (TV/Movies) wiederzuverwenden — das zählt physische
  Top-Level-Ordner, was bei der hybriden Musik-Album-Gruppierung
  (`GroupMusicAlbums`) NICHT der kanonischen Album-Anzahl entspricht.
  `GET /api/libraries/{id}/stats` liefert `albumCount` nur auf der
  Library-Root (`folder=""`, wie `folderCount` bei TV auch) — der Query
  ist library-weit, nicht pro Unterordner scoped.
- **`.m4b` als Hörbuch-Extension ergänzt** (`scanner.go` `supportedExt` +
  `musicExt`) — fehlte komplett, Hörbücher (z. B. David-Baldacci-Serien, i. d.
  R. `.m4b`) wurden dadurch vom Scanner GAR NICHT erst eingelesen (User-
  Bericht 2026-09-04: "finde sie über die Suche nicht" — sie waren nie in
  der DB gelandet, kein reines Such-Problem).
- **⚠ Cover-Art-Streams sind KEIN Video** — MP3/FLAC/M4A mit eingebettetem Bild
  liefern in ffprobe einen ZUSÄTZLICHEN „video"-Stream
  (`disposition.attached_pic=1`, meist mjpeg/png, 1 Frame). `Scanner.probeItem`
  überspringt die beim Setzen von VideoCodec/Width/Height. Sonst hält
  `playback.Decide()` die Datei für ein Video und erzwingt einen für ein
  Einzelbild sinnlosen HLS-Transcode (lange Startverzögerung + „Failed to set
  MediaSource duration"). Backfill: `music_cover_art_videocodec_fix_v1`.
  Derselbe Fallstrick auf ffmpeg-Seite: siehe `-map 0:V:0` unter „Playback".
- **⚠ `mimeForExt()` (`internal/api/stream.go`) muss jede Audio-Extension
  kennen** — beim Fallback `application/octet-stream` verweigert der Browser
  die Dekodierung eines `<video>`/`<audio>`-Elements komplett (kein
  MIME-Sniffing für Medienelemente): `readyState=0` für immer, kein
  Fehler-Event, kein Timeout, einfach dauerhaft „lädt". Abgedeckt sind
  mp3/m4a/m4b/aac/ogg/opus/wav/flac.
- Nebenbei auch behoben: der Mini-Player übergab Direct-Play-Tracks fest mit
  `type="video/mp4"` an Video.js (aus dem Hauptplayer kopiert, dort korrekt
  für echte mp4-Videos) — jetzt `musicDirectMimeType(container)` (mp3→
  audio/mpeg, m4a/m4b→audio/mp4, ogg/opus→audio/ogg, wav→audio/wav). War ein
  echter, aber sekundärer Bug — die Cover-Art-Transcode-Fehlklassifikation
  oben war die eigentliche Hauptursache.
- **Suche findet jetzt auch Künstler + Album** (`ListItems`-Suchklausel um
  `OR i.artist LIKE ? OR i.album LIKE ?` erweitert) — vorher wurde nur
  `i.title`/`m.title` durchsucht, eine Suche nach dem Interpreten-/Autoren-
  Namen (z. B. "Baldacci") lief für Musik/Hörbücher komplett ins Leere.
- **Sortierung nach Künstler/Album** (`ListItems`-Sort-Switch, neue Fälle
  `"artist"`/`"album"`, COLLATE NATSORT + `track_no` als Sekundärschlüssel).
- **Sort-/Filter-Felder sind jetzt bibliothekstyp-spezifisch** (`grid.js`,
  User-Vorgabe 2026-09-04: "sollen nur dafür spezifische Felder zeigen"):
  jede `<option>` im Sort-Dropdown trägt `data-kinds="movies,tv,private,music"`
  — passt die aktuelle Library-`kind` nicht, wird die Option per `opt.hidden`
  ausgeblendet (ohne aktive Library, z. B. Home/Sammlungen, bleibt alles
  sichtbar). Musik blendet zusätzlich Auflösungs-/Gesehen-/Bewertungs-Filter
  komplett aus (`#resolutionFilterLabel`/`#watchedFilterLabel`/
  `#ratingFilterLabel`, per `.hidden`-Klasse) — alle drei sind Video-Konzepte
  bzw. (Gesehen) für Musik bewusst nicht geführt.
- **Listenansicht** (`#musicListViewBtn`, global per `localStorage`
  `musicListView` persistiert wie `flatView`): schaltet Album-Übersicht UND
  Album-Detail von Kacheln auf kompakte Zeilen um (`views.js`
  `renderAlbumTiles`/`renderAlbumTracks` bekommen ein `listView`-Flag,
  gemeinsamer Zeilen-Renderer `renderMusicTrackRow(item, queue, idx, columns)`
  mit konfigurierbaren Spalten). `#grid.track-list-grid` schaltet das sonst
  als CSS-Grid layoutete `#grid` für den Listenfall auf Block-Layout um.
- **"🎵 Alle Titel"-Ansicht** (`#musicAllTracksBtn`, NUR Listendarstellung,
  kein Kachel-Äquivalent): flache Liste ALLER Tracks der Bibliothek
  (Künstler/Album/Titel/Zuletzt-gehört-Spalten, `renderAllTracksList` in
  `views.js`) über den ganz normalen `/api/items?libraryId=`-Endpoint — dafür
  musste `model.Item` um `LastPlayedAt` (aus `user_item_state.last_played_at`,
  bisher nur serverseitig für den `sort=played`-Filter genutzt, nie ins JSON
  exponiert) erweitert werden, `ListItems`-SELECT+Scan entsprechend ergänzt.
- **„Nur Favoriten" zeigt im Album-Root gefilterte ALBEN**
  (`user_music_album_favorites`), nur bei explizit aktiviertem „Alle Titel"
  gefilterte Tracks — Album-Favoriten wären sonst über den Filter nie
  auffindbar.
- **⚠ Drei Fallen in den Musik-Listenzeilen** (alle 2026-09-04 gefixt):
  `ListMusicAlbumTracks` braucht den `user_item_state`-JOIN (sonst springt ein
  favorisierter Track beim nächsten Album-Fetch zurück, obwohl die DB stimmt);
  `.track-row-fav.fav-toggle` muss das von der generischen Kachel-Overlay-
  Klasse `.fav-toggle` geerbte `position:absolute` explizit auf normalen
  Inline-Fluss zurücksetzen; die Grid-Kinder brauchen `min-width:0` (implizit
  `auto` = Inhaltsbreite bei `white-space:nowrap`, sprengt sonst die
  1fr-Spalte). Bulk-Auswahl braucht `.track-row-select` analog `.card-select`.
- **Genre läuft über `items.genre` als Zwischenlager** — `GroupMusicAlbums`
  aggregiert `MAX(genre)` pro Gruppe und schreibt auch NACHTRÄGLICH nach (nicht
  nur `ON CONFLICT DO NOTHING` beim ersten Anlegen). `music_albums.genre` stand
  bis 2026-09-05 im Schema, wurde aber vom Scanner nie befüllt. **Bereits
  gescannte Dateien brauchen einen `force=true`-Rescan**, damit ffprobe das Tag
  nachliefert.
- **Album-Gruppierung läuft PRIMÄR über den physischen Elternordner**
  (`musicGroupKey` in `Store.GroupMusicAlbums`), nicht über das rohe
  `(artist,album)`-Tag-Paar: bei Compilations/Musicals/Klassik enthält das
  `artist`-Tag pro Track eine ANDERE Kombination aller Beteiligten und ist
  strukturell unbrauchbar zum Gruppieren („Das Phantom der Oper" zerfiel sonst
  in eine Kachel pro Track). `canonicalAlbumFields` bestimmt daraus genau EINEN
  Artist-/Album-/Genre-Wert für die ganze Ordner-Gruppe: uneinheitlicher Artist
  → **„Verschiedene Interpreten"** statt eines zufällig gewinnenden
  Einzelnamens; kein Album-Tag in der Gruppe → letzter Ordnername als Titel.
  Dateien direkt im Bibliotheks-Root (kein gemeinsamer Ordner zum Bündeln)
  behalten bewusst das alte reine Tag-Verhalten, damit unabhängige lose Singles
  nicht zusammengeworfen werden. **Kein Rescan nötig** — arbeitet rein auf den
  bereits in der DB stehenden Werten, jeder (auch inkrementelle) Musik-Scan
  ruft `GroupMusicAlbums` am Ende auf. Tests: `music_grouping_test.go`.
- **`GroupMusicAlbums` räumt verwaiste `music_albums`-Zeilen aktiv weg**
  (`DELETE ... WHERE id NOT IN (SELECT DISTINCT music_album_id FROM items …)`),
  `ListMusicAlbums` filtert zusätzlich per `EXISTS` auf vorhandene Tracks —
  sonst erscheinen sie als sichtbare „0 Titel"-Kacheln und wachsen über jeden
  Rescan/Algorithmus-Wechsel weiter an (Favoriten darauf verschwinden per
  `ON DELETE CASCADE` mit). Test: `TestGroupMusicAlbumsCleansUpOrphanedAlbums`.
- **Lektion:** meldet der User zu einem Fix „hat nicht geklappt", zuerst mit
  LIVE-Daten prüfen, welchen Tag-Wert die Datei tatsächlich trägt — nicht eine
  zweite Vermutung auf die erste stapeln. Die erste Diagnose („fehlendes
  `album_artist`-Tag") war plausibel, aber für diese Dateien schlicht falsch.
  Chronik beider Runden: DECISIONS.md.
- **Künstler in der Album-Detail-Listenansicht** (seit 2026-09-06,
  User-Wunsch): `renderMusicTrackRow`-Spalten für die Album-Track-Liste
  (`views.js`) sind jetzt `["track","title","artist","duration","fav"]`
  statt ohne Artist-Spalte — bei "Verschiedene Interpreten"-Alben (siehe
  oben) war der tatsächliche Interpret pro Track sonst nirgends in dieser
  Liste sichtbar. CSS-Grid-Template der Zeile (`.track-row:has(.track-row-
  duration)` in `style.css`) entsprechend um eine Spalte erweitert
  (`32px 1.5fr 1fr 60px 32px`).
- **Spalten-Breite + -Reihenfolge frei konfigurierbar (seit 2026-09-06,
  User-Wunsch)**: gilt für beide Listen-Kontexte — Album-Detail-Liste
  (Titel/Künstler/Dauer) und "🎵 Alle Titel" (Titel/Künstler/Album/Zuletzt
  gehört). Nur die "echten" Text-Spalten sind betroffen — Track-Nummer,
  Cover-Thumbnail und der Favoriten-Button bleiben an fester Position (reine
  Icon-Slots, keine Daten-Spalten). `MUSIC_LIST_CONTEXTS` (`views.js`)
  definiert pro Kontext `fixedLeading`/`reorderable`/`fixedTrailing` +
  Default-/Min-Breiten; Persistenz in `localStorage` unter
  `musicColumns:album`/`musicColumns:all` (`{order, widths}`). Beide Listen
  bekamen dafür eine echte Kopfzeile (`renderMusicColumnHeader`) — die
  Album-Detail-Liste hatte bisher GAR keine Kopfzeile, „Alle Titel" hatte
  eine rein statische. **Resize**: `mousedown` auf `.col-resize-handle`
  (rechter Zellrand) trackt `mousemove` bis `mouseup`, schreibt die neue
  Breite direkt in `localStorage` und ruft `applyMusicGridTemplate()` — setzt
  das berechnete `grid-template-columns` per Inline-Style auf Kopf- UND alle
  Datenzeilen (schlägt die `:has()`-CSS-Fallback-Regeln, die nur für den
  allerersten Sync-Render vor JS-Zugriff greifen). **Reorder**: natives
  HTML5-Drag&Drop auf den Kopfzellen (`draggable=true`,
  dragstart/dragover/drop), verschiebt die Spalte in der `order`-Liste und
  triggert einen kompletten Rebuild von Kopfzeile + allen Zeilen
  (`musicColumnHeaderRefreshers`-WeakMap pro Listen-Container) — nötig, weil
  die Reihenfolge nicht nur das CSS-Raster betrifft, sondern auch welcher
  Inhalt in welcher Zellen-Position im HTML steht (`renderMusicTrackRow`
  baut die Zeile in `columns`-Array-Reihenfolge). Album-Übersicht als Liste
  (`track-row--album`, Cover+Album+Artist+Trackzahl+Fav) war anfangs NICHT
  betroffen — User-Wunsch bezog sich zunächst erkennbar auf die Track-Listen
  ("Titel, Künstler, Dauer"), nicht die Album-Kacheln/-Zeilen selbst.
  **✅ Nachgezogen (2026-09-11, LIVE 1.3.7, User-Report: "Hier fehlen die
  Überschriften der Spalten und die Spalten sind nicht verschiebbar, so wie
  bei den Titeln"):** eigener vierter Kontext `overview` in
  `MUSIC_LIST_CONTEXTS` (Spalten Album/Künstler/Genre/Titelzahl,
  `musicColumns:overview` in localStorage) + eigener Zeilen-Renderer
  `renderAlbumRow(a, columns)` (analog `renderMusicTrackRow`, aber mit
  Album-eigenen Feldern statt Track-Feldern — kein `trackNo`/Dauer). Nutzt
  dieselbe `renderMusicColumnHeader`/`wireMusicColumnHeader`/
  `applyMusicGridTemplate`/`musicColumnHeaderRefreshers`-Infrastruktur wie
  die beiden Track-Listen, keine Server-Änderung nötig. Header-Element
  bekommt zusätzlich die Klasse `.track-row--album`, damit das bestehende
  CSS-Fallback-Grid-Template (`40px 2fr 1fr 1fr 80px 32px`) auch für die
  Kopfzeile vor dem ersten JS-Zugriff greift.
  **⚠ Resize-Handle und Reorder dürfen nicht mit nativem HTML5-DnD gemischt
  werden** (gefixt 2026-09-11): ein `mousedown` auf einem Kind INNERHALB einer
  `draggable="true"`-Zelle wird vom Browser als Drag-Kandidat des Elternteils
  gewertet und unterdrückt danach reguläre `mousemove`-Events komplett — selbst
  `draggable="false"` auf dem Handle half nicht. Reorder läuft deshalb über
  dasselbe reine mousedown/mousemove/mouseup-Tracking wie Resize
  (`REORDER_THRESHOLD` 4px). Zweite Ursache: der Handle lag als
  `position:absolute; right:-6px` im `gap` zwischen den Spalten, wo der
  Head-Container über ihm lag — jetzt normales Flex-Kind (`flex:0 0 10px`) im
  Zellfluss. **Mit echten OS-Mausereignissen testen** — synthetische
  `dispatchEvent`-Aufrufe lösen kein natives Drag aus und verdecken den Bug.
- **Genre-Spalte in beiden Track-Listen** (seit 2026-09-06, User-Wunsch:
  "IN Der Musikansicht fehlt mir Genre noch in der Listenansicht als
  Spalte"): `MUSIC_LIST_CONTEXTS` (`album`/`all`) um `genre` als weitere
  `reorderable`-Spalte ergänzt, `renderMusicTrackRow` bekam den passenden
  `case "genre"`. Voraussetzung: `model.Item.Genre` trug bis dahin
  `json:"-"` (reines internes Zwischenlager für `GroupMusicAlbums`, nie an
  den Client geliefert) — jetzt `json:"genre,omitempty"`, und sowohl
  `ListItems` als auch `GetItemFor` SELECTen `i.genre` jetzt mit (vorher
  fehlte es in beiden SQL-Queries).
  **⚠ Musik-Item-Felder haben DREI unabhängige SELECTs** — `ListItems`,
  `GetItemFor` UND `Store.ListMusicAlbumTracks` (`GET /api/albums/{id}`, die
  tatsächliche Datenquelle der Album-Detail-Trackliste). Ein neues Feld muss in
  alle drei; die dritte wurde beim Genre schon einmal übersehen (Spalte blieb
  leer, obwohl `/api/items?genre=` korrekt lieferte). Test:
  `TestListMusicAlbumTracksIncludesGenre`.
- **Jahr-Spalte + -Feld (seit 2026-09-06, User-Wunsch: "in der Musik
  Listenansicht und in dem Metadaten Formular noch das Jahr ergänzen")**:
  eigene Spalte `items.year INTEGER NOT NULL DEFAULT 0`, exakt wie `genre`
  aus den Tags gelesen (`lookupTag(..., "date", "year", "originaldate",
  "TYER", "TDRC")`, erste 4 Ziffern per Regex — "date" liefert oft ein
  volles Datum wie "2021-05-01"). `year` als weitere `reorderable`-Spalte
  in `MUSIC_LIST_CONTEXTS` (`album`/`all`), Edit-Dialog-Feld `musicYear`
  (1900–2099) neben Genre. `Store.UpdateMusicItemMetadata` schreibt `year`
  direkt auf `items.year`; `year=0` (leeres Feld) lässt den Wert
  unverändert (0 ist kein gültiges Jahr, sondern "unbekannt/unverändert").
  `GroupMusicAlbums`/`canonicalAlbumFields` aggregieren `year` genau wie
  `genre` aufs Album (erster nicht-0-Wert der Gruppe gewinnt), die
  `music_albums`-Upsert-Klausel überschreibt einen bereits gesetzten
  Album-Jahreswert NIE (`year = CASE WHEN music_albums.year = 0 AND
  excluded.year != 0 THEN excluded.year ELSE music_albums.year END`) —
  damit bleibt ein per MusicBrainz-Backfill (siehe oben) bereits gesetztes
  Jahr erhalten, falls ein späterer Rescan keine Tag-Jahr-Angabe findet.
  **⚠ Jahr NIEMALS über `items.released_at`** — das füllt der Scanner IMMER
  mindestens mit der Datei-mtime (`extractReleaseTime`-Fallback) und zeigte
  dadurch bei praktisch jedem frisch gescannten Track das Kopierdatum statt des
  Erscheinungsjahrs („An Innocent Man" [1983] → „2026"). Deshalb die eigene,
  zuverlässig unterscheidbare Spalte `items.year` mit `0` = „kein Jahr-Tag".
- **Musik-Metadaten bearbeiten (seit 2026-09-06, User-Wunsch: "Bei Musik
  fehlt grundsätzlich noch, die Metadaten zu bearbeiten")**: der ✏-Edit-
  Dialog war zwar für Admins auch bei Musik-Tracks sichtbar, zeigte aber nur
  die Film/Serien-Felder (Jahr/Beschreibung/TMDB-Rating/FSK/…), die für
  Musik weder passen noch etwas bewirkt hätten — Speichern lief über
  `POST .../metadata-manual`, das eine `tmdb_type=custom`-**metadata**-Zeile
  anlegt, während Musik-Tracks ihre Felder direkt auf `items` tragen
  (`artist`/`album`/`track_no`/`genre`) und nie mit dem TMDB/metadata-
  Konzept arbeiten. Komplett eigener Pfad: `index.html` bekam vier neue
  Felder (Künstler/Album/Track-Nr./Genre, Klasse `.editmeta-music-field`),
  die bestehenden Film-Felder eine Gegenklasse `.editmeta-movie-field` —
  `openEditMetaDialog()` togglet beide Gruppen per `kind==="music"` und
  befüllt bei Musik direkt aus `it.artist/album/trackNo/genre` (unabhängig
  von `it.metadataId`, das bei Musik immer 0 ist). `handleEditMetaSubmit`
  ruft bei Musik `PUT /api/items/{id}/music-metadata`
  (`Store.UpdateMusicItemMetadata`, `internal/store/music.go`) statt des
  TMDB-Pfads — schreibt `items.title/artist/album/track_no/genre` direkt
  und stößt danach `GroupMusicAlbums(libraryID)` erneut an, weil ein
  geänderter Artist/Album-Wert die Album-Zuordnung dieses Tracks ändern
  kann. Poster-Button ist bei Musik ausgeblendet (kein Item-eigenes Poster,
  nur das Album hat ein Cover — eigener, hier nicht betroffener Mechanismus
  über `/api/poster/album/{id}`). Test:
  `internal/store/music_edit_metadata_test.go`.
  **✏-Overlay-Button, admin-only, NUR bei Musik-Items** — in der Kachel-Ansicht
  (`.edit-toggle`, `top:66 left:6`, cards.js) UND am Zeilenende beider
  Track-Listenansichten (`editMeta`-Slot in
  `MUSIC_LIST_CONTEXTS.fixedTrailing`). Nötig, weil ein Klick auf eine
  Musik-Kachel/-Zeile IMMER `musicPlayAlbum()` ruft und NIE `openDetail()` —
  ohne eigenen Button wäre der fertige Dialog gar nicht erreichbar gewesen.
  Beide Handler setzen `state.currentItem` + rufen `openEditMetaDialog()`, mit
  `stopPropagation()` gegen das sonst auslösende Abspielen.
  `.track-row-edit.edit-toggle` muss wie `.track-row-fav.fav-toggle` das
  geerbte `position:absolute` zurücksetzen.
  **Nach dem Speichern nur `loadItems()` + Toast, NIE `openDetail()`** — sonst
  öffnet sich der Video-Detail-Dialog direkt über dem gerade geschlossenen
  Edit-Dialog, für den User nicht von „schließt nicht" zu unterscheiden.
  **✅ Album-Metadaten bearbeiten (seit 2026-09-11, LIVE 1.3.8, User-Wunsch:
  „Wenn ich beim Album das Jahr zum Beispiel eintrage, dann soll es natürlich
  auch für die Titel übernommen werden"):** Alben bleiben ein reines Aggregat
  (`GroupMusicAlbums`/`canonicalAlbumFields`), es gibt also keine eigene
  Album-Zeile zum Editieren — stattdessen schreibt
  `Store.UpdateMusicAlbumMetadata(albumID, artist, album, genre, year)`
  (`internal/store/music.go`) die vier Felder per
  `UPDATE items ... WHERE music_album_id = ?` auf ALLE Tracks des Albums
  gleichzeitig (`year=0` lässt das Jahr unverändert, wie beim Track-Edit) und
  stößt danach `GroupMusicAlbums` erneut an. Endpoint
  `PUT /api/albums/{id}/metadata` (admin-only, `internal/api/music.go
  updateMusicAlbumMetadata`, prüft `requireLibAccess` über die Library des
  Albums). Frontend: ✏-Button im Album-Detail-Header (neben ♥, admin-only)
  öffnet `#editAlbumMetaDialog` (`openEditAlbumMetaDialog`/
  `handleEditAlbumMetaSubmit` in `music.js`) — eigener Dialog/Speicherpfad,
  getrennt vom Track-Edit, weil es serverseitig kein Album-Metadaten-Objekt
  gibt. Bewusst nur EIN Einstiegspunkt (kein Button auf der Album-Kachel).
  **`GroupMusicAlbums` propagiert Album-Jahr/-Genre auf Tracks ohne eigenes
  Tag** (`year = 0`/`genre = ''`) und liest dafür nach dem Upsert den AKTUELLEN
  Album-Wert — deckt so auch ein per MusicBrainz oder manuellem Album-Edit
  gesetztes Jahr/Genre ab. Ohne das zeigte ein Track ohne Jahr-Tag dauerhaft
  „—", obwohl der Album-Header korrekt ein Jahr anzeigte. Läuft bei jedem (auch
  inkrementellen) Scan, kein `force=true` nötig.
- **IMDb-Zuordnung bei Episoden: Season/Episode kommen PRIMÄR aus den
  Formularfeldern** (`#matchSeason`/`#matchEpisode`), das Datei-Parsing ist nur
  Fallback — bei obfuskierten Dateinamen ohne SxxExx-Muster war jede manuelle
  Eingabe dort vorher wirkungslos (`handleMatchImdb` parste stur nochmal aus
  `item.title`). Serverseitig löst `setItemMetadata` bei `tmdbType=episode` die
  Show-ID zuerst über `client.FindByIMDb` auf (TMDB liefert für eine
  Folgen-IMDb-ID `tv_episode_results[0].show_id`, die Parent-Show) und ruft
  dann denselben `FetchEpisodeMetadata(showID, season, episode)` wie der
  numerische Pfad. Kein OMDb-Fallback hier — OMDb kennt kein Season/Episode.
#### Listenspalten "Zuletzt abgespielt"/"Wiedergaben"/"Hinzugefügt" + Spalten-Auswahl (seit 2026-09-14, LIVE 1.3.26)

User-Wunsch: dieselben drei Zeit-/Zähler-Spalten für Alben UND Titel in den
Musik-Listenansichten, dazu ein Dropdown zum Ein-/Ausblenden einzelner
Spalten — **Browser, Mac, Linux** (explizit NICHT iOS/tvOS, dort bleibt die
Musik-UI unverändert bewusst schlank).

- **Neue Server-Datenspalte `user_item_state.play_count`** (Wiedergabezähler
  pro User+Item). `Store.TouchLastPlayed` (aufgerufen von
  `POST /api/items/{id}/played`, das JEDER Client beim Öffnen des Players
  bereits ruft — derselbe, längst universelle Mechanismus hinter "Zuletzt
  abgespielt") zählt `play_count` im selben UPSERT hoch, kein neuer
  Aufrufpfad für irgendeinen Client nötig.
  `model.Item.PlayCount`/`model.MusicAlbum.PlayCount` (Album = `SUM` über
  alle Tracks) neu, zusammen mit `model.MusicAlbum.AddedAt` (`MIN` über alle
  Tracks — wann der erste Titel des Albums in die Bibliothek kam) und
  `model.MusicAlbum.LastPlayedAt` (`MAX` über alle Tracks, analog zum
  bereits bestehenden `Item.LastPlayedAt`). Ergänzt in `ListItems`
  (`items.go`), `ListMusicAlbumTracks` UND `ListMusicAlbumsFiltered`
  (`music.go`, drei unabhängige SELECTs — siehe frühere Bugs an genau dieser
  Stelle, z. B. "Genre-Spalte blieb leer", immer weil eine der drei
  Datenquellen beim ersten Anlauf übersehen wurde).
  **⚠ `MAX(...)` über eine DATETIME-Spalte liefert bei `modernc.org/sqlite`
  einen rohen `time.Time.String()` INKLUSIVE Monotonic-Clock-Suffix
  (`"… m=+0.098136418"`)** — anders als ein direkter Spaltenzugriff, und von
  keinem `parseDBTime`-Layout matchbar. `parseDBTime` (`sqlite.go`) schneidet
  den `" m=…"`-Teil deshalb vorab ab. Tests: `internal/store/play_count_test.go`.
- **Browser (`music.js`):** `MUSIC_LIST_CONTEXTS` (Album-Übersicht/
  Album-Detail/"Alle Titel") bekommen `lastPlayed`/`playCount`/`added` als
  weitere `reorderable`-Spalten (Labels "Zuletzt gehört"/"Wiedergaben"/
  "Hinzugefügt"). Neuer `defaultVisible`-Schlüssel pro Kontext: die drei
  neuen Spalten sind NICHT automatisch für jeden sichtbar (User-Vorgabe,
  implizit — neue Spalten sollen nicht ungefragt überall auftauchen),
  sondern nur über den neuen **"☰ Spalten"-Dropdown** zuschaltbar
  (`#musicColumnsBtn`/`#musicColumnsDropdown` in `index.html`, gleiches
  Öffnen/Schließen-Muster wie der bestehende Genre-Filter-Dropdown).
  Sichtbarkeit als explizite Allowlist in `localStorage`
  (`musicColumns:<context>.visible`, gemerged ins selbe Objekt wie
  `order`/`widths` — `saveMusicColumnLayout`/`saveMusicColumnVisible` schreiben
  beide non-destruktiv in dasselbe Storage-Objekt). Der Button selbst wird
  NICHT statisch ein-/ausgeblendet, sondern zentral: `loadItemsBody()`
  (`grid.js`) versteckt ihn bei jedem Render-Durchlauf standardmäßig,
  `renderMusicColumnHeader()` (`music.js`) zeigt ihn nur wieder, wenn dieser
  Durchlauf tatsächlich eine Spalten-Kopfzeile gerendert hat — dadurch bleibt
  die Sichtbarkeit exakt an das reale Vorhandensein einer Spalten-Ansicht
  gekoppelt, unabhängig davon, über welchen der drei Wege (Album-Übersicht
  als Liste, "Alle Titel", Album-Detail) man dorthin kam.
- **Nicht angefasst:** das bestehende Reorder-Drag (`wireMusicColumnHeader`)
  arbeitet über `order.indexOf(spaltenName)` auf dem VOLLEN Order-Array
  (inkl. gerade ausgeblendeter Spalten) — unabhängig davon, welche Spalten
  im Header aktuell sichtbar gerendert sind. Ausblenden/Wiedereinblenden
  einer Spalte verliert dadurch nie ihre zuvor gewählte Position.

#### Sortierung per Klick auf die Spaltenüberschrift (seit 2026-09-13)

- User-Wunsch: "warum klappt die Sortierung der Spalten nicht so, wie in
  der Linux App, indem man auf den Kopf der Spalte klickt" — GoldfishLinux
  nutzt für dieselbe Listenansicht ein natives `Gtk.ColumnView`, dessen
  Spalten von Haus aus per Klick auf die Überschrift sortieren
  (`ColumnSpec.sort_key` in `widgets/column_list.py` im Linux-Repo). Der
  Browser hatte für seine (nachgebaute) Spalten-Kopfzeile bisher NUR
  Resize+Reorder, keinerlei Klick-Sortierung.
- `MUSIC_LIST_CONTEXTS[context].valueOf` (neu, `music.js`) — pro
  reorderable Spalte ein Getter `(row) => wert`, analog zu `sort_key` dort
  (Album-Übersicht liest vom Album-Objekt, Album-Detail/„Alle Titel" vom
  Track-Objekt). `sortTypes` markiert numerische/Datums-Spalten (Rest =
  Text, `localeCompare` mit `sensitivity:"base"`). Fixe Icon-Slots (cover/
  track/fav/editMeta/delete) haben keinen Getter → nicht sortierbar, wie
  bei der Linux-App der ebenfalls fixen `actions`-Spalte.
- `loadMusicSort`/`saveMusicSort` persistieren Spalte+Richtung im selben
  `musicColumns:<context>`-localStorage-Objekt wie Order/Widths/Visible
  (`sortCol`/`sortDir`, non-destruktiv gemergt). `musicSortRows(context,
  rows)` sortiert das übergebene Array **in place** — bewusst, weil
  dieselbe Array-Referenz auch als `state.playQueue`/
  `state.lastRenderedItems` dient: die Wiedergabe-Reihenfolge (Shuffle-
  Next etc.) folgt dadurch automatisch der sichtbaren Sortierung, exakt
  wie `ColumnList.visible_rows()` in der Linux-App über ihr
  `Gtk.SortListModel`.
- **Klick vs. Reorder-Drag laufen über denselben mousedown/mousemove/
  mouseup-Handler** (siehe Kommentar bei `wireMusicColumnHeader` zu genau
  diesem Muster) — `onUp()` unterscheidet nur noch zusätzlich: kein
  `dragging` (Maus blieb unter dem `REORDER_THRESHOLD`) UND die Spalte hat
  einen `valueOf`-Getter → `toggleMusicSort()` statt Reorder. Erster Klick
  sortiert aufsteigend, zweiter Klick auf DIESELBE Spalte dreht auf
  absteigend um (wie `Gtk.ColumnView`), Klick auf eine andere Spalte
  beginnt immer wieder aufsteigend. Aktive Sortierspalte zeigt einen
  ▲/▼-Pfeil (`renderMusicColumnHeader`, CSS `.track-row-head-cell.sorted`/
  `.track-row-head-sort-arrow`).
- Gilt für alle drei Kontexte (Album-Übersicht als Liste, Album-Detail-
  Tracklist, „Alle Titel") — dieselbe Infrastruktur wie Resize/Reorder.

### Sammlungen (TMDB-Collections)
- **✅ ACL + FSK abgesichert (2026-09-02)** — `ListCollections`/`GetCollectionParts`/
  `ListItemsInCollection` liefen vorher ohne Library-ACL- UND FSK-Prüfung (Non-Admin sah fremde
  Sammlungen inkl. Datei-Pfaden; ein FSK-18-Film wäre über den Sammlungs-Umweg für
  eingeschränkte Accounts sichtbar gewesen). Alle drei filtern jetzt per
  `store.itemVisibilityClause`/`aclLibraryClause(col, userID, isAdmin)` (Admin immer alles,
  Non-Admin nur `user_library_access` + `MaxAgeRating`). Bei `GetCollectionParts` sitzt die
  Klausel bewusst im `LEFT JOIN`, damit ein unzugänglicher Part wie ein fehlender aussieht
  (`owned:false`) statt zu verraten, dass der Film vorhanden ist. Volle Root-Cause-Analyse:
  DECISIONS.md „Sammlungen liefen ohne ACL- und FSK-Prüfung". Tests:
  `internal/store/collections_acl_test.go`. Siehe [[feedback_user_isolation_before_deploy]] —
  wiederkehrendes Muster: neues Feature mit User-sichtbaren Daten ohne ACL-Prüfung.
- **⚠ Generisches Hardening (`internal/store/hardening.go`, 2026-09-02):**
  optionale, rein per Env-Var (`GOLDFISH_FORCE_ADMIN_ONLY_LIBRARIES`,
  kommagetrennte Bibliotheks-NAMEN) konfigurierte Sperre — für die dort
  genannten Bibliotheken sehen NUR Admins etwas, egal welche
  `user_library_access`-Zeilen für einen Non-Admin existieren oder später
  gesetzt werden. Greift zentral in `UserHasLibraryAccess`,
  `ListLibrariesForUser`, dem `ListItems`-ACL-Block UND den
  Sammlungs-Queries (`aclLibraryClause`). Leer/unset ist ein reines No-op —
  **welche Bibliotheken (falls überhaupt) das betrifft, ist bewusst NICHT
  Teil dieses Repos** (nur als Laufzeit-Env-Var auf dem jeweiligen Server
  gesetzt, siehe Portainer-Stack-Env). Test: `TestForceAdminOnlyLibraries`
  in `hardening_test.go` (mit generischen Test-Bibliotheksnamen).
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
- **Manueller Bulk-Merge**: `POST /api/items/merge` mit `{ids:[…]}` (admin-only)
  — erstes Item mit metadata_id ist canonical, andere übernehmen es. Seit
  2026-09-07 wieder ein `🔗 Zusammenführen`-Button in der Bulk-Auswahl-Leiste
  (`bulkMerge()` in app.js) — User-Anfrage: nach manueller Korrektur einer
  Fehlzuordnung ("A Complete Unknown") blieben zwei Dateien getrennt, weil sie
  bei der Einzelkorrektur auf verschiedene TMDB-Einträge für DENSELBEN Film
  gelandet waren (TMDB listet manche Filme doppelt/fehlerhaft) — der
  automatische Merge (`groupVariants` im Grid) greift nur bei bereits
  identischer `metadata_id`. Funktioniert gleichermaßen für Filme UND Serien-
  Episoden, **auch bibliotheksübergreifend** (die frühere "gleiche
  Bibliothek"-Pflicht wurde noch am selben Tag wieder entfernt, siehe
  Bugfix-Notiz unten — Duplikate derselben Zuordnung liegen in der Praxis
  oft in unterschiedlichen Libraries, z. B. Bluray-Rip + separate Filme-
  Kopie). Verlangt nur mind. 2 ausgewählte Kacheln, mindestens eine davon
  mit TMDB-Zuordnung; Merge ändert ausschließlich `metadata_id`, NIE
  `library_id`/den Datei-Pfad.
  **⚠ Drei Fallen beim Bulk-Merge** (alle 2026-09-07 beim ersten Test gefunden):
  (1) `appendSearchResultCards` ist im Such-Modus die ALLEINIGE Quelle für
  `state.lastRenderedItems` — es gruppiert pro Bibliothek, ein vorher separat
  gesetztes, library-übergreifendes `groupVariants(items)` fasst zu WENIGER
  Einträgen zusammen als tatsächlich gerendert werden, wodurch
  `selectedItems()` angehakte IDs nicht wiederfindet („0 ausgewählt").
  (2) `bulkMerge()` sammelt ALLE `_variants`-IDs jeder Auswahl ein, nicht nur
  die des Repräsentanten — sonst bleiben die Geschwister einer bereits
  gruppierten ×N-Kachel auf der alten `metadata_id` hängen und werden aus ihrer
  vorher richtigen Gruppe gerissen („Merge hat nichts bewirkt").
  (3) `openDetail()` holt die Varianten IMMER frisch über
  `/api/items/{id}/variants` statt aus dem Grid-Kontext — `groupVariants()`
  sieht nur die geladene (oft library-gescopte) Liste, der ×N-Badge kommt
  dagegen aus dem server-seitig über ALLE Bibliotheken gezählten
  `variantCount`; beide Quellen liefen sonst auseinander.
- **Varianten trennen (seit 2026-07-31, Admin-only):** `items.variant_split`
  nimmt ein Item aus der automatischen ×N-Gruppierung heraus, OHNE die
  metadata_id zu ändern — bleibt derselbe Film, erscheint aber als eigene
  Kachel statt im Varianten-Dropdown zu verschwinden. Button
  „🔀 Als eigene Kacheln trennen" im Detail-Dialog (nur sichtbar wenn
  Varianten vorhanden + Admin) setzt das Flag für ALLE Geschwister-Items
  gleichzeitig; „🔗 Wieder zusammenlegen" hebt es wieder auf. `PUT
  /api/items/{id}/variant-split {split: bool}`. `attachVariantCounts`
  (Store) ignoriert gesplittete Items bei der ×N-Zählung der verbleibenden
  Gruppe. Der Varianten-Dropdown selbst bleibt unverändert sichtbar (holt
  weiterhin ALLE Geschwister per metadata_id über `/api/items/{id}/variants`,
  unabhängig vom Split-Flag) — man kann also von einer getrennten Kachel aus
  jederzeit wieder zusammenlegen.
- **Duplikate-Filter (Sort-Dropdown „Duplikate", seit 2026-07-12 zweigeteilt):**
  Movies/TV nutzen weiterhin die bisherige metadata_id-basierte, **library-
  übergreifende** Erkennung (`ItemFilter.DupesOnly`, alle Libs mit gleichem
  `kind`, z. B. Bluray + Filme zusammen). **Privat-Libraries** (`kind=private`)
  haben normalerweise KEINE TMDB/Custom-Zuordnung (`metadata_id` meist NULL) —
  dort greift DupesOnly praktisch nie. Neuer paralleler Mechanismus
  `ItemFilter.FileDupesOnly` (`?fileDuplicates=yes`) erkennt Duplikate anhand
  **gleicher `size_bytes` + `duration_sec`** (starkes Signal für „dieselbe
  Datei versehentlich zweimal"), gescoped auf **eine** Library + optional
  Ordner rekursiv (Konvention „nur nach unten flach" wie bei den Flat-Sorts,
  NICHT library-übergreifend — anders als bei Movies/TV). Frontend-Branch in
  `grid.js` wählt anhand `lib.kind === "private"` zwischen beiden. Fängt keine
  inhaltlich identischen, aber unterschiedlich großen/re-encodeten Dateien ab
  (bewusst konservativ, um False-Positives zu vermeiden).
- **„🔀 Mehrere Versionen" (Sort-Dropdown, seit 2026-09-02):** zeigt genau die
  Kacheln mit ×N-Varianten-Badge — im Unterschied zu „Duplikate" (flach, jede
  Datei einzeln, library-übergreifend über alle Libs gleichen Kinds) bleibt
  diese Ansicht auf die gerade betrachtete Bibliothek beschränkt und mergt
  wie gewohnt zu einer Kachel pro Titel/Episode (User-Wunsch: „jeweils auf
  Bibliotheken bezogen, in Serien nur von Serien"). Braucht KEINEN neuen
  Server-Endpoint — filtert client-seitig auf `item.variantCount >= 2` (das
  `ListItems` ohnehin für jedes Item global mitliefert, exakt die Quelle des
  ×N-Badges) und mergt via `groupVariants()`. Scope wie die anderen
  Flat-Sorts (aktueller Ordner rekursiv, sonst ganze Library).

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

### Serienübersicht — Auto-Merge doppelter Serien-Ordner (seit 2026-09-05)
- **User-Frage 2026-09-05:** „warum ist in der Serienübersicht 2x Bosch drin,
  wenn Filme/Folgen doch auch gemergt werden?" — Ursache: die Serien-Kacheln
  auf TV-Library-Root-Ebene werden von `Store.topLevelFolders`
  (`internal/store/sqlite.go`) **rein nach dem literalen obersten
  Ordnernamen-String** gruppiert (`SUBSTR(rel_path,...) GROUP BY folder`) —
  komplett unabhängig von der TMDB-Zuordnung, die erst danach per
  `folder_metadata`-JOIN nur fürs Anzeigen drangehängt wird. Zwei Ordner mit
  unterschiedlichem Namen (z. B. ein verwaister `Bosch`-Ordner mit nur
  NFO/Poster neben dem echten `Bosch (2014)`) landen deshalb NIE automatisch
  in einer Kachel, selbst wenn beide korrekt auf dieselbe Show matchen — das
  ist ein anderer Mechanismus als das `metadata_id`-basierte Datei-/
  Episoden-Merge (`_variants`/`groupVariants`), das ohnehin nur INNERHALB
  eines bereits geöffneten Ordners läuft, nie auf der Serienübersicht selbst.
- **Fix — rein anzeigeseitig, rührt keine Dateien/DB-Zeilen an:**
  `mergeFoldersBySameShow` (`internal/store/sqlite.go`, aufgerufen am Ende
  von `topLevelFolders`) fasst mehrere Ordner mit identischer
  `folder_metadata.metadata_id` zu EINER Kachel zusammen. Repräsentant =
  der Ordner mit den meisten Items (Klick navigiert weiterhin nur zu diesem
  EINEN physischen Ordner); `ItemCount` wird über alle gemergten Ordner
  aufsummiert, `MergedFolders []string` listet die übrigen Ordnernamen
  (rein informativ). Frontend: kleines 🔗-Icon oben links auf der Kachel
  (`cards.js renderFolderCard`, `.folder-merged` in style.css, Tooltip zeigt
  die zusammengeführten Ordnernamen). Test: `internal/store/folders_merge_test.go`.
- **`ItemCount` zählt seit 2026-09-05 eindeutige Episoden, nicht Dateien**
  (Folgefrage desselben User: "70 Folgen" auf der Kachel, aber nur 68
  unterschiedliche — eine Folge lag doppelt als zwei Qualitäts-Varianten
  vor). Die innere Aggregations-Query in `topLevelFolders` nutzt jetzt
  `COUNT(DISTINCT CASE WHEN metadata_id IS NOT NULL AND
  COALESCE(variant_split,0)=0 THEN 'm'||metadata_id ELSE 'i'||id END)`
  statt `COUNT(*)` — Items mit gleicher `metadata_id` (typischerweise
  mehrere Qualitäts-Dateien derselben Folge) zählen nur einmal, genau wie
  die Staffel-Ansicht sie zu einem Owned-Slot mergt. Unmatched Items UND
  bewusst per `variant_split` entkoppelte Items (siehe „Varianten
  trennen") zählen weiterhin einzeln — gleiche Konvention wie
  `attachVariantCounts`. **Bewusste Lücke:** bei `mergeFoldersBySameShow`
  wird weiterhin naiv aufsummiert — käme dieselbe `metadata_id` in ZWEI
  gemergten Ordnern vor, würde sie doppelt gezählt (kein bekannter
  Praxisfall bisher). Test:
  `internal/store/folders_merge_test.go TestTopLevelFoldersItemCountDedupesVariants`.
- **✅ Multi-Folder-Browsing für Staffel-Ansicht + Fehlende-Folgen-Export
  (seit 2026-09-10, LIVE 1.3.4, User-Report "Two and a Half Men" zeigte
  ×48 auf der Kachel, aber nur 24 Folgen beim Öffnen):** löst jetzt den
  Fall, dass zwei Ordner mit jeweils ECHTEM, unterschiedlichem
  Episoden-Inhalt zur selben Show gehören (Staffel 1 und Staffel 2 als
  zwei getrennte Download-Ordner statt einem gemeinsamen Serien-Ordner) —
  vorher öffnete die 🔗-Kachel weiterhin nur den EINEN Repräsentanten-
  Ordner (Klick auf "Two and a Half Men" zeigte trotz "×48"-Badge nur die
  24 Folgen von S01, S02 war unerreichbar). **User-Vorgabe explizit
  bestätigt: KEINE Dateien werden verschoben** — reines virtuelles
  Zusammenlesen beim Anzeigen, das Dateisystem bleibt unangetastet
  (Alternative "Dateien physisch in einen Ordner verschieben" wurde
  zunächst angefangen, dann auf Wunsch des Users verworfen).
  `Store.MergedFolderNames(libID, folder)` (`internal/store/series.go`)
  findet symmetrisch alle Geschwister-Ordner mit identischer
  `folder_metadata.metadata_id` (funktioniert unabhängig davon, ob `folder`
  der von `mergeFoldersBySameShow` gewählte Repräsentant ist oder einer der
  anderen). `SeriesOwnedEpisodes`/`UnmatchedEpisodeFiles`/
  `WatchedItemIDsInFolder` nehmen jetzt `folders []string` statt `folder
  string` (via `folderScopeClause`-Helper, OR-verknüpfte LIKE-Bedingungen —
  bei genau einem Ordner identisch zum alten Single-Folder-Verhalten,
  **keine Frontend-Änderung nötig**, der Browser schickt weiterhin nur den
  einen `folder=`-Parameter, den Rest löst `seriesSeasons`
  (`internal/api/series.go`) serverseitig auf: `folders := append([]string{
  folder}, siblings...)`). `collectMissingEpisodesForFolder`
  (`internal/api/missing.go`, Export „fehlende Folgen") ebenfalls
  merge-aware gemacht — dabei musste der übergeordnete `missingEpisodes`-
  Handler zusätzlich die Geschwister-Ordner aus der Iterationsliste
  DEDUPEN (sonst hätte er dieselbe fehlende Folge zweimal gemeldet, einmal
  pro Ordnername in der Merge-Gruppe). `ShowTMDBForFolder`/
  `ShowMetadataIDForFolder`/`GetFolderMetadataID` bleiben bewusst
  Single-Folder (Metadaten/Poster hängen nur am jeweils angefragten
  Ordner, beide Geschwister-Ordner haben aber ohnehin dieselbe
  `metadata_id`, liefern also dasselbe Ergebnis). `internal/introskip`
  (Intro-Erkennung, eigene Pro-Ordner-Aktivierung) bleibt bewusst
  Single-Folder — dort ist der exakte physische Ordner Teil der Konfiguration,
  keine Merge-Semantik gewünscht. Tests:
  `internal/store/series_multifolder_test.go` (Multi-Folder-Episodenliste,
  Symmetrie von `MergedFolderNames`, Single-Folder-Fall bleibt unverändert).
- **Bekannte Grenze bei unsauberen Bibliotheken:** die Serien-Zuordnung
  selbst ist ordnergebunden, nicht dateibasiert — `matchItem`
  (`internal/enrich/worker.go`) bestimmt die Show ausschließlich über
  `GetFolderMetadataID(lib.ID, folder)`, der Dateiname liefert nur
  Staffel/Episode-Nummer, nie die Show-Identität. Ist ein Ordner einmal auf
  eine Show gematcht, wird das **bedingungslos** auf jede Datei im Ordner
  angewendet, ohne Gegenprüfung des einzelnen Dateinamens. Ein Ordner mit
  wild gemischten Folgen verschiedener Serien würde also ALLE davon
  fälschlich der einen erkannten Show zuschlagen — kein Bug dieser Änderung,
  sondern eine bestehende Design-Grenze, die dem User bei dieser Gelegenheit
  erklärt wurde.

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
- **„Alle Unbestätigten" (Sort-Dropdown, seit 2026-09-06):** eigener
  Pseudo-Filter-Modus `unconfirmed`, vor allem für Filme gedacht (User-Wunsch:
  "Dort sollen alle Filme erscheinen, welche nicht manuell bestätigt
  wurden"). Anders als `unmatched` (Items OHNE jede TMDB-Zuordnung) zeigt er
  Items MIT `metadata_id`, deren `metadata_confirmed` nie gesetzt wurde —
  unabhängig davon, wie plausibel die Zuordnung aussieht (das unterscheidet
  ihn auch von „⚠ Verdächtige Zuordnungen", das nur Token-Overlap-Heuristik
  nutzt). SQL: `ItemFilter.MatchState == "unconfirmed"` in `ListItems`
  (`internal/store/sqlite.go`): `i.metadata_id IS NOT NULL AND
  COALESCE(i.metadata_confirmed, 0) = 0`. Wie `unmatched` überall dort
  registriert, wo Pseudo-Filter-Modi behandelt werden: `currentMatchMode()`,
  `PSEUDO_FILTER_MODES`, `directionless`-Check in `updateSortDirIcon()`
  (app.js), Season-View-Bypass in `grid.js`, `showConfirm`-Bedingung in
  `cards.js` (inline ✅-Button erscheint auch hier). Test:
  `internal/store/unconfirmed_filter_test.go`.
- **🚫 Zuordnung entfernen** (Detail-Dialog, seit 2026-09-02, admin-only,
  neben 🔍 „Manuell zuordnen"): bis dahin gab es nur „Manuell zuordnen" zum
  ERSETZEN einer Zuordnung — keinen Weg, ein Item wieder komplett in den
  unmatched-Zustand zu versetzen (User-Anfrage: „eine falsche Zuordnung
  löschen"). `POST /items/{id}/unmatch` (`unmatchItemMetadata` in
  `internal/api/tmdb.go`) ruft `Store.SetItemMetadata(id, 0)` — löscht damit
  `metadata_id`, `metadata_confirmed` UND eine ggf. gesetzte Doppelfolgen-
  Range in einem Rutsch (bereits vorhandene Store-Logik, war nur nie über
  einen eigenen Endpoint für Einzel-Items erreichbar). Button sichtbar wenn
  `item.metadataId` gesetzt ist (nichts zu entfernen sonst), mit
  `appConfirm`-Bestätigung davor.
- **🚫 Zuordnung entfernen für ganze Serien-Ordner** (Staffel-Ansicht-Header,
  seit 2026-09-05, admin-only, neben „↻ TMDB neu laden"/„⚠ Episoden neu
  zuordnen"): dieselbe Lücke wie oben, nur auf Ordner-Ebene — „Serie
  zuordnen…" konnte bis dahin nur ERSETZEN, nie auf „keine Zuordnung"
  zurücksetzen (User-Fall: „Terra X" war fälschlich auf die TMDB-Show
  „Terra Xpress" gematcht — Wortüberlappung „Terra" reichte dem
  Auto-Matcher, „⚠ Verdächtige Zuordnungen" übersieht das ebenfalls, weil
  dort Token-Overlap > 0 zählt). `DELETE /api/libraries/{id}/folders/metadata`
  (`unmatchFolderMetadata` in `internal/api/tmdb.go`) setzt
  `folder_metadata.metadata_id` auf NULL (`Store.SetFolderMetadata(...,  0)`)
  und unmatched alle Episoden-Items des Ordners (`UnmatchAllEpisodesInFolder`,
  dieselbe Funktion, die auch „⚠ Episoden neu zuordnen" nutzt). **Löst
  BEWUSST KEIN Re-Match aus** (anders als `setFolderMetadata` beim Setzen
  einer neuen Zuordnung) — sonst würde der Auto-Matcher beim nächsten Lauf
  denselben Ordnernamen erneut gegen TMDB suchen und mit hoher
  Wahrscheinlichkeit wieder beim selben Fehltreffer landen. Der Ordner
  bleibt unmatched, bis ein Admin ihn manuell korrekt zuordnet. Direkt nach
  dem Entfernen zeigt `loadItems()` automatisch die normale Ordneransicht
  (greift der Staffel-Ansicht-Fallback bei leeren Seasons, siehe
  „Serienübersicht — Auto-Merge doppelter Serien-Ordner" oben).
- **⚠ „Zuordnung entfernen" muss gegen den Auto-Retry des Enrichment-Workers
  geschützt sein** (gefixt 2026-09-06, vorbestehender Bug, der erst durch das
  Feature sichtbar wurde) — eine `folder_metadata`-Zeile mit
  `metadata_id IS NULL` bedeutet „bewusst unmatched", nicht „nie versucht".
  Zwei Stellen im 5-Minuten-Worker prüfen das:
  - `Store.PendingFolders` filtert auf **`fm.folder IS NULL`**, nicht
    `fm.metadata_id IS NULL` — bei einem LEFT JOIN ist Letzteres auch bei
    existierender NULL-Zeile wahr; `folder` ist Teil des PRIMARY KEY (NOT NULL)
    und damit der zuverlässige „Zeile existiert überhaupt"-Indikator.
  - `matchItem` triggert `matchShow` nur noch, wenn
    `Store.FolderMetadataRowExists(libID, folder)` false ist — `GetFolderMetadataID`
    allein KANN „keine Zeile" und „Zeile mit NULL" nicht unterscheiden.
  Beide Fixe sind nötig: `enrichFolders` läuft vor `enrichItems` in jedem
  `runOnce()` und hätte den Ordner sonst allein schon wieder gematcht. Tests:
  `internal/store/folder_metadata_test.go`.
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
- **🔴→✅ Zwei gleichzeitige hw=true-Transcode-Sessions desselben Items nach
  Seek/Tonspur-Wechsel (gefixt 2026-09-13):** User-Report per Mac-App-
  Screenshot: `Stream-Fehler (-12888): Playlist File unchanged for longer
  than 1.5 * target duration"`. Live-Diagnose per `docker logs` fand die
  neue `[playback] FEHLER`-Zeile (siehe „Gerät + Wiedergabe-Ende/-Fehler"
  weiter unten) mit der `DiagnoseItem`-Auflösung darin: ZWEI parallel
  laufende Sessions desselben Items — eine bei Start=0/Default-Audio
  (2m16s alt), eine bei Start=45.7s/Audio=1 (56s alt, die gerade aktive,
  die den Timeout auslöste). Root Cause:
  `internal/api/stream.go`s `fresh=1`-Pfad ruft `Manager.StopSession` nur
  für EXAKT denselben Session-Key (identisches `itemID+profile+audioIdx+
  startSec+deinterlace`) auf — ein Seek (ändert `startSec`) ODER ein
  Tonspur-Wechsel (ändert `audioIdx`) erzeugt aber zwangsläufig einen
  ANDEREN Key, wofür `StopSession` nie griff. Die alte Session lief bis
  zum reinen 5-Minuten-Inaktivitäts-GC einfach weiter — zwei parallele
  `hw=true`-Encodes teilen sich denselben Intel-iGPU-VAAPI-Encoder und
  fallen dabei sichtbar zurück, bis der Client den Timeout auslöst. Fix:
  `Manager.StartOrGet` (`internal/playback/ffmpeg.go`) stoppt jetzt, sobald
  eine wirklich NEUE Session gestartet wird (kein exakter Cache-Hit),
  zuerst ALLE anderen bereits laufenden Sessions DESSELBEN Items — es gibt
  konzeptionell immer nur einen aktiven Player pro Item (kein
  Picture-in-Picture/Multi-Stream), ein neuer Session-Key desselben Items
  ist also immer ein Ersatz, nie ein zusätzlicher Zuschauer. Kein neuer
  Test (reine Manager-interne Aufräumlogik, vorhandene Session-Tests decken
  die Kernstruktur bereits ab) — Verhalten am echten Server via
  `DiagnoseItem`-Log-Zeile gegenprüfbar.
  **🔴→✅ Echte Regression durch genau diesen Fix, noch am selben Tag
  (User-Report Stream-Fehler -16847 "HTTP 500"):** Log zeigte ein
  Ping-Pong — Session bei start=0 gestoppt → start=261 gestartet →
  SOFORT wieder start=0 gestoppt → start=261 → ... im Sekundentakt, bis
  der Client aufgab (nie eine fertige Playlist gesehen). Ursache:
  `transcodeSegment` (der `.ts`-Segment-Handler) rief bis dahin ebenfalls
  `StartOrGet` — kann also selbst eine NEUE Session erzeugen. Nach einem
  Seek/Resume treffen oft noch ein paar bereits vom Client in die
  Warteschlange gestellte, VERALTETE Segment-Requests mit dem alten
  `start=` ein, nachdem die alte Session schon korrekt gestoppt wurde —
  das erzeugte über `StartOrGet` eine neue Session bei diesem alten Wert,
  und die neue "andere Sessions desselben Items stoppen"-Logik killte
  daraufhin sofort die gerade erst gestartete ECHTE Session. Der Client
  fragt das nächste Segment der echten Session gleich danach wieder an →
  die entsteht neu → killt die (gerade erst wiederbelebte) alte →
  selbstverstärkendes Ping-Pong. Fix: `transcodeSegment` nutzt jetzt
  `LookupSession` statt `StartOrGet` (analog zu `transcodeProgress`, das
  das schon immer richtig gemacht hat, siehe dessen Kommentar) — ein
  Segment-Request darf grundsätzlich NIE eine Session erzeugen, nur die
  Playlist-Anfrage (`transcodePlaylist`) darf das. Eine wirklich veraltete
  Segment-Anfrage bekommt jetzt schlicht 404 (harmlos, der Client hat
  diese Session ohnehin verlassen) statt eine Geister-Session
  wiederzubeleben. **Lehre:** bevor `StartOrGet` um neue Nebenwirkungen
  (wie das Stoppen von Geschwister-Sessions) erweitert wird, IMMER
  prüfen, welche Aufrufer außer der eigentlichen "Wiedergabe starten"-
  Stelle diese Funktion sonst noch aus einem anderen Grund (hier: nur um
  eine Referenz auf eine erwartete, bereits existierende Session zu
  bekommen) aufrufen — genau dafür ist `LookupSession` da.
  **🔴🔴 Trotzdem noch am selben Tag komplett zurückgenommen — das
  "andere Sessions desselben Items stoppen" in `StartOrGet` selbst war
  der eigentliche Fehler, nicht nur der Segment-Handler:** das
  Ping-Pong trat WEITER auf, diesmal auf Playlist-Ebene (User-Report
  mit HTTP 404 UND HTTP 500, mehrere verschiedene Items betroffen).
  Live-Diagnose zeigte: die Mac-App schickt beim Player-Start teils
  wiederholt (mehrere Zyklen über 5-10+ Sekunden) ZWEI verschiedene
  Playlist-Requests hintereinander — einmal `start=0`, einmal die echte
  Resume-Position (z. B. 631.8s) — beide über `transcodePlaylist`, beide
  also legitime `StartOrGet`-Aufrufer. Das sofortige gegenseitige Stoppen
  verhinderte dabei zuverlässig, dass JEMALS eine der beiden Sessions
  lange genug lebte, um eine Playlist fertigzustellen — Client bekam
  404/500 statt Video, ein STRIKT SCHLIMMERES Ergebnis als das
  ursprüngliche Problem (nur ein vorübergehendes Puffer-Stocken durch
  zwei parallele Encodes). Der komplette "andere Sessions stoppen"-Block
  wurde aus `StartOrGet` entfernt — Ursprungszustand (Sessions leben bis
  zum 5-Minuten-Inaktivitäts-GC) wiederhergestellt. Die
  `transcodeSegment`→`LookupSession`-Änderung blieb bestehen (für sich
  genommen weiterhin korrekt: ein Segment-Request soll nie eine Session
  erzeugen können, unabhängig vom Sibling-Kill-Thema). **Das ursprüngliche
  "zwei parallele hw=true-Encodes"-Problem ist damit wieder ungelöst** —
  ein künftiger Fix müsste zuerst klären, WARUM der Mac-App-Client beim
  Start wiederholt zwei unterschiedliche `start=`-Werte anfragt (Client-
  seitiger Bug, vermutlich eine Race zwischen einem initialen Default-Load
  und der eigentlichen Resume-Positions-Anwendung), statt das Symptom
  serverseitig zu bekämpfen. **Lehre, verschärft:** ein Fix, der auf
  Basis EINES einzigen Diagnose-Logs gebaut wird, muss nach dem Deploy
  aktiv auf neue, andere Fehlermuster am selben Symptom-Ort beobachtet
  werden — hier hätte ein zweiter Blick auf die Client-Seite (warum zwei
  Requests?) VOR dem Server-Fix vermutlich den echten Bug gefunden und
  dieses Hin und Her vermieden.
  **🔴→✅ 2026-09-14, dritte Runde — kompletter Verzicht auf Aufräumung war
  seinerseits ein echtes Problem:** User-Report "Container hängt, über 60%
  Last, kein anderer Dienst läuft". Live-Diagnose per `docker top` fand
  **11 gleichzeitig laufende `hw=true`-ffmpeg-Prozesse** bei nur einer
  Handvoll Items — u. a. FÜNF parallele Sessions für dasselbe Video bei
  Start-Offsets 0/286/680/1074/1648 (jeder Sprung im Player-Zeitstrahl
  hatte seit dem Revert oben eine neue Session erzeugt, die vorherige lief
  aber einfach die vollen 5 Minuten GC-Inaktivität weiter — bei aktivem
  Spulen sammeln sich so beliebig viele parallele Hardware-Encodes an).
  **Fix, als Kompromiss zwischen beiden Extremen:** `StartOrGet` stoppt
  andere Sessions desselben Items jetzt wieder, aber nur wenn sie
  `siblingStopGracePeriod` (10 s) alt sind — jung genug für die eingangs
  beschriebene Doppelrequest-Race lässt der Grace-Zeitraum unangetastet
  (das Ping-Pong dort spielte sich innerhalb von 1-4 Sekunden ab), eine
  wirklich verlassene Session aus echtem Spulen wird aber binnen 10 s statt
  5 Minuten abgeräumt. Das Mac-App-seitige Doppelrequest-Problem selbst
  (siehe App-CLAUDE.md, `setupGeneration`-Guard) bleibt davon unberührt —
  dieser Fix ist eine reine Server-Absicherung, unabhängig davon ob der
  Client sich korrekt verhält.
  **🔴→✅ Vierte Runde (LIVE 1.3.37, 2026-09-14) — geschlossene Sessions ohne
  Geschwister blieben bis zu 30 Min. auf voller Last:** User-Report "auch
  hier hat Goldfish eine hohe Last, auch nachdem kein Video mehr abgespielt
  wird" (Anlass: Shuffle-Play-Stresstest in Firefox unter Linux — der
  Aktivitäts-Log-Eintrag "Browser · Linux" wurde anfangs fälschlich als
  native GoldfishLinux-App-Aktivität gedeutet, siehe
  [[project_feature_goldfish_linux]] — tatsächlich lief der Test im
  Browser, nicht in der nativen App). Live-Diagnose per `docker top` fand
  ZWEI ffmpeg-Sessions mit 519%/567% CPU, **6-8 Minuten NACHDEM** der
  Client für beide Items bereits `POST /playback/{id}/stop` gemeldet hatte
  (per `activity_log` verifiziert: "stop"-Eintrag lag klar vor dem
  Diagnose-Zeitpunkt). Root Cause: `playbackStop` (`internal/api/
  stream.go`) hat den Wiedergabe-Stopp bis dahin NUR protokolliert
  (`LogActivity`) — die zugehörige ffmpeg-Session lief unberührt weiter,
  bis entweder eine Geschwister-Session desselben Items sie ablöste (siehe
  drei Runden oben, greift nur bei WIEDERHOLTEM Abspielen desselben Items)
  oder der reine Idle-GC nach `sessionIdleTimeout` (30 Min., bewusst
  hochgesetzt für AppleTV-Pausen-Toleranz, siehe Kommentar dort) sie
  abräumte. Ein einmalig abgespieltes und dann geschlossenes Item OHNE
  jeden weiteren Zugriff blieb dadurch bis zu 30 Minuten mit voller
  Encoder-Last aktiv — ffmpeg transcodiert ohne Gegendruck vom Client so
  schnell wie die Hardware hergibt, nicht in Echtzeit. Fix: neue
  `Manager.StopAllForItem(itemID)` (`internal/playback/ffmpeg.go`) beendet
  JEDE laufende Session eines Items unabhängig von Profil/Start-Offset;
  `playbackStop` ruft sie direkt nach dem Log-Eintrag auf. Deckt sowohl den
  "ended"- als auch den "closed"-Reason ab (beide sind ein echtes,
  bewusstes Wiedergabe-Ende, siehe „Gerät + Wiedergabe-Ende/-Fehler"
  weiter unten) — kein Einfluss auf `fresh=1`/Seek-Restarts, die laufen
  über eigene, unveränderte Code-Pfade. **Sofortmaßnahme auf dem Live-
  Server:** die beiden verwaisten ffmpeg-Prozesse hatten (Docker ohne
  eigenes PID-Namespace) dieselben PIDs wie am Unraid-Host sichtbar —
  direkt per `kill -TERM <pid>` beendet, Last fiel sofort auf 0,4 % CPU.
  **🔴→✅ Fünfte Runde, noch am selben Tag (LIVE 1.3.38) — der 1.3.37-Fix
  griff nicht immer, Live-Kontrolle direkt nach dem Deploy fand SOFORT eine
  weitere verwaiste Session:** `docker top` zeigte eine ffmpeg-Session mit
  516 % CPU, `activity_log` bestätigte per `stop`-Eintrag, dass der Client
  sie bereits vor Minuten explizit beendet hatte. Server-Log entlarvte die
  exakte Race: `session … gestoppt (Client meldet Wiedergabe-Ende)` gefolgt
  **eine Sekunde später** von `start session=…` für DIESELBE Session-ID.
  Ursache: eine noch im Flug befindliche Playlist-/Segment-Anfrage der
  gerade geschlossenen Wiedergabe (Netzwerk-Race — GStreamer/VHS puffern
  beim Umschalten typischerweise noch einen Request voraus) traf kurz NACH
  `StopAllForItem` ein und erzeugte über `transcodePlaylist`→`StartOrGet`
  eine frische Session desselben Items — die nie wieder einen Stop bekam
  (der Client hatte dieses Item bereits als geschlossen abgehakt), lief
  also bis zum 30-Min-Idle-GC einfach weiter. Beobachtet beim Linux-App-
  Shuffle-Stresstest (viele Play/Stop-Zyklen in Sekundenbruchteilen —
  maximiert die Trefferchance für genau diese Race), aber plattform-
  unabhängig möglich. Fix: `Manager.stoppedAt map[int64]time.Time`
  (`internal/playback/ffmpeg.go`) — `StopAllForItem` trägt den Zeitpunkt
  ein, `StartOrGet` verweigert eine ECHTE Neu-Erzeugung (kein Cache-Hit)
  für dasselbe Item innerhalb von `stopSuppressWindow` (3 s, komfortabel
  über der beobachteten ~1s-Race, ohne einen absichtlichen Sofort-Replay
  spürbar zu blockieren — in der Praxis nie so schnell erneut angeklickt)
  mit einem Fehler statt eines neuen ffmpeg-Starts. `transcodePlaylist`
  antwortet in diesem Fall mit dem bereits vorhandenen 500-Fehlerpfad —
  eine stray Anfrage der geschlossenen Session bekommt einen harmlosen
  Fehler statt eine Geister-Session zu erzeugen.
- **⚠ Hardware-Decode im Transcode-Pfad (seit 2026-09-14) — MIT zwingendem
  Rückfall:** bis dahin lief nur das ENCODEN auf der iGPU, dekodiert wurde per
  CPU. Am laufenden Server gemessen (60 s aus einem 3840×2160-HEVC,
  `profile=orig`, sonst Leerlauf):

  | Weg | CPU-Zeit | Wanduhr |
  |---|---|---|
  | Software-Decode + `hwupload` (alt) | **186 s** | 33 s |
  | `-hwaccel vaapi` + `scale_vaapi` (neu) | **6 s** | 18 s |

  Faktor 31 — genau der Fall, der eine einzelne Session dauerhaft bei ~570 %
  CPU hielt. Die Bilder bleiben die ganze Kette über GPU-Flächen:
  `deinterlace_vaapi` → `scale_vaapi` → `h264_vaapi`, kein `hwupload` mehr.
  **`format=nv12` im `scale_vaapi` ist Pflicht** (8-Bit-Zwang, sonst scheitert
  `h264_vaapi` an 10-Bit-HDR) — auch OHNE Größenänderung, dann als
  `scale_vaapi=format=nv12`. **`deinterlace_vaapi` MUSS vor `scale_vaapi`**
  stehen (auf voller Auflösung entflimmern).

  **⚠⚠ `Manager.RetryWithSoftwareDecode` ist KEIN Komfort, sondern Pflicht:**
  die iGPU dekodiert laut `vainfo` nur MPEG2, H.264, HEVC (Main/Main10/Main12/
  422) und VP9 — **kein AV1**, kein VC-1, kein MPEG-4. Real gegengeprüft: eine
  AV1-Datei (alle yt-dlp-Downloads sind AV1) bricht mit `Failed to inject frame
  into filter network: Function not implemented` ab. Ohne Rückfall wäre jedes
  AV1-Video unabspielbar. Der Rückfall greift NUR bei
  `playback.ErrFFmpegDiedEarly` (ffmpeg stirbt, bevor eine Playlist entsteht) —
  bei einem reinen Timeout arbeitet ffmpeg noch, ein Neustart würde schaden.
  Genau EIN Versuch (`Session.softwareDecode`), sonst Endlosschleife bei
  kaputten Dateien. Verdrahtet in `transcodePlaylist`.
  Tests: `internal/playback/hwdecode_test.go`.
- **⚠ `-map "0:V:0"` (GROSS-V) in `playback/ffmpeg.go` UND
  `download/prepare.go`** — klein-`v` zählt einfach alle Video-Streams durch
  und greift bei einer Datei mit eingebettetem Cover (`attached_pic=1`, z. B.
  WMV mit vorangestelltem mjpeg-Thumbnail) zum Standbild statt zum Film: die
  Wiedergabe zeigte nur ein 1-Frame-Bild. GROSS-`V` bedeutet explizit „Video,
  OHNE attached pictures". Der Scanner hatte diese Ausnahme seit dem
  Musik-Cover-Art-Fix (2026-09-04), der Transcode-/Download-Aufruf bis
  2026-09-07 nicht. `convVersion` dabei auf **5** erhöht, damit vorher erzeugte
  (kaputte) Download-Kopien verworfen werden.
  **Nicht betroffen:** Trickplay-Sprites und Scan-Thumbnails rufen ffmpeg ohne
  `-map` auf — die automatische Stream-Auswahl geht nach Auflösungs-/
  Bitrate-Heuristik, nicht nach Reihenfolge, und griff schon immer richtig.
  **Falsche Auflösungs-Anzeige („180p" statt „720p")** bei solchen Dateien ist
  ein Altdaten-Problem aus der Scan-Zeit vor 2026-09-04, kein separater Bug:
  `UpsertItem` überschreibt `width`/`height` nur bei einem erneuten Scan —
  betroffene Bibliotheken einmal mit **`?force=true`** scannen.
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
  Subs als WebVTT-Remote-Text-Track, Server konvertiert on-the-fly
  (`/api/subtitle/{id}/{idx}.vtt`, `ffmpeg -c:s webvtt`). **Bild-Untertitel
  (PGS/`hdmv_pgs_subtitle`, VOBSUB, DVB) gehen NICHT** — kein Text-Ziel;
  `subtitleVTT` probet den Codec und antwortet 415, das Dropdown markiert sie
  „Bild – nicht einblendbar" und die Auswahl zeigt einen Toast (Ausweg:
  KI-Untertitel via 🎤). Betrifft die meisten Blu-ray-Rips (Kill Bill).
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
  **🔴→✅ "Server +Ns" war nach jedem Seek/Resume falsch berechnet (gefixt
  2026-09-13, User-Report "Buffer springt zwischen 51 und 0, Video stockt
  kurz"):** `player-buffer.js poll()` rechnete `serverAhead = pos - cur` mit
  `pos` = ABSOLUTE Position (`Session.Position()` = `StartSec + transkodierte
  Sekunden`) und `cur` = `vjs.currentTime()`, das aber RELATIV zur aktuellen
  Session ist (startet bei 0 nach jedem Seek-Restart/Von-Anfang-Play — die
  Playlist selbst trägt keine absoluten Zeitstempel, siehe
  `restartTranscodeAt`). Live per Browser-Konsole verifiziert (User-Session
  bei `virtualOffset=716.88`, `currentTime=663.69` — deutlich unterschiedliche
  Bezugspunkte). Nach einem Seek/Resume war der angezeigte Wert dadurch um
  `virtualOffset` zu hoch. Fix: `cur` wird jetzt vor dem Vergleich um
  `state.playback.virtualOffset` erhöht (`cur = vjs.currentTime() +
  offset`). **Nicht abschließend als DIE Ursache des exakten "51/0"-Musters
  bestätigt** (das Muster selbst ließ sich nicht reproduzieren/per Screenshot
  einfangen, laut User "nicht immer, nicht immer bei 51s") — der Frame-
  Mismatch ist aber unabhängig davon ein echter, jetzt behobener Bug in
  diesem Code. Bei einem erneuten Auftreten: dieselbe Live-Konsolen-Diagnose
  (virtualOffset/currentTime + direkter Fetch auf `/api/transcode/{id}/
  progress`) wiederholen — Firefox' Konsole verschluckt mehrzeilig
  eingefügten Code teils falsch (Klammer-Autocomplete), einzeilige Snippets
  ohne Template-Strings verwenden.
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
- **Kontext-Auswahl (Priorität)** in `randomParams()`:
  1. **Playlist** (`state.currentPlaylist`) → `playlistId=<id>` an
     `/api/items/random`. Pool = alle Items dieser Playlist, library-
     übergreifend.
  2. **Person-Filter** (`state.personFilter.tmdbId`) → `personId=<tmdb>`.
     Pool = alle Videos mit diesem Schauspieler, library-übergreifend.
  3. **Geöffnetes Musik-Album** (`state.currentAlbum`, seit 2026-09-05) →
     `albumId=<id>` (`ItemFilter.MusicAlbumID`, `AND i.music_album_id = ?`).
     Bleibt strikt auf die Tracks dieses Albums beschränkt.
  4. **Manuelle Ordner-Auswahl** (`state.shuffleFolders`, seit 2026-08-09) →
     mehrere `folderSel=<libId>:<relPath>`. Siehe „Ordner-Scoping" unten.
  5. **Library** (`state.currentLibrary`) → `libraryId` + ggf. `folder`.
- Zusätzlich greifen IMMER: `search`, `watched`, `favorite`, `match`,
  Auflösungs-Buckets. **Hörbücher sind grundsätzlich ausgeschlossen**
  (`ItemFilter.ExcludeAudiobooks`, unconditional in `randomItem`-Handler
  gesetzt, seit 2026-09-05 — User-Wunsch: "Bei Zufall Play dürfen Hörbücher
  nicht berücksichtigt werden"). **Seit 2026-09-14 (LIVE 1.3.27) zusätzlich
  Namens-Erkennung, nicht nur Container:** reiner `.m4b`-Container-Check
  (`AND i.container != 'm4b'`) übersah Hörbücher, deren Kapitel als
  einzelne `.mp3`-Dateien vorliegen — User-Vorgabe danach: "Bitte
  Hörbücher, Hörbuch, Audiobook ausschließen". Zusätzliche Klauseln prüfen
  `i.rel_path`/`i.title` case- UND akzent-insensitiv auf die Teilstrings
  „hörbuch"/„audiobook" (`UNACCENT(LOWER(...)) LIKE '%horbuch%'` — UNACCENT
  bildet „ö" auf „o" ab, ein einziges Muster trifft dadurch sowohl
  „Hörbuch" als auch „Hörbücher"). Bewusst konservativ als Substring-Match,
  kein Whitelist/Blacklist-Ordnerkonzept — trifft z. B. auch einen Ordner
  „Hörbücher/Baldacci" oder eine Datei „Audiobook_Kapitel_03.mp3".
  **Genre-Tag nachgezogen (LIVE 1.3.28, noch am selben Tag, User-Report
  "der erste Titel bei Shuffle ist wieder ein Hörbuch"):** ein Hörbuch, das
  weder `.m4b` ist noch „Hörbuch"/„Audiobook" in Titel oder Ordnerpfad
  trägt, hat oft trotzdem ein entsprechendes Genre-Tag — dieselbe
  Substring-Prüfung jetzt zusätzlich auf `i.genre` (Spalte ist
  `TEXT NOT NULL DEFAULT ''`, also nie `NULL` — kein Risiko, dass ein
  `NULL`-Vergleich die ganze Klausel stillschweigend auf „alles raus"
  kippt). GoldfishApple zog dieselbe Ergänzung im selben Zug in
  `Item.isLikelyAudiobook` nach (dort für die Client-seitigen Shuffle-Pfade,
  die den Server-Endpoint nicht nutzen — siehe App-CLAUDE.md).
- `openShuffleItem(item)` (playlists.js, seit 2026-09-04) prüft die
  Bibliotheks-Art des gezogenen Items: Musik → `musicPlayShuffleTrack()`
  (Mini-Player), sonst → `openPlayer(item, {fromShuffle:true})`.
  **Lektion (Commit `9723b9b` fixt `a96624f`):** nach einem `sed`/Skript-
  Bulk-Replace IMMER die Funktionsdefinition selbst mit angrep-en, nicht nur
  die Call-Sites — ein zu breiter Suchstring traf hier die eigene
  Implementierung und machte den else-Zweig rekursiv: jeder Zufalls-Klick auf
  eine Nicht-Musik-Bibliothek endete in „Maximum call stack size exceeded".
- Backend: `ItemFilter.PlaylistID` (EXISTS in `playlist_items`) ist neben
  `PersonTMDB`/`MusicAlbumID` ein weiterer optionaler Pool-Selektor. Alle
  werden von `/api/items/random` aus dem Query gelesen.

#### Ordner-Scoping für Shuffle (seit 2026-08-09)
- **🎯-Button** neben „🎲 Zufall" öffnet `#shuffleScopeDialog`: Ordner-Baum
  (lazy geladen über `GET /api/libraries/{id}/folders?parent=…`, dieselbe
  Route wie die normale Ordner-Navigation — **nicht** das admin-only
  `/all-folders` des Verschieben-Dialogs, damit die Funktion auch für
  Non-Admin-User (`Familie`) nutzbar ist) mit Checkboxen pro Ordner/
  Unterordner, kombinierbar über verschiedene Ordner **und** verschiedene
  Bibliotheken hinweg. Auswahl wird als Chip-Liste angezeigt, „Übernehmen"
  committet sie nach `state.shuffleFolders` + `localStorage["shuffleFolders"]`
  (persistiert über Reload).
- Checkbox auf einem Ordner selektiert ihn **rekursiv inkl. aller
  Unterordner** (wie der bestehende Single-Folder-Filter); es gibt keine
  Ausschluss-Logik für einzelne Unterordner innerhalb einer gewählten
  Ordner-Auswahl.
- Ist `state.shuffleFolders` nicht leer, hat die Auswahl Vorrang vor der
  aktuell geöffneten Library/Ordner (Priorität 3 oben) — bis der User sie im
  Dialog über „Zurücksetzen" leert.
- Backend: `store.ItemFilter.Folders []FolderSelector` (`{LibraryID, Folder}`,
  `Folder=""` = ganze Library) — ersetzt bei Nicht-Leer komplett
  `LibraryID`/`LibraryIDs`/`Folder` in `ListItems` (ODER-verknüpfte
  `(library_id = ? [AND rel_path LIKE ?/%])`-Klauseln, auch library-
  übergreifend). `randomItem`-Handler parst wiederholte `folderSel=<libId>:
  <relPath>`-Query-Parameter und ruft `requireLibAccess` pro referenzierter
  Library auf (Zugriffsschutz, den die vorher schon vorhandene
  `libraryId`-Parsing in `randomItem` NICHT hatte — dort unverändert
  gelassen, nur der neue `folderSel`-Pfad ist geschützt).

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
  **Ebenso die SeekBar:** `progressControl.seekBar.update` wird im Transcode-
  Modus zum No-Op gepatched. Sonst schreibt Video.js die `.vjs-play-progress`-
  Breite parallel zum RAF-Loop — und rechnet dabei gegen die wachsende EVENT-
  Playlist-/Live-Dauer statt der forcierten Filmlänge → der Fortschrittsbalken
  flackert während der Wiedergabe (nicht im Pausenzustand). Direct Play
  unverändert (RAF-Loop ist dort inaktiv, Video.js steuert den Balken).
  **⚠ Ein No-Op-Patch per `sb.update = fn` wirkt NICHT** (LIVE 1.2.52):
  Video.js' `SeekBar` registriert ihre `update`-Methode bereits im KONSTRUKTOR
  als Event-Listener (`this.on(player, ["timeupdate","durationchange"],
  this.update)` — verifiziert gegen den gepinnten Build `video.js@8.17.3`),
  synchron beim Bau der ControlBar und damit lange vor
  `syncTranscodeDisplays()`. `on()` hält die Funktions-REFERENZ zum
  Bindungszeitpunkt fest, macht keinen dynamischen Property-Lookup — ein
  späteres Überschreiben ändert am registrierten Listener nichts. Der
  Original-Listener muss explizit per `vjs.off(["timeupdate","durationchange"],
  origUpdate)` entfernt und durch einen modusabhängigen ersetzt werden
  (`player-transcode-seek.js`), der im Transcode-Modus nichts tut und sonst 1:1
  `origUpdate.apply(sb, args)` ruft. Sonst schreibt er im Wechsel mit dem
  RAF-Loop die falsche (relative) Breite — sichtbar als Flackern des
  Fortschrittsbalkens bei Transcode, nicht bei Direct Play. User-Bestätigung
  2026-09-10: behoben.
  **Scrubber-Punkt im Pill-Skin** (LIVE 1.2.53 + 1.3.1): `.vjs-progress-holder`
  darf kein `overflow: hidden` tragen — die Pillenform kommt ohnehin über
  `border-radius: inherit` der Kind-Elemente, das `overflow` verschluckte nur
  Video.js' eingebauten Punkt. Der Punkt selbst braucht `content:""` plus
  **`display:block`**: `:before` ist per Default `inline` und ignoriert
  `width`/`height` komplett, ein bloßes `width:11px` wirkt nie. Positionierung
  `position:absolute; top:50%; right:0; transform:translate(50%,-50%)`
  zentriert ihn AUF dem Balkenende statt daneben/darunter. Gleiches Prinzip für
  den `.vjs-svg-icon`-Kindknoten im SVG-Icon-Modus. Betraf nur den Pill-Skin;
  keine JS-Änderung nötig, der Punkt hängt am rechten Rand von
  `.vjs-play-progress`, dessen Breite beide Modi bereits korrekt setzen.
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
- **Scroll-Position beim Zurück-Navigieren** (`state.scrollPositions`, Map
  navKey→scrollY): beim Verlassen einer Ansicht wird `window.scrollY` unter
  dem `navKey()` der VERLASSENEN Ansicht abgelegt, beim erneuten Betreten
  per doppeltem `requestAnimationFrame` wiederhergestellt (Kommentar im Code
  seit Langem: "content-visibility:auto braucht manchmal zwei Frames").
  **Zusätzlicher Korrektur-Versuch nach 200 ms** (gefixt 2026-09-06): bei
  hunderten Kacheln liefert `content-visibility:auto` + `contain-intrinsic-size`
  beim ersten Layout-Pass nur eine GESCHÄTZTE Höhe für off-screen-Kacheln — der
  doppelte rAF reicht dann nicht, `scrollTo` clampt auf die zu diesem Zeitpunkt
  noch zu kleine maximale Scroll-Höhe und bleibt dort dauerhaft hängen. Der
  Nachschlag läuft nur, wenn die Zielposition noch nicht erreicht ist UND
  `state.lastNavKey === targetKey` (sonst träfe ein verzögerter Restore eine
  inzwischen andere Ansicht).
- **DB-Indexe** auf `items(library_id, added_at|duration_sec|height|rel_path)`
  und `user_item_state(user_id, last_played_at)`.

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

### Person-Filter (Schauspieler-Klick)
- Klick auf einen Schauspieler öffnet eine library-übergreifende Personen-Ansicht
  (`state.personFilter = {tmdbId, name}`). Zwei parallele Fetches:
  - `GET /api/items?personId=<tmdb>` — die im Bestand vorhandenen Titel (ACL-safe).
  - `GET /api/person/<tmdb>` — **Bio-Daten + volle Filmografie** live von TMDB
    (`tmdb.Client.GetPersonDetails`, ein Call mit `append_to_response=combined_credits`,
    gecacht). Handler `getPerson` in `internal/api/cast.go`; Fallback auf den
    lokalen `people`-Eintrag, wenn TMDB aus/fehlschlägt.
- **Rendering:** `renderPersonHeader` (Foto `/api/person/<id>/profile` +
  Lebensdaten + Bio mit „mehr"-Toggle) + EIN Filmografie-Grid
  (`🎞 Filmografie · N`): jeder TMDB-Credit als Kachel — im Bestand → echte
  `renderCard` (Film) bzw. `renderPersonShowCard` (Serie, 1 Sammelkachel pro
  Show), sonst **ausgegraut** `renderPersonFilmCard` (`.person-film-missing`,
  Badge „nicht vorhanden", TMDB-Poster, Rolle). Owned-Titel, die TMDB nicht
  listet, werden hinten angehängt (nie verstecken, was der User hat).
- **„☐ Nur Treffer"-Toggle** im Filmografie-Header (`.person-owned-toggle`):
  blendet die ausgegrauten, nicht vorhandenen Einträge aus.
  `state.personOwnedOnly`, persistiert in `localStorage["personOwnedOnly"]`.
  (App-Pendant: `@AppStorage("personOwnedOnly")` in `PersonItemsView`.)
- **Fallback ohne TMDB:** altes Split-Rendering 🎬 Filme / 📺 Serien nur mit
  den owned Treffern.
- Sektion-Headings via `.person-section-title` (grid-column: 1/-1).
- **Die Show-Unteransicht hat einen eigenen `navKey()`-Suffix**
  (`"person:<tmdbId>:show:<libraryId>:<folder>"`, seit 2026-09-08) — teilte sie
  sich den Key mit der Filmografie-Übersicht, überschrieb der Rücksprung aus
  der kurzen Episodenliste deren Scroll-Position sofort wieder mit ~0, und man
  landete beim Verlassen des Person-Filters immer ganz oben.
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

### KI-Untertitel (Whisper, seit 2026-05-05)

- **whisper-cli** (whisper.cpp, statisch gebaut mit OpenBLAS) läuft lokal im Container.
  Modelle unter `/config/whisper-models/*.bin` (persistent im Volume). Empfehlung: `ggml-small`.
- **Pipeline** pro Job: (1) ffmpeg extrahiert 16kHz-Mono-WAV, (2) whisper-cli transkribiert
  auf Englisch → `en.vtt`, (3) Übersetzungs-Backend konvertiert VTT-Cues für de/it.
  Whisper-Annotationen wie `[MUSIC PLAYING]` werden beim Übersetzen übersprungen.
- **Übersetzungs-Backends** (wählbar in Settings → 🎤 Whisper):
  - `none` — nur Englisch
  - `deepl` — Free-Key endet auf `:fx` → `api-free.deepl.com` (auto-detekted)
  - `libretranslate` — Self-hosted Stack 39 auf `http://<UNRAID-LAN-IP>:5000`
    (nur de/en/it geladen: `LT_LOAD_ONLY=de,en,it`)
- **Timeout**: `audioTimeout(5m) + durationSec/60 * 5m`, Cap bei 8h (Whisper), um
  lange Filme sicher abzudecken. OpenBLAS beschleunigt die CPU-Berechnung 3-5×.
- **Admin-UI**: 🎤-Button im Detail-Dialog öffnet Popover mit Sprach-Auswahl
  (🇩🇪 🇬🇧 🇮🇹). Job-Status wird alle 5s gepollt (⏳ pending, ⚙ running, ✓ done, ✗ failed).
  Fertige Tracks erscheinen im Player-Sub-Dropdown als `🎤 Deutsch (KI)`.
  VTT-Datei auf Disk ist der Wahrheitsanker — ein fehlgeschlagener Retry löscht die
  alte Datei nicht (UpsertSubtitleJob setzt nur `failed`-Jobs zurück, nicht `done`).
- **Glocke 🔔** in der Topbar: sammelt Fertig/Fehler-Meldungen aller Whisper-Jobs in
  `localStorage` (max 50). Globaler Hintergrund-Poll alle 5s in `whisper.js`.
  Lila Statusbar unten zeigt Phase + Fortschritt während Transkription läuft.
- **Dockerfile**: Build-Stage kompiliert whisper.cpp mit
  `-DBUILD_SHARED_LIBS=OFF -DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS`.
  Runtime-Stage: `libgomp1 libopenblas0 curl`. Binary: `/usr/local/bin/whisper-cli`.
  Modell-Download via `curl` aus HuggingFace (`ggerganov/whisper.cpp`).
- **NICHT zurück auf dynamisches Linking** — `libwhisper.so.1` fehlt im Runtime-Image,
  static build ist Pflicht (`-DBUILD_SHARED_LIBS=OFF`).
- Endpoints:
  ```
  POST /api/items/{id}/generate-subtitle    {language:"de"|"en"|"it"}
  GET  /api/items/{id}/subtitle-jobs
  DELETE /api/items/{id}/subtitle/{lang}
  GET  /api/generated-subtitle/{id}/{lang}.vtt
  GET  /api/whisper/status
  GET  /api/whisper/settings
  PUT  /api/whisper/settings                {backend, deeplKey, libreUrl, libreKey}
  POST /api/whisper/download-model          {model:"ggml-tiny|base|small|medium"}
  GET  /api/whisper/download-status
  ```

### OCR-Untertitel (Bild-Untertitel → Text, seit 2026-08-31)

- **Zweck:** PGS/VOBSUB/DVB-Bild-Untertitel (typisch bei Blu-ray-Rips, z.B.
  Kill Bill) lassen sich nicht per `ffmpeg -c:s webvtt` in Text wandeln. Dieser
  Worker macht daraus per **Tesseract-OCR** (`pgsrip`) einbindbare WebVTT-
  Textuntertitel. Läuft einmalig pro Datei, darf lange dauern.
- **Struktur analog `internal/introskip`:** eigenes Paket `internal/ocrsub`
  (`worker.go` + `ocr.go`), Opt-in pro **Bibliothek** (`ocr_sub_folders`,
  `folder=""` = ganze Lib — der User wählt „Filme"/„Serien" per Checkbox),
  globaler An/Aus (`settings.ocr_subs_enabled`), Job-Tabelle `ocr_sub_jobs`
  (ein Job pro Item, Status pending/running/done/failed, `langs`).
- **Pipeline pro Item** (`processItem`): `Store.ItemBitmapSubStreams` liefert
  die Bild-Untertitel-Streams (Codec ∈ `BitmapSubCodecs`) + deren Sprachen.
  Für **.mkv/.mks** (`pgsripContainer`): Symlink der Quelle nach `/tmp`, dann
  `pgsrip --force -l de -l en -l it [+ Stream-Tag-Sprachen] <link>` (die
  Image-`pgsrip`-Version hat KEIN `--all-languages`; German.DL-Rips taggen die
  PGS-Spur oft `eng`, daher mehrere `-l`) — pgsrip nutzt intern `mkvextract`
  (sauber) und OCR-t alle passenden PGS-Spuren in EINEM Lauf. Ergebnis-`.srt`
  werden **geglobbt** (`base.*.srt` / `base.srt` / neben der Quelle, falls
  pgsrip den realpath auflöst), Sprachcode aus dem Dateinamen normalisiert,
  alle `.srt` danach aufgeräumt. Ergebnis: SRT→VTT (Header + Komma→Punkt) →
  `/config/generated-subs/{itemID}/{ietf}-ocr.vtt`. **Der frühere Weg
  `ffmpeg -c:s copy … .sup` → pgsrip(.sup) ist gescheitert** — ffmpegs
  SUP-Muxer wirft „[sup] Not enough data … Invalid data" an
  Display-Set-Grenzen; NICHT wieder darauf umstellen. Nicht-MKV-Quellen
  (m2ts/ts/mp4) → klarer „nur .mkv/.mks"-Fehler (Fallback noch offen).
  Timeout 45 min/Item. Pausiert während Library-Scans UND während aktiver
  Wiedergabe (`SetPauseCheck`, seit 2026-09-11 auch `playback.Active()`,
  siehe „Trickplay" weiter oben).
- **Auto-Enqueue:** `ocrSubWorker.EnqueueNewItems()` im `sc.OnComplete`-Hook —
  neue Dateien mit Bild-Untertiteln in aktivierten Libs kommen nach jedem
  Scan automatisch dazu (`EnqueueOCRSubBacklog` = alle Items in aktiven
  Ordnern mit Bild-Sub-Stream ohne Job).
- **Player:** `playbackInfo` hängt für jede vorhandene `{lang}-ocr.vtt` einen
  Stream `codec=webvtt-ocr` an (Titel „📝 <Sprache> (OCR)"). `applySubtitleChoice`
  in `player.js` lädt die über `/api/ocr-subtitle/{id}/{lang}.vtt`. Wählt der
  User einen (noch nicht OCR-ten) Bild-Untertitel, kommt ein Toast-Hinweis
  aufs Zahnrad-Menü.
- **Docker:** Runtime-Stage installiert `tesseract-ocr` + `-deu/-eng/-ita` +
  `mkvtoolnix` + `python3-pip`, dann `pip3 install --break-system-packages
  pgsrip`. Fehlt `pgsrip` im Image (`exec.LookPath`), no-opt der Worker
  (`toolMissing` im Status → UI warnt).
- **Endpoints (alle admin):**
  ```
  GET  /api/ocrsubs/status                    (enabled, running, counts, toolMissing)
  PUT  /api/ocrsubs/settings                  {enabled}
  GET  /api/ocrsubs/folders                   (Libs + enabled-Flag)
  PUT  /api/ocrsubs/folders                   {libraryId, folder, enabled}
  GET  /api/ocrsubs/log?status=pending|running|done|failed
  POST /api/ocrsubs/run                       ("alle jetzt erzeugen") → {queued}
  POST /api/ocrsubs/retry-failed
  POST /api/ocrsubs/items/{id}/retry
  GET  /api/ocr-subtitle/{id}/{lang}.vtt      (serviert die erzeugte VTT)
  ```
- **UI:** Zahnrad-Menü „📝 OCR-Untertitel erzeugen" → `#ocrSubDialog`
  (`ocrsub.js`), Aufbau wie der Intro-Erkennung-Dialog (Toggle +
  Bibliotheks-Checkboxen + Job-Tabs, `.tp-tab`-Klassen wiederverwendet,
  5-s-Poll solange offen).

### Sidecar-Untertitel (Untertitel-DATEIEN neben dem Video, seit 2026-09-15)

- **Zweck:** Untertitel, die als eigene Datei im Medienordner liegen
  (`Film.de.vtt`, `Film.en.srt`) — der typische yt-dlp-Fall ohne
  `--embed-subs`. Der Scanner indexiert nur Video-/Audio-Endungen
  (`supportedExt`), diese Dateien waren daher für Goldfish **komplett
  unsichtbar**: sie standen in keinem Dropdown und es gab keinen Weg, sie zu
  laden. Eingebettete Textspuren im Container waren nie betroffen (die erfasst
  ffprobe beim Scan in `item_streams`).
- **Erkennung zur ABFRAGEZEIT, nicht beim Scan** (`internal/api/subtitles_sidecar.go`,
  `findSidecarSubs`): gleiche Konvention wie die erzeugten KI-/OCR-Untertitel,
  die ebenfalls per `os.Stat` in `playbackInfo` gefunden werden. Eine
  nachträglich hinzugelegte `.vtt` wirkt dadurch **sofort, ohne Rescan**.
  Kostenpunkt ist EIN `os.ReadDir` des Videoordners pro `playbackInfo`-Aufruf.
- **Unterstützte Endungen:** `.vtt`, `.srt`, `.ass`, `.ssa`. `.sub` fehlt
  absichtlich — MicroDVD ist frame-basiert (bräuchte die Bildrate) bzw. bei
  `.idx/.sub` bildbasiert, beides nichts für einen blinden ffmpeg-Durchlauf.
- **Namenskonvention:** gleicher Ordner, gleicher Dateiname-Stamm wie das
  Video, danach optionale Tokens: Sprache (`de`/`deu`/`ger`/`german`, auch
  `de-DE`/`en-orig` von yt-dlp) und Zusätze (`forced`, `sdh`, `cc`, `hi`,
  `default`, `full`, `orig`, `auto`, `und`). Sprachcodes werden auf die
  3-Buchstaben-Form normalisiert, die ffprobe auch für eingebettete Spuren
  liefert — dadurch greifen die Sprachnamen-Tabellen aller Clients unverändert.
- **⚠ JEDES Token nach dem Stamm muss erkannt sein, sonst wird die Datei
  verworfen.** Ein reiner Präfix-Vergleich greift in flachen Ordnern zu weit:
  `Film.2.de.vtt` gehört zu `Film.2.mkv`, beginnt aber ebenfalls mit `Film.`
  und würde sonst zusätzlich bei `Film.mkv` als Untertitel auftauchen.
- **⚠ Kein Unterordner-Support** (`Subs/`, Plex-Konvention): dort heißen die
  Dateien typischerweise `2_German.srt` und tragen den Videonamen gar nicht,
  das ist ein anderes Problem. Bewusst offen gelassen.
- **KEINE Client-Änderung nötig** — das war die Entwurfsvorgabe: die Spuren
  bekommen synthetische Stream-Indizes ab **`sidecarIndexBase = 2200`**
  (oberhalb Whisper 2000+ und OCR 2100+) und werden vom BESTEHENDEN
  `GET /api/subtitle/{id}/{idx}.vtt` ausgeliefert, auf das alle Clients für
  jeden Codec außer `webvtt-generated`/`webvtt-ocr` ohnehin zurückfallen.
  `subtitleVTT` verzweigt bei `idx >= sidecarIndexBase` auf
  `serveSidecarSubtitle`: `.vtt` geht unverändert raus, `.srt`/`.ass`/`.ssa`
  wandelt ffmpeg einmalig nach `$SubsDir/{itemID}/sidecar-{idx}.vtt` (neu
  gewandelt, wenn die Quelldatei neuer als der Cache ist — eine korrigierte
  `.srt` soll nicht ewig überdeckt bleiben). Codec im Stream-Eintrag:
  `webvtt-sidecar`, Titel `📄 Deutsch (Datei)` (analog 🎤 KI / 📝 OCR).
- **⚠ Die Indizes müssen zwischen `playbackInfo` und der späteren
  `/api/subtitle/...`-Anfrage stabil bleiben** — `findSidecarSubs` sortiert
  deshalb nach Dateipfad und `subtitleVTT` baut dieselbe Liste erneut auf.
  Ändert sich der Ordnerinhalt zwischen beiden Aufrufen, zeigt die Auswahl im
  schlimmsten Fall auf die Nachbardatei; ein erneutes Öffnen des Players heilt
  das. (Gleiche Klasse von Annahme wie die feste Sprachreihenfolge bei OCR.)
- **Cache-Control ist bewusst kürzer** (300 s statt der 24 h bei extrahierten
  eingebetteten Spuren): eine Datei im Medienordner kann jederzeit ersetzt
  werden, ohne dass sich die Item-ID ändert.
- Tests: `internal/api/subtitles_sidecar_test.go` (yt-dlp-Namen mit `#`/
  Leerzeichen, Release-Namen mit vielen Punkten im Stamm, Forced/SDH,
  Groß-/Kleinschreibung, Abgrenzung gegen das Sidecar eines Nachbar-Videos).

### ⚠ #subSelect darf nur EINEN Change-Handler haben (gefixt 2026-09-15)

`#subSelect` ist ein statisches DOM-Element und überlebt jeden Player-Open.
Bis 1.3.42 hingen dort ZWEI Handler: einer in `app.js` (alt, ohne
WebVTT-Prüfung, ohne Zeitstempel-Shift, mit falscher URL für KI-/OCR-Spuren)
und `applySubtitleChoice` in `player.js` — und der `player.js`-Handler wurde
über den Reuse-Pfad bei JEDEM weiteren Video erneut angehängt (das
`dataset`-Flag wurde dort gelöscht, der alte Listener aber nie entfernt).
Er trug `vjs`/`item`/`subs` in einer **Closure**, sodass nach dem zweiten
Video mehrere Handler mit den Daten verschiedener Items gleichzeitig feuerten.
Weil `applySubtitleChoice` async ist und als Erstes alle Text-Tracks abräumt,
gewann ein veralteter Aufruf regelmäßig das Rennen: er holte
`/api/subtitle/<altes Item>/…` (404 → Fehler-Toast) und der korrekte Track war
wieder weg. Sichtbares Symptom: „Untertitel werden nicht angezeigt", obwohl
die Spur im Dropdown stand und der Server sie korrekt lieferte.
**Regel:** Handler auf statischen Dialog-Elementen genau einmal anhängen
(`wireSubSelectOnce`, gleiches Muster wie `wireIntroSkipOverlayOnce`) und den
Kontext IMMER aus `state` lesen (`state.playback.playingItem`,
`state.playback.streams`), nie aus einer Closure. Chronik: DECISIONS.md.

### Glocke / Benachrichtigungen (seit 2026-05-05)

- 🔔-Button in der Topbar (`.bell-btn`) neben dem Zahnrad.
- Rotes Badge mit ungelesener Anzahl; Klick öffnet Dropdown, markiert alle als gelesen.
- Einträge in `localStorage` unter `gf_notifications` (max 50), persistent über Reload.
- Aktuell befüllt von Whisper-Job-Completions (✅ fertig / ❌ fehlgeschlagen).
- `bellAdd(icon, title, sub)` ist global — weitere Features können es nutzen.
- `initBell()` wird aus `boot()` in `app.js` aufgerufen.

### Download & Löschen
- **Download** (`GET /api/download/{id}`): liefert standardmäßig weiterhin die
  Original-Datei mit `Content-Disposition: attachment`, kein Transcode.
- **"Optimierte Downloads" (seit 2026-09-11, User-Wunsch, Plex-Vorbild
  "Optimierte Versionen"):** optionaler `&profile=`-Parameter auf
  `?compat=1`-Downloads, gleicher `playback.Profiles`-Katalog wie beim
  Streaming (Auflösungs-/Bitrate-Stufen). Wirkt NUR als echter Cap, wenn
  das Item ihn tatsächlich überschreitet (`internal/download.needsDownscale`,
  identische Logik wie `playback.DecideWithCap`) — "Automatisch" (kein
  Parameter, oder `orig`) lädt unverändert das Original/die reine
  Codec-Fix-Kopie, KEIN automatischer Cap ("Es macht auf dem iPhone
  keinen Sinn 80GB eines 4K Filmes zu laden" war der Auslöser, aber der
  User wollte explizit KEINEN erzwungenen Cap im Automatisch-Fall).
  Muss das Item wirklich runter, wird das Video IMMER neu encodet
  (unabhängig vom Quell-Codec — Auflösung ändert sich ja) und bekommt
  einen EIGENEN, profilspezifischen Cache-Pfad
  (`<itemID>-<profileID>.mp4` statt `<itemID>.mp4`) — mehrere
  Qualitätsstufen desselben Films können also gleichzeitig im
  Download-Cache liegen. Skalierung+Encode nutzt dasselbe CPU-Scale-
  +hwupload-Muster wie der Streaming-Transcode
  (`internal/playback/ffmpeg.go Manager.buildArgs`) — VAAPI/NVENC/
  Software, mit Software-Fallback bei HW-Fehlschlag (bestehender
  `runPrep`-Retry-Mechanismus, griff automatisch mit ohne Änderung dort).
  Audio-Bitrate wird beim Downscale zusätzlich auf `profile.AudioKbps`
  gedeckelt (256k Stereo bliebe bei z. B. 480p/600kbps unverhältnismäßig
  groß). **Client-seitig (GoldfishApple):** nutzt dieselbe Qualitäts-
  Auswahl, die im Info-Dialog fürs Streaming gilt — kein separater
  Download-Qualitäts-Schalter (User-Entscheidung bei der Diskussion).
  `GET /api/download/{id}/compat-status` nimmt denselben `profile`-
  Parameter, MUSS ihn identisch zum eigentlichen Download-Call auflösen
  (sonst prüft die Status-Abfrage einen anderen Cache-Pfad als der
  spätere Download anfordert). Tests: `internal/download/prepare_test.go`
  (`needsDownscale`-Wahrheitstabelle, `plan()`-Cache-Pfad-Verzweigung).
  **⚠ `clampProfileToSource(profile, itemBitrateKbps)`** deckelt die
  Encode-Bitrate auf die Quelle — sonst kann ein „optimierter" Download GRÖSSER
  werden als das Original (273 MB → 315 MB bei einem effizient kodierten Video,
  das serverseitig auf den ersten Katalog-Eintrag „480p-hq · 2 Mbps" traf).
  Es zieht dabei zuerst `profile.AudioKbps` ab: `Item.BitrateKbps` kommt aus
  ffprobes `format.bit_rate` und ist die GESAMT-Bitrate (Video **+** Audio) —
  ohne den Abzug hob die neu zugeteilte Audiospur die Summe wieder über die
  Quelle (das war Runde 2, convVersion 6 → 7). Untergrenze 200 kbps gegen ein
  degeneriertes Ziel. Der Clamp ändert NUR die tatsächliche Encode-Bitrate,
  nicht die `needsDownscale`-Entscheidung und nicht den Cache-Dateinamen.
  Test: `TestClampProfileToSource`.
- **`?compat=1`** (seit 2026-08-27, `internal/download`): server-seitige
  Kompatibilitätsprüfung + einmalige, dauerhaft gecachte Remux-/Transcode-Kopie
  VOR dem Ausliefern — analog zu Jellyfins Geräteprofil-Direct-Play-Entscheidung.
  Nutzt `items.container/video_codec/audio_codec` für die schnelle "ist eh schon
  passend"-Kurzentscheidung (mp4/mov + h264 + aac → Original unverändert
  ausliefern), sonst frisches `ffprobe` für ALLE Audiostreams (nicht nur den
  ersten, siehe unten) + Video-Codec/-Tag. Remux: h264/hevc(→hvc1-Tag)/prores
  per Stream-Copy, alles andere (av1/vp9/…) per Hardware-Encode (`s.HW`,
  VAAPI/NVENC/Software, Software-Fallback bei HW-Fehlschlag). JEDER
  Audiostream wird einzeln gemappt + kopiert oder zu AAC transkodiert
  (inkl. Sprach-Metadata) — **nicht** nur der erste, sonst geht bei
  Mehrsprachen-Rips eine Tonspur verloren. Cache unter
  `/config/cache/downloads/{itemID}.mp4` + `.json`-Sidecar (Quelle
  mtime+size **+ `convVersion`**), damit ein zweiter Download nicht neu
  konvertiert. **`convVersion` (Konstante in `prepare.go`, aktuell 4) bei
  JEDER `buildArgs`-Änderung hochzählen** — sonst wird eine mit alter (ggf.
  kaputter) Logik erzeugte Kopie ewig weiter ausgeliefert, weil nur
  Quelle-mtime+size verglichen wird.
  **ffmpeg-Härtung:** `-nostdin`; `-analyzeduration/-probesize 200M` an
  ffprobe UND ffmpeg (spät startende zweite Tonspur einer großen MKV wird
  sonst übersehen); `-err_detect ignore_err -fflags +genpts`;
  `-max_muxing_queue_size 4096` (MKV→MP4 mit Video-Copy + Audio-Transcode
  bricht sonst mit „Too many packets buffered" ab). `runPrep` loggt Start /
  Codec+Tonspur-Anzahl / Fehler (2000 Zeichen ffmpeg-Ausgabe) / Erfolg als
  `[download] compat-prep …`.
  **Tempo für große Rips:** `+faststart` läuft IMMER (moov am Dateiende macht
  die MP4 für AVFoundation je nach Gerät unabspielbar) — der zusätzliche
  Rewrite-Pass ist durch die „Wird vorbereitet … %"-Anzeige abgedeckt.
  **Audio (aktuell, `convVersion = 4`):** JEDE Tonspur → **AAC-LC Stereo
  (`-ac 2 -b:a 256k`)**, auch AAC-Quellen (Audio-Pass ist gegen den Video-Pass
  vernachlässigbar) — für den compat-Download (Apple-App, meist
  Stereo-Ausgabe) schlägt eine verlässlich klingende Stereo-Spur eine stumme
  5.1-Spur. **Nicht wieder auf AC-3/E-AC-3 oder auf 5.1-ohne-Layout
  umstellen** (Historie: DECISIONS.md „Kill Bill spielt nicht ab").
  `-movflags +negative_cts_offsets` (immer): B-Frame-Delay als negative CTS
  statt `elst`-edit-list (ffmpegs edit list verhinderte Wiedergabe bei
  kopiertem h264 in AVFoundation komplett). Verwaiste `.tmp.*.mp4`
  (Container-Restart mitten im Lauf) werden vor einem neuen Lauf weggeräumt.
  **⚠ Audio-only-Dateien überspringen den Remux-Pfad komplett** — `plan()`
  liefert `needsPrep=false` (Originaldatei direkt ausliefern), sobald
  `videoCodecHint == ""` (Scanner-Konvention „kein Videostream"). Dieses
  Package ist auf VIDEO-Kompatibilität zugeschnitten, der Remux-Pfad setzt
  bedingungslos `-map 0:V:0` — bei einer Datei ohne Videostream matcht das
  nichts und ffmpeg bricht mit 500 „Stream map '0:V:0' matches no streams" ab.
  Der Browser löste das nie aus (fragt Musik-Downloads immer OHNE `?compat=1`
  an), erst der Mac-App-Musik-Download erreichte diesen Pfad (LIVE 1.3.10).
  **Video-Pixelformat (convVersion 3):** `-c:v copy` für h264 nur noch bei
  **8-Bit 4:2:0** (`pix_fmt` ∈ yuv420p/yuvj420p/nv12/nv21). 10-Bit-H.264 /
  4:2:2 / 4:4:4 kann VideoToolbox/AVFoundation **nicht** dekodieren → wird per
  **libx264 → yuv420p** re-encodet (bewusst Software, nicht VAAPI). HEVC
  10-Bit bleibt `copy` (AVFoundation kann HDR-HEVC). Entscheider:
  `videoNeedsReencode(codec, pixFmt)` in `prepare.go`.
  **`GET /api/download/{id}/compat-status`** liefert `{state,percent,message}`
  (`state` ∈ `ready|preparing|error|idle`) und stößt die Formatanpassung an,
  falls nötig und noch nicht laufend/gecacht. `prepJob` trackt `totalMS`
  (ffprobe-Dauer) + `doneMS` (aus ffmpeg `-progress pipe:1`); `percent()` =
  1–99 während des Laufs, 100 bei Erfolg, Jobs bleiben 2 min in
  `prepReg.recent`. Die Apple-App (`DownloadManager.prepareThenTransfer`)
  pollt das alle 2 s und zeigt „Wird vorbereitet … X %" vor dem eigentlichen
  Byte-Download. Opt-in per Query-Param, damit Browser/Android (die die
  Original-Datei wollen) unverändert bleiben — nur die Mac/iOS-App
  (`GoldfishClient.downloadFileURL`) fragt das an.
  **⚠ Höchstens EINE Formatanpassung gleichzeitig (seit 2026-09-14,
  `maxConcurrentPreps` in `prepare.go`):** vorher gab es keine Grenze — jeder
  `compat-status`-Poll für ein weiteres Item startete sofort einen weiteren
  ffmpeg. Am 2026-09-14 liefen dadurch drei Anpassungen desselben 4K-Remux
  parallel und hielten den Server über eine Stunde bei **1700 % von 2000 %**,
  während die Wiedergabe stockte. `prepSlots` (gepufferter Channel) sitzt
  INNERHALB der Goroutine von `prepRegistry.start`, nicht um sie herum: der
  Job wird sofort angelegt und ist für den Client als „wird vorbereitet"
  sichtbar, er beginnt nur später zu rechnen (`prepJob.waiting` → eigene
  Meldung „wartet, bis eine andere Anpassung fertig ist"). **Eins, nicht
  zwei** — ein Lauf sättigt bereits mehrere Kerne und den iGPU-Encoder,
  parallele Läufe erhöhen den Durchsatz nicht, sondern verzögern alle.
  Tests: `internal/download/prepare_queue_test.go`.

  **⚠ Hardware-Decode auch beim Herunterrechnen (seit 2026-09-14):** bis dahin
  waren Downscale-Läufe bewusst vom HW-Decode ausgenommen (Software-Decode +
  CPU-`scale` + `hwupload`). Bei kleinen Quellen belanglos, bei einem
  4K-HEVC-Remux ruinös. **Am laufenden Server gemessen** (60 s Material,
  3840×2160 HEVC, Server sonst im Leerlauf):

  | Weg | CPU-Zeit | Wanduhr |
  |---|---|---|
  | Software-Decode + CPU-scale (alt) | **157 s** | 16 s |
  | `-hwaccel vaapi` + `scale_vaapi` (neu) | **5 s** | 6 s |

  Faktor 31 weniger Rechenzeit, Ergebnis identisch (beide 853×480 bzw.
  1280×720, je `yuv420p` — mit ffprobe gegengeprüft). **`format=nv12` im
  `scale_vaapi` ist Pflicht** — ohne das liefert der Filter bei einer
  10-Bit-HDR-Quelle 10-Bit-Flächen, die `h264_vaapi` nicht encodieren kann
  (dieselbe Notwendigkeit wie in `internal/trickplay/worker.go`).
  `-vaapi_device` darf dann NICHT mehr hinter `-i` stehen, es kommt bereits
  aus `hwaccelDecodeArgs` vor `-i`. **Nur der VAAPI-Pfad wurde umgestellt** —
  NVENC ist nicht gemessen (keine NVIDIA-Karte im Einsatz) und bleibt
  unverändert. Der `forceSoftware`-Rückfall in `runPrep` greift weiterhin.
  **`convVersion` wurde bewusst NICHT erhöht** (Ausnahme von der Regel, im
  Code begründet): der Umbau ändert den Weg, nicht das Ergebnis — ein
  Hochzählen hätte alle vorhandenen Kopien verworfen und stundenlange
  Neuberechnungen ausgelöst, ohne dass eine davon fehlerhaft wäre.
  Tests: `internal/download/prepare_hwdecode_test.go` (hält die gemessene
  Kommandozeile fest, inkl. „genau ein `-vaapi_device`" und „kein `hwupload`
  mehr").

  **Cache-Fristen (seit 2026-09-14, `internal/download/cleanup.go`):** der
  Download-Cache hatte bis dahin ÜBERHAUPT keine Aufräumung — eine einmal
  erzeugte Kopie lag für immer dort, auch eine nie abgeholte. Gefunden bei
  87 GB in 23 Dateien, darunter eine 46-GB-Kopie, die nachweislich nie
  übertragen wurde. `Server.RunDownloadCacheCleanup` (`internal/api/
  download_cleanup.go`, gestartet in `main.go` neben `RunAutoScan`/
  `RunAutoBackup`) läuft beim Start und danach stündlich:
  - **nie ausgeliefert → 3 Tage** nach Erzeugung (Datei-mtime),
  - **ausgeliefert → 1 Tag** nach dem LETZTEN Zugriff (`cacheMeta.ServedAt`).

  **⚠ Bewusst Fristen statt „sofort nach dem Abholen löschen"** (war die
  ursprüngliche User-Idee): der Server kann „vollständig abgeholt" nicht
  erkennen. Clients holen die Datei in vielen Range-Häppchen und setzen nach
  einem Abbruch genau dort wieder auf — ein Löschen beim ersten ausgelieferten
  Byte würde jeden laufenden Transfer zerstören (bei 46 GB läuft der über
  Stunden). `MarkServed` setzt die Uhr bei jedem Request zurück (gedrosselt auf
  einen Schreibzugriff pro 5 min, sonst tausende Sidecar-Writes pro Download),
  wodurch laufende und unterbrochene Downloads automatisch geschützt sind.
  **`.tmp.`-Dateien werden nie angefasst** — eine laufende Konvertierung heißt
  `<id>.mp4.tmp.<ns>.mp4` und endet ebenfalls auf `.mp4`.
  Ein fehlendes `servedAt` (Bestandsdatei) zählt als „nie abgeholt" —
  User-Entscheidung 2026-09-14: eine gelöschte Kopie kostet nur Rechenzeit,
  keine Daten, das Original liegt unangetastet unter `/media`.
  Protokolliert als `job`/`download_cache_cleanup`, EIN Eintrag pro Lauf.
  Tests: `internal/download/cleanup_test.go`.
  **Robust gegen 99-%-Hänger bei Resume:** `internal/download` hat eine
  `prepRegistry` (detachable single-flight pro `outPath`) — der Konvertierungs-
  Lauf läuft mit `context.Background()` (+2h-Cap) auch weiter, wenn der Client
  abbricht (nur das *Warten* im Handler respektiert `r.Context()`); unique
  `.tmp.<ns>.mp4` + disk-space-Guard vor ffmpeg; `ETag`/`Last-Modified` an die
  QUELLDATEI gekoppelt (nicht die Cache-Kopie), damit `If-Range` über
  Resume-Versuche stabil bleibt. Apple-App:
  `URLSessionConfiguration.timeoutIntervalForRequest = 600`. Volle
  Root-Cause-Chronik (E-AC-3/AAC-5.1/`.mkv`-Dateiendungs-Bug/99-%-Hänger):
  DECISIONS.md „Kill Bill spielt nicht ab" + „Compat-Download blieb bei 99 %
  hängen".
  **Ersetzt die frühere client-seitige Formatanpassung für Downloads**
  (`LocalTranscodeService` in GoldfishApple) komplett — die App bekommt nie
  mehr eine kaputte Datei zum Nachbearbeiten. `LocalTranscodeService` läuft
  seither nur noch für lokale/externe Bibliotheken, die direkt vom
  Datenträger gescannt werden und keinen Server zum Fragen haben.
- **Löschen** (Admin-only, `DELETE /api/items/:id?deleteFile=true|false`): Item aus DB
  und optional auch die Datei von Disk entfernen.

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

### Verschieben in andere Ordner / Bibliotheken (seit 2026-07-12)
- Wiederverwendet dieselbe `rename_history`-Infrastruktur wie Auto-Rename —
  ein Move ist serverseitig nur ein Rename mit geändertem Verzeichnisanteil
  (+ optional geändertem `library_id`). Undo im „📝 Umbenennungen & Verschiebungen
  verwalten"-Manager funktioniert dadurch für Moves automatisch mit.
- **Ziel-Bibliothek wählbar** (`targetLibraryId` im Move-Dialog-Dropdown,
  Default = Quell-Library): Move funktioniert sowohl innerhalb derselben
  Library (nur Ordner ändert sich) als auch bibliotheksübergreifend (z. B.
  Privat-Lib → TV-Lib). `library_id`/`kind`-Mismatch wird NICHT geprüft —
  Admin trägt Verantwortung; bestehende TMDB/Custom-Metadata-Zuordnung des
  Items bleibt unverändert (kein Auto-Rematch beim Move).
- **Root-Auflösung — zwei Fälle** (`executeMove` in
  `internal/api/admin_rename.go`):
  - **Gleiche Library:** der physische Root wird NICHT aus `library_paths`
    nachgeschlagen, sondern direkt aus dem Item selbst abgeleitet
    (`root = Path` minus `"/"+RelPath`-Suffix) — bleibt dadurch immer im
    selben physischen Quellordner wie zuvor, auch bei Multi-Path-Bibliotheken.
    Verschieben ÜBER zwei verschiedene Quellordner DERSELBEN Library hinweg
    ist bewusst NICHT unterstützt (Storage-Grenzen könnte der User nicht im
    Kopf haben — sonst landet die Datei überraschend auf einem anderen Volume).
  - **Andere Ziel-Library:** nutzt deren ERSTEN `library_paths`-Eintrag
    (`Store.LibraryPaths`, Fallback `libraries.path` bei Single-Path-Libs) als
    Root. Bei Multi-Path-Ziel landet die Datei immer im ersten Quellordner —
    bei Bedarf per zweitem Move innerhalb der Ziel-Library weiterverschiebbar.
- **`rename_history.old_library_id`/`new_library_id`** (Migration, addCol):
  0/0 = kein Library-Wechsel (deckt auch alle historischen Einträge vor der
  Migration ab). `Store.RecordMove` (ersetzt/erweitert `RecordRename`, das
  jetzt ein Wrapper mit old=new=0 ist) schreibt bei echtem Wechsel zusätzlich
  `items.library_id`; `MarkRenameUndone` macht das beim Undo symmetrisch rückgängig.
- **Zielordner:** muss nicht existieren, wird per `os.MkdirAll` angelegt.
  Namenskonflikte am Ziel werden wie beim Rename automatisch aufgelöst
  (` (2)`, ` (3)`, … via `rename.ResolveConflict`).
- **Einzel-Move:** 📁-Button im Detail-Dialog (admin-only, neben 🗑) öffnet
  `#moveDialog` mit Ziel-Bibliotheks-Dropdown + Zielordner-Textfeld
  (vorausgefüllt mit aktuellem Ordner) + **Ordner-Baum darunter** (seit
  2026-07-12, ersetzt die anfängliche Datalist-Autocomplete): zeigt alle
  vorhandenen Ordnerpfade der Ziel-Library hierarchisch (auf-/zuklappbar,
  Vorfahren des aktuellen Pfads automatisch aufgeklappt), Klick auf eine
  Zeile übernimmt den Pfad ins Textfeld — für neue Ordnernamen bleibt das
  Textfeld frei eintippbar. Baum wird clientseitig aus der flachen
  `GET /api/libraries/{id}/all-folders`-Liste (`Store.AllFolderPaths` — leitet
  alle Ordnerpfade aus den vorhandenen `rel_path`-Werten ab, Goldfish legt
  Ordner nicht explizit an) aufgebaut (`buildFolderTree`/`renderMoveTree` in
  `admin.js`), bei Dropdown-Wechsel neu geladen für die neue Lib.
- **Bulk-Move:** 📁-Button in der Bulk-Auswahl-Leiste, `POST /api/items/move`
  mit `{ids, targetFolder, targetLibraryId}`. Bricht bei gemischter
  QUELL-Library-Auswahl mit Fehlermeldung ab (Ordnerpfad ist relativ zu genau
  einem Root) — Frontend prüft das VOR dem Öffnen des Dialogs. Ziel-Library
  darf natürlich abweichen.
- **Asynchron + Fortschrittsanzeige (seit 2026-09-09, LIVE 1.2.45):**
  `moveItemsBulk` lief bis dahin als EIN blockierender HTTP-Request, der erst
  nach der letzten Datei antwortete — bei vielen/großen Dateien (v. a.
  Cross-Disk-Moves, wo Unraids `shfs` einen echten Byte-Copy statt eines
  reinen `rename()` macht) sah der Admin nur einen scheinbar hängenden
  Dialog ohne jedes Feedback, ohne Log-Zeile, ohne DB-Spur bis zum Ende.
  Jetzt: Route gibt sofort `202` zurück, der eigentliche Move läuft in einer
  Goroutine; Fortschritt in einem package-weiten Singleton
  `currentMoveJob *moveBulkJob` (analog `download.prepRegistry`), abrufbar
  über `GET /api/items/move/status` (admin). Frontend (`handleMoveSubmit`/
  `pollMoveProgress` in `admin.js`) zeigt sofort einen "Verschieben
  gestartet…"-Toast, pollt danach 1×/s und rendert eine Fortschrittsleiste +
  aktuellen Dateinamen im `#moveDialog` (`#moveProgress`/`#moveProgressFill`/
  `#moveProgressText`). Bei nur einem gleichzeitigen Bulk-Move gedacht — ein
  zweiter, parallel gestarteter Job überschreibt einfach den vorherigen
  Status (kein Queueing, seltener Admin-Edge-Case).
  **⚠ Bekanntes Muster für JEDEN `.modal-flex`-Dialog:** ein Button-Lookup
  relativ zu `e.target`/`form` bricht, sobald `normalizeModalLayout` (siehe
  „Dialoge (.modal) …" oben) den Button beim ersten `showModal()` strukturell
  aus dem `<form>` in den Footer heraushebt (nur noch per `form="…"`
  verknüpft) — IMMER per ID/`document`-Lookup referenzieren, nie per
  `formElement.querySelector(…)`. Genau das ließ „Verschieben" lange still
  scheitern: `e.target.querySelector('button[type="submit"]')` war `null`, die
  Funktion warf beim `.disabled = true` und brach ab, BEVOR der `fetch()`
  losging — deshalb fand die Server-Diagnose auch keinerlei Spur (weder
  `rename_history` noch `activity_log`).
- **Thumbnails/Trickplay unberührt:** beide sind item-ID-keyed unter
  `/config/...`, nicht pfad-abhängig — ein Move invalidiert sie nicht.
- Code: `internal/api/admin_rename.go` (`moveItem`, `moveItemsBulk`,
  `executeMove`, `listAllFolders`), `internal/store/sqlite.go`
  (`AllFolderPaths`), `internal/store/rename_history.go` (`RecordMove`).
  Routen: `POST /api/items/{id}/move`, `POST /api/items/move`,
  `GET /api/libraries/{id}/all-folders` (alle admin-only). Frontend:
  `openMoveDialog()`/`loadMoveFolderList()`/`handleMoveSubmit()` in `admin.js`.

#### Zwischenfenster bei Datenträgerwechsel (seit 2026-09-13, LIVE 1.3.24)

- **Auslöser:** User verschob 126 Dateien von einer externen Unassigned-
  Devices-Platte ("Big18", Teil der Multi-Path-Library „a") — laut Log
  erfolgreich ("126 verschoben, 0 fehlgeschlagen"), aber die Dateien blieben
  physisch auf Big18. Root Cause (siehe „Root-Auflösung" oben): ein Move
  INNERHALB derselben Library wechselt bewusst nie den physischen
  Quellordner — das Ziel „Jdownloader" wurde als Unterordner INNERHALB von
  Big18 angelegt, nicht auf dem Array, wo der User es erwartet hatte. Kein
  Bug in dem Sinne (die Design-Entscheidung war schon dokumentiert), aber
  für den User völlig unsichtbar — er bemerkte es nur, weil 126 Dateien in
  nur ein paar Sekunden "verschoben" waren (zu schnell für einen echten
  Platten-übergreifenden Kopiervorgang).
  **Zusätzlich echter Bug dabei gefunden:** selbst ein Move, der bewusst auf
  eine ANDERE Library mit physisch anderem Datenträger zielt, hätte bis
  dahin schlicht mit einem rohen `os.Rename`-Fehler abgebrochen — `os.Rename`
  kann keine Datenträgergrenzen überqueren (`EXDEV`), und es gab keinerlei
  Fallback auf echtes Kopieren+Löschen.
- **Fix, zwei Teile:**
  1. **`internal/rename/rename.go`**: `RenameOnDisk(old, new,
     allowCrossDevice bool)` — bei `os.Rename`-Fehler `EXDEV` UND
     `allowCrossDevice=false` liefert es `ErrCrossDevice` (rührt nichts an);
     bei `allowCrossDevice=true` folgt `copyAndRemove()` (echtes
     `io.Copy` + `Sync` + Quelle löschen, räumt eine unvollständige
     Zieldatei bei Fehlern auf). Neue `IsCrossDevice(oldPath, newDir)`
     vergleicht die Device-IDs (`syscall.Stat_t.Dev`) OHNE etwas
     anzufassen — läuft bei einem noch nicht angelegten `newDir` (die
     Zielordner existieren vor `executeMove`s `os.MkdirAll` oft noch nicht)
     zum nächsten existierenden Vorfahren hoch.
  2. **Neuer Preflight-Endpoint `POST /api/items/move-preview`**
     (admin-only, `moveItemsPreview` in `admin_rename.go`, gemeinsame
     `resolveMoveTarget()`-Zielauflösung mit `executeMove` — extrahiert aus
     dem alten `executeMove`, KEINE Verhaltensänderung an der eigentlichen
     Root-Auflösung selbst). Nimmt dieselbe Body-Form wie
     `POST /api/items/move` (`ids`, `targetFolder`, `targetLibraryId`),
     liefert pro Item `{id, crossDevice, error?}` + `anyCrossDevice`,
     OHNE irgendetwas zu verschieben.
  `moveItem`/`moveItemsBulk` haben ein neues Body-Feld
  `allowCrossDevice bool` (Default false) — ohne das bricht ein
  Datenträger-übergreifender Move serverseitig mit `code=409`/
  `msg="CROSS_DEVICE"` ab, statt unbestätigt zu kopieren (Verteidigung
  gegen den Fall, dass der Client die Preview übersprungen hat oder sich
  der Zustand zwischen Preview und Ausführung geändert hat).
- **Frontend (`admin.js`):** `handleMoveSubmit()` ruft VOR dem eigentlichen
  Move immer erst `resolveMoveConfirmation()` auf (neue Funktion), die
  `/api/items/move-preview` abfragt. Bei `anyCrossDevice=false` (der
  Normalfall) passiert nichts Sichtbares — direkt weiter wie bisher. Bei
  `anyCrossDevice=true` zwei nacheinander gestellte `appConfirm()`-Dialoge
  (User-Vorgabe, exakte Wortwahl):
  1. „Wirklich physisch verschieben?" — Ja → `allowCrossDevice:true`,
     Ziel-Bibliothek bleibt wie vom User gewählt.
  2. Bei Nein: „Auf gleicher Quelle verschieben?" (OK) vs. „Abbrechen"
     (Cancel) — OK setzt `targetLibraryId` explizit auf `ctx.libId` (die
     Quell-Library) zurück und `allowCrossDevice:false`: **exakt** das
     Verhalten, das beim Auslöser-Vorfall unbeabsichtigt geschah, jetzt
     aber als bewusste, informierte Wahl statt eines stillen
     Nebeneffekts. Cancel bricht komplett ab (kein Request geht raus).
  Ein `CROSS_DEVICE`-Serverfehler (der seltene Race-Fall: Zustand hat sich
  zwischen Preview und Move geändert) zeigt einen verständlichen Text statt
  des rohen Codes.
  Undo (`renameUndo`) und der einfache Auto-/Manual-Rename-Pfad
  (`executeRename`, immer selbes Verzeichnis) rufen `RenameOnDisk`
  unverändert mit `false` bzw. (Undo, bewusst) `true` auf — ein Undo eines
  bereits gerecordeten, Geräte-übergreifenden Moves darf ohne erneutes
  Zwischenfenster zurücklaufen, der User hat mit dem Undo-Klick schon
  entschieden.
- **Einmalige manuelle Korrektur der 126 betroffenen Dateien** (nicht über
  die App, weil ein Move innerhalb derselben Library den physischen Root
  strukturell nie wechselt — das hätte auch das neue Zwischenfenster nicht
  geändert, siehe „Root-Auflösung" oben): Dateien wurden per SSH direkt auf
  dem Server von Big18 auf das Array verschoben (`mv`, cross-device-fähig)
  und `items.path` für die betroffenen 126 Zeilen direkt per SQL
  aktualisiert (`rel_path` blieb gleich, nur der physische Präfix
  wechselte). Kein `rename_history`-Eintrag für diese manuelle Korrektur
  angelegt (kein Undo über die App-UI dafür verfügbar) — bewusste
  Ausnahme, keine Vorlage für künftige Wartungsaktionen.
- Tests: `internal/rename/rename_test.go`
  (`TestIsCrossDevice_SameFilesystem`,
  `TestIsCrossDevice_NonExistentTargetDirWalksUpToExistingAncestor`,
  `TestCopyAndRemove`, bestehende `RenameOnDisk`-Tests auf die neue
  Parameter-Signatur angepasst). Echtes `EXDEV` lässt sich in der
  Test-Suite nicht simulieren (bräuchte zwei echte Mounts) — die
  Copy-Fallback-Logik selbst (`copyAndRemove`) ist aber direkt getestet.

#### Ziel-Quellordner explizit wählbar (seit 2026-09-14, LIVE 1.3.25)

- **Auslöser:** direkt nach dem Zwischenfenster-Fix (oben) verschob der
  User eine weitere Einzeldatei — KEIN Zwischenfenster kam, obwohl er das
  erwartet hatte. Kein Bug: das Ziel blieb korrekt auf Big18 (derselben
  externen Platte), weil er als Ziel-Bibliothek weiterhin „a" gewählt hatte
  — bei einer Multi-Path-Library wie „a" (Quellordner sowohl auf dem Array
  als auch auf zwei externen Platten) bleibt ein Move innerhalb derselben
  Library IMMER auf dem aktuellen physischen Root (siehe „Root-Auflösung"
  oben) — das Zwischenfenster warnt nur vor einem tatsächlich bevorstehenden
  Wechsel, und hier stand keiner bevor. User-Folgeanfrage: „Ich will aber
  auch innerhalb auf andere Quellen verschieben können. Extern ist aktuell
  nicht gesichert. Daher will ich wichtige aufs Array schieben."
- **Fix:** `resolveMoveTarget` bekommt einen neuen, optionalen Parameter
  `targetRoot string` — ein exakter `library_paths`-Eintrag der
  Ziel-Bibliothek. Wenn gesetzt, gewinnt er IMMER als physischer Root,
  UNABHÄNGIG davon, ob `destLibraryID == it.LibraryID` ist — durchbricht
  bewusst die alte Grenze „Verschieben über zwei Quellordner DERSELBEN
  Library hinweg nicht unterstützt": die war ursprünglich eingebaut, weil
  ein impliziter Wechsel den User überraschen könnte (siehe Kommentar) —
  jetzt ist der Wechsel explizit gewählt UND läuft durch dasselbe
  Zwischenfenster (`/api/items/move-preview` erkennt den daraus
  resultierenden Datenträgerwechsel ganz normal). Leer (`""`, Default) =
  unverändertes altes Verhalten, volle Rückwärtskompatibilität.
  `moveItem`/`moveItemsBulk`/`moveItemsPreview` haben alle das neue
  `targetRoot`-Body-Feld bekommen.
- **Frontend:** neues, nur bei Multi-Path-Bibliotheken sichtbares Dropdown
  „Ziel-Quellordner (Datenträger)" im Verschieben-Dialog (`#moveRootRow`,
  `#moveRootSelect` in `index.html`) — `loadMoveRootList()` (`admin.js`)
  fragt `GET /api/libraries/{id}/paths` (bereits vorhandener Endpoint,
  bisher nur vom Library-Manager für die Multi-Path-Bearbeitung genutzt)
  ab; bei `<2` Pfaden bleibt die Zeile versteckt (der Regelfall — die
  meisten Libraries sind Single-Path). Läuft neu sowohl beim
  Dialog-Öffnen als auch bei jedem Wechsel der Ziel-Bibliothek. Der
  gewählte Pfad geht als `targetRoot` in Preview UND tatsächlichen Move.
  Wählt der User im Zwischenfenster „Auf gleicher Quelle verschieben"
  (siehe oben), wird `targetRoot` dabei bewusst wieder auf `""` geleert —
  sonst würde die (gerade verworfene) Ziel-Quellordner-Auswahl aus dem
  Dropdown den automatischen Fallback überschreiben.
- Kein neuer Go-Test (reine Parameter-Durchreichung + eine zusätzliche
  Validierung in `resolveMoveTarget`, kein eigenes API-Test-Harness in
  diesem Repo für HTTP-Handler-Ebene vorhanden) — Build/Vet/Test-Suite
  bleibt grün, Verhalten am echten Server verifiziert.

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
- **Edit-Metadata-Dialog (✏ Pencil)** im Detail-Dialog fuer Admins, auch in
  Privat-Libs verfuegbar (seit 2026-05-16). Speichern triggert
  `POST /api/items/{id}/metadata-manual` → Server legt `tmdb_type=custom`-
  Eintrag an (`TMDBID = -itemID`) und bindet das Item. Bei Privat-Libs:
  Vorbefuellung mit Dateiname ohne Endung als Default-Titel, plus
  releasedAt und durationSec, damit der User schnell einen sprechenden
  Titel fuer „yt-dlp-2024-03-15.mp4" eintragen kann.
  **🖼 Poster-Button jetzt auch bei unmatched Items (seit 2026-09-06,
  User-Feedback: "Das Feld Metadaten bearbeiten bei einer Datei, welche
  nicht zugeordnet ist, unterscheidet sich von einer zugeordneten Datei.
  Das soll identisch sein!!")**: der Button war bei `isNew` (kein
  `metadataId`) bisher komplett ausgeblendet — Begründung im (jetzt
  veralteten) Code-Kommentar war "Poster geht über metadata.id, die es für
  ein neues Item noch nicht gibt". Seit dem generalisierten Poster-Picker
  (Serien-Poster-Feature, nimmt eine explizite metadataId + Callback
  entgegen) ist das lösbar: `openPosterPickerFromEditDialog()`
  (`matching.js`) speichert bei einem `isNew`-Item zuerst automatisch die
  aktuellen Formularwerte (derselbe `POST .../metadata-manual`-Call wie der
  reguläre Submit, nur ohne den Dialog zu schließen), aktualisiert
  `state.currentItem.metadataId` + Dialog-Titel auf "Metadaten bearbeiten"
  und öffnet danach den Picker darauf. Button-Text wechselt entsprechend
  zwischen "🖼 Poster hinzufügen" (neu) und "🖼 Poster ändern" (bestehend) —
  Verhalten sonst identisch für beide Fälle, wie vom User gefordert.
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
      - /mnt/user:/media        # LIVE-Stack OHNE :ro (Goldfish darf Media löschen), s. „Volumes"
    environment:
      - VP_LISTEN=:8096
      - VP_CONFIG_DIR=/config
      - TZ=Europe/Berlin
volumes:
  videoplayer_config:
```

## Server-Hardware (gemessen 2026-09-17, nicht raten)

Host **Tower**, Unraid OS 7.2 (Kernel 6.12.87), Docker 29.3.1, Portainer 2.39.4:

- **CPU: Intel Core i5-13500** (Raptor Lake), **20 Kerne**, **31,2 GB RAM**
- **iGPU: UHD 770** — der aktiv genutzte Transcoder (`hwaccel.backend: vaapi`,
  `/dev/dri/renderD128`, Intel iHD 23.1.1). Kann laut `vainfo` im laufenden
  Container: H.264, **HEVC Main/Main10/Main12**, **VP9 (enc+dec)**, **AV1-Decode**,
  15 Encode-Entrypoints.
- **Zusätzlich verbaut: NVIDIA Quadro P400** (NVENC verfügbar, Treiber 580.159.03,
  `runtime: nvidia` im Stack). **Wird NICHT genutzt und ist der iGPU unterlegen**
  (Pascal 2017: kein HEVC-10-Bit-Encode, kein AV1, kein VP9, nur 2 GB VRAM).
  `hwaccel.go` wählt ohnehin **genau EIN** Backend (`Selected`, Reihenfolge
  vaapi > nvenc > software) — eine echte Verteilung auf zwei GPUs existiert im
  Code nicht und wäre ein Umbau auf Pool-Logik mit Zuweisung pro Session.
  **Die 4 stabilen 4K-Streams stammen allein von der iGPU.**
- `SwapLimit: false` → **kein `memswap_limit`** in Compose-Dateien verwenden.

## 🔜 Hier weitermachen (Stand 2026-09-17)

**Erledigt und LIVE:**
- v1.4.1: Transcode-Limit (App) + Container-Deckel in Stack 37
- v1.4.2: **gewichtetes Budget** (siehe „Laufzeit"), Protokoll-Paginierung
  repariert, Erklärtexte entpersonalisiert

**⚠ Regel für UI-Texte (User-Vorgabe 2026-09-17):** in Erklärtexten darf
**nichts stehen, was nur für diesen einen Server gilt.** Goldfish ist ein
öffentliches Projekt — „auf dieser Hardware sind 4 stabil" ist für jede
andere Installation schlicht falsch. Stattdessen das Prinzip erklären und
sagen, wie man den passenden Wert selbst ermittelt. Ebenso keine echten
Namen, Mail-Adressen oder IPs/Subnetze in Platzhaltern (gefunden und
ersetzt: LibreTranslate-Platzhalter zeigte das echte Subnetz).

**Offen:**

1. ~~Lasttest mit mehreren echten parallelen Streams~~ **erledigt
   2026-09-17**, siehe „Lasttest 2026-09-17" weiter unten. Kernergebnis:
   der Server stürzt nicht mehr ab (Load 178, Kernel durchgehend
   erreichbar, kein OOM, kein Neustart). 4 parallele 4K laufen mit 1,45×
   Echtzeit. **Noch offen: ein sauberer 6er-Lauf ohne parallel laufenden
   Paritätscheck** — der hat die 6er-Messung verfälscht.
2. ~~Container-Log ist mit `[enrich]`-Warnungen geflutet~~ **erledigt
   2026-09-17** (v1.4.3): `logMatchFailures` gibt jetzt eine
   Zusammenfassung je Lauf statt einer Zeile je Datei aus, siehe
   „Laufzeit". **Nach dem Deploy prüfen**, ob im Log wieder
   `[transcode]`-Zeilen sichtbar bleiben — dann lässt sich endlich
   auswerten, welche Profile im Alltag wirklich laufen.
3. **GPU-Kaufberatung:** Empfehlung bleibt **Intel Arc A380** (~110-130 €) —
   AV1-Encode, kein Session-Limit, und vor allem derselbe VAAPI-Pfad wie die
   iGPU (kein Code-Umbau; die P400 bräuchte den separaten NVENC-Pfad).
   **Erst messen, dann kaufen** (Punkt 1). Kostenloser Zwischenschritt: die
   P400 ausbauen — sie wird nicht genutzt und ist der iGPU unterlegen.
4. **Nutzungszahlen sind noch nicht aussagekräftig** (User-Hinweis
   2026-09-17): Goldfish ist zwar live, wird aber noch nicht im vollen
   Umfang genutzt. Statistiken aus dem Aktivitätsprotokoll taugen derzeit
   nicht als Entscheidungsgrundlage.

**Ebenfalls offen (FireTV-App, aus dem dortigen Repo):** Suchfeld zeigt den
Begriff nach dem Suchen nicht mehr an, Player-Options-Dialog (zweimal BACK)
noch nicht mit echter Fernbedienung bestätigt, Trailer-Wiedergabe ungebaut.

## 🔬 Lasttest 2026-09-17 — Ergebnisse und eine wichtige Störgröße

**Aufbau:** N parallele 4K→4K-Umwandlungen (HEVC 10-Bit-Quelle, 90 s
Material), jeweils mit `timeout` und `nice -n 19` abgesichert, bei sonst
leerem Server (`docker top` vorher: 0 ffmpeg).

| Parallel | Dauer für 90 s | API-Erreichbarkeit während des Tests |
|---|---|---|
| 4 | 62 s (1,45× Echtzeit) | ~24 s lang Timeouts, danach normal |
| 6 | (nicht sauber messbar) | **über 4 Minuten komplett tot** |

**⚠ Störgröße, die das 6er-Ergebnis wertlos macht: während des Tests lief
ein Unraid-Paritätscheck** (`mdcmd status` → `mdResyncAction=check P Q`
über 19,5 TB). Die Last kam nachweislich NICHT von den Transcodes:
`pgrep -c ffmpeg` zeigte während der Nicht-Erreichbarkeit **0**, die CPU
ging an `unraidd0` (75 %), `mdrecoveryd` und Dutzende
`btrfs-endio`-Kworker. Load Average stieg auf **178 bei 20 Kernen**.
**Vor jedem künftigen Lasttest `mdcmd status | grep mdResyncAction`
prüfen** — läuft dort ein Check/Rebuild, ist jede Messung Makulatur.

**Was der Test trotzdem belegt:**

1. **Der Server stürzt nicht mehr ab.** Trotz Load 178 blieb der Kernel
   durchgehend erreichbar (Ping + SSH + TCP-Handshakes auf 9000/2202 die
   ganze Zeit ok), kein OOM (`State.OOMKilled=false`), kein
   Container-Neustart (`RestartCount=0`), Erholung ohne jeden Eingriff.
   **Das ist der Unterschied zum 2026-09-16, als der ganze Host neu
   gestartet werden musste** — die Container-Deckel (mem_limit/cpus/
   cpu_shares) wirken.
2. **Alle ffmpeg-Prozesse wurden sauber aufgeräumt** — nach beiden Läufen
   0 verwaiste Prozesse.
3. **⚠ HTTP stirbt lange vor dem Kernel.** Die API antwortete schon nicht
   mehr, während der Host über SSH tadellos bedienbar blieb. **Für die
   Diagnose heißt das: „Goldfish antwortet nicht" ≠ „Server abgestürzt".
   IMMER zuerst per SSH prüfen** (`ssh -p 2202 root@<host>` → `uptime`,
   `pgrep -c ffmpeg`, `mdcmd status`), bevor jemand hart neu startet — ein
   unnötiger Reboot zieht eine mehrstündige Paritätsprüfung nach sich.

**Konsequenz für den Default:** 4 gleichzeitige 4K-Umwandlungen laufen mit
1,45× Echtzeit zwar durch, kosten aber bereits spürbar API-Reaktivität.
Der Default von 4 ist damit eher eine Obergrenze als ein Komfortwert und
deckt sich mit der ursprünglichen Beobachtung des Users („4 liefen gut,
8 waren zu viel").

**Offen:** ein sauberer 6er-Lauf ohne parallelen Paritätscheck.

- **⚠ Der enrich-Worker flutet das Log NICHT mehr je Datei** (gefixt
  2026-09-17): `PendingItems`/`PendingFolders` liefern bei JEDEM Lauf (alle
  5 Minuten) erneut alle Items mit `metadata_id IS NULL` — also dauerhaft
  dieselben, nicht matchbaren Dateien. Eine Logzeile je Datei ergab
  gemessen **300 von 301 Logzeilen** („kein Episodenformat SxxExx im
  Namen", vor allem alte Serien ohne SxxExx-Schema). Das verdrängte
  `[transcode]`-Zeilen binnen Minuten aus dem Docker-Log-Puffer und machte
  die Diagnose echter Störungen unmöglich — bei einem Problem war die Spur
  längst überschrieben. Jetzt sammelt `logMatchFailures` die Gründe und gibt
  EINE Zusammenfassung je Lauf aus (Anzahl + ein Beispielpfad je Grund,
  nach Häufigkeit sortiert). **Regel: in Schleifen über Bestandsdaten nie
  je Element loggen** — die Zeilenzahl muss von der Zahl der GRÜNDE
  abhängen, nicht von der Datenmenge. Tests:
  `internal/enrich/log_summary_test.go`.

  **⚠ Nachtrag v1.4.4 — der erste Versuch lief ins Leere:** TMDB-Fehler
  enthalten die angefragte URL (`TMDB GET /tv/4454/season/9/episode/12:
  {…}`), sind also für JEDE Episode ein anderer String und damit ein
  eigener „Grund". Im Live-Log standen nach dem Deploy von v1.4.3 wieder
  200 Zeilen, nur mit „1 ×" davor. **Wer Fehlermeldungen gruppiert, muss
  sie vorher normalisieren** (`normaliseReason`: angehängte JSON-Antwort
  abschneiden, API-Pfade auf den Endpunkt-Typ reduzieren). Zusätzlich ein
  Deckel von 10 Grund-Zeilen je Lauf, damit unerwartete Vielfalt das Log
  nicht erneut flutet. Die echten Live-Logzeilen stehen als Testdaten in
  `TestNormaliseReasonGroupsTMDBErrors`.
- **⚠ `probeSubtitleCodec` braucht einen Kontext mit Timeout** (gefixt
  2026-09-17): der ffprobe-Aufruf lief als nacktes `exec.Command` ohne
  Abbruchmöglichkeit, obwohl die ffmpeg-Extraktion unmittelbar darunter
  längst `CommandContext` nutzte. Brach der Client ab, lief das ffprobe
  weiter; bei einer Datei auf einer schlafenden UD-Platte blockiert es, bis
  die Platte anläuft. Jetzt `CommandContext` mit dem Request-Kontext plus
  eigenem 20-s-Limit. **Bei jedem neuen `exec`-Aufruf im Request-Pfad
  prüfen, ob er abbrechbar ist.**

- **⚠ Tote Sitzungen müssen SOFORT aus dem Pool** (gefixt 2026-09-17,
  v1.4.5): der GC prüfte nur den Leerlauf (30 Min) — eine Sitzung, deren
  ffmpeg nach zwei Sekunden an der Datei gescheitert war, blockierte also
  eine halbe Stunde lang ihren Platz im Budget UND in der
  Sitzungs-Obergrenze. Live beobachtet: mehrere gescheiterte Versuche an
  einer WMV-Datei summierten sich, bis eine gesunde Wiedergabe mit
  „Sitzungs-Obergrenze erreicht (12)" abgelehnt wurde — **obwohl real kein
  einziger ffmpeg-Prozess mehr lief.** Jetzt räumt sowohl der GC-Lauf als
  auch `StartOrGet` (vor der Budget-Prüfung, der GC läuft nur minütlich)
  alles weg, was `Done()` meldet. Tests:
  `TestDeadSessionsFreeTheirBudgetSlot`.
- **⚠ Der Software-Rückfall darf die Grafikeinheit NICHT mehr anfassen**
  (gefixt 2026-09-17, v1.4.5): `RetryWithSoftwareDecode` dekodierte zwar per
  CPU, lud die Bilder danach aber per `hwupload` zurück auf die
  Grafikeinheit und encodierte mit `h264_vaapi`. Für den Zweck eines
  Rückfalls ist das nutzlos — er existiert ja gerade für Dateien, welche die
  Hardware nicht kann. Live gescheitert an einer **WMV3-Datei**: ffmpeg
  meldete „No support for codec wmv3 profile 1" + „Failed setup for format
  vaapi", in BEIDEN Anläufen, die Wiedergabe war tot statt langsam.
  **`vainfo` listet auf der UHD 770 kein `VAProfileVC1*`** — Intel hat den
  VC-1/WMV-Decoder ab Gen 12 gestrichen (betrifft alle WMV/VC-1-Altbestände).
  Jetzt ist der Rückfall rein CPU-seitig (`libx264 -preset veryfast`, CPU-
  Filter `bwdif`/`scale`); der alte halbe Weg ist ersatzlos entfallen.
  Tests: `TestStreamingSoftwareDecodeFallback`,
  `TestSoftwareFallbackUsesCpuFilters`.
- **⚠ `Session.Stop()` ist jetzt nil-sicher**: `s.cancel()` auf einer
  Sitzung ohne Kontext riss den ganzen Server mit — für eine reine
  Aufräumfunktion ein unnötiges Risiko (beim Testen des GC-Fixes
  aufgefallen).

## Bekannte Probleme & Lösungen (Decision Log)

> Vollständiger Decision-Log ausgelagert in **`DECISIONS.md`** (wird nicht automatisch
> in den Kontext geladen — bei konkreten Debugging-Fragen gezielt lesen).
>
> Kurz-Index der wichtigsten Einträge (nach Kategorie):
> - **Transcode/HLS:** fresh=1-Mechanismus, Buffer-Cycling (_t-Token), Von-Anfang-Session-Reset, HLS-Segment-Query-Params, liveui:false, forcePlayerDuration, Pause>5Min-Session-GC (fehlendes Touch() im Progress-Poll)
> - **Frontend/Emoji:** 🗑/🎞 als Tofu-Box (Font-Fallback-Bug) — VS16 reichte NICHT, final als SVG-Icons gelöst (ICON_TRASH_SVG/ICON_FILM_SVG in helpers.js)
> - **Frontend/CSS-Sticky:** body{height:100%} killt position:sticky nach 1 Viewport-Höhe — body{min-height:100%} verwenden
> - **Trickplay:** -skip_frame nokey, VAAPI-format=nv12 für 10-bit HDR, Software-Fallback-Trigger, Timeout-Caps nach Auflösung
> - **Android:** Transcode-Seek (virtualOffset+Session-Restart), Lib-Flash/Privat-Sort (sync load), User-Isolation (ownerUsername), FFmpeg-Extension (nextlib), NoDeclaredBrand-MP4
> - **Parser/Enricher:** Numerische Episoden-Codes, Obfuskierte Dateinamen (Deleet+Longest-Token), Sample-Skip, Re2-Lookahead-Verbot
> - **DB/SQL:** NATSORT (nicht NATURAL!), Migration-Reihenfolge (ALTER vor INDEX), Endlos-Backfill (cast_fetched_at)
> - **Sonstiges:** Mask-Save-Roundtrip (API-Keys nicht zurückgeben), DOM-Builder nicht async, chi HEAD 405, Stack-Env mitsenden
> - **Bugfix-Chroniken (seit 2026-09-13):** die Root-Cause-Erzählungen zu 47 Fixen,
>   die vorher hier im Volltext standen — ACL-Leaks der Worker-Status-Endpoints,
>   Musik-Album-Gruppierung (Phantom der Oper), Cover-Art/`-map 0:V:0`,
>   Staffel-Ansicht-Merker, SeekBar-Listener, Compat-Download-Bitrate u. a.
>   In CLAUDE.md steht jeweils nur noch die abgeleitete Regel.
> - **Refactor-Serien (seit 2026-09-13):** Schritt-für-Schritt-Protokolle der
>   Code-Review 2026-09-06 (LIVE 1.2.8–1.2.14) und des Frontend-Modul-Splits
>   2026-04-30. Konventionen daraus: „Refactor-Historie & Code-Konventionen".

## API-Referenz

Vollstaendige Routenliste inkl. Admin-Gating: `internal/api/router.go` (`grep -n "r\." internal/api/router.go`). Body-Parameter je Endpoint stehen als Kommentare in den jeweiligen Handlern in `internal/api/*.go`.

## Refactor-Historie & Code-Konventionen

Zwei abgeschlossene Aufräum-Serien: **Frontend-Modul-Split 2026-04-30**
(app.js 7531 → 1371 Zeilen, 13 Module extrahiert) und **Code-Review 2026-09-06**
(7-Punkte-Liste, LIVE 1.2.8–1.2.14, kein Rollback — `internal/store/sqlite.go`
3091 → 204 Zeilen, aufgeteilt auf `schema.go`/`items.go`/`folders.go`/
`libraries.go`/`metadata.go`/`settings.go`/`trickplay_status.go`,
`grid.js loadItemsBody` 1206 Zeilen → Dispatcher mit 16 Branches,
`player.js` → `player-trickplay`/`-transcode-seek`/`-buffer`,
`enrich/worker.go` → `matching.go`, `views.js`' Musik-Views → `music.js`).
Der aktuelle Stand steht unter „Verzeichnisstruktur"/„Frontend-Modul-Layout";
die Schritt-für-Schritt-Chronik in DECISIONS.md.

**Konventionen, die aus diesen Serien stammen und weiter gelten:**

- **Modularisierung nur INTERN** — weitere Go-Dateien im selben Package bzw.
  weitere JS-Module. **Explizit KEINE separaten Repos/Go-Module:** Goldfish
  bleibt bewusst Single-Binary/Single-Container.
- **`store`-Methoden loggen NIE selbst** — das Package importiert nirgendwo
  `"log"`, Logging ist Aufgabe der Aufrufer. `attachMetadata`/
  `attachVariantCounts` schlucken DB-Fehler daher bewusst als Soft-Fail
  (Poster/×N-Badge fehlen dann einfach, kein harter Fehler).
- **`biome lint --write` NICHT blind vertrauen** — die
  `noUnusedVariables`-Regel kennt das global-Window-Scope-Modulmuster dieses
  Projekts nicht und schlug vor, `appPrompt` in `_appPrompt` umzubenennen; das
  hätte den globalen Aufruf aus anderen Modulen gebrochen. Jeden Vorschlag
  einzeln gegen die Datei prüfen.
- **Datei-Verschiebungen per Skript + Multiset-Diff verifizieren** (sortierte
  Zeilen alt vs. neu, Leerzeilen/Package-/Import-Zeilen rausgefiltert) statt
  manuellem Copy-Paste — bei ~1200 Zeilen ist das Übertragungsrisiko sonst zu
  hoch. Grenzen von Go-Funktionen per `go/parser` bestimmen, nicht per
  Klammer-Zählen.
- **`./scripts/check-frontend.sh && go build ./... && go test ./...`** vor
  jedem Commit (siehe „Pre-Deploy-Schutz" oben — `node --check` niemals
  überspringen).
- **Beim Trimmen mit `awk`:** bei Multi-Block-Extraktionen müssen ALLE
  `in_block=0`-Resets VOR der generischen Skip-Aktion stehen, sonst verschluckt
  der Skip aus Phase A alles bis Dateiende (war einmal passiert, admin.js).

**Offene Lint-Bestandsaufnahme** (Stand 2026-09-06, kein akuter Bedarf —
Fundgrube für künftige Aufräum-Sessions): `golangci-lint run ./...` 20 Funde
(10 errcheck, meist unkritisches `defer x.Close()`; 8 staticcheck-Stilhinweise;
2 unused — `scanner.go musicExt` und `api/subtitle_gen.go maskKey` sind toter
Code, vorbestehend). `biome lint` über alle JS-Module: 128× `noUnusedVariables`,
192× `useOptionalChain`, 115× `useTemplate` (alles Stil), 33× `noDoubleEquals`
(`==`/`!=` statt `===`/`!==` — echte Typkoerzitions-Risikoklasse, jede Stelle
einzeln prüfen, nicht pauschal automatisierbar).

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
