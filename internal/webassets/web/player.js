// player.js — Detail-Dialog + Video.js-Player + Trickplay-Hover + Buffer-Gate.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views → grid → player → app
//
// Public Functions:
//   openDetail(item)       — oeffnet den Detail-Dialog (Plot, Cast, Buttons)
//   applyPlayback(...)     — startet Video.js-Player mit Mode/Profile/Audio
//   skipPlayer(deltaSec)   — Skip-Buttons (-15/+30)
//   restartTranscodeAt(s)  — neue ffmpeg-Session bei Seek hinter Buffer
//   disposePlayer()        — Cleanup beim Schliessen
//   closePlayer()          — schliesst Player-Dialog
//   forcePlayerDuration    — schreibt Filmlaenge in Video.js' duration-Cache
//   positionBufferOverlay  — Docked vs. Floating je nach Fullscreen
//   startBufferDisplay     — Server-/Client-Buffer-Polling-Loop
//   stopTranscodeProgress, hideBufferOverlay
//   applyStartBufferGate   — Start-Vorlauf-Overlay mit Pause-bis-Buffer-da
//   clearStartBufferGate
//   syncTranscodeDisplays  — RAF-Loop fuer absolute Zeit-Anzeige bei Transcode
//   attachTrickplayHover   — Hover-VTT-Plugin
//   parseThumbVTT
//   startPausePrefetch, stopPausePrefetch — laedt waehrend Pause bereits
//     transkodierte HLS-Segmente vor, die VHS selbst im Pause-Zustand nicht holt
//
// Referenziert state und globale Funktionen aus app.js (shufflePrev/Next,
// openAddToPlaylist, openPersonView, etc.) und cards.js (renderCard etc.).

// --- Detail-View ---

// fileHintHTML rendert die kleine technische Info-Zeile am Fuß des Detail-
// Dialogs: Pfad · Container · Codecs · ggf. „🪤 Interlaced"-Hinweis · Item-ID.
function fileHintHTML(item) {
  if (!item) return "";
  const path = escapeHTML(item.relPath || item.path || "");
  const container = escapeHTML((item.container || "").toUpperCase());
  const codecs = `${escapeHTML(item.videoCodec || "")}/${escapeHTML(item.audioCodec || "")}`;
  const videoStreams = (item.streams || []).filter(s => s.type === "video");
  const interlaced = videoStreams.some(s => s.fieldOrder && s.fieldOrder !== "progressive" && s.fieldOrder !== "unknown");
  const ilTag = interlaced ? ` · <span title="Interlaced — Halbbilder werden vom Browser nicht entkämmt; Transcode mit Deinterlace empfohlen" style="color:#f59e0b">🪤 Interlaced</span>` : "";
  const idTag = item.id ? ` · <span class="item-id-tag" title="Item-ID in der Datenbank">#${item.id}</span>` : "";
  return `Datei: ${path} · ${container} · ${codecs}${ilTag}${idTag}`;
}

// ISO-639-2-Sprachcode → deutscher Name (kleine Tabelle; unbekannte Codes werden
// groß dargestellt). Für die Tonspur-/Untertitel-Liste im Detail-Dialog.
const STREAM_LANG_NAMES = {
  deu: "Deutsch", ger: "Deutsch", eng: "Englisch", fra: "Französisch", fre: "Französisch",
  spa: "Spanisch", ita: "Italienisch", jpn: "Japanisch", rus: "Russisch", nld: "Niederländisch",
  dut: "Niederländisch", por: "Portugiesisch", pol: "Polnisch", tur: "Türkisch", ces: "Tschechisch",
  cze: "Tschechisch", hun: "Ungarisch", kor: "Koreanisch", zho: "Chinesisch", chi: "Chinesisch",
  ara: "Arabisch", swe: "Schwedisch", dan: "Dänisch", fin: "Finnisch", nor: "Norwegisch",
  ell: "Griechisch", gre: "Griechisch", heb: "Hebräisch", hin: "Hindi", ukr: "Ukrainisch",
  ron: "Rumänisch", rum: "Rumänisch", bul: "Bulgarisch", srp: "Serbisch", hrv: "Kroatisch",
  slk: "Slowakisch", slv: "Slowenisch", tha: "Thai", vie: "Vietnamesisch", ind: "Indonesisch",
};
function streamLangLabel(code) {
  const c = (code || "").toLowerCase();
  return STREAM_LANG_NAMES[c] || (c ? c.toUpperCase() : "");
}
// Bild-basierte Untertitel-Codecs (PGS/VOBSUB/DVB) — die lassen sich NICHT
// nach WebVTT (Text) konvertieren, also auch nicht als Overlay-Track im Player
// einblenden. Der Server-`-c:s webvtt`-Aufruf scheitert bei denen.
const BITMAP_SUB_CODECS = new Set([
  "hdmv_pgs_subtitle", "pgssub", "pgs",
  "dvd_subtitle", "dvdsub",
  "dvb_subtitle", "dvbsub",
  "xsub",
]);
function isBitmapSub(codec) {
  return BITMAP_SUB_CODECS.has((codec || "").toLowerCase());
}

function streamChannelsLabel(n) {
  if (!n) return "";
  if (n === 1) return "Mono";
  if (n === 2) return "Stereo";
  if (n === 6) return "5.1";
  if (n === 7) return "6.1";
  if (n === 8) return "7.1";
  return `${n}ch`;
}

// audioOptLabel: "Deutsch · DTS · 5.1" für eine Audio-Stream-Zeile.
function audioOptLabel(a) {
  const parts = [streamLangLabel(a.language) || "Sprache unbekannt"];
  if (a.title) parts.push(a.title);
  if (a.codec) parts.push(a.codec.toUpperCase());
  const ch = streamChannelsLabel(a.channels);
  if (ch) parts.push(ch);
  return parts.filter(Boolean).join(" · ");
}

// streamsInfoHTML rendert für den Detail-Dialog zwei Dropdowns: Tonspur +
// Untertitel. Die getroffene Wahl wird in state.detailPrefs gemerkt und beim
// "Abspielen" an den Player durchgereicht (wireDetailAVSelects). Beim
// Untertitel-Dropdown erscheinen NUR einblendbare Spuren — unsere erzeugten
// (📝 OCR / 🎤 KI); Bild-Untertitel (PGS/VOBSUB) werden weggelassen
// (User-Wunsch 2026-08-31). `pbStreams` kommt aus /api/playback (enthält auch
// die erzeugten OCR/KI-Untertitel, item.streams allein nicht).
function streamsInfoHTML(item, pbStreams) {
  const streams = Array.isArray(pbStreams) ? pbStreams
    : (Array.isArray(item.streams) ? item.streams : []);
  const audio = streams.filter(s => s.type === "audio");
  const subs = streams.filter(s => s.type === "subtitle" && !isBitmapSub(s.codec));
  if (!audio.length && !subs.length) return `<div class="detail-av" id="detailStreams"></div>`;

  let html = "";
  if (audio.length) {
    const def = audio.find(a => a.isDefault) || audio[0];
    html += `<label class="detail-av-field"><span>🔊 Tonspur</span>
      <select id="detailAudioSelect">${audio.map(a =>
        `<option value="${a.index}"${a.index === def.index ? " selected" : ""}>${escapeHTML(audioOptLabel(a))}</option>`
      ).join("")}</select></label>`;
  }
  html += `<label class="detail-av-field"><span>💬 Untertitel</span>
    <select id="detailSubSelect"><option value="">— Aus —</option>${subs.map(s => {
      const lbl = s.title || (streamLangLabel(s.language) || "Untertitel");
      return `<option value="${s.index}">${escapeHTML(lbl)}</option>`;
    }).join("")}</select></label>`;

  return `<div class="detail-av" id="detailStreams">${html}</div>`;
}

// wireDetailAVSelects: Change-Handler + Init für die beiden Detail-Dropdowns.
// Schreibt state.detailPrefs = { itemId, audioIdx, subValue }.
function wireDetailAVSelects(item) {
  const aSel = $("#detailAudioSelect");
  const sSel = $("#detailSubSelect");
  const initialAudio = aSel ? aSel.value : null;
  const sync = () => {
    // audioIdx nur mitgeben, wenn es mehrere Tonspuren gibt UND der User eine
    // andere als die Standardspur gewählt hat — sonst lassen wir den Server
    // wie gehabt die erste Spur nehmen (keine Verhaltensänderung ohne Auswahl).
    let audioIdx = null;
    if (aSel && aSel.options.length > 1 && aSel.value !== initialAudio) {
      audioIdx = Number(aSel.value);
    }
    state.detailPrefs = {
      itemId: item.id,
      audioIdx: audioIdx,
      subValue: sSel ? sSel.value : "",
    };
  };
  sync();
  if (aSel && !aSel.dataset.wired) { aSel.dataset.wired = "1"; aSel.addEventListener("change", sync); }
  if (sSel && !sSel.dataset.wired) { sSel.dataset.wired = "1"; sSel.addEventListener("change", sync); }
}

// ratingRowHTML: klickbare 3-Sterne-Bewertung für Privat-Lib-Items (analog zur
// Mac/iOS-App `LocalItem.rating`). Für Nicht-Privat-Libs leer (dort gibt es das
// TMDB-Rating). `item.rating` ist 0..3 (user_item_state.rating).
function ratingRowHTML(item, lib) {
  if (!lib || lib.kind !== "private") return `<div class="detail-user-rating" id="detailRating"></div>`;
  const r = item.rating || 0;
  const stars = [1, 2, 3].map(n =>
    `<button type="button" class="star-btn${n <= r ? " on" : ""}" data-star="${n}" title="${n} Stern${n > 1 ? "e" : ""}">★</button>`
  ).join("");
  return `<div class="detail-user-rating" id="detailRating">
    <span class="detail-rating-label">Bewertung</span>
    ${stars}
    <button type="button" class="star-btn star-clear${r === 0 ? " on" : ""}" data-star="0" title="Keine Bewertung">✕</button>
  </div>`;
}

