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
> Es gibt eine **Android-App** unter `/Users/christian/Projekte/GoldfishAndroid/`,
> die aktuell im **Internal-Testing-Track** der Google Play Console verteilt wird
> (NICHT öffentlich im Play Store) und auf einem Samsung-Tablet des Users sowie
> im Pixel-Tablet-Emulator läuft. Die App ist NICHT mitversioniert mit dem Server —
> wenn du eine API-Antwort änderst, kann die App stillschweigend brechen
> (Moshi-Parse-Error → leere Listen).
>
> **VOR** API-Änderungen prüfen:
> - Pfad geändert? → App-Repo `app/src/main/kotlin/com/goldfish/android/data/api/GoldfishApi.kt`
> - JSON-Feld umbenannt oder Typ geändert? → `data/model/Models.kt` (Moshi `@Json(name=…)`)
> - Neuer Endpoint? Optional, App kann ihn ignorieren.
>
> **Wenn du etwas brichst:** versionCode in `app/build.gradle.kts` erhöhen, neue
> AAB bauen (`./gradlew bundleRelease`), in Play Console Internal-Testing-Track
> hochladen. Dauert ~3 Min Build + 5 Min Play-Console-Prozessierung.
>
> **Git-Repo seit 2026-08-19:** `github.com/boernie77/goldfish-android` (privat).
> Release-Signing-Credentials (`goldfish-release.jks` + Passwort) liegen NICHT im
> Repo — `app/build.gradle.kts` liest sie aus `keystore.properties` (git-ignoriert,
> nur lokal, siehe `keystore.properties.example` für die Struktur). **Diesen
> Keystore NIE committen** — er signiert alle Play-Store-Updates.

## Server-API-Quirks, die die App kennt (NICHT brechen)

Diese drei Quirks sind in der App fest verdrahtet und gelten als „bekannte
Konvention" — nicht ändern, sonst stille App-Bugs:

1. **`resumePosSec` ist NICHT in der `getItem`-Antwort.** Es gibt einen separaten
   Endpoint `GET /api/items/{id}/resume` → `{positionSec: float}`. Die App holt
   beide Calls und merget. Der `GetItemFor`-SQL-Query in `internal/store/sqlite.go`
   listet `resume_pos_sec` bewusst nicht auf — Server hat ihn historisch nicht
   im Item-Modell exposed. Wenn du das ändern willst, ist es OK — aber die App
   verlässt sich aktuell auf den separaten Endpoint UND würde von einem Feld
   im JSON profitieren, nicht stören.
2. **Download-Endpoint heißt `/api/download/{id}`**, NICHT `/api/items/{id}/download`.
   Letzteres existiert nicht (404). Routing in `internal/api/router.go` Zeile 100.
3. **Cast-Endpoint via `metadata_id`, nicht `item_id`**: `GET /api/metadata/{id}/cast`.
   Bei Episoden liefert der Server automatisch Show-Hauptcast + Episoden-Gäste.

## Android-App-Featureliste

Ausführliche Feature-/Bugfix-Chronik (Stand 1.2.67 + Snapshot 1.1.3) ausgelagert
in den Skill `android-feature-history` — lädt bei Bedarf, z. B. wenn Details zu
einem konkreten vC-Versionsstand gebraucht werden. Die drei harten API-Quirks
oben gelten weiterhin immer.

## Was die App NICHT hat

- Kein OIDC. Login direkt per Email/Passwort (Cookie-Persistenz in SharedPrefs).
  Die App profitiert NICHT vom Authentik-SSO im Browser.
- Kein Cast/AirPlay (die Buttons im Player sind browser-only, in der App fehlen sie).
- Kein Admin (User-Verwaltung, Library-Manager, Scan, NFO-Bulk, Whisper-UI etc.).

---

# 🍎 Mac/iOS-App (GoldfishApple, seit 2026-08-17)

> **An jede Claude-Session, die Goldfish-Server-API anfasst:**
> Es gibt außer der Android-App auch eine **native Mac/iOS-App** unter
> `/Users/christian/Projekte/GoldfishApple/` (SwiftUI, `GoldfishMac` + `GoldfishiOS`
> Targets via `xcodegen` aus `project.yml`, gemeinsames Swift-Package `GoldfishCore`).
> **Seit 2026-08-19 eigenes Git-Repo:** `github.com/boernie77/goldfish-apple`
> (privat). Analog dazu `github.com/boernie77/goldfish-android` — beide getrennt
> vom Server-Repo (`goldfish`), nicht darin eingegliedert.

## Architektur-Kurzfassung
- macOS: App Sandbox AUS (`GoldfishMac.entitlements` = `<dict/>`, nach jedem
  `xcodegen generate` prüfen, wird sonst zurückgesetzt).
- Player läuft NICHT als `.sheet`, sondern als eigene `WindowGroup(id: "player"/
  "localPlayer")`-Szene (`openWindow(id:)` + `PlayerLaunchCoordinator.shared`
  hält die live Swift-Werte, da `RandomContext`/`[Item]` nicht sinnvoll
  `Codable` für `openWindow(value:)` sind) — Sheets unterstützen kein echtes
  `NSWindow.toggleFullScreen`.
- Custom `AppDelegate` (`Sources/GoldfishApp/AppDelegate.swift`) für Window-
  Lifecycle-Handling, das SwiftUI pur nicht bietet (Dock-Reopen, Space-Handling).
- Build: `xcodebuild -scheme GoldfishMac -configuration Debug -destination
  'platform=macOS' build`, dann App-Bundle aus DerivedData auf den Desktop
  kopieren zum Testen (kein Simulator für Mac-Target nötig).

## Gelöste Bugs (Stand 2026-08-19, Build 0100)
- **Fenster-Verschwinden-Bug** (viele Fixversuche, am Ende zwei echte Root-Causes):
  1. `PlayerLaunchCoordinator.pendingPlayer/pendingLocalPlayer` wurden beim
     Schließen nie auf `nil` zurückgesetzt → SwiftUI/AppKit-Fenster-Bookkeeping
     lief auseinander.
  2. Das Hauptfenster konnte über den grünen Button in einen eigenen nativen
     Vollbild-Space rutschen (`onScreen=false` im Diagnose-Log, Frame = exakt
     Bildschirmgröße) — landete dann auf einer anderen Space als der Player.
     Fix: `window.collectionBehavior = [.managed, .participatesInCycle,
     .canJoinAllSpaces]` (Ganzwert-Neuzuweisung, `.remove()`/`.insert()` auf
     der Property hat NICHT zuverlässig gehalten) + Fenster zieht sich beim
     Start auf `screen.visibleFrame` auf (fast Vollbild ohne echtes Fullscreen).
  - **Debugging-Lehre:** NSLog+Console.app hat trotz aktivem Streaming NIE
    einwandfrei funktioniert (0 Mitteilungen trotz Reproduktion) — Umstieg auf
    eigenes File-Logging (`~/Desktop/goldfish-window-debug.log`, append via
    `FileHandle`) war der Durchbruch. `Read`/`cat` auf `~/Desktop/*` scheitert
    aus dem Coding-Environment heraus an macOS-Datenschutz (EPERM) — User muss
    die Datei selbst öffnen/einfügen oder `! cat ...` selbst ausführen.
