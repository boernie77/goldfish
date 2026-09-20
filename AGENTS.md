# Goldfish — Projektregeln

> **`CLAUDE.md` ist absichtlich nur der Import-Shim `@AGENTS.md` — nicht beschreiben.**
> Regeln, die in jeder Session gelten, gehören in diese Datei; Detailwissen in einen
> Themenskill unter `.claude/skills/<thema>/SKILL.md`. Ein Wächter-Hook
> (`~/.hermes/hooks/claude_md_shim_guard.py`, registriert in `~/.claude/settings.json`)
> lehnt Schreibzugriffe auf `CLAUDE.md` ab und setzt sie bei Drift automatisch zurück —
> auch nach Änderungen per Editor oder Skript. Claude-Code-Spezifisches, das Hermes
> bewusst nicht sehen soll, gehört nach `.claude/rules/`.

**Diese Datei wird bei jedem Agentenstart vollständig geladen — deshalb kurz halten.**
Detailwissen liegt in den Skills (Tabelle unten), nicht hier. Neue Erkenntnisse gehören in den
passenden Themenskill, nicht in diese Datei; sie ist bewusst unter 20.000 Zeichen (harte
Ladegrenze in Hermes) und unter der von Anthropic empfohlenen 200-Zeilen-Marke.
Volltext der früheren Sammel-`CLAUDE.md` (309.865 Zeichen, Stand 2026-09-20): Skill `goldfish-full-archive`.

---

## Produkt & Stack

**Goldfish** ist ein schlanker, Jellyfin-ähnlicher Video-Streaming-Server für einen Unraid-Host.
Das Go-Modul, das Docker-Image und der Stack heißen aus Kompatibilitätsgründen weiterhin
`videoplayer` / `simple-videoplayer` — nur das UI-Branding ist „Goldfish".
Eine Umbenennung des Modulpfads ist bewusst unterlassen (nur Churn).

- Go 1.22, `net/http` + `chi/v5`, `modernc.org/sqlite` (pure Go, kein cgo)
- Frontend: Vanilla HTML/CSS/JS + Video.js 8.x (lokal gebündelt, kein CDN), 10+ fokussierte JS-Module
- Video: ffmpeg mit Intel VAAPI (iHD) + `libx264`-Fallback
- Auth: bcrypt + HttpOnly-Session-Cookies, zusätzlich OIDC
- Container: Debian bookworm-slim, Multi-Stage, `CGO_ENABLED=0`
- HTTP im Container auf `:8096`, am Host gemappt auf **`8098`**

## Versionierung (Pflicht, User-Vorgabe)

- Konstante `appVersion` in `internal/api/router.go`. **Bei JEDEM Deploy die Patch-Stelle um 1
  erhöhen**, im selben Commit. Ausgeliefert als `version` in `/api/health`, angezeigt im
  Zahnrad-Menü-Fuß (`#drawerVersion`).
- Zwei bewusst dokumentierte Ausnahmen vom +0.0.1-Schema: 1.0.94 → **1.2.0** und 1.2.53 → **1.3.0**
  (beide ausdrücklich so gewünscht). Danach läuft die Patch-Regel auf der neuen Basis weiter.
- Die App-Repos (Android/Apple/Linux/FireTV) zählen unabhängig davon.

## Repo-Sichtbarkeit — vier von fünf Repos sind ÖFFENTLICH

| Repo | sichtbar | seit |
|---|---|---|
| `boernie77/goldfish` (Server, MIT) | öffentlich | 2026-09-05 |
| `boernie77/goldfish-apple` (MIT) | öffentlich | 2026-08-18 |
| `boernie77/goldfish-android` (GPLv3) | öffentlich | 2026-08-18 |
| `boernie77/goldfish-linux` (MIT) | öffentlich | 2026-09-12 |
| `boernie77/goldfish-firetv` | privat | — |

**Bei jeder Änderung prüfen, ob committeter Code oder Kommentare echte Namen, E-Mails, interne
IPs oder Secrets enthalten.** In diesem Dokument werden dafür konsequent Platzhalter benutzt:
`<UNRAID-LAN-IP>`, `<your-domain>`. Das ist Absicht — nicht durch echte Werte ersetzen.
**Sichtbarkeit nie aus einer Doku-Zeile übernehmen**, sondern nachsehen:
`gh repo view <repo> --json visibility`. (Ein Irrtum hier hat schon einmal einen Monat lang eine
interne LAN-Adresse in einem öffentlichen Repo stehen lassen.)

## Auth: OIDC/SSO ist LIVE und tabu