async function openDetail(item) {
  let pbStreams = null;   // volle Stream-Liste inkl. erzeugter OCR/KI-Untertitel
  state.detailPrefs = null;
  // Bei jedem Öffnen ein frisches Item vom Server holen — sonst zeigt das
  // Dialog die im Grid-Cache eingebetteten (alten) Metadaten, auch wenn
  // ein Bulk-/Single-Refresh die DB längst aktualisiert hat.
  // WICHTIG: das Frontend-Klebe-Feld `_variants` (gemerkte Geschwister-Files
  // bei mehrfach gemappter metadata_id) NICHT verlieren — der API-Response
  // hat das nicht, und ohne diesen Array gäbe es kein Varianten-Dropdown.
  if (item && item.id) {
    const carriedVariants = Array.isArray(item._variants) ? item._variants : null;
    try {
      const fresh = await api(`/api/items/${item.id}`);
      if (fresh) {
        if (carriedVariants && carriedVariants.length > 1) {
          fresh._variants = carriedVariants;
        }
        item = fresh;
        // Auch den Eintrag im aktuell gerenderten Grid patchen, damit
        // Re-Renders (z. B. nach favoriten-Toggle) frische Daten haben.
        if (Array.isArray(state.lastRenderedItems)) {
          const idx = state.lastRenderedItems.findIndex(x => x.id === fresh.id);
          if (idx >= 0) state.lastRenderedItems[idx] = fresh;
        }
      }
    } catch (e) {
      console.warn("openDetail: konnte frisches Item nicht holen, nutze Cache-Stand", e);
    }
    // Wenn der Aufrufer keine vorgemergten Variants mitgegeben hat (Path-
    // Search-Dialog, Person-Filter, Direktlink, …) und das Item eine
    // metadata_id hat, vom Server die Geschwister-Files nachladen. Erst dann
    // erscheint im Detail-Dialog auch der Varianten-Dropdown — sonst wäre er
    // nur sichtbar bei Klicks aus dem groupVariants-Grid.
    if ((!item._variants || item._variants.length <= 1) && item.metadataId > 0) {
      try {
        const sibs = await api(`/api/items/${item.id}/variants`);
        if (Array.isArray(sibs) && sibs.length > 1) {
          item._variants = sibs;
        }
      } catch (e) {
        console.warn("openDetail: Variants-Fetch fehlgeschlagen", e);
      }
    }
    // Volle Stream-Liste (raw + erzeugte OCR/KI-Untertitel) für die AV-Dropdowns.
    try {
      const pb = await api(`/api/playback/${item.id}`);
      if (pb && Array.isArray(pb.streams)) pbStreams = pb.streams;
    } catch (e) { /* Dropdowns fallen auf item.streams zurück */ }
  }
  state.currentItem = item;
  const meta = item.metadata;
  // Cache-Busting via ?v=<posterPath>, siehe cards.js renderCard-Kommentar (User-Bericht 2026-08-19).
  const posterUrl = (meta && meta.posterPath) ? `/api/poster/metadata/${item.metadataId}?v=${encodeURIComponent(meta.posterPath)}` : (item.hasThumb ? `/api/thumb/${item.id}` : "/placeholder.svg");

  let title = item.title;
  let sub = [];
  let overview = "";
  let rating = "";
  if (meta) {
    title = meta.title || item.title;
    if (meta.tmdbType === "episode") {
      // Show-Namen aus rel_path[0] ableiten — die Item-Metadata trägt nur die
      // Episode, nicht die Show. Format: „Show — Episodentitel" (oder
      // „Show — S01E10" wenn TMDB keinen Episodentitel hat).
      const segs = (item.relPath || "").split("/");
      const showName = segs.length > 1 ? segs[0] : "";
      const code = `S${String(meta.season).padStart(2, "0")}E${String(meta.episode).padStart(2, "0")}`;
      const epTitle = meta.title || "";
      if (showName && epTitle) {
        title = `${showName} — ${epTitle}`;
      } else if (showName) {
        title = `${showName} — ${code}`;
      } else {
        title = epTitle || code;
      }
      sub.push(code);
      if (meta.releaseDate) sub.push(fmtDate(meta.releaseDate));
    } else {
      if (meta.year) sub.push(String(meta.year));
    }
    try {
      const genres = JSON.parse(meta.genres || "[]");
      if (genres.length) sub.push(genres.join(", "));
    } catch {}
    if (meta.runtimeMin > 0) sub.push(`${meta.runtimeMin} Min`);
    if (meta.rating > 0) {
      rating = `<span class="rating-pill">★ ${meta.rating.toFixed(1)}</span>`;
    }
    overview = meta.overview || "";
  } else {
    sub.push(`${item.width}×${item.height}`);
    if (item.releasedAt) sub.push(fmtDate(item.releasedAt));
    sub.push(fmtSize(item.sizeBytes));
  }

  const watchedIcon = item.watched ? "✓ Gesehen" : "";
  // FSK-Badge (wenn gesetzt) — separat vom Sub-Text, damit es farbcodiert
  // angezeigt werden kann. Werte "0"/"6"/"12"/"16"/"18".
  let fskBadge = "";
  if (meta && meta.ageRating) {
    fskBadge = `<span class="fsk-badge fsk-${meta.ageRating}">FSK ${meta.ageRating}</span>`;
  }
  // siblings: ALLE Items mit gleicher metadataId, unabhängig vom Split-Status
  // — Grundlage für den Trennen/Zusammenlegen-Button (der wirkt immer auf die
  // komplette ursprüngliche Gruppe).
  const siblings = Array.isArray(item._variants) ? item._variants : [item];
  const hasSiblings = siblings.length > 1;
  const isAdmin = !!(state.me && state.me.isAdmin);
  const allSplit = hasSiblings && siblings.every(v => v.variantSplit);
  const variantSplitBtn = (hasSiblings && isAdmin) ? `
    <button type="button" id="detailVariantSplit" class="link-btn">
      ${allSplit ? "🔗 Wieder zusammenlegen" : "🔀 Als eigene Kacheln trennen"}
    </button>
  ` : "";
  // dropdownVariants: nur noch die NICHT getrennten Geschwister — ein bereits
  // getrenntes Item ist bewusst eigenständig und soll im "Variante"-Dropdown
  // nicht mehr mit (ggf. völlig anderen, nur falsch zugeordneten) Geschwistern
  // zusammen auftauchen. Ist das aktuell geöffnete Item selbst getrennt,
  // gibt's für dieses Item gar keinen Dropdown mehr (nur sich selbst).
  const dropdownVariants = item.variantSplit ? [item] : siblings.filter(v => !v.variantSplit);
  const hasVariants = dropdownVariants.length > 1;
  const variantDropdown = hasVariants ? `
    <div class="variant-row">
      <label>Variante
        <select id="detailVariant">
          ${dropdownVariants.map(v => {
            const label = escapeHTML(variantLabel(v));
            return `<option value="${v.id}" title="${label}">${label}</option>`;
          }).join("")}
        </select>
      </label>
      ${variantSplitBtn}
    </div>
  ` : (variantSplitBtn ? `<div class="variant-row">${variantSplitBtn}</div>` : "");
  $("#detailContent").innerHTML = `
    <div class="detail-wrap">
      <div class="detail-poster" style="background-image:url('${posterUrl}')"></div>
      <div class="detail-body">
        <h2>${escapeHTML(title)} ${watchedIcon ? `<span style="color:#22c55e;font-size:13px;margin-left:8px">${watchedIcon}</span>` : ""}</h2>
        <div class="sub">
          ${rating}
          ${fskBadge}
          ${sub.map(x => `<span>${escapeHTML(x)}</span>`).join("")}
        </div>
        <p class="overview">${escapeHTML(overview || "—")}</p>
        ${ratingRowHTML(item, state.libraries.find(l => l.id == item.libraryId))}
        ${variantDropdown}
        ${streamsInfoHTML(item, pbStreams)}
        <div id="detailCast" class="cast-strip hidden"></div>
        <p class="hint" id="detailFileHint">${fileHintHTML(item)}</p>
      </div>
    </div>
  `;
  if (hasVariants) {
    const sel = $("#detailVariant");
    sel.value = String(item.id);
    sel.addEventListener("change", () => {
      const pick = dropdownVariants.find(v => String(v.id) === sel.value);
      if (!pick) return;
      // state.currentItem auf ausgewählte Variante setzen (Play/Download/Favorit)
      pick._variants = siblings;
      state.currentItem = pick;
      state.detailPrefs = null;
      $("#detailFileHint").innerHTML = fileHintHTML(pick);
      // Stream-Liste der neuen Variante frisch holen (andere Datei = andere Spuren).
      api(`/api/playback/${pick.id}`).then(pb => {
        const streamsEl = $("#detailStreams");
        if (streamsEl) {
          streamsEl.outerHTML = streamsInfoHTML(pick, pb && pb.streams);
          wireDetailAVSelects(pick);
        }
      }).catch(() => {
        const streamsEl = $("#detailStreams");
        if (streamsEl) { streamsEl.outerHTML = streamsInfoHTML(pick, null); wireDetailAVSelects(pick); }
      });
      updateDetailWatchedBtn();
      updateDetailFavBtn();
    });
  }
  if (hasSiblings && isAdmin) {
    const splitBtn = $("#detailVariantSplit");
    if (splitBtn) splitBtn.addEventListener("click", async () => {
      const makeSplit = !allSplit;
      splitBtn.disabled = true;
      try {
        await Promise.all(siblings.map(v => api(`/api/items/${v.id}/variant-split`, {
          method: "PUT",
          body: JSON.stringify({ split: makeSplit }),
        })));
        showToast(makeSplit ? "Als eigene Kacheln getrennt" : "Wieder zusammengelegt", { kind: "success" });
        // Wichtig: #detailDialog schliessen, NICHT closePlayer() — das würde
        // #playerDialog (den Video-Player) schliessen, ein komplett anderes
        // <dialog>-Element. Der Button lebt im Detail-Dialog; ohne diesen Fix
        // blieb er nach dem Klick einfach offen stehen (Grid re-renderte im
        // Hintergrund, sichtbar passierte scheinbar nichts).
        try { $("#detailDialog").close(); } catch {}
        loadItems();
      } catch (e) {
        showToast(`Fehler: ${e.message}`, { kind: "error" });
        splitBtn.disabled = false;
      }
    });
  }
  updateDetailWatchedBtn();
  updateDetailFavBtn();
  updateConfirmBtn();
  wireDetailRating(item);
  wireDetailAVSelects(item);
  if (typeof initSubGenBtn === "function") initSubGenBtn(item);
  const itemLib = state.libraries.find(l => l.id == item.libraryId);
  const isAdminUser = !!(state.me && state.me.isAdmin);
  // Download-Erlaubnis ist pro User in der Benutzerverwaltung einstellbar
  // (User-Vorgabe 2026-09-02). Server lehnt ohnehin mit 403 ab — hier nur
  // UI-Komfort, damit der Button gar nicht erst zum Klicken einlädt.
  $("#detailDownload").style.display = (isAdminUser || (state.me && state.me.canDownload !== false)) ? "" : "none";
  // Manuell zuordnen + Zuordnung bestätigen sind Admin-Funktionen (User-
  // Vorgabe 2026-09-02) — vorher fehlte hier die isAdmin-Prüfung komplett,
  // jeder eingeloggte User konnte TMDB-Zuordnungen manuell ändern/bestätigen.
  $("#detailMatch").style.display = (isAdminUser && !(itemLib && itemLib.kind === "private")) ? "" : "none";
  // Confirm-Button nur sinnvoll bei TMDB-matchen Items (nicht private, nicht
  // unmatched — sonst gibt's nichts zu bestätigen).
  $("#detailConfirm").style.display = (isAdminUser && !(itemLib && itemLib.kind === "private") && item.metadataId) ? "" : "none";
  // Zuordnung entfernen: nur sinnvoll, wenn überhaupt eine da ist (User-
  // Vorgabe 2026-09-02: "eine falsche Zuordnung löschen" — ergänzt "Manuell
  // zuordnen", das nur ERSETZEN, nicht entfernen kann).
  $("#detailUnmatch").style.display = (isAdminUser && !(itemLib && itemLib.kind === "private") && item.metadataId) ? "" : "none";
  // Refresh-Button: nur sinnvoll wenn TMDB-Zuordnung existiert (Admin-only)
  $("#detailRefreshMeta").style.display = ((state.me && state.me.isAdmin) && item.metadataId && !(itemLib && itemLib.kind === "private")) ? "" : "none";
  // Delete nur für Admins
  $("#detailDelete").style.display = (state.me && state.me.isAdmin) ? "" : "none";
  // Verschieben nur für Admins
  $("#detailMove").style.display = (state.me && state.me.isAdmin) ? "" : "none";
  // Metadaten-Edit/Anlegen nur für Admins, in nicht-Private-Libs. Bei
  // unmatched Items oeffnet der Pencil-Button die Maske leer und legt
  // beim Speichern einen Custom-Metadata-Eintrag an (tmdb_type=custom).
  // Edit-Meta-Pencil ist fuer Admins sichtbar — auch in Privat-Libs.
  // Dort hat man typischerweise keine TMDB-Zuordnung; der Pencil
  // oeffnet die Maske mit dem Dateinamen als Default-Titel, beim
  // Speichern wird ein Custom-Metadata-Eintrag angelegt.
  const canEditMeta = (state.me && state.me.isAdmin);
  $("#detailEditMeta").style.display = canEditMeta ? "" : "none";
  $("#detailEditMeta").title = item.metadataId ? "Metadaten bearbeiten" : "Metadaten manuell anlegen";
  // Scroll-Position merken & nach showModal() wiederherstellen. Browser springt
  // sonst manchmal an den Seitenanfang, weil das Dialog-Element am DOM-Anfang
  // den Fokus zieht und/oder der Hintergrund-Scroll-Lock beim Öffnen kurz zurücksetzt.
  const savedScrollY = window.scrollY;
  const detDlg = $("#detailDialog");
  // Popover + Polling bei Dialog-Schluss aufräumen
  detDlg.addEventListener("close", () => {
    const pop = $("#subGenPopover");
    if (pop) pop.classList.add("hidden");
    if (typeof stopSubGenPolling === "function") stopSubGenPolling();
  }, { once: true });
  detDlg.showModal();
  if (window.scrollY !== savedScrollY) window.scrollTo(0, savedScrollY);
  // Cast lazy nachladen (kein Blockieren des Dialog-Öffnens)
  if (item.metadataId > 0) loadDetailCast(item.metadataId);
}

async function loadDetailCast(metadataId) {
  const el = $("#detailCast");
  if (!el) return;
  try {
    const cast = await api(`/api/metadata/${metadataId}/cast`);
    if (!cast || !cast.length) {
      el.classList.add("hidden");
      return;
    }
    el.classList.remove("hidden");
    el.innerHTML = `
      <h3 class="cast-heading">Besetzung</h3>
      <div class="cast-row">
        ${cast.map(c => `
          <button type="button" class="cast-card" data-tmdb-id="${c.tmdbId}" data-name="${escapeHTML(c.name)}" title="Filme/Serien mit ${escapeHTML(c.name)}">
            <div class="cast-photo" style="background-image:url('/api/person/${c.tmdbId}/profile')"></div>
            <div class="cast-name">${escapeHTML(c.name)}</div>
            <div class="cast-role">${escapeHTML(c.character || "")}${c.role === "guest" ? ' <span class="cast-guest">Gast</span>' : ""}</div>
          </button>
        `).join("")}
      </div>
    `;
    el.querySelectorAll(".cast-card").forEach(btn => {
      btn.addEventListener("click", () => {
        const tmdbId = Number(btn.dataset.tmdbId);
        const name = btn.dataset.name;
        $("#detailDialog").close();
        openPersonView(tmdbId, name);
      });
    });
  } catch (e) {
    console.warn("cast load:", e);
    el.classList.add("hidden");
  }
}

// openPersonView: aktiviert Person-Filter-Modus. Grid zeigt nur Items, bei denen
// diese Person im Cast ist (aus beliebiger Library). Breadcrumb zeigt Namen +
// Zurück-Button, der den Filter wieder aufhebt.
async function openPersonView(tmdbId, name) {
  state.personFilter = { tmdbId, name };
  // Library/Folder-Kontext kurz zwischenspeichern, damit wir zurückkehren können
  state.personFilterBackup = {
    libraryId: state.currentLibrary,
    folder: state.currentFolder,
    drilldown: state.currentFolderDrilldown,
    homeView: state.homeView,
    collectionsView: state.collectionsView,
    currentCollection: state.currentCollection,
    playlistsView: state.playlistsView,
  };
  state.currentLibrary = null;
  state.currentFolder = null;
  state.currentFolderDrilldown = false;
  // Home-/Collection-/Playlist-Ansicht deaktivieren — sonst fängt loadItems
  // einen dieser Zweige vor dem Person-Filter ab und zeigt weiter die alte
  // Ansicht. Genau das war der „Klick auf Schauspieler auf der Startseite
  // tut nichts"-Bug.
  state.homeView = false;
  state.collectionsView = false;
  state.currentCollection = null;
  state.playlistsView = false;
  await loadItems();
}

function clearPersonView() {
  const b = state.personFilterBackup;
  state.personFilter = null;
  state.personFilterShow = null;
  state.personFilterBackup = null;
  if (b) {
    state.currentLibrary = b.libraryId;
    state.currentFolder = b.folder;
    state.currentFolderDrilldown = b.drilldown;
    state.homeView = !!b.homeView;
    state.collectionsView = !!b.collectionsView;
    state.currentCollection = b.currentCollection || null;
    state.playlistsView = !!b.playlistsView;
  }
  loadItems();
}

