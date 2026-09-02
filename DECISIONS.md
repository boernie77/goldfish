## Bekannte Probleme & Lösungen (Decision Log)

### ✅ Papierkorb-Icon zu hoch + A-Z-Leiste beginnt erst bei C (2026-07-12)
- **Symptom 1:** Nach der Umstellung von Emoji auf SVG-Mask (siehe Tofu-Box-
  Eintrag unten) saß das Papierkorb-Icon im Player deutlich zu hoch,
  passte nicht zur Zeile der Nachbar-Buttons (♡/📋/📺/📡).
- **Ursache 1:** Die Zentrierung der Nachbar-Buttons basiert auf
  `line-height: 2.2` (funktioniert für Text-Glyphen, die auf der Baseline
  sitzen). Das Papierkorb-Icon ist aber eine `inline-block`-Mask-Box, keine
  Text-Glyphe — `vertical-align: middle` + `line-height` zentriert sowas
  nicht zuverlässig gleich.
- **Lösung 1:** Absolute Positionierung statt Font-Metrik-Zentrierung:
  `.vjs-icon-placeholder { position: relative }`, `::before` mit
  `position: absolute; top: 50%; left: 50%; transform: translate(-50%,-50%)`,
  feste Pixelgröße (18px) statt `em` (macht die Größe unabhängig von
  ererbter font-size). Robust gegen jede Font-Metrik-Eigenheit.
- **Symptom 2:** Alphabet-Sidebar (A-Z rechts) begann optisch erst bei „C" —
  A und B fehlten scheinbar.
- **Ursache 2:** `.alpha-sidebar { top: 110px }` war hart verdrahtet. Wenn
  Topbar+Lib-Nav zusammen höher als 110px sind (z. B. bei Zeilenumbruch),
  lag der obere Teil der Sidebar-Liste (A, B) unter dem fixed Header
  verdeckt. Exakt dasselbe Bug-Muster wie zuvor bei `.bulk-bar`.
- **Lösung 2:** `top: calc(var(--topbar-h, 60px) + var(--lib-nav-h, 0px) +
  8px)` — dieselben dynamischen Variablen wie `.breadcrumb`/`.lib-nav`/
  `.bulk-bar`.
- **Lehre:** JEDES `position: fixed`/`sticky`-Element unterhalb der Topbar
  MUSS die dynamischen `--topbar-h`/`--lib-nav-h`-Variablen nutzen — nie
  einen geschätzten Pixelwert. Das war jetzt der DRITTE Fund dieses exakt
  gleichen Bug-Musters in dieser Woche (`.bulk-bar`, `.breadcrumb`,
  `.alpha-sidebar`) — beim nächsten Mal direkt danach suchen, wenn ein
  „Element verschwindet/liegt falsch beim Scrollen"-Bug gemeldet wird.

### ✅ Zurück-Pfeil fehlt bei Flat-Sorts + Duplikate zeigt nichts in Privat-Libs (2026-07-12)
- **Symptom 1:** In einem Unterordner nach „Hinzugefügt"/„Zuletzt abgespielt"/
  „Laufzeit" sortieren (Flat-Sort) → kein Zurück-Pfeil in der Breadcrumb mehr.
- **Ursache 1:** Der `opts.flatSortView`-Zweig in `renderBreadcrumb()`
  (`views.js`) hatte NIE einen Back-Button gebaut — unabhängig vom Vortags-Fix
  (Flat-Sort-Persistenz), der Bug bestand schon vorher, wurde aber erst jetzt
  auffällig, weil man durch die neue Persistenz länger in diesem View bleibt.
- **Lösung 1:** Gleiche „eine Ebene zurück"-Logik wie die normale
  Ordner-Navigation ergänzt (nur wenn `state.currentFolder` gesetzt ist).
- **Symptom 2:** Sort-Dropdown „Duplikate" zeigt in einer Privat-Library
  (YouTube/Urlaubsvideos) NICHTS an, obwohl der User weiß, dass es doppelte
  Dateien gibt.
- **Ursache 2:** Die bestehende Duplikate-Erkennung (`ItemFilter.DupesOnly`)
  basiert ausschließlich auf gemeinsamer `metadata_id`. Privat-Libraries
  laufen ohne TMDB-Enrichment — `metadata_id` ist dort fast immer NULL
  (außer manuell per ✏-Button verknüpft), also greift der Filter nie. War
  KEIN Bug im engeren Sinn, sondern ein fehlender Erkennungsmechanismus für
  einen Use-Case (zufällig zweimal heruntergeladene identische Videodatei),
  den es vorher nicht gab.
- **Lösung 2:** Neuer paralleler Mechanismus `ItemFilter.FileDupesOnly`
  (`?fileDuplicates=yes`) — erkennt Duplikate anhand gleicher
  `(size_bytes, duration_sec)`-Kombination (SQL: String-Konkatenation als
  Tupel-Ersatz für portable `GROUP BY ... HAVING COUNT(*) > 1`). Gescoped auf
  EINE Library + optional Folder rekursiv (anders als die metadata_id-Variante,
  die bewusst library-übergreifend über alle Libs gleichen Kinds sucht — für
  Movies/TV bleibt das unverändert). Frontend wählt in `grid.js` anhand
  `lib.kind === "private"` zwischen beiden Mechanismen. Bewusst konservativ:
  fängt nur bytegleiche Duplikate (gleiche Größe UND Laufzeit), keine
  inhaltlich identischen aber unterschiedlich (re-)encodeten Dateien — sonst
  hohes False-Positive-Risiko.

### ✅ Transcode-Pause > 5 Min bricht Wiedergabe ab (2026-07-10)
- **Symptom:** Video pausieren, eine Weile warten, fortsetzen → Wiedergabe
  läuft noch ca. 60 s (der Client-seitige Restbuffer) und bricht dann ab,
  Video muss neu gestartet werden.
