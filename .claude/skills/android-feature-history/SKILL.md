---
name: android-feature-history
description: Detaillierte Feature-/Bugfix-Historie der Goldfish-Android-App (Kotlin/Compose, separates Repo GoldfishAndroid). Laden wenn an Android-App-relevanten Server-API-Antworten gearbeitet wird oder Details zu einem vC-Versionsstand (z.B. "vC 92", "seit 1.1.3") gebraucht werden. Die 3 harten API-Kompatibilitäts-Warnungen stehen weiterhin direkt in CLAUDE.md — dieser Skill ist nur die ausführliche Feature-Chronik.
---

# Android-App-Featureliste (Chronik)

Ausgelagert aus CLAUDE.md (2026-07-31) — reine Feature-/Bugfix-Historie, kein
Sicherheitshinweis. Die App-Repo liegt unter
`/Users/christian/Projekte/GoldfishAndroid/`.

## Android-App-Featureliste (Stand 1.2.67)

Seit 1.1.3 zusaetzlich (knapper Ueberblick — Details in den Memory-Files
`project_android_app.md` und `project_feature_local_libraries.md`):
- **Bibliotheken zusammenlegen (vC 98 Server+App, vC 99 lokale Libs):** In den
  Einstellungen können je 2 Bibliotheken gleichen Typs virtuell zusammengelegt
  werden (Server+Server ODER Lokal+Lokal). Im HomeScreen erscheinen separate
  „🔗 Lib1 + Lib2"-Kacheln; wenn eine Lib nicht verfügbar ist, ausgegraut (kein
  Fehler). Zufallsplay, Sortierung etc. funktionieren über beide. Server: `ItemFilter.
  LibraryIDs []int64` + `IN (…)`-Klausel in `ListItems`/`randomItem`; API akzeptiert
  mehrere `?libraryId=` Query-Params. App: `mergedServerLibraryIds` + `mergedLocal
  LibraryIds` in SettingsDataStore; `GoldfishApi`/`ItemRepository` mit `List<Int>?`;
  `LibraryViewModel.loadMerged()`, `LocalLibraryViewModel.loadMerged()` (Items aus
  beiden Libs kombiniert flach); Routen `MergedLibrary`, `PlayerRandomMerged`,
  `MergedLocalLibrary`. Settings-UI in zwei Gruppen (📡 Server, 📁 Lokal).
- **Online-Lib „Zuletzt gespielt" — App rief /played nie (vC 96):** Server
  trackt `user_item_state.last_played_at` NUR via `POST /api/items/{id}/played`
  (`TouchLastPlayed`); Resume/Watched setzen es NICHT. Der **Browser** ruft das
  beim Player-Open (player.js), die **App tat es nicht** → in der App gespielte
  Videos erschienen nie in der Online-Sortierung „Zuletzt gespielt" (nur im
  Browser gespielte). Fix: `GoldfishApi.markPlayed` + `ItemRepository.markPlayed`
  (invalidiert items-/home-Cache), `PlayerViewModel.load` ruft es fire-and-forget
  beim Öffnen. (Lange als lokales-Lib-Problem fehldiagnostiziert — es war die
  Online-Lib. Lokale Lib ist eine separate Strecke mit eigenem lastPlayedAt.)