function updateDetailWatchedBtn() {
  const btn = $("#detailWatched");
  if (!btn) return;
  const on = !!(state.currentItem && state.currentItem.watched);
  btn.textContent = "✓";
  btn.classList.toggle("icon-btn--on-watched", on);
  btn.title = on ? "Als ungesehen markieren" : "Als gesehen markieren";
}

// wireDetailRating hängt Klick-Handler an die Sterne im Detail-Dialog (nur bei
// Privat-Libs gerendert). Klick auf Stern N setzt Rating=N, „✕" setzt 0.
// PUT /api/items/{id}/rating — aktualisiert das Item lokal + die Kachel im Grid.
function wireDetailRating(item) {
  const row = $("#detailRating");
  if (!row || !row.querySelector(".star-btn")) return;
  row.addEventListener("click", async (e) => {
    const btn = e.target.closest(".star-btn");
    if (!btn) return;
    const val = parseInt(btn.dataset.star, 10) || 0;
    const target = state.currentItem || item;
    try {
      await api(`/api/items/${target.id}/rating`, {
        method: "PUT",
        body: JSON.stringify({ rating: val }),
      });
    } catch (err) {
      showToast(`Fehler: ${err.message}`, { kind: "error" });
      return;
    }
    target.rating = val;
    // Sterne im Dialog updaten
    row.querySelectorAll(".star-btn").forEach(b => {
      const n = parseInt(b.dataset.star, 10) || 0;
      b.classList.toggle("on", n === 0 ? val === 0 : n <= val);
    });
    // Kachel im Grid ohne Full-Reload nachziehen
    if (typeof silentlyRefreshItem === "function") silentlyRefreshItem(target.id);
  });
}

function updateConfirmBtn() {
  const btn = $("#detailConfirm");
  if (!btn) return;
  const on = !!(state.currentItem && state.currentItem.metadataConfirmed);
  btn.classList.toggle("icon-btn--on-confirmed", on);
  btn.textContent = on ? "✅" : "☑";
  btn.title = on ? "Zuordnung ist bestätigt – Klick zum Entfernen" : "Zuordnung als richtig bestätigen";
  // NFO-Button nur bei bestätigten Items: das ist ein Write-to-Disk-Operation,
  // die wir ausschließlich auf kuratierten Zuordnungen erlauben.
  const nfo = $("#detailNFO");
  if (nfo && state.me && state.me.isAdmin) {
    nfo.classList.toggle("hidden", !on);
  } else if (nfo) {
    nfo.classList.add("hidden");
  }
  // Rename-Button (🏷) — admin, nur bei bestaetigten Filmen. Holt eine
  // Preview vom Server, damit der Tooltip den Ziel-Dateinamen zeigt;
  // bei alreadyOK (Datei traegt schon den Wunschnamen) wird der Button
  // ausgeblendet.
  const rn = $("#detailRename");
  if (rn) {
    const it = state.currentItem;
    const isMovie = it && it.metadata && it.metadata.tmdbType === "movie";
    if (state.me && state.me.isAdmin && on && isMovie) {
      rn.classList.remove("hidden");
      rn.title = "Datei zu Titel (Jahr) umbenennen — Vorschau lädt…";
      rn.disabled = true;
      api(`/api/items/${it.id}/rename-preview`).then(p => {
        if (state.currentItem !== it) return; // Dialog geschlossen / anderes Item
        if (!p.canRename) {
          rn.classList.add("hidden");
          return;
        }
        if (p.alreadyOK) {
          rn.classList.add("hidden");
          return;
        }
        rn.title = `Umbenennen zu: ${p.targetBase}`;
        rn.disabled = false;
      }).catch(() => { rn.classList.add("hidden"); });
    } else {
      rn.classList.add("hidden");
    }
  }
}

function updateDetailFavBtn() {
  const btn = $("#detailFavorite");
  if (!btn) return;
  const on = !!(state.currentItem && state.currentItem.favorite);
  btn.textContent = on ? "♥" : "♡";
  btn.classList.toggle("icon-btn--on-fav", on);
  btn.title = on ? "Aus Favoriten entfernen" : "Zu Favoriten hinzufügen";
}

// --- Player ---

// askResume öffnet einen gestylten Dialog mit drei Optionen:
// "continue" = bei gespeicherter Position weitermachen
// "restart"  = von Anfang starten
// "cancel"   = kein Player öffnen
function askResume(positionSec) {
  return new Promise((resolve) => {
    const dlg = $("#resumeDialog");
    $("#resumeTime").textContent = fmtDuration(positionSec);
    const finish = (choice) => {
      ["resumeContinueBtn", "resumeRestartBtn", "resumeCancelBtn"].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.onclick = null;
      });
      dlg.onclose = null;
      try { dlg.close(); } catch {}
      resolve(choice);
    };
    $("#resumeContinueBtn").onclick = () => finish("continue");
    $("#resumeRestartBtn").onclick = () => finish("restart");
    $("#resumeCancelBtn").onclick = () => finish("cancel");
    // ESC oder Backdrop-Close = Abbrechen
    dlg.onclose = () => finish("cancel");
    dlg.showModal();
  });
}

async function openPlayer(item, opts = {}) {
  state.currentItem = item;
  state.watchedFired = false;
  state.pendingResumeSec = 0;
  // "Zuletzt abgespielt"-Timestamp setzen (non-blocking, Fehler ignoriert).
  api(`/api/items/${item.id}/played`, { method: "POST" }).catch(() => {});
  // Resume-Position prüfen (ohne Fortsetzen-Dialog wenn <30s).
  if (!opts.fromShuffle && !opts.skipResume) {
    try {
      const r = await api(`/api/items/${item.id}/resume`);
      const pos = Number(r && r.positionSec) || 0;
      const dur = item.durationSec || 0;
      if (pos >= 30 && (dur === 0 || pos < dur - 30)) {
        const choice = await askResume(pos);
        if (choice === "cancel") return;           // Player gar nicht öffnen
        if (choice === "continue") {
          state.pendingResumeSec = pos;
        } else if (choice === "restart") {
          // Resume auf Server sofort auf 0 setzen — sonst bietet das nächste
          // Öffnen wieder den alten Fortsetzen-Punkt an (weil unser saveResume
          // erst nach 10 s Laufzeit und ab 5 s Position schreibt — bei kurzen
          // „Neu starten"-Sessions bleibt sonst die alte DB-Position stehen).
          api(`/api/items/${item.id}/resume`, {
            method: "PUT",
            body: JSON.stringify({ positionSec: 0 }),
          }).catch(() => {});
        }
      }
    } catch {}
  }
  // Queue-Index berechnen (falls in Playlist-Ansicht)
  if (state.currentPlaylist && state.playQueue.length) {
    state.playQueueIdx = state.playQueue.findIndex(x => x.id === item.id);
  } else {
    state.playQueueIdx = -1;
  }
  // Shuffle-Mode nur behalten wenn explizit über shuffle-Navigation geöffnet
  if (!opts.fromShuffle) {
    state.shuffleMode = false;
    state.shuffleHistory = [];
    state.shuffleIdx = -1;
  }
  $("#modeSelect").value = "auto";
  // Dialog VOR Video.js-Init öffnen, damit der Player seine echte Breite kennt.
  // Sonst misst Video.js Breite 0 und setzt `vjs-layout-tiny`, was die Progress-Bar
  // und andere Controls versteckt.
  const dlg = $("#playerDialog");
  if (!dlg.open) dlg.showModal();
  // Tonspur-Vorwahl aus dem Detail-Dialog (Untertitel-Vorwahl greift in
  // applyPlayback über #subSelect).
  let prefAudioIdx;
  if (state.detailPrefs && state.detailPrefs.itemId === item.id
      && state.detailPrefs.audioIdx != null && state.detailPrefs.audioIdx >= 0) {
    prefAudioIdx = state.detailPrefs.audioIdx;
  }
  await applyPlayback(item, "auto", "orig", prefAudioIdx);
  updatePlayerButtons();
}

// Video.js-Components für Shuffle/Favorit/Playlist in der Control-Bar.
// Einmalig pro Session registriert; Child-Komponenten werden in applyPlayback
// hinzugefügt. So sind die Buttons auch im Vollbildmodus sichtbar.
// skipPlayer: ±N Sekunden absolut. Bei Direct Play simpler currentTime-Set;
// bei Transcode wird der virtualOffset berücksichtigt, und falls das Ziel
// außerhalb des aktuellen Server-Buffers liegt, eine neue Transcode-Session
// am Ziel gestartet (gleich wie der Capture-Handler auf dem progressControl).
function skipPlayer(deltaSec) {
  const vjs = state.vjs;
  if (!vjs || !state.currentItem) return;
  const offset = (state.playback && state.playback.virtualOffset) || 0;
  const total = state.currentItem.durationSec || 0;
  let cur = 0;
  try { cur = vjs.currentTime() || 0; } catch {}
  const absCur = cur + offset;
  const target = Math.max(0, total > 0 ? Math.min(total, absCur + deltaSec) : absCur + deltaSec);
  if (state.playback && state.playback.mode === "transcode") {
    const seekable = vjs.seekable();
    const seekableEnd = (seekable && seekable.length) ? seekable.end(0) : 0;
    const absSeekableEnd = seekableEnd + offset;
    if (target >= offset && target <= absSeekableEnd + 3) {
      try { vjs.currentTime(Math.max(0, target - offset)); } catch {}
    } else {
      restartTranscodeAt(target);
    }
  } else {
    try { vjs.currentTime(target); } catch {}
  }
}

// ensurePlayerComponents() liegt in player-components.js.

// initCastFramework() und startCastSession() liegen in cast.js.

function updatePlayerButtons() {
  if (!state.vjs) return;
  const cb = state.vjs.getChild("controlBar");
  if (!cb) return;
  const prev = cb.getChild("ShufflePrev");
  const next = cb.getChild("ShuffleNext");
  if (prev && next) {
    if (state.shuffleMode) {
      prev.show(); next.show();
      if (state.shuffleIdx <= 0) prev.disable(); else prev.enable();
    } else {
      prev.hide(); next.hide();
    }
  }
  const fav = cb.getChild("FavoriteButton");
  if (fav) {
    const on = !!(state.currentItem && state.currentItem.favorite);
    fav.el().classList.toggle("vjs-favorite--on", on);
    fav.controlText(on ? "Aus Favoriten entfernen" : "Zu Favoriten hinzufügen");
  }
  // Initial versteckt — maybeToggleIntroSkip() uebernimmt ab dem ersten
  // timeupdate, verhindert aber ein kurzes Aufblitzen davor.
  const introSkipOverlay = $("#introSkipOverlayBtn");
  if (introSkipOverlay) introSkipOverlay.classList.add("hidden");
}

// Spielt das nächste Item in der aktiven Playlist-Queue ab, falls vorhanden.
function playNextInQueue() {
  if (state.playQueueIdx < 0) return;
  const next = state.playQueue[state.playQueueIdx + 1];
  if (!next) return;
  openPlayer(next);
}

// Auto-Mark als „gesehen" wenn 90 % der Laufzeit erreicht.
//
// WICHTIG: bei Transcode liefert vjs.currentTime() nur die LOKALE Segment-
// Position (zaehlt von 0, weil ffmpeg ab `virtualOffset` segmentiert).
// Die absolute Video-Position ist `currentTime + virtualOffset`. Ohne
// virtualOffset-Korrektur wuerde der 90-%-Threshold bei Resume-Plays
// nie gezuendet (z.B. Resume bei 50 min, Video-Laenge 100 min →
// currentTime steigt nur bis 50, Ratio 50/100 = 0.5 < 0.9).
// Bei Direct Play ist virtualOffset=0, also keine Aenderung.
function maybeMarkWatched(vjs) {
  if (state.watchedFired) return;
  const item = state.currentItem;
  if (!item || item.watched) return;
  const dur = item.durationSec || (vjs ? vjs.duration() : 0);
  const cur = vjs ? vjs.currentTime() : 0;
  if (!dur || dur <= 0 || !isFinite(cur)) return;
  const offset = (state.playback && state.playback.virtualOffset) || 0;
  const absolute = cur + offset;
  if (absolute / dur >= 0.9) {
    markWatchedNow(item);
  }
}

// Zeigt/versteckt den Intro-Skip-Overlay-Button (#introSkipOverlayBtn, groß
// und direkt im Videobild — bewusst KEIN kleines ControlBar-Icon mehr,
// User-Feedback 2026-08-12 war "übersehe ich"). Sichtbarkeit richtet sich
// nach der absoluten Wiedergabeposition im erkannten Vorspann-Fenster.
// Gleiche virtualOffset-Korrektur wie maybeMarkWatched — bei Transcode
// zaehlt currentTime() nur lokal ab dem Segment-Start.
function maybeToggleIntroSkip(vjs) {
  if (!vjs) return;
  const btn = $("#introSkipOverlayBtn");
  if (!btn) return;
  const item = state.currentItem;
  if (!item || item.introStartSec == null || item.introEndSec == null) {
    btn.classList.add("hidden");
    return;
  }
  let cur = 0;
  try { cur = vjs.currentTime() || 0; } catch {}
  const offset = (state.playback && state.playback.virtualOffset) || 0;
  const absolute = cur + offset;
  if (absolute >= item.introStartSec && absolute < item.introEndSec) btn.classList.remove("hidden");
  else btn.classList.add("hidden");
}

