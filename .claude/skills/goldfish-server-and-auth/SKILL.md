---
name: goldfish-server-and-auth
description: "Use when touching Goldfish identity, OIDC/SSO with Authentik, or the client apps (Android, Apple, Linux, Fire TV). Contains the must-not-break auth rules."
---

# goldfish-server-and-auth

Produktidentität, OIDC-SSO-Regeln und die Constraints der App-Clients (Original-Kopfblock der CLAUDE.md).

---

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

**🌐 VIER der fünf Repos sind ÖFFENTLICH** (Sichtbarkeit am 2026-09-18 direkt
über die GitHub-API geprüft, nicht geschätzt):

| Repo | sichtbar | seit |
|---|---|---|
| `boernie77/goldfish` (Server, MIT) | öffentlich | 2026-09-05 (siehe Fußnote) |
| `boernie77/goldfish-apple` (MIT) | **öffentlich** | 2026-08-18 |
| `boernie77/goldfish-android` (GPLv3) | **öffentlich** | 2026-08-18 |
| `boernie77/goldfish-linux` (MIT) | öffentlich | 2026-09-12 |
| `boernie77/goldfish-firetv` | privat | — |

**Bei JEDER Änderung an einem dieser Repos prüfen, ob committeter Code oder
Kommentare echte Namen, E-Mails, interne IPs oder Secrets enthalten** — das ist
sofort für jeden sichtbar, nicht mehr nur theoretisch.

**⚠ Diese Datei behauptete bis 2026-09-18 fälschlich, `goldfish-apple` und
`goldfish-android` seien „privat".** Beide waren ab dem Tag ihrer Erstellung
(2026-08-18) öffentlich — einen privaten Zeitraum gab es nie. Der Irrtum hatte
eine konkrete Folge: in `goldfish-android` stand einen Monat lang die interne
LAN-Adresse des Heimservers in `network_security_config.xml` (am 2026-09-18
entfernt, Commit `2ec43a8`). Gerettet hat die Lage allein die explizite
Keystore-Warnung im Android-Block weiter unten. **Lehre: die Sichtbarkeit eines
Repos nie aus dieser Datei übernehmen, sondern im Zweifel nachsehen**
(`gh repo view <repo> --json visibility`) — eine Doku-Zeile altert lautlos, die
API nicht.

**Fußnote zum `goldfish`-Datum:** die GitHub-Events-API meldet für dieses Repo
ZWEI `PublicEvent`-Einträge am 2026-04-27 (Tag der Erstellung), nicht den hier
dokumentierten 2026-09-05. Zwei Einträge am selben Tag heißen: mindestens einmal
öffentlich → privat → öffentlich geschaltet. Ob danach noch eine private Phase
bis September lag, lässt sich nicht mehr belegen — die Events-API hält nur ein
begrenztes Fenster vor (ältester sonst erhaltener Eintrag: 2026-08-19). Das
Datum bleibt deshalb stehen wie bisher dokumentiert. **Praktisch irrelevant:
öffentlich ist öffentlich, die Prüfpflicht oben gilt so oder so** — nur als
Warnung, falls jemand dieses Datum je für eine „war zu Zeitpunkt X noch
privat"-Argumentation heranziehen will. Das taugt es nicht.

Der Modulpfad bleibt bewusst `videoplayer` (eine Umbenennung wäre nur noch
Churn).

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
> (eigenes Git-Repo `github.com/boernie77/goldfish-android`, **öffentlich**
> seit 2026-08-18, GPLv3),
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