- **Lokale Lib: Sort „Zuletzt gespielt" (vC 91):** lokale Bibliotheken haben
  jetzt auch den Sort `LOCAL_SORT_PLAYED` — flache library-weite Liste der
  zuletzt im lokalen Player geoeffneten Items. Neue Spalte
  `local_items.lastPlayedAt` (LocalAppDatabase v5, MIGRATION_4_5), beim
  `LocalPlayerViewModel.load` gestempelt (gilt fuer ExoPlayer + VLC), beim
  Re-Scan erhalten. Zeigt nur Items mit lastPlayedAt>0.
  **Fix vC 92:** Nach Player-Rueckkehr wurde die lokale Liste NICHT neu geladen
  (Idempotenz-Guard in `load()` blockt Refresh zum Scroll-Schutz; kein DB-Flow),
  daher tauchten frisch abgespielte Videos nicht in „Zuletzt gespielt" auf.
  vC 92 (LifecycleResumeEffect → `refreshCurrent()`) reichte NICHT zuverlaessig.
  **Fix vC 93:** `LocalLibraryRepository` hat jetzt einen `itemMutated`-
  SharedFlow, der nach jedem `updateItem` emittiert; `LocalLibraryViewModel`
  beobachtet ihn im `init` und ruft `refreshCurrent()` (stilles force-Reload via
  `silent`-Param). Lifecycle-unabhaengig: der Player stempelt lastPlayedAt via
  `updateItem` waehrend das Lib-VM im Backstack lebt → Liste ist bei Rueckkehr
  frisch. Server-Libs nicht betroffen — deren `load()` ruft immer `doReload`.
  **ECHTE Ursache + Fix vC 94:** vC 92/93 reichten nicht, weil es KEIN Refresh-
  Problem war — der `lastPlayedAt`-Stempel wurde wieder ueberschrieben.
  `recoverMissingThumbnails` (laeuft im BG bei jedem Lib-Open, v.a. Privat-Libs
  mit Frame-Thumbnails) UND `LocalEnricher.applyHit` schrieben Items per
  `update(item.copy(...))` als GANZE Zeile aus einem VERALTETEN Snapshot zurueck
  → clobberten den parallel gesetzten lastPlayedAt (und watched/resume) auf 0.
  Fix: gezielte Einzelspalten-Updates `LocalItemDao.setLastPlayed`/
  `setThumbnailPath`; Stempel via `repository.markLocalPlayed` (statt
  updateItem(copy)); recoverMissingThumbnails nutzt setThumbnailPath; applyHit
  liest das Item frisch (getItem) bevor es die Zeile schreibt. **LEHRE:
  Hintergrund-Jobs NIE eine ganze Entity-Zeile aus einem alten Snapshot
  zurueckschreiben — gezielte Spalten-Updates nutzen.**
- **Player Favorit + Löschen (vC 89):** Im Server-Player oben rechts ein
  Favorit-Toggle (♥, optimistisch) und — Admin-only — ein Lösch-Button (🗑)
  mit Bestätigungsdialog (`DELETE /api/items/{id}?deleteFile=true`); nach
  Erfolg `state.deleted=true` → Screen navigiert zurueck. Neuer API-Endpoint
  `deleteItem` + `ItemRepository.deleteItem`, `PlayerViewModel` bekam
  `AuthRepository` injiziert (isAdmin).
- **Flache library-weite Sort-Modi (vC 89):** „Zuletzt abgespielt"/„Zuletzt
  hinzugefügt"/„Laufzeit" zeigen jetzt — wie im Browser — die Top-Videos der
  GANZEN Library, unabhaengig von Ordner/Staffel. `isFlatSortMode()` in
  LibraryViewModel: early-Branch in doReload (folderParam=null), `onSortChange`
  macht vollen reload(), `suppressItemsAtRoot` + Drilldown-Filter respektieren
  den Flat-Modus. **Offline (vC 90):** auch `doReloadOffline` behandelt die drei
  Modi via `OfflineRepository.itemsSortedFlat` aus Room. „Zuletzt abgespielt"
  nutzt ein NEUES lokales `downloads.lastPlayedAt` (DB v6, Migration 5→6), das
  `PlayerViewModel` beim Abspielen eines Downloads via
  `DownloadRepository.markPlayed` stempelt (Server-last_played_at ist offline
  nicht verfuegbar). Nur offline abgespielte Downloads erscheinen dort.
- **Drilldown-Toggle (vC 64):** Long-Press auf eine Folder-Kachel
  (Admin-only) oeffnet Bestaetigungsdialog "Unterordner als Ebene
  anzeigen?". Pendant zum Hover-⚙ im Browser. Folder mit aktivem
  Drilldown bekommen ein ▸-Indicator und navigieren in eine Subfolder-
  Liste statt flach alle Items zu zeigen. Route haengt `?drilldown=true`
  als Query-Param an, persistiert sich ueber mehrere Drilldown-Ebenen
  hinweg.
