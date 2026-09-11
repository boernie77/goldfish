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
  // Protokoll-Start-Log (Bug-Fix 2026-09-11, siehe player.js applyPlayback-
  // Kommentar) — Musik hatte vorher GAR kein explizites Start-Log (der
  // frühere automatische Log am GET-Endpoint traf zufällig auch hier zu,
  // ist aber jetzt entfernt).
  api(`/api/playback/${t.id}/start`, { method: "POST" }).catch(() => {});
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

// --- Aus views.js verschoben (Schritt 4 der Modularisierung,
// siehe CLAUDE.md "Code-Review 2026-09-06") -- fachlich Musik-
// Views, nicht Video/Serien-Views. Reine Verschiebung, keine
// Logikaenderung. ---
// --- Musik-Bibliotheken (seit 2026-09-04) ---
// Album-Kacheln im Library-Root, Track-Liste beim Öffnen eines Albums.
// Analog zur Staffel-Ansicht oben, aber ohne Toggle — die normale Ordner-
// Navigation bleibt für Unterordner unverändert nutzbar (siehe grid.js: der
// Musik-Zweig greift nur bei state.currentFolder === null).

function renderAlbumTiles(grid, albums, listView, searchActive) {
  grid.innerHTML = "";
  document.body.classList.remove("has-alpha-sidebar");
  const bar = $("#alphaSidebar"); if (bar) bar.classList.add("hidden");
  if (!albums.length) {
    grid.innerHTML = searchActive
      ? `<div class="empty">Keine Alben mit Treffern.</div>`
      : `<div class="empty">Keine Alben gefunden. Klicke „⟳ Scan" um die Bibliothek einzulesen.</div>`;
    return;
  }
  if (listView) {
    grid.classList.add("track-list-grid");
    const list = document.createElement("div");
    list.className = "track-list";
    // Kopfzeile + Resize/Reorder seit 2026-09-11 (User-Report: "Hier fehlen
    // die Überschriften der Spalten und die Spalten sind nicht verschiebbar,
    // so wie bei den Titeln") — dieselbe Infrastruktur wie die Track-Listen
    // (Album-Detail/"Alle Titel"), nur mit eigenem Kontext "overview" und
    // eigenem Zeilen-Renderer (renderAlbumRow), weil Alben andere Felder
    // haben als Tracks (kein trackNo/Dauer, dafür Titelzahl).
    const rerenderRows = () => {
      list.innerHTML = "";
      const head = renderMusicColumnHeader("overview", list);
      head.classList.add("track-row--album"); // CSS-Fallback-Grid-Template teilen
      const columns = musicEffectiveColumns("overview");
      for (const a of albums) list.appendChild(renderAlbumRow(a, columns));
      applyMusicGridTemplate(list, "overview");
    };
    musicColumnHeaderRefreshers.set(list, rerenderRows);
    rerenderRows();
    grid.appendChild(list);
    state.lastRenderedItems = [];
    return;
  }
  grid.classList.remove("track-list-grid");
  const frag = document.createDocumentFragment();
  for (const a of albums) {
    const el = document.createElement("article");
    el.className = "card folder card--square";
    el.tabIndex = 0;
    el.setAttribute("role", "button");
    const cover = a.coverSource ? `/api/poster/album/${a.id}` : "/placeholder.svg";
    el.innerHTML = `
      <div class="thumb">
        <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${cover}">
        <button type="button" class="fav-toggle ${a.favorite ? "is-on" : ""}" title="${a.favorite ? "Album aus Favoriten entfernen" : "Album zu Favoriten hinzufügen"}" data-toggle-album-fav aria-label="${a.favorite ? "Favorit" : "Kein Favorit"}">${a.favorite ? "♥" : "♡"}</button>
        <span class="folder-count">${a.trackCount || 0} Titel</span>
      </div>
      <div class="card-body">
        <div class="card-title" title="${escapeHTML(a.album || "")}">${escapeHTML(a.album || "(Unbekanntes Album)")}</div>
        <div class="card-meta"><span>${escapeHTML(a.artist || "")}</span>${a.genre ? `<span>${escapeHTML(a.genre)}</span>` : ""}</div>
      </div>
    `;
    el.addEventListener("click", (ev) => {
      const favBtn = ev.target && ev.target.closest("[data-toggle-album-fav]");
      if (favBtn) { ev.stopPropagation(); toggleAlbumFavorite(a, favBtn); return; }
      openMusicAlbum(a.id);
    });
    el.addEventListener("keydown", e => { if (e.key === "Enter") el.click(); });
    frag.appendChild(el);
  }
  grid.appendChild(frag);
  state.lastRenderedItems = [];
}