Die OIDC-Anbindung an Authentik ist deployt, getestet und im Browser wie in den Apps in Benutzung.
**Nicht „aufräumen", nicht „vereinfachen", nicht „modernisieren".**

- Issuer `https://auth.<your-domain>/application/o/goldfish/` — **Trailing-Slash NICHT trimmen**.
- Signing **RS256**, **niemals zurück auf HS256** (go-oidc lehnt das ab).
- Provider-`sub_mode` bleibt `user_email` — ein Wechsel auf `hashed_user_id` macht alle
  bestehenden User-Verknüpfungen ungültig.
- Email/Passwort-Login bleibt als Fallback erhalten. SSO ist additiv.
- Vier Stack-Env-Vars müssen gesetzt sein (`OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`,
  `OIDC_CLIENT_SECRET`, `OIDC_REDIRECT_URL`) — sonst 503 „SSO nicht konfiguriert".
- Provider nicht neu erstellen (Client-ID/Secret hängen im Portainer-Stack).
- Nicht löschen: `internal/api/oidc.go`, das `OIDC`-Feld in `router.go`, die beiden Routen in
  `auth.go`, `oidc_subject` im User-Schema, der `#ssoBtn`-Block in `login.html`.
- Live-Test: `curl -i https://goldfish.<your-domain>/api/auth/oidc/login` → erwartet **302**.

## Daten: Volume nicht anfassen

Das Volume ist als `external: true, name: videoplayer_videoplayer_config` deklariert. Der echte
Datenbestand (User-DB, Poster, Trickplay-Cache) liegt dort. **Wer das Compose neu schreibt und das
`external`-Mapping vergisst, mountet ein leeres Volume** — die Daten sind dann nicht weg, aber
nicht gemountet. Migrationen sind additiv und idempotent (`ALTER TABLE ADD COLUMN` via `addCol`,
Indizes **nach** den ALTERs).

## Stabilitätsgrenze: gleichzeitige Transcodes

Der wichtigste Schutz des Servers. Ein Test am 2026-09-16 ergab: **vier 4K-Transcodes liefen
stabil, acht rissen den kompletten Unraid-Host mit** (Reboot nötig, nicht nur Container-Neustart).

- Zwei Verteidigungslinien, **beide nötig**: App-Limit (`settings.max_transcodes`, Default 4) und
  gewichtete Kosten statt sturer Zählung (`transcodeCost`, Budget = Wert × 100).
- Am Limit liefert `StartOrGet` `ErrTooManySessions` → der API-Layer macht daraus **HTTP 503 +
  `Retry-After: 30`** mit lesbarer Meldung (bewusst kein 500 — es ist ein temporärer Zustand).
  **Eine abgelehnte Wiedergabe ist immer besser als ein toter Server.**
- Die Kostenfaktoren sind auf der echten Hardware gemessen (VAAPI, 60 s Material, zweifach
  wiederholt) — Tabelle im Skill `goldfish-playback`. **Nicht schätzen, dort nachsehen.**

## Deployment & Checks vor jedem Push

- **Portainer-Stack 37 `videoplayer`**, Endpoint 3 (`<UNRAID-LAN-IP>:9000`), Image
  `simple-videoplayer:latest`. Image-CI: selbst gehosteter Runner (`goldfish-ci`, Stack 38) baut
  bei jedem Push auf `main`.
- **Vor jedem Deploy prüfen, ob eine Transcode-Wiedergabe läuft** (ffmpeg-Prozess auf dem Server).
  Lieber den Deploy verschieben als eine laufende Wiedergabe zu killen.
- Keine lokale Go-Toolchain nötig — der Docker-Build via Portainer-API übernimmt das.
- **Vor jedem Commit:** `./scripts/check-frontend.sh && go build ./... && go test ./...`.
  `node --check` über alle embedded JS-Files **niemals überspringen** — das hat schon einen
  Anführungszeichen-Bug abgefangen, der die komplette Frontend-App tot gemacht hätte.

## API-Kompatibilität zu den Apps (stille Brüche)

Die Apps sind NICHT mit dem Server mitversioniert. Drei Regeln, die jede Session kennen muss:

1. `resumePosSec` ist **nicht** in der `getItem`-Antwort — eigener Endpoint `GET /api/items/{id}/resume`.
2. Download-Endpoint heißt **`/api/download/{id}`**, nicht `/api/items/{id}/download` (404).
3. Cast läuft über **`metadata_id`**, nicht `item_id`: `GET /api/metadata/{id}/cast`.


