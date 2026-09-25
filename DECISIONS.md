## Bekannte Probleme & Lösungen (Decision Log)

### ✅ Transcodes abgelehnt, obwohl kein ffmpeg lief — fertige Sitzungen belegten das Budget (2026-09-25, v1.4.35)

- **User-Meldung:** „es klappen schon wieder keine Transcodes. Als wäre ich
  wieder in einem Limit."
- **Befund:** Log `ABGELEHNT … Budget erschoepft (355+50 von 400 Punkten,
  8 Sitzungen aktiv)`. Gleichzeitig zeigte `docker top goldfish` **keinen**
  ffmpeg-Prozess.
- **Ursache:** Kurze Clips wandelt VAAPI in Sekunden bis Minuten komplett um.
  Erfolgreich beendete Sitzungen bleiben bewusst bis zum 30-Min-Leerlauf-GC im
  Pool, weil der Client die letzten Segmente noch abholen muss (Lehre aus dem
  AV1-„Source error" vom 2026-09-17). `activeCostLocked`/
  `activeVideoSessionsLocked` zählten sie aber weiter voll. Beim schnellen
  Durchklicken vieler Videos war das Budget so mit „Geistern" gefüllt. Der Fix
  vom 17.09. hatte nur *fehlgeschlagene* Sitzungen ausgebucht.
- **Fix:** Beide Zählfunktionen überspringen `Done()==true`. Die Sitzungen
  bleiben im Pool, belegen aber keine Punkte und zählen nicht zum Anzahl-Deckel.
  Laufende, nur verlassene Sitzungen zählen weiter, weil sie die GPU tatsächlich
  belasten. Die Schutzgrenze gegen den Host-Absturz ist damit unverändert.
- **Test:** `TestFinishedSessionsDoNotBlockBudget` (mit Gegenprobe).

### ✅ WebVTT-Untertitel wurden nicht eingeblendet + Sidecar-Dateien komplett unsichtbar (2026-09-15)

- **User-Meldung:** „kann Goldfish webvtt Untertitel verarbeiten?" — bei den
  Videos eines bestimmten Kanal-Ordners in einer Privat-Bibliothek „gibt es
  webvtt, aber die werden bisher nicht angezeigt".
- **Bestandsaufnahme am laufenden Server** (Goldfish-API mit dem
  `?session=<token>`-Fallback, kein Shell-Zugriff nötig): der Kanal-Ordner
  enthält 17 Items, alle `av1`/`opus` (yt-dlp-Downloads). **13 davon
  haben eine EINGEBETTETE `webvtt`-Spur** (`language=deu`, `title=German`,
  Stream-Index 2), 4 haben gar keine Untertitel-Spur. `GET
  /api/subtitle/193767/2.vtt` lieferte auf Anhieb gültiges, korrekt
  übersetztes WebVTT — die Server-Seite war also nie das Problem.
- **Ursache 1 (erklärt die 13 eingebetteten):** zwei konkurrierende
  `change`-Handler auf `#subSelect`, plus Handler-Akkumulation über den
  Player-Reuse-Pfad. Details + abgeleitete Regel in CLAUDE.md
  („⚠ #subSelect darf nur EINEN Change-Handler haben"). Kurz: der
  `player.js`-Handler trug `vjs`/`item`/`subs` in einer Closure und wurde bei
  jedem weiteren Video ein zweites, drittes … Mal angehängt, weil der
  Reuse-Pfad nur das `dataset`-Flag löschte statt den Listener zu entfernen.
  `applySubtitleChoice` ist async und räumt als Erstes alle Text-Tracks ab —
  ein veralteter Aufruf mit der ID eines früher geöffneten Videos gewann
  regelmäßig das Rennen, lief in einen 404 und hinterließ keinen Track.
  Reproduzierbar erst ab dem ZWEITEN im selben Tab geöffneten Video, was die
  Meldung „manchmal" erklärt.
  Das Protokoll (`activity_log`, Gerät „Firefox · Linux") hat die Eingrenzung
  auf den Browser überhaupt erst möglich gemacht — ohne die Geräte-Spalte wäre
  zuerst die native Linux-App verdächtig gewesen.
- **Ursache 2 (die 4 ohne eingebettete Spur):** Untertitel-DATEIEN neben dem
  Video waren strukturell unsichtbar — `supportedExt` im Scanner kennt nur
  Video-/Audio-Endungen, und es gab repo-weit keine Stelle, die im Medienordner
  nach `.vtt`/`.srt` gesucht hätte. Neues Feature, siehe CLAUDE.md
  „Sidecar-Untertitel".
- **Lehre:** „Kann X das Format?" ist im Zweifel drei verschiedene Fragen —
  eingebettete Spur, erzeugte Datei, Sidecar-Datei. Hier war das Format
  (WebVTT) von Anfang an voll unterstützt, die eine Hälfte des Problems lag im
  Frontend-Eventhandling und die andere darin, dass eine ganze QUELLE fehlte.
  Erst die Trennung „welche 13 Dateien haben was, welche 4 nicht" hat beide
  Ursachen sichtbar gemacht; eine einzelne Beispieldatei hätte in die Irre
  geführt.

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

## GoldfishApple (Mac/iOS/tvOS) — historische Bugfixes

Ausführliche, aus CLAUDE.md ausgelagerte Fassung der abgeschlossenen GoldfishApple-Bugs
(dort steht nur noch die Kurzfassung im Abschnitt „Gelöste Bugs"). Reihenfolge = Build-Chronologie.

### ✅ Fenster-Verschwinden-Bug (Build 0100, 2026-08-19)
- **Symptom:** Player-Fenster verschwand teils komplett (kein Dock-Icon-Klick brachte es zurück).
- **Ursache (zwei unabhängige Root-Causes):** (1) `PlayerLaunchCoordinator.pendingPlayer/
  pendingLocalPlayer` wurden beim Schließen nie auf `nil` zurückgesetzt → SwiftUI/AppKit-
  Fenster-Bookkeeping lief auseinander. (2) Das Hauptfenster konnte über den grünen Button
  in einen eigenen nativen Vollbild-Space rutschen (`onScreen=false`, Frame = Bildschirmgröße)
  und landete dann auf einer anderen Space als der Player.
- **Lösung:** Coordinator-Felder korrekt zurücksetzen; `window.collectionBehavior =
  [.managed, .participatesInCycle, .canJoinAllSpaces]` als Ganzwert-Neuzuweisung (`.remove()`/
  `.insert()` auf der Property hielt NICHT zuverlässig) + Fenster zieht sich beim Start auf
  `screen.visibleFrame`.
- **Debugging-Lehre:** NSLog+Console.app funktionierte trotz aktivem Streaming NIE (0
  Mitteilungen trotz Reproduktion) — Umstieg auf File-Logging (`~/Desktop/goldfish-window-
  debug.log`, `FileHandle`-Append) war der Durchbruch. `Read`/`cat` auf `~/Desktop/*`
  scheitert aus dem Coding-Environment an macOS-Datenschutz (EPERM) — User muss die Datei
  selbst öffnen oder per `! cat …` liefern.

### ✅ Bibliotheks-Vorschaubilder offline weg (mehrfach „gefixt", Build 165)
- **Symptom:** Library-Kacheln fielen offline auf den farbigen Gradienten-Kreis zurück statt
  das zuletzt gecachte Poster zu zeigen.
- **Ursache:** `LibrariesView.load()` befüllte `previewURLs` NUR im Erfolgsfall von
  `fetchLibraries()`. Offline → `catch` → `previewURLs` blieb leer. Frühere Fixes
  (Offline-Lib-Liste in UserDefaults, `GoldfishLibraryPreviews/`-Cache, `file://`-Handling in
  `PosterImage`) waren nötig, aber keiner hängte den Cache-Read in den Offline-Zweig.
- **Lösung:** `hydratePreviewsFromCache()` läuft jetzt IMMER, unabhängig vom Netzwerk-Call.
  Zusätzlich zieht `loadPreviews()` nur noch EINMALIG ein Zufalls-Poster pro Bibliothek
  (User: „genau die AKTUELLEN Bilder speichern") — vorhandene Cache-Datei wird behalten statt
  bei jedem Öffnen überschrieben.

### ✅ Offline→online: Bibliotheken kommen nicht wieder, „Session abgelaufen" (Build 166)
- **Symptom:** Nach Offline-Phase blieb die App auf einer rohen 401-Fehlermeldung hängen, ohne
  Retry-Button oder Weg zurück zum Login.
- **Ursache:** (1) `RootView.refreshSessionStatus()` lief nur EINMAL beim Start. (2)
  `LibrariesView.load()`/`HomeView` behandelten 401 fälschlich als `isOffline=true` (der Server
  hatte ja geantwortet) und zeigten bei leerem Cache die Server-Rohmeldung als Sackgasse. (3)
  nichts routete eine tote Session zurück zu `LoginView`.
  `internal/api/auth.go`.
- **Lösung:** `GoldfishClient.isAuthError()` + `markSessionInvalid()` (löscht lokalen Login-
  State + Cookies → `RootView` zeigt `LoginView`); `RootView` gleicht die Session bei jedem
  Wechsel in den Vordergrund neu ab (`scenePhase == .active`); `LibrariesView`/`HomeView`
  unterscheiden Connectivity- vs. Auth- vs. sonstige Fehler und haben einen „Erneut
  versuchen"-Button.

### ✅ SSO-Login in der Mac/iOS-App schlug still fehl (Build 167)
- **Symptom:** Klick auf „Mit SSO anmelden" schloss den Flow, App blieb aber auf dem
  Login-Screen ohne Fehlermeldung.
- **Ursache:** `OIDCWebViewRepresentable`-Coordinator kopierte die WKWebView-Cookies EINMALIG
  im `didFinish` für `/` — das `Set-Cookie` aus der OIDC-Callback-Weiterleitung war zu dem
  Zeitpunkt oft noch nicht im WKWebView-Cookie-Store (bekanntes WKWebView-Timing).
- **Lösung:** `syncCookies` pollt jetzt bis ~2,5 s (8 × 0,3 s) auf den `goldfish_session`-
  Cookie; kommt keiner, meldet der Flow explizit einen Fehler. `LoginView.refreshAfterOIDC()`
  fasst zusätzlich 5× nach.

### ✅ SSO-Sheet war auf macOS leer (Build 168)
- **Symptom:** Der eigentliche „SSO tut nichts"-Grund, kam VOR dem Cookie-Sync-Bug oben.
- **Ursache:** `OIDCLoginView` setzte keine explizite Größe; ein `NSViewRepresentable`
  (WKWebView) hat keine intrinsische Größe → das `.sheet` schrumpfte auf Header + „Abbrechen",
  Authentik-Seite bekam 0 Höhe.
- **Lösung:** `#if os(macOS) .frame(minWidth:720, minHeight:760, ideal 900×900)` auf dem
  `NavigationStack` + `.frame(maxWidth/maxHeight: .infinity)` auf dem Representable (Muster
  aus `ShuffleScopeSheet`).

### ✅ Team-ID für Code-Signing instabil über Mac-Wechsel (2026-09-02)
- **Symptom:** „No Account for Team"/„No signing certificate found" nach Mac-Wechsel.
- **Ursache:** Die Personal-Team-ID ist NICHT stabil über Neuinstallationen — war `F95969PBFU`
  auf dem alten Mac, danach fälschlich `YP6683AT3R` vermutet (ID aus wiederhergestellten
  Provisioning-Profilen anderer Apps, nicht die ID des auf DIESEM Mac erzeugten Zertifikats).
- **Lösung:** Einmal ⌘R in der Xcode-GUI (erzeugt das Zertifikat), danach `security
  find-identity -v -p codesigning` und die dort angezeigte Team-ID in `DEVELOPMENT_TEAM`
  eintragen (am Ende korrekt: `Y83997R5WL`). Zusätzlich war `com.goldfish.ios` noch beim alten
  Team reserviert (App-IDs sind teamgebunden) → für Geräte-Tests auf `com.goldfish.iosdev`
  geändert. `xcodebuild` von der CLI kann bei einem Free-Personal-Team grundsätzlich NICHT
  signieren/auf einem echten Gerät laufen (auch nicht mit `-allowProvisioningUpdates`) — Runs
  auf echten Geräten IMMER über die Xcode-GUI (⌘R).

### ✅ SSO-Kontowechsel unmöglich (Build 171)
- **Symptom:** Erneutes „Mit SSO anmelden" meldete stillschweigend denselben Authentik-User
  wieder an — kein Wechsel Admin ↔ normaler Benutzer möglich.
- **Ursache:** Der eingebettete Authentik-`WKWebView` hat einen persistenten
  `WKWebsiteDataStore`.
- **Lösung:** Neuer Button „Mit anderem Konto anmelden" setzt `OIDCLoginView
  (clearSessionFirst: true)` → `WKWebsiteDataStore.default()` wird vor dem Laden geleert.

### ✅ Gesehen-Status propagierte nicht ans Downloads-Grid
- **Symptom:** `setWatched` lief, aber Downloads-Tab-Kacheln zeigten weiter „ungesehen".
- **Ursache:** Downloads-Kacheln rendern aus `DownloadRecord.cachedItem`, einem beim Download
  eingefrorenen JSON-Snapshot, der nie nachgezogen wurde.
- **Lösung:** `Item.withWatched(_:)` + `DownloadManager.updateCachedWatched(itemId:watched:)`,
  verdrahtet an jedem `setWatched`-Call-Site (PlayerView, ItemCard, ItemDetailView).

## Lokale Bibliotheken (GoldfishApple) — externe Datenträger, Player/Formatanpassung/Puffer (seit 2026-08-24)

Große Session rund um USB-Platten/SD-Karten als lokale Bibliotheken. Aus CLAUDE.md
ausgelagerte Detailfassung — dort steht nur noch die Kurzfassung.

### ✅ Mauszeiger blieb dauerhaft versteckt
- **Ursache:** `NSCursor.hide()/unhide()` ist app-weit refcounted, nicht fensterbezogen — bei
  `onDisappear`-Ausfall (unzuverlässig beim Schließen über den nativen roten Knopf) blieb der
  Cursor versteckt.
- **Lösung:** `setHiddenUntilMouseMoves(true)` statt manuellem Pairing.

### ✅ I/O-Contention zwischen Formatanpassung und Wiedergabe auf langsamen externen Platten
- **Ursache:** Die Konvertierungs-Queue (und ab Build 0152 auch der Thumbnail-/Auflösungs-
  Hintergrund-Loop) lief parallel zur aktiven Wiedergabe und konkurrierte um I/O auf demselben
  Datenträger.
- **Lösung:** `LocalTranscodeService.beginPlayback()`/`endPlayback()`/
  `waitWhilePlaybackActive()` pausieren die Queue während aktiver Wiedergabe. Safety-Net:
  `LocalPlayerView`s `NSWindow.willCloseNotification`-Observer ruft
  `LocalTranscodeService.resetPlaybackActive()` (Hard-Reset) für den Fall eines verpassten
  `onDisappear`. Build 0154 fand zwei WEITERE I/O-Contention-Quellen: verwaiste ffmpeg-Prozesse
  überlebten einen App-Neustart nicht mehr (`terminateAllActiveProcesses()` in
  `applicationWillTerminate`), und mehrere lokale Bibliotheken feuerten je einen eigenen
  Thumbnail-Task ab statt einer geteilten Warteschlange (`LocalLibraryManager.thumbnailQueue`).
  **Diagnose-Reflex bei künftigen Ruckel-Reports:** `ps aux | grep ffmpeg` (Zombie-Check) +
  `lsof +D <externes-Volume>` während Wiedergabe (Contention-Quelle suchen).

### ✅ Cache-Cap schützte nicht vor „No space left on device"
- **Ursache:** `maxCacheBytes` (80 GB nominell) allein prüft nicht, ob die Platte aus anderen
  Gründen voll ist.
- **Lösung:** `enforceCacheSizeCap()` hält zusätzlich `minFreeBytes` (15 GB) über
  `.volumeAvailableCapacityKey` frei (bewusst NICHT `...ForImportantUsage` — zählt verwerfbare
  Time-Machine-Snapshots optimistisch mit). `performRemux` prüft das jetzt VORAB.

### ✅ Cache-Eviction warf teure Re-Encodes für neuere schnelle Remuxe raus
- **Ursache:** Reine Alt-zuerst-Eviction unterschied nicht zwischen teuren Re-Encodes und
  billigen Remuxen.
- **Lösung:** `performRemux` hält die Slow/Fast-Klassifizierung persistent fest
  (`.slow-classification.json`, überlebt Neustarts). `enforceCacheSizeCap()` opfert erst alle
  schnellen Einträge (älteste zuerst), erst danach langsame.

### ✅ Formatanpassungs-Priorität ignorierte parallele Scans
- **Ursache:** Reines Anhängen an die Konvertierungs-Queue reichte nicht — bei mehreren
  parallel scannenden Bibliotheken kamen langsame Items zu spät dran (Build 0145).
- **Lösung:** Langsame Items werden per `insert(at: 0)` global vor alle schnellen gestellt.
  `rescanAllLibraries()` scannt Bibliotheken parallel (`withTaskGroup`), damit eine
  hängende/nicht angeschlossene Bibliothek nicht alle anderen blockiert.

Aktueller Stand (bleibt in CLAUDE.md): Resume-Dialog, Puffer-Regler (`bufferSecondsKey`,
5–180s), lokale Auflösungs-Erkennung (`LocalItem.width/height`), Downloads-Resume
(`resumeData`), Download-Metadaten-Nachkorrektur, Löschen im lokalen Player + „Alle Downloads
löschen".

### ✅ „Kill Bill spielt nicht ab" — Saga über mehrere Fixversuche (2026-08-27 bis 2026-08-30)
- **Symptom:** Kill-Bill-Rip (Blu-ray) ließ sich über den `?compat=1`-Download in der
  Mac/iOS-App wiederholt nicht abspielen — schwarzes Bild, teils stummer Ton, teils
  „Datei kann nicht abgespielt werden".
- **Ursache 1 (Audio):** E-AC-3 5.1 (`ec-3`-Tag in MP4) spielt AVFoundation nicht ab →
  schwarz/stumm. Danach AAC 5.1 OHNE `-ac 2` versucht — erzeugte bei DTS-Quellen
  `channel_layout=unknown`, AVFoundation blieb STUMM.
- **Ursache 2 (Muxing):** ffmpegs `elst`-Edit-List (B-Frame-Delay) brachte AVFoundation bei
  kopiertem h264 dazu, die Datei GAR NICHT abzuspielen (VLC spielte sie klaglos ab).
- **Ursache 3 (Cache-Staleness):** `convVersion` wurde bei `buildArgs`-Änderungen nicht immer
  hochgezählt — eine mit alter, kaputter Logik erzeugte Cache-Kopie wurde ewig weiter
  ausgeliefert, weil der Cache-Validator nur Quelle-mtime+size vergleicht.
- **Ursache 4 (die eigentliche, am 2026-08-30 gefundene):** Der compat-Download kam längst als
  sauberes MP4 an (avc1 h264 8-Bit + 2× AAC 5.1, per ffprobe verifiziert) — die **Mac/iOS-App
  speicherte die Datei aber als `.mkv`** (Dateiname aus `item.container` statt aus der
  Server-Antwort abgeleitet), AVFoundation verweigerte allein wegen der Dateiendung.
- **Lösung (kumulativ):** AAC-LC Stereo (`-ac 2 -b:a 256k`) für JEDE Tonspur, auch AAC-Quellen
  (`convVersion = 4`); `-movflags +negative_cts_offsets` immer (negative CTS statt edit-list);
  `convVersion`-Disziplin bei jeder `buildArgs`-Änderung; GoldfishApple Build 178 erzwingt
  `.mp4`-Endung bei `?compat=1`-Downloads unabhängig von `item.container`.
- **Lehre:** Bei App-seitigen Wiedergabeproblemen zuerst verifizieren, WAS tatsächlich beim
  Client ankommt (ffprobe auf die heruntergeladene Datei), bevor am Server weiter gedreht wird
  — die Server-Fixes waren einzeln alle berechtigt, aber keiner war der Auslöser für DIESEN
  Fall.

### ✅ Compat-Download blieb bei 99 % hängen (2026-08-27)
- **Symptom:** Große Dateien über `?compat=1` in der Apple-App klebten beim Download bei 99 %.
- **Ursache:** Die ffmpeg-Konvertierung lief synchron am Request-Context (`r.Context()`) und
  servte am Ende mit der ModTime der Cache-Datei als einzigem Validator. Die App lief bei
  großen Dateien in ihren 60-s-Read-Timeout, startete per `resumeData` neu → neuer Request
  killte das erste ffmpeg und startete ein frisches (in dieselbe `.tmp.mp4`); beim Resume
  matchte `If-Range` nicht mehr (Cache-ModTime hatte sich geändert) → `ServeContent` lieferte
  200 statt 206.
- **Lösung:** `internal/download` bekam eine `prepRegistry` (detachable single-flight pro
  `outPath`) — paralleles Warmen läuft mit `context.Background()` (+2h-Cap) weiter, auch wenn
  der Client abbricht; nur das *Warten* im Handler respektiert `r.Context()`. Unique
  `.tmp.<ns>.mp4` + disk-space-Guard vor ffmpeg. `ETag`/`Last-Modified` an die QUELLDATEI
  gekoppelt (nicht die Cache-Kopie) → `If-Range` bleibt über Resume-Versuche stabil. Apple-App:
  `timeoutIntervalForRequest = 600`.

### ✅ Sammlungen liefen ohne ACL- und FSK-Prüfung (2026-09-02)
- **Symptom:** Non-Admin „reviewer" sah Sammlungen (inkl. Datei-Pfaden) aus Bibliotheken, auf
  die er per ACL keinen Zugriff hatte.
- **Ursache:** `ListCollections`/`GetCollectionParts`/`ListItemsInCollection` liefen komplett
  ohne ACL-Prüfung. Beim Nachprüfen am selben Tag zweiter Fund: die (dann korrigierte)
  Library-ACL prüfte zwar, aber die FSK-Altersgrenze (`ItemFilter.MaxAgeRating`) gar nicht — ein
  eingeschränkter Account hätte einen FSK-18-Film über den Sammlungs-Umweg trotzdem gesehen.
- **Lösung:** Alle drei Queries filtern jetzt per `store.aclLibraryClause(col, userID,
  isAdmin)` (Admin immer alles, Non-Admin nur `user_library_access`) — bei
  `GetCollectionParts` sitzt die Klausel bewusst im `LEFT JOIN`, ein unzugänglicher Part soll
  wie ein fehlender Part aussehen (`owned:false`), nicht verraten dass der Film vorhanden ist.
  Neuer gemeinsamer Helper `Store.itemVisibilityClause` kombiniert Library-ACL UND FSK-Grenze.
  Tests: `internal/store/collections_acl_test.go`. Siehe `feedback_user_isolation_before_deploy`
  (Memory) — wiederkehrendes Muster: neues Feature mit User-sichtbaren Daten ohne ACL-Prüfung.

---

## Bugfix-Chroniken (ausgelagert aus CLAUDE.md, 2026-09-13)

Root-Cause-Erzählungen zu 47 Fixen, die bis zum 2026-09-13 im Volltext in
CLAUDE.md standen. Die daraus abgeleiteten Regeln und Fallstricke stehen
weiterhin dort (gekürzt, im jeweiligen Feature-Abschnitt) — hier liegt nur
die ausführliche Herleitung: wie der Bug sich äußerte, was die erste
Vermutung war und warum sie nicht stimmte. Gezielt lesen, wenn ein Fix
erneut aufbricht oder jemand fragt „warum ist das so gebaut".

### Scan-Ausschlüsse — NUR Auto-Scan (seit 2026-09-09, LIVE 1.2.47, korrigiert 1.2.48) — Zeilen 435–448 der alten CLAUDE.md

- **🔴 Erste Version (1.2.47) wirkte auf JEDEN Scan (Auto-Scan UND manuell)
  — von der ursprünglichen Design-Annahme her bewusst so gebaut, aber vom
  User explizit korrigiert (2026-09-09):** „bei einem manuellen Scan aber
  mit dabei sind, egal wo sie gemountet oder gespeichert sind". Hintergrund:
  ein manueller ⟳-Scan wird bewusst vom Admin ausgelöst, der zu diesem
  Zeitpunkt selbst weiß, ob die Platte angeschlossen ist — die Schutzlogik
  ist nur für den UNBEAUFSICHTIGTEN Auto-Scan nötig/gewollt. **Fix (LIVE
  1.2.48):** `Scanner.Start`/`run` bekamen einen neuen Parameter
  `respectScanExcludes bool`. `RunAutoScan` (`internal/api/autoscan.go`)
  übergibt `true`; `startScan`/`startScanAll` (`internal/api/scan.go`,
  manueller ⟳-Button UND „Alle Bibliotheken scannen") übergeben `false` —
  bei `false` bleibt `excludedFolders` im Scanner leer, wodurch der
  Walk-Skip UND der Orphan-Schutz weiter unten automatisch zu No-Ops werden
  (keine eigene Verzweigung nötig).

### Benutzer & Zugriff — Zeilen 579–593 der alten CLAUDE.md

- **🔴 FSK-Altersfreigabe griff bei KEINER eingeloggten Session (Bug,
  gefixt 2026-09-02):** `Store.GetSession` — die Query, die `currentUser(r)`
  bei JEDEM authentifizierten Request befüllt — hat `max_age_rating` schlicht
  NICHT mitgeladen. Jeder `me.MaxAgeRating`-Check (`requireAgeAllowed`,
  `ListItems`-Filter, Collections-ACL) sah dadurch IMMER `nil`, unabhängig
  vom tatsächlichen DB-Wert — eine für einen Kinder-Account gesetzte FSK-16-
  Grenze hatte de facto NIE eine Wirkung. Nur der einmalige Login-Query
  (`GetUserByName`) hatte das Feld korrekt gesetzt, wurde aber danach nie
  wieder gelesen (Session-Cookie trägt nur den Token, nicht den User selbst).
  Fix: `GetSession` lädt jetzt `max_age_rating` (und `can_download`, s.u.)
  mit. Test: `internal/store/users_test.go
  TestGetSessionCarriesMaxAgeRatingAndCanDownload`. **Bei jeder künftigen
  Änderung an der `users`-Tabelle/`model.User`: prüfen, ob `GetSession`
  (und nicht nur `GetUserByName`/`GetUser`) das neue Feld auch mitlädt** —
  das ist der eigentliche Angriffspunkt für `currentUser(r)`.

### Benutzer & Zugriff — Zeilen 604–614 der alten CLAUDE.md

- **🔴 „Manuell zuordnen" (🔍) + „Zuordnung bestätigen" (✅) waren KEINE
  Admin-Funktionen (Bug, gefixt 2026-09-02):** `player.js` zeigte beide
  Buttons im Detail-Dialog für JEDEN eingeloggten User (nur nach Library-Kind
  gefiltert, nicht nach `state.me.isAdmin`). Serverseitig war
  `POST /items/{id}/metadata` bereits korrekt `requireAdmin`-geschützt (ein
  Klick eines Non-Admins wäre also nur mit 403 gescheitert), aber
  `PUT /items/{id}/confirm` (Bestätigen) hatte GAR KEINEN Admin-Schutz — jeder
  eingeloggte User konnte `metadata_confirmed` direkt per API togglen. Fix:
  Route jetzt `requireAdmin(s.confirmItemMetadata)`; beide Buttons in
  `player.js` prüfen jetzt zusätzlich `state.me.isAdmin`.


### Trickplay (Hover-Vorschau) — Zeilen 682–771 der alten CLAUDE.md

- **🔴 ACL-Leak: globaler Trickplay-Statustoast zeigte Dateinamen fremder
  Bibliotheken an JEDEN eingeloggten User (Bug, gefixt 2026-09-07, User-
  Report mit Screenshot: Familienaccount "Börnie" sah im Statusbar-Toast
  einen Titel aus der gesperrten Bibliothek "a"):** `GET /api/trickplay/status`
  war absichtlich NICHT `requireAdmin` (Kommentar „Aktivierung admin-only,
  Konsum für alle", Design-Entscheidung aus der Trickplay-Erstversion) —
  lieferte den kompletten Worker-Status inkl. `currentTitle`/`currentItemId`
  (Titel des GERADE bibliotheksübergreifend verarbeiteten Items) an jeden
  authentifizierten Request, ohne Admin- oder Library-ACL-Prüfung. Frontend
  (`app.js boot()`) pollte diesen Endpoint für JEDEN eingeloggten User
  automatisch alle 30s + bei laufendem Job alle 2s. **Gleiches Muster an
  zwei weiteren Stellen gefunden und im selben Zug gefixt:** `GET
  /api/scan/status` (`model.ScanStatus.Current` = aktueller Datei-Pfad,
  bibliotheksübergreifend) und `GET /api/enrich/refresh-all-status`
  (`RefreshAllStatus.Current` = Titel des gerade TMDB-aktualisierten Items)
  — beide ebenfalls „Trigger admin-only, Status für alle", beide ebenfalls
  ohne ACL-Bezug zum abfragenden User. **Zusätzlich war `currentTitle` ein
  zweites Mal komplett UNAUTHENTIFIZIERT über `GET /api/health` sichtbar**
  (Kommentar „Hilfreich für Außen-Checks … ohne Auth") — jeder im Internet
  hätte den Dateinamen des gerade verarbeiteten Items einer beliebigen,
  auch privaten/gesperrten Bibliothek sehen können, ganz ohne Login. Fix:
  alle drei Status-Endpoints (`/trickplay/status`, `/scan/status`,
  `/enrich/refresh-all-status`) jetzt `requireAdmin`; `currentTitle`/
  `currentItemId` komplett aus der `/api/health`-Antwort entfernt (nur noch
  aggregierte Zahlen, kein Item-Bezug); Frontend pollt alle drei Endpoints
  in `boot()` nur noch innerhalb eines `if (state.me.isAdmin)`-Blocks.
  Siehe [[feedback_user_isolation_before_deploy]] — wiederkehrendes Muster:
  Hintergrund-Worker-Status wird als „harmlose Diagnose-Info" behandelt und
  dabei die ACL-Prüfung vergessen, obwohl er Dateinamen preisgibt.
  **Zusätzlich im selben Zug (User-Vorgabe "Benutzer dürfen gar keine
  Toast sehen, und den Button Scan brauchen die eigentlich auch nicht"):**
  der `⟳ Scan`-Button + Dropdown (`.scan-group` in `index.html`) war für
  JEDEN eingeloggten User sichtbar, obwohl `POST /scan/*` schon immer
  `requireAdmin` war — ein Klick eines Non-Admins endete also nur in einem
  403, brachte aber nie einen Mehrwert. `renderUserMenu()` (`admin.js`)
  blendet `.scan-group` jetzt wie die übrigen Admin-Elemente per
  `state.me.isAdmin` aus. **Mac/iOS/tvOS-App und Android-App geprüft**
  (User-Vorgabe "kontrollieren, dass in den 3 bzw 4 Clients sowas nicht
  sichtbar ist") — keiner der beiden nativen Clients ruft
  `/trickplay/status`, `/scan/status` oder `/enrich/refresh-all-status`
  überhaupt auf (beide haben laut eigener CLAUDE.md-Doku „kein Admin" —
  keine Nutzerverwaltung, kein Library-Manager, kein Scan, keine Whisper-UI),
  betroffen war ausschließlich der Browser-Client.
  **Nachtrag (LIVE 1.2.16):** die Sort-Dropdown-Wartungsfilter „Duplikate",
  „🔀 Mehrere Versionen", „≈ Ähnliche Dateinamen", „Ohne TMDB-Zuordnung",
  „Alle Unbestätigten", „⚠ Verdächtige Zuordnungen", „🪤 Nur Interlaced"
  (`data-admin-only="1"` in `index.html`, Sichtbarkeits-Check in `grid.js`
  neben dem bestehenden `data-kinds`-Filter) sind ebenfalls admin-only —
  reine Aufräum-/Zuordnungs-Werkzeuge, kein Browsing-Feature für normale
  User. **„♡ Nur Favoriten" bleibt bewusst sichtbar** (User-Rückfrage
  explizit bestätigt) — liegt zwar mitten in diesem Options-Block, ist
  aber ein normales Nutzer-Feature. Kein Backend-ACL-Fix nötig (diese
  Ansichten sind ohnehin auf `state.currentLibrary` gescoped, für die der
  User schon Zugriff haben muss) — rein UI-Decluttering.
  **🔴→✅ Nachtrag (LIVE 1.2.51, 2026-09-09, User-Auftrag "nochmal genau
  prüfen, dass Benutzer strikt getrennt sind"):** exakt dasselbe Muster war
  bei ZWEI weiteren Worker-Status-Endpoints übersehen worden, die beim
  ursprünglichen Fix (1.2.x oben) nicht mit durchgegangen waren —
  `GET /api/whisper/status` (liefert `currentTitle`/`currentItemId` des
  gerade per Whisper transkribierten Items) und `GET /api/introskip/status`
  (liefert `currentLibraryId`/`currentFolder` des gerade analysierten
  Serien-Ordners) waren beide OHNE `requireAdmin` erreichbar — jeder
  eingeloggte Non-Admin bekam dadurch über die globale
  `#whisperStatus`-Statusleiste UND über Toast/Glocke
  (`checkWhisperJobCompletions` in `whisper.js`) den Titel eines Items
  angezeigt, das gerade von einem ADMIN per Whisper transkribiert wurde —
  unabhängig von der eigenen Library-ACL. Whisper-Generierung selbst war
  schon immer `requireAdmin` (`POST /items/{id}/generate-subtitle`), nur
  der KONSUM-Status nicht — exakt das „Aktivierung admin-only, Konsum für
  alle"-Muster von oben. Fix: beide Endpoints (+ `/api/whisper/download-status`,
  gleiche Klasse, nur aus der admin-only Whisper-Settings-Dialog heraus
  aufgerufen) jetzt `requireAdmin`; `startWhisperGlobalPoll()` in `app.js`
  läuft nur noch innerhalb desselben `if (state.me.isAdmin)`-Blocks wie
  `checkScanActive`/`checkTrickplayWorker` (vorher unconditional für jeden
  eingeloggten User gestartet). `introSkipWorkerStatus` hatte noch gar
  keinen Frontend-Consumer (totes, aber erreichbares Leck) — trotzdem
  gefixt. Gefunden durch systematisches Durchgehen ALLER
  `setInterval`/Polling-Stellen in `internal/webassets/web/*.js` gegen die
  zugehörigen Server-Handler (Muster: grep nach `currentTitle`/
  `currentItemId`/`currentFolder`-Feldern in Go-Structs, dann prüfen ob der
  Endpoint `requireAdmin` trägt UND ob der Frontend-Call innerhalb eines
  `isAdmin`-Gates liegt — beide Seiten separat prüfen, ein admin-gated
  Endpoint mit ungated Frontend-Poll ist nur eine Fehlermeldung in der
  Konsole wert, aber ein ungated Endpoint mit „nur im Admin-UI sichtbar"
  ist der eigentliche Leak, weil jeder die URL direkt aufrufen kann).
  Item-/Library-scoped Endpoints (`/transcode/{id}/progress`,
  `/download/{id}/compat-status`, `/items/{id}/subtitle-jobs`,
  `/items/move/status`) im selben Zug gegengeprüft — alle bereits korrekt
  per `requireLibAccess`/`requireAdmin` abgesichert, kein weiterer Fund.
  Siehe [[feedback_user_isolation_before_deploy]].

### Intro-Erkennung ("Skip Intro", seit 2026-08-11, Algorithmus v2 seit 2026-08-13) — Zeilen 927–947 der alten CLAUDE.md

- **🔴 Aktivierte Serien starteten teils NIE (Bug, gefixt 2026-09-06,
  User-Report "Intro-Erkennung startet nicht"):** `setIntroSkipFolder`
  (`internal/api/introskip.go`) rief `Store.UpsertIntroSkipJob` bis dahin
  fälschlich nur INNERHALB von `body.Enabled && body.Season != nil` auf.
  Ein reiner Checkbox-Toggle OHNE `season`-Feld — genau das, was jeder
  einzelne Zeilen-Klick im Dialog UND „☑ Alle auswählen"
  (`setAllIntroSkipFolders` in introskip.js) senden — aktivierte den Ordner
  zwar (Zeile in `intro_skip_folders` existiert), legte aber NIE einen
  `intro_skip_jobs`-Eintrag an: der Worker hatte für diesen Ordner schlicht
  nichts zu tun, "startet nie", ohne jede Fehlermeldung. Live-Diagnose per
  claude-in-chrome direkt gegen den echten Server fand 6 von 218 aktivierten
  Serien einer Bibliothek ohne jeden `jobStatus`. Fix: `UpsertIntroSkipJob`
  läuft jetzt bei JEDEM `Enabled=true`, unabhängig vom `season`-Feld — nur
  das season-spezifische `SetIntroSkipFolderSeason` bleibt an
  `Season != nil` gekoppelt (das war der korrekte Teil des ursprünglichen
  Season-Zeiger-Fixes vom 2026-08-13, verhinderte ein versehentliches
  Zurücksetzen einer Staffel-Beschränkung — siehe Season-Abschnitt oben,
  unverändert). Einmaliger Backfill `backfillIntroSkipMissingJobs`
  (`cmd/goldfish/main.go`, Settings-Gate `intro_skip_missing_jobs_backfill_v1`)
  holt für alle bereits aktivierten, aber job-losen Ordner den Job
  nachträglich nach.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1110–1121 der alten CLAUDE.md

- **🔴→✅ Selbstbetitelte Alben blieben dauerhaft ohne Genre/Jahr (gefixt
  2026-09-08, LIVE 1.2.29, User-Frage "läuft die Erkennung noch?"):**
  `Store.PendingMusicMetadataAlbums` trug denselben `artist != album`-
  Ausschluss wie die Cover-Suche (`PendingMusicAlbums`) — dort sinnvoll
  (dort bedeutet `artist == album` "kein echtes Album-Tag, Ordnername als
  Notlösung"), für Genre/Jahr aber falsch: selbstbetitelte Alben ("Aerosmith"
  von Aerosmith, "Bon Jovi" von Bon Jovi, "Audioslave" von Audioslave, …) sind
  ein normaler, häufiger Fall und wurden dadurch dauerhaft (kein Log, kein
  Retry) von der MusicBrainz-Suche ausgeschlossen. Live-Diagnose (DB-Kopie +
  Go/modernc.org-sqlite lokal ausgewertet, siehe `feedback_sqlite_debug_technique`)
  fand 69 betroffene Alben, `metadata_fetched_at` seit 2026-09-06 unverändert.
  Ausschluss bleibt bewusst NUR in `PendingMusicAlbums` (Cover-Suche).

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1179–1187 der alten CLAUDE.md

- **🔴 Suchtreffer spielten den vorherigen Track (Bug, gefixt 2026-09-04):**
  `state.playQueue` wurde nur von `renderAlbumTracks`/`renderAllTracksList`
  gesetzt — der generische Rendering-Pfad in `grid.js` (normale Ordner-
  Navigation UND Suche) ließ es unangetastet. Ein Klick auf einen Suchtreffer
  fiel in `cards.js`s Fallback (`queue = state.playQueue.length ? … : [it]`)
  dadurch auf die ALTE Queue vom zuletzt geöffneten Album zurück, `indexOf(it)`
  fand den Suchtreffer darin nicht (→ `-1` → Index 0) — es spielte der erste
  Track der alten Queue statt des angeklickten Titels. Fix: `grid.js` setzt
  `state.playQueue = searching ? items : merged` bei jedem generischen Render.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1188–1200 der alten CLAUDE.md

- **🔴 Mini-Player spielte oft gar nicht / stark verzögert (Bug, gefixt
  2026-09-04):** zwei Ursachen in `music.js`. (1) Ein Doppelklick auf eine
  Kachel feuert zwei "click"-Events → zwei überlappende
  `musicPlayCurrent()`-Aufrufe, deren `await api(/api/playback/…)`-Antworten
  in beliebiger Reihenfolge zurückkamen und sich gegenseitig mit
  `vjs.src()`/`vjs.play()` überschrieben (Video.js bricht den laufenden
  Ladevorgang dabei mit einem lautlos verschluckten `AbortError` ab). Fix:
  `musicState.playSeq`-Sequenz-Token, nur der jeweils NEUESTE Aufruf darf noch
  `src()`/`play()` ausführen (gleiches Muster wie `state.loadSeq` in
  `grid.js`). (2) `vjs.src()`/`vjs.play()` direkt nach `musicEnsureVjs()`
  auf einer FRISCH erzeugten Video.js-Instanz lief teils ins Leere, weil die
  Tech (Html5) noch nicht initialisiert war — jetzt in `vjs.ready(() => {…})`
  gewrappt (feuert sofort, wenn der Player schon bereit ist, sonst verzögert).

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1221–1248 der alten CLAUDE.md

- **🔴 Wurzel des "lange Verzögerung + Wiedergabefehler"-Bugs (behoben
  2026-09-04):** MP3/FLAC/M4A-Dateien mit eingebettetem Cover (ID3-APIC o.ä.)
  liefern in ffprobe einen ZUSÄTZLICHEN "video"-Stream für das Bild
  (`disposition.attached_pic=1`, meist Codec mjpeg/png, 1 Frame). Der Scanner
  setzte diesen fälschlich als `items.video_codec` — `playback.Decide()` hielt
  die Datei dadurch für ein VIDEO statt reines Audio und erzwang einen
  unnötigen, für ein Einzelbild sinnlosen HLS-Transcode: lange Startverzögerung
  + kaputte Wiedergabe ("Failed to set MediaSource duration" in der Konsole).
  Live im Browser reproduziert (DevTools Network zeigte `/api/transcode/…`
  statt `/api/stream/…` für eine ganz normale MP3). Fix: `Scanner.probeItem`
  überspringt Streams mit `attached_pic=1` jetzt beim Setzen von
  VideoCodec/Width/Height. Einmaliger Backfill (`music_cover_art_videocodec_fix_v1`)
  räumt bereits falsch gescannte Musik-Items per Codec-Namens-Heuristik auf
  (kein erneuter ffprobe-Call nötig — mjpeg/png/bmp/gif/tiff/ppm/webp kommen
  in Musik-Bibliotheken nie als echtes Video vor).
- **🔴 Zweiter, tatsächlich ausschlaggebender Teil desselben Bugs:** selbst
  nach dem Cover-Art-Fix blieb die Wiedergabe hängen (`readyState=0` für
  immer, per `javascript_tool`/DevTools direkt am `<video>`-Element
  verifiziert, obwohl der zugrunde liegende `fetch()` auf `/api/stream/{id}`
  in ~80ms fertig war). Ursache: `mimeForExt()` (`internal/api/stream.go`,
  `streamDirect`-Handler) kannte NUR Video-Extensions (mp4/mov/mkv/webm/avi/
  wmv) — jede Musikdatei bekam den Fallback `application/octet-stream` als
  `Content-Type`. Browser lehnen es ab, ein `<video>`/`<audio>`-Element mit
  diesem MIME-Type zu decodieren (kein MIME-Sniffing für Medienelemente),
  das Element bleibt für immer bei `readyState=0` — kein Fehler-Event, kein
  Timeout, einfach dauerhaft "lädt". Fix: `mimeForExt` um
  mp3→audio/mpeg, m4a/m4b→audio/mp4, aac→audio/aac, ogg/opus→audio/ogg,
  wav→audio/wav, flac→audio/flac ergänzt.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1284–1303 der alten CLAUDE.md

- **🔴 Weitere UI-Bugs gefixt (2026-09-04, LIVE 1.0.64):**
  `Store.ListMusicAlbumTracks` lud `favorite`/`last_played_at` nie (kein
  `user_item_state`-JOIN) — ein in der Album-Ansicht favorisierter Track
  sprang beim nächsten Album-Fetch (z.B. Sortierungswechsel) wieder auf
  "nicht favorisiert" zurück, obwohl die DB korrekt war. "Nur Favoriten"
  zeigte in der normalen Album-Übersicht immer die flache Track-Liste
  (Item-Favorit) statt favorisierter ALBEN (`user_music_album_favorites`) —
  Album-Favoriten waren über den Filter nie auffindbar; jetzt zeigt „Nur
  Favoriten" im Album-Root gefilterte Album-Kacheln, nur bei explizit
  aktiviertem „Alle Titel" weiterhin gefilterte Tracks.
  `.track-row-fav.fav-toggle` erbte ungewollt `position:absolute` von der
  generischen Kachel-Overlay-Klasse `.fav-toggle` (Herz "hing" losgelöst im
  nächsten positionierten Vorfahren) — jetzt explizit auf normalen
  Inline-Fluss zurückgesetzt. Fehlendes `min-width:0` auf
  `.track-row-title`/`-artist`/`-album`/`-played` ließ lange Titel die
  1fr-Spalte über die Zeilenbreite hinaus sprengen (Grid-Kinder haben
  implizit `min-width:auto` = Inhaltsbreite bei `white-space:nowrap`).
  Bulk-Auswahl (☑) hatte in der Listenansicht kein sichtbares
  Checkbox-Element (`.track-row-select`, analog `.card-select`, gemeinsamer
  `data-item-id`-Selektor in `toggleSelection`/`selectAllVisible`).

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1304–1313 der alten CLAUDE.md

- **🔴 Genre war für ALLE Alben leer (gefixt 2026-09-05, LIVE 1.0.67):**
  `music_albums.genre` existierte im Schema, aber der Scanner las das
  Genre-Tag nie aus (nur artist/album/track/title). Fix: neue Spalte
  `items.genre` (Zwischenlager), Scanner liest sie jetzt mit,
  `GroupMusicAlbums` aggregiert `MAX(genre)` pro (artist,album)-Gruppe und
  schreibt sie auch NACHTRÄGLICH nach (vorher nur `ON CONFLICT DO NOTHING`
  beim ersten Anlegen). Album-Header zeigt das Genre jetzt neben Künstler/
  Jahr. **Bereits gescannte Dateien brauchen einen vollständigen Rescan**
  (force=true) der Musik-Bibliothek, damit ffprobe das Tag nachliefert —
  ein inkrementeller Scan probet unveränderte Dateien nicht erneut.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1314–1382 der alten CLAUDE.md

- **🔴 Musical-/Soundtrack-Alben zerfielen in eine Kachel PRO TRACK
  (User-Report 2026-09-05, zwei Fix-Runden):** "Das Phantom der Oper"
  zeigte für jeden Titel eine eigene Album-Kachel mit eigenem Cover statt
  EINEM Album mit allen Liedern.
  - **Runde 1 (LIVE 1.0.76, reichte NICHT):** Vermutung war ein fehlendes
    `album_artist`-Tag — Scanner-Priorität von `lookupTag(tags, "artist",
    "album_artist")` auf `lookupTag(tags, "album_artist", "artist")`
    umgedreht (+ unabhängig gefundener Determinismus-Bug in `lookupTag`
    selbst behoben, iterierte vorher `tags` in Go's randomisierter
    Map-Reihenfolge statt die `keys`-Priorität zu respektieren; Test:
    `internal/scanner/scanner_test.go`). **User meldete danach "hat nicht
    geklappt".**
  - **Live-Diagnose (per claude-in-chrome direkt gegen die echten Tracks):**
    Root Cause war etwas anderes als angenommen — es gibt in diesen
    Dateien GAR KEIN "album_artist"-Tag, das "artist"-Tag enthält
    stattdessen pro Track eine ANDERE Kombination/Reihenfolge aller
    beteiligten Sänger (z. B. Track 5: "Peter Hofmann, Andrew Lloyd
    Webber, Anna Maria Kaufmann, …", Track 7: "Thomas Schulze"). Bei
    Compilations/Musicals/Klassik ist das "artist"-Tag pro Track
    strukturell unzuverlässig für die Gruppierung — Runde 1 fiel deshalb
    einfach auf denselben unzuverlässigen Wert zurück.
  - **Runde 2 (LIVE 1.0.77, tatsächlicher Fix):** `Store.GroupMusicAlbums`
    (`internal/store/music.go`) gruppiert seither PRIMÄR über den
    **physischen Elternordner** der Datei (`musicGroupKey`), nicht mehr
    über das rohe `(artist,album)`-Tag-Paar — ein Ordner ist so gut wie
    immer EIN Album, unabhängig davon wie inkonsistent die Tags sind.
    `canonicalAlbumFields` bestimmt daraus GENAU EINEN Artist-/Album-/
    Genre-Wert für die ganze Ordner-Gruppe: uneinheitlicher Artist
    innerhalb der Gruppe → **"Verschiedene Interpreten"** statt eines
    zufällig "gewinnenden" Einzelnamens; fehlt jeder Album-Tag in der
    Gruppe → letzter Ordnername als Titel-Fallback (macht das in CLAUDE.md
    schon lange behauptete, aber nie tatsächlich implementierte
    "Ordnername als Fallback" jetzt real wahr). Dateien direkt im
    Bibliotheks-Root ohne Unterordner (kein gemeinsamer Ordner zum Bündeln)
    behalten bewusst das alte reine `(artist,album)`-Tag-Verhalten —
    verhindert, dass völlig unabhängige lose Singles im Root
    zusammengeworfen werden. Tests:
    `internal/store/music_grouping_test.go` (4 Szenarien: inkonsistenter
    Artist im Ordner, fehlendes Album-Tag, Root-Level-Fallback,
    konsistenter Artist bleibt unverändert).
  - **Kein Rescan nötig für Runde 2** (anders als Runde 1) — die Gruppierung
    arbeitet rein auf bereits in der DB gespeicherten `items.artist/album/
    genre`-Werten, kein erneutes ffprobe-Tag-Lesen nötig. Ein normaler
    (auch inkrementeller) Scan der Musik-Bibliothek reicht, um
    `GroupMusicAlbums` mit dem neuen Algorithmus erneut laufen zu lassen
    (`Scanner.run` ruft es am Ende JEDES Musik-Scans auf, unabhängig von
    `force`).
  - **Lektion:** bei einem gemeldeten Fix, der laut User "nicht geklappt"
    hat, IMMER zuerst mit Live-Daten (claude-in-chrome + direkter
    `fetch()`-Aufruf gegen die eigene API im Browser-Kontext) verifizieren,
    welchen Tag-Wert die Datei tatsächlich hat, statt eine zweite
    Vermutung auf der ersten aufzubauen — die ursprüngliche Diagnose
    ("fehlendes album_artist-Tag") war plausibel, aber schlicht falsch für
    diese konkreten Dateien.
  - **🔴→✅ Korrektur (2026-09-06):** die Annahme "alte, verwaiste
    music_albums-Zeilen sind harmlose Karteileichen, kein Cleanup nötig"
    aus Runde 2 war FALSCH — `ListMusicAlbums` filterte nie nach
    Track-Anzahl, verwaiste Zeilen (kein Item zeigt mehr per
    `music_album_id` drauf) erschienen dadurch als sichtbare "0 Titel"-
    Kacheln (User-Report). Doppelter Fix: `ListMusicAlbums` filtert jetzt
    zusätzlich per `EXISTS(SELECT 1 FROM items i WHERE i.music_album_id =
    a.id)`, UND `GroupMusicAlbums` räumt am Ende jedes Laufs verwaiste
    Zeilen der eigenen Library aktiv per `DELETE ... WHERE id NOT IN
    (SELECT DISTINCT music_album_id FROM items ...)` weg — verhindert
    unbegrenztes Anwachsen von `music_albums`/`user_music_album_favorites`
    über mehrere Rescans/Algorithmus-Wechsel hinweg (Favoriten auf einer
    verwaisten Zeile verschwinden automatisch mit, `ON DELETE CASCADE`).
    Test: `internal/store/music_grouping_test.go
    TestGroupMusicAlbumsCleansUpOrphanedAlbums`.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1431–1450 der alten CLAUDE.md

  **🔴 Vergrößern (Resize) funktionierte zunächst nicht, Verschieben (Reorder)
  schon (Bug, gefixt noch am selben Tag):** zwei unabhängige Ursachen.
  (1) Der Resize-Handle war `position:absolute; right:-6px` — ragte damit in
  den `gap:10px` zwischen den Grid-Spalten hinein, wo der Head-Container
  selbst über ihm lag (`document.elementFromPoint` an der berechneten
  Handle-Mitte traf nie den Handle, nur den Container — live per
  `claude-in-chrome`/`javascript_tool` verifiziert). Fix: Handle liegt jetzt
  als normales Flex-Kind (`flex:0 0 10px`) IM Zellfluss, keine absolute
  Positionierung mehr — Klickfläche = tatsächliche Bounding-Box. (2) Reorder
  lief über natives HTML5-`draggable="true"` auf der Kopfzelle — ein
  Resize-Versuch, der auf einem Kind-Element INNERHALB einer draggable-Zelle
  beginnt, wird vom Browser als Drag-Kandidat des Elternteils erkannt und
  unterdrückt danach reguläre `mousemove`-Events komplett (bestätigt: selbst
  mit explizitem `draggable="false"` auf dem Handle kam nicht einmal das
  `mousedown` an). Fix: Reorder läuft jetzt über dasselbe reine
  mousedown/mousemove/mouseup-Tracking wie Resize, kein natives DnD mehr
  (`REORDER_THRESHOLD` von 4px Mausbewegung, bevor ein Drag als Reorder statt
  Klick gilt). Getestet mit echten OS-Level-Mausereignissen (nicht nur
  synthetischen `dispatchEvent`-Aufrufen, die kein natives Drag auslösen und
  den Bug deshalb zunächst verdeckten).

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1460–1468 der alten CLAUDE.md

  **🔴 Spalte blieb trotzdem leer (Bug, gefixt noch am selben Tag):** die
  tatsächliche Datenquelle für die Album-Detail-Trackliste im Frontend ist
  NICHT `ListItems`, sondern `Store.ListMusicAlbumTracks`
  (`GET /api/albums/{id}`) — ein dritter, unabhängiger SELECT, der beim
  ersten Fix übersehen wurde und `i.genre` ebenfalls nicht lud. Live per
  claude-in-chrome verifiziert (API lieferte `genre` korrekt für
  `/api/items?genre=`, aber nicht für `/api/albums/{id}`, für dasselbe
  Item). Test: `TestListMusicAlbumTracksIncludesGenre` in
  `internal/store/music_edit_metadata_test.go`.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1486–1498 der alten CLAUDE.md

  **🔴 Erster Anlauf war fehlerhaft (Bug, noch am selben Tag gefixt, User-
  Report mit Screenshot: "An Innocent Man" von Billy Joel [1983] zeigte
  Jahr "2026"):** die erste Version nutzte bewusst KEINE neue Spalte,
  sondern `items.released_at` (dieselbe Quelle wie der "Veröffentlicht"-
  Sort) — das füllt der Scanner aber IMMER mit mindestens der Datei-mtime
  (`extractReleaseTime`-Fallback), zeigte dadurch bei praktisch jedem
  frisch gescannten Track das Kopierdatum statt des echten
  Erscheinungsjahrs. Fix: eigene, zuverlässig unterscheidbare Spalte
  (0 = "kein Jahr-Tag gefunden") statt der mtime-verseuchten
  `released_at`-Wiederverwendung. Bestehende Bibliotheken brauchen einen
  Rescan, damit der Scanner die Tags nachträglich liest (inkrementeller
  Scan reicht — unveränderte Dateien werden dabei NICHT neu geprobet, nur
  ein `force=true`-Rescan liest bereits bekannte Dateien erneut).

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1522–1598 der alten CLAUDE.md

  **🔴 Button war zunächst gar nicht erreichbar (Bug, gefixt noch am selben
  Tag, User-Report "ich sehe bei der Musik keinen Button zum Bearbeiten der
  Metadaten"):** ein Klick auf eine Musik-Kachel/-Zeile ruft IMMER
  `musicPlayAlbum()` auf und öffnet NIE `openDetail()` (siehe „Persistenter
  Mini-Player" oben — Musik startet Wiedergabe direkt, kein Video-Detail-
  Dialog). Der neue Formular-Zweig war also für Admins technisch fertig,
  aber es gab keinen Weg, ihn überhaupt zu öffnen. Fix: eigener ✏-Overlay-
  Button, admin-only, NUR bei Musik-Items — in der Kachel-Ansicht
  (`.edit-toggle`, `top:66 left:6`, cards.js) UND in beiden Track-
  Listenansichten (neuer `editMeta`-Slot in `MUSIC_LIST_CONTEXTS.fixedTrailing`,
  nach „fav", views.js) am Zeilenende. Beide Klick-Handler setzen
  `state.currentItem` + rufen `openEditMetaDialog()` direkt, mit
  `stopPropagation()` gegen das sonst auslösende Abspielen. Dieselbe
  Vererbungsfalle wie beim `.fav-toggle` in Listenzeilen (die generische
  Kachel-Overlay-Klasse ist `position:absolute`) — `.track-row-edit.edit-
  toggle` setzt das analog zu `.track-row-fav.fav-toggle` explizit auf
  normalen Inline-Fluss zurück.
  **🔴→✅ "Speichern" schien nichts zu tun — nur "Abbrechen" ging (Bug,
  gefixt 2026-09-10, LIVE 1.3.5, User-Report "ich kann nur Abbrechen
  klicken", präzisiert im Gespräch zu "er speichert das Jahr, nur der
  Dialog schließt nicht"):** der Wert wurde korrekt gespeichert (PUT-Call
  lief durch), aber der Musik-Zweig von `handleEditMetaSubmit` rief danach
  exakt denselben `openDetail(fresh)`-Aufruf wie der Film/Serien-Zweig
  auf — kopiert, ohne die eigene Konvention zu beachten. `openDetail()`
  öffnet `#detailDialog` (Poster/Plot/Cast-Layout), für das ein Musik-
  Track keine sinnvollen Daten hat, UND die App hält sich sonst überall
  strikt an "ein Klick auf eine Musik-Kachel/-Zeile ruft NIE openDetail()
  auf" (siehe „Persistenter Mini-Player" oben). Der zweite Dialog öffnete
  sich optisch direkt über dem gerade per `.close()` geschlossenen
  `editMetaDialog` — für den User nicht von "schließt nicht" zu
  unterscheiden. Fix: Musik-Zweig ruft nach dem Speichern nur noch
  `loadItems()` + Toast auf, kein `openDetail()` mehr.
  **✅ Album-Metadaten bearbeiten (seit 2026-09-11, LIVE 1.3.8, User-Wunsch:
  "Wenn ich beim Album das Jahr zum Beispiel eintrage, dann soll es
  natürlich auch für die Titel übernommen werden")** — nimmt die oben
  beschriebene bewusste Lücke zurück. Alben bleiben weiterhin ein reines
  Aggregat (`GroupMusicAlbums`/`canonicalAlbumFields`), es gibt also
  keine eigene Album-Zeile zum Editieren — stattdessen schreibt
  `Store.UpdateMusicAlbumMetadata(albumID, artist, album, genre, year)`
  (`internal/store/music.go`) die vier Felder per
  `UPDATE items ... WHERE music_album_id = ?` auf ALLE Tracks des Albums
  gleichzeitig (`year=0` lässt das Jahr unverändert, exakt wie beim
  Track-Edit) und stößt danach `GroupMusicAlbums` erneut an, damit die
  Aggregation neu berechnet wird. Endpoint `PUT /api/albums/{id}/metadata`
  (admin-only, `internal/api/music.go updateMusicAlbumMetadata`) prüft
  `requireLibAccess` über die Library des Albums. Frontend: neuer
  ✏-Button im Album-Detail-Header (neben dem ♥-Favoriten-Button, admin-only)
  öffnet `#editAlbumMetaDialog` (Künstler/Album/Genre/Jahr,
  `openEditAlbumMetaDialog`/`handleEditAlbumMetaSubmit` in `music.js`) —
  eigener Dialog/Speicherpfad, getrennt vom Track-Edit-Dialog, weil es
  serverseitig kein eigenes Album-Metadaten-Objekt gibt. Kein separater
  Entry-Point auf der Album-Kachel/-Übersichtszeile (bewusst nur EIN
  Einstiegspunkt, der Album-Detail-Header ist ohnehin bei jedem Album
  erreichbar).
  **🔴→✅ Album zeigte ein Jahr/Genre, einzelne Tracks daraus blieben aber
  leer (Bug, gefixt 2026-09-11, LIVE 1.3.9, User-Report "N Sync UK Version"
  → Song "Tearin' Up My Heart" ohne Jahr, obwohl das Album eins zeigt)**:
  `items.year`/`items.genre` kommen AUSSCHLIESSLICH aus dem eigenen
  Datei-Tag des jeweiligen Tracks — das Album-Jahr/-Genre ist dagegen ein
  reines Aggregat der ganzen Ordner-Gruppe (`canonicalAlbumFields`: erster
  nicht-leerer Wert gewinnt) bzw. kommt vom MusicBrainz-Fallback
  (`ApplyMusicBrainzMetadata`). Dieser Aggregat-Wert wurde bisher NIE auf
  Geschwister-Tracks zurückgeschrieben, die selbst kein eigenes Tag hatten
  — ein Track ohne Jahr-Tag im selben Album wie ein Track MIT Jahr-Tag
  zeigte deshalb dauerhaft "—", obwohl der Album-Header korrekt ein Jahr
  anzeigte. (Genre hatte dieses Problem in der Praxis seltener, weil
  `ApplyMusicBrainzMetadata` es bereits separat propagierte — aber NUR für
  den MusicBrainz-Pfad, nicht für aus Tags aggregierte Album-Werte.) Fix:
  `GroupMusicAlbums` (`internal/store/music.go`) liest nach dem
  Upsert-Schritt pro Gruppe den AKTUELLEN Album-Jahr-/Genre-Wert (nicht nur
  den aus dieser Gruppe frisch berechneten — deckt so auch ein
  nachträglich per MusicBrainz oder manuellem Album-Edit gesetztes
  Jahr/Genre ab) und schreibt ihn auf alle Tracks der Gruppe mit
  `year = 0`/`genre = ''`. Läuft bei JEDEM `GroupMusicAlbums`-Aufruf, also
  bei jedem (auch inkrementellen) Scan der Musik-Bibliothek — kein
  `force=true`-Rescan nötig, reine SQL-Nachbereitung auf bereits in der DB
  stehenden Werten, kein erneutes Tag-Lesen erforderlich.

### Musik-Bibliotheken (seit 2026-09-04) — Zeilen 1599–1630 der alten CLAUDE.md

- **🔴 IMDb-Zuordnung schlug bei obfuskierten Dateinamen fehl (Bug, gefixt
  2026-09-06, User-Report mit Screenshot: Datei „gb-100jamamoihwage-1080p",
  Fehler „Konnte Staffel/Episode aus Dateiname nicht ermitteln")**:
  `handleMatchImdb` (`matching.js`) parste im TV-Zweig stur NOCHMAL
  `S(\d{1,2})E(\d{1,3})` aus `item.title` — ignorierte dabei komplett die im
  selben Dialog sichtbaren `#matchSeason`/`#matchEpisode`-Eingabefelder, die
  laut `openMatchItem`-Kommentar GENAU für diesen Fall gedacht sind ("User
  kann manuell korrigieren, wenn der Dateiname nichts hergibt"). Bei einem
  Dateinamen ganz ohne SxxExx-Muster (Release-Obfuskation) blieben die
  Felder zwar sichtbar und ausfüllbar, wurden aber nie ausgelesen — jede
  manuelle Eingabe dort war wirkungslos. Fix: Season/Episode kommen jetzt
  primär aus den Formularfeldern, das Datei-Parsing ist nur noch Fallback
  falls die Felder leer sind.
  **🔴 Zweiter, tieferliegender Bug im selben Flow (gefixt 2026-09-06,
  direkt danach entdeckt):** selbst mit korrekt übermittelten Season/
  Episode-Werten ignorierte der Server (`setItemMetadata`,
  `internal/api/tmdb.go`) sie im IMDb-Zweig komplett — rief immer
  `Enrich.EnrichByIMDbID` auf, das bei einem TV-Treffer nur SHOW-Metadata
  liefert (kein Episode-Konzept) und hätte das Item fälschlich an die ganze
  Show statt an die konkrete Episode gebunden. Fix: bei
  `tmdbType=episode` wird die Show-ID jetzt zuerst über
  `client.FindByIMDb` aufgelöst (funktioniert auch, wenn die IMDb-ID einer
  einzelnen Folge gehört — TMDB liefert dafür `tv_episode_results[0].show_id`,
  die Parent-Show), danach exakt derselbe `FetchEpisodeMetadata(showID,
  season, episode)`-Call wie beim normalen numerischen-TMDB-ID-Pfad. Kein
  OMDb-Fallback für diesen Zweig — OMDb kennt kein Season/Episode-Konzept.
  **Live-Diagnose des konkreten User-Falls** (`tt42958561`, Server-Log via
  SSH geprüft: `[tmdb.FindByIMDb] tt42958561 -> movies=0 tv=0 episodes=0
  seasons=0`): reines Datenproblem, TMDB **und** OMDb (beide laut
  `GET /api/settings` konfiguriert) kennen diese IMDb-ID schlicht nicht —
  kein Software-Bug, der zweite Fund war unabhängig davon.


### Listenspalten "Zuletzt abgespielt"/"Wiedergaben"/"Hinzugefügt" + Spalten-Auswahl (seit 2026-09-14, LIVE 1.3.26) — Zeilen 1653–1663 der alten CLAUDE.md

  **🔴→✅ Fallstrick beim ersten Testlauf:** `MAX(us.last_played_at)` über
  eine korrelierte Subquery lieferte bei `modernc.org/sqlite` einen rohen
  `time.Time.String()`-String INKLUSIVE Monotonic-Clock-Suffix
  (`"... m=+0.098136418"`) zurück statt eines sauber typisierten DATETIME-
  Werts, wie es ein direkter Spaltenzugriff (kein Aggregat) liefert — keine
  der bestehenden `parseDBTime`-Layouts kann diesen variablen Suffix
  matchen. Fix: `parseDBTime` (`sqlite.go`) schneidet den `" m=..."`-Teil
  jetzt vorab ab, bevor es die bekannten Layouts probiert. Tests:
  `internal/store/play_count_test.go`
  (`TestTouchLastPlayedIncrementsPlayCount`,
  `TestListMusicAlbumsAggregatesPlayCountAndLastPlayed`).

### Merge-Duplikate — Zeilen 1803–1840 der alten CLAUDE.md

  **🔴 Bug direkt beim ersten Test gefunden (gefixt noch am selben Tag):**
  Auswahl über Suchtreffer hinweg (genau der "A Complete Unknown"-Fall, Dateien
  in zwei verschiedenen Bibliotheken Filme+Bluray) meldete "0 ausgewählt"
  trotz sichtbar angehakter Kacheln — `appendSearchResultCards` gruppiert PRO
  BIBLIOTHEK (siehe dort), aber beide Aufrufer (`renderHomeBranch` in der
  Startseiten-Suche, der `searching`-Zweig in `loadItemsBody`) setzten
  `state.lastRenderedItems` VORHER aus einem separaten, library-übergreifenden
  `groupVariants(items)`-Aufruf. Hat eine Zuordnung Dateien in mehreren
  Bibliotheken, fasst dieser äußere Aufruf sie zu WENIGER Einträgen zusammen
  als tatsächlich als eigene Kacheln gerendert werden — `state.selection`
  enthielt zwar die angeklickten IDs korrekt, aber `selectedItems()` filtert
  gegen `lastRenderedItems`, das die fehlende ID nie enthielt. Fix:
  `appendSearchResultCards` ist jetzt die alleinige Quelle für
  `state.lastRenderedItems` im Such-Modus, setzt es selbst aus exakt den
  Items, die es tatsächlich als Kachel rendert.
  **🔴 Zweiter Bug, vom User beim eigenen Test gefunden:** eine ausgewählte
  Kachel kann bereits mehrere Dateien bündeln (`groupVariants()` legt die
  Geschwister in `it._variants` ab, `it.id` ist nur der Repräsentant).
  `bulkMerge()` schickte bisher NUR die Repräsentanten-IDs der Auswahl — bei
  einer bereits gruppierten ×N-Kachel bekam dadurch nur die sichtbare Datei
  die neue gemeinsame Zuordnung, ihre bis dahin korrekt gruppierten
  Geschwister blieben auf der ALTEN metadata_id hängen und wurden aus ihrer
  eigenen, vorher richtigen Gruppe herausgerissen — sichtbar als "Merge
  hat nichts bewirkt, es bleiben 2 Kacheln". Fix: `bulkMerge()` sammelt jetzt
  alle `_variants`-IDs jeder Auswahl ein, nicht nur die des Repräsentanten.
  **🔴 Dritter Bug, selber Fall:** nach dem korrekten Merge (3 Dateien, eine
  davon über `variant_split=true` versteckt fehlerhaft als eigene Kachel
  ausgeblendet — separat gefixt, s.u.) zeigte die Kachel korrekt "×3", aber
  das Varianten-Dropdown im Detail-Dialog listete nur 2 Einträge.
  `openDetail()` (player.js) übernahm ein vom Grid mitgegebenes
  `item._variants` ungeprüft, sobald es `.length > 1` hatte — `groupVariants()`
  gruppiert aber nur INNERHALB der gerade geladenen (oft bibliotheksgescopten)
  Liste, enthält also nie Geschwister aus einer ANDEREN Bibliothek. Der
  ×N-Badge kommt dagegen aus dem server-seitig über ALLE Bibliotheken
  gezählten `variantCount` — beide Quellen liefen auseinander. Fix:
  `openDetail()` holt die Varianten nicht mehr aus dem Grid-Kontext, sondern
  IMMER frisch über `/api/items/{id}/variants` (die einzige wirklich
  vollständige, bibliotheksübergreifende Quelle).

### UI — Zeilen 1962–1972 der alten CLAUDE.md

  **🔴 Custom-Metadaten ohne Poster zeigten trotzdem den Dateinamen (Bug,
  gefixt 2026-09-06, User-Report mit Screenshot: 12 per Custom-Metadaten
  betitelte "Terra X History"-Folgen zeigten weiterhin ihre kryptischen
  Release-Dateinamen als Kachel-Titel):** der Titel-Override
  (`title = it.metadata.title`) saß bisher NUR im `posterPath`-Zweig von
  `renderCard` — ein Item MIT Metadaten, aber OHNE Poster (Custom-Match ohne
  hochgeladenes Bild, oder ein TMDB-Treffer ohne Poster-URL) fiel in den
  reinen Thumbnail-`else`-Zweig, der `title` nie anfasste. Betrifft nicht nur
  den hier gemeldeten Fall, sondern jede Custom-Zuordnung ohne Poster-Upload
  (auch Privat-Libs, `POST .../metadata-manual`). Fix: derselbe
  Title/Jahr-Override läuft jetzt auch im Non-Poster-`else`-Zweig.

### UI — Zeilen 2042–2076 der alten CLAUDE.md

  **🔴 Filter ging beim Rein-und-Wieder-Rausnavigieren verloren (Bug, gefixt
  2026-09-06, User-Report: "Wenn ich bei Serien nach Buchstabe filtere, und
  dann in eine Serie rein gehe, und dann wieder raus, dann ist der
  Buchstabenfilter weg. Der soll jedoch bleiben"):** der Reset in
  `loadItems()` (grid.js) feuerte bisher bei JEDER `navKey()`-Änderung — ein
  reiner Rundgang Library-Root → Serien-Ordner → zurück zum Library-Root
  sind DREI verschiedene navKeys, jeder Schritt löschte den Filter. Der
  Filter ist aber an die BIBLIOTHEK gebunden, nicht an den exakten navKey.
  Fix: neue `navLibraryKey(key)` (app.js) reduziert einen navKey auf seinen
  Bibliotheks-/Kontext-Teil (`"lib:7:Billions:s1"` → `"lib:7"`) — der Reset
  vergleicht jetzt NUR diesen Teil, bleibt also über Ordner/Staffel/Album-
  Wechsel INNERHALB derselben Bibliothek erhalten und feuert nur noch beim
  echten Wechsel in eine andere Bibliothek oder einen anderen Top-Level-
  Kontext (Home/Sammlungen/Playlists/Person-Filter — dort bleibt das alte
  Verhalten: kompletter navKey-Vergleich, da `navLibraryKey` für
  Nicht-`"lib:"`-Keys den Key unverändert durchreicht).
  **🔴→✅ Zweite Runde, noch am selben Tag (User-Korrektur: "Da läuft was
  schief... Der Buchstabenfilter soll erhalten bleiben, wenn man wieder
  zurück geht. In den Ordner/Serie wenn man reingeht, darf kein Filter
  greifen"):** die erste Fix-Version behielt zwar den Filter-WERT über die
  ganze Bibliothek hinweg, wendete ihn aber weiterhin blind auf JEDES Grid
  an — beim Reingehen in eine Serie wurden dadurch praktisch alle Episoden
  ausgeblendet (Episodentitel starten selten mit demselben Buchstaben wie
  der Show-Name). Fix: neuer State `state.alphaFilterScopeKey` (app.js) —
  merkt sich den `navKey()`, an dem der Filter per Sidebar-Klick GESETZT
  wurde. `applyAlphaFilter()` (läuft nach jedem Render, auch nach reiner
  Navigation) blendet Kacheln nur noch aus, wenn `state.alphaFilterScopeKey
  === navKey()` — also exakt an der Stelle, wo der User ihn gesetzt hat
  (typischerweise die Serien-Übersicht einer Library), nicht mehr in jedem
  Ordner darunter oder danach. Der WERT (`state.alphaFilter`) selbst bleibt
  weiterhin bibliotheksweit erhalten (siehe `navLibraryKey`-Reset oben) und
  wird beim Zurücknavigieren zum ursprünglichen navKey automatisch wieder
  sichtbar aktiv — inkl. Banner- und Sidebar-Aktiv-Markierung, die jetzt
  ebenfalls zentral in `applyAlphaFilter()` statt verstreut in
  `setAlphaFilter()` gepflegt werden.

### Staffel-Ansicht für Serien — Zeilen 2351–2451 der alten CLAUDE.md

  **🔴→✅ Nach manueller Serien-Zuordnung blieb die Staffel-Ansicht
  dauerhaft deaktiviert (Bug, gefixt 2026-09-10, LIVE 1.3.2, User-Report
  „Wenn ich eine Serie manuell zuordne, werden die Folgen danach nicht zu
  Staffeln gruppiert"):** hatte der User denselben Ordner VOR der Zuordnung
  schon mal geöffnet (typisch: Ordner ohne TMDB-Match anklicken → obiger
  Fallback greift → `seasonView:<libID>:<folder>="0"` wird persistiert),
  blieb dieser Per-Ordner-Override nach der manuellen Zuordnung über den
  Matching-Dialog unverändert stehen — `applyMatch()`/`handleMatchImdb()`
  (`matching.js`) riefen nach erfolgreichem Folder-Match zwar `loadItems()`
  neu auf, löschten aber nie den alten „keine Staffeln"-Eintrag.
  `seasonViewEffective()` (`app.js`) las weiterhin `false` für diesen
  Ordner, obwohl der Server inzwischen (nach dem serverseitigen
  Episode-Matching) echte Staffeldaten geliefert hätte — Ergebnis: flache
  Dateiliste statt Staffel-Kacheln, dauerhaft, bis der User manuell im
  „🔤 Anzeige"-Menü o.ä. nachhilft. Fix: beide Folder-Match-Zweige
  (`tgt.type === "folder"` in `applyMatch`/`handleMatchImdb`) löschen den
  `seasonView:<libID>:<folder>`-Key per `localStorage.removeItem` direkt
  nach erfolgreichem `POST .../folders/metadata`, bevor `loadItems()`
  läuft — kein neuer Scan nötig (Season/Episode werden ohnehin live aus dem
  Dateinamen geparst, siehe `matchItem` in `internal/enrich/matching.go`).
  **Bekannte Restlücke:** Dateien, deren Name kein SxxExx-Muster hergibt,
  bleiben unabhängig davon ungruppiert (weder der synchrone
  `UnmatchedEpisodeFiles`-Fallback in `internal/api/series.go` noch das
  asynchrone Matching in `matchItem` können ohne erkennbares Muster eine
  Episode zuordnen) — das ist ein Namensschema-Problem, kein Bug dieses Fixes.
  **🔴→✅ Nachkorrektur (LIVE 1.3.3, noch am selben Tag):** obiger Fix reicht
  nur für NEUE Zuordnungen ab diesem Zeitpunkt — bereits VOR dem Fix
  entstandene `seasonView:<libID>:<folder>="0"`-Merker (der Sackgassen-
  Fallback selbst ist seit Monaten live, betrifft potenziell jede Serie, die
  jemals in diese Sackgasse gelaufen ist) blieben weiterhin für immer hängen,
  weil `matching.js` sie nur schreibseitig bei einer neuen Aktion aufräumt.
  User-Report bestätigte das direkt: zwei bereits vor dem Fix zugeordnete
  Serien blieben trotz korrekt gesetzter `season`/`episode` in der DB
  weiterhin ungruppiert. Fix: `grid.js` (im selben Block, der `hasSeasons`
  aus der Seasons-API berechnet) räumt den Merker jetzt zusätzlich
  LESESEITIG auf — liefert die API tatsächlich Staffeln, aber der
  Pro-Ordner-Merker steht noch auf `"0"`, wird er als veraltet erkannt
  (er wird an KEINER anderen Stelle im Code je auf `"0"` gesetzt außer im
  Sackgassen-Fallback selbst) und entfernt; `state.seasonView` wird danach
  über `seasonViewEffective()` neu berechnet (fällt auf den Library-Default
  zurück, überschreibt also nicht eine bewusst library-weit ausgeschaltete
  Staffel-Ansicht). Heilt sich dadurch für JEDE betroffene Bestandsserie
  automatisch beim nächsten Öffnen — kein manueller Browser-Console-Eingriff
  oder `localStorage.clear()` nötig.
  **Bleibender Info-Header im Fallback (seit 2026-09-06, User-Wunsch: „bei
  nicht zugeordneten Serien soll auch so ein Infofenster aufgehen, mit den
  gleichen Buttons"):** der Toast allein verschwindet nach wenigen Sekunden
  ohne bleibenden Hinweis. `grid.js` merkt sich beim Fallback in
  `state.pendingShowInfoHeader` entweder `data.show` (Ordner IST TMDB-
  zugeordnet, nur keine erkennbare Staffel-Struktur — Tatort/Terra-X-Fall)
  oder `{unmatched:true, folder}` (showTmdbId===0, gar keine Zuordnung) und
  stellt danach — NACH dem `grid.innerHTML=""` des normalen Ordner-
  Renderings, sonst sofort wieder gelöscht — einen Header voran:
  `renderShowHeader(data.show, null)` (voller Header inkl. ALLER Buttons:
  TMDB neu laden/Poster ändern/Episoden neu zuordnen/Zuordnung entfernen)
  im ersten Fall, `renderUnmatchedFolderHeader(folder)` (views.js, Header mit
  „🔍 Serie zuordnen…" + „🖼 Poster hochladen") im zweiten. `showOut` trägt
  seit diesem Feature zusätzlich `showTmdbId` (0 bei einem reinen Custom-
  Eintrag ohne echtes TMDB-Match) — `renderShowHeader` blendet „↻ TMDB neu
  laden"/„⚠ Episoden neu zuordnen" aus, wenn `showTmdbId` fehlt, „🖼 Poster
  ändern"/„🚫 Zuordnung entfernen" bleiben immer sichtbar (funktionieren
  generisch über `metadataId`, unabhängig vom TMDB-Match).
  **🖼 Poster auch bei komplett unzugeordneten Serien (seit 2026-09-06,
  User-Wunsch: „ich will auch bei unzugeordneten Serien ein Poster
  hinzufügen können"):** `renderUnmatchedFolderHeader`s „🖼 Poster
  hochladen"-Button legt bei Klick zuerst per
  `POST /api/libraries/{id}/folders/metadata-manual` (`createCustomFolderMetadata`
  in `internal/api/tmdb.go`, Pendant zu `createCustomMetadata` — dort für
  ein Item, hier für den ganzen Ordner) einen `tmdb_type="custom"`-Metadata-
  Eintrag an (`TMDBID = -time.Now().UnixNano()` für Eindeutigkeit, Titel =
  Ordnername) und verknüpft ihn per `SetFolderMetadata`, dann öffnet er den
  bestehenden `openPosterPicker(metadataId, onApplied)`-Dialog darauf (der
  TMDB-Tab bleibt dort leer, der Upload-Teil funktioniert unverändert
  generisch). **`seriesSeasons`-Handler liefert jetzt auch im
  `showTmdbId===0`-Early-Return ein `show`-Objekt**, wenn
  `Store.GetFolderMetadataID` (bewusst OHNE `tmdb_type`-Filter, anders als
  `ShowTMDBForFolder`/`ShowMetadataIDForFolder`, die nur `tv` matchen) eine
  Zuordnung findet — sonst wäre der gerade hochgeladene Custom-Titel/Poster
  beim nächsten Öffnen des Ordners nicht mehr sichtbar gewesen (nur
  `metadataId`/`title`/`posterPath`, keine Seasons/Cast — die gibt's nur
  bei echtem TMDB-Match).
  **Bekannte Einschränkung:** die wiederverwendeten Show-Header-Buttons
  (TMDB neu laden etc.) rufen bei Erfolg weiterhin `renderSeasonFolders`/
  `renderSeasonEpisodes` direkt auf statt `loadItems()` — im Fallback-
  Kontext (Season-View bereits deaktiviert) kann das kurzzeitig ein leeres
  Season-Grid statt der normalen Dateiliste zeigen, bis erneut navigiert
  wird. Kein Crash, nur ein UX-Rest, der bei Bedarf durch Umstellen auf
  `loadItems()` als gemeinsamen Refresh-Pfad behoben werden könnte.
  **🔴 Header verschwand nach dem ersten Öffnen wieder (Bug, gefixt noch am
  selben Tag, User-Report "ich sehe den Poster-Button nicht"):** der ganze
  Block inkl. Info-Header-Logik hing an `if (state.seasonView && …)`. Der
  Fallback selbst persistiert `seasonView:<lib>:<folder>="0"` — beim
  NÄCHSTEN Öffnen desselben Ordners war `state.seasonView` dadurch schon
  `false`, der komplette Block (nicht nur die Staffel-Kachel-Darstellung)
  wurde übersprungen, der Header erschien nur beim allerersten Aufruf. Fix:
  der Seasons-API-Call + die Header-Entscheidung laufen jetzt IMMER für
  TV-Ordner (unabhängig von `state.seasonView`); nur ob Staffel-KACHELN
  oder die normale Liste gerendert werden, hängt weiter vom Toggle ab. Der
  Auto-Disable-Toast feuert weiterhin nur beim ÜBERGANG true→false (Guard
  `&& state.seasonView` vor dem Umschalten), sonst hätte er bei jedem
  Öffnen erneut auftauchen können.

### Metadaten-Bestätigung + Verdächtige Zuordnungen — Zeilen 2572–2610 der alten CLAUDE.md

- **🔴 "Zuordnung entfernen" hielt nicht — Ordner wurde binnen Minuten vom
  periodischen Enrichment-Worker automatisch wieder (falsch) gematcht
  (gefixt 2026-09-06):** User-Report direkt nach dem Feature oben: "Terra X"
  war Minuten nach dem manuellen Entfernen schon wieder zugeordnet — diesmal
  auf "Terra X History" statt "Terra Xpress", also erneut falsch. Root
  Cause war ein VORBESTEHENDER Bug, der durch das neue Feature erst
  sichtbar wurde, an ZWEI unabhängigen Stellen im 5-Minuten-Worker
  (`internal/enrich/worker.go runOnce` → `enrichFolders` UND `enrichItems`):
  beide prüften nur, ob der Ordner (noch) eine `metadata_id` hat, NIE ob
  bereits ein bewusster Versuch (mit Ergebnis "NULL") stattgefunden hat.
  - **`Store.PendingFolders`** (SQL): `LEFT JOIN folder_metadata fm ...
    WHERE fm.metadata_id IS NULL` — bei einem LEFT JOIN ist `fm.metadata_id`
    NICHT NUR NULL, wenn GAR KEINE Zeile existiert, sondern AUCH, wenn eine
    Zeile existiert und ihr `metadata_id` NULL ist (TMDB fand nichts, ODER
    Admin hat entfernt). Fix: Bedingung auf `fm.folder IS NULL` geändert —
    `folder_metadata` hat `PRIMARY KEY (library_id, folder)`, beide NOT
    NULL, `fm.folder` ist daher ein zuverlässiger "Zeile existiert
    überhaupt"-Indikator, unabhängig vom `metadata_id`-Wert. Das war
    ursprünglich SCHON ALS BUG vorhanden (der Code-Kommentar bei
    `matchShow`s NULL-Write sagt explizit "damit wir nicht endlos
    retry'en") — nur bis jetzt nie aufgefallen, weil vor "🚫 Zuordnung
    entfernen" der einzige Weg zu einer NULL-Zeile ein gescheiterter
    TMDB-Suchversuch war (seltener Fall, kaum beobachtet).
  - **`enrichItems`/`enrichFolderSync`** (über `matchItem`,
    `internal/enrich/worker.go`): prüfte nur `showMetaID == 0` (aus
    `GetFolderMetadataID`, das „keine Zeile" und „Zeile mit NULL" NICHT
    unterscheiden KANN) und löste bei 0 sofort erneut `matchShow` aus.
    Fix: neue `Store.FolderMetadataRowExists(libID, folder)` — liefert
    `true`, sobald irgendeine Zeile existiert (Wert egal). `matchItem`
    triggert `matchShow` jetzt NUR NOCH, wenn GAR KEINE Zeile existiert;
    existiert eine (auch mit NULL), gibt es einen Fehler zurück
    ("bewusst unmatched (kein Auto-Retry)") statt erneut zu suchen.
  - Beide Fixe zusammen sind nötig — `enrichFolders` läuft VOR `enrichItems`
    in jedem `runOnce()`-Zyklus und hätte den Ordner sonst weiterhin allein
    schon wieder gematcht, selbst mit nur einem der beiden Fixe.
  - Tests: `internal/store/folder_metadata_test.go` (`PendingFolders`
    ignoriert eine bewusst-NULL-Zeile, `FolderMetadataRowExists`
    unterscheidet beide Fälle direkt).


### Playback — Zeilen 2644–2676 der alten CLAUDE.md

- **🔴 Dateien mit eingebettetem Cover-Bild spielten nur ein 1-Frame-
  Standbild statt des echten Films (Bug, gefixt 2026-09-07, User-Report
  "Immer Ärger mit 40"/"Vielleicht lieber morgen" — WMV-Dateien mit
  eingebettetem `mjpeg`-Thumbnail, `disposition.attached_pic=1`):**
  `internal/playback/ffmpeg.go` (Transcode) UND `internal/download/prepare.go`
  (Compat-Download) bauten den ffmpeg-Befehl mit `-map "0:v:0"`
  (klein-`v`) — ffmpegs Stream-Specifier `v` zählt EINFACH alle
  Video-Streams durch, ein vorangestellter Cover-Thumbnail-Stream (z. B.
  Index 0 = mjpeg 320×180 attached_pic) gilt dabei als "Video-Stream 0"
  und wurde statt des echten Films (z. B. Index 2 = `wmv1` 1280×720)
  transcodiert/kopiert. Fix: Großbuchstabe `-map "0:V:0"` — ffmpegs
  Stream-Specifier `V` bedeutet explizit "Video, OHNE attached
  pictures/Thumbnails/Cover-Art". `internal/scanner/scanner.go` hatte
  dieselbe Ausnahme für die Metadaten-Erkennung (VideoCodec/Width/Height)
  schon seit dem Musik-Cover-Art-Fix vom 2026-09-04 (`disposition
  .attached_pic == 1 → continue`), aber NUR dort — beim tatsächlichen
  Transcode/Download-ffmpeg-Aufruf fehlte die gleiche Ausnahme bisher.
  Erklärt auch die **falsche Auflösungs-Anzeige (z. B. "180p" statt
  "720p")** bei betroffenen Dateien — reines Datenproblem aus der Scan-
  Zeit VOR dem 2026-09-04-Fix, kein separater Bug: `Store.UpsertItem`
  überschreibt `width`/`height` nur bei einem erneuten (Force-)Scan, ein
  inkrementeller Scan probet unveränderte Dateien nie erneut. Betroffene
  Bibliotheken brauchen einmal **`?force=true`**, damit der Scanner
  Video-Codec/Auflösung mit der schon länger korrekten Logik neu ermittelt.
  `download/prepare.go`s `convVersion` (Cache-Invalidierung für
  Compat-Downloads) auf **5** erhöht, damit eine vor diesem Fix erzeugte
  (kaputte, nur-Cover-Bild-)Download-Kopie verworfen und neu erzeugt wird.
  **Nicht betroffen:** Trickplay-Sprite-Generierung und die
  Thumbnail-Extraktion beim Scan — beide rufen ffmpeg ohne explizites
  `-map` auf, ffmpegs automatische Stream-Auswahl ohne `-map` wählt den
  Video-Stream nach einer Auflösungs/Bitrate-Heuristik, nicht nach
  Reihenfolge, und griff dadurch schon vorher korrekt zum echten Film statt
  zum kleinen Thumbnail.

### Shuffle-Play — Zeilen 2821–2827 der alten CLAUDE.md

  **🔴 War kurzzeitig live kaputt (Commit `9723b9b` fixt `a96624f`):** der
  else-Zweig rief sich versehentlich selbst rekursiv auf (Tippfehler bei
  einem `sed`-Bulk-Replace) — jeder Zufalls-Klick auf eine NICHT-Musik-
  Bibliothek endete in "Maximum call stack size exceeded" statt den Player
  zu öffnen. **Lektion: nach einem `sed`/Skript-Bulk-Replace IMMER die
  Funktionsdefinition selbst mit angrep-en**, nicht nur die Call-Sites —
  ein zu breiter Suchstring kann die eigene Implementierung mittreffen.

### Transcode-Seek (Capture-Handler + Session-Restart) — Zeilen 2916–2983 der alten CLAUDE.md

  **🔴→✅ Der ursprüngliche No-Op-Patch wirkte NICHT zuverlässig (User-Report
  2026-09-10, "flackert/springt bei transcodierten Videos, bei Direct Play
  nicht" — LIVE 1.2.52):** reines `sb.update = wrapperFn` reicht nicht.
  Video.js' `SeekBar` (verifiziert gegen den exakten gepinnten Build
  `video.js@8.17.3`) registriert ihre `update`-Methode bereits im
  KONSTRUKTOR direkt als Event-Listener
  (`this.on(player, ["timeupdate","durationchange"], this.update)`) —
  synchron beim Bau der ControlBar, also BEVOR `syncTranscodeDisplays()`
  überhaupt läuft. `on(target, event, fn)` hält die Funktions-REFERENZ zum
  Bindungszeitpunkt fest, keinen dynamischen Property-Lookup — ein
  späteres `sb.update = ...` ändert an diesem bereits registrierten
  Listener nichts. Der native Handler feuerte dadurch bei JEDEM
  `timeupdate` (mehrmals pro Sekunde) unverändert weiter und schrieb die
  falsche (relative) Breite, im Wechsel mit dem RAF-Loop — exakt das
  beobachtete Flackern. Fix (`player-transcode-seek.js`): den
  ORIGINAL-Listener explizit per `vjs.off(["timeupdate","durationchange"],
  origUpdate)` entfernen (identische Funktionsreferenz, `off` matched wie
  `on` per strikter Gleichheit) und durch einen eigenen, modusabhängigen
  Listener ersetzen (`vjs.on([...], guardedUpdate)`), der im Transcode-Modus
  gar nichts aufruft, sonst 1:1 `origUpdate.apply(sb, args)`. **Nicht live
  im Browser verifizierbar in dieser Session** (bekannte
  claude-in-chrome-Einschränkung, `document.visibilityState` bleibt im
  MCP-Tab „hidden", `<video>` lädt dadurch nie echt — siehe „GoldfishTV"-
  Abschnitt) — Fix basiert auf Analyse des tatsächlichen gepinnten
  Video.js-Bundles (per curl heruntergeladen, `SeekBar`-Konstruktor +
  `enableInterval_`/`this.setInterval(this.update,30)` durchgelesen), nicht
  nur Vermutung. **Sollte vom User im echten Browser gegengeprüft werden**
  — falls das Flackern weiterhin auftritt, als nächstes prüfen, ob
  `vjs.off()` den Listener tatsächlich entfernt (z.B. `console.log` der
  Listener-Anzahl vor/nach, oder ob Video.js' `on(target,type,fn)`-Overload
  die Funktion intern nochmal wrapped statt der rohen Referenz — dann
  müsste der Original-Listener stattdessen über die SeekBar-Komponente
  selbst `sb.off(vjs, [...], origUpdate)` entfernt werden statt über `vjs`).
  **User-Bestätigung (2026-09-10): Flackern behoben.** Direkter
  Folgewunsch danach: „ein kleiner Punkt zeigt die aktuelle Stelle" — im
  **Pill-Skin** (`style.css`) verschluckte `overflow: hidden` auf
  `.vjs-progress-holder` (nur dort für die runde Pillenform gesetzt,
  eigentlich unnötig, da `.vjs-load-progress`/`.vjs-play-progress` bereits
  `border-radius: inherit` selbst tragen) Video.js' eingebauten
  Scrubber-Punkt (`.vjs-play-progress:before`, per Default-CSS mit
  `right:-.5em` leicht über den Balkenrand hinaus positioniert — im
  SVG-Icon-Modus stattdessen ein `.vjs-svg-icon`-Kindknoten). Fix (LIVE
  1.2.53): `overflow: hidden` vom Holder entfernt (Pillenform bleibt über
  die Kind-Elemente erhalten) + der Punkt selbst als expliziter weißer
  Kreis mit Schatten gestylt (`color: transparent` verbirgt den
  ursprünglichen Font-Icon-Glyphen, `.vjs-svg-icon svg { display: none }`
  im SVG-Modus), statt sich auf den dezenten Video.js-Default zu
  verlassen. Betraf **nur den Pill-Skin** — der Standard-Skin setzte nie
  `overflow:hidden` auf den Holder. Keine JS-Änderung nötig: der Punkt
  hängt per CSS am rechten Rand von `.vjs-play-progress` selbst, dessen
  Breite sowohl Direct Play (nativ) als auch Transcode (unser RAF-Loop
  oben) bereits korrekt setzen.
  **🔴→✅ Nachkorrektur (User-Report 2026-09-10: "nicht auf der Zeitleiste,
  sondern schneidet ihn tangenzial. Der Punkt ist unter der Leiste. Und er
  ist mir zu groß", LIVE 1.3.1):** `width`/`height` auf einem
  `:before`-Pseudo-Element OHNE `display:block` bewirken bei Browsern
  schlicht gar nichts — `:before`/`:after` sind standardmäßig `inline`,
  und Inline-Boxen (nicht ersetzte Elemente) ignorieren explizite
  Breiten-/Höhenangaben komplett. Der sichtbare Kreis kam beim ersten
  Versuch dadurch weiterhin nur aus dem UNVERÄNDERTEN Glyphen-Kasten des
  Original-Font-Icons (Video.js' eigene `line-height:.35em`-Positionierung,
  für einen Text-Glyphen gedacht, nicht für einen zentrierten Punkt) — mein
  `width:11px;height:11px` griff nie. Fix: `content:""` (kein Glyph mehr),
  `display:block`, komplett eigene Positionierung
  (`position:absolute;top:50%;right:0;transform:translate(50%,-50%)` —
  zentriert den Punkt exakt AUF dem Balkenende statt daneben/darunter),
  kleiner (8px statt 11px). Gleiches Prinzip für den `.vjs-svg-icon`-
  Kindknoten im SVG-Icon-Modus.

### Performance — Zeilen 3008–3031 der alten CLAUDE.md

  **🔴 Reichte bei sehr vielen Kacheln nicht (Bug, gefixt 2026-09-06,
  User-Report "ich bin immer ganz oben, das hatten wir schon einmal
  besser"):** bei Ansichten mit hunderten Kacheln (z. B. 219 Serien in der
  TV-Bibliotheks-Übersicht) liefert `content-visibility:auto` +
  `contain-intrinsic-size` beim allerersten Layout-Pass nur eine GESCHÄTZTE
  Höhe für off-screen-Kacheln — der doppelte rAF reicht nicht immer, bis der
  Browser genug Kacheln tatsächlich vermessen hat, damit die Seite schon
  hoch genug für die Ziel-Scroll-Position ist. `scrollTo` clampt dann auf
  die zu diesem frühen Zeitpunkt noch zu kleine maximale Scroll-Höhe — ohne
  weiteren Versuch bleibt die Seite dauerhaft dort hängen, auch nachdem der
  Inhalt seine finale Höhe erreicht hat. Fix: ein zusätzlicher, einmaliger
  Korrektur-Versuch nach 200ms (nur wenn `window.scrollY` die gespeicherte
  Position noch nicht erreicht hat UND der User inzwischen nicht bereits
  weiternavigiert ist — `state.lastNavKey === targetKey`-Check verhindert,
  dass ein verzögerter Restore einen erst später geöffneten, anderen navKey
  trifft). **Nicht End-to-End live verifizierbar in dieser Session** —
  `document.visibilityState` ist im claude-in-chrome-MCP-Tab "hidden",
  wodurch `requestAnimationFrame` browserseitig gedrosselt/pausiert wird und
  das Timing-Verhalten dort nicht reproduzierbar testbar ist (der reine
  State-Save-Teil wurde bestätigt: `scrollPositions`-Map enthielt nach
  Navigation korrekt den vorherigen scrollY-Wert). Der Fix ist rein additiv
  (ein zusätzlicher späterer Korrekturversuch, kein Eingriff in den
  bestehenden Pfad) — sollte im echten, fokussierten Browser des Users
  bestätigt werden.

### Filter-UI — Zeilen 3065–3080 der alten CLAUDE.md

  **🔴 Wirkte zunächst nicht in der Musik-Album-Übersicht (Bug, gefixt noch
  am selben Tag):** die Standard-Ansicht einer Musik-Bibliothek ist die
  Album-Kachel-Übersicht (`GET /api/libraries/{id}/albums`), NICHT der
  generische `/api/items`-Pfad, den `ItemFilter.Genres` bedient — der Filter
  lief dort also komplett ins Leere (live per `claude-in-chrome` verifiziert:
  gleiche Trefferzahl mit und ohne `genre=`-Query-Param). Fix:
  `Store.ListMusicAlbumsFiltered(libraryID, userID, genres)` (neue Funktion,
  `ListMusicAlbums` ist jetzt ein dünner Wrapper ohne Filter — bewahrt die
  alte 2-Arg-Signatur für bestehende Aufrufer/Tests) filtert zusätzlich per
  `a.genre IN (...)` auf `music_albums.genre` (die bereits aggregierte
  Album-Genre-Spalte, kein LIKE nötig wie bei `items.genre`/Multi-Genre-
  Strings). `listAlbums`-Handler + alle drei Frontend-Album-Fetch-Stellen in
  `grid.js` (Übersicht/Favoriten/"Alle Titel") hängen den Filter jetzt mit an
  (`musicGenreQS()`-Helper in app.js für den Albums-Endpoint, der anders als
  `/api/items` keine URLSearchParams vorab baut). Test:
  `internal/store/music_albums_genre_filter_test.go`.

### Person-Filter (Schauspieler-Klick) — Zeilen 3215–3232 der alten CLAUDE.md

- **🔴 Scroll-Position ging beim Rein/Raus verloren, wenn eine Serien-Kachel
  in der Filmografie geöffnet wurde (Bug, gefixt 2026-09-08, User-Wunsch:
  „möchte an der gleichen Stelle wieder rauskommen")**: `navKey()` (`app.js`)
  lieferte für die Filmografie-Übersicht UND die per Show-Kachel geöffnete
  Episoden-Unteransicht (`state.personFilterShow`, siehe
  `renderPersonShowCard` in `cards.js`) denselben Key `"person:<tmdbId>"`.
  Ein Klick auf eine Serie speicherte die Filmografie-Scrollposition zwar
  korrekt unter diesem Key, der kurze Rücksprung aus der Episodenliste
  überschrieb sie aber sofort wieder (meist mit ~0, da die Episodenliste kurz
  ist) — man landete beim endgültigen Verlassen des Person-Filters immer
  ganz oben. Fix: analog zu den `lib:`-Keys (die Folder/Staffel/Album-Tiefe
  im Key kodieren) bekommt die Show-Unteransicht jetzt einen eigenen Suffix
  (`"person:<tmdbId>:show:<libraryId>:<folder>"`), beide Ebenen landen
  dadurch in getrennten `state.scrollPositions`-Slots. Der einfache
  Fall (Schauspieler öffnen → direkt zurück, ohne Serien-Zwischenstopp) war
  bereits vorher korrekt (openPersonView/clearPersonView in player.js laufen
  beide durch den normalen `loadItems()`-Scroll-Save/Restore-Pfad).


### Sammlungs-Komplett-Badge — Zeilen 3265–3280 der alten CLAUDE.md

  **🔴 Bug + Fix am selben Tag:** die erste Version verlangte ein konkretes
  ZUKÜNFTIGES Datum (`release_date > date('now')`) — ein Part mit leerem
  `release_date` (typisch bei früh angekündigten Fortsetzungen, die bei TMDB
  schon ein Poster, aber noch kein Datum haben, real beobachtet: „Den of
  Thieves 3" in der „Criminal Squad"-Sammlung) fiel dadurch durchs Raster und
  zählte als „fehlt wirklich" statt „noch nicht erschienen" — Sammlung blieb
  trotz vollständigem Bestand als unvollständig markiert. Fix: Bedingung ist
  jetzt `release_date IS NULL OR release_date = '' OR release_date > date('now')`.
  Enrichment (`internal/enrich/worker.go`, Collection-Parts-Fetch) überspringt
  Parts nur, wenn SOWOHL `release_date` ALS AUCH `poster_path` leer sind
  (reine TMDB-Platzhalter ohne jede Info) — ein Part mit nur fehlendem Datum
  aber vorhandenem Poster bleibt als „Bald"-Kachel sichtbar.
  Frontend: `renderCollectionPartCard` (`cards.js`) zeigt für solche Teile
  ein blaues „Bald"-Badge (`.missing-badge--upcoming`) statt des roten
  „Fehlt"-Badges — rein kosmetisch, ändert nichts an der Vollständigkeits-Logik.
  Test: `internal/store/collections_acl_test.go TestCollectionsUnreleasedParts`.

### Download & Löschen — Zeilen 3497–3522 der alten CLAUDE.md

  **🔴→✅ Downscale konnte die Datei GRÖSSER als das Original machen (Bug,
  gefixt noch am selben Tag, convVersion 6, User-Report: ein YouTube-Video
  mit effizient kodierten ~1,9 Mbps wurde auf "480p" gestellt — traf
  serverseitig aber auf den ERSTEN Katalog-Eintrag "480p-hq · 2 Mbps" (drei
  Bitraten-Stufen pro Auflösung in `playback.Profiles`) — 273 MB Original
  → 315 MB "optimierter" Download trotz Auflösungs-Downscale auf 480p):**
  `needsDownscale` entscheidet nur, OB überhaupt runtergerechnet wird
  (Höhe- oder Bitrate-Cap überschritten) — die tatsächlich für den Encode
  verwendete Ziel-Bitrate war bisher ungeprüft der rohe Katalogwert, auch
  wenn der über der (schon bekannten) Quell-Bitrate lag. Fix: neue
  `clampProfileToSource(profile, itemBitrateKbps)` — kappt `VideoKbps` auf
  die Quell-Bitrate, wenn die niedriger als der Katalogwert ist (nur die
  tatsächliche Encode-Bitrate, NICHT die `needsDownscale`-Entscheidung
  selbst, und NICHT der Cache-Dateiname — der bleibt beim gewählten
  Profil-ID). Test: `TestClampProfileToSource`.
  **🔴→✅ Zweite Runde, noch am selben Tag (convVersion 7, User meldete beim
  erneuten Test immer noch eine zu große Datei: 273 MB Original → 293 statt
  < 273 MB):** der erste Fix klemmte `profile.VideoKbps` direkt auf
  `itemBitrateKbps` — aber `it.BitrateKbps` (`Item.BitrateKbps`) kommt aus
  ffprobes `format.bit_rate` (`internal/scanner/scanner.go`), das ist die
  GESAMTE Container-Bitrate (Video **+** Audio), keine reine Video-Bitrate.
  Die neue, per Profil zugeteilte Audiospur kam dadurch oben drauf und hob
  die Summe wieder über die Quelle. Fix: `clampProfileToSource` zieht jetzt
  erst `profile.AudioKbps` von der Quell-Gesamtbitrate ab, bevor der Rest
  als Video-Zieldeckel dient (Sicherheits-Untergrenze 200 kbps gegen ein
  degeneriertes Ziel bei sehr niedriger Quell-Bitrate).

### Download & Löschen — Zeilen 3561–3579 der alten CLAUDE.md

  **🔴→✅ Audio-only-Dateien (Musik-Bibliotheken) schlugen mit `?compat=1`
  IMMER fehl (Bug, gefixt 2026-09-11, LIVE 1.3.10, User-Report über die
  neue Mac-App-Musik-Download-Funktion: "SOS" von ABBA Gold, eine ganz
  normale mp3, lieferte 500 "Stream map '0:V:0' matches no streams"):**
  dieses ganze Package ist auf VIDEO-Kompatibilität zugeschnitten (siehe
  Paket-Kommentar), die schnelle "ist eh schon passend"-Kurzentscheidung
  in `plan()` kannte aber nur den mp4/mov/m4v+h264+aac-Fall — jede Audio-
  Datei (mp3, m4a, flac, …) fiel dadurch immer in den Remux-Pfad, der
  bedingungslos `-map 0:V:0` setzt (Großbuchstabe, schließt Cover-Art
  bewusst aus, siehe `convVersion=5`-Historie) — bei einer Datei OHNE
  jeden Videostream matcht das nichts, ffmpeg bricht sofort ab. Der
  Browser hat das nie ausgelöst (fragt Musik-Downloads immer OHNE
  `?compat=1` an), der neue Mac-App-Musik-Download (siehe
  `project_feature_apple_music_player`-Memory) war der erste Aufrufer,
  der diesen Pfad für Audio überhaupt erreicht hat. Fix: `plan()` liefert
  jetzt `needsPrep=false` (Originaldatei direkt ausliefern) sobald
  `videoCodecHint == ""` (Scanner-Konvention "kein Videostream in der
  Datei") — Audio-Formate brauchen für den Download keine MP4-Remux-
  Behandlung, AVFoundation spielt mp3/m4a/aac nativ.

### Aktivitäts-Protokoll & Backup/Restore (seit 2026-09-02) — Zeilen 3709–3721 der alten CLAUDE.md

  - **🔴→✅ "play" wurde anfangs doppelt geloggt (Bug, gefixt noch am
    selben Tag, User-Report: "Jetzt habe ich aber 2x Wiedergabe gestartet
    im Protokoll stehen!"):** `GET /api/playback/{id}` dient ZWEI Zwecken —
    tatsächlicher Wiedergabe-Start UND reines Vorab-Laden der Stream-Liste
    fürs Detail-Dialog-Dropdown (Ton/Untertitel/Qualität). Ein automatisches
    Log direkt im GET-Handler feuerte für BEIDE Fälle — allein das Öffnen
    des Detail-Dialogs erzeugte schon einen Eintrag, tatsächliches
    Abspielen direkt danach einen zweiten. Fix: kein Auto-Log mehr im GET;
    stattdessen client-getriggertes `POST /api/playback/{id}/start`
    (`stream.go playbackStart`, exakt symmetrisch zu `stop`/`error`) — nur
    von den tatsächlichen Play-Auslösern aufgerufen (`player.js
    applyPlayback`, `music.js` Track-Start, `PlayerView.setUp`), NIE vom
    reinen Stream-Info-Prefetch.

### Verschieben in andere Ordner / Bibliotheken (seit 2026-07-12) — Zeilen 3953–3972 der alten CLAUDE.md

  **🔴 Eigentlicher Root Cause, gefunden beim ersten Live-Test (LIVE 1.2.46):**
  Verschieben tat schon VOR diesem Async-Umbau nichts — nicht "langsam",
  sondern ein stiller `TypeError` ganz am Funktionsanfang von
  `handleMoveSubmit`. `const submitBtn = e.target.querySelector('button[type="submit"]')`
  fand den Button nicht mehr, seit `normalizeModalLayout` (siehe „Dialoge
  (.modal) haben seit 2026-09-01 einen fixen Kopf + Fuß" oben) ihn beim
  ersten `showModal()` strukturell aus dem `<form>` heraus in einen
  separaten Footer verschiebt (bleibt nur über `form="moveForm"` verknüpft,
  submitted zwar weiterhin dasselbe Formular, ist aber kein Kind mehr davon).
  `submitBtn` war dadurch `null`, `submitBtn.disabled = true` warf sofort —
  die Funktion brach ab, BEVOR der `fetch()` je losging. Erklärt auch,
  warum die Server-Diagnose beim User-Report keinerlei Spur fand (weder
  CPU/IO noch `rename_history` noch `activity_log`): der Request wurde nie
  abgeschickt. Fix: `#moveSubmitBtn`-ID auf dem Button, Lookup per
  `$("#moveSubmitBtn")` statt `e.target.querySelector(...)` — unabhängig
  von der DOM-Restrukturierung. **Bekanntes Muster für JEDEN künftigen
  `.modal-flex`-Dialog:** ein Button-Lookup relativ zu `e.target`/`form`
  bricht, sobald `normalizeModalLayout` ihn aus dem Formular herauslöst —
  IMMER per ID/`document`-Lookup referenzieren, nie per
  `formElement.querySelector(...)`.

### Refactor-Serien im Volltext (ausgelagert aus CLAUDE.md, 2026-09-13)

Die vollständigen Schritt-für-Schritt-Protokolle der Code-Review 2026-09-06
und des Frontend-Modul-Splits 2026-04-30. Beide Serien sind abgeschlossen;
die daraus abgeleiteten Konventionen stehen in CLAUDE.md.

## Code-Review 2026-09-06 (Clean-Code/SOLID/Performance/Security, User-Auftrag)

User-Auftrag: vollständige Codebasis (Go-Backend + JS-Frontend) auf Lesbarkeit,
DRY/SOLID, Performance/Sicherheit, Fehlerbehandlung prüfen; Modularisierung
NUR intern (weitere Go-Dateien im selben Package bzw. weitere JS-Module) —
**explizit KEINE separaten Repos/Go-Module** (Goldfish bleibt bewusst Single-
Binary/Single-Container, siehe Projektbeschreibung oben).

**Sofort behobene, konkrete Funde (LIVE):**
- **cards.js:769 — XSS-Lücke:** `subtitle` (u. a. roher Musik-Artist-Tag)
  landete ungeschützt in `innerHTML`, während `title` an jeder anderen Stelle
  konsequent durch `escapeHTML()` läuft. Fix: `escapeHTML(subtitle)`.
- **cards.js `renderCard` — DRY-Verstoß**, selbst in dieser Session
  eingeführt (siehe "Kachel-Overlay"-Abschnitt oben, Custom-Metadaten-Titel-
  Fix): Titel-/Jahr-Override stand doppelt (posterPath-Zweig UND neuer
  Non-Poster-Zweig). Zusammengeführt zu einem einzigen, von der Bild-URL-
  Ermittlung entkoppelten Override-Block.
- **scanner.go — `lookupTag`-Fallstrick, selbst in dieser Session
  eingeführt:** `lookupTag` vergleicht ausschließlich gegen kleingeschriebene
  Keys (baut eine lowercase-Lookup-Map). Der neue Jahr-Tag-Aufruf übergab
  `"TYER", "TDRC"` in Großschreibung — hätten NIE gematcht. Auf `"tyer",
  "tdrc"` korrigiert.
- **biome-Autofixes (sicher, einzeln verifiziert):** `let`→`const` wo nie
  reassigned, `function(){}`→Arrow-Function wo kein `this` im Rumpf
  verwendet wird, unnötige Regex-Escapes. **Warnung für künftige Sessions:**
  `biome lint --write` NICHT blind vertrauen — die `noUnusedVariables`-Regel
  kennt das global-Window-Scope-Modulmuster dieses Projekts nicht und hätte
  (laut Diagnose-Vorschau, NICHT tatsächlich geschrieben) `appPrompt` in
  `_appPrompt` umbenannt — das hätte den globalen Aufruf aus anderen Modulen
  gebrochen. Jede vorgeschlagene Änderung einzeln gegen die Datei prüfen,
  bevor sie übernommen wird.

**Strukturanalyse (Ergebnis, noch NICHT umgesetzt — größerer Umbau, braucht
eigene Session(s) mit Tests nach jedem Schritt):**

*Backend, größter Kandidat `internal/store/sqlite.go` (3091 Zeilen):* passt
zum bereits etablierten Muster (`music.go`/`stats.go`/`users.go`/
`collections.go`/`introskip.go` sind schon eigene Dateien) — sqlite.go ist
der nie ausgelagerte Rest. Vorschlag: `schema.go` (migrate()-Funktion),
`items.go` (ListItems/UpsertItem/GetItemFor/CountItems/attachMetadata/
attachVariantCounts), `folders.go`, `metadata.go`, `trickplay_status.go`,
`settings.go`, `libraries.go` — reine Datei-Umzüge, keine Signatur-/API-
Änderung. Nebenfund: `attachMetadata`/`attachVariantCounts` schlucken
DB-Fehler komplett ohne Logging (`sqlite.go` ~1861/~1926) — sollten
mindestens `log.Printf` bekommen. `internal/enrich/worker.go` (1103 Zeilen):
`matchItem` (~185 Zeilen) ist die größte Einzelfunktion des Backends,
Kandidat für `matching.go`-Auslagerung. `internal/tmdb/client.go`,
`internal/api/tmdb.go`, `internal/api/items.go`, `internal/store/
collections.go`, `internal/scanner/scanner.go` wurden geprüft und sind
strukturell in Ordnung (je eine zusammenhängende Domäne, Größe kommt von
fachlicher Breite, nicht Vermischung) — keine Aufteilung nötig.

*Frontend, schärfster Einzelfund:* `grid.js loadItemsBody` ist **1206
Zeilen in einer einzigen Funktion** (fast die ganze Datei) — eine
If/Switch-Kette über alle Anzeige-Modi (Playlist/Home/Sammlungen/
Season-View/Musik/Standard). Läuft bei praktisch jeder Navigation, größter
Lesbarkeits-Hebel im gesamten Frontend. Vorschlag: Dispatcher +
`loadItemsForPlaylist`/`loadItemsForHome`/`loadItemsForSeasonView`/
`loadItemsForMusic`/`loadItemsDefault`. `views.js renderBreadcrumb`
(~480 Zeilen) ist eine ähnliche, kleinere God-Function. `player.js`
(2250 Zeilen) hat vier sauber abgrenzbare Unterthemen (Detail-Dialog,
Trickplay-Hover, Untertitel, Transcode-Seek/Buffer-Gate — letztere beiden
decken sich exakt mit eigenen CLAUDE.md-Abschnitten), Kandidaten für
`player-detail.js`/`player-trickplay.js`/`player-subtitles.js`/
`player-buffer.js`. `views.js`' Musik-Views (~530 Zeilen, `renderAlbumTiles`
bis `renderMusicTrackRow`) gehören fachlich zu `music.js`, nicht `views.js`.
`views.js`' Trickplay-Admin-Toolbar (Ende der Datei) ist fehlplatziert,
gehört zu `matching.js`, wo der Rest der Trickplay-Verwaltung schon liegt.
`admin.js` ist bereits klar organisiert, kein akuter Bedarf.

**Priorisierte Reihenfolge für einen künftigen Umbau** (Impact vs. Risiko):
1. ✅ **sqlite.go → schema.go** (LIVE 1.2.8) — `migrate()` (640 Zeilen,
   alle CREATE-TABLE/addCol) reine Funktionsverschiebung, keine Logik-
   /Signaturänderung. sqlite.go: 3091 → 2457 Zeilen. Nebenfund im selben
   Schritt: `attachMetadata`/`attachVariantCounts` schluckten DB-Fehler
   ohne jeden Kommentar — jetzt explizit als bewusstes Soft-Fail
   dokumentiert (Poster/×N-Badge fehlen dann einfach, kein harter Fehler).
   **Wichtige Korrektur zum ursprünglichen Plan:** `log.Printf` wurde
   NICHT ergänzt — das `store`-Package importiert nirgendwo `"log"`
   (Store-Methoden loggen grundsätzlich nie selbst, das ist Aufgabe der
   Aufrufer). Ein Logging-Import hier hätte diese Konvention gebrochen.
2. ✅ **grid.js loadItemsBody in benannte Handler zerlegen** (LIVE 1.2.9) —
   beim genauen Lesen waren es **16 Branches statt der ursprünglich
   angenommenen 7** (tpFailedView/homeView/currentPlaylist/playlistsRoot/
   collectionsView/personFilter/multiversion/simnames/suspicious/
   interlaced/duplicates/favoritesFlat/FLAT_SORTS-Library/music/
   Standard-Grid). 15 davon (alle außer der Staffel-Ansicht) wurden per
   Skript **mechanisch** (Python, kein manuelles Copy-Paste — Risiko einer
   Übertragungs-Fehlers bei ~1200 Zeilen war zu hoch) in verschachtelte
   `async function render<Name>()`-Funktionen extrahiert, `loadItemsBody`
   selbst ist jetzt ein reiner Dispatcher aus 15 `if (cond) return await
   render...();`-Zeilen. Verschachtelt (nicht Top-Level), damit sie
   `grid`/`stale`/`mySeq`/`lib`/`sort`/`matchMode`/`musicFlatLib`/
   `isMusicFlatLib`/`FLAT_SORTS`/`flatSort` weiterhin per Closure sehen —
   **keine einzige Parameter-Signatur geändert, keine Variable neu
   referenziert**, reine Textverschiebung. Verifiziert per Multiset-Diff
   (sortierte Zeilen alt vs. neu) — exakt nur die 15 geänderten
   Dispatch-Zeilen + 15 neue Funktions-Wrapper unterscheiden sich, sonst
   ist der Inhalt Zeile für Zeile identisch.
   **Staffel-Ansicht (Season-View, ~984–1027) bewusst NICHT extrahiert:**
   einziger Branch mit echtem Fallthrough (setzt bei fehlender
   Staffel-Struktur `state.pendingShowInfoHeader` und läuft dann WEITER in
   den Musik-Check und das Standard-Grid) — das passt nicht zum
   "if (cond) return await fn()"-Dispatcher-Muster, ohne die
   Fallthrough-Semantik selbst umzubauen (siehe „Automatischer Fallback
   bei fehlenden Staffel-Daten" oben). Bleibt zusammen mit der
   Bibliotheks-/Sort-Vorberechnung (`lib`/`sort`/`matchMode`) und den
   `FLAT_SORTS`/`musicFlatLib`-Konstanten inline im Dispatcher-Körper.
   `grid.js`: 1302 → 1340 Zeilen (mehr, nicht weniger — Funktions-Wrapper
   + Doku-Kommentar kosten Zeilen, der Lesbarkeits-Gewinn liegt in der
   Struktur, nicht in der Kürze). Live getestet: Home, Sammlungen,
   Playlist-Root + einzelne Playlist, Person-Filter, normales
   Filme/Serien-Grid, Staffel-Ansicht (Tatort — Fallback-Pfad),
   Musik-Album-Übersicht.
3. ✅ **sqlite.go → metadata.go + trickplay_status.go** (LIVE 1.2.10) —
   Grenzen diesmal nicht per Hand gesucht, sondern per kleinem Go-AST-Tool
   (`go/parser`, Zeilen-Offsets aller Top-Level-`FuncDecl`s inkl.
   Doc-Kommentar) exakt bestimmt — sicherer als Klammer-Zählen von Hand bei
   ~2500 Zeilen Go. `trickplay_status.go` (300 Zeilen): `SetTrickplayFolder`
   bis `ListTrickplayFolders` (13 Funktionen). `metadata.go` (443 Zeilen):
   `UpsertMetadata` bis `PendingFolders` (21 Funktionen) — `nullInt`/
   `nullTime` (generische Helper, keine Metadata-Spezifika) bleiben bewusst
   in sqlite.go. sqlite.go: 2457 → 1738 Zeilen. Verifiziert per Multiset-Diff
   (sortierte Zeilen alt vs. neu-3-Dateien-kombiniert) — einzige
   Unterschiede sind die neuen Datei-Header/Package/Import-Zeilen selbst,
   kein Code verloren oder verändert. `go build`/`go vet`/`go test ./...`
   grün. Reine Backend-Datei-Verschiebung ohne API-Auswirkung — kein
   Browser-Live-Test nötig (anders als Schritt 2), Test-Suite deckt
   `internal/store` ab.
4. ✅ **views.js Musik-Views → music.js** (LIVE 1.2.11) — Ziel war laut
   ursprünglichem Vorschlag ein neues `music-views.js`, aber es gibt
   bereits ein passendes Modul `music.js` (Mini-Player) in der
   Lade-Reihenfolge — Funktionen dort ergänzt statt eine weitere Datei
   + einen weiteren `<script>`-Tag anzulegen. Verschobener Block (529
   Zeilen, `renderAlbumTiles` bis `renderMusicTrackRow` inkl. der
   Spalten-Resize/Reorder-Helfer): views.js 1965 → 1435 Zeilen, music.js
   290 → 825 Zeilen. Ladereihenfolge-Unbedenklichkeit: alle Funktionen
   sind einfache globale `function`-Deklarationen (kein ES-Module), erst
   NACH `DOMContentLoaded`/`boot()` aufgerufen — zu dem Zeitpunkt haben
   alle `<script defer>`-Tags bereits ausgeführt, `music.js` lädt zwar
   nach `views.js`/`grid.js` in `index.html`, das spielt aber keine Rolle
   (bestehendes Projekt-Muster, kein Sonderfall). Verifiziert per
   Multiset-Diff (views.js+music.js alt vs. neu) — nur neue Kommentarzeilen
   unterscheiden sich. Live getestet: Musik-Album-Übersicht (Kacheln +
   Liste), Album-Detail-Tracklist, „Alle Titel", Spalten-Resize/Reorder.
5. ✅ **player.js → player-buffer.js/player-transcode-seek.js/player-trickplay.js**
   (LIVE 1.2.12) — drei klar abgrenzbare, per Kommentar-Header bereits
   vormarkierte Blöcke extrahiert: `player-trickplay.js` (108 Zeilen,
   Trickplay-Hover-Plugin inkl. seinem eigenen `trickplayState`-WeakMap),
   `player-transcode-seek.js` (212 Zeilen, `syncTranscodeDisplays` +
   `formatPlayerTime` + `attachSeekRestart` + `restartTranscodeAt`),
   `player-buffer.js` (479 Zeilen, Startpuffer-Gate + Pause-Prefetch +
   Buffer-Overlay inkl. `pausePrefetchTimer`/`pausePrefetchSeen`/
   `forcedDurationState`). Vor dem Schneiden alle modul-scoped
   `const`/`let`-Deklarationen (`grep -n "^const \|^let "`) durchsucht,
   um sicherzustellen, dass jede mit ihren tatsächlichen Nutzern in
   dieselbe neue Datei wandert (kein Modul-Grenzen-Bruch trotz weiterhin
   globalem window-Scope). player.js: 2250 → 1475 Zeilen. Drei neue
   `<script defer>`-Tags in `index.html` zwischen `player.js` und
   `admin.js` ergänzt (`go:embed all:web` fasst sie automatisch mit).
   Verifiziert per Multiset-Diff (nur neue Datei-Header unterscheiden
   sich). Live-Test im Browser bestätigte Detail-Dialog/Resume-Dialog/
   Modus-Dropdowns/Cast-Token-Fetch/Buffer-Overlay-Statuszeile/HLS-
   Playlist-Polling (`/api/transcode/…/progress` lief korrekt +342s→+812s
   hoch) — die eigentliche Pixel-Wiedergabe (`<video>` zeigt ein Bild)
   ließ sich in dieser Session NICHT verifizieren: `document
   .visibilityState` ist im claude-in-chrome-Tab dauerhaft `"hidden"`
   (bestätigt per `javascript_tool`, reproduziert in einem komplett
   frischen zweiten Tab, sowohl bei Direct Play als auch Transcode) —
   Chrome defers dadurch das eigentliche Laden der Videodatei komplett
   (`readyState=0`/`networkState=LOADING` für immer, **null** Netzwerk-
   Requests an `/api/stream/…` trotz korrekt gesetztem `<video src>` +
   `autoplay`). Bekannte, bereits an anderer Stelle in CLAUDE.md
   dokumentierte Umgebungseinschränkung (siehe „GoldfishTV"-Abschnitt:
   „document.visibilityState ist im claude-in-chrome-MCP-Tab 'hidden'"),
   kein Bug dieses Refactors — Beweis: der komplette Multiset-Diff zeigt
   nirgendwo eine inhaltliche Änderung an `vjs.src()`/Player-Erzeugung
   (die bleiben unverändert in player.js). Echte Pixel-Wiedergabe sollte
   bei Gelegenheit einmal vom User selbst im echten Browser gegengeprüft
   werden — nicht erneut per claude-in-chrome versuchen, das Ergebnis ist
   umgebungsbedingt vorhersagbar negativ.
6. ✅ **enrich/worker.go → matching.go** (LIVE 1.2.13) — der Ziel-Block war
   bereits ein einziger zusammenhängender, unveränderter Abschnitt
   (`enrichFolders`/`enrichItems`/`matchShow`/`matchItem`, Zeilen 262–533)
   ohne modul-scoped `var`/`const` (nur `type`-Deklarationen im
   Worker-struct, unberührt) — einfachster Schritt der ganzen Serie.
   worker.go: 1103 → 829 Zeilen, matching.go: 289 Zeilen. Verifiziert per
   Multiset-Diff (nur neue Datei-Header/Imports unterscheiden sich) +
   `go build`/`go vet`/`go test ./...` grün (kein `enrich`-Package-Test
   vorhanden, war schon vorher so). Reine Backend-Verschiebung ohne
   API-Auswirkung, kein Browser-Live-Test nötig (wie Schritt 3).
7. ✅ **Rest von sqlite.go → items.go/folders.go/libraries.go/settings.go**
   (LIVE 1.2.14, letzter Schritt der Serie) — die verbliebenen ~1700
   Zeilen waren KEIN sauber zusammenhängender Block mehr (Bibliotheks-
   und Item-Funktionen liegen verschachtelt, z. B. `CountItems`
   zwischen zwei `libraries`-Funktionen), daher per Skript anhand der
   go/parser-Zeilenbereiche in 4 thematische Buckets sortiert:
   `libraries.go` (239 Zeilen, 11 Funktionen: Bibliotheks-CRUD, Multi-
   Path, Sortierung), `items.go` (997 Zeilen, 20 Funktionen: Item-CRUD,
   `ListItems`-Hauptquery, Suche, `attachMetadata`/`attachVariantCounts`),
   `folders.go` (321 Zeilen, 8 Funktionen: TV-Top-Level-Folder-Navigation,
   Auto-Merge gleicher Show), `settings.go` (24 Zeilen, 2 Funktionen:
   Key-Value-Settings). `sqlite.go` selbst bleibt als schlanker Kern
   (204 Zeilen: `Store`-Struct, `Open`/`Close`, NATSORT-Collation-
   Registrierung) — von ursprünglich **3091 Zeilen zu Beginn dieser
   Modularisierungs-Serie auf 204 Zeilen**. Verifiziert per bereinigtem
   Multiset-Diff (Leerzeilen/Package-/Import-/Kommentarzeilen beidseitig
   rausgefiltert, dann sortiert verglichen) — **exakt null** übrig
   gebliebene Differenz, jede Import-Korrektur einzeln anhand echter
   Compiler-Fehler vorgenommen (nicht geraten). `go build`/`go vet`/
   `go test ./...` grün. Reine Backend-Verschiebung, kein Browser-Test
   nötig.
   **Damit ist die komplette 7-Punkte-Prioritätenliste der Code-Review
   2026-09-06 abgearbeitet** (Versionen 1.2.8 bis 1.2.14, jeder Schritt
   einzeln deployed + verifiziert, kein einziger Rollback nötig).

**Golint/biome-Bestandsaufnahme** (nicht alles behoben, nur dokumentiert):
`golangci-lint run ./...` fand 20 Funde (10 errcheck — meist unkritisches
`defer x.Close()`, 8 staticcheck-Stilhinweise, 2 unused: `scanner.go
musicExt` und `api/subtitle_gen.go maskKey` sind toter Code, vorbestehend).
`biome lint` über alle 19 JS-Module: 128× `noUnusedVariables`, 192×
`useOptionalChain`, 115× `useTemplate` (alles Stil, überwiegend
FIXABLE-aber-nicht-blind-anzuwenden, siehe Warnung oben), 33×
`noDoubleEquals` (`==`/`!=` statt `===`/`!==` — echte Typkoerzitions-
Risikoklasse, aber nicht pauschal automatisierbar, jede Stelle einzeln
prüfen). Kein akuter Handlungsbedarf, aber als Fundgrube für künftige
Aufräum-Sessions hier vermerkt.

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

### Erledigte TODOs (ausgelagert aus CLAUDE.md, 2026-09-13)

Beide Punkte waren abgehakt; die Dauerregel zum öffentlichen Repo steht
jetzt im Kopf von CLAUDE.md.

## TODO

- [x] **✅ Repo ist seit 2026-09-05 ÖFFENTLICH** (Open-Source, MIT-Lizenz) —
  `github.com/boernie77/goldfish`, verifiziert per `gh repo view` (`visibility:
  PUBLIC`). Modulpfad bewusst NICHT anonymisiert (war nur "optional" markiert,
  jetzt mit öffentlichem Repo unter demselben Pfad ohnehin hinfällig — eine
  Umbenennung wäre nur noch sinnlose Churn). Topics gesetzt: `golang`,
  `homelab`, `jellyfin-alternative`, `media-server`, `unraid`, `vaapi`, `go`,
  `self-hosted`, `sqlite`, `streaming`, `tmdb`. Vorbereitung (Lizenz/NOTICE.md,
  sanitisiertes CLAUDE.md, `.env.example`, `install.sh` E2E-getestet,
  Datenschutz-/Git-Historie-Check) war bereits vorher abgeschlossen, siehe
  Memory `project_installer_e2e_test.md`.
  **Konsequenz für jede künftige Session:** das Repo ist jetzt live-öffentlich
  einsehbar — bei JEDER Änderung zusätzlich zum bestehenden CLAUDE.md-
  Sanitisierungsstandard prüfen, ob committeter Code/Kommentare versehentlich
  echte Namen/E-Mails/interne IPs/Secrets enthalten (nicht mehr nur
  theoretisch relevant, sondern sofort für jeden sichtbar).

- [x] **✅ Echte Folgen-Zusammenführung für Serien in getrennten Ordnern**
  (LIVE 1.3.4, 2026-09-10) — User-Wunsch 2026-09-05 aufgegriffen, aber
  anders gelöst als ursprünglich skizziert: statt Dateien physisch zu
  verschieben (Jellyfin-Stil, hätte die Move-Infrastruktur wiederverwendet)
  wollte der User **ausdrücklich KEIN Verschieben auf Disk** — stattdessen
  virtuelles Multi-Folder-Browsing, siehe „Serienübersicht — Auto-Merge
  doppelter Serien-Ordner" oben (`MergedFolderNames`,
  `SeriesOwnedEpisodes(folders []string)`). Löst den konkreten
  Auslöser-Fall ("Two and a Half Men" mit S01/S02 in zwei physischen
  Ordnern) vollständig, ohne Dateisystem-Änderung. Ein UI zum manuellen
  "diese zwei Ordner gehören zusammen"-Markieren (für Fälle, die NICHT
  bereits über `folder_metadata.metadata_id` automatisch erkannt werden)
  ist damit weiterhin nicht gebaut — bisher kein konkreter Bedarf dafür,
  da die automatische Erkennung über die gemeinsame TMDB-Zuordnung den
  praktischen Fall abdeckt.