- **Ursache:** ffmpeg-Transcode-Sessions werden nach 5 Min Inaktivität vom
  GC-Loop beendet (`internal/playback/ffmpeg.go` `gcLoop`, 5*time.Minute).
  Der Client pollt `/api/transcode/{id}/progress` durchgehend alle 1 s, auch
  während der Player pausiert ist (kein Gate auf `vjs.paused()` in
  `startBufferDisplay`) — aber der Handler `transcodeProgress` rief nirgends
  `sess.Touch()` auf (nur `transcodeSegment` touched die Session). Während
  einer Pause kommen keine Segment-Requests mehr rein → nach 5 Min killt der
  GC die Session, obwohl der Player fleißig weiterpollt. Beim Fortsetzen
  spielt der Client noch seinen Restbuffer, dann 404 auf ein nicht mehr
  existierendes Segment.
- **Lösung:** `sess.Touch()` im `transcodeProgress`-Handler ergänzt
  (`internal/api/stream.go`). Damit hält jeder offene Player (auch pausiert)
  seine Session beliebig lange am Leben; echte Inaktivität (Player-Dialog
  geschlossen → `stopTranscodeProgress()` stoppt den Poll-Timer) lässt den
  GC weiterhin nach 5 Min greifen wie vorgesehen.
- **Verworfener Zwischenstand:** Im Working Tree lag zeitweise ein unfertiger,
  nie deployter Ansatz („predictive playlist" mit `#EXT-X-ENDLIST` +
  Phantom-Segmenten + 60s-Segment-Wait-Timeout in `stream.go`/`player.js`),
  der dasselbe Symptom über einen anderen (komplizierteren, mit eigenen
  Race-Conditions behafteten) Weg zu lösen versuchte. Wurde verworfen
  (`git checkout --`) zugunsten des minimalen Touch()-Fixes.

### ✅ Breadcrumb + Bulk-Bar scrollten nach ~1 Viewport-Höhe doch weg (2026-07-10)
- **Symptom:** Die Zeile mit Zurück-Pfeil/Ordner-Info/Trickplay-Fortschritt
  UND die Bulk-Auswahl-Leiste sollten unter Topbar+Lib-Nav fixiert bleiben.
  Erster Fixversuch (nur `position: sticky` + korrekter `top`-Wert auf
  `.breadcrumb`) linderte es kurz, aber: "Am Anfang ist sie fest, aber dann
  scrollt sie doch weg" — nach ca. einer Viewport-Höhe Scroll hörte das
  Kleben auf.
- **Echte Ursache:** `html, body { height: 100%; }` in style.css. `position:
  sticky` spannt sein Sticky-Fenster über die tatsächliche Layout-Box-Höhe
  des Elternelements (body) auf, nicht über den sichtbar überlaufenden
  Content. Mit `height:100%` war body's Box hart auf Viewport-Höhe begrenzt,
  obwohl das Grid (hunderte/tausende Kacheln) weit darüber hinausragte
  (overflow:visible lässt das optisch zu, ändert aber body's Layout-Box
  nicht). Scrollte man weiter als eine Viewport-Höhe, verließ man body's Box
  und Sticky griff nicht mehr.
- **Zusatzfund:** `.bulk-bar` hatte zusätzlich `top: 60px` hart verdrahtet
  statt der dynamischen `--topbar-h`/`--lib-nav-h`-Variablen — klebte dadurch
  effektiv UNTER dem fixed Header (unsichtbar dahinter).
- **Lösung:** `body { height: 100% }` → `body { min-height: 100% }` (html
  behält `height: 100%`) — body wächst jetzt mit dem Inhalt, behält aber
  volle Bildschirmhöhe als Minimum. `.bulk-bar` auf dieselben
  `--topbar-h`/`--lib-nav-h`-Variablen umgestellt wie `.breadcrumb`/`.lib-nav`.
- **Nebenfund:** Zu Sessionbeginn lag unfertiges WIP in `stream.go`,
  `player.js`, `style.css`, `app.js` (predictive-playlist-Versuch für den
  Pause/Resume-Bug, siehe oben). Nur `stream.go`/`player.js` wurden
  inhaltlich geprüft, alle vier Dateien wurden per `git checkout --`
  verworfen ohne den style.css/app.js-Diff zu lesen. Ob darin bereits ein
  Sticky-Fix steckte, ist offen (nie committed/gestasht → nicht mehr
  rekonstruierbar) — der Bug selbst lag aber unabhängig davon im
  langjährig bestehenden `body{height:100%}`.
- **Lehre:** vor `git checkout --`/`git restore` auf mehrere Dateien auf
  einmal JEDEN Diff einzeln lesen. Bei `position: sticky`, das nach kurzem
  Scrollen aufhört zu greifen: zuerst `height: 100%` auf body/html-Vorfahren
  prüfen, nicht nur `top`/`z-index`/Selector am Sticky-Element selbst.

### ✅ 🗑 und 🎞 rendern als Tofu-Box mit Hex-Code auf macOS (2026-07-10)
- **Symptom:** Lösch-Icon (Detail-Dialog, Bulk-Bar, Trickplay-Manager,
  Video.js-Player-Button) und Trickplay-Vorhanden-Badge auf Kacheln zeigen
  einen roten/leeren Kasten mit Hex-Text (z. B. „01F"/„5D1") statt des Emojis.
- **Ursache:** `🗑` (U+1F5D1 WASTEBASKET) und `🎞` (U+1F39E FILM FRAMES) haben
  laut Unicode-Emoji-Daten `Emoji_Presentation=No` — ohne den Variation-
  Selector `U+FE0F` (VS16) rendern Browser sie standardmäßig in
  Text-Darstellung statt als farbiges Emoji. Fehlt in dem gewählten
  Font-Stack eine Textglyphe für den Codepoint, fällt Chromium auf den
  „Last Resort"-Font zurück, der den Hex-Codepoint in einer Box zeichnet.
  Alle anderen im Repo verwendeten Emoji (♡, ✓, ↻, ✏, 💾, …) haben
  `Emoji_Presentation=Yes` und sind davon nicht betroffen.