- **„Von Anfang" startete mitten im Video** (Transcode): identischer Root-Cause
  wie im Browser (siehe DECISIONS.md „„Von Anfang" startet mitten im Film") —
  Server matched eine bereits vorangeschrittene gecachte Transcode-Session.
  Fix mirrored `player.js`: `&fresh=1` (wenn Start=0) + `_t=<timestamp>`
  Cache-Bust an die Transcode-URL anhängen (`PlayerView.transcodeURLWithParams`).
- Per-User-Isolation für lokale Bibliotheken/Downloads/Shuffle-Scope (analog
  zum früheren Android-Bug, gleiche Klasse von Fehler: fehlender User-Filter).
- **Bibliotheks-Vorschaubilder offline weg** (mehrfach „gefixt", Build 165 die
  echte Ursache): in `LibrariesView.load()` wurde `previewURLs` NUR aus
  `loadPreviews()` befüllt, und das lief ausschließlich im Erfolgsfall von
  `fetchLibraries()`. Offline → `catch` → `previewURLs` leer → jede
  `LibraryCard` fiel auf den farbigen Gradienten-Kreis zurück. Die früheren
  Fixes (Offline-Lib-Liste in UserDefaults, eigener `GoldfishLibraryPreviews/`-
  Platten-Cache, `file://`-Handling in `PosterImage`) waren alle nötig, aber
  keiner hat den Cache-Read in den Offline-Zweig gehängt. Fix:
  `hydratePreviewsFromCache()` läuft jetzt IMMER (vor + unabhängig vom
  Netzwerk-Call). Zusätzlich (User: „genau die AKTUELLEN Bilder speichern"):
  `loadPreviews()` zieht nur noch EINMALIG ein Zufalls-Poster pro Bibliothek —
  ist die Cache-Datei da, wird sie behalten statt bei jedem Öffnen mit einem
  neuen Zufallsbild überschrieben. Bild ist damit online wie offline stabil.
- **Zufallsmodus-Auto-Weiter** (Build 173): `PlayerView` + `LocalPlayerView`
  starten am Videoende (`AVPlayerItemDidPlayToEndTime`-Observer) automatisch
  das nächste Zufallsvideo, wenn ein Zufallskontext aktiv ist (`randomContext`
  bzw. `randomPool`) → `jumpRandom(by:1)` / `jump(by:1)`, gleicher Pfad wie ⏭.
  `LocalPlayerView` hatte vorher keinen End-Observer; markiert bei Auto-Weiter
  jetzt auch als gesehen.
- **Gesehene Downloads löschen + Staffel-Gesehen + lokale Sternebewertung**
  (Build 174): (1) `DownloadsView`-Toolbar-„Alle löschen" ist jetzt ein `Menu`
  mit zusätzlichem „Alle gesehenen löschen" (`DownloadManager
  .deleteWatchedDownloads()` = nur `state==.done` + `cachedItem?.watched`;
  `watchedDownloadCount`). (2) `ShowSeasonsView.SeasonCard` hat einen
  Gesehen-Toggle, der ALLE vorhandenen Folgen der Staffel auf einmal
  markiert (`client.setWatched` je `episode.itemId`). (3) Lokale Bibliotheken:
  `LocalItem.rating` (0–3, Default → alte Indizes dekodieren, Rescan behält's),
  `LocalLibraryManager.setRating`, Kontextmenü-Bewertung + Stern-Overlay
  (oben rechts) auf `LocalItemCard` + `LocalRatingFilter` im Filter-Menü.
- **Ton-/Untertitel-Dropdowns im Detail-Dialog + Untertitel-Overlay**
  (Build 179): `ItemDetailView` hat zwei `Picker` (🔊 Tonspur = alle
  Audiostreams, 💬 Untertitel = NUR `MediaStream.isDisplayableGeneratedSub`,
  also codec `webvtt-ocr`/`webvtt-generated` — Bitmap-Subs PGS/VOBSUB werden
  gar nicht mehr angeboten). Streams jetzt aus `client.playback(itemId:)` statt
  `fetchItem` (nur der Playback-Endpoint liefert die erzeugten Untertitel mit).
  Auswahl → `preferredAudioIndex`/`preferredSubtitle` (`PreferredSubtitle`-Struct,
  `vttPath(itemID:)`) in `PlayerLaunchRequest` (macOS) UND `PlayerView(...)`
  (iOS). `PlayerView` rendert ein eigenes WebVTT-Overlay (`SubtitleCue`,
  `parseVTT`, `activeSubtitleText`, `loadSubtitleCues`) + Ein/Aus-Button in
  `PlayerControlsBar`. `currentTime` ist im Transcode-Modus bereits absolut
  (virtualOffset applied) → KEIN Cue-Shift nötig (Browser verschiebt die VTT
  dagegen um `-virtualOffset`). `GoldfishClient.fetchText(serverPath:)` neu.
- **Offline→online: Bibliotheken kommen nicht wieder, „Session abgelaufen"**
  (Build 166): der Text ist die wörtliche 401-Antwort des Servers
  (`internal/api/auth.go` — Cookie wird gesendet, aber die Session ist
  serverseitig weg/abgelaufen). App-seitige Fehler: (1)
  `RootView.refreshSessionStatus()` lief nur EINMAL beim Start — offline→online
  hat die Session nie neu abgeglichen. (2) `LibrariesView.load()`/`HomeView`
  behandelten den 401 als `isOffline=true` (falsch — der Server hat ja
  geantwortet) und zeigten bei leerem Cache die rohe Server-Meldung als
  Vollbild-Sackgasse OHNE Retry-Knopf und ohne Weg zurück zum Login. (3) nichts
  hat eine tote Session je nach `LoginView` geroutet. Fixe:
  `GoldfishClient.isAuthError()` + `markSessionInvalid()` (löscht lokalen
  Login-State + Host-Cookies → `RootView` zeigt `LoginView`); `RootView`
  gleicht die Session bei jedem Wechsel in den Vordergrund neu ab
  (`scenePhase == .active`, `authStatus` ist public/kein 401, wirft nur bei
  echtem Verbindungsproblem → kein Fehlalarm-Logout); `LibrariesView`/`HomeView`
  laden bei `scenePhase == .active` neu, unterscheiden Connectivity- vs.
  Auth- vs. sonstige Fehler und haben jetzt einen „Erneut versuchen"-Button
  (Offline-Banner ist zusätzlich antippbar).
- **SSO-Login in der Mac/iOS-App schlug still fehl** (Build 167): der
  `OIDCWebViewRepresentable`-Coordinator kopierte die WKWebView-Cookies EINMALIG
  im `didFinish` für `/` nach `HTTPCookieStorage.shared`. Das `Set-Cookie` aus
  der OIDC-Callback-Weiterleitung ist zu dem Zeitpunkt aber oft noch nicht im
  WKWebView-Cookie-Store (bekanntes WKWebView-Timing) → `goldfish_session` wurde
  nicht übernommen → `authStatus()` = `loggedIn:false` → App blieb auf dem
  Login-Screen, ohne Fehlermeldung. Fix: `syncCookies` pollt jetzt bis zu ~2,5 s
  (8 × 0,3 s), bis der `goldfish_session`-Cookie auftaucht; kommt keiner, meldet
  der Flow das explizit als Fehler statt still zu schließen.
  `LoginView.refreshAfterOIDC()` fasst zusätzlich 5× kurz nach (`authStatus`),
  bevor es aufgibt, und zeigt sonst eine klare Meldung.
- **SSO-Sheet war auf macOS leer** (Build 168, der eigentliche „SSO tut nichts"-
  Grund — kam VOR dem Cookie-Sync): `OIDCLoginView` setzte keine explizite Größe.
  Ein `NSViewRepresentable` (WKWebView) hat keine intrinsische Größe → das
  `.sheet` schrumpfte auf Header + „Abbrechen", die Authentik-Seite bekam 0 Höhe
  und war unsichtbar. Fix: `#if os(macOS) .frame(minWidth:720, minHeight:760,
  ideal 900×900)` auf dem `NavigationStack` + `.frame(maxWidth/maxHeight:
  .infinity)` auf dem Representable — dasselbe Muster, das `ShuffleScopeSheet`
  in `LibrariesView` schon hatte.
- **Passwort-Manager-AutoFill im Login** (Build 169): `LoginView`s Felder haben
  jetzt `.textContentType(.username)` / `.textContentType(.password)` — ohne das
  erkennt das OS (und damit Bitwardens AutoFill-Provider / die QuickType-Leiste)
  sie nicht als Login-Felder. Domain-genaue Vorschläge
  (`webcredentials:goldfish.<your-domain>`) bräuchten zusätzlich die
  Associated-Domains-Entitlement + `apple-app-site-association` auf dem Server +
  Team-Signierung → geht nicht mit den Ad-hoc-Testbuilds. Für die Authentik-Seite
  im WKWebView hilft nur Bitwarden-Desktop mit globalem Autofill-Hotkey (⌘\) +
  Bedienungshilfen-Freigabe.
- **Signierung + Version in `project.yml` verankert** (2026-08-28):
  `DEVELOPMENT_TEAM: F95969PBFU` (persönliches Team) + `CFBundleVersion` in
  `info.properties` (`xcodegen generate` setzte die plist-Datei sonst auf „1"
  zurück). `xcodebuild`-CLI kann mit der Free-Personal-Team-ID nicht
  auto-signieren → echt signierte Builds über Xcode (Run/Archive) oder manuell
  mit `CODE_SIGN_STYLE=Manual CODE_SIGN_IDENTITY=<hash>`. Signierte 1.0-Kopie:
  `~/Desktop/Goldfish_1.0.app` (Build 170).
- **SSO-Kontowechsel** (Build 171): der eingebettete Authentik-WebView
  (`OIDCLoginView`) hat einen persistenten `WKWebsiteDataStore` — beim erneuten
  „Mit SSO anmelden" wurde stillschweigend derselbe Authentik-User wieder
  angemeldet, ein Wechsel Admin ↔ normaler Benutzer war unmöglich. Neu:
  zweiter Button **„Mit anderem Konto anmelden"** auf dem Login-Screen setzt
  `OIDCLoginView(clearSessionFirst: true)` → `WKWebsiteDataStore.default()` wird
  vor dem Laden geleert → Authentik zeigt seine Anmeldemaske. Flow: oben rechts
  Abmelden → „Mit anderem Konto anmelden". SSO ist immer nur EIN aktives Konto
  gleichzeitig (kein Parallel-Login), und Offline-Kontowechsel bleibt
  passwort-only.
- Sammlungen (z. B. James Bond) sortieren Filme jetzt chronologisch nach
  Erscheinungsdatum, wie im Browser.
- Gesehen-Status wurde nicht ans Server-Grid propagiert, obwohl `setWatched`
  lief: Downloads-Tab-Kacheln rendern aus `DownloadRecord.cachedItem`, einem
  beim Download eingefrorenen JSON-Snapshot, der nie nachgezogen wurde. Fix:
  `Item.withWatched(_:)` + `DownloadManager.updateCachedWatched(itemId:
  watched:)`, verdrahtet an jedem `setWatched`-Call-Site (PlayerView,
  ItemCard, ItemDetailView).
- **Tonspur-Auswahl bei Server-Transcode** (Build 176): Der Player hatte nur
  einen Audiospur-Umschalter für lokale/Direct-Play-Quellen
  (`AVMediaSelectionGroup`). Bei einer Transcode-Session enthält der
  HLS-Stream nur die eine vom Server gewählte Spur — Auswahl läuft jetzt über
  `PlaybackResponse.streams` (Server liefert alle Quell-Audiospuren) +
  `&audio=<index>` an der Transcode-URL, `restartTranscodeSession` an der
  aktuellen Position (Browser-Pendant: das Audio-Dropdown im Player-Dialog).
  Zweites `waveform`-Menü in `PlayerControlsBar`, sichtbar bei >1 Spur.

## Gesehen-Sync zwischen zwei Usern (seit 2026-08-19)
- User-Anfrage: zwei eigene Accounts (z. B. Christian + Alex/Börnie) sollen
  ihren Gesehen-Status synchronisieren können, mutual opt-in (beide müssen
  bestätigen), und respektiert dabei die eigene Library-ACL + FSK-Grenze
  **des Partners** — es werden nur Items gespiegelt, die der Partner selbst
  sehen dürfte.
- **Bewusst server-seitig implementiert** (nicht nur in der Mac-App), damit
  der Sync für ALLE Clients automatisch funktioniert (Browser, Android, Mac).
- Server (`~/Projekte/Videoplayer/`):
  - Neue Tabelle `user_watch_links` (`internal/store/sqlite.go`): eine Zeile
    pro Paar (`user_a_id < user_b_id` per CHECK erzwungen, normalisiert via
    `normalizeWatchLinkPair`), `status` ∈ `pending`/`accepted`,
    `requester_id` für die UI-Anzeige "wartet auf …". Anfrage + Gegenanfrage
    vom Partner bestätigt automatisch (kein doppeltes pending nötig).
  - Store: `internal/store/watch_links.go` — `RequestWatchLink`,
    `ConfirmWatchLink`, `UnlinkWatchLink` (dient auch als Ablehnen — Zeile
    wird einfach gelöscht), `GetWatchLinks`, `ActiveWatchPartnerIDs`.
  - API: `internal/api/watch_links.go` + Routen in `router.go` (alle
    authenticated, NICHT admin-only — jeder User verwaltet nur seine
    eigenen Links):
    ```
    GET    /api/users/names            — Partner-Picker (nur id+username)
    GET    /api/watch-links            — eigene Links (aktiv + offen)
    POST   /api/watch-links            {username}
    POST   /api/watch-links/{partnerId}/confirm
    DELETE /api/watch-links/{partnerId}  — trennen ODER ablehnen
    ```
  - **Propagation-Hook** in `setWatched` (`internal/api/watched.go`):
    `propagateWatchedToLinkedPartners` (in `watch_links.go`) läuft NACH dem
    eigenen `SetWatchedFor`, holt alle `ActiveWatchPartnerIDs`, prüft pro
    Partner `Store.UserHasLibraryAccess` + eine neue reine (writer-lose)
    `isAgeAllowedForUser`-Variante von `requireAgeAllowed` (Refactor:
    `requireAgeAllowed` ist jetzt ein dünner HTTP-Wrapper darum) und
    spiegelt nur bei Erlaubnis via `Store.SetWatchedFor(partner.ID, …)`.
    Fehler werden nur geloggt — Sync ist Komfort-Feature, kein Blocker
    (Pattern analog NFO-Auto-Write).
- Mac-App: `GoldfishClient` (`fetchOtherUsers`, `fetchWatchLinks`,
  `requestWatchLink`, `confirmWatchLink`, `unlinkWatchLink`) + neue Models
  `OtherUser`/`WatchLink` in `GoldfishCore/Models/Models.swift`. UI:
  neue Section „Gesehen-Sync" in `SettingsView.swift` (Zusammenfassungszeile
  via `watchLinkSummary`) → `WatchLinkSettingsView.swift` (Partner-Picker,
  Bestätigen/Ablehnen/Trennen-Buttons).
- Browser/Android-UI für den Partner-Picker **noch nicht gebaut** — nur der
  Server-Endpoint + die Mac-App-UI sind live. Sync wirkt serverseitig aber
  bereits für Watched-Toggles aus JEDEM Client, sobald eine Verknüpfung
  `accepted` ist (auch aus dem Browser heraus, nur die Verwaltungs-UI dafür
  fehlt dort noch).

## Auflösung + FSK im Detail-Dialog (Mac-App, seit 2026-08-19)
- `ItemDetailView.swift`: FSK-Badge neben der bereits vorhandenen
  Auflösungs-Anzeige, liest `item.metadata?.ageRating` (liefert auch für
  Episoden korrekt, wenn der Server das Parent-Show-Rating durchreicht).
  Nur in der Mac/iOS-App — kein Server-Change nötig, das Feld kam schon vorher
  in der Item-JSON mit.
- **Noch offen:** `ShowSeasonsView.swift`'s `ShowHeader` (Serien-Übersicht)
  zeigt noch keine FSK — der Server liefert dafür aktuell KEIN Age-Rating-
  Feld im `ShowOut`-Struct (`internal/api/series.go`), müsste separat
  ergänzt werden. Nicht Teil dieser Runde.

## Was die App NICHT hat
- Kein Windows/Linux-Target (nur macOS + iOS).

## Lokale Bibliotheken — Player/Formatanpassung/Puffer (seit 2026-08-24, Stand Build 0153)

Große Session rund um externe Datenträger (USB-Platten, SD-Karten) für lokale
Bibliotheken. Reihenfolge unten = Chronologie, spätere Einträge fixen teils
Regressionen aus früheren.

- **Resume-Dialog**: `LocalLibraryItemsView`-Kachel-Tap fragt jetzt wie bei
  Server-Items "Von Anfang/Fortsetzen" (`LocalPlayerLaunchRequest.startFromBeginning`).
- **Mauszeiger-Leak gefixt**: `NSCursor.hide()/unhide()` ist app-weit
  refcounted, nicht fensterbezogen — bei `onDisappear`-Ausfall (bekannt
  unzuverlässig beim Schließen über den nativen roten Knopf) blieb der
  Cursor dauerhaft versteckt. Fix: `setHiddenUntilMouseMoves(true)` statt
  manuellem Pairing (`PlayerView` + `LocalPlayerView`).
- **Formatanpassung pausiert während aktiver Wiedergabe** (I/O-Contention auf
  langsamen externen Platten): `LocalTranscodeService.beginPlayback()`/
  `endPlayback()`/`waitWhilePlaybackActive()` (jetzt `public`) — gilt für
  die Konvertierungs-Queue UND (seit Build 0152, zweiter unabhängiger Bug)
  den Thumbnail-/Auflösungs-Hintergrund-Loop in `LocalLibraryManager`.
  Safety-Net gegen denselben `onDisappear`-Ausfall:
  `LocalPlayerView`s echter `NSWindow.willCloseNotification`-Observer ruft
  `LocalTranscodeService.resetPlaybackActive()` (Hard-Reset, kein Decrement)
  — sonst blockiert ein verpasster Teardown die Queue für den Rest der
  Session.
- **Formatanpassungs-Priorität**: nur Dateien, die einen ECHTEN Re-Encode
  brauchen (AV1/VP9/…, `h264_videotoolbox`, Minuten), werden bedingungslos
  vorab konvertiert; reine Remuxe (HEVC/H264/ProRes, `-c:v copy`, Sekunden)
  nur wenn im Cache noch Platz ist. Bei mehreren parallel scannenden
  Bibliotheken werden langsame Items per `insert(at: 0)` global vor alle
  schnellen gestellt (reines Anhängen reichte nicht, siehe Build 0145).
  `rescanAllLibraries()` scannt Bibliotheken parallel (`withTaskGroup`) —
  eine hängende/nicht angeschlossene Bibliothek blockiert sonst alle
  anderen.
- **Cache-Cap ist disk-space-aware**: `maxCacheBytes` (80 GB nominell)
  allein schützt NICHT vor "No space left on device", wenn die Platte aus
  anderen Gründen voll ist — `enforceCacheSizeCap()` hält zusätzlich
  `minFreeBytes` (15 GB) über `.volumeAvailableCapacityKey` frei (bewusst
  NICHT `...ForImportantUsage` — die zählt verwerfbare Time-Machine-
  Snapshots optimistisch mit, real 101 GB vs. 35 GB auf demselben Mac
  gemessen). `performRemux` ruft das jetzt VORAB auf, bricht bei zu wenig
  Platz sofort mit klarer Meldung ab statt nach Minuten mitten im ffmpeg-Lauf.
- **Eviction schützt langsame Re-Encodes**: reine Alt-zuerst-Eviction warf
  bei vollem Cache teure Re-Encodes zugunsten neuerer raus (Cache-Füllstand
  81 GB real beobachtet). Fix: `performRemux` hält die Slow/Fast-
  Klassifizierung persistent fest (`.slow-classification.json` im
  Cache-Ordner) — überlebt Neustarts, funktioniert auch bei nicht
  erreichbarem Datenträger. `enforceCacheSizeCap()` opfert erst alle
  schnellen Einträge (älteste zuerst), erst danach langsame.
- **Puffer-Regler**: `LocalPlaybackSettings.bufferSecondsKey`
  (`UserDefaults`, global über alle Accounts, Default 60s) steuert
  `AVPlayerItem.preferredForwardBufferDuration` in `LocalPlayerView`.
  Regler in `SettingsView` (5–180s). **Wichtig für Erwartungshaltung:**
  das ist kein "X Sekunden garantiert ruckelfrei"-Wert — nur ein Ziel, wie
  weit vorausgelesen wird; bei einer Quelle, die dauerhaft langsamer liefert
  als die Video-Bitrate braucht, verzögert ein großer Puffer das erste
  Ruckeln nur, verhindert es nicht.
- **Auflösung für lokale Items**: `LocalItem.width/height` (optional,
  AVAssetTrack-Probing statt ffprobe, funktioniert auch auf iOS), gleiche
  Bucket-Grenzen wie der Server (`ResolutionBucket`,
  `internal/store/sqlite.go` ResBuckets). Kachel-Badge + Filter + Sortierung
  in `LocalLibraryItemsView`. Wird nur beim SCANNEN ermittelt — Bestands-
  bibliotheken brauchen einmal "Neu einlesen", bevor Daten da sind.
- **Auflösungs-Sortierung auch für Server-Bibliotheken nachgezogen**:
  `ItemSort.resolution` (Server unterstützte `sort=resolution` schon lange,
  fehlte nur im Mac-Client) — kein Rescan nötig, Server-Items haben
  Breite/Höhe schon seit ihrem ursprünglichen Scan.
- **Gesamtgröße neben Datei-Anzahl**: `DisplaySettings.showTotalSizeKey`
  (ein gemeinsamer Schalter für Server- UND lokale Bibliotheken), Toggle im
  bestehenden "Filter"-Menü. Bei Server-Bibliotheken automatisch auf jeder
  Unterordner-Tiefe korrekt, weil `items` ohnehin pro Ordner-Ebene neu
  geladen wird.
- **Downloads fortsetzbar nach Verbindungsabbruch**: `DownloadRecord.resumeData`
  (`NSURLSessionDownloadTaskResumeData`, persistiert, übersteht App-Neustart)
  — `startDownload` nutzt `session.downloadTask(withResumeData:)` statt
  komplett neu, wenn vorhanden. Nicht bei jedem Abbruch garantiert (fragile
  Foundation-API) — fällt sonst automatisch auf Neustart zurück.
- **Download-Metadaten-Nachkorrektur**: falsch zugeordnete, bereits
  heruntergeladene Items korrigieren sich beim nächsten Öffnen des
  Downloads-Tabs automatisch (Titel/Poster/Jahr), sobald online — die
  Videodatei selbst bleibt unangetastet (`DownloadManager.
  refreshCachedMetadataIfChanged`).
- **Zwei weitere I/O-Contention-Quellen (Build 0154):** (1) verwaiste
  ffmpeg-Prozesse überleben einen App-Neustart NICHT automatisch mehr —
  `LocalTranscodeService.activeProcesses`/`terminateAllActiveProcesses()`,
  aufgerufen aus `AppDelegate.applicationWillTerminate`. (2) bei mehreren
  lokalen Bibliotheken feuerte `scan()` pro Bibliothek einen eigenen
  Thumbnail-/Auflösungs-Hintergrund-Task ab — jetzt EINE geteilte
  Warteschlange (`LocalLibraryManager.thumbnailQueue`) über alle
  Bibliotheken, garantiert höchstens 1 Hintergrund-Datei-Handle statt N.
  **Diagnose-Reflex bei künftigen Ruckel-Reports:** `ps aux | grep ffmpeg`
  (Start-Zeitstempel älter als die laufende App-Instanz? → Zombie) +
  `lsof +D <externes-Volume>` während aktiver Wiedergabe (mehr als 1
  Handle offen? → Contention-Quelle suchen).
- **Löschen im lokalen Player + "Alle Downloads löschen"** (Build 0155):
  `LocalPlayerView`/`LocalPlayerControlsBar` haben jetzt einen 🗑-Button
  (Bestätigungsdialog, löscht die echte Datei) — vorher ging Löschen nur
  per Rechtsklick auf die Kachel, nicht während der Wiedergabe.
  `DownloadManager.deleteAllDownloads()` + Toolbar-Button in
  `DownloadsView` (Bestätigungsdialog) — bricht laufende Downloads über
  `tasks[itemId]?.cancel()` ab, NICHT über `cancelDownload()` (das würde
  den Record vorzeitig löschen, bevor `deleteDownload()` die Datei noch
  finden kann).

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

Module unter `internal/webassets/web/` (helpers, dialogs, api, cast, player-components, cards, views, grid, player, admin, playlists, scan, matching, whisper, introskip, ocrsub) + app.js — Groessen per `wc -l`, Zweck per Dateikopf-Kommentar.


**Lade-Reihenfolge in index.html:**
```
helpers → dialogs → api → cast → player-components → cards → views → grid → player → admin → playlists → scan → matching → whisper → introskip → ocrsub → app
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

