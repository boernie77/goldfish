---
name: goldfish-deploy-ops
description: "Use when deploying, building or operating the Goldfish server (Portainer stack, image CI, volumes, DB schema, hardware, lasttest, code conventions). Measured facts, not guesses."
metadata:
  project: Goldfish (boernie77/goldfish)
  source: "CLAUDE.md-Aufteilung 2026-09-20"
---

# goldfish-deploy-ops

Aus der früheren Sammel-CLAUDE.md des Goldfish-Repos ausgelagerter Themenbereich (Zeichen: 30220, Sektionen: 14). Volltext des Originals: Skill `goldfish-full-archive`.

## Harte Regeln (zuerst lesen)

- Vor jedem Push prüfen, ob eine Transcode-Wiedergabe läuft (ffmpeg auf dem Server) — ein Deploy darf keine laufende Wiedergabe killen.
- appVersion in internal/api/router.go bei JEDEM Deploy um +1 erhöhen.
- Das Volume bleibt external mit dem Namen videoplayer_videoplayer_config — sonst leere User-DB.
- Portainer-Stack 37 'videoplayer' auf Endpoint 3; Image-CI baut bei Push auf main.

---

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
- **🔴 KORREKTUR desselben Fixes, noch selbiger Tag (v1.4.8, User-Report
  "Source error" bei AV1-Dateien):** der Fix oben behandelte JEDES beendete
  ffmpeg gleich — auch ein ganz normal ERFOLGREICH fertig transkodiertes
  Video (Dateiende erreicht, komplette Playlist geschrieben), nicht nur
  einen echten Fehlschlag. Bei AV1-Quellen (854 Titel im Bestand, alle
  Profil "Main"/yuv420p) scheitert der VAAPI-Hardware-Decoder auf der
  UHD 770 zuverlässig ("Impossible to convert between the formats... filter
  'auto_scale_0'"), der CPU-Fallback läuft aber komplett durch — KEIN
  Fehler. Trotzdem löschte der GC das Session-Verzeichnis SOFORT, sobald
  `Done()` true war, noch bevor der Client alle Segmente abgeholt hatte →
  404 auf gerade gelöschte Dateien, beim Client als "Source error"
  sichtbar. **Fix:** neues `Session.failed`-Feld (gesetzt VOR `close(done)`,
  geschützt durch `s.mu` — Race-Detector-getestet), nur bei `err != nil &&
  ctx.Err() == nil` in der `cmd.Wait()`-Goroutine gesetzt. GC-Loop und
  `StartOrGet` räumen jetzt nur noch bei `Done() && Failed()` sofort auf;
  ein regulär beendeter Transcode fällt auf den normalen Idle-Pfad zurück
  (`sessionIdleTimeout`, 30 Min — `Touch()` läuft bei jedem Segment-Abruf,
  siehe `internal/api/stream.go`, hält eine noch aktiv abgeholte Session
  am Leben). **Lehre:** `Done()` sagt nur "der Prozess ist vorbei", nicht
  "es lief etwas schief" — bei jeder Aufräum-Entscheidung, die auf einem
  beendeten Prozess basiert, den tatsächlichen Exit-Status prüfen, nicht
  nur ob er beendet ist. Tests: `TestGCKeepsSuccessfullyFinishedSessions`
  (neu), `TestDeadSessionsFreeTheirBudgetSlot`/
  `TestGCRemovesDeadSessionsRegardlessOfIdle` (angepasst, setzen jetzt
  bewusst `failed = true`, um den echten Fehlerfall nachzubilden).
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

- **⚠ Zufallswiedergabe und Filter in der Playlist-ÜBERSICHT** (gebaut
  2026-09-17, v1.4.6): `playRandom()` prüfte nur `currentLibrary`/
  `currentPlaylist`/`personFilter`/`shuffleFolders` — in der Playlist-Liste
  ist keines davon gesetzt, der Zufall brach also mit „Bitte erst eine
  Bibliothek … wählen" ab (User-Report: „wenn ich in Playlist drin bin, geht
  der Shuffle Play nicht. Nur wenn ich eine Playlist öffne"). Neu:
  `playlistId=any` → `ItemFilter.AnyPlaylist` = „liegt in IRGENDEINER für
  diesen Nutzer sichtbaren Playlist".
  **🔒 Die Sichtbarkeitsregel MUSS exakt der von `ListPlaylistsForUser`
  entsprechen** (`user_id = ?` ODER besitzerlos UND Admin) — Playlists sind
  private Kuratierung, **es gibt KEINE Admin-Ausnahme auf fremde Playlists**
  (siehe `TestPlaylistUserIsolation`). Sonst zöge die Zufallswiedergabe
  Titel aus Playlists, die in der Übersicht gar nicht auftauchen.
  Tests: `internal/store/any_playlist_test.go` (inkl. Leck-Test gegen den
  Admin-Fall und Dublettenprüfung bei Items in mehreren Playlists).
  **Die Gegenprobe gehört dazu:** mit absichtlich eingebautem Leck
  (`pl.user_id = ? OR ? = 1`) schlägt der Test fehl — ein Test, der ein
  echtes Leck nicht bemerkt, ist wertlos.

  **🔴 Nachtrag v1.4.7 — `listItems` und `randomItem` sind ZWEI Handler:**
  der erste Anlauf parste `playlistId` nur in `randomItem`. `listItems`
  ignorierte den Parameter still und lieferte statt der ~4.500
  Playlist-Titel die kompletten **95.069** Items der Bibliothek. Der
  Store-Test war grün, weil er den Handler gar nicht durchläuft — **erst
  die Prüfung gegen den Live-Server hat es gezeigt** (Vergleich: Summe der
  Einträge laut Playlist-Übersicht vs. Anzahl aus `playlistId=any`).
  `TestListAndRandomShareFilterParams` (`internal/api/`) hält seither fest,
  dass beide Handler dieselben umfangsbestimmenden Parameter auswerten und
  beide `UserID`/`IsAdmin` aus der Session setzen.
  **Regel: Wer in einem der beiden Handler einen Filter-Parameter ergänzt,
  muss prüfen, ob der andere ihn auch braucht** — und das Ergebnis gegen
  echte Daten gegenrechnen, nicht nur gegen Unit-Tests.
  **Die Filterleiste wirkt dort jetzt ebenfalls:** sobald Suche/Sortierung/
  Gesehen/Favorit/Bewertung/Auflösung gesetzt sind, zeigt die Übersicht die
  TITEL aus allen Playlists statt der Playlist-Kacheln — vorher lief die
  Leiste dort ins Leere, weil Kacheln weder „zuletzt gespielt" noch
  „gesehen" kennen. Ohne Filter bleiben die Kacheln der normale Einstieg.
- **⚠ Beim Schreiben von Store-Tests:** `UpsertItem` schreibt die vergebene
  ID **nicht** in das übergebene Item zurück (`it.ID` bleibt 0) — die ID
  über den Pfad nachschlagen. Und **ohne `SetUserLibraryAccess` liefert
  `ListItems` für Nicht-Admins grundsätzlich nichts** (ACL-Sperrpunkt für
  jeden Aufruf mit echter UserID); ein Test ohne diese Freigabe wird aus
  dem falschen Grund grün oder rot.

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