- **Erster Versuch (reichte NICHT):** überall wo `🗑`/`🎞` als sichtbares
  UI-Icon verwendet werden, VS16 angehängt (`🗑️`/`🎞️`). Auf dem
  Test-Mac weiterhin dieselbe Tofu-Box — offenbar ein Blink-internes
  Fallback-Problem für diese Codepoints, das über die Presentation-Property
  hinausgeht, nicht (nur) ein VS16-Thema.
- **Endgültige Lösung:** `🗑`/`🎞` komplett durch inline SVG-Icons ersetzt
  (Feather-Icon-Style Papierkorb/Filmstreifen, `stroke="currentColor"`,
  `width/height="1em"`) — dadurch unabhängig von jeglichem Font-/Emoji-
  Fallback-Verhalten. Zwei wiederverwendbare Konstanten `ICON_TRASH_SVG` /
  `ICON_FILM_SVG` in `helpers.js` (lädt als erstes Modul, global verfügbar).
  Bei `textContent`-Zuweisungen musste auf `innerHTML` umgestellt werden
  (SVG-Markup wird sonst als Text escaped). Der Video.js-Player-Delete-Button
  nutzt `mask-image` mit Data-URI-SVG in `style.css` statt `content: "🗑"`,
  damit `opacity`/`color`-Hover-Regeln unverändert weiter funktionieren.
  Betroffen: `index.html`, `admin.js`, `scan.js`, `cards.js`, `views.js`,
  `style.css` (`.vjs-delete::before`).
- **Lehre:** VS16 ist ein guter erster Reflex bei Emoji-Rendering-Problemen,
  aber KEINE Garantie — bei hartnäckigen Fällen (insbesondere seltener
  genutzte Symbol-Emoji wie 🗑/🎞) ist ein SVG-Icon der zuverlässigere Weg,
  komplett unabhängig von Font-/Browser-/OS-Fallback-Verhalten.

### ✅ Android: Lib-Flash „kein Inhalt" + Privat-Sort stimmt erst nach Toggle — vC 86/87/88 (2026-06-06)
- **Symptom 1:** Privat-Libs (v. a. YouTube) zeigten beim Öffnen kurz alle
  Folgen flach, dann verschwand alles → „kein Inhalt gefunden".
- **Ursache 1:** `LibraryViewModel.doReload` entscheidet anhand
  `state.library?.kind` zwischen Folders (TV/Privat) und flachen Items
  (Movies). War `state.library` noch null (reload() aus dem Settings-Collector
  lief vor load(), oder getLibraries() langsam) → kind="" → usesFolders=false
  → loadItems() lädt ALLE Items flach. `LibraryScreen` unterdrückte flache
  Root-Items nur bei bekanntem tv/private-Kind, nicht bei `null`.
- **Lösung 1:** (a) `doReload` lädt die Library synchron nach wenn
  `state.library==null`, bevor Folders-vs-Items entschieden wird. (b)
  `suppressItemsAtRoot` auf `library?.kind != "movies"` umgestellt (statt
  `tv||private`) → flache Root-Items auch bei unbekanntem Kind unterdrückt.
- **Symptom 2:** Sort „Veröffentlicht" in Privat-Libs stimmte erst nach
  Pfeil-Toggle bzw. Sort-Wechsel-und-zurück. Topbar zeigte „Veröffentlicht",
  die Liste war aber nicht nach Datum. Daten korrekt (Browser sortiert sauber).
- **Ursache 2 (vC 87, echte Ursache):** Stale-Sort-Race beim ersten Laden. Bei
  einer neuen Library wurde `sortMode` erst ASYNCHRON im suspend-Block (nach
  `getLibraries()`) auf `released` gesetzt; bis dahin stand der Data-Class-
  Default `SORT_TITLE`. Lief in diesem Fenster ein `loadItems` (z. B. paralleles
  `reload()` aus dem Settings-Collector), gewann dessen title-/unsortiertes
  Ergebnis per Generation-Guard, während `sortMode` danach auf `released`
  sprang. (vC 86 — asc-Default + Client-Sort — reichte daher nicht.)
- **Lösung 2 (vC 87):** (a) Sort/Richtung/Season SYNCHRON in `load()` setzen
  (vor dem suspend), Kind aus neuem prefs-Cache `kind_<libId>`; Erstbesuch wird
  im suspend-Block nachkorrigiert + frische `reloadGeneration` gezogen, damit
  load()s doReload jede parallele Generation schlägt. (b) Default-Richtung asc;
  `onSortChange`: released→asc NUR in Privat-Libs (sonst desc). (c) Client-
  `released`-Sort (relKey year→"YYYY-01-01" sonst `releasedAt`, nur Privat)
  bleibt als Absicherung.
- **Symptom 3 (vC 88):** In „nur Offline" war Privat-Lib im Folder wieder
  durcheinander (ohne Offline passte es).
- **Ursache 3:** Der Offline-Pfad `doReloadOffline` → `offlineRepository.items()`
  ist eine eigene Strecke, ruft NICHT `loadItems` und sortierte gar nicht
  (rohe Room-Insert-Reihenfolge).
- **Lösung 3:** Client-Sort in Helper `sortItemsForDisplay(items)` extrahiert
  (liest `_state`: Title→Natural, Released+Privat→year/releasedAt) und in BEIDEN
  Pfaden genutzt (loadItems + doReloadOffline) → identische Reihenfolge
  online/offline.
- **NICHT zurückbauen:** Sort MUSS synchron vor dem ersten loadItems stehen —
  die async-Initialisierung war der Bug. Das doReload-Nachladen der Library ist
  die primäre Sicherung gegen den Flash (Symptom 1). Offline + online MÜSSEN
  denselben sortItemsForDisplay-Helper nutzen.

### ✅ Android: Transcode-Seek griff nicht (Drag/Skip blieb stehen) — vC 85 (2026-05-29)
- **Symptom:** Im Server-Streaming-Player zog der User den Fortschrittsbalken
  vor, Trickplay-Vorschau erschien korrekt, aber die Wiedergabe sprang NICHT
  an die neue Stelle. Die 15-s-Skip-Buttons sprangen danach „deutlich weiter
  zurueck" — gefuehlt an die Position, wo der Player ohne das Fingerspulen
  waere. Betrifft NUR Transcode (HLS), nicht Direct Play.
