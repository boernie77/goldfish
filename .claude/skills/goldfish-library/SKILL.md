---
name: goldfish-library
description: "Use when changing Goldfish library/scan behaviour: auto-scan, scan exclusions, metadata matching, duplicates/series auto-merge, NFO sidecars, renaming, moving files, download & delete, music libraries."
metadata:
  project: Goldfish (boernie77/goldfish)
  source: "CLAUDE.md-Aufteilung 2026-09-20"
---

# goldfish-library

Aus der früheren Sammel-CLAUDE.md des Goldfish-Repos ausgelagerter Themenbereich (Zeichen: 92048, Sektionen: 13). Volltext des Originals: Skill `goldfish-full-archive`.

## Harte Regeln (zuerst lesen)

- ACL/FSK wird im Handler gefiltert, nie ungeprüft durchgereicht (Autoplay/Next-Episode).
- Einmalige Backfills (z. B. Statistiken) NICHT erneut ausführen — Doppelzählung.

---

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