// toggleAlbumFavorite: Album-Favorit ist eine EIGENE Server-Ressource
// (user_music_album_favorites), nicht dasselbe wie ein Track-Favorit —
// User-Anfrage 2026-09-04: "Favoriten will ich für Alben als auch für
// einzelne Songs erstellen können".
async function toggleAlbumFavorite(album, btn) {
  const newState = !album.favorite;
  btn.disabled = true;
  try {
    await api(`/api/albums/${album.id}/favorite`, {
      method: "PUT",
      body: JSON.stringify({ favorite: newState }),
    });
    album.favorite = newState;
    btn.classList.toggle("is-on", newState);
    btn.textContent = newState ? "♥" : "♡";
    btn.title = newState ? "Album aus Favoriten entfernen" : "Album zu Favoriten hinzufügen";
  } catch (e) {
    appAlert(e.message);
  } finally {
    btn.disabled = false;
  }
}

// Album-Metadaten bearbeiten (Admin) — kaskadiert auf ALLE Titel des Albums
// (Store.UpdateMusicAlbumMetadata), User-Wunsch 2026-09-11: "Wenn ich beim
// Album das Jahr eintrage, soll es auch für die Titel übernommen werden."
// Eigener Dialog/Speicherpfad statt des Track-Edit-Dialogs — music_albums
// selbst ist nur eine Aggregat-Tabelle (siehe GroupMusicAlbums), Album-
// Felder existieren serverseitig nicht separat von den Track-Feldern.
function openEditAlbumMetaDialog(album) {
  state.currentEditAlbum = album;
  const f = $("#editAlbumMetaForm");
  f.album.value = album.album || "";
  f.artist.value = album.artist || "";
  f.genre.value = album.genre || "";
  f.year.value = album.year || "";
  $("#editAlbumMetaDialog").showModal();
}

async function handleEditAlbumMetaSubmit(e) {
  e.preventDefault();
  const album = state.currentEditAlbum;
  if (!album) return;
  const f = e.target;
  const body = {
    artist: f.artist.value.trim(),
    album: f.album.value.trim(),
    genre: f.genre.value.trim(),
    year: f.year.value ? Number(f.year.value) : 0,
  };
  try {
    await api(`/api/albums/${album.id}/metadata`, { method: "PUT", body: JSON.stringify(body) });
    $("#editAlbumMetaDialog").close();
    invalidateItemsCache();
    loadItems();
    showToast("Album-Metadaten gespeichert", { kind: "success" });
  } catch (err) {
    appAlert("Fehler: " + err.message);
  }
}

// renderAlbumRow: Zeilen-Renderer für die Album-Übersicht als Liste
// (analog renderMusicTrackRow für Track-Listen, aber mit Album-eigenen
// Feldern — kein trackNo/Dauer, dafür Titelzahl). `columns` kommt aus
// musicEffectiveColumns("overview") und steuert Inhalt + Reihenfolge.
function renderAlbumRow(a, columns) {
  const cover = a.coverSource ? `/api/poster/album/${a.id}` : "/placeholder.svg";
  const row = document.createElement("div");
  row.className = "track-row track-row--album";
  row.tabIndex = 0;
  row.setAttribute("role", "button");
  let html = "";
  for (const col of columns) {
    switch (col) {
      case "cover":
        html += `<img class="track-row-cover" loading="lazy" decoding="async" alt="" src="${cover}">`;
        break;
      case "title":
        html += `<span class="track-row-title" title="${escapeHTML(a.album || "")}">${escapeHTML(a.album || "(Unbekanntes Album)")}</span>`;
        break;
      case "artist":
        html += `<span class="track-row-artist">${escapeHTML(a.artist || "")}</span>`;
        break;
      case "genre":
        html += `<span class="track-row-genre">${escapeHTML(a.genre || "")}</span>`;
        break;
      case "count":
        html += `<span class="track-row-count">${a.trackCount || 0} Titel</span>`;
        break;
      case "fav":
        html += `<button type="button" class="fav-toggle track-row-fav ${a.favorite ? "is-on" : ""}" title="${a.favorite ? "Album aus Favoriten entfernen" : "Album zu Favoriten hinzufügen"}" data-toggle-album-fav>${a.favorite ? "♥" : "♡"}</button>`;
        break;
    }
  }
  row.innerHTML = html;
  row.addEventListener("click", (ev) => {
    const favBtn = ev.target && ev.target.closest("[data-toggle-album-fav]");
    if (favBtn) { ev.stopPropagation(); toggleAlbumFavorite(a, favBtn); return; }
    openMusicAlbum(a.id);
  });
  row.addEventListener("keydown", e => { if (e.key === "Enter") row.click(); });
  return row;
}