// wireIntroSkipOverlayOnce: Klick-Handler für den Overlay-Button — einmalig
// gewired (statisches DOM-Element, anders als die pro-Player-Open neu
// erzeugten Video.js-ControlBar-Kinder).
function wireIntroSkipOverlayOnce() {
  const btn = $("#introSkipOverlayBtn");
  if (!btn || btn.dataset.wired) return;
  btn.dataset.wired = "1";
  btn.addEventListener("click", () => {
    const item = state.currentItem;
    const vjs = state.vjs;
    if (!item || !vjs || item.introEndSec == null) return;
    const offset = (state.playback && state.playback.virtualOffset) || 0;
    let cur = 0;
    try { cur = vjs.currentTime() || 0; } catch {}
    const delta = item.introEndSec - (cur + offset);
    skipPlayer(delta);
    btn.classList.add("hidden");
  });
}

// markWatchedNow: gemeinsamer Pfad fuer 90-%-Threshold UND ended-Event.
// Idempotent durch state.watchedFired-Flag.
function markWatchedNow(item) {
  if (state.watchedFired) return;
  state.watchedFired = true;
  api(`/api/items/${item.id}/watched`, { method: "PUT", body: JSON.stringify({ watched: true }) })
    .then(() => {
      item.watched = true;
      updateDetailWatchedBtn();
      // Lautlos die Kachel im Grid auffrischen, damit der gruene
      // Watched-Haken sofort sichtbar ist — ohne Page-Reload.
      if (typeof silentlyRefreshItem === "function") silentlyRefreshItem(item.id);
    })
    .catch(console.warn);
}

// --- Trickplay-Hover-Thumbnails (eigenes Plugin) ---
// Parst eine WebVTT-Datei mit Cues wie "sprite.jpg#xywh=X,Y,W,H" und blendet
// beim Hovern über die Video.js-Progress-Bar das passende Sprite-Bild ein.

const trickplayState = new WeakMap(); // vjs-Instanz → { cues, el, spriteUrl, cleanup }

async function attachTrickplayHover(vjs, vttUrl) {
  detachTrickplayHover(vjs);
  let txt;
  try {
    const res = await fetch(vttUrl, { credentials: "same-origin" });
    if (!res.ok) return;
    txt = await res.text();
  } catch (e) { return; }
  const cues = parseThumbVTT(txt);
  if (!cues.length) return;
  const base = vttUrl.replace(/[^/]+$/, "");
  const spriteUrl = base + "sprite.jpg";

  const cb = vjs.getChild("controlBar");
  const pc = cb && cb.getChild("progressControl");
  if (!pc || !pc.el()) return;
  const pcEl = pc.el();

  const preview = document.createElement("div");
  preview.className = "trickplay-preview";
  preview.style.cssText = "position:absolute;bottom:100%;margin-bottom:8px;pointer-events:none;display:none;border:2px solid #fff;border-radius:3px;background:#000 no-repeat;box-shadow:0 2px 12px rgba(0,0,0,0.6);z-index:2;";
  pcEl.appendChild(preview);

  const firstCue = cues[0];
  const tileW = firstCue.w || 160;
  const tileH = firstCue.h || 90;
  preview.style.width = tileW + "px";
  preview.style.height = tileH + "px";
  preview.style.backgroundImage = `url(${spriteUrl})`;

  const onMove = (ev) => {
    const dur = vjs.duration();
    if (!dur || !isFinite(dur)) { preview.style.display = "none"; return; }
    const rect = pcEl.getBoundingClientRect();
    const x = ev.clientX - rect.left;
    if (x < 0 || x > rect.width) { preview.style.display = "none"; return; }
    const t = (x / rect.width) * dur;
    const cue = findCue(cues, t);
    if (!cue) { preview.style.display = "none"; return; }
    preview.style.backgroundPosition = `-${cue.x}px -${cue.y}px`;
    preview.style.display = "block";
    let left = x - tileW / 2;
    if (left < 0) left = 0;
    if (left + tileW > rect.width) left = rect.width - tileW;
    preview.style.left = left + "px";
  };
  const onLeave = () => { preview.style.display = "none"; };

  pcEl.addEventListener("mousemove", onMove);
  pcEl.addEventListener("mouseleave", onLeave);

  trickplayState.set(vjs, {
    cleanup: () => {
      pcEl.removeEventListener("mousemove", onMove);
      pcEl.removeEventListener("mouseleave", onLeave);
      try { preview.remove(); } catch {}
    },
  });
}

function detachTrickplayHover(vjs) {
  if (!vjs) return;
  const s = trickplayState.get(vjs);
  if (s && s.cleanup) s.cleanup();
  trickplayState.delete(vjs);
}

function parseThumbVTT(text) {
  const cues = [];
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/(\d+):(\d+):(\d+)[.,](\d+)\s*-->\s*(\d+):(\d+):(\d+)[.,](\d+)/);
    if (!m) continue;
    const start = (+m[1]) * 3600 + (+m[2]) * 60 + (+m[3]) + (+m[4]) / 1000;
    const end   = (+m[5]) * 3600 + (+m[6]) * 60 + (+m[7]) + (+m[8]) / 1000;
    const payload = (lines[i + 1] || "").trim();
    const xm = payload.match(/#xywh=(\d+),(\d+),(\d+),(\d+)/);
    if (!xm) continue;
    cues.push({ start, end, x: +xm[1], y: +xm[2], w: +xm[3], h: +xm[4] });
  }
  return cues;
}

function findCue(cues, t) {
  // Binärsuche: cues sind sortiert nach start
  let lo = 0, hi = cues.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const c = cues[mid];
    if (t < c.start) hi = mid - 1;
    else if (t >= c.end) lo = mid + 1;
    else return c;
  }
  return cues[Math.max(0, Math.min(cues.length - 1, lo - 1))];
}