- **Bugfix lokale-Lib-Thumbnails (vC 64):** Frame-Vorschaubilder lagen
  in `cacheDir/local-thumbs`, das Android unter Speicherdruck loescht
  (= "verschwinden staendig"). Jetzt `filesDir/local-thumbs` plus
  `recoverMissingThumbnails`-Background-Job beim Library-Open, der
  fehlende JPEGs neu extrahiert und den Pfad zurueck in die Room-DB
  schreibt.
- **Lokale Bibliotheken (SAF)** mit eigener Room-DB, NameParser-Port,
  MediaProbe via MediaMetadataRetriever, TMDB-Anreicherung via
  Server-Proxy, Frame-Thumbnails als Fallback, Show→Staffel→Folge-
  Navigation, Show-Re-Match-Dialog, Zufallswiedergabe pro Lib/Folder,
  Long-Press-Delete (SAF + DB), ansicht-scoped Suchfeld in der Lib
- **Privat-Libs gruppieren nach Channel-Folder**, Items sortiert nach
  `releasedAtMs ?: modifiedTime` DESC (neueste oben); Container-DATE-Tag
  (yt-dlp MKV-DATE `YYYYMMDD`, mp4 creation_time) per MediaProbe
  ausgelesen + in `local_items.releasedAtMs` persistiert
- **Offline-Verbesserungen**: `library_folder_cache` + `library_seasons_cache`
  cachen Show-Poster + komplette SeasonResponse persistent (Bilder offline
  verfuegbar); Offline-Mode filtert Staffel-Ansicht auf owned-Episoden
- **Re-Match-Sync-Button (↻)** im Show-Header von Server-Libs nach
  Browser-seitiger Korrektur (`?refresh=true`)
- **Globale Suche** (🔍 in der HomeScreen-Topbar) ueber Server-Libs +
  Offline-Downloads + lokale Libs
- **Long-Press-Delete fuer Downloads** in jeder Lib-Ansicht + Settings-
  Button „Alle Downloads entfernen" mit bulk-File-Cleanup
- **`channelLabelOnTop`-Toggle** aus Server respektiert
- **Datei-Titel im LocalPlayer** mit ControllerVisibility synchron
- **HomeScreen reagiert auf Item-Mutations** (`itemUpdated`-SharedFlow,
  300ms debounce) → „Fortsetzen"/„Als naechstes"-Strips refreshen nach
  watched/favorite/resume statt stale-Items zu behalten
- Setup-Wizard (Server-URL + Login), Persistent-Auth, Cast/AirPlay aus
  Browser-only (bewusst nicht in der App)

## Android-App-Featureliste (Snapshot Stand 1.1.3 — Basis)

- Library-Grid (adaptive Spalten Tablet/Phone), Filter (Sort/Watched/Favorit/
  Auflösung/Rating/Flach/Staffeln/Zufall/Auswahl), Detail-Screen mit Cast-Strip
- **Buchstaben-Sidebar** rechts bei Sort=Title und ≥10 Kacheln — sucht zuerst
  in Show-Folders (TV-Lib), dann in Items (wie im Browser)
- **Sortier-Richtung** wird explizit als `dir=asc|desc` gesendet, nicht
  client-side gereverst
- Player: Media3/ExoPlayer, eigenes Compose-Settings-Zahnrad oben rechts (synced
  mit ControlBar), Quality-Auswahl, **Untertitel-Dropdown** (Text-VTT,
  Whisper-VTT, PGS bei Direct Play via ExoPlayer-PgsParser), Trickplay-Hover
  beim Scrub-Drag, Resume-Dialog (Daten aus separatem `/resume`-Endpoint)
- Show-Header in der Staffel-Übersicht mit Beschreibung
- Episoden-Grid mit Auflösung-Badges und Offline-Indikator
- Download via SAF-Picker („Ordner auswählen…") in Settings, Application-Scope-
  Coroutine, Progress-Ring auf der Kachel + grünes CloudDone nach Abschluss
- Performance: in-Memory ApiCache (TTL pro Endpoint), Coil 1 GB Disk-Cache,
  OkHttp HTTP-Cache (User-konfigurierbar)
- Adaptive Launcher-Icon (Goldfisch CC-BY 4.0 Twemoji)
- Versionsnummer aus `BuildConfig.VERSION_NAME` im Settings-Screen sichtbar