// openMusicAlbum: öffnet ein Album aus der Übersicht (Kachel- oder
// Listenansicht, auch aus gefilterten Suchtreffern). Leert dabei das
// Suchfeld — sonst würde ein noch aktiver Künstler-/Album-Suchbegriff (der
// NICHT zwangsläufig in den Track-TITELN vorkommt) beim Öffnen des Albums
// fälschlich als "keine Titel gefunden"-Filter weiterwirken (User-Bericht
// 2026-09-04: Album zeigte "Keine Titel in diesem Album", obwohl die Kachel
// zuvor die korrekte Trackzahl anzeigte).
function openMusicAlbum(albumId) {
  const si = $("#searchInput"); if (si) si.value = "";
  state.currentAlbum = albumId;
  loadItems();
}

function renderAlbumTracks(grid, data, listView) {
  grid.innerHTML = "";
  // War fest auf "remove" gesetzt — dadurch blieb #grid im normalen
  // CSS-Grid-Layout (Kachel-Spaltenbreite), jede .track-row landete als
  // EIN Grid-Item in einer schmalen Spalte statt über die volle Breite zu
  // laufen (User-Bericht 2026-09-04: Listenansicht zeigte nur "Au..." statt
  // des vollen Titels — reine Folge der zu schmalen Spalte, nicht der
  // Textkürzung selbst).
  grid.classList.toggle("track-list-grid", !!listView);
  document.body.classList.remove("has-alpha-sidebar");
  const bar = $("#alphaSidebar"); if (bar) bar.classList.add("hidden");
  const album = data.album || {};
  const tracks = data.tracks || [];
  const header = document.createElement("section");
  // "album-header-sticky" NUR in der Listenansicht (User-Wunsch 2026-09-04:
  // "hätte ich gerne den Header mit Album und Künstler fix") — bei langen
  // Tracklisten soll man beim Scrollen weiterhin sehen, in welchem Album man
  // ist. Eigene Modifier-Klasse statt die geteilte .show-header direkt
  // anzufassen, damit die TV-Staffel-Ansicht (nutzt dieselbe Basis-Klasse)
  // unverändert bleibt.
  header.className = "show-header detail-wrap" + (listView ? " album-header-sticky" : "");
  const cover = album.coverSource ? `/api/poster/album/${album.id}` : "/placeholder.svg";
  header.innerHTML = `
    <div class="detail-poster" style="background-image:url('${cover}')"></div>
    <div class="detail-body">
      <button type="button" class="link-btn" id="albumBackBtn" title="Zurück zur Album-Übersicht">←</button>
      <h2>${escapeHTML(album.album || "")}
        <button type="button" class="fav-toggle-inline ${album.favorite ? "is-on" : ""}" id="albumFavBtn" title="${album.favorite ? "Album aus Favoriten entfernen" : "Album zu Favoriten hinzufügen"}">${album.favorite ? "♥" : "♡"}</button>
        ${(state.me && state.me.isAdmin) ? `<button type="button" class="link-btn" id="albumEditMetaBtn" title="Album-Metadaten bearbeiten">✏</button>` : ""}
      </h2>
      <div class="sub"><span>${escapeHTML(album.artist || "")}</span>${album.year ? `<span>${album.year}</span>` : ""}${album.genre ? `<span>${escapeHTML(album.genre)}</span>` : ""}</div>
    </div>
  `;
  grid.appendChild(header);
  header.querySelector("#albumBackBtn").addEventListener("click", () => {
    state.currentAlbum = null;
    loadItems();
  });
  header.querySelector("#albumFavBtn").addEventListener("click", (ev) => toggleAlbumFavorite(album, ev.currentTarget));
  const editMetaBtn = header.querySelector("#albumEditMetaBtn");
  if (editMetaBtn) editMetaBtn.addEventListener("click", () => openEditAlbumMetaDialog(album));
  if (!tracks.length) {
    const e = document.createElement("div");
    e.className = "empty";
    e.textContent = "Keine Titel in diesem Album.";
    grid.appendChild(e);
    return;
  }
  state.playQueue = tracks;
  state.lastRenderedItems = tracks;
  if (listView) {
    const list = document.createElement("div");
    list.className = "track-list";
    // "artist" seit 2026-09-06 ergänzt (User-Wunsch) — bei Compilations/
    // Musicals mit "Verschiedene Interpreten" als Album-Artist (siehe
    // Store.GroupMusicAlbums) ist der TATSÄCHLICHE Interpret pro Track sonst
    // nirgends in dieser Liste sichtbar. Spalten Titel/Künstler/Dauer sind
    // seit 2026-09-06 breiten-/reihenfolge-verschiebbar (musicColumns:album).
    const rerenderRows = () => {
      list.innerHTML = "";
      renderMusicColumnHeader("album", list);
      const columns = musicEffectiveColumns("album");
      tracks.forEach((it, idx) => list.appendChild(renderMusicTrackRow(it, tracks, idx, columns)));
      applyMusicGridTemplate(list, "album");
    };
    musicColumnHeaderRefreshers.set(list, rerenderRows);
    rerenderRows();
    grid.appendChild(list);
    return;
  }
  const frag = document.createDocumentFragment();
  tracks.forEach((it, idx) => frag.appendChild(renderCard(it, { queueIdx: idx })));
  grid.appendChild(frag);
}