async function applyPlayback(item, mode, profile, audioIdx, deinterlace) {
  const params = new URLSearchParams();
  if (mode) params.set("mode", mode);
  if (profile) params.set("profile", profile);
  if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
    params.set("audio", String(audioIdx));
  }
  // Deinterlace-Override: "auto" (Default), "on" oder "off". Bei "auto" lässt
  // der Server selbst anhand des field_order entscheiden.
  const deiVal = deinterlace || $("#deinterlaceSelect").value || "auto";
  if (deiVal && deiVal !== "auto") params.set("deinterlace", deiVal);
  const info = await api(`/api/playback/${item.id}?${params}`);
  state.playback = info;
  // virtualOffset: bei initialem Load 0; nach Seek-Restart auf den neuen Startpunkt gesetzt.
  state.playback.virtualOffset = 0;
  state.playback.audioIdx = audioIdx;
  // deinterlace im state, damit Progress-Poll dieselben Session-Keys hat wie
  // die Playback-Session (sonst spawnt der Server eine zweite ffmpeg-Instanz).
  state.playback.deinterlace = deiVal;

  // Profile-Select: bei Transcode = Zielprofil, bei Auto = Qualitäts-Maximum
  const currentMode = $("#modeSelect").value;
  const profSel = $("#profileSelect");
  profSel.innerHTML = "";
  for (const p of info.profiles || []) {
    const o = document.createElement("option");
    o.value = p.ID;
    // "orig" bei Auto als "Keine Beschränkung" ausgeben, sonst "Original (Qualität)"
    o.textContent = (currentMode === "auto" && p.ID === "orig") ? "Keine Beschränkung" : p.Label;
    profSel.appendChild(o);
  }
  profSel.value = info.profile || "orig";
  // Profil bei "Transcode" als erzwungenes Profil, bei "Auto" als Qualitäts-Maximum.
  // Bei "Direct Play" ausgeblendet (Originaldatei bleibt unverändert).
  const userMode = $("#modeSelect").value;
  const profileWrap = $("#profileWrap");
  if (userMode === "direct") {
    profileWrap.style.display = "none";
  } else {
    profileWrap.style.display = "";
    // Erstes Child-Textnode ersetzen: "Profil" bei Transcode, "Maximum" bei Auto
    const newLabel = userMode === "transcode" ? "Profil" : "Maximum";
    // Ersetze den ersten Text-Node im Label
    for (const n of profileWrap.childNodes) {
      if (n.nodeType === 3) { n.textContent = newLabel + " "; break; }
    }
  }

  // Deinterlace-Select: nur sichtbar im Transcode-Modus (Direct Play kann
  // ohnehin nicht deinterlacen). Default-Wahl folgt dem Server-Echo.
  const deiWrap = $("#deinterlaceWrap");
  if (deiWrap) {
    if (info.mode === "transcode") {
      deiWrap.style.display = "";
      const sel = $("#deinterlaceSelect");
      if (sel && info.deinterlace) sel.value = info.deinterlace;
      // Visuelles Hint: bei aktivem Deinterlace-Filter Label „· aktiv" anhängen
      for (const n of deiWrap.childNodes) {
        if (n.nodeType === 3) {
          n.textContent = info.deinterlaceActive ? "Deinterlace (aktiv) " : "Deinterlace ";
          break;
        }
      }
    } else {
      deiWrap.style.display = "none";
    }
  }

  // Audio-Tracks (nur bei Transcode wirksam — bei Direct Play übernimmt der Browser)
  const streams = info.streams || [];
  const audios = streams.filter(st => st.type === "audio");
  // Nur einblendbare Untertitel: unsere erzeugten (webvtt-generated / webvtt-ocr)
  // und echte Text-Subs. Bild-Untertitel (PGS/VOBSUB) fliegen komplett raus —
  // User-Wunsch 2026-08-31: "nicht einblendbare nicht mehr anzeigen".
  const subs = streams.filter(st => st.type === "subtitle" && !isBitmapSub(st.codec));
  const audioSel = $("#audioSelect");
  audioSel.innerHTML = "";
  if (audios.length > 1 && info.mode === "transcode") {
    for (const a of audios) {
      const o = document.createElement("option");
      o.value = String(a.index);
      const parts = [];
      if (a.language) parts.push(a.language.toUpperCase());
      if (a.title) parts.push(a.title);
      parts.push(a.codec || "");
      if (a.channels) parts.push(a.channels === 6 ? "5.1" : (a.channels + "ch"));
      o.textContent = parts.filter(Boolean).join(" · ");
      audioSel.appendChild(o);
    }
    if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
      audioSel.value = String(audioIdx);
    } else {
      const def = audios.find(a => a.isDefault) || audios[0];
      audioSel.value = String(def.index);
    }
    $("#audioWrap").style.display = "";
  } else {
    $("#audioWrap").style.display = "none";
  }

  // Untertitel: Liste mit "Aus" + allen Subtitle-Streams
  const subSel = $("#subSelect");
  subSel.innerHTML = "";
  if (subs.length > 0) {
    const off = document.createElement("option");
    off.value = ""; off.textContent = "— Aus —";
    subSel.appendChild(off);
    for (const t of subs) {
      const o = document.createElement("option");
      o.value = String(t.index);
      const parts = [];
      if (t.language) parts.push(t.language.toUpperCase());
      if (t.title) parts.push(t.title);
      if (t.isForced) parts.push("Forced");
      // Codec nur bei echten Text-Subs zeigen — bei unseren erzeugten ist der
      // Titel (📝 … (OCR) / 🎤 … (KI)) schon aussagekräftig.
      if (t.codec !== "webvtt-ocr" && t.codec !== "webvtt-generated") parts.push(t.codec || "");
      o.textContent = parts.filter(Boolean).join(" · ");
      subSel.appendChild(o);
    }
    // Vorwahl aus dem Detail-Dialog (Untertitel-Dropdown dort) übernehmen.
    if (state.detailPrefs && state.detailPrefs.itemId === item.id
        && state.detailPrefs.subValue != null
        && subSel.querySelector(`option[value="${String(state.detailPrefs.subValue).replace(/"/g, '\\"')}"]`)) {
      subSel.value = String(state.detailPrefs.subValue);
    }
    $("#subWrap").style.display = "";
  } else {
    $("#subWrap").style.display = "none";
  }

  // Metadaten-Footer
  // Player-Titel: bei TMDB-Match den richtigen Titel zeigen (bei Episoden
  // „Show — Episodentitel"), sonst Fallback auf den Dateinamen.
  let playerTitle = item.title;
  const md = item.metadata;
  if (md && md.title) {
    if (md.tmdbType === "episode") {
      const segs = (item.relPath || "").split("/");
      const showName = segs.length > 1 ? segs[0] : "";
      playerTitle = showName ? `${showName} — ${md.title}` : md.title;
    } else {
      playerTitle = md.title;
    }
  }
  $("#playerTitle").textContent = playerTitle;
  $("#playerMeta").innerHTML = `
    <span><strong>Format:</strong> ${escapeHTML((item.container || "").toUpperCase())}</span>
    <span><strong>Video:</strong> ${escapeHTML(item.videoCodec || "?")} ${item.width}×${item.height}</span>
    <span><strong>Audio:</strong> ${escapeHTML(item.audioCodec || "?")}</span>
    <span><strong>Laufzeit:</strong> ${fmtDuration(item.durationSec)}</span>
    <span><strong>Größe:</strong> ${fmtSize(item.sizeBytes)}</span>
    <span><strong>Veröffentlicht:</strong> ${fmtDate(item.releasedAt) || "—"}</span>
    <span><strong>Modus:</strong> ${info.mode === "direct" ? "Direct Play" : "Transcode"} (${escapeHTML(info.reason)})</span>
  `;

  // Resume-Position anwenden: bei Transcode die URL direkt mit start=<pos>
  // erzeugen (die Seek-Restart-Logik setzt dann virtualOffset korrekt).
  // Bei Direct Play wird die Position nach loadedmetadata via currentTime gesetzt.
  const resumeSec = state.pendingResumeSec || 0;
  state.pendingResumeSec = 0;
  let resumeForDirectPlay = 0;
  if (resumeSec > 0) {
    if (info.mode === "transcode") {
      const sep = info.url.includes("?") ? "&" : "?";
      info.url = `${info.url}${sep}start=${Math.floor(resumeSec)}`;
      state.playback.virtualOffset = resumeSec;
    } else {
      resumeForDirectPlay = resumeSec;
    }
  }
  // Cache-Bust + optional Server-Reset bei „Von Anfang":
  // - `_t=<now>` zwingt VHS, die Source als komplett neu zu behandeln (sonst
  //   übernimmt der Browser-interne State von einer früheren identischen URL).
  // - `fresh=1` (nur wenn resumeSec=0, also „Von Anfang"): zwingt den Server,
  //   eine evtl. existierende Session bei start=0 zu beenden und neu zu
  //   starten. Sonst hat die alte Session bereits viele Sekunden Material in
  //   ihrer Playlist und der Browser springt nicht zu Position 0.
  if (info.mode === "transcode") {
    const sep = info.url.includes("?") ? "&" : "?";
    let tail = `_t=${Date.now()}`;
    if (resumeSec === 0) {
      tail += "&fresh=1";
    }
    info.url = `${info.url}${sep}${tail}`;
  }
  // Für applyStartBufferGate: bei Transcode ist die Player-Local-Start-Zeit
  // immer 0 (der Offset steckt in der URL); bei Direct Play ist es die
  // Resume-Position bzw. 0 bei „Von Anfang".
  state.playback.startWantedSec = info.mode === "transcode" ? 0 : resumeForDirectPlay;

  // Cast-/AirPlay-Auth: Wenn der User den Stream auf einen externen Receiver
  // (AppleTV, Chromecast, FireTV) routet, holt das Gerät die URL SELBST vom
  // Server — ohne Browser-Cookie. Ein Session-Token im Query-Param ist die
  // einzige Möglichkeit, das ohne Auth-Bypass zu erlauben. Wir hängen ihn
  // proaktiv an die URL an: sowohl bei Direct Play (für AirPlay) als auch
  // bei Transcode (für Chromecast). Cookie-Auth bleibt parallel gültig — der
  // Browser nutzt weiterhin das Cookie, der Token ist nur für externe Geräte
  // relevant. Token wird einmal pro Player-Session geholt + gecacht.
  if (!state.castToken) {
    try {
      const r = await api("/api/auth/cast-token", { method: "POST" });
      if (r && r.token) state.castToken = r.token;
    } catch (e) { /* nicht-blockierend; Browser-Cookie reicht */ }
  }
  if (state.castToken) {
    const sep = info.url.includes("?") ? "&" : "?";
    info.url = `${info.url}${sep}session=${encodeURIComponent(state.castToken)}`;
  }

  // Video.js-Instanz bei Möglichkeit wiederverwenden (erhält Vollbild-Modus
  // beim Shuffle-Weiterschalten). Nur bei erstem Öffnen neu erzeugen.
  const srcType = info.mode === "transcode"
    ? "application/vnd.apple.mpegurl"
    : "video/mp4";
  const reuse = state.vjs && typeof state.vjs.isDisposed === "function" && !state.vjs.isDisposed();
  let vjs;
  if (reuse) {
    vjs = state.vjs;
    // Alte Remote-Text-Tracks entfernen + Handler-Flag zurücksetzen
    const rtt = vjs.remoteTextTracks();
    if (rtt) {
      for (let i = rtt.length - 1; i >= 0; i--) {
        try { vjs.removeRemoteTextTrack(rtt[i]); } catch {}
      }
    }
    const subSelReuse = $("#subSelect");
    if (subSelReuse) delete subSelReuse.dataset.subHandlerAttached;
    vjs.src({ src: info.url, type: srcType });
    // currentTime explizit setzen, sonst „erinnert" sich der wiederverwendete
    // Player an die letzte Position des vorherigen Streams. Direct Play:
    // resumeForDirectPlay (0 bei „Von Anfang"). Transcode: lokal IMMER 0,
    // weil der Resume-Offset bereits in der URL (start=…) steckt — ohne
    // expliziten Reset würde der Player bei der vorherigen lokalen Position
    // weiterspielen, was beim zweiten Open-Mit-Von-Anfang-Reuse das Symptom
    // „springt etwas weiter, nicht zur Resume-Pos und nicht zu 0" erzeugt.
    const localStart = info.mode === "direct" ? (resumeForDirectPlay || 0) : 0;
    vjs.one("loadedmetadata", () => { try { vjs.currentTime(localStart); } catch {} });
    const pp = vjs.play();
    if (pp && typeof pp.catch === "function") pp.catch(() => {});
  } else {
    disposePlayer();
    const el = $("#video");
    vjs = window.videojs(el, {
      autoplay: true,
      controls: true,
      preload: "auto",
      fluid: false,
      // responsive:false — wir nutzen einen Modal-Player mit stabiler Breite;
      // responsive=true hat bei Dialog-Anzeige fehlerhaft `vjs-layout-tiny`
      // gesetzt und Progress-Bar/Zeitanzeige versteckt.
      responsive: false,
      // liveui:false — unsere HLS-Transcodes nutzen `hls_playlist_type=event`;
      // Video.js' LiveTracker interpretiert das als Live-Stream und blendet
      // die Progress-Bar weg. Da es bei uns immer VOD ist → Live-UI deaktivieren.
      liveui: false,
      playbackRates: [0.5, 1, 1.25, 1.5, 2],
      html5: {
        vhs: {
          // Ziel-Puffer beim Abspielen (entspricht dem Settings-Slider).
          GOAL_BUFFER_LENGTH: state.settings.bufferSeconds,
          // Obergrenze bewusst hoch (30 min), damit VHS bei Pause weiterhin
          // Segmente vorlädt statt zu stoppen. Browser-Memory ist bei
          // HLS-Segmenten moderat, der User hat das explizit so gewünscht.
          MAX_GOAL_BUFFER_LENGTH: 1800,
          // Segment-Pre-Fetch-Budget proportional anheben, sonst bremst VHS
          // intern nach wenigen Segmenten.
          BANDWIDTH_VARIANCE: 1.2,
          // overrideNative: Safari hat eine eigene native HLS-Engine, die
          // unsere progressive EVENT-Playlist (kein ENDLIST) als „media
          // aborted/corruption" abbricht. VHS-Pfad zwingt Safari in den
          // gleichen MSE-Code wie Chrome/Firefox — VHS transmuxt MPEG-TS
          // zu fMP4 für Safaris MSE. Voraussetzung: H.264 + AAC im Output
          // (unser Transcode liefert genau das, ac3 wird re-encoded).
          overrideNative: true,
        },
      },
    });
    vjs.src({ src: info.url, type: srcType });
    // currentTime explizit setzen — siehe Reuse-Branch oben für Begründung.
    // Bei Transcode lokal 0 (Resume-Offset steckt in der URL als start=…),
    // bei Direct Play die Resume-Pos bzw. 0 bei „Von Anfang".
    {
      const localStart0 = info.mode === "direct" ? (resumeForDirectPlay || 0) : 0;
      vjs.one("loadedmetadata", () => { try { vjs.currentTime(localStart0); } catch {} });
    }
    state.vjs = vjs;
    vjs.on("timeupdate", () => {
      maybeMarkWatched(vjs);
      maybeToggleIntroSkip(vjs);
    });
    vjs.on("ended", () => {
      // Wiedergabe durchgespielt → Resume-Marker löschen + als gesehen markieren.
      // Letzteres ist Sicherheitsnetz: maybeMarkWatched feuert idealerweise
      // schon bei 90 %, aber falls timeupdate-Events kurz vor Ende ausgelassen
      // werden (Buffering, Tab-Wechsel) fängt das ended-Event es hier auf.
      if (state.currentItem) {
        api(`/api/items/${state.currentItem.id}/resume`, {
          method: "PUT",
          body: JSON.stringify({ positionSec: 0 }),
        }).catch(() => {});
        markWatchedNow(state.currentItem);
      }
      if (state.currentPlaylist) playNextInQueue();
    });
    // Resume-Position regelmäßig speichern (throttled auf 10s-Takt) + bei Pause.
    let lastSaved = 0;
    const saveResume = () => {
      if (!state.currentItem) return;
      const cur = vjs.currentTime();
      if (!isFinite(cur) || cur < 5) return;
      const now = Date.now();
      if (now - lastSaved < 10_000) return;
      lastSaved = now;
      // Absolute Position (Transcode-Seek-Restart rechnet virtualOffset ein)
      const abs = cur + ((state.playback && state.playback.virtualOffset) || 0);
      api(`/api/items/${state.currentItem.id}/resume`, {
        method: "PUT",
        body: JSON.stringify({ positionSec: abs }),
      }).catch(() => {});
    };
    vjs.on("timeupdate", saveResume);
    vjs.on("pause", saveResume);
    // Pause-Prefetch: VHS laedt im Pause-Zustand selbst nur ~1 Segment vor.
    // Der Server transkodiert im Hintergrund aber unabhaengig weiter — waehrend
    // der Pause holen wir die bereits fertigen, aber noch nicht abgespielten
    // Segmente per eigenem fetch() in den Browser-HTTP-Cache. VHS bedient sich
    // beim Weiterspielen dann daraus statt erneut ueber die (ggf. langsame)
    // Leitung zu muessen.
    vjs.on("pause", () => startPausePrefetch(vjs));
    vjs.on("play", stopPausePrefetch);
    vjs.on("ended", stopPausePrefetch);
    // Dialog-Resize → Video.js intern neu layouten (ControlBar-Breite, Tech-Size).
    // Ohne diesen Push reagiert der Player nicht auf manuelles Ziehen der Dialog-Ecke.
    attachPlayerResizeObserver(vjs);
    // Seek-Restart: beim Transcode endet die Playlist am aktuell produzierten
    // Segment. Klickt der User weiter vorne, würde Video.js den Seek-Target aufs
    // Seekable-Ende clamp'en → sieht aus wie "nur ein paar Sekunden vorwärts".
    // Lösung: Klicks in den Progress-Control-Bereich abfangen (Capture-Phase),
    // bei Ziel-Position > seekable-Ende den Transcode neu starten.
    attachSeekRestart(vjs);
    syncTranscodeDisplays(vjs);
    // Overlay (#transcodeAhead) bei Fullscreen in den Video.js-Root verschieben,
    // damit es im Fullscreen-Modus sichtbar bleibt (Fullscreen-Element zeigt nur
    // sich selbst + Nachfahren). Bei Exit zurück in .video-stage.
    vjs.on("fullscreenchange", () => {
      positionBufferOverlay(vjs);
    });
  }

  // Overlay-Sichtbarkeit wird per CSS an die Video.js-Klassen
  // `.vjs-user-active` / `.vjs-user-inactive.vjs-playing` gekoppelt (fadet
  // gemeinsam mit der Progress-Bar). Position je nach Fullscreen:
  //   Fullscreen  → inside vjs.el(), floating top-right, mit Titel
  //   Normal-View → docked im Footer, ohne Titel (Titel steht eh im Header)
  {
    const overlay = document.getElementById("transcodeAhead");
    if (overlay) overlay.classList.remove("hidden");
  }
  positionBufferOverlay(vjs);

  // Custom-Buttons direkt VOR dem Fullscreen-Toggle einfügen — am Ende der ControlBar.
  // Früher Einfügen (Index 1-4) quetscht im responsive-Layout die Progress-Bar raus.
  ensurePlayerComponents();
  const cb = vjs.getChild("controlBar");
  if (cb) {
    // Skip-Buttons direkt nach PlayToggle einsortieren — typisches UX-Pattern
    // (links, neben Play). Falls PlayToggle nicht gefunden, am Anfang.
    const playIdx = cb.children().findIndex(c => c.name_ === "PlayToggle");
    const skipBase = playIdx >= 0 ? playIdx + 1 : 0;
    const addAt = (name, idx) => {
      if (!cb.getChild(name)) cb.addChild(name, {}, idx);
    };
    addAt("Skip15Back", skipBase);
    addAt("Skip30Forward", skipBase + 1);
    // Custom-Buttons rechts (vor FullscreenToggle) — am Ende der ControlBar.
    // Counter statt fester Offsets: wenn ein optionaler Button uebersprungen
    // wird (AirPlay nur bei Direct Play, Delete nur bei Admin), darf das
    // KEIN „Loch" hinterlassen — sonst landen spaetere Buttons hinter
    // FullscreenToggle und Vollbild ist nicht mehr ganz aussen rechts.
    const fsIdx = cb.children().findIndex(c => c.name_ === "FullscreenToggle");
    const insertAt = fsIdx >= 0 ? fsIdx : cb.children().length;
    let nextOffset = 0;
    const addIfMissing = (name) => {
      if (!cb.getChild(name)) {
        cb.addChild(name, {}, insertAt + nextOffset);
      }
      nextOffset++;
    };
    addIfMissing("ShufflePrev");
    addIfMissing("ShuffleNext");
    addIfMissing("FavoriteButton");
    addIfMissing("PlaylistButton");
    // Cast-Button — bleibt unsichtbar, bis das Cast-Framework geladen ist
    // (initCastFramework markiert state.castReady und ruft btn.show()).
    addIfMissing("CastButton");
    // AirPlay-Button NUR bei Direct Play hinzufügen. Bei Transcode (HLS via
    // VHS-MSE) zeigt Safari zwar den AirPlay-Picker, kann den Stream aber
    // nicht an den AppleTV weiterreichen — der Spinner dreht sich auf dem
    // AppleTV ohne dass je Frames ankommen. Apple unterstützt AirPlay-
    // Routing nur, wenn das <video>-Element direkt eine Source-URL liest
    // (progressives MP4 = Direct Play). Bei Transcode-Items gibt's stattdessen
    // den Hinweis im UI: macOS-Bildschirmsynchronisierung verwenden.
    if ((info && info.mode) === "direct") {
      addIfMissing("AirPlayButton");
    }
    // Löschbutton nur für Admins; liegt direkt vor FullscreenToggle.
    if (state.me && state.me.isAdmin) {
      addIfMissing("DeleteButton");
    }
  }
  wireIntroSkipOverlayOnce();
  updatePlayerButtons();

  // Puffer-Overlay starten. Bei Transcode zeigen wir zusätzlich den Server-Vorlauf
  // (ffmpeg vs. Playback), bei Direct Play nur den Client-Buffer.
  startBufferDisplay(item, info.mode, info.profile || "orig", audioIdx);
  // Optionaler Pre-Buffer-Gate: wenn startBufferSeconds > 0 gesetzt ist, pausiert
  // der Player zu Beginn, bis so viele Sekunden gepuffert sind.
  applyStartBufferGate(vjs);
  if (info.mode === "transcode") {
    // Beim Transcode hat die wachsende HLS-Playlist keine bekannte Gesamtlänge
    // (kein ENDLIST). Video.js würde dann eine live-artige Dauer liefern →
    // Progress-Bar nie voll, Trickplay-Hover rechnet falsche Zeit. Wir forcen
    // die echte Filmlänge aus ffprobe als duration-Cache.
    forcePlayerDuration(vjs, item.durationSec);
  } else {
    releasePlayerDuration(vjs);
  }

  // Trickplay-Hover-Thumbnails: eigenes Mini-Plugin, inline in app.js.
  if (item.trickplayStatus === "done") {
    try { attachTrickplayHover(vjs, `/api/trickplay/${item.id}/thumbs.vtt`); }
    catch (e) { console.warn("trickplay init:", e); }
  } else {
    detachTrickplayHover(vjs);
  }

  // Subtitle-Verwaltung: Track laden + Change-Handler auf dem Dropdown
  applySubtitleChoice(vjs, item, subs);
  const subSelEl = $("#subSelect");
  if (subSelEl && !subSelEl.dataset.subHandlerAttached) {
    subSelEl.dataset.subHandlerAttached = "1";
    subSelEl.addEventListener("change", () => applySubtitleChoice(vjs, item, subs));
  }
}

