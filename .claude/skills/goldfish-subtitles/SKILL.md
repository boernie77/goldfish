---
name: goldfish-subtitles
description: "Use when working on Goldfish subtitles: Whisper AI subtitles, OCR image subtitles, sidecar subtitle files, subtitle selector handler."
metadata:
  project: Goldfish (boernie77/goldfish)
  source: "CLAUDE.md-Aufteilung 2026-09-20"
---

# goldfish-subtitles

Aus der früheren Sammel-CLAUDE.md des Goldfish-Repos ausgelagerter Themenbereich (Zeichen: 11315, Sektionen: 4). Volltext des Originals: Skill `goldfish-full-archive`.

## Harte Regeln (zuerst lesen)

- #subSelect darf nur EINEN Change-Handler haben (sonst Doppel-Trigger).

---

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