// renderAllTracksList: flache Liste ALLER Titel einer Musik-Bibliothek
// (Künstler/Album/Titel/letzte Wiedergabe) — bewusst NUR als Liste, keine
// Kachel-Variante (User-Wunsch 2026-09-04: eine Übersicht, um z. B. per
// letzter Wiedergabe zu sortieren; als Kacheln wäre das unübersichtlich).
function renderAllTracksList(grid, tracks) {
  grid.innerHTML = "";
  grid.classList.add("track-list-grid");
  document.body.classList.remove("has-alpha-sidebar");
  const bar = $("#alphaSidebar"); if (bar) bar.classList.add("hidden");
  if (!tracks.length) {
    grid.innerHTML = `<div class="empty">Keine Titel in dieser Bibliothek.</div>`;
    return;
  }
  state.playQueue = tracks;
  state.lastRenderedItems = tracks;
  const list = document.createElement("div");
  list.className = "track-list track-list--all";
  // "fav": Favoriten-Herz auch in der flachen Liste — war hier bisher die
  // einzige Musik-Ansicht ohne Möglichkeit, einen einzelnen Titel zu
  // favorisieren (User-Anfrage 2026-09-04). Spalten Titel/Künstler/Album/
  // Zuletzt gehört sind seit 2026-09-06 breiten-/reihenfolge-verschiebbar
  // (musicColumns:all, siehe MUSIC_LIST_CONTEXTS).
  const rerenderRows = () => {
    list.innerHTML = "";
    renderMusicColumnHeader("all", list);
    const columns = musicEffectiveColumns("all");
    tracks.forEach((it, idx) => list.appendChild(renderMusicTrackRow(it, tracks, idx, columns)));
    applyMusicGridTemplate(list, "all");
  };
  musicColumnHeaderRefreshers.set(list, rerenderRows);
  rerenderRows();
  grid.appendChild(list);
}

