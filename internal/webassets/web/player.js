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

// resSizeHTML: Auflösung + Dateigröße als eigene Spans in der Detail-Dialog-
// Sub-Zeile (User-Anfrage 2026-09-07: "In den Infofenstern steht nirgends die
// Auflösung und die Dateigröße" — vorher stand die Auflösung nur außen auf der
// Kachel, die Dateigröße nur im Varianten-Dropdown bei ≥2 Varianten). Eigene
// Funktion (statt einfach in die generische `sub`-Liste zu pushen), weil beide
// Werte beim Wechsel des Varianten-Dropdowns aktualisiert werden müssen —
// braucht ein eigenes, gezielt ersetzbares Element (siehe `#detailResSize`-
// Update im Variant-Change-Handler in `openDetail`).
function resSizeHTML(item) {
  if (!item) return "";
  const parts = [];
  if (item.width > 0 && item.height > 0) parts.push(`${item.width}×${item.height}`);
  if (item.sizeBytes > 0) parts.push(fmtSize(item.sizeBytes));
  return parts.map(x => `<span>${escapeHTML(x)}</span>`).join("");
}

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
  // ein Bulk-/Single-Refresh die DB längst aktualisiert hat. Die API-Antwort
  // trägt kein `_variants`-Array — das wird direkt im Anschluss IMMER frisch
  // per `/api/items/{id}/variants` nachgeladen (siehe dort, nicht mehr aus
  // dem Grid-Kontext übernommen — der ist ggf. bibliotheksgescoped).
  if (item && item.id) {
    try {
      const fresh = await api(`/api/items/${item.id}`);
      if (fresh) {
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
    // Varianten-Geschwister IMMER frisch vom Server holen (nie das
    // client-seitig mitgegebene item._variants ungeprüft übernehmen).
    // Bug gefixt 2026-09-07 (User-Report: Kachel zeigt "×3", Dropdown im
    // Detail-Dialog aber nur 2 Einträge): groupVariants() im Grid gruppiert
    // nur INNERHALB der gerade geladenen Liste — beim Blättern in EINER
    // Bibliothek enthält item._variants dadurch nie Geschwister aus einer
    // ANDEREN Bibliothek, auch wenn die frühere Prüfung
    // "_variants.length <= 1" das fälschlich als "schon vollständig"
    // durchgehen ließ (2 > 1). Der ×N-Badge kommt dagegen aus dem
    // server-seitig über ALLE Bibliotheken gezählten variantCount — beide
    // Quellen liefen dadurch auseinander. `/api/items/{id}/variants` ist
    // die einzige wirklich vollständige, bibliotheksübergreifende Quelle.
    if (item.metadataId > 0) {
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
  const sub = [];
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
    if (item.releasedAt) sub.push(fmtDate(item.releasedAt));
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
          <span id="detailResSize">${resSizeHTML(item)}</span>
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
      const resSizeEl = $("#detailResSize");
      if (resSizeEl) resSizeEl.innerHTML = resSizeHTML(pick);
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
  // Trailer (User-Anfrage 2026-09-04, Jellyfin-artige Trailer-Funktion): nur bei
  // echten Filmen sinnvoll — der Server lehnt bei anderem tmdb_type ohnehin mit
  // 404 ab, die Lib-Kind-Prüfung hier spart nur den unnötigen Request.
  $("#detailTrailer").classList.add("hidden");
  if (item.metadataId > 0 && itemLib && itemLib.kind === "movies") loadDetailTrailer(item.metadataId);
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

// Trailer-Button (User-Anfrage 2026-09-04): lazy nachgeladen wie der Cast, damit
// das Dialog-Öffnen nicht auf einen zusätzlichen TMDB-Roundtrip wartet. Ein 404
// (kein Trailer gefunden, TMDB deaktiviert, o.ä.) ist erwartbar und kein Fehler —
// der Button bleibt dann einfach versteckt.
async function loadDetailTrailer(metadataId) {
  const btn = $("#detailTrailer");
  if (!btn) return;
  try {
    const trailer = await api(`/api/metadata/${metadataId}/trailer`);
    if (!trailer || !trailer.key) return;
    btn.dataset.trailerKey = trailer.key;
    btn.dataset.trailerName = trailer.name || "";
    btn.classList.remove("hidden");
  } catch (e) {
    // Kein Trailer verfügbar — kein Fehler-Log nötig (häufigster Fall).
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


// applyPlayerSkin — schaltet die "Pill"-Steuerleiste (Zahnrad-Menü → Anzeige,
// state.playerSkin) per CSS-Klasse auf dem Video.js-Root um. Reine Optik,
// keine Buttons/Funktionen ändern sich (siehe .video-js.player-skin-pill in
// style.css). Wird bei jedem applyPlayback()-Aufruf neu angewendet (auch bei
// wiederverwendeter Instanz), damit ein Wechsel im Menü ohne Reload greift.
function applyPlayerSkin(vjs) {
  if (!vjs || typeof vjs.el !== "function") return;
  const root = vjs.el();
  if (!root || !root.classList) return;
  root.classList.toggle("player-skin-pill", state.playerSkin === "pill");
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

  // Player-Steuerleisten-Stil ("Pill" vs. Standard, Zahnrad-Menü → Anzeige) —
  // reine CSS-Klasse auf dem Video.js-Root, gleiche Buttons/Funktionen
  // dahinter unverändert. Bei jedem Öffnen neu angewendet (auch bei
  // Instanz-Reuse), damit eine Änderung im Menü ohne Reload wirkt.
  applyPlayerSkin(vjs);

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



function closePlayer() {
  disposePlayer();
  $("#playerDialog").close();
}