// vttShiftTimestamps verschiebt alle Cue-Zeiten in einem WebVTT-Text um
// -offsetSec — nötig im Transcode-Modus nach einem Seek-Restart: der
// <video>-Element-Zeitstempel zählt dann ab dem Segment-Start (0), die
// VTT-Cues aber ab absoluter Filmzeit. Ohne Shift wären die Untertitel um
// virtualOffset Sekunden versetzt (oder erschienen nie, wenn virtualOffset
// > Cue-Zeit).
function vttShiftTimestamps(vttText, offsetSec) {
  if (!offsetSec) return vttText;
  const toSec = (t) => {
    const p = t.split(":").map(Number);
    return p.length === 3 ? p[0] * 3600 + p[1] * 60 + p[2] : p[0] * 60 + p[1];
  };
  const fmt = (s) => {
    if (s < 0) s = 0;
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
    return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${sec.toFixed(3).padStart(6, "0")}`;
  };
  const TS = String.raw`\d{1,2}:\d{2}:\d{2}\.\d{1,3}|\d{2}:\d{2}\.\d{1,3}`;
  const re = new RegExp(`(${TS})\\s*-->\\s*(${TS})(.*)`, "g");
  return vttText.replace(re, (m, a, b, rest) => `${fmt(toSec(a) - offsetSec)} --> ${fmt(toSec(b) - offsetSec)}${rest}`);
}

// applySubtitleChoice entfernt alle vorhandenen Subtitle-Tracks und lädt den
// aktuell gewählten neu. Wird beim Player-Open und bei jeder Dropdown-Änderung
// aufgerufen. Lädt die VTT selbst per fetch (statt sie Video.js über `src`
// laden zu lassen) — so können wir (a) Ladefehler melden, (b) im
// Transcode-Modus die Zeitstempel um virtualOffset verschieben, (c) den Track
// zuverlässig aktivieren.
async function applySubtitleChoice(vjs, item, subs) {
  // Nur echte Untertitel-Tracks entfernen (Trickplay-Metadata-Track bleibt).
  const existing = vjs.remoteTextTracks();
  for (let i = existing.length - 1; i >= 0; i--) {
    const t = existing[i];
    if (t && (t.kind === "subtitles" || t.kind === "captions")) {
      vjs.removeRemoteTextTrack(t);
    }
  }

  const subChoice = $("#subSelect").value;
  if (!subChoice) return;

  const sub = subs.find(s => String(s.index) === subChoice);
  if (sub && isBitmapSub(sub.codec)) {
    showToast("Bild-Untertitel (" + (sub.codec || "PGS") + ") koennen nicht direkt eingeblendet werden. " +
      "Im Zahnrad-Menue unter 'OCR-Untertitel erzeugen' lassen sie sich per Texterkennung im Hintergrund " +
      "erstellen — danach erscheinen sie hier als 'OCR'-Eintrag.",
      { kind: "info", duration: 8000 });
    $("#subSelect").value = "";
    return;
  }
  const label = (sub && sub.title) || (sub && sub.language && sub.language.toUpperCase()) || "Untertitel";
  const subSrc = (sub && sub.codec === "webvtt-generated")
    ? `/api/generated-subtitle/${item.id}/${sub.language}.vtt`
    : (sub && sub.codec === "webvtt-ocr")
      ? `/api/ocr-subtitle/${item.id}/${sub.language}.vtt`
      : `/api/subtitle/${item.id}/${subChoice}.vtt`;

  let vttText;
  try {
    const res = await fetch(subSrc, { credentials: "same-origin" });
    if (!res.ok) throw new Error("HTTP " + res.status);
    vttText = await res.text();
  } catch (e) {
    console.warn("[subs] Laden fehlgeschlagen:", subSrc, e);
    showToast("Untertitel konnte nicht geladen werden (" + e.message + ")", { kind: "error" });
    return;
  }
  if (!/^\uFEFF?WEBVTT/.test(vttText)) {
    console.warn("[subs] keine gültige WebVTT-Antwort von", subSrc, vttText.slice(0, 60));
    showToast("Untertitel-Datei ist keine gültige WebVTT.", { kind: "error" });
    return;
  }

  const info = state.playback || {};
  if (info.mode === "transcode" && info.virtualOffset) {
    vttText = vttShiftTimestamps(vttText, info.virtualOffset);
  }
  const blobUrl = URL.createObjectURL(new Blob([vttText], { type: "text/vtt" }));

  const trackEl = vjs.addRemoteTextTrack({
    kind: "subtitles",
    src: blobUrl,
    srclang: (sub && sub.language) || "und",
    label: label,
    default: true,
  }, false);
  // Blob-URL nach kurzer Zeit freigeben (Track hat dann geladen).
  setTimeout(() => { try { URL.revokeObjectURL(blobUrl); } catch (e) {} }, 60000);

  const show = () => {
    try {
      if (trackEl && trackEl.track) { trackEl.track.mode = "showing"; return true; }
    } catch (e) {}
    const tracks = vjs.textTracks();
    for (let i = 0; i < tracks.length; i++) {
      if (tracks[i].label === label) { tracks[i].mode = "showing"; return true; }
    }
    return false;
  };
  show();
  setTimeout(show, 150);
  setTimeout(show, 600);
  setTimeout(show, 1500);
}

const playerResizeObservers = new WeakMap();
function attachPlayerResizeObserver(vjs) {
  if (!window.ResizeObserver) return;
  const dlg = $("#playerDialog");
  if (!dlg) return;
  const ro = new ResizeObserver(() => {
    if (!vjs || (typeof vjs.isDisposed === "function" && vjs.isDisposed())) return;
    try { vjs.trigger("playerresize"); } catch {}
  });
  ro.observe(dlg);
  playerResizeObservers.set(vjs, ro);
}
function detachPlayerResizeObserver(vjs) {
  const ro = playerResizeObservers.get(vjs);
  if (ro) try { ro.disconnect(); } catch {}
  playerResizeObservers.delete(vjs);
}

function disposePlayer() {
  hideBufferOverlay();
  clearStartBufferGate();
  stopPausePrefetch();
  if (state.vjs) {
    detachTrickplayHover(state.vjs);
    detachPlayerResizeObserver(state.vjs);
    try { state.vjs.dispose(); } catch {}
    state.vjs = null;
  }
  // Video.js ersetzt das <video>-Element beim dispose — wir müssen es neu einfügen.
  const stage = document.querySelector(".video-stage");
  const existing = document.getElementById("video");
  if (!existing && stage) {
    const v = document.createElement("video");
    v.id = "video";
    v.className = "video-js vjs-big-play-centered";
    v.setAttribute("controls", "");
    v.setAttribute("playsinline", "");
    v.setAttribute("preload", "auto");
    // AirPlay-Erlaubnis (Safari/iOS) — siehe index.html.
    v.setAttribute("x-webkit-airplay", "allow");
    v.setAttribute("airplay", "allow");
    stage.appendChild(v);
  }
  // Overlay zurück in die Docked-Position (.player-wrap). Falls es gerade im
  // Fullscreen im vjs.el() saß, wäre es jetzt am verschwindenden Root und
  // würde beim nächsten Open am falschen Platz landen.
  const overlay = document.getElementById("transcodeAhead");
  const wrap = document.querySelector(".player-wrap");
  const meta = document.getElementById("playerMeta");
  if (overlay && wrap && meta && overlay.parentElement !== wrap) {
    wrap.insertBefore(overlay, meta);
    overlay.classList.add("transcode-ahead--docked");
  }
}

// Transcode-Progress: alle 2s den Server fragen, bis ffmpeg fertig ist oder
// der Player geschlossen wird. Zeigt "+N s" Puffer-Abstand.
// Hält Zeit-/Progress-Anzeige absolut, obwohl die HLS-Session tech-seitig nach
// Seek-Restart bei 0 beginnt. Läuft auf requestAnimationFrame, damit Video.js'
// eigene SeekBar-Updates uns nicht ständig überschreiben.
function syncTranscodeDisplays(vjs) {
  if (!vjs || !vjs.el) return;
  // Re-Entry-Schutz: bei Player-Reuse (Shuffle-Next, restartTranscodeAt) ruft
  // applyPlayback erneut auf — früher startete jedes Mal ein WEITERER RAF-
  // Loop, alte liefen parallel weiter. Symptom: konkatenierte Zeit-Strings
  // („6:500:000:010:020:031:39…") oder verschwundene Status-Leiste, weil
  // mehrere Loops in dieselben (oder duplizierten) Display-Spans schreiben.
  if (vjs._transcodeDisplaysActive) return;
  vjs._transcodeDisplaysActive = true;
  const root = vjs.el();
  // Video.js' eigene TimeDisplay-Updates ausschalten, sobald wir im Transcode-
  // Modus sind — sonst schreiben Video.js und unser RAF-Loop abwechselnd Text
  // in die Elemente → Flackern. Original-Methode bleibt erhalten für Direct Play.
  const cb = vjs.getChild("controlBar");
  if (cb) {
    for (const name of ["currentTimeDisplay", "durationDisplay", "remainingTimeDisplay"]) {
      const c = cb.getChild(name);
      if (!c || typeof c.updateContent !== "function" || c._patchedForTranscode) continue;
      const orig = c.updateContent.bind(c);
      c.updateContent = function (ev) {
        if (state.playback && state.playback.mode === "transcode") return; // wir übernehmen
        return orig(ev);
      };
      c._patchedForTranscode = true;
    }
    // Genauso die SeekBar: Video.js aktualisiert die `.vjs-play-progress`-
    // Breite selbst (timeupdate + eigener RAF), rechnet dabei aber gegen die
    // wachsende EVENT-Playlist-/Live-Dauer statt der forcierten Filmlaenge.
    // Parallel setzt unser RAF-Loop unten dieselbe Breite auf den korrekten
    // (absoluten) Wert → die beiden schreiben abwechselnd unterschiedliche
    // Positionen → Fortschrittsbalken flackert waehrend der Wiedergabe. Im
    // Transcode-Modus deshalb Video.js' SeekBar-Update aussetzen, unser RAF
    // uebernimmt; Direct Play laeuft unveraendert ueber Video.js.
    const pc = cb.getChild("progressControl");
    const sb = pc && pc.getChild("seekBar");
    if (sb && typeof sb.update === "function" && !sb._patchedForTranscode) {
      const origUpdate = sb.update.bind(sb);
      sb.update = function () {
        if (state.playback && state.playback.mode === "transcode") return;
        return origUpdate();
      };
      sb._patchedForTranscode = true;
    }
  }
  // Display-Span-Update OHNE textContent — sonst loggt Video.js
  // „TimeDisplay#updateTextnode_: Prevented replacement of text node element"
  // und akkumuliert TextNodes nebeneinander („6:500:000:010:020:031:39…").
  // Grund: Video.js' TimeDisplay hält eine interne Referenz auf den ersten
  // Text-Node. textContent ersetzt alle Children → Referenz wird ungültig →
  // beim nächsten Internal-Update macht Video.js appendChild statt
  // replaceChild. Workaround: ersten TextNode behalten, dessen nodeValue
  // setzen; alle weiteren TextNodes entfernen.
  const setText = (el, text) => {
    let first = null;
    for (let i = el.childNodes.length - 1; i >= 0; i--) {
      const c = el.childNodes[i];
      if (c.nodeType !== Node.TEXT_NODE) continue;
      if (first) el.removeChild(c);
      else first = c;
    }
    if (first) {
      if (first.nodeValue !== text) first.nodeValue = text;
    } else {
      el.appendChild(document.createTextNode(text));
    }
  };
  const setAll = (sel, text) => {
    const list = root.querySelectorAll(sel);
    for (let i = 0; i < list.length; i++) setText(list[i], text);
  };
  let rafId = 0;
  const tick = () => {
    if (!state.vjs || state.vjs !== vjs || (typeof vjs.isDisposed === "function" && vjs.isDisposed())) {
      vjs._transcodeDisplaysActive = false;
      return; // Loop beendet sich selbst
    }
    if (state.playback && state.playback.mode === "transcode") {
      const offset = state.playback.virtualOffset || 0;
      const total = (state.currentItem && state.currentItem.durationSec) || 0;
      if (total > 0) {
        const absCur = Math.max(0, vjs.currentTime() + offset);
        setAll(".vjs-current-time-display", formatPlayerTime(absCur));
        setAll(".vjs-duration-display", formatPlayerTime(total));
        setAll(".vjs-remaining-time-display", "-" + formatPlayerTime(Math.max(0, total - absCur)));
        const progs = root.querySelectorAll(".vjs-play-progress");
        for (let i = 0; i < progs.length; i++) {
          progs[i].style.width = Math.min(100, (absCur / total) * 100) + "%";
          const tt = progs[i].querySelector(".vjs-time-tooltip");
          if (tt) tt.textContent = formatPlayerTime(absCur);
        }
        const seekable = vjs.seekable();
        const seekableEnd = seekable && seekable.length ? seekable.end(0) : 0;
        const absLoad = seekableEnd + offset;
        const loads = root.querySelectorAll(".vjs-load-progress");
        for (let i = 0; i < loads.length; i++) {
          loads[i].style.width = Math.min(100, (absLoad / total) * 100) + "%";
        }
      }
    }
    rafId = requestAnimationFrame(tick);
  };
  rafId = requestAnimationFrame(tick);
  vjs.on("dispose", () => {
    if (rafId) cancelAnimationFrame(rafId);
    vjs._transcodeDisplaysActive = false;
  });
}

function formatPlayerTime(sec) {
  sec = Math.floor(sec || 0);
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  const pad = (n) => (n < 10 ? "0" + n : "" + n);
  if (h > 0) return `${h}:${pad(m)}:${pad(s)}`;
  return `${m}:${pad(s)}`;
}

// Seek-Restart für Transcode: wenn der User in der Progress-Bar jenseits des
// bereits transkodierten Bereichs klickt, würde Video.js den Ziel-Wert an den
// seekable-Range clampen ("nur ein paar Sekunden vorwärts"). Stattdessen fangen
// wir den Klick ab (Capture-Phase am progressControl) und starten die
// Transcode-Session an der gewünschten Position neu. Klicks innerhalb des
// transkodierten Bereichs leiten wir mit Offset-Korrektur an vjs.currentTime weiter.
function attachSeekRestart(vjs) {
  const cb = vjs.getChild("controlBar");
  const pc = cb && cb.getChild("progressControl");
  if (!pc || !pc.el) return;
  const pcEl = pc.el();
  const handler = (ev) => {
    if (!state.playback || state.playback.mode !== "transcode") return;
    const holder = pcEl.querySelector(".vjs-progress-holder") || pcEl;
    const rect = holder.getBoundingClientRect();
    if (rect.width <= 0) return;
    const cx = ev.clientX !== undefined
      ? ev.clientX
      : (ev.touches && ev.touches[0] ? ev.touches[0].clientX : undefined);
    if (cx === undefined) return;
    if (cx < rect.left || cx > rect.right) return;
    const x = Math.max(0, Math.min(rect.width, cx - rect.left));
    const ratio = x / rect.width;
    const total = (state.currentItem && state.currentItem.durationSec) || 0;
    if (total <= 0) return;
    // Ziel = absolute Zeit in der Original-Datei (Progress-Bar zeigt 0..total).
    const absoluteTarget = ratio * total;
    const offset = state.playback.virtualOffset || 0;
    const seekable = vjs.seekable();
    const seekableEnd = seekable && seekable.length ? seekable.end(0) : 0;
    // Absolutes Ende des bereits Transkodierten
    const absSeekableEnd = seekableEnd + offset;
    ev.stopImmediatePropagation();
    ev.preventDefault();
    if (absoluteTarget >= offset && absoluteTarget <= absSeekableEnd + 3) {
      // Ziel liegt in der aktuellen Session → tech-seitig mit Offset seeken
      try { vjs.currentTime(absoluteTarget - offset); } catch {}
    } else {
      // Außerhalb → neue Session am Ziel starten
      restartTranscodeAt(absoluteTarget);
    }
  };
  pcEl.addEventListener("mousedown", handler, true);
  pcEl.addEventListener("pointerdown", handler, true);
  pcEl.addEventListener("touchstart", handler, true);
}

async function restartTranscodeAt(absoluteStart) {
  const item = state.currentItem;
  const vjs = state.vjs;
  if (!item || !vjs) return;
  const info = state.playback;
  const profile = info.profile || "orig";
  const audioIdx = info.audioIdx;
  const params = new URLSearchParams({
    profile,
    start: String(Math.floor(absoluteStart)),
  });
  if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
    params.set("audio", String(audioIdx));
  }
  const newUrl = `/api/transcode/${item.id}/index.m3u8?${params}`;
  // State aktualisieren: virtualOffset für Progress-Anzeige + Display-Berechnung.
  state.playback.url = newUrl;
  state.playback.virtualOffset = absoluteStart;
  // Forced Duration bleibt total; Progress-Bar wird per syncTranscodeDisplays
  // manuell auf die absolute Position gesetzt.
  forcePlayerDuration(vjs, item.durationSec || 0);
  // Buffer-Overlay mit neuer Session neu starten (Progress-Polling mit neuem start)
  stopTranscodeProgress();
  // Alter Prefetch zielt noch auf die alte Session-URL (anderer start=) — verwerfen.
  stopPausePrefetch();
  // Neue Source laden
  vjs.src({ src: newUrl, type: "application/vnd.apple.mpegurl" });
  vjs.one("loadedmetadata", () => {
    vjs.play().catch(() => {});
    // Buffer-Anzeige neu starten (Transcode-Progress liest die neue Session)
    startBufferDisplayWithStart(item, "transcode", profile, audioIdx, Math.floor(absoluteStart));
  });
}

// Variante, die beim Progress-Endpoint explizit `start=` mitgibt.
function startBufferDisplayWithStart(item, mode, profile, audioIdx, startSec) {
  const orig = state.playback;
  // temporärer Override: Start-Param in die URL des Progress-Endpoints einfließen
  // lassen. Wir wrappen startBufferDisplay indem wir den URL-Builder dort ersetzen.
  state.__transcodeStartOverride = startSec;
  startBufferDisplay(item, mode, profile, audioIdx);
  void orig;
}

// applyStartBufferGate pausiert den Player zu Beginn, bis mindestens
// `startBufferSeconds` Sekunden vorgeladen sind. Zeigt währenddessen ein
// Overlay mit Fortschritt + Skip-Button.
//
// Wichtig für HLS-Transcode: Video.js/VHS lädt bei einer EVENT-Playlist
// Segmente nahe `currentTime`. Ohne expliziten Seek auf 0 würde VHS
// irgendwo in der Playlist puffern (oder gar nicht), und buffered() am
// currentTime wäre leer. Darum forcen wir currentTime(0) beim Start
// UND beim Release — sonst springt der Player an die Live-Kante und
// der Film beginnt mittendrin.
function applyStartBufferGate(vjs) {
  if (!vjs) return;
  clearStartBufferGate();
  const target = Number(state.settings && state.settings.startBufferSeconds) || 0;
  if (target <= 0) return;

  const overlay = $("#prebufferOverlay");
  const bar = overlay && overlay.querySelector(".prebuffer-bar");
  const text = overlay && overlay.querySelector(".prebuffer-text");
  const skip = overlay && overlay.querySelector("#prebufferSkip");

  const wantedPos = (state.playback && state.playback.startWantedSec) || 0;
  const isTranscode = state.playback && state.playback.mode === "transcode";
  console.log("[gate] start — mode=%s wantedPos=%s target=%s", state.playback && state.playback.mode, wantedPos, target);

  // Progress-URL für Transcode: ffmpeg-Position ist DER zuverlässige Puffer-
  // Indikator, nicht der Client-Buffer. VHS bufffert im Pause-Zustand absichtlich
  // nur ein Segment ahead — mehr liefert GOAL_BUFFER_LENGTH nicht, solange
  // nicht gespielt wird. Wir messen stattdessen, wie weit der Server bereits
  // transcodiert hat.
  const progressURL = () => {
    const p = new URLSearchParams({ profile: (state.playback && state.playback.profile) || "orig" });
    const aIdx = state.playback && state.playback.audioIdx;
    if (typeof aIdx === "number" && aIdx >= 0) p.set("audio", String(aIdx));
    if (state.playback && state.playback.virtualOffset) {
      p.set("start", String(Math.floor(state.playback.virtualOffset)));
    }
    return `/api/transcode/${state.currentItem && state.currentItem.id}/progress?${p}`;
  };

  let gateActive = true;
  let releasing = false;

  const seekToWanted = () => {
    try {
      if (Math.abs((vjs.currentTime() || 0) - wantedPos) > 0.5) {
        vjs.currentTime(wantedPos);
      }
    } catch {}
  };

  const release = (reason) => {
    if (!gateActive) return;
    gateActive = false;
    releasing = true;
    vjs.off("play", onPlayGate);
    clearStartBufferGate();
    if (overlay) overlay.classList.add("hidden");
    // VOR dem play() einmal auf Soll-Position snappen — dann play().
    seekToWanted();
    try {
      const pp = vjs.play();
      if (pp && typeof pp.catch === "function") pp.catch(() => {});
    } catch {}
    if (reason) showToast(`Start: ${reason}`, { kind: "success", duration: 1500 });
  };

  // Repeat-Pause-Listener — wird erst AFTER dem Initial-Kick-off aktiviert,
  // verhindert unerwünschtes Weiterspielen (z.B. durch Video.js-Autoplay).
  // `onSeeked`-Schleife wurde entfernt: die hat VHS' internen Segment-Loader
  // nach einem Segment blockiert (Seek-Loop). Wir seeken nur EINMAL beim
  // Pausieren und verlassen uns darauf, dass VHS die Goal-Buffer-Logik
  // alleine abarbeitet.
  const onPlayGate = () => {
    if (!gateActive || releasing) return;
    try { vjs.pause(); } catch {}
  };

  // VHS lädt im Pause-Zustand KEINE Segmente — Goal-Buffer greift erst nach
  // dem ersten Segment-Append. Darum: anspielen bis der erste Frame da ist,
  // dann pausieren und zurück auf wantedPos. Wir warten auf `canplay` /
  // `progress` statt auf ein festes Timeout — bei einer frisch gestarteten
  // ffmpeg-Session dauert es oft mehrere Sekunden, bis das erste Segment
  // verfügbar ist.
  if (overlay) {
    overlay.classList.remove("hidden");
    if (bar) bar.style.width = "0%";
    if (text) text.textContent = `0 / ${target} s`;
  }
  if (skip) skip.onclick = () => release("manuell gestartet");

  // Effektives Ziel auf den Haupt-Buffer cappen — VHS lädt nicht über
  // GOAL_BUFFER_LENGTH hinaus, also wäre ein höheres Ziel nie erreichbar.
  const goalBuffer = Number(state.settings && state.settings.bufferSeconds) || 30;
  const cappedTarget = Math.min(target, Math.max(5, goalBuffer - 2));

  let kicked = false;
  const startPolling = () => {
    if (!gateActive || kicked) return;
    kicked = true;
    vjs.off("canplay", onFirstReady);
    vjs.off("progress", onFirstReady);
    if (readyTimer) clearTimeout(readyTimer);
    try { vjs.pause(); } catch {}
    // Einmalig auf wantedPos seeken. Danach NICHT mehr aggressiv snappen —
    // sonst blockieren die seeked-Events VHS' Segment-Loader (Symptom:
    // Buffer bleibt bei ~5 s = genau ein Segment hängen).
    seekToWanted();
    vjs.on("play", onPlayGate);
    console.log("[gate] kicked off, pipeline aktiv — polling startet");
  };
  const onFirstReady = () => {
    const br = vjs.buffered();
    // Nur akzeptieren wenn echter Buffer vorhanden ist (nicht [0,0])
    if (br.length > 0 && br.end(br.length - 1) > 0.5) startPolling();
  };
  vjs.on("canplay", onFirstReady);
  vjs.on("progress", onFirstReady);
  // Safety-Fallback: nach 5 s sicher pausieren, auch wenn kein Event kam.
  const readyTimer = setTimeout(startPolling, 5000);

  // Kick-off: Play triggert VHS-Segment-Loader.
  try {
    const pp = vjs.play();
    if (pp && typeof pp.catch === "function") pp.catch((err) => {
      console.warn("[gate] play() abgelehnt:", err && err.message);
      // Falls Autoplay geblockt ist: kein Kick-off möglich. Polling starten
      // und hoffen dass der User manuell „Jetzt starten" drückt.
      startPolling();
    });
  } catch (e) {
    console.warn("[gate] play() throw:", e);
    startPolling();
  }

  // Hilfsfunktion: beste „Buffer ahead"-Schätzung. Primär die Range, die
  // wantedPos enthält. Falls keine: größte Range im Stream (Fallback — kommt
  // vor wenn VHS noch an der Live-Edge lädt statt am Anfang).
  const computeAhead = () => {
    const br = vjs.buffered();
    let atWanted = 0;
    let anywhere = 0;
    for (let i = 0; i < br.length; i++) {
      const s = br.start(i), e = br.end(i);
      if (s <= wantedPos + 0.5 && e >= wantedPos) {
        atWanted = Math.max(atWanted, e - wantedPos);
      }
      anywhere = Math.max(anywhere, e - s);
    }
    return { atWanted, anywhere };
  };

  // Für Transcode: Server-Progress ist die Gate-Metrik. VHS pre-bufffert
  // im Pause-Zustand nur 1 Segment (~5 s) — also messen wir stattdessen,
  // wie weit ffmpeg-seitig bereits transcodiert wurde. Wenn Server +60 s
  // produziert hat, ist smooth Playback gesichert; Client lädt dann beim
  // Play nach Bedarf.
  // Für Direct Play: Client-buffered() wächst während Pause normal
  // (progressives mp4), darum bleibt's hier beim Browser-Buffer.
  let tickCount = 0;
  let serverAhead = 0;
  let lastServerFetch = 0;

  state.startBufferTimer = setInterval(async () => {
    if (!gateActive) return;
    if (!state.vjs || (state.vjs.isDisposed && state.vjs.isDisposed())) {
      gateActive = false;
      clearStartBufferGate();
      return;
    }
    const dur = vjs.duration() || 0;
    const effective = dur > 0 && dur / 2 < cappedTarget
      ? Math.max(2, Math.floor(dur / 2))
      : cappedTarget;

    let atWanted = 0;
    let anywhere = 0;

    if (isTranscode) {
      // Server-Progress-Fetch alle 800 ms (Poll-Loop selbst läuft 400 ms).
      const now = Date.now();
      if (now - lastServerFetch > 800) {
        lastServerFetch = now;
        try {
          const r = await api(progressURL());
          const pos = Number(r.positionSec) || 0;
          const start = Number(r.startSec) || 0;
          serverAhead = Math.max(0, pos - start);
        } catch {}
      }
      atWanted = serverAhead;
    } else {
      const a = computeAhead();
      atWanted = a.atWanted;
      anywhere = a.anywhere;
    }

    const display = atWanted;

    if (tickCount++ % 2 === 0) {
      console.log("[gate] tick mode=%s server=%s atWanted=%s target=%s paused=%s",
        isTranscode ? "transcode" : "direct",
        serverAhead.toFixed(1), atWanted.toFixed(1), effective, vjs.paused());
    }

    if (bar) bar.style.width = `${Math.min(100, (display / effective) * 100)}%`;
    if (text) text.textContent = `${Math.round(display)} / ${effective} s`;

    if (atWanted >= effective) {
      release(`${Math.round(atWanted)} s gepuffert`);
    }
  }, 400);
}

function clearStartBufferGate() {
  if (state.startBufferTimer) {
    clearInterval(state.startBufferTimer);
    state.startBufferTimer = null;
  }
  const overlay = $("#prebufferOverlay");
  if (overlay) overlay.classList.add("hidden");
}

// --- Pause-Prefetch (Transcode) ---
//
// Video.js/VHS laedt im Pause-Zustand bewusst nur ~1 Segment voraus (siehe
// Kommentar in applyStartBufferGate). Der ffmpeg-Prozess auf dem
// Server transkodiert aber unabhaengig vom Player-Zustand weiter — das
// "Server +Ns"-Overlay zeigt genau diesen Vorsprung an. Waehrend der Pause
// pollen wir die m3u8-Playlist und laden neu erschienene Segmente per
// eigenem fetch() in den Browser-HTTP-Cache (die Segment-Route liefert dafuer
// `Cache-Control: max-age=300`, siehe stream.go). VHS bedient sich beim
// Weiterspielen daraus, ohne erneut ueber eine ggf. langsame Leitung zu muessen.
let pausePrefetchTimer = null;
let pausePrefetchSeen = null;

function stopPausePrefetch() {
  if (pausePrefetchTimer) {
    clearInterval(pausePrefetchTimer);
    pausePrefetchTimer = null;
  }
  pausePrefetchSeen = null;
}

function startPausePrefetch(vjs) {
  stopPausePrefetch();
  if (!state.playback || state.playback.mode !== "transcode") return;
  const item = state.currentItem;
  if (!item) return;
  pausePrefetchSeen = new Set();
  const params = new URLSearchParams({ profile: state.playback.profile || "orig" });
  const aIdx = state.playback.audioIdx;
  if (aIdx !== undefined && aIdx !== null && aIdx >= 0) params.set("audio", String(aIdx));
  const off = state.playback.virtualOffset;
  if (off && off > 0) params.set("start", String(Math.floor(off)));
  const dei = state.playback.deinterlace;
  if (dei && dei !== "auto") params.set("deinterlace", dei);
  const playlistUrl = `/api/transcode/${item.id}/index.m3u8?${params}`;

  const tick = async () => {
    // Sicherheitsnetz: Player kann zwischen Timer-Ticks weitergespielt oder
    // geschlossen worden sein, ohne dass stopPausePrefetch() zwischenzeitlich
    // gegriffen hat (z.B. Source-Wechsel ohne "play"-Event).
    if (!state.vjs || state.vjs !== vjs || !vjs.paused()) { stopPausePrefetch(); return; }
    let text;
    try {
      const res = await fetch(playlistUrl, { credentials: "same-origin" });
      if (!res.ok) return;
      text = await res.text();
    } catch { return; }
    const segUrls = text.split("\n")
      .map(l => l.trim())
      .filter(l => l && !l.startsWith("#"));
    for (const rel of segUrls) {
      if (!pausePrefetchSeen || !vjs.paused()) return;
      let abs;
      try { abs = new URL(rel, location.href).toString(); } catch { continue; }
      if (pausePrefetchSeen.has(abs)) continue;
      pausePrefetchSeen.add(abs);
      try { await fetch(abs, { credentials: "same-origin" }); } catch { /* still */ }
    }
  };
  tick();
  pausePrefetchTimer = setInterval(tick, 4000);
}

function startBufferDisplay(item, mode, profile, audioIdx) {
  stopTranscodeProgress();
  const el = $("#transcodeAhead");
  if (!el) return;
  el.classList.remove("hidden");
  const title = el.querySelector(".ta-title");
  const stats = el.querySelector(".ta-stats");
  if (title) title.textContent = item.title || (item.relPath || "").split("/").pop();
  if (stats) stats.textContent = "…";
  const isTranscode = mode === "transcode";
  let url = null;
  if (isTranscode) {
    // Progress-URL muss EXAKT denselben Session-Key liefern wie die laufende
    // Playback-Session: profile + audio + start + deinterlace. Ohne start
    // sucht der Server bei start=0 — bei Resume- oder Seek-Restart matcht
    // das nicht und `StartOrGet` spawnt eine zweite ffmpeg-Instanz parallel,
    // die mit der eigentlichen Wiedergabe um die iGPU konkurriert. Daher
    // hier ALLE relevanten Parameter an die URL haengen.
    const params = new URLSearchParams({ profile: profile || "orig" });
    if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
      params.set("audio", String(audioIdx));
    }
    const off = state.playback && state.playback.virtualOffset;
    if (off && off > 0) params.set("start", String(Math.floor(off)));
    const dei = state.playback && state.playback.deinterlace;
    if (dei && dei !== "auto") params.set("deinterlace", dei);
    url = `/api/transcode/${item.id}/progress?${params}`;
  }
  const clientBuffer = () => {
    if (!state.vjs) return 0;
    const cur = state.vjs.currentTime();
    const br = state.vjs.buffered();
    for (let i = 0; i < br.length; i++) {
      if (br.start(i) <= cur + 0.1 && br.end(i) >= cur) {
        return Math.max(0, br.end(i) - cur);
      }
    }
    return 0;
  };
  // Aktuelle Render-Auflösung des Video-Elements (beim Transcode = Output
  // von ffmpeg, beim Direct Play = Quell-Auflösung). Fallback auf Profil-
  // Label, falls das Video-Element noch keine Metadaten hat.
  const currentPlayingRes = () => {
    const root = state.vjs && state.vjs.el ? state.vjs.el() : null;
    const vid = root ? root.querySelector("video") : null;
    const w = vid && vid.videoWidth ? vid.videoWidth : 0;
    const h = vid && vid.videoHeight ? vid.videoHeight : 0;
    if (w || h) return resLabel({ width: w, height: h });
    // Fallback anhand Profil
    if (isTranscode && profile && profile !== "orig") return profile;
    return resLabel(item);
  };
  const setStats = (text) => { if (stats) stats.textContent = text; };
  const setClass = (...cls) => {
    // Nur die Status-Marker (behind/low) togglen — alles andere behalten.
    // Wichtig: --docked darf NICHT überschrieben werden, sonst fällt das
    // Overlay zurück in die absolute Position oben rechts.
    el.classList.remove("behind", "low");
    cls.forEach(c => c && el.classList.add(c));
  };
  // Lokaler Flag: sobald der Transcode fertig ist, fällt der Poll in den
  // Buffer-only-Modus zurück. Timer läuft weiter, damit das Overlay auch
  // nach Transcode-Ende bei jedem Mouse-Move aktuelle Werte zeigt.
  let transcodeDone = false;
  const poll = async () => {
    const clientAhead = clientBuffer();
    const res = currentPlayingRes();
    const resChip = res ? `${res} · ` : "";
    if (!isTranscode || transcodeDone) {
      setStats(`${resChip}Buffer +${Math.round(clientAhead)} s`);
      setClass(clientAhead < 1 ? "behind" : (clientAhead < 5 ? "low" : null));
      return;
    }
    try {
      const d = await api(url);
      const pos = Number(d.positionSec || 0);
      const cur = state.vjs ? state.vjs.currentTime() : 0;
      const serverAhead = Math.max(0, pos - cur);
      if (d.done) {
        transcodeDone = true;
        setStats(`${resChip}Buffer +${Math.round(clientAhead)} s`);
        setClass(clientAhead < 1 ? "behind" : (clientAhead < 5 ? "low" : null));
        return;
      }
      const sign = serverAhead >= 1 ? "+" : "";
      setStats(`${resChip}Server ${sign}${Math.round(serverAhead)} s · Buffer +${Math.round(clientAhead)} s`);
      setClass(serverAhead < 1 ? "behind" : (clientAhead < 5 ? "low" : null));
    } catch (e) {
      // Poll-Fehler — stumm
    }
  };
  poll();
  // Timer alle 1s: in Transcode-Mode zieht das Server-Progress nach, sonst
  // reine Buffer-Anzeige. 1s fühlt sich bei Mouse-Move responsiver an als 2s.
  state.transcodePollTimer = setInterval(poll, 1000);
}

// Rückwärtskompatibler Alias, falls noch jemand die alte Funktion ruft.
function startTranscodeProgress(item, profile, audioIdx) {
  return startBufferDisplay(item, "transcode", profile, audioIdx);
}

// Beim Transcode hat VHS keine stabile Gesamtlänge. Wir setzen die aus ffprobe
// bekannte Filmdauer als duration-Cache. Bei jeder tech-seitigen durationchange
// re-applyen wir, damit VHS sie nicht mit der wachsenden seekable.end überschreibt.
const forcedDurationState = new WeakMap(); // vjs → { total, onChange }

function forcePlayerDuration(vjs, totalSec) {
  if (!vjs || !totalSec || totalSec <= 0) return;
  releasePlayerDuration(vjs);
  let applying = false;
  const apply = () => {
    if (applying) return;
    const cur = vjs.duration();
    if (cur !== totalSec) {
      applying = true;
      try { vjs.duration(totalSec); } catch {}
      applying = false;
    }
  };
  vjs.on("durationchange", apply);
  vjs.on("loadedmetadata", apply);
  vjs.on("loadeddata", apply);
  apply();
  forcedDurationState.set(vjs, { total: totalSec, apply });
}

function releasePlayerDuration(vjs) {
  if (!vjs) return;
  const s = forcedDurationState.get(vjs);
  if (s && s.apply) {
    vjs.off("durationchange", s.apply);
    vjs.off("loadedmetadata", s.apply);
    vjs.off("loadeddata", s.apply);
  }
  forcedDurationState.delete(vjs);
}

// Stoppt nur den Poll-Timer, lässt aber das Overlay sichtbar.
// So bleibt die Buffer/Auflösungs-Anzeige nach Transcode-Ende weiter bestehen.
function stopTranscodeProgress() {
  if (state.transcodePollTimer) {
    clearInterval(state.transcodePollTimer);
    state.transcodePollTimer = null;
  }
}
// Beim Player-Close / Source-Wechsel komplett ausblenden.
function hideBufferOverlay() {
  stopTranscodeProgress();
  const el = document.getElementById("transcodeAhead");
  if (el) el.classList.add("hidden");
}

// positionBufferOverlay verschiebt das Buffer-/Stats-Overlay je nach
// Fullscreen-Status. Im Fullscreen sitzt es absolut positioniert oben rechts
// im Video.js-Root (inkl. Title-Zeile). Im eingebetteten Modus wandert es in
// den Player-Footer und wird als kompakter Inline-Streifen dargestellt — so
// liegt es AUSSERHALB vom Bild und der Titel wird unterdrückt (steht ohnehin
// im Dialog-Header).
function positionBufferOverlay(vjs) {
  const overlay = document.getElementById("transcodeAhead");
  if (!overlay) return;
  const inFullscreen = vjs && typeof vjs.isFullscreen === "function" && vjs.isFullscreen();
  if (inFullscreen) {
    overlay.classList.remove("transcode-ahead--docked");
    if (vjs && overlay.parentElement !== vjs.el()) vjs.el().appendChild(overlay);
  } else {
    overlay.classList.add("transcode-ahead--docked");
    const wrap = document.querySelector(".player-wrap");
    const meta = document.getElementById("playerMeta");
    if (wrap && meta && overlay.parentElement !== wrap) {
      wrap.insertBefore(overlay, meta);
    }
  }
}

function closePlayer() {
  disposePlayer();
  $("#playerDialog").close();
}