// Musik-Listenansicht: Spalten (Breite + Reihenfolge) frei konfigurierbar
// (User-Wunsch 2026-09-06: "Diese Spalten möchte ich von der Breite und
// damit auch von der Position verschiebbar machen"). Betroffen sind nur die
// "echten" Text-Spalten (Titel/Künstler/Album/Dauer/Zuletzt gehört) — Track-
// Nummer, Cover-Thumbnail und der Favoriten-Button bleiben an fester
// Position, das sind reine Icon-Slots, keine Daten-Spalten im Sinne des
// User-Wunschs. Persistiert pro Kontext in localStorage
// (musicColumns:album / musicColumns:all), analog anderen Listen-Prefs wie
// flatView/musicListView.
// "genre" seit 2026-09-06 in beiden Kontexten ergänzt (User-Wunsch: "IN Der
// Musikansicht fehlt mir Genre noch in der Listenansicht als Spalte") —
// dieselbe Spalte, die es in der Album-ÜBERSICHT schon gibt (Kachel +
// Listenzeile), jetzt auch in den beiden Track-Listen. Braucht items.genre
// im JSON-Response (model.Item.Genre trug bis dahin `json:"-"`, war nur
// internes Zwischenlager für GroupMusicAlbums — jetzt exportiert +
// ListItems SELECTed es).
const MUSIC_LIST_CONTEXTS = {
  // Album-Übersicht als Liste (Cover+Album+Künstler+Genre+Titelzahl+Fav) —
  // seit 2026-09-11 ergänzt (User-Report: fehlte bisher komplett, im
  // Gegensatz zu den beiden Track-Listen unten).
  overview: {
    fixedLeading: ["cover"],
    reorderable: ["title", "artist", "genre", "count"],
    fixedTrailing: ["fav"],
    labels: { title: "Album", artist: "Künstler", genre: "Genre", count: "Titel" },
    defaultWidths: { title: 260, artist: 160, genre: 120, count: 90 },
    minWidths: { title: 100, artist: 80, genre: 70, count: 60 },
    fixedWidths: { cover: 40, fav: 32 },
  },
  album: {
    fixedLeading: ["track"],
    reorderable: ["title", "artist", "genre", "year", "duration"],
    // "editMeta" seit 2026-09-06 ergänzt (User-Wunsch: "Der Bearbeitungs-
    // button soll auch in der Listenansicht am Ende der Zeile sein") — reiner
    // Icon-Slot wie "fav", kein Spalten-Label nötig (renderMusicColumnHeader
    // baut für fixedLeading/fixedTrailing nur leere Platzhalter).
    fixedTrailing: ["fav", "editMeta"],
    labels: { title: "Titel", artist: "Künstler", genre: "Genre", year: "Jahr", duration: "Dauer" },
    defaultWidths: { title: 260, artist: 160, genre: 110, year: 60, duration: 70 },
    minWidths: { title: 100, artist: 80, genre: 70, year: 50, duration: 50 },
    fixedWidths: { track: 32, fav: 32, editMeta: 32 },
  },
  all: {
    fixedLeading: ["cover"],
    reorderable: ["title", "artist", "album", "genre", "year", "lastPlayed"],
    fixedTrailing: ["fav", "editMeta"],
    labels: { title: "Titel", artist: "Künstler", album: "Album", genre: "Genre", year: "Jahr", lastPlayed: "Zuletzt gehört" },
    defaultWidths: { title: 280, artist: 160, album: 160, genre: 110, year: 60, lastPlayed: 140 },
    minWidths: { title: 100, artist: 80, album: 80, genre: 70, year: 50, lastPlayed: 100 },
    fixedWidths: { cover: 40, fav: 32, editMeta: 32 },
  },
};

function musicColumnLayoutKey(context) { return `musicColumns:${context}`; }

function loadMusicColumnLayoutRaw(context) {
  try { return JSON.parse(localStorage.getItem(musicColumnLayoutKey(context)) || "null"); } catch { return null; }
}

function loadMusicColumnOrder(context) {
  const cfg = MUSIC_LIST_CONTEXTS[context];
  const saved = loadMusicColumnLayoutRaw(context);
  if (saved && Array.isArray(saved.order) && saved.order.length === cfg.reorderable.length
      && cfg.reorderable.every(c => saved.order.includes(c))) {
    return saved.order.slice();
  }
  return cfg.reorderable.slice();
}

function loadMusicColumnWidths(context) {
  const cfg = MUSIC_LIST_CONTEXTS[context];
  const saved = loadMusicColumnLayoutRaw(context);
  const widths = {};
  for (const col of cfg.reorderable) {
    widths[col] = (saved && saved.widths && typeof saved.widths[col] === "number")
      ? saved.widths[col] : cfg.defaultWidths[col];
  }
  return widths;
}

