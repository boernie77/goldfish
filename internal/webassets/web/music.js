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
};

function musicCurrentTrack() {
  return musicState.queue[musicState.idx] || null;
}

// musicPlayAlbum: startet Wiedergabe einer Track-Liste ab startIdx. Wird von
// cards.js beim Klick auf eine Musik-Kachel aufgerufen (statt openDetail()).
function musicPlayAlbum(tracks, startIdx) {
  musicState.queue = Array.isArray(tracks) ? tracks : [];
  musicState.idx = startIdx || 0;
  musicPlayCurrent();
}

async function musicPlayCurrent() {
  const t = musicCurrentTrack();
  if (!t) return;
  const bar = $("#miniPlayer");
  bar.classList.remove("hidden");
  $("#miniTitle").textContent = t.title || "";
  $("#miniArtist").textContent = t.artist || "";
  $("#miniCover").src = t.musicAlbumId ? `/api/poster/album/${t.musicAlbumId}` : "/placeholder.svg";
  $("#miniPrev").disabled = musicState.idx <= 0;
  $("#miniNext").disabled = musicState.idx >= musicState.queue.length - 1;

  let info;
  try {
    info = await api(`/api/playback/${t.id}`);
  } catch (e) {
    showToast(`Wiedergabe fehlgeschlagen: ${e.message}`, { kind: "error" });
    return;
  }
  const srcType = info.mode === "transcode" ? "application/vnd.apple.mpegurl" : "video/mp4";
  const vjs = musicEnsureVjs();
  vjs.src({ src: info.url, type: srcType });
  const pp = vjs.play();
  if (pp && typeof pp.catch === "function") pp.catch(() => {});
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
  vjs.on("play", updateMiniPlayPauseIcon);
  vjs.on("pause", updateMiniPlayPauseIcon);
  vjs.on("timeupdate", () => {
    const seek = $("#miniSeek");
    const dur = vjs.duration();
    if (seek && dur && isFinite(dur)) {
      seek.value = String((vjs.currentTime() / dur) * 100);
    }
  });
  musicState.vjs = vjs;
  return vjs;
}

function musicNext() {
  if (musicState.idx + 1 < musicState.queue.length) {
    musicState.idx++;
    musicPlayCurrent();
  }
}

function musicPrev() {
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
  const btn = $("#miniPlayPause");
  if (!btn || !musicState.vjs) return;
  btn.textContent = musicState.vjs.paused() ? "▶" : "⏸";
}

function musicCloseBar() {
  if (musicState.vjs) {
    try { musicState.vjs.pause(); } catch {}
  }
  musicState.queue = [];
  musicState.idx = -1;
  $("#miniPlayer").classList.add("hidden");
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