- `/media` → Unraid-Share `/mnt/user` (alle Shares als Unterordner).
  **Achtung:** Repo-Compose zeigt historisch `:ro`, der LIVE-Stack (Portainer
  Stack 37) mountet es aber **read-write** (`- /mnt/user:/media`, kein `:ro`) —
  verifiziert 2026-08-28. Deshalb kann Goldfish Media-Dateien löschen
  (Detail-Dialog 🗑, „Datei in anderem Ordner"-Aufräumen). Der Kommentar in
  `internal/api/delete_download.go` über „ist /media read-only gemountet?" ist
  entsprechend meist gegenstandslos.
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
  Select + 🗑-Icon (Tooltip „Bibliothek löschen"), Zeile 2 hat ▲▼ +
  Toggle-Pillen (🏠 Startseite, 🏷 Ordner oben). Beide Zeilen mit
  `flex-wrap` für narrow Modals (`.modal` hat `max-width: 520px`).

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
  **Admins:** OHNE eigene ACL-Zeile → alle Libraries; MIT expliziter ACL-Liste
  → nur diese Liste (User-Wunsch 2026-08-31 — auch Admins sollen Bibliotheken
  ausblenden können). Entscheider: `Store.UserHasExplicitLibraryACL(userID)`,
  angewandt in `UserHasLibraryAccess`, `ListLibrariesForUser` UND dem zentralen
  `ListItems`-ACL-Block (dort per `NOT EXISTS(…) OR library_id IN(…)`).
  `requireLibAccess(w, r, libID)` wird in jedem Item-/Stream-/Transcode-Handler
  aufgerufen. Der ACL-Editor holt die Gesamtliste über `/api/libraries?all=1`
  (admin-only). Test: `internal/store/acl_test.go`.
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
  GET     /api/introskip/log?status=done|failed|pending
  GET     /api/libraries/{id}/introskip/episodes?folder=
  POST    /api/introskip/folders/{id}/retry         {folder}
  POST    /api/introskip/retry-failed
  ```
- **Deaktivierte Serien werden vom Worker wirklich ignoriert** (seit
  2026-08-13): `Store.ListPendingIntroSkipJobs` joint gegen
  `intro_skip_folders`, sodass nur Jobs aktivierter Ordner gezogen werden —
  vorher hätte ein bereits `pending` stehender Job trotz Deaktivierung
  weitergelaufen.
- **Pausiert automatisch während eines Library-Scans** (`Worker.SetPauseCheck`
  in `cmd/goldfish/main.go`, gespeist aus `sc.Status().Running`): Introskip
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
  **Episoden-Treffer werden pro Serie** (`libraryId` + erstes rel_path-Segment)
  **zu EINER Sammelkachel gebündelt** (`appendSearchResultCards` /
  `renderSearchShowCard` in `cards.js`), Klick öffnet den Serien-Ordner —
  Filme/Privatvideos bleiben Einzelkacheln. Gilt für Library-Suche UND
  Home-View-Global-Suche.
- **Alphabet-Sidebar rechts** (`#alphaSidebar`, seit 2026-07-12 immer sichtbar):
  zeigt A-Z + `#`, unabhängig vom Sort-Feld — wirkt als **Filter** (nicht
  Scroll-Sprung): `jumpToLetter()` → `setAlphaFilter()` blendet Kacheln, die
  nicht mit dem gewählten Buchstaben starten, per `.alpha-hidden`-Klasse aus
  (Toggle bei erneutem Klick, ✕-Chip im Breadcrumb-Banner hebt den Filter
  ebenfalls auf). Body bekommt Klasse `has-alpha-sidebar`, dann nimmt das
  Grid 36px Rand rechts frei.
  **Per-Ordner-Toggle:** Button `🔤 A-Z` in der Topbar-Toolbar blendet die
  Leiste NUR für den aktuell offenen Ordner aus/ein — localStorage
  `alphaSidebar:<libID>:<folder|"root">` = `"0"` (ausgeblendet) oder kein Key
  (Default an). Eigener Namespace, unabhängig von `sort:lib:…`/`seasonView:…`.
  In der Collections-Root-Ansicht (`state.currentLibrary === null` dort) gibt
  es keinen Toggle-Button — die Leiste bleibt dort wie bisher unconditional an.
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
- **⧉ Datei in anderem Ordner** im Sort-Dropdown (`namedupes`, seit
  2026-08-28): Items, deren Dateiname (case-insensitiv) + Größe (±`tol`
  Bytes, Default 2048) auch in einem ANDEREN Ordner derselben Library
  vorkommen — für versehentliche Doppel-Kopien (z. B. ein Sammel-Ordner
  neben dem eigentlichen Ablageort; „57-Byte-Zwillinge"). Folder-gescoped
  wenn man in einem Ordner steht, sonst ganze Library. Kachel bekommt einen
  orangenen **⧉-Badge** (Overlay `top:66 right:6`) + eine „↳ auch in: …"-Zeile
  mit den anderen Ordnern; `item.dupeOtherPaths` trägt die vollen rel_paths.
  Backend: `GET /api/libraries/{id}/name-dupes?folder=&tol=`,
  `store/namedupes.go` (`CrossFolderNameDupes` — Name→Index über die ganze
  Library, dann `ListItems` für die vollen Items). Registriert in
  `currentSortMode`/`PSEUDO_FILTER_MODES`/`directionless`.

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
  - **Default seit 2026-07-11: AN.** `seasonViewEffective()` in app.js gibt
    ohne gespeicherten Wert `true` zurück (`!== "0"` statt `=== "1"`) — Serien
    öffnen automatisch in der Staffel-Ansicht. Explizites Ausschalten pro
    Library (Toggle-Button in Library-Root) bleibt als "0" gespeichert und
    respektiert.
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
  3. **Manuelle Ordner-Auswahl** (`state.shuffleFolders`, seit 2026-08-09) →
     mehrere `folderSel=<libId>:<relPath>`. Siehe „Ordner-Scoping" unten.
  4. **Library** (`state.currentLibrary`) → `libraryId` + ggf. `folder`.
- Zusätzlich greifen IMMER: `search`, `watched`, `favorite`, `match`,
  Auflösungs-Buckets.
- `openPlayer(item, {fromShuffle: true})` erhält den Shuffle-State beim Item-Wechsel.
- Backend: `ItemFilter.PlaylistID` (EXISTS in `playlist_items`) ist neben
  `PersonTMDB` der zweite optionale Pool-Selektor. Beide werden von
  `/api/items/random` aus dem Query gelesen.

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
- **Favoriten-Filter** auf „Nur" stellt sofort eine flache Ansicht (wie
  Duplikate, aber eigener Pfad in loadItems). Scope = aktueller Ordner
  rekursiv, sonst library-weit (`folder`-Param an `/api/items`, Server
  kombiniert `favorite=yes` + `folder` bereits per AND).
- **Flache library-weite Sort-Modi** „Zuletzt abgespielt" (`played`), „Zuletzt
  hinzugefügt" (`added`) und „Laufzeit" (`duration`) zeigen die Top-N Videos der
  GANZEN Library, ignorieren die Ordner-/Staffel-Struktur (keine Folders). Ein
  gemeinsamer Branch in `grid.js` (`FLAT_SORTS`) holt sie flach, Breadcrumb via
  `renderBreadcrumb({flatSortView:<mode>})`. Server-seitig filtert ListItems bei
  `Sort=="played"` zusätzlich `AND us.last_played_at IS NOT NULL`. (App-Pendant:
  `isFlatSortMode()` in LibraryViewModel.)
  **Persistenz (seit 2026-07-11):** Diese 3 Sorts werden pro Library/Folder
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
  Timeout 45 min/Item. Pausiert während Library-Scans (`SetPauseCheck`).
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
  Mehrsprachen-Rips eine Tonspur verloren (genau der Bug, der den früheren
  client-seitigen Ansatz hatte, siehe unten). Cache unter
  `/config/cache/downloads/{itemID}.mp4` + `.json`-Sidecar (Quelle
  mtime+size **+ `convVersion`**), damit ein zweiter Download nicht neu
  konvertiert. **`convVersion` (Konstante in `prepare.go`, aktuell 2) bei
  JEDER `buildArgs`-Änderung hochzählen** — sonst wird eine mit alter (ggf.
  kaputter) Logik erzeugte Kopie ewig weiter ausgeliefert, weil nur
  Quelle-mtime+size verglichen wird (2026-08-30: E-AC-3-Fehlversuch blieb
  trotz AAC-Fix im Cache → Kill Bill „zum wiederholten Male" schwarz).
  **ffmpeg-Härtung (2026-08-28, „Kill Bill startet nicht"):** `-nostdin`;
  `-analyzeduration/-probesize 200M` an ffprobe UND ffmpeg (spät startende
  zweite Tonspur einer großen MKV wird sonst übersehen); `-err_detect
  ignore_err -fflags +genpts`; `-max_muxing_queue_size 4096` (MKV→MP4 mit
  Video-Copy + Audio-Transcode bricht sonst mit „Too many packets buffered"
  ab). `runPrep` loggt jetzt Start / Codec+Tonspur-Anzahl / Fehler (2000
  Zeichen ffmpeg-Ausgabe) / Erfolg als `[download] compat-prep …`.
  **Tempo für große Rips (2026-08-28):** `+faststart` läuft IMMER (die 4-GB-
  Schwelle war ein Fehler — moov am Dateiende macht die MP4 für AVFoundation
  je nach Gerät unabspielbar, real bei Enola Holmes 6 GB passiert; der
  zusätzliche Rewrite-Pass ist durch die „Wird vorbereitet … %"-Anzeige
  abgedeckt). **Audio (Stand 2026-08-30, `convVersion = 4`):** JEDE Tonspur →
  **AAC-LC Stereo (`-ac 2 -b:a 256k`)**, auch AAC-Quellen (Audio-Pass ist gegen
  den Video-Pass vernachlässigbar). Chronik: E-AC-3 5.1 (`ec-3` in MP4 spielt
  AVFoundation nicht → Kill Bill schwarz) → AAC 5.1 OHNE `-ac 2` (erzeugte bei
  DTS-Quellen `channel_layout=unknown` → AVFoundation STUMM, Spur teils nicht
  wählbar) → **AAC Stereo mit definiertem Layout**. Für den compat-Download
  (Apple-App, meist Stereo-Ausgabe) schlägt eine verlässlich klingende
  Stereo-Spur eine stumme 5.1-Spur. **Nicht wieder auf AC-3/E-AC-3 oder auf
  5.1-ohne-Layout umstellen.**
  `-movflags +negative_cts_offsets` (immer):
  B-Frame-Delay als negative CTS statt `elst`-edit-list — ffmpegs edit list
  brachte AVFoundation bei kopiertem h264 dazu, die Datei GAR NICHT
  abzuspielen (VLC dagegen klaglos; 2026-08-28, Kill Bill). Verwaiste `.tmp.*.mp4` (Container-Restart mitten
  im Lauf) werden vor einem neuen Lauf weggeräumt.
  **Kill Bill final (2026-08-30):** die eigentliche Ursache war NICHT der
  Server — der compat-Download kam als sauberes MP4 an (avc1 h264 8-Bit +
  2× AAC 5.1, per ffprobe verifiziert). Die **Mac/iOS-App speicherte die
  Datei aber als `.mkv`** (Dateiname aus `item.container`, nicht aus der
  Server-Antwort) → AVFoundation verweigert allein wegen der Endung. Fix in
  GoldfishApple Build 178 (`?compat=1` → immer `.mp4`). Die
  Server-Änderungen unten (10-Bit-Reencode / convVersion / ETag) waren
  trotzdem nötig, nur nicht der Auslöser für DIESEN Fall.
  **Video-Pixelformat (2026-08-30, convVersion 3):** `-c:v copy` für h264 nur
  noch bei **8-Bit 4:2:0** (`pix_fmt` ∈
  yuv420p/yuvj420p/nv12/nv21). 10-Bit-H.264 / 4:2:2 / 4:4:4 kann VideoToolbox/
  AVFoundation **nicht** dekodieren (VLCs Software-Decoder schon) → wird per
  **libx264 → yuv420p** re-encodet (bewusst Software, nicht VAAPI — Intel-iGPU
  kann solche h264-Profile oft nicht decoden). HEVC 10-Bit bleibt `copy`
  (AVFoundation kann HDR-HEVC). `probeVideo` liefert dafür jetzt `pix_fmt`;
  Entscheider ist `videoNeedsReencode(codec, pixFmt)` in `prepare.go`.
  **`convVersion`-Konstante bei JEDER `buildArgs`-Änderung hochzählen** — der
  Cache-Validator (`cachedCopyValid`) verwirft sonst kaputte Alt-Kopien nicht
  (nur Quelle-mtime+size werden sonst verglichen).
  **`GET /api/download/{id}/compat-status`** (2026-08-28) liefert
  `{state,percent,message}` (`state` ∈ `ready|preparing|error|idle`) und stößt
  die Formatanpassung an, falls nötig und noch nicht laufend/gecacht.
  `prepJob` trackt `totalMS` (ffprobe-Dauer) + `doneMS` (fortlaufend aus
  ffmpeg `-progress pipe:1` / `out_time_us`); `percent()` = 1–99 während des
  Laufs, 100 bei Erfolg. Fertige Jobs bleiben 2 min in `prepReg.recent`, damit
  der Status auch Erfolg/Fehler direkt danach noch melden kann. Die Apple-App
  (`DownloadManager.prepareThenTransfer`, Build 172) pollt das alle 2 s und
  zeigt „Wird vorbereitet … X %", bevor der eigentliche Byte-Download startet;
  `state=error` → Download failed, alter Server ohne den Endpoint → direkt laden.
  Opt-in per Query-Param, damit Browser/Android (die die Original-Datei wollen
  bzw. selbst breiter dekodieren) unverändert bleiben — nur die Mac/iOS-App
  (`GoldfishClient.downloadFileURL`) fragt das an.
  **99-%-Hänger-Fix (2026-08-27, gleicher Tag):** die erste Version lief die
  ffmpeg-Konvertierung synchron am Request-Context (`r.Context()`) und servte
  am Ende mit der ModTime der Cache-Datei als einzigem Validator. Folge: die
  Apple-App lief bei großen Dateien in ihren 60-s-Read-Timeout
  (`timeoutIntervalForRequest`), startete per `resumeData` neu → neuer Request
  killte das erste ffmpeg und trat ein frisches los (in dieselbe `.tmp.mp4`);
  beim Resume matchte zudem `If-Range` nicht mehr (Cache-ModTime hatte sich
  geändert) → `ServeContent` lieferte 200 statt 206 → Download klebte bei
  99 %. Fixe: (a) `internal/download` hat jetzt eine `prepRegistry`
  (detachable single-flight pro `outPath`) — parallele/Retry-Requests teilen
  EINEN Lauf, und der Lauf läuft mit `context.Background()` (+ 2-h-Cap) auch
  weiter, wenn der Client abbricht, und wärmt so den Cache; nur das *Warten*
  im Handler respektiert `r.Context()`. (b) unique `.tmp.<ns>.mp4` +
  disk-space-Guard (`unix.Statfs`, Quelle + 2 GiB) vor ffmpeg. (c) Handler
  koppelt `ETag` **und** Last-Modified an die QUELLDATEI (mtime+size), nicht
  an die Cache-Kopie → `If-Range` ist über Resume-Versuche stabil.
  (d) Apple-App: `URLSessionConfiguration.timeoutIntervalForRequest = 600`
  in `DownloadManager` (Mac-Build 164).
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
- **Thumbnails/Trickplay unberührt:** beide sind item-ID-keyed unter
  `/config/...`, nicht pfad-abhängig — ein Move invalidiert sie nicht.
- Code: `internal/api/admin_rename.go` (`moveItem`, `moveItemsBulk`,
  `executeMove`, `listAllFolders`), `internal/store/sqlite.go`
  (`AllFolderPaths`), `internal/store/rename_history.go` (`RecordMove`).
  Routen: `POST /api/items/{id}/move`, `POST /api/items/move`,
  `GET /api/libraries/{id}/all-folders` (alle admin-only). Frontend:
  `openMoveDialog()`/`loadMoveFolderList()`/`handleMoveSubmit()` in `admin.js`.

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

## API-Referenz

Vollstaendige Routenliste inkl. Admin-Gating: `internal/api/router.go` (`grep -n "r\." internal/api/router.go`). Body-Parameter je Endpoint stehen als Kommentare in den jeweiligen Handlern in `internal/api/*.go`.

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

- [ ] **Repo öffentlich schalten als Open-Source-Projekt** (aktuell PRIVAT
  unter `github.com/boernie77/goldfish`, Verifikation: `curl -I` → 404).
  Vorbereitung ist abgeschlossen:
  * MIT-Lizenz + NOTICE.md mit Third-Party-Attributionen
  * README.md auf Englisch, TMDB-Attribution im Settings-Dialog
  * CLAUDE.md sanitisiert (keine Email-Adressen, LAN-IPs nur als Platzhalter)
  * `scripts/redeploy.sh` benötigt `$UNRAID_HOST` als Env (kein Hardcoded-IP)
  * `docker-compose.yml` env-konfigurierbar via `RENDER_GID` + `MEDIA_ROOT`
  * `.env.example` mit Hinweisen für Unraid/Synology/Arch/Fedora
  * Setup-Wizard im Server (`/api/auth/setup`) fragt TMDB+OMDb beim ersten
    Login mit ab — User muss nicht in Settings nachträglich
  * **`install.sh`**: interaktiver Bash-Installer mit whiptail-Dialog-Boxen,
    macht git clone + .env + docker compose up in einem Rutsch (commit
    `24a1db1`). Auto-erkennt render-GID, fragt NVIDIA + OIDC ab, schreibt
    docker-compose.override.yml für Port + NVIDIA. Fallback auf read-Prompts
    wenn whiptail fehlt. **End-to-End-Test auf fremdem Linux steht noch aus.**
  Offene Schritte vor public-Schalten:
  1. install.sh auf frischem Linux testen (siehe Memory
     `project_installer_e2e_test.md`)
  2. Final-Check über git-Historie auf evtl. übersehene Secrets (vor dem
     2026-05-08-Sanitization-Commit `f46eebf` waren Email + IP in CLAUDE.md
     drin — falls History-Cleanup gewünscht: `git filter-repo` + force-push)
  3. Optional: Modulpfad anonymisieren (`go mod edit -module …`)
  4. In Repo-Settings auf „Public" stellen
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