function saveMusicColumnLayout(context, order, widths) {
  try { localStorage.setItem(musicColumnLayoutKey(context), JSON.stringify({ order, widths })); } catch {}
}

// Komplettes Spalten-Array inkl. fixer Leading/Trailing-Slots in aktueller
// Reihenfolge — direkt als `columns`-Parameter für renderMusicTrackRow nutzbar.
function musicEffectiveColumns(context) {
  const cfg = MUSIC_LIST_CONTEXTS[context];
  return [...cfg.fixedLeading, ...loadMusicColumnOrder(context), ...cfg.fixedTrailing];
}

function musicGridTemplate(context) {
  const cfg = MUSIC_LIST_CONTEXTS[context];
  const order = loadMusicColumnOrder(context);
  const widths = loadMusicColumnWidths(context);
  const parts = [];
  for (const col of cfg.fixedLeading) parts.push(`${cfg.fixedWidths[col]}px`);
  for (const col of order) parts.push(`${Math.max(widths[col], cfg.minWidths[col])}px`);
  for (const col of cfg.fixedTrailing) parts.push(`${cfg.fixedWidths[col]}px`);
  return parts.join(" ");
}

// Setzt das berechnete Grid-Template auf Kopf- UND alle Daten-Zeilen eines
// Track-List-Containers — ein zentraler Anwendungspunkt, damit Resize sofort
// überall konsistent wirkt (inline style schlägt die CSS-":has()"-Fallback-
// Regeln in style.css, die nur für den Erstanstrich vor JS-Init greifen).
function applyMusicGridTemplate(list, context) {
  const tmpl = musicGridTemplate(context);
  list.querySelectorAll(".track-row").forEach(row => { row.style.gridTemplateColumns = tmpl; });
}

// Pro Listen-Container hinterlegter Re-Render-Callback (kompletter Rebuild
// von Kopfzeile + allen Zeilen mit neuer Spaltenreihenfolge) — nötig weil
// die Spalten-Reihenfolge nicht nur das CSS-Raster betrifft, sondern auch
// welcher Inhalt in welcher Zellen-Position steht (renderMusicTrackRow baut
// das HTML in `columns`-Reihenfolge). Von renderAlbumTracks/
// renderAllTracksList gesetzt, da nur deren Closure Zugriff auf `tracks` hat.
const musicColumnHeaderRefreshers = new WeakMap();

// renderMusicColumnHeader: Kopfzeile mit Resize-Handles (Drag am rechten
// Zellrand) + Drag-and-Drop-Reorder (natives HTML5-DnD) für die
// reorderable-Spalten. Fixe Icon-Slots bekommen nur einen leeren Platzhalter,
// damit das Grid-Raster mit den Datenzeilen übereinstimmt.
function renderMusicColumnHeader(context, list) {
  const cfg = MUSIC_LIST_CONTEXTS[context];
  const head = document.createElement("div");
  head.className = "track-row track-row--head track-row--col-head";
  let html = "";
  for (const _ of cfg.fixedLeading) html += `<span class="track-row-head-fixed"></span>`;
  for (const col of loadMusicColumnOrder(context)) {
    html += `<span class="track-row-head-cell" data-col="${col}">` +
      `<span class="track-row-head-label">${escapeHTML(cfg.labels[col] || col)}</span>` +
      `<span class="col-resize-handle" data-resize="${col}" title="Spaltenbreite ziehen"></span></span>`;
  }
  for (const _ of cfg.fixedTrailing) html += `<span class="track-row-head-fixed"></span>`;
  head.innerHTML = html;
  list.appendChild(head);
  wireMusicColumnHeader(head, context, list);
  return head;
}