- **Ursache:** Beim Transcode startete die App den HLS-Stream immer mit ffmpeg
  `start=0` und verliess sich auf `exoPlayer.seekTo()`. Die wachsende EVENT-
  Playlist enthaelt aber nur die bereits produzierten Segmente. Ein seekTo
  hinter den produzierten Rand clampt ExoPlayer auf das Seekable-Ende →
  Wiedergabe bleibt stehen. Die `DurationOverrideTimeline` zeigt zwar die volle
  Film-Dauer auf der TimeBar (man kann ueberall hinziehen), der reale Stream
  reicht aber nur bis zur Produktionsfront. Genau das Problem, das der Browser
  mit „Transcode-Seek (Capture-Handler + Session-Restart)" loest — der App
  fehlte das Pendant.
- **Loesung (`ui/player/PlayerScreen.kt`, mirror des Browser-Mechanismus):**
  1. `virtualOffset`-State (ms): die ffmpeg-`start`-Basis der laufenden
     Session. `remember(playbackUrl)` → reset auf 0 bei neuem Item/Quality.
  2. `wrappedPlayer` (ForwardingPlayer) meldet **absolute** Position
     (`getCurrentPosition/Content/Buffered… + virtualOffset`), damit TimeBar
     und Skip-Buttons die echte Film-Position sehen — auch wenn die Session
     mitten im Film gestartet ist.
  3. Seek-Interception: `seekTo`/`seekForward`/`seekBack` gehen durch
     `handleAbsoluteSeek`. Liegt das Ziel im bereits produzierten Material
     (lokal ≤ `super.getDuration()`/buffered + 5 s Toleranz) → lokal seeken;
     sonst `loadHls(start=Zielsekunde)` → neue ffmpeg-Session, `virtualOffset =
     Ziel`, lokale Playlist startet wieder bei 0.
  4. URL-Bau zentral in `loadHls(startSec)`: `…&start=<sec>&_t=<now>` (das `_t`
     bricht nur den OkHttp-Cache, der Server ignoriert es beim Session-Key
     `(item,profile,audio,int(startSec),deint)`).
  5. Transcode-**Resume** ebenfalls gefixt: nicht mehr `seekTo(resumeMs)`
     (clampte), sondern direkt `start=<resumeSec>` + `virtualOffset` setzen.
  6. Resume-Speichern beim Verlassen/Pause schreibt jetzt die **absolute**
     Position (`currentPosition + virtualOffset`).
- **NICHT zurueckbauen:** Ohne Session-Restart kann die App im Transcode nicht
  ueber die Produktionsfront hinaus springen. Direct Play (ganze Datei per
  Range) bleibt unveraendert — dort ist `currentIsTranscode=false`, die Seek-
  Overrides forwarden 1:1.
- **Server-Seite unveraendert** — `start`-Param + Session-Keying existierten
  schon fuer den Browser.

### ✅ Android: lokale Bibliotheken zwischen Usern geleakt (2026-05-25)
- **Symptom:** Auf einem geteilten Tablet sah jeder Goldfish-User in
  Settings/Home/Suche die lokalen Bibliotheken der anderen User. Konkret:
  Christian sah Alex' Privat-Lib und konnte deren Inhalte abspielen.
- **Ursache:** Lokale Libraries lebten in der Room-DB (`goldfish-local.db`)
  ohne User-Bezug. `LocalLibraryRepository.observeLibraries()` lieferte
  alle Eintraege, unabhaengig vom aktuell eingeloggten User.
- **Loesung:** Schema-Bump v3→v4 mit `local_libraries.ownerUsername TEXT`.
  Beim Anlegen einer Lib wird der aktuell eingeloggte Goldfish-User
  (via `AuthRepository.authStatus.value?.username`) als Owner gesetzt.
  Alle UI-Flows (LocalLibrariesViewModel, HomeViewModel, SearchViewModel)
  nutzen jetzt `observeLibrariesForUser(currentUser)` mit
  `flatMapLatest(authStatus)` — bei Login-Wechsel switch'ed der Flow
  automatisch.
- **Defense-in-depth:** `LocalLibraryViewModel.load` und
  `LocalPlayerViewModel.load` haben einen harten Owner-Check, falls
  jemand per Deep-Link auf eine fremde Library-ID navigiert.
- **Auto-Migration:** Bestehende Libs ohne Owner (NULL) werden beim
  ersten Aufruf der Settings dem aktuell eingeloggten User assigniert
  (`claimUnownedFor(username)`). Im Familien-Setup mit Tablet-
  Primary-User passt das fast immer; sonst kann der User die Lib loeschen.
- **NICHT zurueck:** `observeLibraries()` (ohne User-Filter) ist im
  Repository nur noch fuer interne Jobs (recoverMissingThumbnails)
  exposed. UI-Code MUSS `observeLibrariesForUser` nutzen. Bei jedem
  weiteren Schema-Bump (v5, v6 …) NICHT vergessen LOCAL_MIGRATION_x_y in
  AppModule.provideLocalAppDatabase.addMigrations(...) mit zu listen.
