// music.js — persistenter Mini-Player für Musik-Bibliotheken (kind=music,
// seit 2026-09-04, User-Anfrage: "persistenter Mini-Player, der über
// Seitennavigation hinweg weiterläuft, wie Spotify/YouTube Music").
//
// Reihenfolge in index.html: ... ocrsub → music → app
//
// Bewusst GETRENNT von player.js' state.playQueue/playQueueIdx (Video-Modal-
// gebunden, wird bei disposePlayer() verworfen). #miniPlayer sitzt als
// Geschwister von #playerDialog im DOM, AUSSERHALB von #grid — überlebt
// dadurch jeden loadItems()/View-Wechsel unverändert.
//
// Media-Element ist ein EIGENER Video.js-Player (nicht state.vjs!) auf einem
// unsichtbaren <video>-Element — für HLS-Transcode-Support bei flac/wav via
// VHS, exakt wie der normale Player. mp3/aac/vorbis/opus (die meisten Dateien)
// spielen ohnehin direkt, HLS wird nur für die Transcode-Fälle gebraucht.

const musicState = {
  queue: [],
  idx: -1,
  vjs: null,
  // playSeq: Sequenz-Token gegen überlappende Play-Anfragen (Doppelklick auf
  // eine Kachel feuert ZWEI "click"-Events, jedes startet einen eigenen
  // musicPlayCurrent()-Aufruf; ohne Guard konnte die zuerst gestartete, aber
  // später auflösende Anfrage die zweite überschreiben — Video.js bricht den
  // laufenden play()/src()-Vorgang dann mit einem stillschweigend verschluckten
  // AbortError ab, das Ergebnis war "spielt oft gar nicht bzw. sehr verzögert"
  // (User-Bericht 2026-09-04). Nur der jeweils NEUESTE Aufruf darf noch
  // src()/play() ausführen.
  playSeq: 0,
  loading: false,
};

function musicCurrentTrack() {
  return musicState.queue[musicState.idx] || null;
}

// musicDirectMimeType: echter MIME-Type für Direct-Play-Audiodateien, nach
// Container (siehe scanner.probeItem/model.Item.Container). NICHT
// "video/mp4" wie beim Hauptplayer — das ist dort korrekt (echte mp4-Video-
// Dateien), für Musik-Direct-Play aber schlicht falsch.
function musicDirectMimeType(container) {
  const map = {
    mp3: "audio/mpeg",
    m4a: "audio/mp4",
    m4b: "audio/mp4",
    mp4: "audio/mp4",
    aac: "audio/aac",
    ogg: "audio/ogg",
    opus: "audio/ogg",
    wav: "audio/wav",
    flac: "audio/flac",
  };
  return map[(container || "").toLowerCase()] || "audio/mpeg";
}

// musicPlayAlbum: startet Wiedergabe einer Track-Liste ab startIdx. Wird von
// cards.js beim Klick auf eine Musik-Kachel aufgerufen (statt openDetail()).
function musicPlayAlbum(tracks, startIdx) {
  // Explizite Album-/Titel-Auswahl beendet einen laufenden Zufallsmodus
  // (gleiche Konvention wie player.js openPlayer() ohne opts.fromShuffle) —
  // sonst würde ⏮/⏭ danach still auf Zufalls-Navigation umschalten.
  state.shuffleMode = false;
  state.shuffleHistory = [];
  state.shuffleIdx = -1;
  musicState.queue = Array.isArray(tracks) ? tracks : [];
  musicState.idx = startIdx || 0;
  musicPlayCurrent();
}

// musicPlayShuffleTrack: von playRandom/shuffleNext/shufflePrev (playlists.js)
// aufgerufen, wenn das gezogene Zufalls-Item aus einer Musik-Bibliothek
// stammt (User-Bericht 2026-09-04: "Zufalls-Play öffnet das große
// Playerfenster statt den Musikplayer" — bisher rief playRandom
// ausnahmslos openPlayer() auf). Im Unterschied zu musicPlayAlbum wird
// state.shuffleMode dabei NICHT zurückgesetzt — playlists.js hat es gerade
// erst gesetzt, das ⏮/⏭ am Mini-Player soll während des Zufallsmodus
// weiterhin shufflePrev()/shuffleNext() ansteuern (siehe musicNext/musicPrev).
function musicPlayShuffleTrack(item) {
  musicState.queue = [item];
  musicState.idx = 0;
  musicPlayCurrent();
}