// Reorder UND Resize laufen bewusst über dasselbe reine mousedown/mousemove/
// mouseup-System, NICHT über natives HTML5-Drag&Drop (draggable="true"):
// ein `draggable`-Kopfzellen-Container "verschluckt" jede Mausbewegung, die
// INNERHALB der Zelle beginnt — auch auf einem Kind-Element wie dem Resize-
// Handle, selbst mit explizitem draggable="false" darauf. Der Browser
// wechselt intern in den nativen Drag-Modus, sobald die Maus über dem
// draggable-Vorfahren bewegt wird, und liefert danach GAR KEINE regulären
// `mousemove`-Events mehr an JS — der Resize-Handler lief dadurch komplett
// leer (User-Bericht 2026-09-06: "Verschieben klappt, Vergrößern nicht").
// Live mit `computer`-Tool-Drag UND manuell verifiziert: mit natives-DnD kam
// nicht einmal das `mousedown` beim Handle an.
function wireMusicColumnHeader(head, context, list) {
  const cfg = MUSIC_LIST_CONTEXTS[context];
  const REORDER_THRESHOLD = 4; // px Mausbewegung bis ein Reorder-Drag beginnt

  // --- Breite ziehen ---
  head.querySelectorAll(".col-resize-handle").forEach(handle => {
    handle.addEventListener("mousedown", (e) => {
      e.preventDefault();
      e.stopPropagation();
      const col = handle.dataset.resize;
      const startX = e.clientX;
      const widths = loadMusicColumnWidths(context);
      const startWidth = widths[col];
      function onMove(ev) {
        const delta = ev.clientX - startX;
        widths[col] = Math.max(cfg.minWidths[col], startWidth + delta);
        saveMusicColumnLayout(context, loadMusicColumnOrder(context), widths);
        applyMusicGridTemplate(list, context);
      }
      function onUp() {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
      }
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    });
    handle.addEventListener("click", (e) => e.stopPropagation());
  });

  // --- Reihenfolge per Maus-Drag (eigenes Pointer-Tracking statt HTML5-DnD) ---
  head.querySelectorAll(".track-row-head-cell").forEach(cell => {
    cell.addEventListener("mousedown", (e) => {
      if (e.target.closest(".col-resize-handle")) return; // Resize hat Vorrang
      const startX = e.clientX;
      const startY = e.clientY;
      const sourceCol = cell.dataset.col;
      let dragging = false;
      let overCell = null;

      function onMove(ev) {
        if (!dragging) {
          if (Math.abs(ev.clientX - startX) < REORDER_THRESHOLD && Math.abs(ev.clientY - startY) < REORDER_THRESHOLD) return;
          dragging = true;
          cell.classList.add("dragging");
        }
        const target = document.elementFromPoint(ev.clientX, ev.clientY);
        const targetCell = target && target.closest(".track-row-head-cell");
        if (overCell && overCell !== targetCell) overCell.classList.remove("drag-over");
        overCell = (targetCell && targetCell !== cell && head.contains(targetCell)) ? targetCell : null;
        if (overCell) overCell.classList.add("drag-over");
      }
      function onUp() {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
        cell.classList.remove("dragging");
        if (overCell) overCell.classList.remove("drag-over");
        if (!dragging || !overCell) return;
        const targetCol = overCell.dataset.col;
        const order = loadMusicColumnOrder(context);
        const from = order.indexOf(sourceCol);
        const to = order.indexOf(targetCol);
        if (from === -1 || to === -1) return;
        order.splice(from, 1);
        order.splice(to, 0, sourceCol);
        saveMusicColumnLayout(context, order, loadMusicColumnWidths(context));
        const refresh = musicColumnHeaderRefreshers.get(list);
        if (refresh) refresh();
      }
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    });
  });
}