**Dauer-Constraints der Apple-App (die Server-API-Anfasser kennen müssen):** SSO läuft dort über
WKWebView mit persistentem `WKWebsiteDataStore`; auf tvOS öffnet `Menu` in der Toolbar zuverlässig
nichts (immer `.sheet`/`.confirmationDialog`); eine persistente Leiste darf auf iOS nie als
`.safeAreaInset` um eine `TabView` gelegt werden; kein Windows/Linux-Target. **Keine
Apple-Produktbegriffe (Mac, Apple TV, iPhone, iPad) im App-Namen oder Untertitel** — Apple hat
deswegen zweimal abgelehnt (Guideline 5.2.5). Details im Skill `goldfish-server-and-auth`.

Bei API-Änderungen die Client-Repos gegenprüfen: `GoldfishCore/GoldfishClient.swift` (Apple),
`data/api/GoldfishApi.kt` (Android **und** Fire TV — identischer Code, kein Auto-Sync),
`goldfish_linux/api.py` (Linux).

## Mehrbenutzer-Datentrennung (Kernanliegen des Betreibers)

Christian betreibt Goldfish als Familienserver (mehrere Konten, teils mit Kindersicherung).
**Private Kuratierung darf sich NIE vermischen — auch nicht für Admins.** Jede Änderung an
Filtern, ACL- oder FSK-Abfragen daraufhin prüfen. ACL/FSK wird im Handler gefiltert, nie
ungeprüft durchgereicht.

## Code-Konventionen

- **Modularisierung nur INTERN** — weitere Go-Dateien im selben Package bzw. weitere JS-Module.
  **Keine separaten Repos oder Go-Module:** Goldfish bleibt Single-Binary/Single-Container.
- **`store`-Methoden loggen NIE selbst** (Package importiert kein `log`); Logging ist Sache der Aufrufer.
- Frontend läuft bewusst über den gemeinsamen `window`-Scope mit `<script defer>` in festgelegter
  Reihenfolge — **kein** `<script type="module">`.
- `biome lint --write` **nicht blind vertrauen** (z. B. `noUnusedVariables` kennt das
  Window-Scope-Muster nicht). Jeden Vorschlag einzeln gegen die Datei prüfen.
- Datei-Verschiebungen per Skript + Multiset-Diff verifizieren, nicht per Copy-Paste.
- Bei `awk`-Trims: alle `in_block=0`-Resets VOR die generische Skip-Aktion setzen.

## Wo das Detailwissen liegt

| Skill | Wofür |
|---|---|
| `goldfish-playback` | Playback, HLS/Transcode, VAAPI, Trickplay, Intro-Erkennung, Player-UI, Shuffle, TMDB/Trailer, API-Routen, Name-Parser |
| `goldfish-library` | Auto-Scan, Scan-Ausschlüsse, Metadaten/Matching, Duplikate & Serien-Auto-Merge, NFO-Sidecars, Rename, Verschieben, Download & Löschen, Musik |
| `goldfish-web-ui` | Views, Filter-UI, Kacheln/Overlays, Werkzeugleiste, Einstellungen/Admin-UI, Playlists-Seiten, Bulk-Auswahl, Modul-Layout |
| `goldfish-users` | Benutzer & Zugriff, ACL/FSK, Playlists pro User, Statistik, Benachrichtigungen, Aktivitätsprotokoll, Backup/Restore |
| `goldfish-subtitles` | Whisper-Untertitel, OCR-Untertitel, Sidecar-Untertitel, Untertitel-Selector |
| `goldfish-deploy-ops` | Portainer/CI, Build-Flow, Volumes, DB-Schema, Hardware, Lasttest, Refactor-Historie, offene Punkte, Decision Log |
| `goldfish-server-and-auth` | Produktidentität, OIDC-Details, Constraints der Apps (Android/Apple/Linux/FireTV) |
| `goldfish-full-archive` | Vollständiges Original der früheren CLAUDE.md, verbatim — Fallback, wenn etwas fehlt |
| `android-feature-history` | Ältere Feature-/Bugfix-Chronik des Android-Clients (vor der Auslagerung in das App-Repo) |

Alle Skills liegen unter `.claude/skills/` und sind über den Symlink `.hermes/skills/` auch für
Hermes sichtbar (`.hermes/` ist git-ignoriert). Claude Code liest sie aus `.claude/skills/`,
Hermes nach `hermes skills trust` ebenfalls — eine Datei, zwei Agenten.

## Regel für neue Erkenntnisse

Neue dokumentationswürdige Erkenntnisse gehören **in den passenden Themenskill**, nicht in diese
Datei. Was hier steht, muss bei jedem einzelnen Start relevant sein — alles andere kostet nur
Kontext und senkt die Befolgungsrate.