async function musicPlayCurrent() {
  const t = musicCurrentTrack();
  if (!t) return;
  const seq = ++musicState.playSeq;
  // "Zuletzt abgespielt"-Timestamp setzen, sobald der Titel STARTET — exakt
  // dasselbe Verhalten wie beim Video-Player (player.js openPlayer), NICHT
  // erst bei vollständigem Durchhören.
  api(`/api/items/${t.id}/played`, { method: "POST" }).catch(() => {});
  const bar = $("#miniPlayer");
  bar.classList.remove("hidden");
  document.body.classList.add("has-mini-player");
  $("#miniTitle").textContent = t.title || "";
  $("#miniArtist").textContent = t.artist || "";
  $("#miniCover").src = t.musicAlbumId ? `/api/poster/album/${t.musicAlbumId}` : "/placeholder.svg";
  $("#miniSeek").value = "0";
  $("#miniTime").textContent = `0:00 / ${t.durationSec ? fmtDuration(t.durationSec) : "--:--"}`;
  // Im Zufallsmodus (state.shuffleMode) besteht die "Queue" hier immer nur
  // aus dem einen aktuell gezogenen Titel — Zurück/Weiter richten sich dann
  // nach der Zufalls-History (state.shuffleHistory/-Idx), nicht nach
  // musicState.queue.length (siehe musicNext/musicPrev).
  if (state.shuffleMode) {
    $("#miniPrev").disabled = state.shuffleIdx <= 0;
    $("#miniNext").disabled = false;
  } else {
    $("#miniPrev").disabled = musicState.idx <= 0;
    $("#miniNext").disabled = musicState.idx >= musicState.queue.length - 1;
  }
  // Sofortiges Feedback statt eines stillen, unklaren Wartens (User-Bericht
  // 2026-09-04: "Ich weiß immer nicht, ob er was abspielen wird. Der hat
  // teilweise lange Verzögerungen.") — der Play/Pause-Button zeigt ab hier
  // ein Ladesymbol, bis der Stream WIRKLICH läuft (vjs "playing"-Event,
  // siehe musicEnsureVjs) oder ein Fehler auftritt. `data-loading` statt nur
  // einer CSS-Klasse, damit updateMiniPlayPauseIcon weiß, dass es die
  // Play/Pause-Anzeige NICHT überschreiben soll, solange geladen wird.
  musicState.loading = true;
  setMiniLoading(true);

  let info;
  try {
    info = await api(`/api/playback/${t.id}`);
  } catch (e) {
    if (seq !== musicState.playSeq) return; // inzwischen durch neueren Klick überholt
    musicState.loading = false;
    setMiniLoading(false);
    showToast(`Wiedergabe fehlgeschlagen: ${e.message}`, { kind: "error" });
    return;
  }
  if (seq !== musicState.playSeq) return; // stale — ein neuerer Track wurde inzwischen angefordert
  // 🔴 Fund 2026-09-04 (User-Screenshot: "The media could not be loaded …
  // because the format is not supported"): Direct-Play-Tracks wurden IMMER
  // mit type="video/mp4" an Video.js übergeben — kopiert vom Hauptplayer
  // (player.js), der ausschließlich echte mp4/mov-Videos direkt abspielt.
  // Musik-Direct-Play ist aber mp3/aac/ogg/opus, NIE ein mp4-Video-Container
  // — der falsche MIME-Type-Hint ließ den Browser die Wiedergabe teils
  // sofort ablehnen, teils (je nach Tech-Fallback-Reihenfolge) erst nach
  // mehreren Sekunden Retry doch noch starten (erklärt vermutlich auch die
  // beobachtete 10-15s-Verzögerung).
  const srcType = info.mode === "transcode" ? "application/vnd.apple.mpegurl" : musicDirectMimeType(t.container);
  const vjs = musicEnsureVjs();
  // vjs.ready(): bei einer FRISCH erzeugten Video.js-Instanz ist die Tech
  // (Html5) direkt nach dem Konstruktor-Aufruf noch nicht initialisiert —
  // ein sofortiges src()/play() konnte dadurch beim allerersten Titel nach
  // dem Laden der Seite lautlos ins Leere laufen. ready() feuert sofort,
  // wenn der Player schon bereit ist (Normalfall bei Track-Wechseln),
  // sonst erst sobald die Tech steht.
  vjs.ready(() => {
    if (seq !== musicState.playSeq) return; // inzwischen überholt
    vjs.src({ src: info.url, type: srcType });
    const pp = vjs.play();
    if (pp && typeof pp.catch === "function") {
      pp.catch(err => {
        if (seq !== musicState.playSeq) return;
        console.warn("[music] Wiedergabe blockiert/abgebrochen:", err);
        musicState.loading = false;
        setMiniLoading(false);
        showToast("Wiedergabe konnte nicht gestartet werden", { kind: "error" });
      });
    }
  });
}