- **⚠ REGRESSION + Re-Fix (2026-09-02):** der obige Fix wurde in einer
  spaeteren Session zweimal wieder aufgeweicht, OHNE dass dieser Eintrag
  aktualisiert wurde — das hat den Leak wieder scharf gemacht (User-Report:
  "Börnie sieht die lokalen Bibliotheken von Christian"):
  1. `HomeViewModel.kt` nutzt seit einem authStatus-Timing-Fix (Admin wurde
     durch `flatMapLatest(authStatus)` in Edge-Cases ausgesperrt) wieder
     das ungefilterte `observeLibraries()` + einen CLIENT-SEITIGEN Filter
     — der aber `ownerUsername.isNullOrBlank()` als "gehoert mir" fuer
     JEDEN User durchliess statt nur fuer den rechtmaessigen Owner.
  2. `claimUnownedFor` (der hier oben beschriebene Auto-Migration-Claim)
     wurde in `LocalLibrariesViewModel.kt` komplett entfernt (eigener,
     unabhaengiger Bugfix: "hat Libs an den falschen User zugeordnet wenn
     der Familien-User zuerst eingeloggt war") — aber NIRGENDS sonst neu
     aufgerufen. Damit blieben NULL-Owner-Libs fuer immer NULL, UND
     HomeViewModel zeigte sie deshalb dauerhaft JEDEM User.
  3. `LocalLibraryViewModel.load()`s "Defense-in-depth Owner-Check" aus
     Punkt 1 des Fixes oben existiert im aktuellen Code NICHT — dort steht
     ein expliziter Kommentar, dass der Check bewusst weggelassen wurde
     ("Admin sperrt sich aus"-Sorge), im Vertrauen darauf, dass der Aufrufer
     (Home/Settings) schon nur erlaubte IDs uebergibt. Das war also die
     EINZIGE Schutzschicht — und genau die war in Punkt 1 kaputt.
  **Re-Fix:** `HomeViewModel.displayLibraries()`-Filter ist jetzt wieder
  strikt (`ownerUsername == currentUser`, kein Null-Passthrough mehr) —
  unclaimed Libs sind jetzt fuer NIEMANDEN sichtbar statt fuer ALLE, bis
  sie ueber "Andere Bibliotheken" → "Mir zuordnen" (Settings) manuell
  geclaimt werden. Toter `claimUnownedFor`-Code + zugehoerige
  LocalLibrariesViewModel-Leiche (`appContext`/`prefs`/
  `LEGACY_CLAIM_DONE_KEY`, nie gelesen) entfernt.
  **Lehre:** bei sicherheitsrelevanten Fixes, die spaeter aus Usability-
  Gruenden nochmal angefasst werden, MUSS dieser Decision-Log-Eintrag
  mitgepflegt werden — sonst hält die Doku einen Fix für lebendig, der
  laengst durch einen unabhaengigen, korrekt begruendeten Folge-Fix
  wieder ausgehebelt wurde.

### ✅ Android: NoDeclaredBrand-MP4 — Extractor lehnt Sniff ab (2026-05-25)
- **Symptom:** Manche .mp4 lieferten im LocalPlayer den Fehler
  `None of the available extractors (g91, np2, e61, a41, xu4, r6, ...)
  could read the stream. {contentIsMalformed=false, dataType=1}
  sniff failures: [NoDeclaredBrand]`. Files spielen in VLC einwandfrei.
- **Ursache:** ExoPlayer's `Mp4Extractor.sniff()` lehnt MP4-Files ab,
  deren `ftyp`-Box einen unbekannten Brand deklariert (oder gar kein
  `ftyp` hat). Die Files sind strukturell valides MP4 — nur der
  Brand-Code ist exotisch oder Custom. Der Default-Sniffer wird sehr
  konservativ ausgewertet und liefert false, sodass die Datei nie
  einen Decoder sieht. FFmpeg-Extension hilft NICHT, weil sie Decoder
  liefert, nicht Demuxer.
- **Loesung:** Eigene `TolerantExtractorsFactory`
  (`ui/locallib/TolerantExtractorsFactory.kt`) wrapt
  `DefaultExtractorsFactory` und haengt einen `ForcedMp4Extractor` ans
  Ende der Extractor-Liste. Dessen `sniff()` gibt immer `true` zurueck,
  alle anderen Methoden delegieren auf einen frischen `Mp4Extractor`.
  Wenn das File wirklich kein MP4 ist, scheitert das Parsen mit klarem
  Fehler — aber NoDeclaredBrand-Files laufen jetzt.
- **Wiring:** ExoPlayer.Builder bekommt
  `setMediaSourceFactory(DefaultMediaSourceFactory(context,
  TolerantExtractorsFactory()))`. Der Force-Extractor steht NACH den
  Default-Sniffern, sodass normale Files (auch mkv/webm/avi) weiterhin
  vom richtigen Default-Extractor uebernommen werden.
- **Nicht ueberall verallgemeinerbar:** TolerantExtractorsFactory ist
  ausschliesslich im LocalPlayer (lokale SAF-URIs) aktiv. Der
  Server-Streaming-PlayerView braucht das nicht — Streams kommen
  einheitlich von goldfish-Server mit korrektem ftyp.

### ✅ Android: viele .mp4 mit "Container/Format nicht unterstuetzt" (2026-05-25)
- **Symptom:** In den lokalen Bibliotheken der Android-App schlugen
  zahlreiche .mp4 mit "⚠ Wiedergabe nicht moeglich · Container/Format
  nicht unterstuetzt" fehl. Dieselben Files liefen in VLC bzw. ueber den
  Datei-Manager ohne Probleme — also klares Codec-Problem, nicht
  Container-Schaden.
- **Ursache:** ExoPlayer/Media3 nutzt per Default nur die System-
  MediaCodec-Decoder. Manche Codecs (AC-3-Audio, DTS, einzelne HEVC-
  Profile, ProRes, …) sind je nach Geraet/Android-Version nicht im
  System-MediaCodec verfuegbar — der Decoder lehnt das Format ab. VLC
  bringt sein eigenes FFmpeg mit und kennt das alles.
- **Loesung:** `nextlib-media3ext`
  (`io.github.anilbeesetti:nextlib-media3ext:1.9.3-0.12.0`) als
  Dependency, vorgebaute FFmpeg-Decoder-Extension fuer Media3. Im
  ExoPlayer-Builder wird `NextRenderersFactory(context)
  .setExtensionRendererMode(EXTENSION_RENDERER_MODE_PREFER)` gesetzt —
  FFmpeg uebernimmt wenn er kann, sonst fall-back auf System-MediaCodec.
  Standard h264/h265-Files bleiben HW-decoded; nur problematische Files
  landen bei FFmpeg.
- **AAB-Wachstum:** 9 MB → 19,5 MB durch native FFmpeg-Binaries fuer
  arm64-v8a + armeabi-v7a + x86_64.
- **Zweite Sicherung:** Im Error-State des LocalPlayer gibt es zusaetzlich
  einen "In anderem Player oeffnen"-Button (ACTION_VIEW + FLAG_GRANT_READ_
  URI_PERMISSION auf die SAF-URI). Faengt die 10 % Edge-Cases ab, bei
  denen auch FFmpeg versagt — User waehlt VLC/MX/etc. im System-Chooser.
- **WICHTIG (Versions-Pinning):** Die nextlib-Version folgt dem Schema
  `<Media3-Version>-<NextLib-Version>` (z.B. `1.9.3-0.12.0`,
  `1.10.0-0.12.1`). Bei jedem Media3-Upgrade MUSS nextlib mit der gleichen
  Major-Minor mitkommen. Compile bleibt sonst gruen (Java-API
  kompatibel) — Runtime crasht mit `NoClassDefFoundError` weil die
  nativen .so-Binaries nicht zur Java-API passen. Liste der Releases:
  https://github.com/anilbeesetti/nextlib/releases.
- **NICHT zurueckbauen:** Ohne FFmpeg-Extension waeren die User-Files
  reihenweise nicht mehr spielbar. Wenn das Native-Footprint-Wachstum
  stoert, ABI-Splits konfigurieren statt die Extension komplett zu
  entfernen.

### ✅ Edit-Metadata fuer Privat-Libs freigegeben (2026-05-16)
- **Bisher:** Pencil-Button „Metadaten bearbeiten" war in Privat-Libs
  ausgeblendet (`canEditMeta` checkte `kind !== "private"`). Der Server-
  Endpoint `POST /api/items/{id}/metadata-manual` funktionierte aber
  schon fuer alle Lib-Typen.
- **Aenderung in `player.js`:** `canEditMeta = state.me.isAdmin` — kein
  Lib-Kind-Check mehr. Admin sieht den Pencil auch in YouTube/Urlaubs-
  Libs.
- **Aenderung in `matching.js openEditMetaDialog`:** zwei separate
  Vorbefuellungs-Branches je Lib-Kind:
  - Privat-Libs: Default-Titel = Dateiname **ohne Endung** (`.mp4`
    etc. via Regex gestrippt). releaseDate aus `it.releasedAt` (yt-dlp
    MKV-DATE), Runtime aus `it.durationSec`.
  - Movies/TV (unveraendert): Show-Name aus rel_path[0], Episodencode
    in der Beschreibung.
- **Wirkung:** User kann fuer ein YouTube-Video „Bauarbeiten\_2024-03-15.mp4"
  einen sprechenden Titel „Garage aufgeraeumt" eintragen + speichern.
  Der Server legt einen `tmdb_type=custom`-Eintrag an
  (`TMDBID = -itemID` fuer Uniqueness) und bindet das Item daran. Die
  VideoCard zeigt danach den eingegebenen Titel als displayTitle.

### ✅ Generische Episoden-Titel „Folge 1/2/…" bei TMDB-Lücken (2026-05-14)
- **Symptom:** Bei einigen Serien (z.B. Sullivans Crossing) zeigte das Browser-
  Frontend statt echter Episodentitel nur „Folge 1", „Folge 2", … —
  obwohl TMDB die englischen Episodentitel hat.
- **Ursache:** TMDB API mit `language=de-DE` liefert selbst die generischen
  Defaults wenn fuer eine Episode keine deutsche Uebersetzung hinterlegt
  ist. Server hat `ep.Name` 1:1 uebernommen → User sieht den TMDB-Default.
- **ZWEI Pfade müssen gefixt werden** — wer nur einen patched, kuriert
  nur die Hälfte:
  1. `tmdb.Client.GetSeason` — fuer die Browser-Live-Anzeige der
     Staffel-Liste (api/series.go).
  2. `tmdb.Client.GetEpisode` — fuer das DB-Enrichment in
     `enrich/worker.go` Zeile 471 (pro einzelne Episode, schreibt
     in `metadata`-Tabelle).
- **Fix:** beide Methoden machen jetzt einen zweiten Call mit
  `language=en-US`, wenn mindestens eine Episode einen generischen
  Namen oder leeres Overview hat. Pro Episode wird ein generisches
  `Name` durch das englische Pendant ersetzt (sofern dort nicht auch
  generisch), und leeres `Overview` durch das englische gefuellt. Der
  Merged-Result wird gecached.
- **Helper:** `isGenericEpisodeName(name, epNum)` erkennt „Folge N",
  „Episode N", „Episodio N", „Épisode N" und leere Strings.
- **`c.get`** wurde erweitert: caller-gesetzte `params.language` wird
  nicht mehr von `c.language` ueberschrieben.
- **Bestehende DB-Eintraege** mit „Folge N"-Namen werden nicht
  automatisch korrigiert — User kann via „⚠ Episoden neu zuordnen"
  im Show-Header die betroffenen Folder neu enrichen (verwendet den
  jetzt korrigierten GetSeason-Pfad).
- **NICHT zurueck**: der zweite Call ist conditional + gecached, kostet
  also nur einen extra Request pro Show-Season bei Erstbesuch von
  generischen-Title-Shows. Sprachenneutral falls c.language eh „en…".



### ✅ Trickplay 4K-Files schlugen massenhaft mit „signal: killed" fehl (2026-05-10)
- **Symptom:** 146 Failed-Items im Trickplay-Manager, davon 142× nur
  `ffmpeg: signal: killed ()` mit leerem stderr. Betroffen fast nur
  4K-h264-60fps-Files (3840×2160 / 4096×2160), 13–78 min, 20–32 Mbps.
- **Ursache:** `fps=1/10` ist nur ein Output-Filter — der Decoder muss
  trotzdem JEDEN Frame durchlaufen, auch wenn nur 1 von 600 ausgegeben
  wird. Bei 4K-60fps sind das hunderttausende Frames pro File. Timeout
  `dur/10 + 120s` reichte für 78-min-File nicht (= ~10 min Cap).
- **Lösung (in `internal/trickplay/worker.go`):**
  1. **`-skip_frame nokey`** vor `-i` → Decoder gibt nur Keyframes raus,
     ~50× schneller. Bei typischem Keyframe-Abstand ≤5s bleibt jeder
     Sprite-Slot (10s-Intervall) nah genug am Soll-Timestamp.
  2. **`-err_detect ignore_err -fflags +discardcorrupt+genpts`** → kaputte
     NAL-Units / Invalid-Stream-Daten brechen ffmpeg nicht mehr ab.
  3. **Erweiterte Fallback-Pattern**: `Could not find ref`, `Failed to
     inject frame`, `Failed to query surface`, `hwdownload` triggern jetzt
     auch den Software-Fallback.
  4. **Klarere Timeout-Meldung**: wenn `tctx.Err() == DeadlineExceeded`,
     bekommt der User `"timeout nach 10m0s"` statt `signal: killed ()`.
  5. **Timeout-Bump**: `dur/5 + 5min` statt `dur/10 + 2min` (29-min-File
     bekommt jetzt ~11 min Timeout, 80-min-4K ~21 min). Caps bleiben.
- **Resultat:** 146 → 1 Fehler (das eine ist ein kaputtes mp4 ohne
  Streams). „↻ Fehler erneut versuchen" gerollt — fast alle durch.
- **NICHT zurückbauen:** `-skip_frame nokey` ist die Hauptmedizin gegen
  4K-Timeouts; die Fallback-Pattern + Tolerance-Flags fangen den
  Rest auf.

### ✅ `COLLATE NATURAL` ist SQLite-Reserved-Word — bricht ORDER BY (2026-05-11)
- **Symptom:** Nach Einbau einer Custom-Collation für Natural-Sort
  (Zahlen als ganze Werte) reagierte `/api/items?sort=title` mit HTTP 500
  `SQL logic error: near "NATURAL": syntax error`. User konnte keine
  Bibliotheken mehr wechseln.
- **Ursache:** `NATURAL` ist in SQLite reserviertes Keyword (für
  `NATURAL JOIN`). Bei `ORDER BY … COLLATE NATURAL` parst der Tokenizer
  das als beginnenden NATURAL-JOIN-Ausdruck und failt.
- **Lösung:** Collation umbenannt auf `NATSORT` (registriert in
  `internal/store/sqlite.go` via `sqlite.MustRegisterCollationUtf8`,
  Impl in `internal/store/collation.go`). Gleiches Verhalten,
  funktioniert in ORDER-BY-Klauseln.
- **NICHT zurück auf `NATURAL`** — und generell: bei neuen Custom-SQL-
  Identifiern (Collations, Funktionen, Spalten-Aliasen) IMMER vor Push
  einen echten Query gegen die DB feuern, Go-Unit-Tests fangen
  Reserved-Word-Konflikte nicht.

### ✅ Sort „Veröffentlicht" sortierte nach Datei-mtime statt Kino-Release (2026-05-08)
- **Symptom:** „Veröffentlicht"-Sortierung in Filme-Lib gab durcheinandere
  Reihenfolge — Kachel zeigt z. B. "2025", Sortierung packt den Film
  irgendwo zwischen 1990er-Filme. Browser + App gleichermaßen betroffen.
- **Ursache:** `internal/store/sqlite.go` `case "released"` sortierte nach
  `COALESCE(i.released_at, i.mod_time)` — also ffprobe-creation_time bzw.
  File-mtime der Datei auf Disk. Das ist meist das Encoding-/Download-
  Datum, NICHT das Kino-Release. Aber die Kachel zeigt `metadata.year`
  vom TMDB. → Sortierung passte nie zum Anzeige-Datum.
- **Lösung:** SortKey auf `metadata.release_date` umgestellt mit
  Fallback-Kette:
  ```sql
  COALESCE(
    NULLIF(m.release_date, ''),                            -- TMDB primary
    CASE WHEN m.year>0 THEN printf('%d-01-01', m.year) END, -- Jahr-only
    (SELECT mp.release_date FROM metadata mp                -- Episoden
       WHERE mp.id = m.parent_id AND mp.release_date != ''),
    i.released_at,                                          -- YouTube/yt-dlp
    i.mod_time                                              -- last resort
  )
  ```
- **NICHT** zurück auf `i.released_at` — da steht das Wrong Date drin.
  Wer einen TMDB-Match hat, soll TMDB-Datum sehen UND danach sortieren.

### ✅ Stack-Update via Portainer-API zerstört Stack-Env-Variablen (2026-05-08)
- **Symptom:** Nach mehreren `PUT /api/stacks/37`-Calls antwortete
  `<öffentliche-domain>/api/auth/oidc/login` mit **503 „SSO nicht
  konfiguriert"**. Browser-SSO-Login tot. App war nicht betroffen, weil sie
  Email/Passwort nutzt.
- **Ursache:** Portainer-Stack-Update löscht das `Env`-Array, wenn der
  PUT-Body nur `{"stackFileContent": ..., "prune": false, "pullImage": false}`
  enthält. Das Compose-File nutzt `${OIDC_*:-}`-Substitution → bei leerer
  Stack-Env wird der Container mit leeren OIDC-Werten gestartet → goldfish.go
  schaltet OIDC-Routes auf 503.
- **Lösung:** Beim Stack-Redeploy IMMER das `env`-Array mitsenden — entweder
  vorher per `GET /api/stacks/37` rausziehen und 1:1 zurück schreiben, oder
  explizit die 4 OIDC-Vars setzen. Memory: `project_stack_env_pitfall.md`.
- **NICHT** wieder vergessen — der Bug zerstört SSO ohne Vorwarnung.

### ✅ DeepL-API-Key wurde als maskierter String in der DB gespeichert (2026-05-08)
- **Symptom:** Whisper-VTTs für `de` und `en` waren bytegleich, kein
  Übersetzungsfehler im Log, aber Text war englisch. DeepL-Test mit dem
  echten Key (vom User-Screenshot) lieferte 200, vom Server aus 403.
- **Ursache:** Klassischer Mask-Save-Roundtrip-Bug. `whisperGetSettings`
  gab `deeplKey` maskiert (`a22e…d:fx`) zurück. Frontend füllte das
  `<input>`-Feld damit. Beim Save schickte die UI den maskierten Wert
  zurück → Server überschrieb DB-Wert mit der Maske → DeepL antwortet
  auf den Müll-Key mit 403. Mein zweiter Bugfix in `TranslateVTT`
  (Original-Zeile bei Fehler behalten) maskierte das zusätzlich, weil
  alle Cues unverändert blieben.
- **Lösung:** Server gibt API-Keys NICHT mehr zurück, nur ein bool
  `deeplKeySet`/`libreKeySet`. Save überschreibt Keys nur wenn das Feld
  nicht leer ist und keine Maske (`…` oder `***`) enthält. Frontend zeigt
  Placeholder „(gespeichert — leer lassen zum Behalten)".
- **NICHT** auf den maskKey-Roundtrip zurück. Wenn ein neues Setting einen
  Secret-Wert hat: bool-Indikator + leeres Eingabefeld, nie maskieren-und-
  zurückschicken.

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

### ✅ whisper-cli: libwhisper.so.1 nicht gefunden (2026-05-05)
- **Symptom:** `exit status 127 — whisper-cli: error while loading shared libraries: libwhisper.so.1`
- **Ursache:** cmake baut whisper.cpp default als dynamische Library. Die `.so` wird
  in den Build-Stage kopiert, aber nicht in den Runtime-Stage.
- **Lösung:** `-DBUILD_SHARED_LIBS=OFF` in cmake → statisches Binary, keine `.so` nötig.
- **NICHT** zurück auf dynamisches Linking ohne auch `libwhisper.so.1` in den Runtime-Stage zu kopieren.

### ✅ whisper-cli: exit status 3 — Modell nicht geladen (2026-05-05)
- **Symptom:** Job schlägt sofort fehl, Meldung „exit status 3".
- **Ursache:** Falscher Download-URL — Format-String hatte `ggml-%s.bin` statt `%s.bin`,
  was zu `ggml-ggml-small.bin` führte. Datei existierte nicht auf Disk.
- **Lösung:** URL-Format auf `%s.bin` korrigiert; Modellname inkl. `ggml-`-Präfix.
  Klare Fehlermeldung für exit status 3: „Modell nicht gefunden — bitte im Admin-Menü herunterladen".

### ✅ Whisper-Timeout killt 4K-Trickplay (signal: killed) (2026-05-05)
- **Symptom:** Trickplay-Generierung für 4K-Dateien schlägt mit `ffmpeg: signal: killed` fehl,
  obwohl genug RAM vorhanden ist.
- **Ursache:** Trickplay-Timeout-Cap war 30 Minuten. Für 4K-Dateien bei Software-Decode-Fallback
  (z.B. HEVC Main10 mit VAAPI-Quirks) kann ffmpeg deutlich länger brauchen.
- **Lösung:** Timeout-Cap nach Auflösung gestaffelt: 4K (≥2160p) → 3h, 1080p → 60min, Rest → 30min.

### ✅ VAAPI Trickplay schlägt bei 10-bit HDR (HEVC Main10) fehl (2026-05-05)
- **Symptom:** VAAPI-Trickplay für HEVC Main10 (HDR) Dateien schlägt fehl;
  Software-Fallback greift, der für 4K langsam ist und in den Timeout läuft.
- **Ursache:** `hwdownload,format=nv12` erwartet 8-bit Input, HEVC Main10 liefert 10-bit.
  `scale_vaapi` ohne `format=nv12` gibt `p010le` aus statt `nv12`.
- **Lösung:** `scale_vaapi=...:format=nv12` explizit setzen — erzwingt 8-bit Ausgabe
  vor hwdownload. Filter-Chain: `fps=1/N,scale_vaapi=w=W:h=H:...:format=nv12,hwdownload,format=nv12,...`

### ✅ Video.js Untertitel werden nicht angezeigt (2026-05-05)
- **Symptom:** Untertitel-Track ist im Dropdown wählbar, wird aber nicht im Video angezeigt.
- **Ursachen (zwei):**
  1. `addRemoteTextTrack({default: true})` aktiviert den Track in Video.js nicht zuverlässig.
  2. Kein Change-Handler auf `#subSelect` — Dropdown-Änderungen hatten keinen Effekt.
- **Lösung:** `applySubtitleChoice(vjs, item, subs)` als eigene Funktion; entfernt alle
  alten Tracks, fügt neuen hinzu, ruft dann `tracks[i].mode = "showing"` explizit auf
  (sofort + nach 300ms Timeout). Change-Handler auf `#subSelect` verdrahtet beim Player-Open.
  Flag `subSel.dataset.subHandlerAttached` verhindert doppelte Handler-Registrierung.

### ✅ SubtitleJob-Felder nicht in camelCase (undefined in UI) (2026-05-05)
- **Symptom:** Whisper-Popover zeigte „? undefined" statt Job-Status.
- **Ursache:** Go-Struct `SubtitleJob` hatte keine JSON-Tags → Felder als `Status`, `Language`
  serialisiert; JavaScript erwartete `status`, `language` (lowercase).
- **Lösung:** JSON-Tags hinzugefügt: `json:"status"`, `json:"language"` etc.

### ✅ Numerische Episoden-Codes (104 = S1E04)
- **Symptom:** Dateien wie `Derrick 104.avi` wurden nicht als Episoden erkannt.
- **Ursache:** Parser kannte nur SxxExx und NxN.
- **Lösung:** `ParseEpisodeFile` für TV-Kontext mit zusätzlicher Regex für 3–4-stellige
  Zahlen; Jahres-Ausschluss (1900–2099) und Plausibilitäts-Check (S 1–29, E 1–99).
  Bei Filmen **nicht** aktiv (`Matrix 1999 1080.mkv` soll nicht S10E80 sein).