// renderMusicTrackRow: gemeinsamer Zeilen-Renderer für alle Musik-
// Listenansichten (Album-Detail-Liste + "Alle Titel"). `columns` steuert,
// welche Felder gezeigt werden. Album-Kontext zeigt seit 2026-09-06 auch
// "artist" (User-Wunsch) — bei "Verschiedene Interpreten"-Alben (siehe
// Store.GroupMusicAlbums) ist der tatsächliche Interpret pro Track sonst
// nirgends sichtbar, das Album-Feld selbst ist dort redundant/weggelassen.
function renderMusicTrackRow(it, queue, idx, columns) {
  const row = document.createElement("div");
  row.className = "track-row";
  row.tabIndex = 0;
  row.setAttribute("role", "button");
  row.dataset.itemId = it.id;
  // Checkbox-Overlay analog .card-select (immer im DOM, per CSS nur bei
  // body.selection-mode sichtbar) — ohne sichtbares Element gab es in der
  // Listenansicht keinerlei Hinweis, dass/wie Titel auswählbar sind
  // (User-Bericht 2026-09-04: "Auswählen in der Listenansicht finde ich
  // auch nicht"). toggleSelection()/selectAllVisible() (app.js) aktualisieren
  // dieses Element bereits generisch über denselben data-item-id-Selektor
  // wie .card-select.
  let html = `<span class="track-row-select" data-select>${state.selection.has(it.id) ? "✓" : ""}</span>`;
  for (const col of columns) {
    switch (col) {
      case "cover": {
        const cover = it.musicAlbumId ? `/api/poster/album/${it.musicAlbumId}` : "/placeholder.svg";
        html += `<img class="track-row-cover" loading="lazy" decoding="async" alt="" src="${cover}">`;
        break;
      }
      case "track":
        html += `<span class="track-row-num">${it.trackNo || "—"}</span>`;
        break;
      case "title":
        html += `<span class="track-row-title" title="${escapeHTML(it.title || "")}">${escapeHTML(it.title || "")}</span>`;
        break;
      case "artist":
        html += `<span class="track-row-artist">${escapeHTML(it.artist || "")}</span>`;
        break;
      case "album":
        html += `<span class="track-row-album">${escapeHTML(it.album || "")}</span>`;
        break;
      case "genre":
        html += `<span class="track-row-genre">${escapeHTML(it.genre || "")}</span>`;
        break;
      case "year":
        html += `<span class="track-row-year">${it.year || "—"}</span>`;
        break;
      case "duration":
        html += `<span class="track-row-duration">${fmtDuration(it.durationSec)}</span>`;
        break;
      case "lastPlayed":
        html += `<span class="track-row-played">${it.lastPlayedAt ? fmtDate(it.lastPlayedAt) : "—"}</span>`;
        break;
      case "fav":
        html += `<button type="button" class="fav-toggle track-row-fav ${it.favorite ? "is-on" : ""}" title="${it.favorite ? "Aus Favoriten entfernen" : "Zu Favoriten hinzufügen"}" data-toggle-fav aria-label="${it.favorite ? "Favorit" : "Kein Favorit"}">${it.favorite ? "♥" : "♡"}</button>`;
        break;
      case "editMeta":
        // Admin-only — Klick auf eine Musik-Zeile spielt sonst sofort ab
        // (musicPlayAlbum), es gibt keinen anderen Weg zum Edit-Dialog.
        html += (state.me && state.me.isAdmin)
          ? `<button type="button" class="edit-toggle track-row-edit" title="Metadaten bearbeiten" data-toggle-edit-meta aria-label="Metadaten bearbeiten">✏</button>`
          : `<span></span>`;
        break;
    }
  }
  row.innerHTML = html;
  if (state.selectionMode) row.classList.add("selected-row-mode");
  row.classList.toggle("selected", state.selection.has(it.id));
  row.addEventListener("click", (ev) => {
    // Bulk-Auswahl (☑ Auswählen) fehlte in der Listenansicht komplett —
    // ohne sie gab es keinen Weg, mehrere Musik-Titel auf einmal zu einer
    // Playlist hinzuzufügen (User-Anfrage 2026-09-04: "wie kann ich nur für
    // Musik Playlisten anlegen?" — Playlists sind bereits generisch, es
    // fehlte nur ein Weg, Titel dafür auszuwählen). Gleiches Verhalten wie
    // cards.js: im Auswahl-Modus togglet jeder Klick die Selektion statt
    // abzuspielen.
    if (state.selectionMode) {
      toggleSelection(it);
      row.classList.toggle("selected", state.selection.has(it.id));
      return;
    }
    const favTog = ev.target && ev.target.closest("[data-toggle-fav]");
    if (favTog) {
      ev.stopPropagation();
      toggleFavoriteOnCard(it, favTog);
      return;
    }
    const editTog = ev.target && ev.target.closest("[data-toggle-edit-meta]");
    if (editTog) {
      ev.stopPropagation();
      state.currentItem = it;
      openEditMetaDialog();
      return;
    }
    if (typeof musicPlayAlbum === "function") musicPlayAlbum(queue, idx);
  });
  row.addEventListener("keydown", e => { if (e.key === "Enter") row.click(); });
  return row;
}