// musicEnsureVjs: erzeugt den unsichtbaren Video.js-Player einmalig (lazy,
// beim ersten Abspielen — nicht schon beim Booten, das würde unnötig eine
// Video.js-Instanz für Nutzer ohne Musik-Bibliothek anlegen).
function musicEnsureVjs() {
  if (musicState.vjs) return musicState.vjs;
  const el = $("#miniAudio");
  const vjs = window.videojs(el, {
    autoplay: false,
    controls: false,
    preload: "auto",
    fluid: false,
    responsive: false,
    liveui: false, // wie im Hauptplayer: unsere HLS-Transcodes sind technisch "live" (kein ENDLIST)
  });
  vjs.on("ended", musicNext);
  // "playing" statt "play": "play" feuert schon beim BLOSSEN Anfordern der
  // Wiedergabe (kann bei langsamem Netz/Transcode-Start Sekunden vor dem
  // ersten hörbaren Ton liegen), "playing" erst wenn tatsächlich Audio läuft
  // — das eigentliche Signal, um das Ladesymbol zu beenden.
  vjs.on("playing", () => { musicState.loading = false; setMiniLoading(false); });
  vjs.on("waiting", () => setMiniLoading(true));
  vjs.on("pause", updateMiniPlayPauseIcon);
  vjs.on("error", () => {
    musicState.loading = false;
    setMiniLoading(false);
    const err = vjs.error();
    showToast(`Wiedergabefehler: ${(err && err.message) || "unbekannt"}`, { kind: "error" });
  });
  // Zeitanzeige "M:SS / M:SS" — fehlte bisher komplett (kein Element im Markup,
  // User-Bericht 2026-09-05: "Der Player zeigt keine Zeit an. Weder Titellänge
  // noch die Position"). vjs.duration() ist bei HLS-Transcode anfangs oft noch
  // NaN/Infinity (wachsende EVENT-Playlist, siehe Hauptplayer-Pendant
  // forcePlayerDuration) — Fallback auf die vom Server bekannte durationSec
  // des aktuellen Tracks, bis vjs selbst eine reale Zahl liefert.
  vjs.on("timeupdate", () => {
    const seek = $("#miniSeek");
    const t = musicCurrentTrack();
    const vd = vjs.duration();
    const dur = (isFinite(vd) && vd > 0) ? vd : ((t && t.durationSec) || 0);
    const cur = vjs.currentTime() || 0;
    if (seek && dur) seek.value = String((cur / dur) * 100);
    const timeEl = $("#miniTime");
    if (timeEl) timeEl.textContent = `${fmtDuration(cur)} / ${dur ? fmtDuration(dur) : "--:--"}`;
  });
  musicState.vjs = vjs;
  return vjs;
}

function musicNext() {
  // Zufallsmodus: ⏭ am Mini-Player soll dasselbe tun wie der ⏭-Button des
  // großen Players im Zufallsmodus — nächstes Zufalls-Item ziehen (oder in
  // der bereits besuchten History vorwärtsblättern), nicht die (hier nur
  // 1 Element lange) musicState.queue durchgehen.
  if (state.shuffleMode) { shuffleNext(); return; }
  if (musicState.idx + 1 < musicState.queue.length) {
    musicState.idx++;
    musicPlayCurrent();
  }
}

function musicPrev() {
  if (state.shuffleMode) { shufflePrev(); return; }
  if (musicState.idx > 0) {
    musicState.idx--;
    musicPlayCurrent();
  }
}

function musicTogglePlay() {
  const vjs = musicState.vjs;
  if (!vjs) return;
  if (vjs.paused()) {
    const pp = vjs.play();
    if (pp && typeof pp.catch === "function") pp.catch(() => {});
  } else {
    vjs.pause();
  }
}

function updateMiniPlayPauseIcon() {
  if (musicState.loading) return; // Ladesymbol hat Vorrang, siehe setMiniLoading
  const btn = $("#miniPlayPause");
  if (!btn || !musicState.vjs) return;
  btn.textContent = musicState.vjs.paused() ? "▶" : "⏸";
}

// setMiniLoading: zeigt/versteckt das Ladesymbol auf dem Play/Pause-Button
// und sperrt ⏮/⏭/Play während des Ladens — verhindert sowohl das "weiß
// nicht ob er spielt"-Gefühl (klares Signal: da tut sich was) als auch
// Doppelklicks, die früher die playSeq-Race auslösten.
function setMiniLoading(loading) {
  const btn = $("#miniPlayPause");
  if (btn) {
    btn.textContent = loading ? "⏳" : (musicState.vjs && !musicState.vjs.paused() ? "⏸" : "▶");
    btn.disabled = loading;
  }
  $("#miniPlayer").classList.toggle("is-loading", loading);
}

function musicCloseBar() {
  if (musicState.vjs) {
    try { musicState.vjs.pause(); } catch {}
  }
  musicState.queue = [];
  musicState.idx = -1;
  state.shuffleMode = false;
  state.shuffleHistory = [];
  state.shuffleIdx = -1;
  $("#miniPlayer").classList.add("hidden");
  document.body.classList.remove("has-mini-player");
}

// initMiniPlayer: einmalig aus boot() (app.js) aufgerufen.
function initMiniPlayer() {
  $("#miniPlayPause").addEventListener("click", musicTogglePlay);
  $("#miniNext").addEventListener("click", musicNext);
  $("#miniPrev").addEventListener("click", musicPrev);
  $("#miniClose").addEventListener("click", musicCloseBar);
  $("#miniSeek").addEventListener("input", (e) => {
    const vjs = musicState.vjs;
    const dur = vjs && vjs.duration();
    if (vjs && dur && isFinite(dur)) {
      vjs.currentTime((Number(e.target.value) / 100) * dur);
    }
  });
}
