---
name: goldfish-playback
description: "Use when touching Goldfish playback: HLS/transcode sessions, VAAPI, trickplay, intro detection, player UI, shuffle, TMDB/trailer, API routes. Contains the measured transcode cost table."
metadata:
  project: Goldfish (boernie77/goldfish)
  source: "CLAUDE.md-Aufteilung 2026-09-20"
---

# goldfish-playback

Aus der früheren Sammel-CLAUDE.md des Goldfish-Repos ausgelagerter Themenbereich (Zeichen: 70755, Sektionen: 16). Volltext des Originals: Skill `goldfish-full-archive`.

## Harte Regeln (zuerst lesen)

- Harte Obergrenze gleichzeitiger Transcodes (gewichtet) — eine abgelehnte Wiedergabe (HTTP 503 + Retry-After) ist immer besser als ein toter Server.
- Transcode-Kosten sind gemessen, nicht geschätzt (Tabelle im Skill).
- 3 API-Kompatibilitätsregeln der Apps: resumePosSec separat, /api/download/{id}, Cast über metadata_id.

---

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

#### Rückfall-Stufen bei gescheitertem Hardware-Decode (2026-09-22)

Scheitert der Hardware-Decoder an einer Datei, läuft die Sitzung über
`Manager.RetryWithFallback` zwei Stufen ab — **eine pro Versuch**, damit eine
wirklich kaputte Datei keine Neustart-Schleife erzeugt (`internal/api/stream.go`
versucht höchstens beide und nur bei `ErrFFmpegDiedEarly`):

1. `stageCPUEncodeVAAPI` — **CPU dekodiert, die Grafikeinheit encodiert**
   (`-vaapi_device …` + `-vf format=nv12,hwupload` + `-c:v h264_vaapi`,
   bewusst **ohne** `-hwaccel`). Nur wenn VAAPI gewählt und das Gerät gesetzt ist.
2. `stageFullSoftware` — CPU dekodiert und encodiert (libx264 `veryfast`).
   Letzte Stufe, funktioniert für jeden Codec.

Am 2026-09-22 am laufenden Server gemessen (je 20 s Material, CPU-Zeit; die
Dateien kommen aus der echten Sammlung):

| Quelle | Stufe 1 | reiner Software-Weg | Faktor |
|---|---|---|---|
| AV1 (YouTube, 1080p) | 12,6 s | 37,3 s | 3,0× |
| H.264 (Serie, mp4) | 1,45 s | 5,12 s | 3,5× |
| MPEG-2 (Film, mkv) | 1,43 s | 6,09 s | 4,3× |
| MPEG-4 ASP (alte .avi-Serie) | 0,96 s | 6,66 s | 6,9× |

Speicherbedarf je Sitzung bei AV1: ~195 MB statt ~889 MB. Auslöser waren die
AV1-Dateien: `vainfo` listet auf der UHD 770 zwar `VAProfileAV1Profile0`, ffmpeg
scheitert aber mit „Failed to inject frame into filter network: Function not
implemented" (Startfehler im Log: `driverInitFileInfo … result=11`) — **ein
neuerer Treiber hilft nicht** (getestet mit `intel-media-va-driver 25.2.3` +
`libva 2.22.0`: AV1 wird dort gar nicht mehr gelistet).

**Nicht mehr nachmessen, sondern hier nachlesen:** Der frühere „halbe"
Rückfallweg (CPU-Decode + `hwupload` + `h264_vaapi`) war am 2026-09-17 entfallen,
weil er bei VC1/WMV3 mit derselben Meldung scheiterte — damals aber mit
`-hwaccel`-Decoder. Die Stufe 1 von heute dekodiert bewusst in Software, damit
entsteht dieser Fehler nicht mehr. **VC-1/WMV ist bislang ungetestet** (kein
solches Material in der Sammlung); scheitert Stufe 1 dort, greift automatisch
Stufe 2 — maximal ein zusätzlicher Fehlversuch, nie eine tote Wiedergabe.

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

## API-Referenz

Vollstaendige Routenliste inkl. Admin-Gating: `internal/api/router.go` (`grep -n "r\." internal/api/router.go`). Body-Parameter je Endpoint stehen als Kommentare in den jeweiligen Handlern in `internal/api/*.go`.

### „Nächste Folge automatisch starten" (seit v1.4.13)

Feature über alle Clients (Browser, iOS/macOS/tvOS, Android, Fire TV, Linux):

- `GET /api/items/{id}/next-episode` → `{"next": <Item>|null}`. Die
  Reihenfolge-Logik liegt serverseitig (`store.NextEpisodeCandidates`,
  `internal/store/next_episode.go`): gleiche Serie über `metadata.parent_id`,
  Ordnung `(season, episode)`, Doppelfolgen (`items.episode_end`) als Block,
  pro (Staffel, Folge) genau ein Kandidat (höhere Quelle als Vertreter — wie
  `groupVariants`). Die letzte Folge liefert **200 + `next: null`**, keinen 404:
  Serienende ist ein Normalfall, kein Fehler.
- ACL/FSK werden im Handler (`internal/api/playback_next.go`) gefiltert — der
  Store liefert bewusst mehrere Kandidaten, der Handler nimmt den ersten, den
  der Nutzer sehen darf (`UserHasLibraryAccess` + `isAgeAllowedForUser`).
  **Niemals ungeprüft durchreichen**: Kandidaten können in einer Bibliothek
  ohne Freigabe liegen (Auto-Merge derselben Serie über mehrere Ordner) und
  dann wäre der Autoplay-Modus ein Weg an der FSK-Sperre vorbei.
- `GET/PUT /api/playback/preferences` → `{"autoplayNext": bool}`
  (`user_settings.playback_autoplay_next`, **pro Konto**, Default **AUS**).
  Bewusst serverseitig und nicht lokal je Gerät: dieselbe Einstellung gilt
  dann in jeder App desselben Kontos.
- Die **Auflösung/Qualität** ist bewusst NICHT Teil der Server-Präferenzen.
  Jeder Client merkt seine letzte Profil-Wahl selbst und schickt sie beim
  Start der Folge mit (`?profile=` in `/api/playback/{id}`).
- Tests: `internal/store/next_episode_test.go` (Reihenfolge, Staffelwechsel,
  Doppelfolgen, Varianten, letzte Folge, Film), `internal/api/playback_next_test.go`
  (ACL- und FSK-Filter, Admin-Gegenprobe) und
  `internal/api/playback_next_e2e_test.go` (durch den echten chi-Router mit
  Login: 401 ohne Session, 200 mit, PUT/GET-Roundtrip, Trennung zweier Konten).


