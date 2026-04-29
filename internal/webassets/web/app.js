// Vanilla JS frontend. Video.js (gebundelt) als Player, HLS via Video.js-VHS.

const $ = (s, root = document) => root.querySelector(s);

const state = {
  libraries: [],
  playlists: [],
  currentLibrary: null,
  currentPlaylist: null, // wenn gesetzt, wird eine Playlist angezeigt statt einer Library
  currentFolder: null,   // null = Library-Root, sonst Pfad wie "a/Siterips"
  currentFolderDrilldown: false, // ob der aktive Folder Subfolder statt flacher Items zeigt
  playQueue: [],         // für auto-next in Playlists: die Items in Reihenfolge
  playQueueIdx: -1,      // aktueller Index innerhalb der Queue
  shuffleMode: false,    // wenn true, sind Prev/Next-Buttons im Player aktiv
  shuffleHistory: [],    // bereits abgespielte Items in Reihenfolge
  shuffleIdx: -1,        // aktuelle Position in shuffleHistory
  settings: { bufferSeconds: 30, startBufferSeconds: 0, trickplayIntervalSec: 10 },
  hwaccel: { enabled: false, device: "", driver: "" },
  scanPoll: null,
  vjs: null,
  currentItem: null,
  playback: null,
  browseAt: "/media",
  personFilter: null,       // {tmdbId, name} wenn Person-Filter aktiv
  personFilterBackup: null, // zwischengespeicherter Lib/Folder-Kontext
  transcodePollTimer: null, // setInterval-Handle für Transcode-Progress-Polling
  flatView: false,          // wenn true: loadItems überspringt Ordner-Navigation
  selectionMode: false,     // Bulk-Auswahl aktiv
  selection: new Set(),     // Set von item-IDs in der aktuellen Auswahl
  lastRenderedItems: [],    // Referenz auf zuletzt gerenderte Items (für „Alle auswählen")
  loadSeq: 0,               // Sequenz-Zähler für loadItems — verhindert, dass veraltete async-Responses das Grid überschreiben
  sortDir: "",              // "asc" | "desc" | "" (Default) — Richtungs-Override
  resBuckets: new Set(),    // ausgewählte Auflösungs-Buckets ("4k","1080p",…) — Multi-Select
  collectionsView: false,   // true = Collections-Root-Ansicht (Liste aller Sammlungen)
  currentCollection: null,  // {id, name} wenn eine einzelne Collection geöffnet ist
  playlistsView: false,     // true = Playlists-Root (Kacheln aller Playlists)
  lastSortContextKey: "",   // merkt, wann Sortierung neu aus localStorage zu laden ist
  pendingResumeSec: 0,      // beim nächsten applyPlayback angewandte Resume-Position
  playlistReturnNav: null,  // Snapshot der Nav vor Playlist-Öffnen — Close-Button stellt ihn wieder her
  startBufferTimer: null,   // setInterval-Handle für den Pre-Buffer-Gate
  scrollPositions: new Map(), // navKey → scrollY für Zurück-Navigation
  lastNavKey: null,            // navKey der zuletzt gerenderten Ansicht
  alphaFilter: null,           // null oder "A".."Z"|"#" — Anfangsbuchstaben-Filter via Sidebar-Klick
};

// captureNavSnapshot: merkt sich die aktuelle Nav-Position (Library, Folder,
// Home-View, Collection, Person-Filter). Nur setzen, wenn wir noch NICHT in
// einer Playlist-Ansicht sind — damit das erste Verlassen der „echten" Nav
// den Rücksprungs-Punkt festhält. Späteres Wechseln zwischen Playlist-Root
// und einzelnen Playlists überschreibt ihn nicht.
function captureNavSnapshot() {
  if (state.playlistsView || state.currentPlaylist) return;
  state.playlistReturnNav = {
    currentLibrary: state.currentLibrary,
    currentFolder: state.currentFolder,
    currentSeason: state.currentSeason,
    currentFolderDrilldown: state.currentFolderDrilldown,
    homeView: state.homeView,
    collectionsView: state.collectionsView,
    currentCollection: state.currentCollection,
    personFilter: state.personFilter,
  };
}

// enterPlaylist: öffnet eine einzelne Playlist. Snapshot nur setzen, falls
// wir von außerhalb des Playlist-Bereichs reinkommen.
function enterPlaylist(playlistID) {
  captureNavSnapshot();
  state.currentPlaylist = playlistID;
}

// enterPlaylistsView: öffnet die Playlist-Root (Kacheln aller Playlists).
// Snapshot speichern, damit der ✕-Button in der Root auch dort zur vorherigen
// echten Nav zurückführt.
function enterPlaylistsView() {
  captureNavSnapshot();
  state.playlistsView = true;
  state.currentPlaylist = null;
  state.currentLibrary = null;
  state.collectionsView = false;
  state.currentCollection = null;
  state.homeView = false;
}

// exitPlaylist: stellt den vorher gemerkten Nav-Kontext wieder her. Funktioniert
// sowohl aus einer einzelnen Playlist als auch aus der Playlist-Root-Ansicht.
// Wenn keiner gespeichert ist (Direktzugriff oder Reload), fällt auf Home zurück.
function exitPlaylist() {
  const nav = state.playlistReturnNav;
  state.currentPlaylist = null;
  state.playlistsView = false;
  state.playlistReturnNav = null;
  if (!nav) {
    // Fallback: Home
    state.homeView = true;
    state.currentLibrary = null;
    state.currentFolder = null;
    loadItems();
    return;
  }
  state.currentLibrary = nav.currentLibrary;
  state.currentFolder = nav.currentFolder;
  state.currentSeason = nav.currentSeason;
  state.currentFolderDrilldown = nav.currentFolderDrilldown;
  state.homeView = nav.homeView;
  state.collectionsView = nav.collectionsView;
  state.currentCollection = nav.currentCollection;
  state.personFilter = nav.personFilter;
  const sel = $("#librarySelect");
  if (sel) {
    if (state.currentLibrary) sel.value = "lib:" + state.currentLibrary;
    else sel.value = "";
  }
  loadItems();
}

// Cache für GET /api/items?... — hält die letzten 5 Listenantworten im Speicher.
// Bei Mutationen (watched/favorite/delete) wird der Cache invalidiert.
const itemsCache = new Map(); // key → { ts, data }
const ITEMS_CACHE_LIMIT = 5;
const ITEMS_CACHE_TTL_MS = 30_000; // 30s

function itemsCacheKey(path) { return path; }
function itemsCacheGet(path) {
  const e = itemsCache.get(path);
  if (!e) return null;
  if (Date.now() - e.ts > ITEMS_CACHE_TTL_MS) { itemsCache.delete(path); return null; }
  return e.data;
}
function itemsCacheSet(path, data) {
  if (itemsCache.size >= ITEMS_CACHE_LIMIT) {
    const firstKey = itemsCache.keys().next().value;
    itemsCache.delete(firstKey);
  }
  itemsCache.set(path, { ts: Date.now(), data });
}
function invalidateItemsCache() { itemsCache.clear(); }

async function apiGetCached(path) {
  const hit = itemsCacheGet(path);
  if (hit) return hit;
  const data = await api(path);
  // Nur Listen-Responses cachen
  if (Array.isArray(data)) itemsCacheSet(path, data);
  return data;
}

async function api(path, opts = {}) {
  // Mutationen invalidieren den Items-Cache
  if (opts.method && opts.method !== "GET") invalidateItemsCache();
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...opts,
  });
  if (res.status === 401) {
    // Session abgelaufen → zum Login leiten
    location.href = "/login.html";
    throw new Error("nicht angemeldet");
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = await res.json();
      if (j.error) msg = j.error;
    } catch {}
    throw new Error(msg);
  }
  if (res.status === 204) return null;
  return res.json();
}

function fmtDuration(sec) {
  sec = Math.round(sec || 0);
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function fmtSize(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, n = bytes || 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 ? 1 : 0)} ${units[i]}`;
}

function fmtDate(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleDateString("de-DE", { year: "numeric", month: "2-digit", day: "2-digit" });
}

// applyNaturalTitleSort: re-sortiert items mit "natural" / "human" Vergleich,
// damit Zahlen im Titel als Werte sortiert werden (1, 2, …, 9, 10, 11) statt
// lexikographisch (1, 10, 11, …, 2, 3). SQLite kann das nativ nicht.
// Greift nur, wenn das Sort-Dropdown auf "title" steht. Die Server-Sortierung
// liefert eine stabile Reihenfolge — wir verfeinern sie nur.
function applyNaturalTitleSort(items, opts) {
  if (!Array.isArray(items)) return items;
  const dir = (opts && opts.dir) || effectiveSortDir();
  const sortMode = (opts && opts.sort) || currentSortMode();
  if (sortMode !== "title") return items;
  const collator = new Intl.Collator("de", { numeric: true, sensitivity: "base" });
  const titleOf = (it) => {
    if (it && it.metadata && it.metadata.title) return it.metadata.title;
    return (it && it.title) || "";
  };
  const sign = dir === "desc" ? -1 : 1;
  items.sort((a, b) => sign * collator.compare(titleOf(a), titleOf(b)));
  return items;
}

// groupVariants: fasst Items mit gleichem metadataId zu Gruppen zusammen. Repräsentant
// pro Gruppe = Item mit höchster Auflösung (dann größter Bitrate). Alle Geschwister
// werden am Repräsentanten unter `_variants` abgelegt (für Detail-Dropdown).
// Items ohne metadataId bleiben unverändert.
function groupVariants(items) {
  const groups = new Map();
  const out = [];
  for (const it of items) {
    if (!it.metadataId) { out.push(it); continue; }
    const key = it.metadataId;
    if (!groups.has(key)) {
      const rep = { ...it, _variants: [it] };
      groups.set(key, rep);
      out.push(rep);
    } else {
      const g = groups.get(key);
      g._variants.push(it);
      // Besseren Repräsentanten übernehmen (höher aufgelöst, sonst größere Bitrate)
      if ((it.height || 0) > (g.height || 0) ||
          ((it.height || 0) === (g.height || 0) && (it.bitrateKbps || 0) > (g.bitrateKbps || 0))) {
        // Werte übernehmen, aber _variants und Basis-ID beibehalten
        Object.assign(g, it, { _variants: g._variants });
      }
    }
  }
  return out;
}

// cardFileName: letztes Pfad-Segment (Dateiname) einer Variante. Bei gemergten
// Kacheln wird die Anzahl der Varianten daneben angezeigt.
function cardFileName(it) {
  const name = (it.relPath || it.path || "").split("/").filter(Boolean).pop() || "";
  if (it._variants && it._variants.length > 1) {
    return `${name}  (+${it._variants.length - 1} weitere)`;
  }
  return name;
}

// variantLabel: Beschreibung einer Datei-Variante für den Dropdown.
// Enthält Dateiname + technische Details, damit man auch bei gleicher
// Auflösung/Größe noch unterscheiden kann, welche Datei gemeint ist.
function variantLabel(v) {
  const fileName = (v.relPath || v.path || "").split("/").filter(Boolean).pop() || "";
  const parts = [];
  parts.push((v.container || "").toUpperCase() || "?");
  const r = resLabel(v);
  if (r) parts.push(r);
  if (v.sizeBytes) parts.push(fmtSize(v.sizeBytes));
  if (v.bitrateKbps) parts.push(`${Math.round(v.bitrateKbps / 100) / 10} Mbps`);
  if (v.videoCodec) parts.push(v.videoCodec.toUpperCase());
  if (v.audioCodec) parts.push(v.audioCodec.toUpperCase());
  if (v.watched) parts.push("✓ gesehen");
  const tech = parts.join(" · ");
  return fileName ? `${fileName}  —  ${tech}` : tech;
}

// --- Trickplay-Verwaltung (Admin) ---

// --- Datei/Pfad-Suche (Diagnose, admin-only) ---

// openMissingDialog: Admin-Dialog zum Export fehlender Filme/Folgen für
// Radarr/Sonarr. Filme sind reine SQL-Liste (sofort), Folgen brauchen TMDB-
// Calls und werden auf Klick pro Library berechnet.
// runRefreshAllMetadata stößt einen Bulk-Refresh aller Metadata-Einträge an.
// Pollt danach den Status und zeigt einen Live-Toast mit Fortschritt.
async function runRefreshAllMetadata() {
  if (!(await appConfirm(
    "Metadaten für ALLE Filme, Serien und Episoden frisch von TMDB laden?\n\n" +
    "Bei vielen Items kann das einige Minuten bis Stunden dauern (TMDB-Rate-Limit 35/10s). " +
    "Leere Felder (Plot/Runtime/Genres) werden zusätzlich aus OMDb nachgefüllt, falls verfügbar.\n\n" +
    "Die TMDB-Zuordnungen werden NICHT verändert — nur die Detail-Daten aktualisiert."
  ))) return;
  try {
    await api("/api/enrich/refresh-all-metadata", { method: "POST" });
  } catch (e) {
    appAlert("Fehler: " + e.message);
    return;
  }
  showToast("Bulk-Refresh läuft im Hintergrund.", { kind: "success", duration: 4000 });
  pollRefreshAllStatus();
}

// pollRefreshAllStatus aktualisiert eine Live-Toast-Anzeige mit dem
// Bulk-Refresh-Fortschritt. Endet wenn der Server `running=false` meldet.
function pollRefreshAllStatus() {
  if (state.refreshAllPoll) return;
  state.refreshAllPoll = setInterval(async () => {
    try {
      const st = await api("/api/enrich/refresh-all-status");
      if (st.running) {
        const pct = st.total > 0 ? Math.round((st.done / st.total) * 100) : 0;
        showToast(`↻ Refresh: ${st.done}/${st.total} (${pct}%)${st.current ? " · " + st.current : ""}`,
          { kind: "info", duration: 3500 });
      } else {
        clearInterval(state.refreshAllPoll);
        state.refreshAllPoll = null;
        if (st.startedAt && st.finishedAt) {
          showToast(`✓ Refresh fertig: ${st.updated} aktualisiert, ${st.failed} Fehler`,
            { kind: "success", duration: 6000 });
          invalidateItemsCache();
          loadItems();
        }
      }
    } catch (e) { console.warn(e); }
  }, 3000);
}

async function openMissingDialog() {
  const dlg = $("#missingDialog");
  if (!dlg) return;
  dlg.showModal();

  // 1) Filme — sofort laden
  $("#missingMoviesStatus").textContent = "Wird geladen…";
  $("#missingMoviesPreview").innerHTML = "";
  let movies = [];
  try {
    movies = await api("/api/missing/movies");
  } catch (e) {
    $("#missingMoviesStatus").textContent = "Fehler: " + e.message;
    return;
  }
  $("#missingMoviesStatus").textContent = movies.length
    ? `${movies.length} fehlende Filme gefunden.`
    : "Keine fehlenden Filme — alle Sammlungen vollständig.";
  $("#missingMoviesPreview").innerHTML = renderMissingPreview(
    movies.slice(0, 30).map(m => ({
      label: `${m.title}${m.releaseDate ? " (" + m.releaseDate.slice(0, 4) + ")" : ""}`,
      sub: `TMDB ${m.tmdbId} · ${m.collectionName}`,
    })),
    movies.length,
  );
  $("#missingMoviesCsv").onclick = () => {
    window.location.href = "/api/missing/movies?format=csv";
  };
  $("#missingMoviesCopy").onclick = async () => {
    const ids = movies.map(m => m.tmdbId).join("\n");
    try {
      await navigator.clipboard.writeText(ids);
      showToast(`${movies.length} TMDB-IDs in Zwischenablage`, { kind: "success" });
    } catch {
      appAlert("Konnte nicht in die Zwischenablage schreiben. CSV-Download nutzen.");
    }
  };

  // 2) Folgen — Library-Auswahl füllen
  const sel = $("#missingEpLib");
  sel.innerHTML = "";
  for (const lib of state.libraries) {
    if (lib.kind !== "tv") continue;
    const o = document.createElement("option");
    o.value = String(lib.id);
    o.textContent = lib.name;
    sel.appendChild(o);
  }
  if (!sel.options.length) {
    $("#missingEpStatus").textContent = "Keine TV-Bibliothek vorhanden.";
    $("#missingEpRun").disabled = true;
  } else {
    $("#missingEpStatus").textContent = "";
    $("#missingEpRun").disabled = false;
  }
  $("#missingEpRun").onclick = () => runMissingEpisodesScan(Number(sel.value));
  $("#missingEpCsv").onclick = () => {
    if (!state.lastMissingEpisodesLib) return;
    window.location.href = `/api/missing/episodes?libraryId=${state.lastMissingEpisodesLib}&format=csv`;
  };
  $("#missingEpPreview").innerHTML = "";
  $("#missingEpCsv").disabled = true;
}

async function runMissingEpisodesScan(libID) {
  if (!libID) return;
  $("#missingEpStatus").textContent = "Prüfe (kann dauern, fragt TMDB pro Show)…";
  $("#missingEpRun").disabled = true;
  $("#missingEpCsv").disabled = true;
  try {
    const list = await api(`/api/missing/episodes?libraryId=${libID}`);
    state.lastMissingEpisodesLib = libID;
    $("#missingEpStatus").textContent = list.length
      ? `${list.length} fehlende Folgen gefunden.`
      : "Keine fehlenden Folgen für diese Bibliothek (oder kein TMDB-Match).";
    $("#missingEpPreview").innerHTML = renderMissingPreview(
      list.slice(0, 30).map(e => ({
        label: `${e.showTitle} S${String(e.season).padStart(2, "0")}E${String(e.episode).padStart(2, "0")}: ${e.title}`,
        sub: `Show TMDB ${e.showTmdbId} · ${e.airDate || "?"}`,
      })),
      list.length,
    );
    $("#missingEpCsv").disabled = list.length === 0;
  } catch (e) {
    $("#missingEpStatus").textContent = "Fehler: " + e.message;
  } finally {
    $("#missingEpRun").disabled = false;
  }
}

function renderMissingPreview(items, total) {
  if (!items.length) return "";
  const more = total > items.length ? `<div class="hint">… und ${total - items.length} weitere (vollständig im CSV)</div>` : "";
  return `<ul class="missing-list">${items.map(it =>
    `<li><div class="missing-label">${escapeHTML(it.label)}</div><div class="missing-sub">${escapeHTML(it.sub)}</div></li>`
  ).join("")}</ul>${more}`;
}

function openPathSearch() {
  const dlg = $("#pathSearchDialog");
  if (!dlg) return;
  const input = $("#pathSearchInput");
  const results = $("#pathSearchResults");
  input.value = "";
  results.innerHTML = `<div class="hint">Tippe mindestens 2 Zeichen…</div>`;
  // Event-Listener nur einmal binden
  if (!dlg.dataset.wired) {
    input.addEventListener("input", debounce(runPathSearch, 250));
    dlg.addEventListener("click", (e) => {
      if (e.target === dlg || e.target.hasAttribute("data-close")) dlg.close();
    });
    dlg.dataset.wired = "1";
  }
  dlg.showModal();
  setTimeout(() => input.focus(), 50);
}

async function runPathSearch() {
  const q = $("#pathSearchInput").value.trim();
  const results = $("#pathSearchResults");
  if (q.length < 2) {
    results.innerHTML = `<div class="hint">Tippe mindestens 2 Zeichen…</div>`;
    return;
  }
  results.innerHTML = `<div class="hint">Suche…</div>`;
  try {
    const items = await api(`/api/items/search-path?q=${encodeURIComponent(q)}`);
    if (!items.length) {
      results.innerHTML = `<div class="hint">Keine Treffer.</div>`;
      return;
    }
    results.innerHTML = "";
    // Duplikat-Erkennung: gleiches metadata_id = mehrere Files auf dieselbe
    // TMDB-Zuordnung. Wichtig für Auto-Rollover-Fälle (z.B. „S01E10" + „S02E01"
    // beide auf dieselbe TMDB-Episode), damit der User den Konflikt sieht.
    const metaCounts = new Map();
    for (const it of items) {
      if (it.metadataId) metaCounts.set(it.metadataId, (metaCounts.get(it.metadataId) || 0) + 1);
    }
    for (const it of items) {
      const row = document.createElement("button");
      row.type = "button";
      row.className = "pathsearch-row";
      const md = it.metadata || {};
      const mdTitle = md.title || "";
      const mdYear = md.year ? ` (${md.year})` : "";
      // Bei Episoden: SxxExx mit anzeigen, damit eine Diskrepanz zwischen
      // Dateiname (S01E10) und tatsächlicher TMDB-Zuordnung (S02E01) sofort
      // sichtbar ist.
      let mdCode = "";
      if (md.tmdbType === "episode" && md.season != null && md.episode != null) {
        mdCode = ` <span class="ps-code">S${String(md.season).padStart(2,"0")}E${String(md.episode).padStart(2,"0")}</span>`;
      }
      const dupCount = it.metadataId ? (metaCounts.get(it.metadataId) || 1) : 0;
      const dupBadge = dupCount > 1
        ? ` <span class="ps-dup" title="${dupCount} Files teilen diese Zuordnung">⚠ ×${dupCount}</span>`
        : "";
      const matchBadge = it.metadataId
        ? `<span class="ps-match">→ ${escapeHTML(mdTitle)}${mdYear}${mdCode}${dupBadge}</span>`
        : `<span class="ps-unmatched">keine TMDB-Zuordnung</span>`;
      row.innerHTML = `
        <div class="ps-path">${escapeHTML(it.relPath || it.path || "")}</div>
        <div class="ps-meta">${matchBadge}</div>
      `;
      if (dupCount > 1) row.classList.add("pathsearch-row--dup");
      row.addEventListener("click", () => {
        $("#pathSearchDialog").close();
        // Detail-Dialog des Items öffnen — Benutzer kann von dort manuell
        // zuordnen oder löschen.
        openDetail(it);
      });
      results.appendChild(row);
    }
    results.insertAdjacentHTML("afterbegin",
      `<div class="hint">${items.length} Treffer${items.length >= 200 ? " (max. erreicht — bitte genauer suchen)" : ""}</div>`);
  } catch (e) {
    results.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`;
  }
}

async function openTrickplayManager() {
  state.tpTab = state.tpTab || "done";
  await refreshTrickplayManager();
  $("#trickplayDialog").showModal();
}

async function refreshTrickplayManager() {
  // Zähl-Stats aus /trickplay/log?status=... parallel ziehen
  const [done, failed, pending] = await Promise.all([
    api("/api/trickplay/log?status=done").catch(() => []),
    api("/api/trickplay/log?status=failed").catch(() => []),
    api("/api/trickplay/log?status=pending").catch(() => []),
  ]);
  const counts = { done: done.length, failed: failed.length, pending: pending.length };
  $("#trickplayStats").innerHTML = `
    <span><strong>${counts.done}</strong>✓ erfolgreich</span>
    <span><strong>${counts.failed}</strong>⚠ Fehler</span>
    <span><strong>${counts.pending}</strong>⋯ wartend</span>
  `;
  // Aktiven Tab hervorheben
  document.querySelectorAll(".tp-tab").forEach(b => {
    b.classList.toggle("primary", b.dataset.tpTab === state.tpTab);
  });
  const list = { done, failed, pending }[state.tpTab] || [];
  const logEl = $("#trickplayLog");
  if (!list.length) {
    logEl.innerHTML = `<div class="trickplay-log-entry"><em style="color:#6b7280">Keine Einträge.</em></div>`;
    return;
  }
  logEl.innerHTML = list.map(e => {
    const err = (state.tpTab === "failed" && e.error)
      ? `<div class="err">✗ ${escapeHTML(e.error)}</div>` : "";
    // Einzel-Retry-Button nur in der Failed-Liste — pending lebt schon in der
    // Queue, done würde nichts kaputt machen aber ist sinnlos.
    const retryBtn = (state.tpTab === "failed" && e.id)
      ? `<button type="button" class="tp-row-retry" data-tp-retry="${e.id}" title="Diese Datei erneut versuchen">↻</button>`
      : "";
    return `<div class="trickplay-log-entry ${state.tpTab}">
      <div class="tp-row-main">
        <div class="tp-row-path">${escapeHTML(e.relPath || e.path)}</div>
        ${err}
      </div>
      ${retryBtn}
    </div>`;
  }).join("");
  // Click-Delegation für die Retry-Buttons. Nicht via inline-onclick, weil
  // refreshTrickplayManager bei jedem Tab-Wechsel die Liste neu rendert.
  logEl.querySelectorAll("[data-tp-retry]").forEach(btn => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.tpRetry;
      btn.disabled = true;
      btn.textContent = "…";
      try {
        await api(`/api/trickplay/items/${id}/retry`, { method: "POST" });
        await refreshTrickplayManager();
      } catch (e) {
        appAlert("Konnte Retry nicht starten: " + e.message);
        btn.disabled = false;
        btn.textContent = "↻";
      }
    });
  });
}

async function cancelTrickplayRun() {
  try {
    await api("/api/trickplay/cancel", { method: "POST" });
  } catch (e) { appAlert(e.message); }
}

async function cancelScanRun() {
  try {
    await api("/api/scan/cancel", { method: "POST" });
  } catch (e) { appAlert(e.message); }
}

async function retryFailedTrickplay() {
  try {
    const r = await api("/api/trickplay/retry-failed", { method: "POST" });
    appAlert(`${r.reset} Items zurückgesetzt. Generation läuft jetzt wieder an.`);
    await refreshTrickplayManager();
  } catch (e) { appAlert(e.message); }
}

// Wechsel vom Trickplay-Manager-Dialog in eine flache Grid-Ansicht aller
// Items mit trickplay_status=failed. Vorherige Nav (Library/Folder/Home/…)
// wird gemerkt; das ✕ im Breadcrumb stellt sie wieder her und öffnet den
// Dialog erneut.
function openTrickplayFailedView() {
  state.tpFailedReturnNav = {
    currentLibrary: state.currentLibrary,
    currentFolder: state.currentFolder,
    currentSeason: state.currentSeason,
    currentFolderDrilldown: state.currentFolderDrilldown,
    homeView: state.homeView,
    collectionsView: state.collectionsView,
    currentCollection: state.currentCollection,
    personFilter: state.personFilter,
    playlistsView: state.playlistsView,
    currentPlaylist: state.currentPlaylist,
  };
  $("#trickplayDialog").close();
  state.tpFailedView = true;
  loadItems();
}

function exitTrickplayFailedView() {
  const nav = state.tpFailedReturnNav;
  state.tpFailedView = false;
  state.tpFailedReturnNav = null;
  if (nav) {
    state.currentLibrary = nav.currentLibrary;
    state.currentFolder = nav.currentFolder;
    state.currentSeason = nav.currentSeason;
    state.currentFolderDrilldown = nav.currentFolderDrilldown;
    state.homeView = nav.homeView;
    state.collectionsView = nav.collectionsView;
    state.currentCollection = nav.currentCollection;
    state.personFilter = nav.personFilter;
    state.playlistsView = nav.playlistsView;
    state.currentPlaylist = nav.currentPlaylist;
  }
  loadItems();
  // Dialog wieder öffnen — der bleibt der natürliche Anker für diese Diagnose.
  openTrickplayManager();
}

async function deleteAllTrickplay() {
  if (!(await appConfirm("Alle generierten Trickplay-Dateien löschen? Items bleiben erhalten, müssen aber bei aktivierten Ordnern neu generiert werden."))) return;
  try {
    await api("/api/trickplay/delete-all", { method: "POST" });
    await refreshTrickplayManager();
    appAlert("Alle Trickplay-Dateien gelöscht.");
  } catch (e) { appAlert(e.message); }
}

// „Favoriten-Modus" und „Unmatched-Modus" sind jetzt Einträge im Sort-Dropdown,
// keine separaten Filter mehr. Die Helfer kapseln das Lookup, damit der Rest
// des Codes unverändert „favorite=yes / match=unmatched"-Semantik nutzen kann.
function currentFavoriteMode() {
  return $("#sortSelect").value === "favorites" ? "yes" : "";
}
function currentMatchMode() {
  return $("#sortSelect").value === "unmatched" ? "unmatched" : "";
}
function currentInterlacedMode() {
  return $("#sortSelect").value === "interlaced" ? "yes" : "";
}
// Gibt einen für die Items-API gültigen Sortier-Wert zurück. Die View-Modi
// „favorites" / „unmatched" / „duplicates" / „interlaced" sind rein UI-
// Schalter und dürfen nicht als Sort-Key an den Server gehen — sonst
// ignoriert SQLite sie und fällt auf Default-Order zurück.
function currentSortMode() {
  const v = $("#sortSelect").value || "title";
  if (v === "favorites" || v === "unmatched" || v === "duplicates" || v === "suspicious" || v === "interlaced" || v === "shuffle") return "title";
  return v;
}
// Fisher-Yates Shuffle für client-seitige Zufalls-Sortierung — Server liefert
// in „natürlicher" Reihenfolge, wir würfeln nach Erhalt. Vorteil gegenüber
// SQL ORDER BY RANDOM(): keine 60k-Items-Vollscan-Latenz.
function shuffleInPlace(arr) {
  if (!Array.isArray(arr)) return arr;
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const tmp = arr[i]; arr[i] = arr[j]; arr[j] = tmp;
  }
  return arr;
}

// Setzt alle Suche/Filter-Kontrollen zurück (beim Library-Wechsel).
function resetFilters() {
  const inputs = ["#searchInput", "#watchedFilter", "#sortSelect"];
  for (const sel of inputs) {
    const el = $(sel);
    if (!el) continue;
    if (el.tagName === "SELECT") {
      el.selectedIndex = 0;
    } else {
      el.value = "";
    }
  }
  // Auflösungs-Buckets ebenfalls leeren
  state.resBuckets.clear();
  document.querySelectorAll('#resDropdown input[type="checkbox"]').forEach(cb => { cb.checked = false; });
  updateResDropdownLabel();
  $("#searchClear").classList.add("hidden");
  // Person-Filter ebenfalls aufheben
  state.personFilter = null;
  state.personFilterBackup = null;
  // Bulk-Auswahl raus
  if (state.selectionMode) setSelectionMode(false);
}

// --- Dialog-Drag ---

// Macht den Player-Dialog per Drag am Header verschiebbar. Bricht ab, wenn
// auf einem interaktiven Element geklickt wird.
function setupDialogDrag() {
  const dlg = $("#playerDialog");
  if (!dlg) return;
  const head = dlg.querySelector(".player-head");
  if (!head) return;
  let sX = 0, sY = 0, sL = 0, sT = 0, dragging = false;
  head.addEventListener("mousedown", (e) => {
    if (e.target.closest("button, select, input, label, a")) return;
    const rect = dlg.getBoundingClientRect();
    sX = e.clientX; sY = e.clientY;
    sL = rect.left; sT = rect.top;
    // Auto-Zentrierung aufheben, absolute Position setzen
    dlg.style.margin = "0";
    dlg.style.left = sL + "px";
    dlg.style.top = sT + "px";
    dragging = true;
    e.preventDefault();
  });
  window.addEventListener("mousemove", (e) => {
    if (!dragging) return;
    const nl = sL + (e.clientX - sX);
    const nt = sT + (e.clientY - sY);
    // leicht im Viewport klemmen
    const maxL = window.innerWidth - 80;
    const maxT = window.innerHeight - 40;
    dlg.style.left = Math.max(-dlg.offsetWidth + 200, Math.min(maxL, nl)) + "px";
    dlg.style.top = Math.max(0, Math.min(maxT, nt)) + "px";
  });
  window.addEventListener("mouseup", () => { dragging = false; });
}

// --- Alphabet-Sidebar (rechter Schnellsprung bei Name-Sortierung) ---

// Nach jedem Render aufgerufen. Zeigt A-Z-Leiste nur bei Namen-Sortierung in
// Movie/TV-Libraries, sonst macht Alphabet keinen Sinn.
function updateAlphaSidebar() {
  const bar = $("#alphaSidebar");
  if (!bar) return;
  const sort = ($("#sortSelect").value || "title");
  const sortByName = sort === "title";
  // In der Collections-Root-Ansicht sind die Kacheln immer alphabetisch nach
  // Sammlungsname sortiert — Alphabet-Sidebar dort unabhängig vom Sort-Feld
  // erlauben.
  const collectionsRoot = !!(state.collectionsView && !state.currentCollection);
  const items = state.lastRenderedItems || [];
  // Folder-Kacheln (z. B. Serien-Ordner in TV-Library-Root oder Show-Ordner
  // in Movies-Library) zählen für die Alphabet-Sidebar mit. Sonst hätte die
  // Hauptseite einer TV-Library keine Buchstabenleiste, weil dort nur
  // Folder gerendert werden, keine Items.
  const folders = state.lastRenderedFolders || [];
  const totalCount = items.length + folders.length;
  // Sidebar erscheint, sobald nach Name sortiert wird (oder in Collections-Root),
  // egal ob 1 oder 1000 Kacheln. Bei leerer Liste blenden wir sie aus.
  if ((!sortByName && !collectionsRoot) || totalCount === 0) {
    bar.classList.add("hidden");
    bar.innerHTML = "";
    document.body.classList.remove("has-alpha-sidebar");
    return;
  }
  document.body.classList.add("has-alpha-sidebar");
  // Vorhandene Anfangsbuchstaben bestimmen — Folders + Items.
  const present = new Set();
  const collect = (title) => {
    const t = (title || "").replace(/^\W+/, "").toUpperCase();
    if (!t) return;
    const ch = t[0];
    if (ch >= "A" && ch <= "Z") present.add(ch);
    else if (/^\d/.test(t)) present.add("#");
  };
  for (const f of folders) collect(folderDisplayTitle(f));
  for (const it of items) collect((it.metadata && it.metadata.title) || it.title);
  const letters = ["#", ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("")];
  bar.innerHTML = "";
  for (const ch of letters) {
    const btn = document.createElement("button");
    btn.textContent = ch;
    btn.type = "button";
    if (!present.has(ch)) btn.disabled = true;
    else btn.addEventListener("click", () => jumpToLetter(ch));
    bar.appendChild(btn);
  }
  bar.classList.remove("hidden");
}

// jumpToLetter: vom Sidebar-Klick aufgerufen. Setzt einen Anfangsbuchstaben-
// Filter — die Kacheln, die nicht mit diesem Buchstaben starten, werden via
// .alpha-hidden-Klasse weggeblendet. Nochmaliger Klick auf denselben
// Buchstaben hebt den Filter wieder auf (Toggle). Schließen geht außerdem
// über den ✕-Chip im Breadcrumb (renderBreadcrumb fügt ihn ein).
function jumpToLetter(ch) {
  setAlphaFilter(state.alphaFilter === ch ? null : ch);
}

function setAlphaFilter(ch) {
  state.alphaFilter = ch || null;
  applyAlphaFilter();
  // Aktiv-Markierung in der Sidebar nachziehen (ohne den ganzen Bar zu rebuilden).
  const bar = $("#alphaSidebar");
  if (bar) {
    for (const btn of bar.querySelectorAll("button")) {
      btn.classList.toggle("is-active", !!ch && btn.textContent === ch);
    }
  }
  // Banner ein-/ausblenden — sitzt zwischen Breadcrumb und Grid und ist
  // immer sichtbar, wenn der Filter aktiv ist (egal in welcher View).
  const banner = $("#alphaFilterBanner");
  const letter = $("#alphaFilterLetter");
  if (banner && letter) {
    if (ch) {
      letter.textContent = ch;
      banner.classList.remove("hidden");
    } else {
      banner.classList.add("hidden");
    }
  }
}

// applyAlphaFilter: läuft nach jedem Render und verbirgt Kacheln, deren
// Anfangsbuchstabe nicht zum aktiven Filter passt. Liest den Titel aus
// `.card-title` der gerade gerenderten Kachel — funktioniert sowohl für
// Folder- als auch Item-Cards, ohne dass beide Render-Funktionen
// einzelne data-Attribute setzen müssen.
function applyAlphaFilter() {
  const grid = $("#grid");
  if (!grid) return;
  const ch = state.alphaFilter;
  for (const card of grid.querySelectorAll(".card")) {
    if (!ch) {
      card.classList.remove("alpha-hidden");
      continue;
    }
    const titleEl = card.querySelector(".card-title");
    const t = (titleEl ? titleEl.textContent : "").replace(/^\W+/, "").toUpperCase();
    let match;
    if (ch === "#") match = /^\d/.test(t);
    else match = !!t && t.startsWith(ch);
    card.classList.toggle("alpha-hidden", !match);
  }
}

// --- Bulk-Selection ---

function setSelectionMode(on) {
  state.selectionMode = on;
  document.body.classList.toggle("selection-mode", on);
  $("#selectModeBtn").classList.toggle("active", on);
  if (!on) {
    state.selection.clear();
    updateBulkBar();
    // Kartenstyling zurücksetzen
    document.querySelectorAll(".card.selected").forEach(c => c.classList.remove("selected"));
    document.querySelectorAll(".card-select").forEach(c => c.textContent = "");
  } else {
    updateBulkBar();
  }
}

function toggleSelection(item) {
  if (state.selection.has(item.id)) {
    state.selection.delete(item.id);
  } else {
    state.selection.add(item.id);
  }
  const card = document.querySelector(`.card[data-item-id="${item.id}"]`);
  if (card) {
    const on = state.selection.has(item.id);
    card.classList.toggle("selected", on);
    const box = card.querySelector(".card-select");
    if (box) box.textContent = on ? "✓" : "";
  }
  updateBulkBar();
}

function updateBulkBar() {
  const bar = $("#bulkBar");
  if (!bar) return;
  if (!state.selectionMode) { bar.classList.add("hidden"); return; }
  bar.classList.remove("hidden");
  $("#bulkCount").textContent = `${state.selection.size} ausgewählt`;
}

function selectAllVisible() {
  for (const it of state.lastRenderedItems || []) {
    if (it && it.id) state.selection.add(it.id);
  }
  // Alle Kacheln visuell markieren
  document.querySelectorAll(".card").forEach(c => {
    const id = Number(c.dataset.itemId);
    if (id && state.selection.has(id)) {
      c.classList.add("selected");
      const b = c.querySelector(".card-select");
      if (b) b.textContent = "✓";
    }
  });
  updateBulkBar();
}

function selectedItems() {
  return (state.lastRenderedItems || []).filter(it => state.selection.has(it.id));
}

async function bulkSetFavorite() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  let fails = 0;
  for (const id of ids) {
    try {
      await api(`/api/items/${id}/favorite`, { method: "PUT", body: JSON.stringify({ favorite: true }) });
    } catch { fails++; }
  }
  if (fails) appAlert(`${fails} von ${ids.length} fehlgeschlagen.`);
  setSelectionMode(false);
  loadItems();
}

async function bulkSetWatched() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  let fails = 0;
  for (const id of ids) {
    try {
      await api(`/api/items/${id}/watched`, { method: "PUT", body: JSON.stringify({ watched: true }) });
    } catch { fails++; }
  }
  if (fails) appAlert(`${fails} von ${ids.length} fehlgeschlagen.`);
  setSelectionMode(false);
  loadItems();
}

async function bulkAddToPlaylist() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  // Wiederverwendung des Detail-Dialogs: zeigt die Liste aller Playlists UND
  // das Quick-Create-Formular, sodass auch aus der Bulk-Auswahl direkt eine
  // neue Playlist mit den gewählten Videos erstellt werden kann.
  await openAddToPlaylist({ itemIDs: ids });
}

async function bulkDownload() {
  const items = selectedItems();
  if (!items.length) return;
  if (!(await appConfirm(`${items.length} Dateien herunterladen? Dein Browser blockiert evtl. mehrfache Downloads — bestätige die Nachfrage.`))) return;
  // Zeitversetzt triggern, damit Browser nicht als Popup-Spam blockt
  items.forEach((it, i) => {
    setTimeout(() => {
      const a = document.createElement("a");
      a.href = `/api/download/${it.id}`;
      a.download = "";
      document.body.appendChild(a);
      a.click();
      a.remove();
    }, i * 400);
  });
}

async function bulkDelete() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  if (!(await appConfirm(`${ids.length} Dateien endgültig löschen (inkl. Datei auf Disk)?`))) return;
  let fails = 0;
  for (const id of ids) {
    try {
      await api(`/api/items/${id}?deleteFile=true`, { method: "DELETE" });
    } catch { fails++; }
  }
  if (fails) appAlert(`${fails} von ${ids.length} fehlgeschlagen.`);
  setSelectionMode(false);
  loadItems();
}

// Manueller Duplikat-Merge: alle ausgewählten Items erhalten dieselbe
// metadata_id wie das erste Item mit Zuordnung. Das Grid gruppiert sie
// danach automatisch zu einer Kachel (groupVariants).
async function bulkMerge() {
  const ids = Array.from(state.selection);
  if (ids.length < 2) { appAlert("Mindestens 2 Videos auswählen."); return; }
  // Kleines Preview der Titel, damit der User sieht, was zusammengeführt wird.
  const titles = ids.slice(0, 5)
    .map(id => (state.lastRenderedItems || []).find(it => it.id === id))
    .filter(Boolean)
    .map(it => `• ${(it.metadata && it.metadata.title) || it.title}`)
    .join("\n");
  const extra = ids.length > 5 ? `\n… und ${ids.length - 5} weitere` : "";
  if (!(await appConfirm(`Diese ${ids.length} Videos als Duplikate zusammenführen?\n\n${titles}${extra}\n\nAlle erhalten dieselbe TMDB-Zuordnung wie das erste bereits zugeordnete Item.`))) return;
  try {
    await api("/api/items/merge", { method: "POST", body: JSON.stringify({ ids }) });
    setSelectionMode(false);
    await loadItems();
  } catch (e) { appAlert("Merge fehlgeschlagen: " + e.message); }
}

// Schlüssel für die pro-Ordner-Sortierung im localStorage.
// Staffel-Ansicht-Einstellungen (3-stufig):
//  - pro Serie:  seasonView:<libID>:<folder> = "1" / "0"  (setzt Default außer Kraft)
//  - pro Library: seasonView:lib:<libID>         = "1" / "0"  (Default für alle Serien)
// Effective = pro Serie if set, else library-Default, else false.
function seasonViewKey() {
  if (state.currentFolder) {
    return `seasonView:${state.currentLibrary || 0}:${state.currentFolder}`;
  }
  return `seasonView:lib:${state.currentLibrary || 0}`;
}
function seasonViewEffective() {
  try {
    if (state.currentFolder) {
      const perFolder = localStorage.getItem(`seasonView:${state.currentLibrary || 0}:${state.currentFolder}`);
      if (perFolder === "1") return true;
      if (perFolder === "0") return false;
    }
    return localStorage.getItem(`seasonView:lib:${state.currentLibrary || 0}`) === "1";
  } catch { return false; }
}

function sortStorageKey() {
  if (state.currentLibrary) {
    const folder = state.currentFolder || "";
    return `sort:lib:${state.currentLibrary}:${folder}`;
  }
  if (state.collectionsView) return "sort:collections";
  if (state.playlistsView) return "sort:playlists";
  if (state.personFilter) return "sort:person";
  return "";
}

// Pseudo-Filter-Modi (im Sort-Dropdown technisch ein "Sort", semantisch
// aber globale Filter): nicht per-Folder persistieren — sonst bekommt der
// User beim erneuten Öffnen des Ordners überraschend „Nur unmatched".
// Gleichzeitig sollen sie beim Folder-Wechsel NICHT durch die per-Folder-
// Default-Sortierung ersetzt werden — sonst verliert man den Filter sofort
// beim Reingehen und sieht alle Items, statt der unmatched/duplicates/etc.
const PSEUDO_FILTER_MODES = new Set([
  "unmatched", "favorites", "duplicates", "suspicious", "interlaced",
]);

function persistSortForContext() {
  const key = sortStorageKey();
  if (!key) return;
  const s = $("#sortSelect").value;
  if (PSEUDO_FILTER_MODES.has(s)) return;
  const d = state.sortDir;
  try {
    if (!s && !d) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify({ sort: s, dir: d }));
  } catch {}
}

function restoreSortForContext() {
  // Aktiven Pseudo-Filter beibehalten — global gedacht, nicht per-Folder.
  if (PSEUDO_FILTER_MODES.has($("#sortSelect").value)) return;
  const key = sortStorageKey();
  if (!key) return;
  try {
    const raw = localStorage.getItem(key);
    if (!raw) {
      // Kein gespeicherter Sort für diesen Kontext → Library-spezifischer Default.
      // Private Libraries (YouTube-Downloads, Urlaubsvideos etc.) sollen nach
      // „Veröffentlicht" absteigend sortiert sein — Chronologie ist dort die
      // sinnvolle Reihenfolge. Alle anderen bleiben auf "title" aufsteigend.
      const lib = state.libraries && state.libraries.find(l => l.id == state.currentLibrary);
      if (lib && lib.kind === "private") {
        // Chronologisch aufsteigend: älteste zuerst (bei YouTube/Urlaubsvideos
        // üblicherweise die Wunsch-Reihenfolge). `effectiveSortDir` würde für
        // "released" sonst "desc" liefern, deshalb explizit "asc" setzen.
        $("#sortSelect").value = "released";
        state.sortDir = "asc";
      } else {
        $("#sortSelect").value = "title";
        state.sortDir = "";
      }
      updateSortDirIcon();
      return;
    }
    const { sort, dir } = JSON.parse(raw);
    if (sort) $("#sortSelect").value = sort;
    state.sortDir = dir || "";
    updateSortDirIcon();
  } catch {}
}

// Standard-Richtung je Sort-Feld. "title" wird natürlich aufsteigend sortiert,
// zeitbezogene Felder absteigend (neueste zuerst).
function effectiveSortDir() {
  if (state.sortDir === "asc" || state.sortDir === "desc") return state.sortDir;
  const s = $("#sortSelect").value;
  return s === "title" || s === "episode" ? "asc" : "desc";
}

function updateSortDirIcon() {
  const btn = $("#sortDirBtn");
  if (!btn) return;
  // Modi ohne sinnvolle Richtung: Pseudo-Filter (Favoriten/Duplikate/etc.)
  // und „Zufällig". Button wird ausgegraut + disabled, damit der User nicht
  // erwartet dass ein Klick was bewirkt.
  const v = $("#sortSelect").value || "title";
  const directionless = (v === "shuffle" || v === "favorites" || v === "unmatched"
                       || v === "duplicates" || v === "suspicious" || v === "interlaced");
  if (directionless) {
    btn.disabled = true;
    btn.classList.add("is-disabled");
    btn.textContent = v === "shuffle" ? "🎲" : "—";
    btn.title = v === "shuffle" ? "Zufällige Reihenfolge — keine Sortierrichtung" : "Sortierrichtung in diesem Modus nicht relevant";
    return;
  }
  btn.disabled = false;
  btn.classList.remove("is-disabled");
  btn.textContent = effectiveSortDir() === "asc" ? "⬆" : "⬇";
  btn.title = effectiveSortDir() === "asc" ? "Aufsteigend · klick für absteigend" : "Absteigend · klick für aufsteigend";
}

// Multi-Select-Auflösungs-Filter: alle ausgewählten Buckets als `bucket=xxx`
// an den Server übergeben. Backend ORed die Höhen-Ranges.
function applyResolutionFilter(params) {
  for (const b of state.resBuckets) params.append("bucket", b);
}

function setupResolutionDropdown() {
  const btn = $("#resDropdownBtn");
  const panel = $("#resDropdown");
  if (!btn || !panel) return;
  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = panel.classList.toggle("hidden");
    btn.setAttribute("aria-expanded", String(!open));
  });
  panel.addEventListener("click", (e) => e.stopPropagation());
  document.addEventListener("click", () => {
    if (!panel.classList.contains("hidden")) {
      panel.classList.add("hidden");
      btn.setAttribute("aria-expanded", "false");
    }
  });
  panel.querySelectorAll('input[type="checkbox"][data-bucket]').forEach(cb => {
    cb.addEventListener("change", () => {
      const b = cb.dataset.bucket;
      if (cb.checked) state.resBuckets.add(b);
      else state.resBuckets.delete(b);
      updateResDropdownLabel();
      loadItems();
    });
  });
  updateResDropdownLabel();
}

function updateResDropdownLabel() {
  const btn = $("#resDropdownBtn");
  if (!btn) return;
  const n = state.resBuckets.size;
  if (n === 0) {
    btn.textContent = "Alle ▾";
  } else if (n <= 2) {
    btn.textContent = Array.from(state.resBuckets).join(", ") + " ▾";
  } else {
    btn.textContent = `${n} Auflösungen ▾`;
  }
}

async function toggleWatchedOnCard(item, btn) {
  const newState = !item.watched;
  btn.disabled = true;
  try {
    await api(`/api/items/${item.id}/watched`, {
      method: "PUT",
      body: JSON.stringify({ watched: newState }),
    });
    item.watched = newState;
    btn.classList.toggle("is-on", newState);
    btn.title = newState ? "Als ungesehen markieren" : "Als gesehen markieren";
    // Card-Dimming auf "watched"-Klasse halten
    const card = btn.closest(".card");
    if (card) card.classList.toggle("watched", newState);
  } catch (e) {
    appAlert(e.message);
  } finally {
    btn.disabled = false;
  }
}

async function toggleFavoriteOnCard(item, btn) {
  const newState = !item.favorite;
  btn.disabled = true;
  try {
    await api(`/api/items/${item.id}/favorite`, {
      method: "PUT",
      body: JSON.stringify({ favorite: newState }),
    });
    item.favorite = newState;
    btn.classList.toggle("is-on", newState);
    btn.textContent = newState ? "♥" : "♡";
    btn.title = newState ? "Aus Favoriten entfernen" : "Zu Favoriten hinzufügen";
  } catch (e) {
    appAlert(e.message);
  } finally {
    btn.disabled = false;
  }
}

function resLabel(it) {
  if (!it) return "";
  const w = it.width || 0;
  const h = it.height || 0;
  // Cinemascope-Filme (2.40:1) sind bei 1080p-Quelle nur ~800 Pixel hoch,
  // aber volle 1920 breit. Wir nehmen deshalb das Maximum aus Höhe und auf
  // 16:9 normalisierter Breite als „effektive Höhe" — dann landen sie im
  // korrekten 1080p-Bucket.
  const effH = Math.max(h, Math.round(w * 9 / 16));
  if (effH >= 2000) return "4K";
  if (effH >= 1400) return "2K";
  if (effH >= 1000) return "1080p";
  if (effH >= 700) return "720p";
  if (effH >= 540) return "576p";
  if (effH >= 440) return "480p";
  if (effH >= 300) return "360p";
  if (effH > 0) return effH + "p";
  return "";
}

// --- App-Dialoge (Ersatz für native alert/confirm/prompt) ---
// Ein einzelnes <dialog>-Element wird je nach Modus umgeschaltet.
// appAlert(msg)   — Promise<void>: nur OK-Button
// appConfirm(msg) — Promise<boolean>: OK + Abbrechen
// appPrompt(msg, default) — Promise<string|null>: Input + OK/Abbrechen
// Alle Fenster rendern sich im App-eigenen Stil, blockieren Focus auf den
// Dialog und schließen bei Escape oder Backdrop-Klick.
let appDialogQueue = Promise.resolve();
function appDialog({ title = "", body = "", input = null, showCancel = false, okLabel = "OK", cancelLabel = "Abbrechen", danger = false }) {
  return (appDialogQueue = appDialogQueue.then(() => new Promise(resolve => {
    const dlg = document.getElementById("appDialog");
    const tEl = document.getElementById("appDialogTitle");
    const bEl = document.getElementById("appDialogBody");
    const inputRow = document.getElementById("appDialogInputRow");
    const inputEl = document.getElementById("appDialogInput");
    const okBtn = document.getElementById("appDialogOk");
    const cancelBtn = document.getElementById("appDialogCancel");
    if (!dlg) { resolve(showCancel ? (input != null ? null : false) : undefined); return; }

    tEl.textContent = title || "";
    tEl.classList.toggle("hidden", !title);
    bEl.innerHTML = body ? String(body).replace(/\n/g, "<br>") : "";
    okBtn.textContent = okLabel;
    okBtn.classList.toggle("danger", !!danger);
    okBtn.classList.toggle("primary", !danger);
    cancelBtn.textContent = cancelLabel;
    cancelBtn.classList.toggle("hidden", !showCancel);

    if (input != null) {
      inputRow.classList.remove("hidden");
      inputEl.value = String(input);
      inputEl.type = "text";
    } else {
      inputRow.classList.add("hidden");
      inputEl.value = "";
    }

    const cleanup = () => {
      okBtn.removeEventListener("click", onOk);
      cancelBtn.removeEventListener("click", onCancel);
      dlg.removeEventListener("cancel", onCancel);
      dlg.removeEventListener("keydown", onKey);
      dlg.close();
    };
    const onOk = () => {
      const result = input != null ? inputEl.value : (showCancel ? true : undefined);
      cleanup();
      resolve(result);
    };
    const onCancel = (ev) => {
      if (ev) ev.preventDefault();
      cleanup();
      resolve(input != null ? null : (showCancel ? false : undefined));
    };
    const onKey = (e) => {
      if (e.key === "Enter" && input != null) {
        e.preventDefault();
        onOk();
      }
    };
    okBtn.addEventListener("click", onOk);
    cancelBtn.addEventListener("click", onCancel);
    dlg.addEventListener("cancel", onCancel);
    dlg.addEventListener("keydown", onKey);
    dlg.showModal();
    // Fokus nach dem showModal setzen
    setTimeout(() => { (input != null ? inputEl : okBtn).focus(); }, 0);
  })));
}
function appAlert(msg, opts = {}) {
  return appDialog({ title: opts.title || "", body: msg, showCancel: false, okLabel: opts.okLabel || "OK" });
}

// showToast: unaufdringlicher Hinweis rechts unten für 3 s. Nicht modal.
// Nutzung: showToast("Ist schon in der Playlist") oder showToast(msg, {kind:"error"}).
function showToast(msg, opts = {}) {
  const kind = opts.kind || "info";
  let root = document.getElementById("toastRoot");
  if (!root) {
    root = document.createElement("div");
    root.id = "toastRoot";
    document.body.appendChild(root);
  }
  const t = document.createElement("div");
  t.className = `toast toast--${kind}`;
  t.textContent = msg;
  root.appendChild(t);
  // Reflow erzwingen, damit CSS-Transition greift.
  void t.offsetWidth;
  t.classList.add("toast--shown");
  setTimeout(() => {
    t.classList.remove("toast--shown");
    setTimeout(() => t.remove(), 300);
  }, opts.duration || 3000);
}
function appConfirm(msg, opts = {}) {
  return appDialog({
    title: opts.title || "",
    body: msg,
    showCancel: true,
    okLabel: opts.okLabel || "OK",
    cancelLabel: opts.cancelLabel || "Abbrechen",
    danger: !!opts.danger,
  });
}
function appPrompt(msg, defaultValue = "", opts = {}) {
  return appDialog({
    title: opts.title || "",
    body: msg,
    input: defaultValue == null ? "" : String(defaultValue),
    showCancel: true,
    okLabel: opts.okLabel || "OK",
  });
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, c => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

// --- Libraries ---

async function loadLibraries() {
  [state.libraries, state.playlists] = await Promise.all([
    api("/api/libraries"),
    api("/api/playlists").catch(() => []),
  ]);
  const sel = $("#librarySelect");
  sel.innerHTML = "";
  if (state.libraries.length === 0 && state.playlists.length === 0) {
    sel.innerHTML = `<option value="">— keine Quelle —</option>`;
    state.currentLibrary = null;
    state.currentPlaylist = null;
    return;
  }
  if (state.libraries.length) {
    const og = document.createElement("optgroup");
    og.label = "Bibliotheken";
    for (const l of state.libraries) {
      const o = document.createElement("option");
      o.value = "lib:" + l.id;
      o.textContent = l.name;
      og.appendChild(o);
    }
    sel.appendChild(og);
  }
  // Playlists werden NICHT ins Library-Dropdown eingebaut — eigener Button
  // im Header zeigt die Playlist-Übersicht.
  // Aktuelle Auswahl wiederherstellen oder Default setzen
  let currentVal = "";
  if (state.currentPlaylist && state.playlists.find(p => p.id == state.currentPlaylist)) {
    currentVal = "pl:" + state.currentPlaylist;
  } else if (state.currentLibrary && state.libraries.find(l => l.id == state.currentLibrary)) {
    currentVal = "lib:" + state.currentLibrary;
  } else if (state.libraries.length) {
    currentVal = "lib:" + state.libraries[0].id;
    state.currentLibrary = state.libraries[0].id;
    state.currentPlaylist = null;
  } else if (state.playlists.length) {
    currentVal = "pl:" + state.playlists[0].id;
    state.currentPlaylist = state.playlists[0].id;
    state.currentLibrary = null;
  }
  sel.value = currentVal;
  renderLibNav();
}

// goHomeView / goCollectionsView / goLibraryView wechseln in den jeweiligen
// Bereich und resetten die Navigation. Werden sowohl von der pinned Lib-Nav
// als auch aus diversen Codepfaden (Home-Kachel, Search-Result-Klick, …) aufgerufen.
function goHomeView() {
  state.homeView = true;
  state.currentLibrary = null;
  state.currentFolder = null;
  state.currentPlaylist = null;
  state.collectionsView = false;
  state.currentCollection = null;
  state.playlistsView = false;
  state.personFilter = null;
  const sel = $("#librarySelect"); if (sel) sel.value = "";
  resetFilters();
  loadItems();
}
function goCollectionsView() {
  state.collectionsView = true;
  state.currentCollection = null;
  state.currentLibrary = null;
  state.currentFolder = null;
  state.currentPlaylist = null;
  state.playlistsView = false;
  state.homeView = false;
  state.personFilter = null;
  const sel = $("#librarySelect"); if (sel) sel.value = "";
  resetFilters();
  loadItems();
}
function goPlaylistsView() {
  enterPlaylistsView();
  const sel = $("#librarySelect"); if (sel) sel.value = "";
  loadItems();
}
function goLibraryView(libId) {
  state.currentFolder = null;
  state.currentFolderDrilldown = null;
  state.currentSeason = null;
  state.collectionsView = false;
  state.currentCollection = null;
  state.playlistsView = false;
  state.currentPlaylist = null;
  state.homeView = false;
  state.personFilter = null;
  state.currentLibrary = Number(libId) || null;
  const sel = $("#librarySelect");
  if (sel) sel.value = state.currentLibrary ? "lib:" + state.currentLibrary : "";
  resetFilters();
  loadItems();
}

// libIcon: Emoji-Icon pro Library-Kind. Gemeinsam genutzt von Lib-Nav und
// Breadcrumb, damit Icon und Name immer konsistent sind.
function libIcon(lib) {
  if (!lib) return "📁";
  if (lib.kind === "movies") return "🎬";
  if (lib.kind === "tv") return "📺";
  if (lib.kind === "private") return "🎥";
  return "📁";
}

// renderLibNav baut die zentrale, sticky Buttons-Leiste mit allen Quellen:
// Startseite · [eine Library pro Button] · Sammlungen · Playlists.
// Aktive Auswahl wird hervorgehoben. Wird nach loadLibraries() und nach jedem
// Navigationswechsel (in renderBreadcrumb) gerufen.
function renderLibNav() {
  const nav = $("#libNav");
  if (!nav) return;
  nav.innerHTML = "";

  const make = (label, isActive, onClick, title) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "lib-nav-btn" + (isActive ? " is-active" : "");
    b.textContent = label;
    if (title) b.title = title;
    b.addEventListener("click", onClick);
    return b;
  };
  const sep = () => { const s = document.createElement("span"); s.className = "lib-nav-sep"; return s; };

  // Startseite
  nav.appendChild(make("🏠 Startseite", !!state.homeView, goHomeView, "Startseite"));

  // Pro Library ein Button
  const libs = state.libraries || [];
  if (libs.length) nav.appendChild(sep());
  for (const l of libs) {
    const active = !state.homeView && !state.collectionsView && !state.playlistsView
      && !state.currentPlaylist && !state.personFilter && state.currentLibrary == l.id;
    nav.appendChild(make(`${libIcon(l)} ${l.name}`, active, () => goLibraryView(l.id), l.name));
  }

  // Sammlungen + Playlists
  nav.appendChild(sep());
  nav.appendChild(make("📚 Sammlungen", !!state.collectionsView, goCollectionsView, "Sammlungen"));
  nav.appendChild(make("📋 Playlists", !!(state.playlistsView || state.currentPlaylist), goPlaylistsView, "Playlists"));
}

// navKey: stabiler String, der den aktuellen Grid-Kontext beschreibt.
// Wird für scrollPositions (Map: navKey → scrollY) genutzt, damit die Seite
// beim Zurück-Navigieren wieder an der vorherigen Y-Position landet.
function navKey() {
  if (state.homeView) return "home";
  if (state.collectionsView) return state.currentCollection ? "col:" + state.currentCollection.id : "col-root";
  if (state.playlistsView && !state.currentPlaylist) return "pl-root";
  if (state.currentPlaylist) return "pl:" + state.currentPlaylist;
  if (state.personFilter) return "person:" + state.personFilter.tmdbId;
  if (state.currentLibrary) {
    const f = state.currentFolder || "";
    const s = state.currentSeason != null ? ":s" + state.currentSeason : "";
    return "lib:" + state.currentLibrary + ":" + f + s;
  }
  return "root";
}

async function loadItems() {
  // Save: vor jedem Re-Render aktuellen ScrollY für den vorigen NavKey ablegen.
  // Restore: nach Render erfolgt im finally-Block — funktioniert auch bei den
  // vielen frühen returns weiter unten (return innerhalb try → finally läuft).
  if (state.lastNavKey) {
    state.scrollPositions.set(state.lastNavKey, window.scrollY);
  }
  const targetKey = navKey();
  // Bei Navigations-Wechsel den Anfangsbuchstaben-Filter zurücksetzen — er
  // ist immer kontextspezifisch (z. B. „M in Filme") und macht in einer
  // anderen Library/Ordner keinen Sinn mehr. setAlphaFilter pflegt
  // gleichzeitig das Banner und die Sidebar-Markierung.
  if (state.lastNavKey && state.lastNavKey !== targetKey && state.alphaFilter) {
    setAlphaFilter(null);
  }
  state.lastNavKey = targetKey;
  try {
    return await loadItemsBody();
  } finally {
    // Nach jedem Render den Alpha-Filter erneut anwenden (DOM-Cards sind
    // frisch und haben die .alpha-hidden-Klasse noch nicht).
    applyAlphaFilter();
    // requestAnimationFrame: Browser hatte einen Frame Zeit, das Grid zu
    // layouten — sonst landen wir auf scrollY=0 weil der Inhalt noch nicht
    // hoch genug ist. Doppelt rAF, weil rendering manchmal zwei Frames
    // braucht (gerade bei content-visibility:auto auf Cards).
    requestAnimationFrame(() => requestAnimationFrame(() => {
      const saved = state.scrollPositions.get(targetKey);
      window.scrollTo(0, saved != null ? saved : 0);
    }));
  }
}

async function loadItemsBody() {
  const grid = $("#grid");
  // Default: keine Folder im Render-Snapshot. Der Standard-Library-Pfad
  // setzt das später auf die echte Folder-Liste; alle anderen Pfade
  // (Favoriten/Duplikate/Suspicious/Interlaced/Person/Collections/Playlists)
  // rendern keine Folders, also Reset hier sauber halten — sonst würden
  // Alphabet-Sidebar-Buchstaben aus einer vorherigen Library-Root weiterleben.
  state.lastRenderedFolders = [];
  // Sortierung wiederherstellen, wenn der Kontext (Library/Folder/…) gewechselt
  // hat — so merkt sich jeder Ordner seine zuletzt gewählte Sortierung.
  const ctxKey = sortStorageKey();
  if (ctxKey !== state.lastSortContextKey) {
    state.lastSortContextKey = ctxKey;
    restoreSortForContext();
  }
  // Sequenz-Token: wenn in der Zwischenzeit ein neuer loadItems-Aufruf startet
  // (z. B. durch schnelles Tippen ins Suchfeld), sollen unsere stale-Responses
  // das Grid nicht mehr überschreiben.
  const mySeq = ++state.loadSeq;
  const stale = () => mySeq !== state.loadSeq;
  renderBreadcrumb({});

  // Staffel-Toggle-Button: sichtbar in TV-Libraries (Root + Show-Folder).
  // Effective-State wird aus library-weitem Default + optional pro-Serie-Override gebildet.
  {
    const lib0 = state.libraries.find(l => l.id == state.currentLibrary);
    const eligible = lib0 && lib0.kind === "tv";
    $("#seasonViewBtn").classList.toggle("hidden", !eligible);
    if (!eligible) { state.seasonView = false; state.currentSeason = null; }
    else {
      state.seasonView = seasonViewEffective();
      $("#seasonViewBtn").classList.toggle("active", !!state.seasonView);
      // Kein Show-Folder offen oder Toggle deaktiviert → Season zurücksetzen
      if (!state.currentFolder || !state.seasonView) state.currentSeason = null;
    }
  }

  // Trickplay-Fehler-View (aus dem Trickplay-Manager-Dialog ausgelöst):
  // flache Library-übergreifende Liste aller Items mit trickplay_status=failed.
  // ✕ im Breadcrumb öffnet den Trickplay-Manager-Dialog wieder.
  if (state.tpFailedView) {
    const params = new URLSearchParams({ trickplay: "failed", sort: "title", dir: "asc" });
    let items = [];
    try { items = await apiGetCached(`/api/items?${params}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({ tpFailedView: true, searchCount: items.length });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine Items mit Trickplay-Fehler.</div>`;
      return;
    }
    const merged = groupVariants(items);
    state.lastRenderedItems = merged;
    grid.innerHTML = "";
    const frag = document.createDocumentFragment();
    for (const it of merged) frag.appendChild(renderCard(it));
    grid.appendChild(frag);
    return;
  }

  // Startseite: bei aktiver Suche wird sie zu einer globalen Suche quer
  // über alle Libraries; sonst die drei klassischen Sektionen aus /api/home.
  if (state.homeView) {
    const sq = $("#searchInput").value.trim();
    $("#searchClear").classList.toggle("hidden", sq === "");
    if (sq) {
      let items = [];
      try {
        const params = new URLSearchParams({ search: sq, sort: "title", dir: "asc" });
        items = await apiGetCached(`/api/items?${params}`);
      } catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
      if (stale()) return;
      renderBreadcrumb({ homeRoot: true, searchCount: items.length });
      if (!items.length) {
        grid.innerHTML = `<div class="empty">Keine Treffer für „${escapeHTML(sq)}" in allen Bibliotheken.</div>`;
        return;
      }
      const merged = groupVariants(items);
      state.lastRenderedItems = merged;
      grid.innerHTML = "";
      const frag = document.createDocumentFragment();
      for (const it of merged) frag.appendChild(renderCard(it));
      grid.appendChild(frag);
      return;
    }
    let data;
    try { data = await api("/api/home"); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({ homeRoot: true });
    renderHomeView(grid, data);
    return;
  }

  // Playlist-Ansicht
  if (state.currentPlaylist) {
    let items = [];
    try {
      items = await api(`/api/playlists/${state.currentPlaylist}/items`);
    } catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    // Client-seitige Filter anwenden (Suche/Favorit/Watched)
    const q = $("#searchInput").value.trim().toLowerCase();
    const wat = $("#watchedFilter").value;
    const fav = currentFavoriteMode();
    items = items.filter(it => {
      if (q && !(it.title || "").toLowerCase().includes(q) && !((it.metadata && it.metadata.title) || "").toLowerCase().includes(q)) return false;
      if (wat === "yes" && !it.watched) return false;
      if (wat === "no" && it.watched) return false;
      if (fav === "yes" && !it.favorite) return false;
      return true;
    });
    $("#searchClear").classList.toggle("hidden", q === "");
    state.playQueue = items;
    state.lastRenderedItems = items;
    if (q) {
      // Bei aktiver Suche: Breadcrumb mit Treffer-Anzahl aktualisieren
      renderBreadcrumb({ searchCount: items.length });
    }
    if (!items.length) {
      grid.innerHTML = q
        ? `<div class="empty">Keine Treffer für „${escapeHTML(q)}".</div>`
        : `<div class="empty">Playlist ist leer. Füge Videos via „📋 Zu Playlist…" im Detail-Dialog hinzu.</div>`;
      return;
    }
    const frag = document.createDocumentFragment();
    items.forEach((it, idx) => frag.appendChild(renderCard(it, { queueIdx: idx })));
    grid.innerHTML = "";
    grid.appendChild(frag);
    return;
  }

  // Playlists-Ansicht (Root): zeigt alle Playlists als Kacheln + eigene Toolbar.
  if (state.playlistsView && !state.currentPlaylist) {
    let pls = [];
    try { pls = await api("/api/playlists"); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({ playlistsRoot: true, searchCount: pls.length });
    // Toolbar oberhalb des Grids mit "+ Neue Playlist"-Button
    grid.innerHTML = "";
    const toolbar = document.createElement("div");
    toolbar.className = "subview-toolbar";
    const addBtn = document.createElement("button");
    addBtn.className = "primary";
    addBtn.textContent = "+ Neue Playlist";
    addBtn.addEventListener("click", openPlaylistsManager);
    toolbar.appendChild(addBtn);
    const mgr = document.createElement("button");
    mgr.textContent = "Verwalten";
    mgr.addEventListener("click", openPlaylistsManager);
    toolbar.appendChild(mgr);
    grid.appendChild(toolbar);
    if (!pls.length) {
      const empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = 'Noch keine Playlists angelegt. Klicke oben auf "+ Neue Playlist".';
      grid.appendChild(empty);
      return;
    }
    const wrap = document.createElement("div");
    wrap.className = "subview-grid";
    for (const p of pls) wrap.appendChild(renderPlaylistCard(p));
    grid.appendChild(wrap);
    return;
  }

  // Collections-Ansicht: zeigt alle TMDB-Sammlungen, in die mindestens ein Film fällt.
  if (state.collectionsView) {
    const searchQ = $("#searchInput").value.trim();
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    // Geöffnete Collection: alle TMDB-Parts (eigene + fehlende) nebeneinander.
    if (state.currentCollection) {
      let parts = [];
      try { parts = await api(`/api/collections/${state.currentCollection.id}/items`); }
      catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
      if (stale()) return;
      // Suche: Titel der Parts filtern
      if (searchQ) {
        const q = searchQ.toLowerCase();
        parts = parts.filter(p => {
          const t = (p.title || (p.item && p.item.title) || (p.metadata && p.metadata.title) || "").toLowerCase();
          return t.includes(q);
        });
      }
      renderBreadcrumb({ collectionName: state.currentCollection.name, searchCount: parts.length });
      if (!parts.length) {
        grid.innerHTML = searchQ
          ? `<div class="empty">Keine Treffer für „${escapeHTML(searchQ)}" in dieser Sammlung.</div>`
          : `<div class="empty">Keine Filme in dieser Sammlung.</div>`;
        return;
      }
      // Wenn die erste Zeile "owned" als Schlüssel hat → Parts-Antwort (mit Fehlt-Markern),
      // sonst Fallback (normale Item-Liste für noch nicht gefetchte Collections).
      const isParts = parts[0] && (parts[0].owned !== undefined);
      // Chronologisch nach Release-Datum sortieren
      parts.sort((a, b) => {
        const ay = isParts ? (a.releaseDate || "9999") : String((a.metadata && a.metadata.year) || 9999);
        const by = isParts ? (b.releaseDate || "9999") : String((b.metadata && b.metadata.year) || 9999);
        return ay.localeCompare(by);
      });
      // Per-User ausgeblendete Parts filtern (außer im Debug-/Toggle-Modus)
      const hiddenCount = isParts ? parts.filter(p => p.hidden).length : 0;
      const visibleParts = isParts && !state.showHiddenParts
        ? parts.filter(p => !p.hidden)
        : parts;
      state.lastRenderedItems = isParts
        ? visibleParts.filter(p => p.owned).map(p => ({ id: p.itemId, metadataId: p.metadataId }))
        : parts;
      const frag = document.createDocumentFragment();
      for (const p of visibleParts) {
        if (isParts) frag.appendChild(renderCollectionPartCard(p, state.currentCollection.id));
        else frag.appendChild(renderCard(p));
      }
      grid.innerHTML = "";
      grid.appendChild(frag);
      // Footer: wenn es ausgeblendete Parts gibt, Toggle-Link zum Wiederanzeigen.
      if (hiddenCount > 0) {
        const footer = document.createElement("div");
        footer.className = "collection-hidden-footer";
        const label = state.showHiddenParts
          ? `${hiddenCount} ausgeblendet · nur Standard zeigen`
          : `${hiddenCount} ausgeblendet · alle anzeigen`;
        footer.innerHTML = `<button class="link-btn" type="button">${label}</button>`;
        footer.querySelector("button").addEventListener("click", () => {
          state.showHiddenParts = !state.showHiddenParts;
          loadItems();
        });
        grid.appendChild(footer);
      }
      return;
    }
    // Root-Liste: alle Sammlungen als Kacheln
    let cols = [];
    try { cols = await api("/api/collections"); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    // Suche: Collection-Name filtern
    if (searchQ) {
      const q = searchQ.toLowerCase();
      cols = cols.filter(c => (c.name || "").toLowerCase().includes(q));
    }
    renderBreadcrumb({ collectionsRoot: true, searchCount: cols.length });
    if (!cols.length) {
      grid.innerHTML = searchQ
        ? `<div class="empty">Keine Sammlungen für „${escapeHTML(searchQ)}".</div>`
        : `<div class="empty">Noch keine Sammlungen gefunden. Scanne eine Film-Bibliothek und TMDB markiert Filme mit ihrer Collection (z. B. James Bond).</div>`;
      return;
    }
    // Für Alphabet-Sidebar: als „Items" mit .title = c.name mappen, damit
    // updateAlphaSidebar/jumpToLetter Sprungziele findet (Cards haben
    // data-item-id="col-<id>", das wird vom Jumper verwendet).
    state.lastRenderedItems = cols.map(c => ({ id: `col-${c.id}`, title: c.name }));
    const frag = document.createDocumentFragment();
    for (const c of cols) frag.appendChild(renderCollectionCard(c));
    grid.innerHTML = "";
    grid.appendChild(frag);
    updateAlphaSidebar();
    return;
  }

  // Person-Filter-Ansicht: zeigt quer über alle Libraries alle Items, bei denen
  // die gewählte Person im Cast ist. Filme und Serien werden getrennt gerendert:
  //   - Filme: Standard-Kacheln, chronologisch sortiert (neueste zuerst)
  //   - Serien: pro Show genau EINE Kachel mit Show-Poster. Klick navigiert zur
  //     Serie wie aus dem Episoden-Titel heraus.
  if (state.personFilter) {
    const searchQ = $("#searchInput").value.trim();
    const p = new URLSearchParams({
      personId: String(state.personFilter.tmdbId),
      sort: "title",  // Server-Sort ist egal — wir re-sortieren client-seitig.
    });
    if (searchQ) p.set("search", searchQ);
    const watched = $("#watchedFilter").value; if (watched) p.set("watched", watched);
    const fav = currentFavoriteMode(); if (fav) p.set("favorite", fav);
    applyResolutionFilter(p);
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    let items = [];
    try { items = await apiGetCached(`/api/items?${p}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;

    const movies = items.filter(x => x.metadata && x.metadata.tmdbType === "movie");
    const episodes = items.filter(x => x.metadata && x.metadata.tmdbType === "episode");

    // Movies: chronologisch, neueste zuerst
    movies.sort((a, b) => {
      const aD = (a.metadata && a.metadata.releaseDate) || a.releasedAt || "";
      const bD = (b.metadata && b.metadata.releaseDate) || b.releasedAt || "";
      return String(bD).localeCompare(String(aD));
    });

    // Episoden: pro Show (libraryId + rel_path[0]) eine Sammelkachel.
    const showsMap = new Map();
    for (const ep of episodes) {
      const folder = (ep.relPath || "").split("/")[0] || "";
      if (!folder) continue;
      const key = `${ep.libraryId}|${folder}`;
      if (!showsMap.has(key)) {
        showsMap.set(key, {
          libraryId: ep.libraryId,
          folder,
          showParentId: (ep.metadata && ep.metadata.parentId) || 0,
          fallbackThumbId: ep.id,
          count: 0,
        });
      }
      showsMap.get(key).count++;
    }
    const shows = Array.from(showsMap.values()).sort((a, b) => a.folder.localeCompare(b.folder));

    const totalHits = movies.length + shows.length;
    renderBreadcrumb({ searchCount: totalHits });
    if (!movies.length && !shows.length) {
      grid.innerHTML = `<div class="empty">Keine Videos mit ${escapeHTML(state.personFilter.name)} gefunden.</div>`;
      return;
    }

    grid.innerHTML = "";
    const frag = document.createDocumentFragment();
    const mergedMovies = groupVariants(movies);
    state.lastRenderedItems = mergedMovies;

    if (mergedMovies.length) {
      const h = document.createElement("h2");
      h.className = "person-section-title";
      h.textContent = `🎬 Filme · ${mergedMovies.length}`;
      frag.appendChild(h);
      const mg = document.createElement("div");
      mg.className = "subview-grid";
      for (const m of mergedMovies) mg.appendChild(renderCard(m));
      frag.appendChild(mg);
    }
    if (shows.length) {
      const h = document.createElement("h2");
      h.className = "person-section-title";
      h.textContent = `📺 Serien · ${shows.length}`;
      frag.appendChild(h);
      const sg = document.createElement("div");
      sg.className = "subview-grid";
      for (const s of shows) sg.appendChild(renderPersonShowCard(s));
      frag.appendChild(sg);
    }
    grid.appendChild(frag);
    return;
  }

  // Verdächtige TMDB-Zuordnungen (Dropdown-Option). Server-seitig heuristisch
  // bestimmt: kein Token-Overlap zwischen Top-Folder und Metadata-Titel, kein
  // Jahres-Match → wahrscheinlich falsch gematcht.
  if ($("#sortSelect").value === "suspicious" && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    let items = [];
    try {
      items = await apiGetCached(`/api/items/suspicious?libraryId=${state.currentLibrary}`);
    } catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    if (searchQ) {
      const q = searchQ.toLowerCase();
      items = items.filter(it => (it.title || "").toLowerCase().includes(q)
        || ((it.metadata && it.metadata.title) || "").toLowerCase().includes(q)
        || (it.relPath || "").toLowerCase().includes(q));
    }
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    renderBreadcrumb({ searchCount: items.length });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine verdächtigen Zuordnungen gefunden. ✓</div>`;
      return;
    }
    // Nach Folder sortieren, damit ähnlich benannte nebeneinander landen
    items.sort((a, b) => (a.relPath || "").localeCompare(b.relPath || ""));
    state.lastRenderedItems = items;
    const frag = document.createDocumentFragment();
    items.forEach(it => frag.appendChild(renderCard(it)));
    grid.innerHTML = "";
    grid.appendChild(frag);
    return;
  }

  // Interlaced-Ansicht (via Sort-Dropdown): flache Liste aller Items mit
  // mindestens einem Video-Stream, dessen field_order Halbbilder hat. Gut
  // für Diagnose und gezielten Force-Re-Encode mit Deinterlace.
  if (currentInterlacedMode() === "yes" && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    const p = new URLSearchParams({
      libraryId: state.currentLibrary,
      interlaced: "yes",
      sort: "title",
      dir: "asc",
    });
    if (searchQ) p.set("search", searchQ);
    const watched = $("#watchedFilter").value; if (watched) p.set("watched", watched);
    const fav = currentFavoriteMode(); if (fav) p.set("favorite", fav);
    applyResolutionFilter(p);
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    let items = [];
    try { items = await apiGetCached(`/api/items?${p}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({ searchCount: items.length, interlacedView: true });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine interlaced-Items gefunden. ✓<br><span class="hint">Hinweis: Damit der Filter alte Files erkennt, ist einmal ein <strong>Force-Scan</strong> der Bibliothek nötig.</span></div>`;
      return;
    }
    items.sort((a, b) => (a.relPath || "").localeCompare(b.relPath || ""));
    state.lastRenderedItems = items;
    const frag = document.createDocumentFragment();
    items.forEach(it => frag.appendChild(renderCard(it)));
    grid.innerHTML = "";
    grid.appendChild(frag);
    return;
  }

  // Duplikate-Ansicht (via Sortierungs-Dropdown-Option): alle Items mit mehrfach
  // vergebener metadata_id, flach innerhalb der aktuellen Library, und OHNE
  // Merge — der User will ja gerade jede Einzeldatei sehen, um Kopien vergleichen
  // und ggf. löschen zu können.
  if ($("#sortSelect").value === "duplicates" && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    // Duplikate-Modus: zeige alle Versionen eines duplizierten Items als
    // eigene Kachel — aber nur aus Bibliotheken mit demselben „kind" wie
    // die gerade aktive (Bluray + Filme zusammen wenn beide movies, Serien
    // separat, Private separat). Sonst würde z.B. in Bluray ein Episoden-
    // Duplikat aus „Serien" mit auftauchen.
    const currentLib = state.libraries.find(l => l.id == state.currentLibrary);
    const currentKind = currentLib ? currentLib.kind : null;
    const sameKindLibIds = new Set(
      state.libraries.filter(l => l.kind === currentKind).map(l => l.id)
    );
    const p = new URLSearchParams({
      duplicates: "yes",
      sort: "title",
      dir: "asc",
    });
    if (searchQ) p.set("search", searchQ);
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    let items = [];
    try { items = await apiGetCached(`/api/items?${p}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    // Filter auf Libraries gleichen Kinds (siehe Kommentar oben).
    items = items.filter(it => sameKindLibIds.has(it.libraryId));
    // Server zählt Duplikate global — wenn zwei Versionen library-übergreifend
    // existieren aber durch den Kind-Filter rausfallen, ist eine ggf. allein.
    // Solche „nicht-mehr-doppelten" Items entfernen, damit keine 1er-Kacheln
    // im Duplikate-View landen.
    {
      const cnt = new Map();
      for (const it of items) cnt.set(it.metadataId, (cnt.get(it.metadataId) || 0) + 1);
      items = items.filter(it => (cnt.get(it.metadataId) || 0) >= 2);
    }
    renderBreadcrumb({ searchCount: items.length, duplicatesView: true, duplicatesKind: currentKind });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine Duplikate gefunden.</div>`;
      return;
    }
    // Kein Merge — Gruppierung nach metadata_id für visuelle Bündelung,
    // aber jede Datei bekommt ihre eigene Kachel.
    items.sort((a, b) => {
      if (a.metadataId !== b.metadataId) return a.metadataId - b.metadataId;
      return (a.relPath || "").localeCompare(b.relPath || "");
    });
    state.lastRenderedItems = items;
    const frag = document.createDocumentFragment();
    items.forEach(it => frag.appendChild(renderCard(it)));
    grid.innerHTML = "";
    grid.appendChild(frag);
    return;
  }

  // Favoriten-Modus (via Sort-Dropdown): flache Ansicht innerhalb der aktuellen
  // Library (keine Folders, keine Subfolder-Struktur). Library-Wechsel wirkt
  // weiterhin; die Ansicht bleibt beschränkt auf die aktuell gewählte Library.
  if (currentFavoriteMode() === "yes" && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    const p = new URLSearchParams({
      libraryId: state.currentLibrary,
      favorite: "yes",
      sort: currentSortMode(),
      dir: effectiveSortDir(),
    });
    if (searchQ) p.set("search", searchQ);
    const watched = $("#watchedFilter").value; if (watched) p.set("watched", watched);
    applyResolutionFilter(p);
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    let items = [];
    try { items = await apiGetCached(`/api/items?${p}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({ searchCount: items.length, favoriteView: true });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine Favoriten in dieser Bibliothek.</div>`;
      return;
    }
    const merged = groupVariants(items);
    applyNaturalTitleSort(merged);
    state.lastRenderedItems = merged;
    const frag = document.createDocumentFragment();
    merged.forEach(it => frag.appendChild(renderCard(it)));
    grid.innerHTML = "";
    grid.appendChild(frag);
    return;
  }

  // "Zuletzt abgespielt"-Modus: flache Ansicht innerhalb der aktuellen Library
  // (gleiche Logik wie Favoriten — keine Folders, keine Subfolder-Struktur).
  // Nur Items mit einem last_played_at sind interessant; nie gespielte werden
  // client-seitig ausgefiltert (server liefert sie sonst ans Listenende).
  if ($("#sortSelect").value === "played" && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    const p = new URLSearchParams({
      libraryId: state.currentLibrary,
      sort: "played",
      dir: effectiveSortDir(),
    });
    if (searchQ) p.set("search", searchQ);
    const watched = $("#watchedFilter").value; if (watched) p.set("watched", watched);
    applyResolutionFilter(p);
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    let items = [];
    try { items = await apiGetCached(`/api/items?${p}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    // Server filtert bei sort=played bereits auf last_played_at IS NOT NULL.
    renderBreadcrumb({ searchCount: items.length, playedView: true });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Noch nichts abgespielt in dieser Bibliothek.</div>`;
      return;
    }
    const merged = groupVariants(items);
    state.lastRenderedItems = merged;
    const frag = document.createDocumentFragment();
    merged.forEach(it => frag.appendChild(renderCard(it)));
    grid.innerHTML = "";
    grid.appendChild(frag);
    return;
  }

  if (!state.currentLibrary) {
    grid.innerHTML = `<div class="empty">Noch keine Bibliothek. Klicke auf 📁, um eine hinzuzufügen.</div>`;
    return;
  }

  // In einem TV-Subfolder automatisch nach Episode sortieren (außer der User hat
  // bewusst eine andere Sortierung gewählt).
  const lib = state.libraries.find(l => l.id == state.currentLibrary);
  let sort = currentSortMode();
  if (lib && lib.kind === "tv" && state.currentFolder !== null && sort === "title") {
    sort = "episode";
  }

  // Staffel-Ansicht: Toggle aktiv + TV-Lib + Show-Ordner. Zwei Modi:
  //  (a) state.currentSeason === null → Staffel-Kacheln (Drilldown-Eintritt)
  //  (b) state.currentSeason === N    → flache Folgen-Liste der Staffel
  // Bei aktivem „Ohne TMDB-Zuordnung"-Filter muss die Staffel-Ansicht jedoch
  // weichen — die Seasons-API kennt nur Items mit erkennbarem SxxExx im Pfad,
  // unmatched Bonus-/Extras-/Behind-the-Scenes-Dateien wären sonst unsichtbar.
  const matchMode = currentMatchMode();
  if (state.seasonView && lib && lib.kind === "tv" && state.currentFolder && matchMode !== "unmatched") {
    let data;
    try {
      data = await api(`/api/libraries/${state.currentLibrary}/seasons?folder=${encodeURIComponent(state.currentFolder)}`);
    } catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({});
    if (state.currentSeason == null) {
      renderSeasonFolders(grid, data);
    } else {
      renderSeasonEpisodes(grid, data, state.currentSeason);
    }
    return;
  }
  const searchQ = $("#searchInput").value.trim();
  const params = new URLSearchParams({
    libraryId: state.currentLibrary,
    search: searchQ,
    sort,
    dir: effectiveSortDir(),
  });
  const watched = $("#watchedFilter").value;
  if (watched) params.set("watched", watched);
  const fav = currentFavoriteMode();
  if (fav) params.set("favorite", fav);
  const match = matchMode;
  if (match) params.set("match", match);
  applyResolutionFilter(params);
  // Clear-X nur sichtbar bei nicht-leerem Suchfeld
  $("#searchClear").classList.toggle("hidden", searchQ === "");

  let folders = [];
  let items = [];

  const searching = $("#searchInput").value.trim() !== "";
  // Filter zählen zur "Treffer-Anzeige" genauso wie die Suche
  const filterActive = searching
    || !!$("#watchedFilter").value
    || !!currentFavoriteMode()
    || !!currentMatchMode()
    || state.resBuckets.size > 0;
  // Flat-View: wenn Toggle aktiv ODER Movies-Library (keine Ordner-Struktur)
  // ODER aktiver Filter ODER Sort=Zufällig (sonst würden im Library-Root nur
  // Folder-Kacheln + Direkt-Root-Files gemischt werden — der User erwartet
  // aber, dass „Zufällig" wirklich aus allen Videos der Library/des Folders
  // wählt, rekursiv).
  const isShuffle = $("#sortSelect").value === "shuffle";
  const flatView = state.flatView || (lib && lib.kind === "movies") || isShuffle;
  if (searching || flatView) {
    // Bei aktiver Suche IM Subfolder: Suche auf diesen Folder + Subfolder
    // beschränken (Server-Filter `folder=<path>` matcht rekursiv via LIKE).
    // Im Library-Root oder Flat-View suchen wir die ganze Library.
    // Bei Shuffle in einem Subfolder: ebenfalls auf diesen Folder begrenzen,
    // damit „Zufällig in /Sterne" nicht in die ganze Library spillt.
    if ((searching || isShuffle) && state.currentFolder) {
      params.set("folder", state.currentFolder);
    }
    items = await apiGetCached(`/api/items?${params}`);
  } else if (state.currentFolder === null) {
    // Library-Root: Folders + Root-Items
    params.set("folder", "/");
    // Bei aktivem „Ohne TMDB-Zuordnung"-Filter: nur Folder mit ≥1 unmatched
    // Item zurückgeben (siehe SubfoldersAtFiltered im Store). Sonst werden im
    // TV-Library-Root komplett ungemappte Serien überhaupt nicht sichtbar,
    // weil die Episoden in Subfoldern liegen und der Items-Filter dort nicht greift.
    const folderQS = match ? `?match=${match}` : "";
    [folders, items] = await Promise.all([
      api(`/api/libraries/${state.currentLibrary}/folders${folderQS}`),
      api(`/api/items?${params}`),
    ]);
  } else if (state.currentFolderDrilldown) {
    // Subfolder mit aktiviertem Drilldown: zeige seine direkten Unterordner + Items direkt in diesem Ordner
    const dpath = state.currentFolder;
    const ddMatchQS = match ? `&match=${match}` : "";
    [folders, items] = await Promise.all([
      api(`/api/libraries/${state.currentLibrary}/folders?parent=${encodeURIComponent(dpath)}${ddMatchQS}`),
      (async () => {
        // Items direkt in currentFolder (nicht rekursiv durch Subfolder):
        // Dazu holen wir alle Items unter dem Pfad und filtern client-seitig auf direkte Children.
        const p2 = new URLSearchParams(params);
        p2.set("folder", dpath);
        const all = await apiGetCached(`/api/items?${p2}`);
        const prefix = dpath + "/";
        return all.filter(it => {
          if (!it.relPath.startsWith(prefix)) return false;
          const rest = it.relPath.substring(prefix.length);
          return rest.indexOf("/") === -1;
        });
      })(),
    ]);
  } else {
    // Standard: Subfolder ohne Drilldown → alle Items rekursiv flach
    params.set("folder", state.currentFolder);
    items = await apiGetCached(`/api/items?${params}`);
  }
  if (stale()) return;

  if (folders.length === 0 && items.length === 0) {
    if (filterActive) {
      renderBreadcrumb({ searchCount: 0 });
      const msg = searching
        ? `Keine Treffer für „${escapeHTML($("#searchInput").value.trim())}".`
        : `Keine Treffer mit den gewählten Filtern.`;
      grid.innerHTML = `<div class="empty">${msg}</div>`;
    } else {
      grid.innerHTML = `<div class="empty">Keine Einträge. Klicke „⟳ Scan" um die Bibliothek einzulesen.</div>`;
    }
    return;
  }

  // Bei aktiver Suche oder aktivem Filter: Breadcrumb mit Treffer-Anzahl aktualisieren
  if (filterActive) {
    // Bei Suche zählen wir nur items (keine Folders). Sonst items + folders.
    const count = searching ? items.length : items.length + folders.length;
    renderBreadcrumb({ searchCount: count });
  }

  // Duplikate (mehrere Dateien mit gleicher TMDB-Metadata) zu einer Kachel mergen.
  // Der Varianten-Dropdown im Detail-Dialog lässt die Wahl zu, welche Datei
  // tatsächlich abgespielt/heruntergeladen wird.
  const merged = groupVariants(items);
  // Natürliche Title-Sortierung: Zahlen werden als Werte verglichen
  // (1, 2, …, 10, 11) statt lexikographisch (1, 10, 11, …, 2). Greift nur
  // wenn Sort = title; bei anderen Modi belassen wir die Server-Reihenfolge.
  applyNaturalTitleSort(merged);
  // Zufalls-Sort: Items + Folder client-seitig shuffeln. Frische Reihenfolge
  // bei jedem loadItems-Aufruf — nicht stabil über Reloads, aber das ist die
  // Definition von „zufällig".
  if ($("#sortSelect").value === "shuffle") {
    shuffleInPlace(merged);
    if (Array.isArray(folders)) shuffleInPlace(folders);
  }
  // Folder-Reihenfolge ebenfalls natürlich (Staffel 1, Staffel 2, …, Staffel 10)
  if (Array.isArray(folders) && folders.length > 1) {
    const folderCollator = new Intl.Collator("de", { numeric: true, sensitivity: "base" });
    folders.sort((a, b) => folderCollator.compare(folderDisplayTitle(a), folderDisplayTitle(b)));
  }
  state.lastRenderedItems = merged;
  state.lastRenderedFolders = folders || [];

  // DocumentFragment: alle Kacheln im Speicher aufbauen und in einem einzigen
  // Reflow in den DOM hängen (statt einzeln appendChild → Layout-Trashing).
  const frag = document.createDocumentFragment();
  for (const f of folders) frag.appendChild(renderFolderCard(f));
  for (const it of merged) frag.appendChild(renderCard(it));
  grid.innerHTML = "";
  grid.appendChild(frag);
  updateAlphaSidebar();
}

// --- Startseite ---

// renderHomeView: rendert die Startseite als Stapel pro Library. Jede Lib
// bekommt bis zu drei horizontal scrollbare Streifen (Fortsetzen, Als
// nächstes, Zuletzt hinzugefügt). Leere Streifen werden weggelassen.
function renderHomeView(grid, data) {
  grid.innerHTML = "";
  document.body.classList.remove("has-alpha-sidebar");
  const bar = $("#alphaSidebar"); if (bar) bar.classList.add("hidden");

  const wrap = document.createElement("div");
  wrap.className = "home-view";
  const sections = data.sections || [];

  const renderStrip = (parent, title, items) => {
    if (!items || !items.length) return;
    const secEl = document.createElement("section");
    secEl.className = "home-section";
    const h = document.createElement("h3");
    h.className = "home-section-title";
    h.textContent = title;
    secEl.appendChild(h);
    const strip = document.createElement("div");
    strip.className = "home-strip";
    const merged = groupVariants(items);
    for (const it of merged) {
      const card = renderCard(it);
      card.classList.add("home-card");
      strip.appendChild(card);
    }
    secEl.appendChild(strip);
    parent.appendChild(secEl);
  };

  const flatAll = [];
  for (const sec of sections) {
    const lib = sec.library || {};
    const libBlock = document.createElement("div");
    libBlock.className = "home-lib-block";
    // Library-Überschrift
    const h2 = document.createElement("h2");
    h2.className = "home-lib-title";
    h2.textContent = lib.name || "";
    h2.addEventListener("click", () => {
      state.homeView = false;
      state.currentLibrary = lib.id;
      state.currentFolder = null;
      $("#librarySelect").value = "lib:" + lib.id;
      loadItems();
    });
    libBlock.appendChild(h2);

    renderStrip(libBlock, "▶ Fortsetzen", sec.continue);
    renderStrip(libBlock, "📺 Als nächstes", sec.nextUp);
    renderStrip(libBlock, "🆕 Zuletzt hinzugefügt", sec.recent);

    wrap.appendChild(libBlock);
    flatAll.push(...(sec.continue || []), ...(sec.nextUp || []), ...(sec.recent || []));
  }
  if (!sections.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "Noch keine Inhalte. Lege eine Bibliothek an (📁 im Zahnrad-Menü) und scanne sie. "
      + "Im Menü kannst du festlegen, welche Bibliotheken hier erscheinen.";
    wrap.appendChild(empty);
  }
  grid.appendChild(wrap);
  state.lastRenderedItems = flatAll;
}

// --- Staffel-Ansicht für Serien ---

// renderSeasonFolders: in einem Serien-Ordner mit aktivem Staffel-Toggle
// werden die Staffeln als Kacheln angezeigt (wie Ordner). Klick öffnet
// die Folgen dieser Staffel.
function renderSeasonFolders(grid, data) {
  grid.innerHTML = "";
  document.body.classList.remove("has-alpha-sidebar");
  const bar = $("#alphaSidebar"); if (bar) bar.classList.add("hidden");

  // Show-Info-Header: Poster, Titel, Jahre, Genres, Rating, Beschreibung, Cast
  // counts: aus den geladenen Staffeln berechnen (owned + total falls TMDB sie liefert)
  const seasonsArr = data.seasons || [];
  const counts = {
    ownedSeasons: seasonsArr.length,
    ownedEpisodes: seasonsArr.reduce((s, x) => s + (x.ownedCount || 0), 0),
    totalSeasons: seasonsArr.length, // Fallback wenn TMDB-NumberOfSeasons nicht da
    totalEpisodes: seasonsArr.reduce((s, x) => s + (x.total || 0), 0),
  };
  if (data.show) grid.appendChild(renderShowHeader(data.show, counts));

  const seasons = data.seasons || [];
  if (!seasons.length) {
    const e = document.createElement("div");
    e.className = "empty";
    e.textContent = "Keine Staffel-Daten verfügbar. Möglicherweise ist die Serie noch nicht TMDB-zugeordnet oder der TMDB-Key fehlt.";
    grid.appendChild(e);
    return;
  }
  const frag = document.createDocumentFragment();
  for (const sn of seasons) {
    const el = document.createElement("article");
    el.className = "card folder card--poster";
    el.tabIndex = 0;
    el.setAttribute("role", "button");
    const poster = sn.posterPath
      ? `https://image.tmdb.org/t/p/w342${sn.posterPath}`
      : "/placeholder.svg";
    el.innerHTML = `
      <div class="thumb">
        <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${poster}">
        <span class="folder-count">${sn.ownedCount}/${sn.total} Folgen</span>
      </div>
      <div class="card-body">
        <div class="card-title" title="${escapeHTML(sn.name || "")}">${escapeHTML(sn.name || ("Staffel " + sn.seasonNumber))}</div>
        <div class="card-meta"><span>Staffel ${sn.seasonNumber}</span></div>
      </div>
    `;
    el.addEventListener("click", () => {
      state.currentSeason = sn.seasonNumber;
      loadItems();
    });
    el.addEventListener("keydown", e => { if (e.key === "Enter") el.click(); });
    frag.appendChild(el);
  }
  grid.appendChild(frag);
  state.lastRenderedItems = [];
}

// renderShowHeader: Info-Block über den Staffeln (Poster, Titel, Jahre,
// Genres, Rating, Beschreibung, horizontale Cast-Reihe — gleicher Stil
// wie der Detail-Dialog).
// Mapping für TMDB-Show-Status auf deutsche Labels.
const SHOW_STATUS_DE = {
  "Returning Series": "Laufend",
  "Ended": "Beendet",
  "Canceled": "Abgesetzt",
  "Cancelled": "Abgesetzt",
  "In Production": "In Produktion",
  "Planned": "Geplant",
  "Pilot": "Pilot",
};

function renderShowHeader(show, counts) {
  const el = document.createElement("section");
  el.className = "show-header detail-wrap";
  const poster = show.posterPath
    ? `https://image.tmdb.org/t/p/w342${show.posterPath}`
    : "/placeholder.svg";
  const y1 = (show.firstAirDate || "").slice(0, 4);
  const y2 = (show.lastAirDate || "").slice(0, 4);
  const yearRange = y1 && y2 && y1 !== y2 ? `${y1}–${y2}` : (y1 || y2 || "");
  const sub = [];
  if (yearRange) sub.push(yearRange);
  if (show.status) sub.push(escapeHTML(SHOW_STATUS_DE[show.status] || show.status));
  // Staffel-/Folgen-Anzahl: "3 Staffeln · 34 Folgen" (owned / total wenn beides bekannt)
  if (counts) {
    const totalS = show.numberOfSeasons || counts.totalSeasons || 0;
    const totalE = show.numberOfEpisodes || counts.totalEpisodes || 0;
    if (totalS > 0) {
      const s = counts.ownedSeasons != null && counts.ownedSeasons !== totalS
        ? `${counts.ownedSeasons}/${totalS} Staffeln` : `${totalS} Staffel${totalS === 1 ? "" : "n"}`;
      sub.push(escapeHTML(s));
    }
    if (totalE > 0) {
      const e = counts.ownedEpisodes != null && counts.ownedEpisodes !== totalE
        ? `${counts.ownedEpisodes}/${totalE} Folgen` : `${totalE} Folgen`;
      sub.push(escapeHTML(e));
    }
  }
  if (show.genres && show.genres.length) sub.push(escapeHTML(show.genres.join(" · ")));
  const rating = (show.rating || 0) > 0
    ? `<span class="detail-rating">★ ${show.rating.toFixed(1)}</span>` : "";
  const castHtml = (show.cast && show.cast.length)
    ? `<div class="cast-strip">
        <h3 class="cast-heading">Besetzung</h3>
        <div class="cast-row">
          ${show.cast.map(c => `
            <button type="button" class="cast-card" data-tmdb-id="${c.tmdbId}" data-name="${escapeHTML(c.name)}" title="Filme/Serien mit ${escapeHTML(c.name)}">
              <div class="cast-photo" style="background-image:url('${c.profilePath ? `https://image.tmdb.org/t/p/w185${c.profilePath}` : "/placeholder.svg"}')"></div>
              <div class="cast-name">${escapeHTML(c.name)}</div>
              <div class="cast-role">${escapeHTML(c.character || "")}</div>
            </button>`).join("")}
        </div>
      </div>`
    : "";
  el.innerHTML = `
    <div class="detail-poster" style="background-image:url('${poster}')"></div>
    <div class="detail-body">
      <h2>${escapeHTML(show.title || "")}</h2>
      <div class="sub">
        ${rating}
        ${sub.map(x => `<span>${x}</span>`).join("")}
      </div>
      ${show.overview ? `<p class="overview">${escapeHTML(show.overview)}</p>` : ""}
      <div class="show-actions" style="margin-top:8px;display:flex;gap:8px;flex-wrap:wrap;">
        <button type="button" data-refresh-tmdb title="Lädt Show- und Staffel-Daten frisch von TMDB (Cache umgehen)">↻ TMDB neu laden</button>
        <button type="button" data-reenrich-episodes title="Setzt ALLE Episoden-Zuordnungen dieses Ordners zurück und matcht neu (hilft bei Off-by-One-Fehlern)" style="background:#b45309;">⚠ Episoden neu zuordnen</button>
      </div>
      ${castHtml}
    </div>
  `;
  // "↻ TMDB neu laden": invalidiert serverseitig den Cache für diese Show
  // und fetcht die Staffel-Antwort frisch. Nützlich wenn TMDB Episoden-Titel
  // oder Stills aktualisiert hat, aber unser Cache noch die alten Daten hält.
  const refreshBtn = el.querySelector("[data-refresh-tmdb]");
  if (refreshBtn) {
    refreshBtn.addEventListener("click", async () => {
      refreshBtn.disabled = true;
      refreshBtn.textContent = "Lade frisch von TMDB…";
      const t0 = performance.now();
      try {
        const url = `/api/libraries/${state.currentLibrary}/seasons?folder=${encodeURIComponent(state.currentFolder)}&refresh=true`;
        const data = await api(url);
        const dur = Math.round(performance.now() - t0);
        const grid = $("#grid");
        grid.innerHTML = "";
        if (state.currentSeason == null) renderSeasonFolders(grid, data);
        else renderSeasonEpisodes(grid, data, state.currentSeason);
        // Kurzes Bestätigungs-Feedback am neu gerenderten Button (der alte
        // ist nach dem Re-Render weg). Sonst wirkt der Refresh unsichtbar,
        // wenn sich an den Daten nichts geändert hat.
        const newBtn = document.querySelector("[data-refresh-tmdb]");
        if (newBtn) {
          const original = newBtn.textContent;
          newBtn.textContent = `✓ Frisch geladen (${dur} ms)`;
          newBtn.style.background = "#16a34a";
          newBtn.style.color = "#fff";
          setTimeout(() => {
            newBtn.textContent = original;
            newBtn.style.background = "";
            newBtn.style.color = "";
          }, 2500);
        }
      } catch (err) {
        appAlert("TMDB-Refresh fehlgeschlagen: " + err.message);
        refreshBtn.disabled = false;
        refreshBtn.textContent = "↻ TMDB neu laden";
      }
    });
  }
  // „⚠ Episoden neu zuordnen": nukes alle Episoden-Matches im Ordner
  // (inkl. bestätigter!) und triggert den Enricher. Für Off-by-One-Probleme
  // wie Billions S2.
  const reenrichBtn = el.querySelector("[data-reenrich-episodes]");
  if (reenrichBtn) {
    reenrichBtn.addEventListener("click", async () => {
      const ok = await appConfirm("Alle Episoden-Zuordnungen dieser Serie zurücksetzen und neu matchen?\n\n" +
        "Auch bestätigte Zuordnungen werden verworfen. Die Show-Zuordnung selbst bleibt erhalten.");
      if (!ok) return;
      reenrichBtn.disabled = true;
      reenrichBtn.textContent = "Setze zurück…";
      try {
        const res = await api(`/api/libraries/${state.currentLibrary}/folders/re-enrich-episodes`, {
          method: "POST",
          body: JSON.stringify({ folder: state.currentFolder }),
        });
        reenrichBtn.textContent = `✓ ${res.unmatched || 0} Episoden zurückgesetzt, matche neu…`;
        reenrichBtn.style.background = "#16a34a";
        // 4s warten, dann Ansicht neu laden damit der Enricher Zeit hatte.
        setTimeout(async () => {
          const url = `/api/libraries/${state.currentLibrary}/seasons?folder=${encodeURIComponent(state.currentFolder)}&refresh=true`;
          const data = await api(url);
          const grid = $("#grid");
          grid.innerHTML = "";
          if (state.currentSeason == null) renderSeasonFolders(grid, data);
          else renderSeasonEpisodes(grid, data, state.currentSeason);
        }, 4000);
      } catch (err) {
        appAlert("Re-Match fehlgeschlagen: " + err.message);
        reenrichBtn.disabled = false;
        reenrichBtn.textContent = "⚠ Episoden neu zuordnen";
      }
    });
  }
  // Cast-Karten klickbar: öffnet Person-Filter quer über alle Libraries
  el.querySelectorAll(".cast-card").forEach(btn => {
    btn.addEventListener("click", () => {
      const tmdbId = Number(btn.dataset.tmdbId);
      const name = btn.dataset.name;
      // openPersonView setzt Home/Collections/Playlist konsistent zurück.
      openPersonView(tmdbId, name);
    });
  });
  return el;
}

// renderSeasonEpisodes: nach Klick auf eine Staffel — normales Grid mit
// owned + missing Kacheln.
async function renderSeasonEpisodes(grid, data, seasonNum) {
  grid.innerHTML = "";
  document.body.classList.remove("has-alpha-sidebar");
  const bar = $("#alphaSidebar"); if (bar) bar.classList.add("hidden");

  // Show-Header auch hier — damit der TMDB-Refresh-Button verfügbar ist,
  // wenn man direkt in einer Staffel steckt. Counts beziehen sich auf die
  // aktuelle Staffel (owned/total der Folgen).
  const seasonData = (data.seasons || []).find(s => s.seasonNumber === seasonNum);
  if (data.show) {
    const counts = seasonData ? {
      ownedSeasons: 1,
      ownedEpisodes: seasonData.ownedCount || 0,
      totalSeasons: 1,
      totalEpisodes: seasonData.total || 0,
    } : null;
    grid.appendChild(renderShowHeader(data.show, counts));
  }

  const season = seasonData;
  if (!season) {
    const e = document.createElement("div");
    e.className = "empty";
    e.textContent = `Staffel ${seasonNum} nicht gefunden.`;
    grid.appendChild(e);
    return;
  }
  // Für owned-Kacheln brauchen wir die echten Item-Daten (damit renderCard die
  // normalen Badges + Klick-Verhalten hat). Wir fetchen die parallel — und
  // zwar ALLE Files pro Episode, falls mehrere Dateien dieselbe TMDB-Episode
  // mappen (typisch nach Auto-Rollover: S01E10-Release + S02E01-Release zeigen
  // beide auf dieselbe TMDB-Episode). Repräsentant pro Episode + _variants.
  const allIds = new Set();
  for (const e of (season.episodes || [])) {
    if (!e.owned) continue;
    const ids = (e.itemIds && e.itemIds.length) ? e.itemIds : (e.itemId ? [e.itemId] : []);
    ids.forEach(id => allIds.add(id));
  }
  const ownedItems = {};
  if (allIds.size) {
    const fetched = await Promise.all([...allIds].map(id => api(`/api/items/${id}`).catch(() => null)));
    for (const it of fetched) if (it) ownedItems[it.id] = it;
  }
  const frag = document.createDocumentFragment();
  const flatOwned = [];
  const seenPrimary = new Set();
  for (const ep of season.episodes || []) {
    let card;
    const epIds = (ep.itemIds && ep.itemIds.length) ? ep.itemIds : (ep.itemId ? [ep.itemId] : []);
    const epItems = epIds.map(id => ownedItems[id]).filter(Boolean);
    if (ep.owned && epItems.length) {
      // Repräsentant: höchste Auflösung, dann größte Bitrate (analog
      // groupVariants im Standard-Grid). Alle Geschwister-Files landen
      // unter `_variants` → das bestehende Variant-Dropdown im Detail-
      // Dialog greift automatisch und der „×N"-Badge erscheint.
      let rep = epItems[0];
      for (const cand of epItems) {
        if ((cand.height || 0) > (rep.height || 0) ||
            ((cand.height || 0) === (rep.height || 0) && (cand.bitrateKbps || 0) > (rep.bitrateKbps || 0))) {
          rep = cand;
        }
      }
      if (epItems.length > 1) {
        rep = { ...rep, _variants: epItems };
      }
      // Doppelfolge: der Slot für die erste Episode bekommt die normale
      // Kachel (renderCard zeigt "S07E23-24"). Die weiteren Slots der Range
      // werden als Continuation-Stub gerendert, öffnen aber dieselbe Datei.
      const primaryEpisode = rep.metadata && rep.metadata.episode;
      if (ep.episodeEnd && primaryEpisode && ep.episode !== primaryEpisode) {
        card = renderRangeContinuationCard(rep, ep);
      } else {
        card = renderCard(rep);
        if (!seenPrimary.has(rep.id)) {
          flatOwned.push(rep);
          seenPrimary.add(rep.id);
        }
      }
    } else {
      card = renderMissingEpisodeCard(ep);
    }
    frag.appendChild(card);
  }
  grid.appendChild(frag);
  state.lastRenderedItems = flatOwned;
}

// renderRangeContinuationCard zeigt einen Folgeplatz einer Doppelfolgen-Datei
// (z.B. S07E24, wenn die Datei S07E23E24.mkv heißt). Klick öffnet dasselbe
// Item wie der primäre Slot.
function renderRangeContinuationCard(it, ep) {
  const el = document.createElement("article");
  el.className = "card folder";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  el.dataset.itemId = String(it.id);
  const still = ep.stillPath
    ? `https://image.tmdb.org/t/p/w342${ep.stillPath}`
    : (it.hasThumb ? `/api/thumb/${it.id}` : "/placeholder.svg");
  const sxxexx = `S${String(ep.season).padStart(2,"0")}E${String(ep.episode).padStart(2,"0")}`;
  const rangeStart = it.metadata && it.metadata.episode || ep.episode;
  const rangeLabel = `S${String(ep.season).padStart(2,"0")}E${String(rangeStart).padStart(2,"0")}-${String(ep.episodeEnd).padStart(2,"0")}`;
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${still}">
      <span class="badge" title="Teil einer Doppelfolge">Teil von ${rangeLabel}</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(ep.title || "")}">${escapeHTML(ep.title || sxxexx)}</div>
      <div class="card-meta">
        <span class="episode-code">${sxxexx}</span>
        ${ep.airDate ? `<span>${escapeHTML(ep.airDate.slice(0,10))}</span>` : ""}
      </div>
    </div>
  `;
  el.addEventListener("click", () => openDetail(it));
  return el;
}

function renderOwnedEpisodeStub(ep) {
  const el = document.createElement("article");
  el.className = "card";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  el.dataset.itemId = String(ep.itemId);
  const still = ep.stillPath
    ? `https://image.tmdb.org/t/p/w342${ep.stillPath}`
    : `/api/thumb/${ep.itemId}`;
  const sxxexx = `S${String(ep.season).padStart(2,"0")}E${String(ep.episode).padStart(2,"0")}`;
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${still}">
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(ep.title || "")}">${escapeHTML(ep.title || sxxexx)}</div>
      <div class="card-meta">
        <span class="episode-code">${sxxexx}</span>
        ${ep.airDate ? `<span>${escapeHTML(ep.airDate.slice(0,10))}</span>` : ""}
      </div>
    </div>
  `;
  el.addEventListener("click", async () => {
    try {
      const it = await api(`/api/items/${ep.itemId}`);
      openDetail(it);
    } catch (e) { appAlert(e.message); }
  });
  return el;
}

function renderMissingEpisodeCard(ep) {
  const el = document.createElement("article");
  el.className = "card collection-part is-missing";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  const still = ep.stillPath
    ? `https://image.tmdb.org/t/p/w342${ep.stillPath}`
    : "/placeholder.svg";
  const sxxexx = `S${String(ep.season).padStart(2,"0")}E${String(ep.episode).padStart(2,"0")}`;
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${still}">
      <span class="missing-badge" title="Folge nicht in der Bibliothek">Fehlt</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(ep.title || "")}">${escapeHTML(ep.title || sxxexx)}</div>
      <div class="card-meta">
        <span class="episode-code">${sxxexx}</span>
        ${ep.airDate ? `<span>${escapeHTML(ep.airDate.slice(0,10))}</span>` : ""}
        <span style="color:#ef4444;font-weight:600">Fehlt</span>
      </div>
    </div>
  `;
  // Klick zeigt Episoden-Infos: wir nutzen einen leichten info-Dialog
  el.addEventListener("click", () => {
    appAlert(`${ep.title || sxxexx}\n${sxxexx}${ep.airDate ? " · " + ep.airDate : ""}\n\n${ep.overview || "Keine Beschreibung."}`);
  });
  return el;
}

async function loadCount(el, libId, folder) {
  try {
    const q = folder ? `?folder=${encodeURIComponent(folder)}` : "";
    const data = await api(`/api/libraries/${libId}/stats${q}`);
    const n = data.totalItems;
    const lib = state.libraries.find(l => l.id == libId);
    const isTV = lib && lib.kind === "tv";
    // Im TV-Library-Root: „N Folgen · M Serien". In Subfoldern (oder Movies/
    // Privat) bleibt das alte „N Videos"-Label.
    if (isTV && !folder && typeof data.folderCount === "number") {
      const showWord = data.folderCount === 1 ? "Serie" : "Serien";
      const epWord = n === 1 ? "Folge" : "Folgen";
      el.textContent = `(${n.toLocaleString("de-DE")} ${epWord} · ${data.folderCount.toLocaleString("de-DE")} ${showWord})`;
    } else {
      el.textContent = `(${n.toLocaleString("de-DE")} Video${n === 1 ? "" : "s"})`;
    }
  } catch { el.textContent = ""; }
}

function renderBreadcrumb(opts) {
  opts = opts || {};
  // Bei jedem Breadcrumb-Render auch die pinned Lib-Nav aktualisieren, damit
  // die aktive Auswahl (Hervorhebung) immer mit der echten Navigation matcht.
  renderLibNav();
  const bc = $("#breadcrumb");
  bc.innerHTML = "";

  if (state.currentPlaylist) {
    // Zurück-Button nur wenn wir in Playlist-Grid-Modus sind
    if (state.playlistsView) {
      const back = document.createElement("button");
      back.className = "back-btn";
      back.title = "Zurück zu allen Playlists";
      back.textContent = "←";
      back.addEventListener("click", () => {
        state.currentPlaylist = null;
        loadItems();
      });
      bc.appendChild(back);
    }
    const p = state.playlists.find(x => x.id == state.currentPlaylist);
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = "📋 " + (p ? p.name : "Playlist");
    bc.appendChild(cur);
    const count = document.createElement("span"); count.className = "count";
    if (p) count.textContent = `(${p.itemCount} Video${p.itemCount === 1 ? "" : "s"})`;
    bc.appendChild(count);
    // Close-Button rechts: restoriert den Nav-Kontext vor dem Playlist-Öffnen.
    // Nur anzeigen, wenn KEIN `←`-Pfeil schon da ist (also der User ist nicht
    // über die Playlist-Root reingekommen, sondern z.B. über das Dropdown
    // oder einen Direkt-Link). Sonst wäre ✕ + ← doppelt.
    if (state.playlistReturnNav && !state.playlistsView) {
      const close = document.createElement("button");
      close.className = "back-btn";
      close.style.marginLeft = "auto";
      close.title = "Playlist schließen — zurück zur vorherigen Ansicht";
      close.textContent = "✕";
      close.addEventListener("click", exitPlaylist);
      bc.appendChild(close);
    }
    return;
  }

  if (opts.playlistsRoot) {
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = "📋 Playlists";
    bc.appendChild(cur);
    const count = document.createElement("span"); count.className = "count";
    if (opts.searchCount !== undefined) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Playlists)`;
    }
    bc.appendChild(count);
    // Close-Button: zurück zur Nav-Position, von der aus der User die
    // Playlist-Übersicht geöffnet hat (z.B. Library-Grid oder Home).
    if (state.playlistReturnNav) {
      const close = document.createElement("button");
      close.className = "back-btn";
      close.style.marginLeft = "auto";
      close.title = "Playlists schließen — zurück zur vorherigen Ansicht";
      close.textContent = "✕";
      close.addEventListener("click", exitPlaylist);
      bc.appendChild(close);
    }
    return;
  }

  if (opts.homeRoot) {
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = "🏠 Startseite";
    bc.appendChild(cur);
    return;
  }

  if (state.personFilter) {
    const back = document.createElement("button");
    back.className = "back-btn";
    back.title = "Person-Filter aufheben";
    back.textContent = "←";
    back.addEventListener("click", clearPersonView);
    bc.appendChild(back);
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = "🎭 " + state.personFilter.name;
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Treffer)`;
    }
    bc.appendChild(count);
    return;
  }

  if (opts.collectionsRoot || opts.collectionName) {
    if (opts.collectionName) {
      const back = document.createElement("button");
      back.className = "back-btn";
      back.title = "Zurück zu allen Sammlungen";
      back.textContent = "←";
      back.addEventListener("click", () => {
        state.currentCollection = null;
        loadItems();
      });
      bc.appendChild(back);
    }
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = opts.collectionName ? ("📚 " + opts.collectionName) : "📚 Sammlungen";
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      const label = opts.collectionName ? "Filme" : "Sammlungen";
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} ${label})`;
    }
    bc.appendChild(count);
    return;
  }

  if (opts.tpFailedView) {
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = "🎞 Trickplay-Fehler";
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Videos)`;
    }
    bc.appendChild(count);
    const close = document.createElement("button");
    close.className = "back-btn";
    close.style.marginLeft = "auto";
    close.title = "Schließen — zurück zu Trickplay verwalten";
    close.textContent = "✕";
    close.addEventListener("click", exitTrickplayFailedView);
    bc.appendChild(close);
    return;
  }

  if (opts.favoriteView) {
    const lib = state.libraries.find(l => l.id == state.currentLibrary);
    const libName = lib ? lib.name : "";
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = libName ? `♥ Favoriten in ${libIcon(lib)} ${libName}` : "♥ Favoriten";
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Videos)`;
    }
    bc.appendChild(count);
    return;
  }

  if (opts.playedView) {
    const lib = state.libraries.find(l => l.id == state.currentLibrary);
    const libName = lib ? lib.name : "";
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = libName ? `🕘 Zuletzt abgespielt in ${libIcon(lib)} ${libName}` : "🕘 Zuletzt abgespielt";
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Videos)`;
    }
    bc.appendChild(count);
    return;
  }

  if (opts.duplicatesView) {
    // Duplikate-Modus ist library-übergreifend, aber begrenzt auf Bibliotheken
    // mit demselben Kind wie die aktive (siehe loadItems-Branch).
    const kindLabel = opts.duplicatesKind === "movies" ? "Filme"
      : opts.duplicatesKind === "tv" ? "Serien"
      : opts.duplicatesKind === "private" ? "Privatvideos"
      : "Bibliotheken";
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = `⧉ Duplikate (alle ${kindLabel})`;
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Dateien)`;
    }
    bc.appendChild(count);
    return;
  }

  if (opts.interlacedView) {
    const lib = state.libraries.find(l => l.id == state.currentLibrary);
    const libName = lib ? lib.name : "";
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = libName ? `🪤 Interlaced in ${libIcon(lib)} ${libName}` : "🪤 Interlaced";
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Dateien)`;
    }
    bc.appendChild(count);
    return;
  }

  if (!state.currentLibrary) return;
  const lib = state.libraries.find(l => l.id == state.currentLibrary);
  const libName = lib ? lib.name : "Bibliothek";

  if (state.currentFolder === null) {
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = `${libIcon(lib)} ${libName}`;
    bc.appendChild(cur);
    const count = document.createElement("span");
    count.className = "count";
    count.id = "bc-count";
    bc.appendChild(count);
    if (opts.searchCount !== undefined && opts.searchCount !== null) {
      count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Treffer)`;
    } else {
      loadCount(count, lib.id, "");
    }

    const toolbar = document.createElement("div");
    toolbar.className = "toolbar";
    bc.appendChild(toolbar);
    if (lib) renderTrickplayToolbar(toolbar, lib.id, "");
    return;
  }

  // Zurück-Button: eine Ebene hoch. Im Staffel-Drilldown (currentSeason
  // gesetzt) geht's zuerst zurück zur Staffel-Übersicht, dann ins Parent-Folder.
  const back = document.createElement("button");
  back.className = "back-btn";
  back.title = "Eine Ebene zurück";
  back.textContent = "←";
  back.addEventListener("click", () => {
    if (state.currentSeason != null) {
      state.currentSeason = null;
      loadItems();
      return;
    }
    const segs = (state.currentFolder || "").split("/");
    if (segs.length > 1) {
      state.currentFolder = segs.slice(0, -1).join("/");
      state.currentFolderDrilldown = true;
    } else {
      state.currentFolder = null;
      state.currentFolderDrilldown = false;
    }
    loadItems();
  });
  bc.appendChild(back);

  const home = document.createElement("a");
  home.textContent = `${libIcon(lib)} ${libName}`;
  home.addEventListener("click", () => {
    state.currentFolder = null;
    state.currentFolderDrilldown = false;
    loadItems();
  });
  bc.appendChild(home);

  // Pfadsegmente klickbar rendern
  const segs = state.currentFolder.split("/");
  const seasonActive = state.currentSeason != null;
  for (let i = 0; i < segs.length; i++) {
    const sep = document.createElement("span"); sep.className = "sep"; sep.textContent = "›"; bc.appendChild(sep);
    const isLast = i === segs.length - 1;
    if (!isLast || seasonActive) {
      // Alle Segmente außer dem letzten sind immer klickbar; beim letzten
      // nur, wenn wir gerade tiefer (in einer Staffel) sind — dann soll
      // der Klick zurück zur Serien-Übersicht führen.
      const link = document.createElement("a");
      link.textContent = segs[i];
      const subPath = segs.slice(0, i + 1).join("/");
      link.addEventListener("click", () => {
        if (isLast) {
          state.currentSeason = null;
          loadItems();
          return;
        }
        state.currentFolder = subPath;
        state.currentFolderDrilldown = true;
        state.currentSeason = null;
        loadItems();
      });
      bc.appendChild(link);
    } else {
      const cur = document.createElement("span");
      cur.className = "current";
      cur.textContent = segs[i];
      bc.appendChild(cur);
    }
  }
  // Staffel als zusätzliche Breadcrumb-Stufe anzeigen
  if (seasonActive) {
    const sep = document.createElement("span"); sep.className = "sep"; sep.textContent = "›"; bc.appendChild(sep);
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = `Staffel ${state.currentSeason}`;
    bc.appendChild(cur);
  }
  const count = document.createElement("span"); count.className = "count"; count.id = "bc-count";
  bc.appendChild(count);
  if (opts.searchCount !== undefined && opts.searchCount !== null) {
    count.textContent = `(${opts.searchCount.toLocaleString("de-DE")} Treffer)`;
  } else {
    loadCount(count, lib.id, state.currentFolder);
  }

  const toolbar = document.createElement("div");
  toolbar.className = "toolbar";
  bc.appendChild(toolbar);

  if (lib && lib.kind === "tv") {
    const btn = document.createElement("button");
    btn.textContent = "Serie zuordnen…";
    btn.addEventListener("click", () => openMatchFolder(lib.id, state.currentFolder));
    toolbar.appendChild(btn);
  }

  if (lib) renderTrickplayToolbar(toolbar, lib.id, state.currentFolder);
}

async function renderTrickplayToolbar(toolbar, libId, folder) {
  let data;
  try {
    data = await api(`/api/libraries/${libId}/trickplay/status?folder=${encodeURIComponent(folder)}`);
  } catch (e) { return; }
  const label = folder === "" ? "Trickplay (ganze Bibliothek)" : "Trickplay";
  const wrap = document.createElement("label");
  wrap.title = folder === ""
    ? "Vorschau-Thumbnails für ALLE Videos dieser Bibliothek generieren"
    : "Vorschau-Thumbnails für diesen Ordner generieren";
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.checked = !!data.enabled;
  cb.addEventListener("change", async () => {
    try {
      await api(`/api/libraries/${libId}/trickplay`, {
        method: "PUT",
        body: JSON.stringify({ folder, enabled: cb.checked }),
      });
      renderBreadcrumb();
      if (cb.checked) {
        startTrickplayPoll(libId, folder);
        setTimeout(checkTrickplayWorker, 500);
      }
    } catch (e) { appAlert(e.message); cb.checked = !cb.checked; }
  });
  wrap.appendChild(cb);
  wrap.appendChild(document.createTextNode(" " + label));
  toolbar.appendChild(wrap);

  if (data.enabled) {
    const status = document.createElement("span");
    status.className = "status";
    status.id = `trickplay-status-${libId}-${encodeURIComponent(folder || "__lib__")}`;
    renderTrickplayStatusInto(status, data);
    toolbar.appendChild(status);
    startTrickplayPoll(libId, folder);
  }
}

function renderTrickplayStatusInto(el, data) {
  const pct = data.total > 0 ? Math.round(data.done * 100 / data.total) : 0;
  el.innerHTML = `
    ${data.done}/${data.total}${data.failed ? ` · ⚠ ${data.failed}` : ""}
    <span class="progress"><div style="width:${pct}%"></div></span>
  `;
}

async function startTrickplayPoll(libId, folder) {
  if (state.trickplayPoll) clearInterval(state.trickplayPoll);
  // library-level: currentFolder === null ↔ folder === ""
  state.trickplayPoll = setInterval(async () => {
    const sameFolder = (folder === "" && state.currentFolder === null)
                    || state.currentFolder === folder;
    if (!sameFolder || state.currentLibrary != libId) {
      clearInterval(state.trickplayPoll);
      state.trickplayPoll = null;
      return;
    }
    try {
      const data = await api(`/api/libraries/${libId}/trickplay/status?folder=${encodeURIComponent(folder)}`);
      const key = `trickplay-status-${libId}-${encodeURIComponent(folder || "__lib__")}`;
      const el = document.getElementById(key);
      if (el) renderTrickplayStatusInto(el, data);
      if (data.done >= data.total && data.pending === 0) {
        clearInterval(state.trickplayPoll);
        state.trickplayPoll = null;
      }
    } catch (e) { console.warn(e); }
  }, 3000);
}

function renderPlaylistCard(p) {
  const el = document.createElement("article");
  // card--poster = gleiches 2:3-Format wie Film-/Serien-Kacheln
  el.className = "card folder card--poster playlist-card";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  // Poster-Priorität: TMDB-Metadata → Item-Thumbnail → 📋-Fallback
  let thumbInner;
  if (p.posterMetadataId) {
    thumbInner = `<img class="thumb-img" loading="lazy" decoding="async" alt="" src="/api/poster/metadata/${p.posterMetadataId}">`;
  } else if (p.posterItemId) {
    thumbInner = `<img class="thumb-img" loading="lazy" decoding="async" alt="" src="/api/thumb/${p.posterItemId}">`;
  } else {
    thumbInner = `<div class="playlist-thumb-icon">📋</div>`;
  }
  el.innerHTML = `
    <div class="thumb">
      ${thumbInner}
      <span class="folder-count">${p.itemCount} Video${p.itemCount === 1 ? "" : "s"}</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(p.name)}">📋 ${escapeHTML(p.name)}</div>
      <div class="card-meta"><span>Playlist</span></div>
    </div>
  `;
  const open = () => {
    enterPlaylist(p.id);
    loadItems();
  };
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  return el;
}

// Collection-Part-Kachel: für owned Parts wird renderCard() mit dem inline
// gelieferten Item-Record aufgerufen → identische Optik wie in der Library
// (Watched-Toggle, Format/Res/Duration, Poster). Fehlende Filme bekommen eine
// gedimmte Kachel im gleichen 2:3-Format plus "Fehlt"-Badge.
function renderCollectionPartCard(p, collectionId) {
  const hideBtn = collectionId ? hidePartButton(collectionId, p) : null;
  if (p.owned && p.item) {
    const card = renderCard(p.item);
    card.classList.add("collection-part");
    if (p.hidden) card.classList.add("is-hidden-part");
    if (hideBtn) card.appendChild(hideBtn);
    return card;
  }
  const el = document.createElement("article");
  el.className = "card card--poster collection-part is-missing";
  if (p.hidden) el.classList.add("is-hidden-part");
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  const imgUrl = p.posterPath
    ? `https://image.tmdb.org/t/p/w342${p.posterPath}`
    : "/placeholder.svg";
  const year = (p.releaseDate || "").slice(0, 4);
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${imgUrl}">
      <span class="missing-badge" title="Film nicht in der Bibliothek">Fehlt</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(p.title)}">${escapeHTML(p.title)}</div>
      <div class="card-meta">
        ${year ? `<span>${year}</span>` : ""}
        <span style="color:#94a3b8">Fehlt</span>
      </div>
    </div>
  `;
  // Klick öffnet den TMDB-Detail-Dialog — Beschreibung, Rating, Cast.
  const open = (ev) => {
    if (ev && ev.target && ev.target.closest(".part-hide-btn")) return;
    openMissingMovieDialog(p.tmdbMovieId, p.title);
  };
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(e); });
  if (hideBtn) el.appendChild(hideBtn);
  return el;
}

// Öffnet einen Detail-Dialog für Filme, die NICHT in der Bibliothek sind.
// Holt Daten direkt aus TMDB via Server-Proxy und rendert Poster, Plot,
// Rating, Cast. Kein Play/Download, nur Info.
async function openMissingMovieDialog(tmdbId, fallbackTitle) {
  const dlg = $("#missingMovieDialog") || createMissingMovieDialog();
  const body = dlg.querySelector(".missing-body");
  body.innerHTML = `<div class="loading">Lade TMDB-Daten für „${escapeHTML(fallbackTitle || "Film")}" …</div>`;
  if (!dlg.open) dlg.showModal();
  try {
    const m = await api(`/api/tmdb/movie/${tmdbId}`);
    const poster = m.posterPath
      ? `https://image.tmdb.org/t/p/w500${m.posterPath}`
      : "/placeholder.svg";
    const yearStr = m.year ? String(m.year) : "";
    const rating = (m.rating || 0) > 0
      ? `<span class="detail-rating">★ ${m.rating.toFixed(1)}</span>`
      : "";
    const runtime = m.runtimeMin ? `${m.runtimeMin} min` : "";
    // Identisches Cast-Markup wie im normalen Detail-Dialog: Wrapper
     // `.cast-strip`, dann `.cast-heading` + `.cast-row` mit <button>-Cards.
    // So scrollt er horizontal und Klick öffnet den Person-Filter.
    const castHtml = (m.cast && m.cast.length)
      ? `<div class="cast-strip">
          <h3 class="cast-heading">Besetzung</h3>
          <div class="cast-row">
            ${m.cast.map(c => `
              <button type="button" class="cast-card" data-tmdb-id="${c.tmdbId}" data-name="${escapeHTML(c.name)}" title="Filme/Serien mit ${escapeHTML(c.name)}">
                <div class="cast-photo" style="background-image:url('${c.profilePath ? `https://image.tmdb.org/t/p/w185${c.profilePath}` : "/placeholder.svg"}')"></div>
                <div class="cast-name">${escapeHTML(c.name)}</div>
                <div class="cast-role">${escapeHTML(c.character || "")}</div>
              </button>
            `).join("")}
          </div>
        </div>`
      : "";
    body.innerHTML = `
      <div class="detail-wrap">
        <div class="detail-poster" style="background-image:url('${poster}')"></div>
        <div class="detail-body">
          <h2>${escapeHTML(m.title || fallbackTitle || "")} <span class="missing-tag">Fehlt in der Bibliothek</span></h2>
          <div class="sub">
            ${rating}
            ${yearStr ? `<span>${yearStr}</span>` : ""}
            ${runtime ? `<span>${runtime}</span>` : ""}
            ${m.imdbId ? `<a href="https://www.imdb.com/title/${m.imdbId}" target="_blank" rel="noopener">IMDb ↗</a>` : ""}
            <a href="https://www.themoviedb.org/movie/${m.tmdbId}" target="_blank" rel="noopener">TMDB ↗</a>
          </div>
          <p class="overview">${escapeHTML(m.overview || "Keine Beschreibung verfügbar.")}</p>
          ${castHtml}
        </div>
      </div>
    `;
    // Cast-Karten klickbar machen — öffnet Person-Filter über alle Libs,
    // damit der User prüft, ob er Filme mit diesen Schauspielern besitzt.
    body.querySelectorAll(".cast-card").forEach(btn => {
      btn.addEventListener("click", () => {
        const tmdbId = Number(btn.dataset.tmdbId);
        const name = btn.dataset.name;
        dlg.close();
        // openPersonView resettet Home/Collections/Playlist-Flags konsistent.
        openPersonView(tmdbId, name);
      });
    });
  } catch (e) {
    body.innerHTML = `<div class="empty">TMDB-Daten konnten nicht geladen werden: ${escapeHTML(e.message)}</div>`;
  }
}

function createMissingMovieDialog() {
  const dlg = document.createElement("dialog");
  dlg.id = "missingMovieDialog";
  // Gleiche Klassen wie der echte Detail-Dialog, damit Breite/Padding/Poster-
  // Layout identisch aussehen.
  dlg.className = "modal detail-modal";
  dlg.innerHTML = `
    <div class="missing-body"></div>
    <div class="row icon-row">
      <button type="button" class="icon-btn" data-close title="Schließen" aria-label="Schließen">✕</button>
    </div>
  `;
  dlg.addEventListener("click", (e) => {
    if (e.target === dlg || e.target.hasAttribute("data-close")) dlg.close();
  });
  document.body.appendChild(dlg);
  return dlg;
}

// Erzeugt einen kleinen Button in der Kachel-Ecke, um diesen Part aus der
// Sammlung auszublenden bzw. wieder einzublenden.
function hidePartButton(collectionId, p) {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "part-hide-btn";
  btn.textContent = p.hidden ? "↺" : "✕";
  btn.title = p.hidden ? "Wieder einblenden" : "Aus dieser Sammlung ausblenden";
  btn.addEventListener("click", async (ev) => {
    ev.stopPropagation();
    ev.preventDefault();
    try {
      if (p.hidden) {
        await api(`/api/collections/${collectionId}/parts/${p.tmdbMovieId}/hide`, { method: "DELETE" });
      } else {
        await api(`/api/collections/${collectionId}/parts/${p.tmdbMovieId}/hide`, { method: "POST" });
      }
      invalidateItemsCache();
      loadItems();
    } catch (e) { appAlert("Aktualisierung fehlgeschlagen: " + e.message); }
  });
  return btn;
}

// renderPersonShowCard: Sammelkachel pro Serie im Person-Filter-Modus.
// Zeigt das Show-Poster (von der Parent-Show-Metadata, sonst Fallback auf
// Episoden-Thumbnail) + Show-Name aus dem Folder-Namen + Anzahl gefundener
// Episoden. Klick navigiert zum Serien-Ordner wie das Show-Link-Klick einer
// Episoden-Kachel.
function renderPersonShowCard(show) {
  const el = document.createElement("article");
  el.className = "card folder card--poster";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  const poster = show.showParentId
    ? `/api/poster/metadata/${show.showParentId}`
    : (show.fallbackThumbId ? `/api/thumb/${show.fallbackThumbId}` : "/placeholder.svg");
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${poster}">
      <span class="folder-count">${show.count} Folge${show.count === 1 ? "" : "n"}</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(show.folder)}">${escapeHTML(show.folder)}</div>
      <div class="card-meta"><span>📺 Serie</span></div>
    </div>
  `;
  const open = () => {
    state.homeView = false;
    state.collectionsView = false;
    state.currentCollection = null;
    state.playlistsView = false;
    state.personFilter = null;
    state.personFilterBackup = null;
    state.currentLibrary = show.libraryId;
    state.currentFolder = show.folder;
    state.currentFolderDrilldown = false;
    const sel = $("#librarySelect");
    if (sel) sel.value = "lib:" + show.libraryId;
    loadItems();
  };
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  return el;
}

function renderCollectionCard(c) {
  const el = document.createElement("article");
  el.className = "card folder card--poster";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  // Datensatz für Alphabet-Sidebar (Sprung-Ziel) und Suche
  el.dataset.itemId = `col-${c.id}`;
  // Poster-Priorität: Sammlung selbst → erster Film der Sammlung → Placeholder
  let imgUrl = "/placeholder.svg";
  if (c.posterPath) imgUrl = `/api/poster/collection/${c.id}`;
  else if (c.fallbackMetaId) imgUrl = `/api/poster/metadata/${c.fallbackMetaId}`;
  // „Komplett"-Marker: Sammlung gilt als vollständig, wenn wir alle (nicht
  // ausgeblendeten) ERSCHIENENEN TMDB-Parts besitzen. Filme, deren Release
  // noch in der Zukunft liegt, zählen NICHT als fehlend — sobald sie
  // erscheinen, wird die Sammlung automatisch wieder „unvollständig".
  // part_count=0 heißt, die Parts wurden noch nicht vom Server gefetcht
  // → dann keine Bewertung möglich.
  const unreleased = c.unreleasedCount || 0;
  const effectiveTotal = Math.max(0, (c.partCount || 0) - (c.hiddenCount || 0));
  const releasedTotal = Math.max(0, effectiveTotal - unreleased);
  const complete = (c.partCount || 0) > 0 && c.movieCount >= releasedTotal;
  const completeBadge = complete
    ? (unreleased > 0
        ? `<span class="collection-complete" title="Alle erschienenen Filme vorhanden — ${unreleased} noch nicht erschienen">✓ komplett</span>`
        : `<span class="collection-complete" title="Sammlung komplett">✓ komplett</span>`)
    : "";
  const countLabel = c.partCount
    ? `${c.movieCount}/${effectiveTotal} Film${effectiveTotal === 1 ? "" : "e"}`
    : `${c.movieCount} Film${c.movieCount === 1 ? "" : "e"}`;
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${imgUrl}">
      <span class="folder-count">${countLabel}</span>
      ${completeBadge}
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(c.name)}">${escapeHTML(c.name)}</div>
      <div class="card-meta"><span>📚 Sammlung</span></div>
    </div>
  `;
  const open = () => {
    state.currentCollection = { id: c.id, name: c.name };
    loadItems();
  };
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  return el;
}

// folderDisplayTitle gibt den Anzeigetitel einer Folder-Kachel zurück
// (TMDB-Metadata-Titel falls verknüpft, sonst letztes Pfadsegment).
// Wird auch von updateAlphaSidebar/jumpToLetter genutzt.
function folderDisplayTitle(f) {
  if (f && f.metadata && f.metadata.title) return f.metadata.title;
  const segs = ((f && f.name) || "").split("/");
  return segs[segs.length - 1] || (f && f.name) || "";
}

function renderFolderCard(f) {
  const el = document.createElement("article");
  // Poster-Format bei Movies/TV-Libs (Show-Poster), 16:9 bei Privat
  const activeLib = state.libraries.find(l => l.id == state.currentLibrary);
  const isPosterLib = activeLib && (activeLib.kind === "movies" || activeLib.kind === "tv");
  el.className = "card folder" + (isPosterLib ? " card--poster" : "");
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  // Damit die Alphabet-Sidebar zu Folder-Kacheln springen kann:
  if (f && f.name) el.dataset.folderName = f.name;

  // Nur letztes Pfadsegment anzeigen (z.B. "KanalX" statt "a/Siterips/KanalX")
  const segs = f.name.split("/");
  const shortName = segs[segs.length - 1];

  let imgUrl = "", title = shortName, meta = "Ordner";
  let rating = "";
  if (f.metadata && f.metadata.posterPath) {
    imgUrl = `/api/poster/metadata/${f.metadataId}`;
    title = f.metadata.title || shortName;
    if (f.metadata.year) meta = `${f.metadata.year} · ${f.itemCount} Episoden`;
    else meta = `${f.itemCount} Episoden`;
    if (f.metadata.rating) {
      rating = `<span class="rating">★ ${f.metadata.rating.toFixed(1)}</span>`;
    }
  } else if (f.thumbItemId) {
    imgUrl = `/api/thumb/${f.thumbItemId}`;
  }
  const imgTag = imgUrl
    ? `<img class="thumb-img" loading="lazy" decoding="async" alt="" src="${imgUrl}">`
    : "";

  const drilldownIcon = f.drilldown
    ? `<span class="drilldown-indicator" title="Unterordner werden angezeigt">▸</span>`
    : "";
  const adminGear = state.me && state.me.isAdmin
    ? `<button class="folder-gear" title="Unterordner als Ebene anzeigen">⚙</button>`
    : "";

  el.innerHTML = `
    <div class="thumb">
      ${imgTag}
      ${rating}
      ${drilldownIcon}
      ${adminGear}
      <span class="folder-count">${f.itemCount} Video${f.itemCount === 1 ? "" : "s"}</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(title)}">${escapeHTML(title)}</div>
      <div class="card-meta"><span>${escapeHTML(meta)}</span></div>
    </div>
  `;

  const enter = () => {
    state.currentFolder = f.name;
    state.currentFolderDrilldown = !!f.drilldown;
    loadItems();
  };
  el.addEventListener("click", (e) => {
    if (e.target.classList.contains("folder-gear")) return; // gear handled separately
    enter();
  });
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") enter(); });

  // Admin-Toggle für Drilldown via Gear-Icon
  const gearBtn = el.querySelector(".folder-gear");
  if (gearBtn) {
    gearBtn.addEventListener("click", async (e) => {
      e.stopPropagation();
      const newState = !f.drilldown;
      try {
        await api(`/api/libraries/${state.currentLibrary}/folders/drilldown`, {
          method: "PUT",
          body: JSON.stringify({ folder: f.name, drilldown: newState }),
        });
        loadItems();
      } catch (err) { appAlert(err.message); }
    });
  }
  return el;
}

function renderCard(it, opts = {}) {
  const el = document.createElement("article");
  if (opts.queueIdx !== undefined) el.dataset.queueIdx = opts.queueIdx;
  // Poster-Format (2:3) für Movies/TV-Libraries, Video-Thumbnail-Format (16:9) für Privat
  const itLib = state.libraries.find(l => l.id == it.libraryId);
  const isPosterLib = itLib && (itLib.kind === "movies" || itLib.kind === "tv");
  el.className = "card" + (it.watched ? " watched" : "") + (isPosterLib ? " card--poster" : "");
  el.tabIndex = 0;
  el.setAttribute("role", "button");

  // Poster bevorzugen, sonst Video-Thumbnail, sonst Placeholder
  let imgUrl;
  let title = it.title;
  let subtitle = "";
  let episodeCode = ""; // separat, damit wir fett+groß stylen können
  let episodeName = ""; // TMDB-Episodentitel (kommt als 2. Zeile)

  // Episode-Handling IMMER wenn TMDB-Type=episode, unabhängig davon ob ein
  // Still-Bild (posterPath) vorhanden ist. Vorher haben Episoden ohne
  // posterPath (z. B. TMDB-Platzhalter wie „Folge 16") die Release-Group als
  // Titel gezeigt — das war der NCIS-Sydney-Bug.
  const isEpisode = it.metadata && it.metadata.tmdbType === "episode";
  if (isEpisode) {
    const rel = (it.relPath || "").split("/");
    const showName = rel.length > 1 ? rel[0] : "";
    if (showName) title = showName;
    const sn = String(it.metadata.season || 0).padStart(2, "0");
    const ep = String(it.metadata.episode || 0).padStart(2, "0");
    episodeCode = `S${sn}E${ep}`;
    // Doppelfolgen (S07E23E24.mkv): der Parser speichert episode_end auf
    // dem Item. Im Code als "S07E23-24" anzeigen.
    if (it.episodeEnd && it.episodeEnd > (it.metadata.episode || 0)) {
      const ee = String(it.episodeEnd).padStart(2, "0");
      episodeCode = `S${sn}E${ep}-${ee}`;
    }
    episodeName = it.metadata.title || "";
  } else if (itLib && itLib.kind === "tv") {
    // Unmatched TV-Item (z. B. Hallmark-Special, das TMDB unter Season 0
    // hat): Show-Name aus rel_path[0], Episodencode aus dem Filename
    // parsen — sonst zeigt die Kachel den Release-Junk-Filename als Titel.
    const rel = (it.relPath || "").split("/");
    const showName = rel.length > 1 ? rel[0] : "";
    const m = (it.title || "").match(/[Ss](\d{1,2})[Ee](\d{1,3})/);
    if (showName && m) {
      title = showName;
      episodeCode = `S${m[1].padStart(2, "0")}E${m[2].padStart(2, "0")}`;
      episodeName = "(ohne TMDB-Zuordnung)";
    }
  }

  if (it.metadata && it.metadata.posterPath) {
    imgUrl = `/api/poster/metadata/${it.metadataId}`;
    if (!isEpisode) {
      title = it.metadata.title || it.title;
      if (it.metadata.year) subtitle = String(it.metadata.year);
    }
  } else {
    imgUrl = it.hasThumb ? `/api/thumb/${it.id}` : "/placeholder.svg";
  }
  // Private Libraries (YouTube-Channels, Urlaubsordner, etc.): Kachel zeigt
  // den Dateinamen (it.title = Basename ohne Extension). Der Ordnername
  // taucht auf der Kachel nicht auf — er ist ohnehin im Breadcrumb und im
  // Detail-Dialog (rel_path) sichtbar.
  const released = fmtDate(it.releasedAt);
  const rating = it.metadata && it.metadata.rating
    ? `<span class="rating">★ ${it.metadata.rating.toFixed(1)}</span>` : "";
  // Click-baren Haken einblenden: dimmt sich bei ungesehen, leuchtet grün bei gesehen.
  const watched = `<button type="button" class="watched-toggle ${it.watched ? "is-on" : ""}" title="${it.watched ? "Als ungesehen markieren" : "Als gesehen markieren"}" data-toggle-watched aria-label="${it.watched ? "Gesehen" : "Ungesehen"}">✓</button>`;
  // Confirm-Button nur in den Filter-Modi „duplicates" und „suspicious" — dort
  // will der User häufig die Zuordnung bestätigen, ohne jedes Item einzeln
  // zu öffnen. In anderen Views wäre der Button Clutter.
  const sortVal = $("#sortSelect") ? $("#sortSelect").value : "";
  const showConfirm = (sortVal === "duplicates" || sortVal === "suspicious")
                      && it.metadataId > 0 && !it.metadataConfirmed;
  const confirmBtn = showConfirm
    ? `<button type="button" class="confirm-toggle" title="Zuordnung bestätigen" data-toggle-confirm aria-label="Zuordnung bestätigen">✅</button>`
    : "";
  // Klickbares Herz wie der Watched-Haken: gedimmt wenn kein Favorit, rot wenn
  // Favorit. Click togglet und schließt Card-Click (Detail öffnen) aus.
  const fav = `<button type="button" class="fav-toggle ${it.favorite ? "is-on" : ""}" title="${it.favorite ? "Aus Favoriten entfernen" : "Zu Favoriten hinzufügen"}" data-toggle-fav aria-label="${it.favorite ? "Favorit" : "Kein Favorit"}">${it.favorite ? "♥" : "♡"}</button>`;
  let tp = "";
  if (it.trickplayStatus === "done") {
    tp = `<span class="tp-badge" title="Trickplay vorhanden">🎞</span>`;
  } else if (it.trickplayStatus === "pending") {
    tp = `<span class="tp-badge tp-pending" title="Trickplay wird generiert…">⋯</span>`;
  } else if (it.trickplayStatus === "failed") {
    tp = `<span class="tp-badge tp-failed" title="Trickplay fehlgeschlagen">!</span>`;
  }
  const res = resLabel(it);
  // ×N-Badge: bevorzugt aus dem clientseitigen _variants-Array (wenn der Merge
  // schon im aktuellen Grid passiert ist), sonst aus dem serverseitigen
  // variantCount (zeigt Geschwister-Files in anderen Libraries an, die im
  // aktuellen Render gar nicht enthalten sind).
  const localVariants = (it._variants && it._variants.length > 1) ? it._variants.length : 0;
  const serverVariants = (typeof it.variantCount === "number" && it.variantCount > 1) ? it.variantCount : 0;
  const vcount = Math.max(localVariants, serverVariants);
  const variantBadge = vcount ? `<span class="variant-badge" title="${vcount} Varianten verfügbar">×${vcount}</span>` : "";
  const selected = state.selection.has(it.id);
  if (selected) el.classList.add("selected");
  el.dataset.itemId = String(it.id);
  el.innerHTML = `
    <div class="card-select" data-select>${selected ? "✓" : ""}</div>
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${imgUrl}">
      ${rating}
      ${watched}
      ${confirmBtn}
      ${fav}
      ${tp}
      ${variantBadge}
      <span class="badge">${(it.container || "").toUpperCase()}</span>
      ${res ? `<span class="res-badge">${res}</span>` : ""}
      <span class="duration">${fmtDuration(it.durationSec)}</span>
    </div>
    <div class="card-body">
      <div class="card-title ${episodeCode ? "is-show-link" : ""}" title="${escapeHTML(title)}" data-show-link="${episodeCode ? 1 : 0}">${escapeHTML(title)}</div>
      <div class="card-meta">
        ${episodeCode ? `<span class="episode-code">${episodeCode}</span>` : ""}
        ${episodeName ? `<span class="episode-name">${escapeHTML(episodeName)}</span>` :
          (subtitle ? `<span>${subtitle}</span>` : `<span>${it.width || "?"}×${it.height || "?"}</span>`)}
        <span>${fmtSize(it.sizeBytes)}</span>
        ${released && !subtitle && !episodeName ? `<span>${released}</span>` : ""}
      </div>
      <div class="card-filename" title="${escapeHTML(it.relPath || it.path || "")}">${escapeHTML(cardFileName(it))}</div>
    </div>
  `;
  el.addEventListener("click", (ev) => {
    // Im Auswahl-Modus: Click togglet Selektion statt Detail zu öffnen
    if (state.selectionMode) {
      toggleSelection(it);
      return;
    }
    // Click auf die Checkbox togglet immer (auch außerhalb des Modus → aktiviert ihn)
    if (ev.target && ev.target.closest("[data-select]")) {
      if (!state.selectionMode) setSelectionMode(true);
      toggleSelection(it);
      return;
    }
    // Click auf den Watched-Haken: Status togglen, Detail NICHT öffnen.
    const tog = ev.target && ev.target.closest("[data-toggle-watched]");
    if (tog) {
      ev.stopPropagation();
      toggleWatchedOnCard(it, tog);
      return;
    }
    // Click auf den Favoriten-Herzen: Status togglen, Detail NICHT öffnen.
    const favTog = ev.target && ev.target.closest("[data-toggle-fav]");
    if (favTog) {
      ev.stopPropagation();
      toggleFavoriteOnCard(it, favTog);
      return;
    }
    // Click auf den ✅-Confirm-Button (nur im Duplikate/Suspicious-Modus
    // gerendert): Zuordnung bestätigen, Detail NICHT öffnen. Danach Kachel
    // grün blinken lassen und Grid neu laden (im Suspicious-Modus
    // verschwindet das Item aus der Liste, im Duplikate-Modus bleibt es
    // bestätigt sichtbar).
    const cf = ev.target && ev.target.closest("[data-toggle-confirm]");
    if (cf) {
      ev.stopPropagation();
      cf.disabled = true;
      cf.textContent = "⏳";
      api(`/api/items/${it.id}/confirm`, {
        method: "PUT",
        body: JSON.stringify({ confirmed: true }),
      }).then(() => {
        showToast("Zuordnung bestätigt", { kind: "success" });
        invalidateItemsCache();
        loadItems();
      }).catch(err => {
        cf.disabled = false;
        cf.textContent = "✅";
        appAlert("Fehler: " + err.message);
      });
      return;
    }
    // Click auf den Serien-Titel (nur wenn Episode): zum Serien-Ordner springen
    const showLink = ev.target && ev.target.closest('[data-show-link="1"]');
    if (showLink) {
      ev.stopPropagation();
      const rel = (it.relPath || "").split("/");
      if (rel.length > 1) {
        state.homeView = false;
        state.collectionsView = false;
        state.currentCollection = null;
        state.playlistsView = false;
        state.personFilter = null;
        state.currentLibrary = it.libraryId;
        state.currentFolder = rel[0];
        state.currentFolderDrilldown = false;
        $("#librarySelect").value = "lib:" + it.libraryId;
        loadItems();
        return;
      }
    }
    openDetail(it);
  });
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") openDetail(it); });
  return el;
}

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

async function openDetail(item) {
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
  }
  state.currentItem = item;
  const meta = item.metadata;
  const posterUrl = (meta && meta.posterPath) ? `/api/poster/metadata/${item.metadataId}` : (item.hasThumb ? `/api/thumb/${item.id}` : "/placeholder.svg");

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
  const variants = Array.isArray(item._variants) ? item._variants : [item];
  const hasVariants = variants.length > 1;
  const variantDropdown = hasVariants ? `
    <div class="variant-row">
      <label>Variante
        <select id="detailVariant">
          ${variants.map(v => {
            const label = escapeHTML(variantLabel(v));
            return `<option value="${v.id}" title="${label}">${label}</option>`;
          }).join("")}
        </select>
      </label>
    </div>
  ` : "";
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
        ${variantDropdown}
        <div id="detailCast" class="cast-strip hidden"></div>
        <p class="hint" id="detailFileHint">${fileHintHTML(item)}</p>
      </div>
    </div>
  `;
  if (hasVariants) {
    const sel = $("#detailVariant");
    sel.value = String(item.id);
    sel.addEventListener("change", () => {
      const pick = variants.find(v => String(v.id) === sel.value);
      if (!pick) return;
      // state.currentItem auf ausgewählte Variante setzen (Play/Download/Favorit)
      pick._variants = variants;
      state.currentItem = pick;
      $("#detailFileHint").innerHTML = fileHintHTML(pick);
      updateDetailWatchedBtn();
      updateDetailFavBtn();
    });
  }
  updateDetailWatchedBtn();
  updateDetailFavBtn();
  updateConfirmBtn();
  const itemLib = state.libraries.find(l => l.id == item.libraryId);
  $("#detailMatch").style.display = (itemLib && itemLib.kind === "private") ? "none" : "";
  // Confirm-Button nur sinnvoll bei TMDB-matchen Items (nicht private, nicht
  // unmatched — sonst gibt's nichts zu bestätigen).
  $("#detailConfirm").style.display = ((itemLib && itemLib.kind === "private") || !item.metadataId) ? "none" : "";
  // Refresh-Button: nur sinnvoll wenn TMDB-Zuordnung existiert (Admin-only)
  $("#detailRefreshMeta").style.display = ((state.me && state.me.isAdmin) && item.metadataId && !(itemLib && itemLib.kind === "private")) ? "" : "none";
  // Delete nur für Admins
  $("#detailDelete").style.display = (state.me && state.me.isAdmin) ? "" : "none";
  // Metadaten-Edit/Anlegen nur für Admins, in nicht-Private-Libs. Bei
  // unmatched Items oeffnet der Pencil-Button die Maske leer und legt
  // beim Speichern einen Custom-Metadata-Eintrag an (tmdb_type=custom).
  const canEditMeta = (state.me && state.me.isAdmin) && !(itemLib && itemLib.kind === "private");
  $("#detailEditMeta").style.display = canEditMeta ? "" : "none";
  $("#detailEditMeta").title = item.metadataId ? "Metadaten bearbeiten" : "Metadaten manuell anlegen";
  // Scroll-Position merken & nach showModal() wiederherstellen. Browser springt
  // sonst manchmal an den Seitenanfang, weil das Dialog-Element am DOM-Anfang
  // den Fokus zieht und/oder der Hintergrund-Scroll-Lock beim Öffnen kurz zurücksetzt.
  const savedScrollY = window.scrollY;
  $("#detailDialog").showModal();
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
  await applyPlayback(item, "auto", "orig");
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

function ensurePlayerComponents() {
  if (!window.videojs || state.playerComponentsRegistered) return;
  const Button = window.videojs.getComponent("Button");
  class Skip30Back extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("30 Sekunden zurück");
      // Native Video.js-Icon-Klassen — gleiche Schrift, Größe, Höhe wie der
      // Play-Button. Eigene Glyph-CSS-Overrides würden das brechen.
      this.addClass("vjs-skip-backward-30");
      this.addClass("vjs-icon-replay-30");
    }
    handleClick() { skipPlayer(-30); }
  }
  class Skip30Forward extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("30 Sekunden vor");
      this.addClass("vjs-skip-forward-30");
      this.addClass("vjs-icon-forward-30");
    }
    handleClick() { skipPlayer(30); }
  }
  class ShufflePrev extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Vorheriges Zufalls-Video");
      this.addClass("vjs-shuffle-prev");
    }
    handleClick() { shufflePrev(); }
  }
  class ShuffleNext extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Nächstes Zufalls-Video");
      this.addClass("vjs-shuffle-next");
    }
    handleClick() { shuffleNext(); }
  }
  class FavoriteButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Zu Favoriten hinzufügen");
      this.addClass("vjs-favorite");
    }
    handleClick() {
      const it = state.currentItem;
      if (!it) return;
      const newState = !it.favorite;
      api(`/api/items/${it.id}/favorite`, { method: "PUT", body: JSON.stringify({ favorite: newState }) })
        .then(() => {
          it.favorite = newState;
          updatePlayerButtons();
          updateDetailFavBtn();
          loadItems();
        })
        .catch(e => appAlert(e.message));
    }
  }
  class PlaylistButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Zu Playlist hinzufügen");
      this.addClass("vjs-addplaylist");
    }
    handleClick() {
      const player = this.player_;
      const open = () => { try { openAddToPlaylist(); } catch (e) { console.warn(e); } };
      if (player && typeof player.isFullscreen === "function" && player.isFullscreen()) {
        const p = player.exitFullscreen();
        if (p && typeof p.then === "function") p.then(open, open);
        else setTimeout(open, 60);
      } else {
        open();
      }
    }
  }
  // Admin-Löschbutton in der Control-Bar (neben PlaylistButton). Dezent
  // gehalten: Grauton im Idle, rotet erst bei Hover. Doppelte Bestätigung.
  class DeleteButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Datei löschen");
      this.addClass("vjs-delete");
    }
    handleClick() {
      const player = this.player_;
      const run = async () => {
        const it = state.currentItem;
        if (!it) return;
        const name = (it.metadata && it.metadata.title) || it.title;
        if (!(await appConfirm(`Datei "${name}" UNWIEDERRUFLICH vom Server löschen?\n\nPfad: ${it.path}\n\nDies kann nicht rückgängig gemacht werden.`))) return;
        if (!(await appConfirm(`Wirklich sicher? Die Datei wird für IMMER gelöscht.`))) return;
        try {
          await api(`/api/items/${it.id}?deleteFile=true`, { method: "DELETE" });
          closePlayer();
          invalidateItemsCache();
          loadItems();
          showToast("Datei gelöscht", { kind: "success" });
        } catch (e) { appAlert(e.message); }
      };
      if (player && typeof player.isFullscreen === "function" && player.isFullscreen()) {
        const p = player.exitFullscreen();
        if (p && typeof p.then === "function") p.then(run, run);
        else setTimeout(run, 60);
      } else {
        run();
      }
    }
  }
  // Cast-Button für Chromecast / FireTV / Smart-TVs mit Google Cast.
  // Initialisierung des Cast-Frameworks läuft in initCastFramework() —
  // wird einmal beim Browser-Start aufgerufen und setzt state.castReady,
  // sobald das Framework verfügbar ist.
  class CastButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Auf Gerät streamen");
      this.addClass("vjs-cast-button");
      // Bis Cast-Framework bereit ist: Button ausblenden.
      if (!state.castReady) this.hide();
    }
    handleClick(e) {
      if (e && typeof e.preventDefault === "function") e.preventDefault();
      if (e && typeof e.stopPropagation === "function") e.stopPropagation();
      startCastSession();
    }
  }
  // AirPlay-Button für Safari/iOS. Die HTML-Attribute `airplay="allow"`
  // alleine zeigen das AirPlay-Icon nur an den NATIVEN Browser-Controls.
  // Da Video.js die Controls überlagert, brauchen wir einen Custom-Button,
  // der `webkitShowPlaybackTargetPicker()` triggert. Sichtbarkeit folgt
  // dem `webkitplaybacktargetavailabilitychanged`-Event am <video>.
  class AirPlayButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Auf AirPlay-Gerät streamen");
      this.addClass("vjs-airplay-button");
      this.hide();
      player.ready(() => {
        const v = player.tech_ && player.tech_.el_;
        if (!v || typeof v.webkitShowPlaybackTargetPicker !== "function") return;
        const onAvail = (e) => {
          if (e.availability === "available") this.show(); else this.hide();
        };
        v.addEventListener("webkitplaybacktargetavailabilitychanged", onAvail);
      });
    }
    handleClick() {
      const v = this.player().tech_ && this.player().tech_.el_;
      if (v && typeof v.webkitShowPlaybackTargetPicker === "function") {
        v.webkitShowPlaybackTargetPicker();
      }
    }
  }
  if (!window.videojs.getComponent("ShufflePrev")) {
    window.videojs.registerComponent("Skip30Back", Skip30Back);
    window.videojs.registerComponent("Skip30Forward", Skip30Forward);
    window.videojs.registerComponent("ShufflePrev", ShufflePrev);
    window.videojs.registerComponent("ShuffleNext", ShuffleNext);
    window.videojs.registerComponent("FavoriteButton", FavoriteButton);
    window.videojs.registerComponent("PlaylistButton", PlaylistButton);
    window.videojs.registerComponent("DeleteButton", DeleteButton);
    window.videojs.registerComponent("CastButton", CastButton);
    window.videojs.registerComponent("AirPlayButton", AirPlayButton);
  }
  state.playerComponentsRegistered = true;
}

// --- Google Cast Integration ---
//
// Das Cast-Framework lädt asynchron (Script in index.html). Sobald es
// verfügbar ist, initialisieren wir es mit dem Default-Media-Receiver
// (CC1AD845) — das ist Googles offizielle App, die direkte Stream-URLs
// (HLS, MP4) abspielt. Eigener Receiver wäre möglich, aber unnötig: für
// einfaches Streaming reicht der Default.
function initCastFramework() {
  // Setup-Function: läuft sobald wir wissen, ob Cast verfügbar ist.
  // Idempotent — kann sowohl aus dem frühen __onGCastApiAvailable-Pfad
  // (Stub in index.html) als auch aus einem späten Callback aufgerufen werden.
  const setup = (isAvailable) => {
    if (!isAvailable || !window.cast || !window.cast.framework) {
      console.log("[cast] not available in this browser (Firefox? kein Chromium?)");
      return;
    }
    try {
      const ctx = window.cast.framework.CastContext.getInstance();
      ctx.setOptions({
        receiverApplicationId: window.chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
        autoJoinPolicy: window.chrome.cast.AutoJoinPolicy.ORIGIN_SCOPED,
      });
      state.castReady = true;
      // Sichtbarkeit aller schon registrierten Cast-Buttons aktualisieren.
      if (state.vjs) {
        const cb = state.vjs.getChild("controlBar");
        const btn = cb && cb.getChild("CastButton");
        if (btn) btn.show();
      }
      console.log("[cast] framework ready");
    } catch (e) {
      console.warn("[cast] init failed", e);
    }
  };
  // Race-Handling: Wenn das Cast-SDK aus dem Browser-Cache schneller fertig
  // ist als app.js, hat der index.html-Stub das Ergebnis bereits gespeichert.
  // Sonst registrieren wir uns als Callback für den späteren SDK-Ready-Event.
  if (window.__castSdkReady) {
    setup(window.__castSdkAvailable);
  } else {
    window.__onCastSdkResult = setup;
  }
}

// startCastSession: vom CastButton-Click. Holt einen Cast-Session-Token vom
// Server (via /api/auth/cast-token, 4 h TTL), baut die Stream-URL mit
// `?session=<token>` und startet die Cast-Session am Default-Receiver.
async function startCastSession() {
  console.log("[cast] CastButton clicked", {
    castReady: state.castReady,
    castGlobal: !!window.cast,
    castFramework: !!(window.cast && window.cast.framework),
    chromeCast: !!(window.chrome && window.chrome.cast),
    currentItem: state.currentItem && state.currentItem.id,
  });
  if (!state.castReady || !window.cast || !window.cast.framework) {
    appAlert('Cast-Framework ist nicht bereit. Konsole öffnen (Cmd-Opt-I) und nach "[cast]" suchen.');
    return;
  }
  if (!state.currentItem) return;
  const item = state.currentItem;
  let token = state.castToken || "";
  try {
    if (!token) {
      const r = await api("/api/auth/cast-token", { method: "POST" });
      token = r && r.token;
      if (token) state.castToken = token;
    }
  } catch (e) {
    appAlert("Cast-Token konnte nicht erstellt werden: " + e.message);
    return;
  }
  if (!token) return;
  // Stream-URL: bei Direct Play unsere /api/stream/{id}-Route, bei Transcode
  // die HLS-Playlist (Cast unterstützt HLS nativ via Default-Receiver).
  const mode = (state.playback && state.playback.mode) || "direct";
  const sep = (u) => u.includes("?") ? "&" : "?";
  let url, contentType;
  if (mode === "transcode") {
    const profile = (state.playback && state.playback.profile) || "orig";
    const audioIdx = state.playback && state.playback.audioIdx;
    url = `${location.origin}/api/transcode/${item.id}/index.m3u8?profile=${encodeURIComponent(profile)}`;
    if (typeof audioIdx === "number" && audioIdx >= 0) url += `&audio=${audioIdx}`;
    url += `&session=${encodeURIComponent(token)}`;
    contentType = "application/vnd.apple.mpegurl";
  } else {
    url = `${location.origin}/api/stream/${item.id}${sep(`/api/stream/${item.id}`)}session=${encodeURIComponent(token)}`;
    contentType = "video/mp4";
  }
  try {
    const ctx = window.cast.framework.CastContext.getInstance();
    await ctx.requestSession();
    const session = ctx.getCurrentSession();
    if (!session) return;
    const mediaInfo = new window.chrome.cast.media.MediaInfo(url, contentType);
    mediaInfo.metadata = new window.chrome.cast.media.GenericMediaMetadata();
    const md = item.metadata;
    mediaInfo.metadata.title = (md && md.title) || item.title;
    if (md && md.posterPath && item.metadataId) {
      mediaInfo.metadata.images = [
        new window.chrome.cast.Image(`${location.origin}/api/poster/metadata/${item.metadataId}`),
      ];
    }
    const request = new window.chrome.cast.media.LoadRequest(mediaInfo);
    // Wenn der lokale Player bereits läuft: an aktueller Position weiterspielen.
    if (state.vjs && typeof state.vjs.currentTime === "function") {
      const cur = state.vjs.currentTime() || 0;
      const offset = (state.playback && state.playback.virtualOffset) || 0;
      request.currentTime = Math.max(0, cur + offset);
      // Lokal pausieren — Cast übernimmt jetzt.
      try { state.vjs.pause(); } catch {}
    }
    await session.loadMedia(request);
    showToast("Cast gestartet — Wiedergabe läuft am gewählten Gerät", { kind: "success", duration: 3500 });
  } catch (e) {
    if (e && e.code === "cancel") return; // User hat das Geräte-Picker-Modal abgebrochen
    const code = e && e.code;
    let msg;
    if (code === "session_error" || code === "receiver_unavailable") {
      msg = `Kein Cast-Gerät gefunden.\n\nGoogle-Cast funktioniert mit Chromecast, FireTV (mit 'AirScreen'-App) oder Smart-TVs mit eingebautem Cast (Sony Bravia, Vizio …).\n\nApple TV unterstützt kein Google-Cast — dafür AirPlay aus Safari nutzen.`;
    } else if (code === "timeout") {
      msg = `Cast-Verbindung Timeout — Gerät reagiert nicht. Im selben WLAN? Neu starten?`;
    } else {
      msg = "Cast-Fehler: " + (e && e.description || code || e);
    }
    appAlert(msg);
  }
}

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
}

// Spielt das nächste Item in der aktiven Playlist-Queue ab, falls vorhanden.
function playNextInQueue() {
  if (state.playQueueIdx < 0) return;
  const next = state.playQueue[state.playQueueIdx + 1];
  if (!next) return;
  openPlayer(next);
}

// Auto-Mark als "gesehen" wenn 90% der Laufzeit erreicht
function maybeMarkWatched(vjs) {
  if (state.watchedFired) return;
  const item = state.currentItem;
  if (!item || item.watched) return;
  const dur = vjs ? vjs.duration() : 0;
  const cur = vjs ? vjs.currentTime() : 0;
  if (!dur || dur <= 0) return;
  if (cur / dur >= 0.9) {
    state.watchedFired = true;
    api(`/api/items/${item.id}/watched`, { method: "PUT", body: JSON.stringify({ watched: true }) })
      .then(() => { item.watched = true; updateDetailWatchedBtn(); })
      .catch(console.warn);
  }
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
  const subs = streams.filter(st => st.type === "subtitle");
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
      parts.push(t.codec || "");
      o.textContent = parts.filter(Boolean).join(" · ");
      subSel.appendChild(o);
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
    // Alte Remote-Text-Tracks entfernen, damit sie sich nicht stapeln
    const rtt = vjs.remoteTextTracks();
    if (rtt) {
      for (let i = rtt.length - 1; i >= 0; i--) {
        try { vjs.removeRemoteTextTrack(rtt[i]); } catch {}
      }
    }
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
    vjs.on("timeupdate", () => maybeMarkWatched(vjs));
    vjs.on("ended", () => {
      // Wiedergabe durchgespielt → Resume-Marker löschen.
      if (state.currentItem) {
        api(`/api/items/${state.currentItem.id}/resume`, {
          method: "PUT",
          body: JSON.stringify({ positionSec: 0 }),
        }).catch(() => {});
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
    addAt("Skip30Back", skipBase);
    addAt("Skip30Forward", skipBase + 1);
    // Custom-Buttons rechts (vor FullscreenToggle) — am Ende der ControlBar.
    const fsIdx = cb.children().findIndex(c => c.name_ === "FullscreenToggle");
    const insertAt = fsIdx >= 0 ? fsIdx : cb.children().length;
    const addIfMissing = (name, offset) => {
      if (!cb.getChild(name)) cb.addChild(name, {}, insertAt + offset);
    };
    addIfMissing("ShufflePrev", 0);
    addIfMissing("ShuffleNext", 1);
    addIfMissing("FavoriteButton", 2);
    addIfMissing("PlaylistButton", 3);
    // Cast-Button — bleibt unsichtbar, bis das Cast-Framework geladen ist
    // (initCastFramework markiert state.castReady und ruft btn.show()).
    addIfMissing("CastButton", 4);
    // AirPlay-Button NUR bei Direct Play hinzufügen. Bei Transcode (HLS via
    // VHS-MSE) zeigt Safari zwar den AirPlay-Picker, kann den Stream aber
    // nicht an den AppleTV weiterreichen — der Spinner dreht sich auf dem
    // AppleTV ohne dass je Frames ankommen. Apple unterstützt AirPlay-
    // Routing nur, wenn das <video>-Element direkt eine Source-URL liest
    // (progressives MP4 = Direct Play). Bei Transcode-Items gibt's stattdessen
    // den Hinweis im UI: macOS-Bildschirmsynchronisierung verwenden.
    if ((info && info.mode) === "direct") {
      addIfMissing("AirPlayButton", 5);
    }
    // Löschbutton nur für Admins; liegt direkt neben PlaylistButton.
    if (state.me && state.me.isAdmin) {
      addIfMissing("DeleteButton", 6);
    }
  }
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

  // Aktuell gewählten Subtitle-Stream als text-track einhängen
  const subChoice = $("#subSelect").value;
  if (subChoice) {
    const sub = subs.find(s => String(s.index) === subChoice);
    const label = (sub && sub.title) || (sub && sub.language && sub.language.toUpperCase()) || "Untertitel";
    vjs.addRemoteTextTrack({
      kind: "subtitles",
      src: `/api/subtitle/${item.id}/${subChoice}.vtt`,
      srclang: (sub && sub.language) || "und",
      label: label,
      default: true,
    }, false);
  }
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
    const params = new URLSearchParams({ profile: profile || "orig" });
    if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
      params.set("audio", String(audioIdx));
    }
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

// --- User-Menü + Admin-Panel ---

function renderUserMenu() {
  const wrap = $("#userMenu");
  wrap.innerHTML = "";
  if (!state.me) return;
  const u = document.createElement("span");
  u.innerHTML = `<strong>${escapeHTML(state.me.username)}</strong>${state.me.isAdmin ? ' <span class="admin-badge">Admin</span>' : ""}`;
  wrap.appendChild(u);
  // Passwort-Dialog ist weiterhin verfügbar (Benutzer-Verwaltung); nicht mehr
  // als eigener Button in der Topleiste.
  const lo = document.createElement("button");
  lo.textContent = "Abmelden";
  lo.addEventListener("click", async () => {
    try { await fetch("/api/auth/logout", { method: "POST" }); } catch {}
    location.href = "/login.html";
  });
  wrap.appendChild(lo);
  // Admin-Menüeinträge nur für Admins sichtbar
  $("#settingsMenuUsers").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuTrickplay").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuPathSearch").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuMissing").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuRefreshAllMeta").classList.toggle("hidden", !state.me.isAdmin);
  // Das gesamte Zahnrad-Menü ist Admin-only: alle übrigen Einträge (Settings,
  // Bibliotheken) sind ebenfalls administrative Aktionen. Für reguläre User
  // wird der Button komplett ausgeblendet.
  $("#settingsBtn").classList.toggle("hidden", !state.me.isAdmin);
}

async function openUsersManager() {
  const users = await api("/api/users");
  const wrap = $("#usersList");
  wrap.innerHTML = "";
  for (const u of users) {
    wrap.appendChild(renderUserCard(u));
  }
  if (!$("#usersDialog").open) $("#usersDialog").showModal();
}

// renderUserCard baut eine saubere Karte pro User:
//   [Avatar-Initial] Benutzername [Badges: Admin, FSK]
//                    angelegt am XX.XX.XXXX
//   ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈
//   Altersfreigabe: [Dropdown]
//   Bibliotheken:   [Anzahl] [▸ Verwalten]
//   Aktionen:       [🔐 Passwort] [👑 Admin-Toggle] [🗑 Löschen]
function renderUserCard(u) {
  const card = document.createElement("div");
  card.className = "user-card";
  const isMe = !!(state.me && state.me.username === u.username);
  const initial = (u.username || "?").charAt(0).toUpperCase();
  const created = u.createdAt ? fmtDate(u.createdAt) : "";
  const fskBadge = u.maxAgeRating != null
    ? `<span class="fsk-badge fsk-${u.maxAgeRating}">FSK ${u.maxAgeRating}</span>`
    : "";
  card.innerHTML = `
    <div class="user-card-head">
      <div class="user-avatar" aria-hidden="true">${escapeHTML(initial)}</div>
      <div class="user-card-title">
        <div class="user-name">
          ${escapeHTML(u.username)}
          ${u.isAdmin ? '<span class="admin-badge">Admin</span>' : ""}
          ${fskBadge}
          ${isMe ? '<span class="user-self-badge">du</span>' : ""}
        </div>
        ${created ? `<div class="user-sub">angelegt am ${escapeHTML(created)}</div>` : ""}
      </div>
    </div>
    <div class="user-card-body">
      <div class="user-field">
        <label>Altersfreigabe</label>
        <select class="user-age-select">
          <option value="">– keine Beschränkung –</option>
          <option value="0">FSK 0</option>
          <option value="6">FSK 6</option>
          <option value="12">FSK 12</option>
          <option value="16">FSK 16</option>
          <option value="18">FSK 18</option>
        </select>
      </div>
      <div class="user-field">
        <label>Bibliotheken</label>
        <button type="button" class="user-btn user-acl-btn">🗂 Verwalten</button>
      </div>
      <div class="user-field user-actions">
        <label>Aktionen</label>
        <div class="user-action-row">
          <button type="button" class="user-btn user-pw-btn" title="Passwort neu setzen">🔐 Passwort</button>
          ${isMe ? "" : `
            <button type="button" class="user-btn user-admin-btn" title="${u.isAdmin ? "Admin-Rechte entziehen" : "Zu Admin machen"}">
              ${u.isAdmin ? "👤 Admin entfernen" : "👑 Zu Admin machen"}
            </button>
            <button type="button" class="user-btn danger user-del-btn" title="Benutzer löschen">🗑 Löschen</button>
          `}
        </div>
      </div>
    </div>
  `;
  // Event-Handler anhängen (nachdem das Markup im DOM ist).
  const sel = card.querySelector(".user-age-select");
  sel.value = u.maxAgeRating != null ? String(u.maxAgeRating) : "";
  sel.addEventListener("change", async () => {
    const v = sel.value === "" ? null : parseInt(sel.value, 10);
    try {
      await api(`/api/users/${u.id}/age-rating`, {
        method: "PUT",
        body: JSON.stringify({ maxAgeRating: v }),
      });
      showToast(v === null ? `${u.username}: keine FSK-Beschränkung` : `${u.username}: max FSK ${v}`, { kind: "success" });
      openUsersManager();
    } catch (e) {
      appAlert(e.message);
      sel.value = u.maxAgeRating != null ? String(u.maxAgeRating) : "";
    }
  });
  card.querySelector(".user-acl-btn").addEventListener("click", () => openUserAcl(u));
  card.querySelector(".user-pw-btn").addEventListener("click", async () => {
    const np = await appPrompt(`Neues Passwort für ${u.username} (min. 6 Zeichen):`);
    if (!np || np.length < 6) return;
    try {
      await api(`/api/users/${u.id}/password`, { method: "PUT", body: JSON.stringify({ password: np }) });
      showToast("Passwort gesetzt", { kind: "success" });
    } catch (e) { appAlert(e.message); }
  });
  const adminBtn = card.querySelector(".user-admin-btn");
  if (adminBtn) {
    adminBtn.addEventListener("click", async () => {
      try {
        await api(`/api/users/${u.id}/admin`, {
          method: "PUT",
          body: JSON.stringify({ isAdmin: !u.isAdmin }),
        });
        openUsersManager();
      } catch (e) { appAlert(e.message); }
    });
  }
  const delBtn = card.querySelector(".user-del-btn");
  if (delBtn) {
    delBtn.addEventListener("click", async () => {
      if (!(await appConfirm(`Benutzer '${u.username}' wirklich löschen?`))) return;
      try {
        await api(`/api/users/${u.id}`, { method: "DELETE" });
        openUsersManager();
      } catch (e) { appAlert(e.message); }
    });
  }
  return card;
}

async function handleNewUser(e) {
  e.preventDefault();
  const form = e.target;
  const fd = new FormData(form);
  const body = {
    username: fd.get("username"),
    password: fd.get("password"),
    isAdmin: fd.get("isAdmin") === "on",
  };
  try {
    await api("/api/users", { method: "POST", body: JSON.stringify(body) });
    form.reset();
    openUsersManager();
  } catch (err) { appAlert(err.message); }
}

async function openUserAcl(u) {
  const [allLibs, userIDs] = await Promise.all([
    api("/api/libraries"),
    api(`/api/users/${u.id}/libraries`),
  ]);
  const granted = new Set(userIDs);
  $("#userAclTitle").textContent = `Bibliothek-Zugriff für "${u.username}"`;
  const list = $("#userAclList");
  list.innerHTML = "";
  for (const l of allLibs) {
    const lbl = document.createElement("label");
    lbl.style.display = "flex"; lbl.style.gap = "8px"; lbl.style.alignItems = "center";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.dataset.libId = l.id;
    cb.checked = granted.has(l.id);
    lbl.appendChild(cb);
    const span = document.createElement("span");
    const kindLabel = l.kind === "movies" ? "Filme" : l.kind === "tv" ? "Serien" : "Privat";
    span.innerHTML = `<strong>${escapeHTML(l.name)}</strong> <small style="color:var(--text-dim)">(${kindLabel})</small>`;
    lbl.appendChild(span);
    list.appendChild(lbl);
  }
  $("#userAclSave").onclick = async () => {
    const ids = [];
    list.querySelectorAll("input[type=checkbox]").forEach(cb => {
      if (cb.checked) ids.push(Number(cb.dataset.libId));
    });
    try {
      await api(`/api/users/${u.id}/libraries`, { method: "PUT", body: JSON.stringify({ libraryIds: ids }) });
      $("#userAclDialog").close();
    } catch (e) { appAlert(e.message); }
  };
  $("#userAclDialog").showModal();
}

async function handleMyPassword(e) {
  e.preventDefault();
  const form = e.target;
  const body = Object.fromEntries(new FormData(form));
  try {
    await api("/api/auth/password", { method: "PUT", body: JSON.stringify(body) });
    form.reset();
    appAlert("Passwort geändert.");
    $("#passwordDialog").close();
  } catch (err) { appAlert(err.message); }
}

// --- Playlists ---

async function openPlaylistsManager() {
  await loadLibraries(); // refresh
  const ul = $("#playlistsList");
  ul.innerHTML = "";
  if (!state.playlists.length) {
    ul.innerHTML = `<li><em>Noch keine Playlists.</em></li>`;
  }
  for (const p of state.playlists) {
    const li = document.createElement("li");
    li.className = "lib-row";
    const head = document.createElement("div");
    head.className = "lib-row-head";
    head.innerHTML = `<div><strong>${escapeHTML(p.name)}</strong> <small style="color:#9ca3af">${p.itemCount} Video${p.itemCount===1?"":"s"}</small></div>`;
    const actions = document.createElement("div");
    actions.style.display = "flex"; actions.style.gap = "6px";
    const open = document.createElement("button");
    open.textContent = "Öffnen";
    open.addEventListener("click", () => {
      enterPlaylist(p.id);
      state.currentLibrary = null;
      state.currentFolder = null;
      $("#playlistsDialog").close();
      loadLibraries().then(loadItems);
    });
    actions.appendChild(open);
    const rename = document.createElement("button");
    rename.textContent = "Umbenennen";
    rename.addEventListener("click", async () => {
      const newName = await appPrompt("Neuer Name:", p.name);
      if (!newName || newName === p.name) return;
      try {
        await api(`/api/playlists/${p.id}`, { method: "PUT", body: JSON.stringify({ name: newName }) });
        openPlaylistsManager();
      } catch (e) { appAlert(e.message); }
    });
    actions.appendChild(rename);
    const del = document.createElement("button");
    del.textContent = "Löschen";
    del.classList.add("danger");
    del.addEventListener("click", async () => {
      if (!(await appConfirm(`Playlist "${p.name}" löschen?`))) return;
      await api(`/api/playlists/${p.id}`, { method: "DELETE" });
      if (state.currentPlaylist == p.id) {
        state.currentPlaylist = null;
        state.currentLibrary = state.libraries[0] ? state.libraries[0].id : null;
      }
      openPlaylistsManager();
      loadItems();
    });
    actions.appendChild(del);
    head.appendChild(actions);
    li.appendChild(head);
    ul.appendChild(li);
  }
  $("#playlistsDialog").showModal();
}

async function handleNewPlaylist(e) {
  e.preventDefault();
  const form = e.target;
  const data = Object.fromEntries(new FormData(form));
  try {
    await api("/api/playlists", { method: "POST", body: JSON.stringify({ name: data.name }) });
    form.reset();
    openPlaylistsManager();
  } catch (err) { appAlert(err.message); }
}

// openAddToPlaylist öffnet den „Zu Playlist hinzufügen"-Dialog mit Liste aller
// vorhandenen Playlists plus Quick-Create-Formular. Arbeitet sowohl für ein
// einzelnes Item (Detail-Dialog) als auch für Bulk-Auswahl.
//
// opts.itemIDs: optionales Array von Item-IDs. Ohne → fällt auf
//   [state.currentItem.id] zurück. Mit Array → Bulk-Modus: Klick auf eine
//   Playlist hängt alle IDs an, Quick-Create erstellt neue Playlist und hängt
//   alle IDs rein.
async function openAddToPlaylist(opts = {}) {
  const itemIDs = Array.isArray(opts.itemIDs) && opts.itemIDs.length
    ? opts.itemIDs.slice()
    : (state.currentItem ? [state.currentItem.id] : []);
  if (!itemIDs.length) return;
  state.playlistAddContext = { itemIDs, isBulk: itemIDs.length > 1 };

  const isBulk = state.playlistAddContext.isBulk;
  // Im Bulk-Modus zeigen wir keine ✓-Markierung (wäre uneinheitlich, wenn
  // manche der Items in der Playlist sind und andere nicht).
  const allPL = await api("/api/playlists");
  const currentIDs = isBulk ? [] : await api(`/api/items/${itemIDs[0]}/playlists`);
  const idSet = new Set(currentIDs);
  const ul = $("#addToPlaylistList");
  ul.innerHTML = "";
  if (!allPL.length) {
    ul.innerHTML = `<li><em style="color:#6b7280">Noch keine Playlist angelegt — nutze das Formular unten.</em></li>`;
  }
  const h2 = $("#addToPlaylistDialog h2");
  if (h2) h2.textContent = isBulk
    ? `${itemIDs.length} Videos zu Playlist hinzufügen`
    : "Zu Playlist hinzufügen";
  for (const p of allPL) {
    const li = document.createElement("li");
    const inSet = !isBulk && idSet.has(p.id);
    li.innerHTML = `${inSet ? "✓ " : ""}<strong>${escapeHTML(p.name)}</strong> <small style="color:#6b7280">(${p.itemCount})</small>`;
    li.addEventListener("click", async () => {
      try {
        if (isBulk) {
          await addItemsToPlaylistBulk(p, itemIDs);
          $("#addToPlaylistDialog").close();
          setSelectionMode(false);
          return;
        }
        if (inSet) {
          await api(`/api/playlists/${p.id}/items/${itemIDs[0]}`, { method: "DELETE" });
          showToast(`Aus „${p.name}" entfernt`, { kind: "success" });
        } else {
          const res = await api(`/api/playlists/${p.id}/items`, {
            method: "POST",
            body: JSON.stringify({ itemId: itemIDs[0] }),
          });
          if (res && res.added) {
            showToast(`Zu „${p.name}" hinzugefügt`, { kind: "success" });
          } else {
            showToast(`Ist bereits in „${p.name}"`);
          }
        }
        openAddToPlaylist({ itemIDs });
      } catch (e) { appAlert(e.message); }
    });
    ul.appendChild(li);
  }
  $("#addToPlaylistDialog").showModal();
}

// Hilfsfunktion: hängt mehrere Items an eine bestehende Playlist und zeigt
// einen Sammel-Toast mit neu/duplikat/Fehler-Zählung.
async function addItemsToPlaylistBulk(pl, itemIDs) {
  let added = 0, dup = 0, fails = 0;
  for (const id of itemIDs) {
    try {
      const res = await api(`/api/playlists/${pl.id}/items`, {
        method: "POST",
        body: JSON.stringify({ itemId: id }),
      });
      if (res && res.added) added++; else dup++;
    } catch { fails++; }
  }
  const parts = [`${added} zu „${pl.name}" hinzugefügt`];
  if (dup) parts.push(`${dup} bereits drin`);
  if (fails) parts.push(`${fails} Fehler`);
  showToast(parts.join(", "), { kind: fails ? "error" : "success", duration: 4000 });
}

async function handleQuickCreatePlaylist(e) {
  e.preventDefault();
  const ctx = state.playlistAddContext;
  const itemIDs = (ctx && ctx.itemIDs) || (state.currentItem ? [state.currentItem.id] : []);
  if (!itemIDs.length) return;
  const form = e.target;
  const data = Object.fromEntries(new FormData(form));
  try {
    const pl = await api("/api/playlists", { method: "POST", body: JSON.stringify({ name: data.name }) });
    if (itemIDs.length > 1) {
      await addItemsToPlaylistBulk(pl, itemIDs);
    } else {
      await api(`/api/playlists/${pl.id}/items`, {
        method: "POST",
        body: JSON.stringify({ itemId: itemIDs[0] }),
      });
      showToast(`Neue Playlist „${pl.name}" mit Video erstellt`, { kind: "success" });
    }
    form.reset();
    $("#addToPlaylistDialog").close();
    if (ctx && ctx.isBulk) setSelectionMode(false);
  } catch (err) { appAlert(err.message); }
}

// --- Zufällige Wiedergabe ---

// randomParams: baut die URL-Parameter für /api/items/random aus dem aktuellen Kontext.
function randomParams() {
  const params = new URLSearchParams();
  if (state.currentLibrary) params.set("libraryId", state.currentLibrary);
  if (state.currentFolder !== null) params.set("folder", state.currentFolder);
  const search = $("#searchInput").value.trim();
  if (search) params.set("search", search);
  const watched = $("#watchedFilter").value; if (watched) params.set("watched", watched);
  const fav = currentFavoriteMode(); if (fav) params.set("favorite", fav);
  const match = currentMatchMode(); if (match) params.set("match", match);
  // Auflösungs-Filter (Multi-Select-Buckets)
  applyResolutionFilter(params);
  return params;
}

async function playRandom() {
  if (!state.currentLibrary) {
    appAlert("Keine Bibliothek gewählt.");
    return;
  }
  try {
    const item = await api(`/api/items/random?${randomParams()}`);
    state.shuffleMode = true;
    state.shuffleHistory = [item];
    state.shuffleIdx = 0;
    openPlayer(item, { fromShuffle: true });
  } catch (e) {
    appAlert("Kein zufälliges Video gefunden: " + e.message);
  }
}

async function shuffleNext() {
  if (!state.shuffleMode) return;
  // Wenn wir in der History zurückgesprungen sind, vorwärts innerhalb der History
  if (state.shuffleIdx < state.shuffleHistory.length - 1) {
    state.shuffleIdx++;
    openPlayer(state.shuffleHistory[state.shuffleIdx], { fromShuffle: true });
    return;
  }
  // Sonst: neues Zufalls-Item holen und anhängen
  try {
    const item = await api(`/api/items/random?${randomParams()}`);
    state.shuffleHistory.push(item);
    state.shuffleIdx = state.shuffleHistory.length - 1;
    openPlayer(item, { fromShuffle: true });
  } catch (e) {
    appAlert("Kein weiteres Zufallsvideo: " + e.message);
  }
}

function shufflePrev() {
  if (!state.shuffleMode || state.shuffleIdx <= 0) return;
  state.shuffleIdx--;
  openPlayer(state.shuffleHistory[state.shuffleIdx], { fromShuffle: true });
}

// --- Scan ---

async function startScan(mode = "incremental") {
  // mode: "incremental" | "force" | "folder" | "folder-force" | "all" | "all-force"
  try {
    if (mode === "all" || mode === "all-force") {
      // Batch-Modus aktivieren: pollScan sammelt jede Lib-Zusammenfassung, statt
      // sie einzeln anzuzeigen. Am Ende kommt EIN Dialog mit allen Libs.
      state.expectingScanAll = true;
      state.scanAllSummaries = [];
      state.scanAllCaptured = new Set();
      state.scanAllForce = (mode === "all-force");
      const qs = mode === "all-force" ? "?force=true" : "";
      await api(`/api/scan/all${qs}`, { method: "POST" });
    } else {
      if (!state.currentLibrary) {
        appAlert("Keine Bibliothek gewählt.");
        return;
      }
      const params = new URLSearchParams();
      if (mode === "force" || mode === "folder-force") params.set("force", "true");
      if (mode === "folder" || mode === "folder-force") {
        if (!state.currentFolder) {
          appAlert("Kein Ordner offen.");
          return;
        }
        params.set("folder", state.currentFolder);
      }
      const qs = params.toString() ? "?" + params.toString() : "";
      await api(`/api/scan/${state.currentLibrary}${qs}`, { method: "POST" });
    }
  } catch (e) {
    appAlert("Scan konnte nicht gestartet werden: " + e.message);
    return;
  }
  pollScan();
}

// showScanSummary öffnet einen Modal-Dialog mit der Bilanz eines beendeten
// Scans. Stat-Kacheln und Folder-Zeilen sind klickbar — beim Klick wird die
// passende Pfad-Liste in einer Detail-Box unter den Kacheln eingeblendet.
function showScanSummary(s) {
  if (!s) return;
  state.lastScanSummary = s;
  const head = $("#scanSummaryHead");
  const stats = $("#scanSummaryStats");
  const folders = $("#scanSummaryFolders");
  const detail = $("#scanSummaryDetail");
  if (!head || !stats || !folders || !detail) return;

  const dur = s.startedAt && s.finishedAt
    ? Math.max(0, Math.round((new Date(s.finishedAt) - new Date(s.startedAt)) / 1000))
    : 0;
  const scope = s.folder ? `Ordner <strong>${escapeHTML(s.folder)}</strong>` : "ganze Bibliothek";
  head.innerHTML = `
    <div><strong>${escapeHTML(s.libraryName || "?")}</strong> · ${scope}${s.force ? " · force" : ""}</div>
    <div class="hint">Dauer: ${dur}s · ${(new Date(s.finishedAt)).toLocaleTimeString()}</div>
    ${s.error ? `<div class="hint" style="color:#ef4444;margin-top:4px">Fehler: ${escapeHTML(s.error)}</div>` : ""}
  `;

  // Klickbare Stat-Kacheln. „Übersprungen" + „Gesamt" haben keine Pfad-Liste,
  // sind also nicht interaktiv.
  const cells = [
    { label: "🆕 Neu",          value: s.new || 0,     cls: "stat-new",  kind: "new" },
    { label: "🔄 Aktualisiert", value: s.updated || 0, cls: "stat-upd",  kind: "updated" },
    { label: "⏭ Übersprungen", value: s.skipped || 0, cls: "stat-skip", kind: null },
    { label: "🗑 Entfernt",     value: s.removed || 0, cls: "stat-del",  kind: "removed" },
    { label: "Gesamt",          value: s.total || 0,   cls: "stat-total", kind: null },
  ];
  stats.innerHTML = cells.map(c => {
    const interactive = c.kind && c.value > 0;
    return `<div class="scan-stat ${c.cls} ${interactive ? "is-clickable" : ""}" ${interactive ? `data-stat="${c.kind}"` : ""}>
       <div class="scan-stat-num">${c.value}</div>
       <div class="scan-stat-label">${c.label}</div>
     </div>`;
  }).join("");
  stats.querySelectorAll("[data-stat]").forEach(el => {
    el.addEventListener("click", () => showScanDetail(el.dataset.stat, null));
  });

  const pf = s.perFolder || {};
  const keys = Object.keys(pf).filter(k => {
    const v = pf[k] || {};
    return (v.new || 0) + (v.updated || 0) + (v.removed || 0) > 0;
  }).sort((a, b) => a.localeCompare(b));
  if (!keys.length) {
    folders.innerHTML = `<div class="hint">Keine Änderungen pro Ordner.</div>`;
  } else {
    folders.innerHTML = `
      <table class="scan-folder-table">
        <thead><tr><th>Ordner</th><th>Neu</th><th>Aktual.</th><th>Entfernt</th></tr></thead>
        <tbody>${keys.map(k => {
          const v = pf[k] || {};
          const name = k === "" ? "(Library-Root)" : escapeHTML(k);
          const dataK = k === "" ? "__root__" : k;
          return `<tr data-folder="${escapeHTML(dataK)}">
            <td><span class="scan-folder-name">${name}</span></td>
            <td class="scan-cell ${(v.new||0)>0?"clickable":""}" data-kind="new">${v.new || 0}</td>
            <td class="scan-cell ${(v.updated||0)>0?"clickable":""}" data-kind="updated">${v.updated || 0}</td>
            <td class="scan-cell ${(v.removed||0)>0?"clickable":""}" data-kind="removed">${v.removed || 0}</td>
          </tr>`;
        }).join("")}</tbody>
      </table>
    `;
    folders.querySelectorAll("td.scan-cell.clickable").forEach(td => {
      td.addEventListener("click", () => {
        const tr = td.closest("tr");
        const f = tr.dataset.folder === "__root__" ? "" : tr.dataset.folder;
        showScanDetail(td.dataset.kind, f);
      });
    });
  }

  detail.classList.add("hidden");
  detail.innerHTML = "";
  $("#scanSummaryDialog").showModal();
}

// showScanDetail blendet die Pfad-Liste für eine Kategorie (new/updated/removed)
// optional gefiltert auf einen Top-Level-Folder ein.
function showScanDetail(kind, folderFilter) {
  const s = state.lastScanSummary;
  if (!s) return;
  const detail = $("#scanSummaryDetail");
  if (!detail) return;
  const map = { new: s.newPaths || [], updated: s.updatedPaths || [], removed: s.removedPaths || [] };
  const labels = { new: "🆕 Neu", updated: "🔄 Aktualisiert", removed: "🗑 Entfernt" };
  let paths = map[kind] || [];
  if (folderFilter !== null && folderFilter !== undefined) {
    paths = paths.filter(p => {
      const top = p.split("/")[0] || "";
      return folderFilter === "" ? !p.includes("/") : top === folderFilter;
    });
  }
  if (!paths.length) {
    detail.innerHTML = `<div class="hint">Keine Pfade in dieser Kategorie.</div>`;
    detail.classList.remove("hidden");
    return;
  }
  const scopeLabel = folderFilter
    ? `${labels[kind]} in <strong>${escapeHTML(folderFilter)}</strong>`
    : (folderFilter === "" ? `${labels[kind]} im Library-Root` : labels[kind]);
  detail.innerHTML = `
    <div class="scan-detail-head">
      <span>${scopeLabel} · ${paths.length} Datei${paths.length === 1 ? "" : "en"}</span>
      <button type="button" class="scan-detail-close" aria-label="Schließen">✕</button>
    </div>
    <ul class="scan-detail-list">
      ${paths.slice(0, 500).map(p => `<li>${escapeHTML(p)}</li>`).join("")}
    </ul>
    ${paths.length > 500 ? `<div class="hint">… und ${paths.length - 500} weitere (Limit für die Anzeige).</div>` : ""}
  `;
  detail.classList.remove("hidden");
  detail.querySelector(".scan-detail-close").addEventListener("click", () => {
    detail.classList.add("hidden");
    detail.innerHTML = "";
  });
  // Sanft hochscrollen, damit der User die Liste direkt sieht
  detail.scrollIntoView({ behavior: "smooth", block: "center" });
}

// showScanAllSummary zeigt nach einem „Alle Bibliotheken"-Scan EINEN Dialog
// mit Aggregat-Counts oben + pro Lib eine eigene Sektion. Klick auf eine Lib
// blendet die per-Lib-Detail-Ansicht (ähnlich wie Single-Scan) inline ein.
function showScanAllSummary(summaries, force) {
  if (!summaries || !summaries.length) return;
  state.lastScanAllSummaries = summaries;
  const head = $("#scanSummaryHead");
  const stats = $("#scanSummaryStats");
  const folders = $("#scanSummaryFolders");
  const detail = $("#scanSummaryDetail");
  if (!head || !stats || !folders || !detail) return;

  const totals = summaries.reduce((acc, s) => {
    acc.total   += s.total   || 0;
    acc.new     += s.new     || 0;
    acc.updated += s.updated || 0;
    acc.skipped += s.skipped || 0;
    acc.removed += s.removed || 0;
    return acc;
  }, { total: 0, new: 0, updated: 0, skipped: 0, removed: 0 });

  const earliest = summaries.reduce((a, s) => (!a || (s.startedAt && new Date(s.startedAt) < a)) ? new Date(s.startedAt) : a, null);
  const latest   = summaries.reduce((a, s) => (!a || (s.finishedAt && new Date(s.finishedAt) > a)) ? new Date(s.finishedAt) : a, null);
  const dur = (earliest && latest) ? Math.max(0, Math.round((latest - earliest) / 1000)) : 0;

  head.innerHTML = `
    <div><strong>Alle Bibliotheken</strong> · ${summaries.length} Lib${summaries.length === 1 ? "" : "s"} gescannt${force ? " · force" : ""}</div>
    <div class="hint">Gesamtdauer: ${dur}s${latest ? " · " + latest.toLocaleTimeString() : ""}</div>
  `;

  // Aggregat-Stats (nicht klickbar — die Pfade sind pro Lib aufgeteilt).
  const cells = [
    { label: "🆕 Neu",          value: totals.new,     cls: "stat-new" },
    { label: "🔄 Aktualisiert", value: totals.updated, cls: "stat-upd" },
    { label: "⏭ Übersprungen", value: totals.skipped, cls: "stat-skip" },
    { label: "🗑 Entfernt",     value: totals.removed, cls: "stat-del" },
    { label: "Gesamt",          value: totals.total,   cls: "stat-total" },
  ];
  stats.innerHTML = cells.map(c => `
    <div class="scan-stat ${c.cls}">
      <div class="scan-stat-num">${c.value}</div>
      <div class="scan-stat-label">${c.label}</div>
    </div>
  `).join("");

  // Pro Lib eine Zeile in einer Tabelle. Click auf Zellen öffnet die Detail-
  // Ansicht (Pfad-Liste der jeweiligen Lib).
  folders.innerHTML = `
    <table class="scan-folder-table">
      <thead><tr><th>Bibliothek</th><th>Neu</th><th>Aktual.</th><th>Entfernt</th><th>Übersp.</th><th>Gesamt</th></tr></thead>
      <tbody>${summaries.map((s, idx) => `
        <tr data-lib-idx="${idx}">
          <td><span class="scan-folder-name">${escapeHTML(s.libraryName || "Bibliothek")}</span></td>
          <td class="scan-cell ${(s.new||0)>0?"clickable":""}" data-kind="new">${s.new || 0}</td>
          <td class="scan-cell ${(s.updated||0)>0?"clickable":""}" data-kind="updated">${s.updated || 0}</td>
          <td class="scan-cell ${(s.removed||0)>0?"clickable":""}" data-kind="removed">${s.removed || 0}</td>
          <td>${s.skipped || 0}</td>
          <td>${s.total || 0}</td>
        </tr>
      `).join("")}</tbody>
    </table>
  `;
  folders.querySelectorAll("td.scan-cell.clickable").forEach(td => {
    td.addEventListener("click", () => {
      const tr = td.closest("tr");
      const idx = Number(tr.dataset.libIdx);
      // Single-Lib-Detail-Ansicht öffnen, indem wir die globale lastScanSummary
      // temporär setzen und showScanDetail nutzen.
      state.lastScanSummary = summaries[idx];
      showScanDetail(td.dataset.kind, null);
    });
  });

  detail.classList.add("hidden");
  detail.innerHTML = "";
  $("#scanSummaryDialog").showModal();
}

function pollScan() {
  if (state.scanPoll) return;
  const bar = $("#scanStatus");
  bar.classList.remove("hidden");

  // finalize wird nach Single- oder All-Scan einmalig zur Auflösung gerufen.
  const finalize = () => {
    clearInterval(state.scanPoll);
    state.scanPoll = null;
    setTimeout(() => bar.classList.add("hidden"), 4000);
    if (state.expectingScanAll) {
      const list = state.scanAllSummaries || [];
      state.expectingScanAll = false;
      state.scanAllSummaries = null;
      state.scanAllCaptured = null;
      if (state.scanAllEndTimer) { clearTimeout(state.scanAllEndTimer); state.scanAllEndTimer = null; }
      if (list.length === 1) showScanSummary(list[0]);
      else if (list.length > 1) showScanAllSummary(list, !!state.scanAllForce);
    }
    loadItems();
  };

  state.scanPoll = setInterval(async () => {
    try {
      const st = await api("/api/scan/status");
      renderScanStatus(st);

      // Batch-Mode: zwischen zwei Libs ist Running kurz false. Nicht abbrechen,
      // sondern abwarten, ob die nächste Lib startet. Per-Lib-Summary einsammeln.
      if (state.expectingScanAll) {
        if (st.lastSummary) {
          const sig = `${st.lastSummary.libraryId}|${st.lastSummary.finishedAt}`;
          if (!state.scanAllCaptured.has(sig)) {
            state.scanAllCaptured.add(sig);
            state.scanAllSummaries.push(st.lastSummary);
          }
        }
        if (st.running) {
          // Nächste Lib läuft schon → End-Timer abbrechen falls gesetzt
          if (state.scanAllEndTimer) { clearTimeout(state.scanAllEndTimer); state.scanAllEndTimer = null; }
        } else {
          // Running=false: könnte Pause zwischen Libs sein. 6 s warten, dann
          // gilt der Batch als abgeschlossen.
          if (!state.scanAllEndTimer) {
            state.scanAllEndTimer = setTimeout(finalize, 6000);
          }
        }
        return;
      }

      // Single-Lib-Modus: alte Logik
      if (!st.running) {
        if (st.lastSummary) {
          // finalize zeigt den Single-Lib-Dialog
          clearInterval(state.scanPoll);
          state.scanPoll = null;
          setTimeout(() => bar.classList.add("hidden"), 4000);
          showScanSummary(st.lastSummary);
          loadItems();
        } else {
          clearInterval(state.scanPoll);
          state.scanPoll = null;
          setTimeout(() => bar.classList.add("hidden"), 4000);
          loadItems();
        }
      }
    } catch (e) { console.warn(e); }
  }, 1000);
}

// Beim Boot + periodisch prüfen, ob ein Scan läuft (z.B. durch anderen Client getriggert
// oder serverseitig via /api/scan/all). Wenn ja → Polling-Bar anzeigen.
async function checkScanActive() {
  try {
    const st = await api("/api/scan/status");
    if (st.running) {
      renderScanStatus(st);
      pollScan();
    }
  } catch (e) { console.warn(e); }
}

// --- Globale Trickplay-Statusleiste ---

function pollTrickplayWorker() {
  if (state.trickplayWorkerPoll) return;
  const bar = $("#trickplayStatus");
  bar.classList.remove("hidden");
  state.trickplayWorkerPoll = setInterval(async () => {
    try {
      const st = await api("/api/trickplay/status");
      renderTrickplayWorkerStatus(st);
      if (!st.running) {
        clearInterval(state.trickplayWorkerPoll);
        state.trickplayWorkerPoll = null;
        setTimeout(() => bar.classList.add("hidden"), 4000);
        loadItems();
      }
    } catch (e) { console.warn(e); }
  }, 2000);
}

function renderTrickplayWorkerStatus(st) {
  const bar = $("#trickplayStatus");
  if (!st.running) {
    bar.innerHTML = `🎞 Trickplay-Lauf fertig: ${st.processed || 0} erfolgreich, ${st.failed || 0} Fehler`;
    return;
  }
  const total = st.total || 0;
  const done = (st.processed || 0) + (st.failed || 0);
  const pct = total ? Math.round(done * 100 / total) : 0;
  const cur = st.currentTitle ? ` · ${escapeHTML(st.currentTitle)}` : "";
  bar.innerHTML = `
    <div class="statusbar-row">
      <div class="statusbar-text">🎞 Trickplay: ${st.processed || 0}/${total} verarbeitet${st.failed ? ` · ⚠ ${st.failed}` : ""}${cur}</div>
      <button class="statusbar-cancel" data-action="cancel-trickplay" title="Trickplay abbrechen">✕</button>
    </div>
    <div class="bar"><div style="width:${pct}%"></div></div>
  `;
}

async function checkTrickplayWorker() {
  try {
    const st = await api("/api/trickplay/status");
    if (st.running) {
      renderTrickplayWorkerStatus(st);
      pollTrickplayWorker();
    }
  } catch {}
}

function renderScanStatus(st) {
  const bar = $("#scanStatus");
  const mode = st.force ? " (Force)" : "";
  const scope = st.folder ? ` · Ordner „${escapeHTML(st.folder)}"` : "";
  if (!st.running && !st.lastError) {
    bar.innerHTML = `✓ Scan fertig${mode}${scope} (${st.done}/${st.total}, neu/aktualisiert: ${st.new || 0}, übersprungen: ${st.skipped || 0})`;
    return;
  }
  if (st.lastError) { bar.innerHTML = `✗ Fehler: ${escapeHTML(st.lastError)}`; return; }
  const pct = st.total ? Math.round(st.done * 100 / st.total) : 0;
  const name = st.current ? st.current.split("/").pop() : "";
  bar.innerHTML = `
    <div class="statusbar-row">
      <div class="statusbar-text">Scanne${mode}${scope}… ${st.done}/${st.total} (neu ${st.new || 0} · übersprungen ${st.skipped || 0}) – ${escapeHTML(name)}</div>
      <button class="statusbar-cancel" data-action="cancel-scan" title="Scan abbrechen">✕</button>
    </div>
    <div class="bar"><div style="width:${pct}%"></div></div>
  `;
}

// --- Manage Libraries ---

async function openManage() {
  await loadLibraries();
  const ul = $("#libList");
  ul.innerHTML = "";
  if (!state.libraries.length) {
    ul.innerHTML = `<li><em>Noch keine Bibliotheken angelegt.</em></li>`;
  }
  for (const l of state.libraries) {
    const li = document.createElement("li");
    li.classList.add("lib-row");
    const kindLabel = l.kind === "movies" ? "Filme" : l.kind === "tv" ? "Serien" : "Privat";

    // Header-Zeile
    const header = document.createElement("div");
    header.className = "lib-row-head";
    header.innerHTML = `<div><strong>${escapeHTML(l.name)}</strong> <span class="lib-kind">${kindLabel}</span></div>`;

    const actions = document.createElement("div");
    actions.style.display = "flex";
    actions.style.gap = "6px";
    const kindSel = document.createElement("select");
    for (const [k, lbl] of [["movies", "Filme"], ["tv", "Serien"], ["private", "Privat"]]) {
      const o = document.createElement("option");
      o.value = k; o.textContent = lbl;
      if (k === l.kind) o.selected = true;
      kindSel.appendChild(o);
    }
    kindSel.addEventListener("change", async () => {
      try {
        await api(`/api/libraries/${l.id}/kind`, { method: "PUT", body: JSON.stringify({ kind: kindSel.value }) });
        await loadLibraries();
      } catch (e) { appAlert(e.message); }
    });
    actions.appendChild(kindSel);
    // Startseiten-Sichtbarkeit
    const onHomeLabel = document.createElement("label");
    onHomeLabel.className = "lib-on-home";
    onHomeLabel.title = "Auf der Startseite anzeigen";
    const onHomeBox = document.createElement("input");
    onHomeBox.type = "checkbox";
    onHomeBox.checked = l.onHome !== false;
    onHomeBox.addEventListener("change", async () => {
      try {
        await api(`/api/libraries/${l.id}/home-visibility`, {
          method: "PUT",
          body: JSON.stringify({ onHome: onHomeBox.checked }),
        });
        await loadLibraries();
      } catch (e) { appAlert(e.message); }
    });
    onHomeLabel.appendChild(onHomeBox);
    onHomeLabel.appendChild(document.createTextNode(" 🏠 Startseite"));
    actions.appendChild(onHomeLabel);
    const del = document.createElement("button");
    del.textContent = "Bibliothek löschen";
    del.classList.add("danger");
    del.addEventListener("click", async () => {
      if (!(await appConfirm(`Bibliothek "${l.name}" löschen?`))) return;
      await api(`/api/libraries/${l.id}`, { method: "DELETE" });
      await loadLibraries();
      openManage();
      loadItems();
    });
    actions.appendChild(del);
    header.appendChild(actions);
    li.appendChild(header);

    // Paths-Liste
    const pathsBox = document.createElement("div");
    pathsBox.className = "lib-paths";
    pathsBox.innerHTML = `<small style="color:var(--text-dim)">Quellordner werden geladen …</small>`;
    li.appendChild(pathsBox);

    ul.appendChild(li);
    loadLibraryPaths(l, pathsBox);
  }
  $("#manageDialog").showModal();
}

async function loadLibraryPaths(lib, box) {
  let paths = [];
  try {
    paths = await api(`/api/libraries/${lib.id}/paths`);
  } catch (e) {
    box.innerHTML = `<small style="color:#ef4444">Fehler: ${escapeHTML(e.message)}</small>`;
    return;
  }
  box.innerHTML = "";
  if (!paths.length) {
    const msg = document.createElement("small");
    msg.style.color = "var(--text-dim)";
    msg.textContent = "(Keine Quellordner)";
    box.appendChild(msg);
  }
  for (const p of paths) {
    const row = document.createElement("div");
    row.className = "lib-path-row";
    row.innerHTML = `<code>${escapeHTML(p)}</code>`;
    const rm = document.createElement("button");
    rm.textContent = "Entfernen";
    rm.className = "danger";
    rm.addEventListener("click", async () => {
      if (!(await appConfirm(`Quellordner "${p}" aus "${lib.name}" entfernen?\n(Items aus diesem Pfad werden beim nächsten Scan entfernt.)`))) return;
      try {
        await api(`/api/libraries/${lib.id}/paths?path=${encodeURIComponent(p)}`, { method: "DELETE" });
        loadLibraryPaths(lib, box);
      } catch (e) { appAlert(e.message); }
    });
    row.appendChild(rm);
    box.appendChild(row);
  }
  // Add-Button
  const add = document.createElement("div");
  add.className = "lib-path-row";
  const addInput = document.createElement("input");
  addInput.type = "text";
  addInput.placeholder = "/media/Video/NochEinOrdner";
  addInput.style.flex = "1";
  const browseBtn = document.createElement("button");
  browseBtn.textContent = "Durchsuchen…";
  browseBtn.addEventListener("click", () => {
    state.browseTarget = (path) => { addInput.value = path; };
    openBrowse(addInput.value || "/media");
  });
  const addBtn = document.createElement("button");
  addBtn.textContent = "+ Hinzufügen";
  addBtn.className = "primary";
  addBtn.addEventListener("click", async () => {
    const path = addInput.value.trim();
    if (!path) return;
    try {
      await api(`/api/libraries/${lib.id}/paths`, { method: "POST", body: JSON.stringify({ path }) });
      addInput.value = "";
      loadLibraryPaths(lib, box);
    } catch (e) { appAlert(e.message); }
  });
  add.appendChild(addInput);
  add.appendChild(browseBtn);
  add.appendChild(addBtn);
  box.appendChild(add);
}

async function handleAddLibrary(e) {
  e.preventDefault();
  const form = e.target;
  const data = Object.fromEntries(new FormData(form));
  try {
    await api("/api/libraries", { method: "POST", body: JSON.stringify(data) });
    form.reset();
    await loadLibraries();
    openManage();
    loadItems();
  } catch (err) {
    appAlert("Fehler: " + err.message);
  }
}

// --- Path Browser ---

async function openBrowse(startPath) {
  await loadBrowse(startPath || "/media");
  $("#browseDialog").showModal();
}

async function loadBrowse(path) {
  try {
    const data = await api(`/api/browse?path=${encodeURIComponent(path)}`);
    state.browseAt = data.path;
    $("#browsePath").textContent = data.path;
    const ul = $("#browseList");
    ul.innerHTML = "";
    if (data.parent) {
      const li = document.createElement("li");
      li.className = "up";
      li.innerHTML = `⤴ .. (zurück)`;
      li.addEventListener("click", () => loadBrowse(data.parent));
      ul.appendChild(li);
    }
    if (!data.entries.length) {
      const li = document.createElement("li");
      li.innerHTML = `<em style="color:#6b7280">Keine Unterordner</em>`;
      ul.appendChild(li);
    }
    for (const e of data.entries) {
      const li = document.createElement("li");
      li.innerHTML = `📁 ${escapeHTML(e.name)}`;
      li.addEventListener("click", () => loadBrowse(e.path));
      ul.appendChild(li);
    }
  } catch (e) {
    appAlert("Fehler: " + e.message);
  }
}

function chooseBrowse() {
  if (state.browseTarget) {
    state.browseTarget(state.browseAt);
    state.browseTarget = null;
  } else {
    $("#libPathInput").value = state.browseAt;
  }
  $("#browseDialog").close();
}

// --- Settings ---

async function openSettings() {
  $("#bufRange").value = state.settings.bufferSeconds;
  $("#bufVal").textContent = state.settings.bufferSeconds;
  const sbs = state.settings.startBufferSeconds || 0;
  $("#startBufRange").value = sbs;
  $("#startBufVal").textContent = sbs;
  const tpi = state.settings.trickplayIntervalSec || 10;
  $("#tpInterval").value = tpi;
  $("#tpIntervalVal").textContent = tpi;
  $("#tmdbKeyInput").value = "";
  $("#omdbKeyInput").value = "";
  $("#tmdbStatus").innerHTML = state.settings.tmdbConfigured
    ? "✓ TMDB-Key konfiguriert"
    : "⚠ Noch kein TMDB-Key gesetzt – ohne Key keine Filmposter/Beschreibungen.";
  $("#omdbStatus").innerHTML = state.settings.omdbConfigured
    ? "✓ OMDb-Key konfiguriert (aktiv als Fallback)"
    : "⚠ Kein OMDb-Key – Fallback deaktiviert (optional).";
  const hw = state.hwaccel || {};
  // Mode-Dropdown auf gespeicherten Wert setzen (Default: auto)
  $("#hwaccelMode").value = state.settings.hwaccelMode || "auto";
  // Statuszeile zeigt detektierte Backends + aktiv genutzten
  const lines = [];
  lines.push(`Aktiv: <strong>${escapeHTML(hw.backend || "software")}</strong>`);
  if (hw.vaapiAvailable) {
    lines.push(`✓ VAAPI verfügbar (${escapeHTML(hw.vaapiDriver || hw.device || "Intel/AMD GPU")})`);
  } else {
    lines.push(`✗ VAAPI nicht erkannt — <code>/dev/dri</code> in den Container durchreichen`);
  }
  if (hw.nvencAvailable) {
    lines.push(`✓ NVENC verfügbar (${escapeHTML(hw.nvencInfo || "NVIDIA GPU")})`);
  } else {
    lines.push(`✗ NVENC nicht erkannt — NVIDIA-Plugin auf dem Host + <code>runtime: nvidia</code> nötig`);
  }
  $("#hwaccelInfo").innerHTML = lines.join("<br>");
  updateEnrichStatus();
  $("#settingsDialog").showModal();
}

async function saveSettings(e) {
  e.preventDefault();
  const body = {
    bufferSeconds: parseInt($("#bufRange").value, 10),
    startBufferSeconds: parseInt($("#startBufRange").value, 10) || 0,
    trickplayIntervalSec: parseInt($("#tpInterval").value, 10),
    hwaccelMode: $("#hwaccelMode").value || "auto",
  };
  const tmdbKey = $("#tmdbKeyInput").value.trim();
  if (tmdbKey) body.tmdbKey = tmdbKey;
  const omdbKey = $("#omdbKeyInput").value.trim();
  if (omdbKey) body.omdbKey = omdbKey;
  try {
    const res = await api("/api/settings", { method: "PUT", body: JSON.stringify(body) });
    state.settings = res;
    // Health neu laden, damit hwaccel.backend (geändert durch Settings) frisch ist
    try { await loadHealth(); } catch {}
    $("#settingsDialog").close();
  } catch (err) {
    appAlert("Fehler: " + err.message);
  }
}

async function clearTMDBKey() {
  if (!(await appConfirm("TMDB-Key wirklich löschen?"))) return;
  try {
    await api("/api/settings", { method: "PUT", body: JSON.stringify({ bufferSeconds: state.settings.bufferSeconds, tmdbKey: "__clear__" }) });
    state.settings.tmdbConfigured = false;
    $("#tmdbStatus").innerHTML = "⚠ TMDB-Key gelöscht.";
  } catch (e) { appAlert(e.message); }
}

async function clearOMDbKey() {
  if (!(await appConfirm("OMDb-Key wirklich löschen?"))) return;
  try {
    await api("/api/settings", { method: "PUT", body: JSON.stringify({ bufferSeconds: state.settings.bufferSeconds, omdbKey: "__clear__" }) });
    state.settings.omdbConfigured = false;
    $("#omdbStatus").innerHTML = "⚠ OMDb-Key gelöscht.";
  } catch (e) { appAlert(e.message); }
}

async function runEnrich() {
  try {
    await api("/api/enrich/run", { method: "POST" });
    updateEnrichStatus();
  } catch (e) { appAlert(e.message); }
}

async function updateEnrichStatus() {
  try {
    const s = await api("/api/enrich/status");
    if (!s || !s.running) {
      let txt = s && s.lastRun ? `Letzter Lauf: ${fmtDate(s.lastRun)} ${new Date(s.lastRun).toLocaleTimeString()}` : "Noch kein Lauf";
      if (s && s.itemsMatched > 0) txt += ` · ${s.itemsMatched} Items gematcht`;
      if (s && s.lastError) txt += ` · ⚠ ${s.lastError}`;
      $("#enrichStatus").textContent = txt;
    } else {
      const pct = s.itemsTotal > 0 ? Math.round((s.itemsMatched + s.itemsFailed) * 100 / s.itemsTotal) : 0;
      $("#enrichStatus").textContent = `Läuft… ${s.itemsMatched + s.itemsFailed}/${s.itemsTotal} (${pct}%) · Ordner: ${s.foldersMatched}/${s.foldersTotal}`;
      setTimeout(updateEnrichStatus, 2000);
    }
  } catch (e) {
    $("#enrichStatus").textContent = "";
  }
}

// --- Manuelles Matching ---

// openEditMetaDialog füllt das Formular mit den aktuellen Metadata-Werten
// des geöffneten Items und öffnet den Dialog. Admin-only — der Button ist
// für Non-Admins per JS ausgeblendet.
function openEditMetaDialog() {
  const it = state.currentItem;
  if (!it) return;
  const f = $("#editMetaForm");
  const isNew = !it.metadataId;
  if (isNew) {
    // Manuelles Anlegen: Vorbefüllung aus dem Filename, damit der User nicht
    // bei null anfangen muss. Show-Name aus rel_path[0], Titel = Filename
    // ohne Release-Junk (Best-Effort), Episodencode bleibt im Beschreibungstext.
    const rel = (it.relPath || "").split("/");
    const showName = rel.length > 1 ? rel[0] : "";
    const m = (it.title || "").match(/[Ss](\d{1,2})[Ee](\d{1,3})/);
    f.title.value = showName || it.title || "";
    f.originalTitle.value = "";
    f.year.value = "";
    f.releaseDate.value = "";
    f.overview.value = m ? `S${m[1].padStart(2,"0")}E${m[2].padStart(2,"0")}` : "";
    f.rating.value = 0;
    f.runtimeMin.value = it.durationSec ? Math.round(it.durationSec / 60) : 0;
    f.genres.value = "";
    f.ageRating.value = "";
    $("#editMetaDialog").querySelector("h2").textContent = "Metadaten manuell anlegen";
  } else {
    const m = it.metadata || {};
    f.title.value = m.title || "";
    f.originalTitle.value = m.originalTitle || "";
    f.year.value = m.year || "";
    f.releaseDate.value = (m.releaseDate || "").slice(0, 10);
    f.overview.value = m.overview || "";
    f.rating.value = m.rating || 0;
    f.runtimeMin.value = m.runtimeMin || 0;
    f.genres.value = m.genres || "";
    f.ageRating.value = m.ageRating || "";
    $("#editMetaDialog").querySelector("h2").textContent = "Metadaten bearbeiten";
  }
  // Poster-Button für ungematched Items ausblenden — Poster geht ueber metadata.id
  // und braucht TMDB-Lookup, der bei custom-Metadata nichts liefert.
  const posterBtn = $("#editMetaPoster");
  if (posterBtn) posterBtn.style.display = isNew ? "none" : "";
  $("#editMetaDialog").showModal();
}

// openPosterPicker zeigt ein Grid mit verfügbaren TMDB-Postern + ein Upload-
// Formular für ein eigenes Bild. Beide Wege rufen denselben Endpoint, der
// das Poster im posters-Cache ablegt und metadata.poster_path aktualisiert.
async function openPosterPicker() {
  const it = state.currentItem;
  if (!it || !it.metadataId) return;
  const dlg = $("#posterPickerDialog");
  const grid = $("#posterPickerGrid");
  const status = $("#posterPickerStatus");
  grid.innerHTML = "";
  status.textContent = "Lade TMDB-Poster…";
  dlg.showModal();
  try {
    const list = await api(`/api/metadata/${it.metadataId}/posters`);
    if (!Array.isArray(list) || list.length === 0) {
      status.textContent = "TMDB hat keine Poster — nutze den Upload unten.";
      return;
    }
    status.textContent = `${list.length} Varianten gefunden — DE bevorzugt, dann nach TMDB-Rating sortiert.`;
    for (const p of list) {
      const tile = document.createElement("button");
      tile.type = "button";
      tile.className = "poster-picker-tile";
      const langTag = p.language === "de" ? "DE" : (p.language === "en" ? "EN" : (p.language === "" ? "—" : (p.language || "—").toUpperCase()));
      tile.innerHTML = `
        <img src="https://image.tmdb.org/t/p/w185${p.filePath}" loading="lazy" alt="">
        <div class="poster-picker-meta">
          <span class="poster-lang">${langTag}</span>
          ${p.voteAverage ? `<span class="poster-vote">★ ${p.voteAverage.toFixed(1)}</span>` : ""}
          <span class="poster-dim">${p.width}×${p.height}</span>
        </div>
      `;
      tile.addEventListener("click", () => applyTMDBPoster(it.metadataId, p.filePath));
      grid.appendChild(tile);
    }
  } catch (e) {
    status.textContent = "Fehler: " + e.message;
  }
}

async function applyTMDBPoster(metaID, tmdbPath) {
  try {
    await api(`/api/metadata/${metaID}/poster`, {
      method: "POST",
      body: JSON.stringify({ tmdbPath }),
    });
    showToast("Poster aktualisiert", { kind: "success" });
    $("#posterPickerDialog").close();
    invalidateItemsCache();
    // Cache-Buster für die <img>-URLs: einfach Detail-Dialog mit frischem
    // Item neu öffnen — der Browser holt das Bild dann frisch.
    if (state.currentItem) {
      try {
        const fresh = await api(`/api/items/${state.currentItem.id}`);
        state.currentItem = fresh;
        $("#detailDialog").close();
        openDetail(fresh);
      } catch {}
    }
    loadItems();
  } catch (e) {
    appAlert("Fehler: " + e.message);
  }
}

async function handlePosterUpload(e) {
  e.preventDefault();
  const it = state.currentItem;
  if (!it || !it.metadataId) return;
  const form = e.target;
  const file = form.file.files[0];
  if (!file) return;
  const fd = new FormData();
  fd.append("file", file);
  try {
    // Direct fetch — `api()`-Helper setzt JSON-Content-Type, das brauchen wir
    // hier nicht. Cookie-Auth läuft mit credentials:include.
    const r = await fetch(`/api/metadata/${it.metadataId}/poster`, {
      method: "POST",
      body: fd,
      credentials: "include",
    });
    if (!r.ok) {
      const t = await r.text();
      throw new Error(t || `HTTP ${r.status}`);
    }
    showToast("Poster hochgeladen", { kind: "success" });
    form.reset();
    $("#posterPickerDialog").close();
    invalidateItemsCache();
    if (state.currentItem) {
      try {
        const fresh = await api(`/api/items/${state.currentItem.id}`);
        state.currentItem = fresh;
        $("#detailDialog").close();
        openDetail(fresh);
      } catch {}
    }
    loadItems();
  } catch (err) {
    appAlert("Upload fehlgeschlagen: " + err.message);
  }
}

async function handleEditMetaSubmit(e) {
  e.preventDefault();
  const it = state.currentItem;
  if (!it) return;
  const f = e.target;
  const body = {
    title: f.title.value.trim(),
    originalTitle: f.originalTitle.value.trim(),
    year: parseInt(f.year.value, 10) || 0,
    releaseDate: f.releaseDate.value,
    overview: f.overview.value,
    rating: parseFloat(f.rating.value) || 0,
    runtimeMin: parseInt(f.runtimeMin.value, 10) || 0,
    genres: f.genres.value.trim(),
    ageRating: f.ageRating.value,
  };
  try {
    if (it.metadataId) {
      await api(`/api/metadata/${it.metadataId}`, {
        method: "PUT",
        body: JSON.stringify(body),
      });
    } else {
      await api(`/api/items/${it.id}/metadata-manual`, {
        method: "POST",
        body: JSON.stringify(body),
      });
    }
    $("#editMetaDialog").close();
    invalidateItemsCache();
    // Frisch geladenes Item im Detail-Dialog anzeigen, damit die neuen Werte
    // sofort sichtbar sind.
    try {
      const fresh = await api(`/api/items/${it.id}`);
      state.currentItem = fresh;
      openDetail(fresh);
    } catch {}
    loadItems();
    showToast("Metadaten gespeichert", { kind: "success" });
  } catch (err) {
    appAlert("Fehler: " + err.message);
  }
}

function openMatchItem(item) {
  state.matchTarget = { type: "item", itemId: item.id, libraryKind: null };
  const lib = state.libraries.find(l => l.id == item.libraryId);
  state.matchTarget.libraryKind = lib ? lib.kind : "movies";
  $("#matchTitle").textContent = `TMDB-Suche: ${item.title}`;
  const parsed = parseTitle(item.title);
  $("#matchQuery").value = parsed.title || item.title;
  $("#matchYear").value = parsed.year || "";
  $("#matchResults").innerHTML = "";
  // S/E-Felder: nur bei TV-Items relevant. Aus Dateiname (rel_path / title)
  // SxxExx parsen und vorbefüllen — User kann manuell korrigieren, wenn
  // der TMDB-Episoden-Lookup 404 wirft (z. B. abweichende Staffel-Aufteilung).
  const seWrap = $("#matchEpisodeWrap");
  const seHint = $("#matchEpisodeHint");
  if (state.matchTarget.libraryKind === "tv") {
    seWrap.classList.remove("hidden");
    seHint.style.display = "";
    const src = item.relPath || item.title || "";
    const m = src.match(/S(\d{1,2})\s*E(\d{1,3})/i) || src.match(/(\d{1,2})x(\d{1,3})/i);
    $("#matchSeason").value = m ? parseInt(m[1], 10) : "";
    $("#matchEpisode").value = m ? parseInt(m[2], 10) : "";
  } else {
    seWrap.classList.add("hidden");
    seHint.style.display = "none";
    $("#matchSeason").value = "";
    $("#matchEpisode").value = "";
  }
  $("#matchDialog").showModal();
}

function openMatchFolder(libraryId, folderName) {
  state.matchTarget = { type: "folder", libraryId, folder: folderName };
  $("#matchTitle").textContent = `TMDB-Suche (Serie): ${folderName}`;
  const parsed = parseTitle(folderName);
  $("#matchQuery").value = parsed.title || folderName;
  $("#matchYear").value = parsed.year || "";
  $("#matchResults").innerHTML = "";
  // Folder-Match ist Show-Level, keine S/E-Eingabe nötig.
  $("#matchEpisodeWrap").classList.add("hidden");
  $("#matchEpisodeHint").style.display = "none";
  $("#matchDialog").showModal();
}

// Einfacher Titel-/Jahr-Parser (dupliziert Logik des Backends, nur fürs UI).
function parseTitle(name) {
  const s = String(name).replace(/\.(mkv|mp4|avi|wmv|mov)$/i, "");
  const m = s.match(/(19|20)\d{2}/);
  let year = m ? parseInt(m[0], 10) : 0;
  let title = s;
  if (m) title = s.substring(0, m.index);
  title = title.replace(/[._]+/g, " ").replace(/[()\[\]]/g, " ").replace(/\s+/g, " ").trim();
  return { title, year };
}

async function handleMatchImdb(e) {
  e.preventDefault();
  const imdbId = $("#matchImdb").value.trim();
  if (!/^tt\d+$/.test(imdbId)) {
    appAlert("Ungültige IMDb-ID. Format: tt1234567");
    return;
  }
  const tgt = state.matchTarget;
  try {
    if (tgt.type === "folder") {
      await api(`/api/libraries/${tgt.libraryId}/folders/metadata`, {
        method: "POST",
        body: JSON.stringify({ folder: tgt.folder, imdbId }),
      });
    } else {
      // Item: IMDb-Typ ("movie" oder "episode" anhand Lib-Kind + Dateiname)
      const lib = state.libraries.find(l => l.id == state.matchTarget.libraryKind ? state.matchTarget.libraryKind : null) || null;
      const kind = tgt.libraryKind;
      if (kind === "movies") {
        await api(`/api/items/${tgt.itemId}/metadata`, {
          method: "POST",
          body: JSON.stringify({ tmdbType: "movie", imdbId }),
        });
      } else {
        const item = state.currentItem;
        const m = (item && item.title || "").match(/S(\d{1,2})E(\d{1,3})/i);
        if (!m) {
          appAlert("Konnte Staffel/Episode aus Dateiname nicht ermitteln.");
          return;
        }
        await api(`/api/items/${tgt.itemId}/metadata`, {
          method: "POST",
          body: JSON.stringify({
            tmdbType: "episode",
            imdbId, // IMDb der SHOW, nicht der Episode
            season: parseInt(m[1], 10),
            episode: parseInt(m[2], 10),
          }),
        });
      }
    }
    $("#matchDialog").close();
    invalidateItemsCache();
    // Nach manueller IMDb-Zuordnung: Detail-Dialog mit frisch gelesenem Item
    // öffnen — analog zum TMDB-Match-Pfad (applyMatch). User sieht sofort
    // Poster, Plot, Cast und kann verifizieren oder abspielen. Folder-Match
    // (Show via IMDb) öffnet keinen Dialog (kein einzelnes Item im Fokus).
    if (tgt.type !== "folder" && tgt.itemId) {
      try {
        const fresh = await api(`/api/items/${tgt.itemId}`);
        try { $("#detailDialog").close(); } catch {}
        openDetail(fresh);
      } catch { /* fallthrough zu loadItems */ }
    }
    loadItems();
  } catch (err) {
    appAlert("Fehler: " + err.message);
  }
}

async function handleMatchSearch(e) {
  e.preventDefault();
  const q = $("#matchQuery").value.trim();
  const y = $("#matchYear").value.trim();
  if (!q) return;
  const type = (state.matchTarget.type === "folder" || state.matchTarget.libraryKind === "tv") ? "tv" : "movie";
  const params = new URLSearchParams({ q, type });
  if (y && type === "movie") params.set("year", y);
  try {
    const results = await api(`/api/metadata/search?${params}`);
    renderMatchResults(results, type);
  } catch (err) {
    appAlert(err.message);
  }
}

function renderMatchResults(results, type) {
  const ul = $("#matchResults");
  ul.innerHTML = "";
  if (!results.length) {
    ul.innerHTML = `<li><em>Keine Treffer</em></li>`;
    return;
  }
  for (const r of results.slice(0, 20)) {
    const li = document.createElement("li");
    const posterBg = r.posterPath
      ? `background-image:url('https://image.tmdb.org/t/p/w92${r.posterPath}')`
      : "";
    li.innerHTML = `
      <div class="mini" style="${posterBg}"></div>
      <div class="meta">
        <strong>${escapeHTML(r.title)}</strong>${r.year ? ` (${r.year})` : ""}
        <p>${escapeHTML(r.overview || "")}</p>
      </div>
    `;
    li.addEventListener("click", () => applyMatch(r, type));
    ul.appendChild(li);
  }
}

async function applyMatch(result, type) {
  const tgt = state.matchTarget;
  try {
    if (tgt.type === "folder") {
      await api(`/api/libraries/${tgt.libraryId}/folders/metadata`, {
        method: "POST",
        body: JSON.stringify({ folder: tgt.folder, tmdbId: result.id }),
      });
    } else {
      // Item: für Filme direkt zuordnen; für Episoden braucht's Season/Episode aus dem Dateinamen.
      if (type === "movie") {
        await api(`/api/items/${tgt.itemId}/metadata`, {
          method: "POST",
          body: JSON.stringify({ tmdbType: "movie", tmdbId: result.id }),
        });
      } else {
        // Staffel/Episode aus den (vorbefüllten) Eingabefeldern lesen — der
        // User kann sie hier korrigieren, falls der Dateiname-Parser daneben
        // liegt oder TMDB anders aufteilt (z.B. „S01E10" im Releasenamen,
        // aber TMDB hat S1=5 Episoden + S2=5 Episoden).
        const seasonRaw = $("#matchSeason").value;
        const episodeRaw = $("#matchEpisode").value;
        const season = parseInt(seasonRaw, 10);
        const episode = parseInt(episodeRaw, 10);
        if (!Number.isFinite(season) || !Number.isFinite(episode)) {
          appAlert("Bitte Staffel und Episode angeben.");
          return;
        }
        await api(`/api/items/${tgt.itemId}/metadata`, {
          method: "POST",
          body: JSON.stringify({
            tmdbType: "episode",
            tmdbId: result.id, // TV-Show-ID
            season,
            episode,
          }),
        });
      }
    }
    $("#matchDialog").close();
    invalidateItemsCache();
    // Nach manueller/TMDB-Zuordnung: Detail-Dialog mit frisch gelesenem Item
    // öffnen — User sieht sofort Poster, Plot, Cast und kann verifizieren
    // oder abspielen. Für Folder-Match (Show-Zuordnung) gibt's kein
    // Einzel-Item → nur Reload.
    if (tgt.type !== "folder" && tgt.itemId) {
      try {
        const fresh = await api(`/api/items/${tgt.itemId}`);
        try { $("#detailDialog").close(); } catch {}
        openDetail(fresh);
      } catch { /* fallthrough zu loadItems */ }
    }
    loadItems();
  } catch (e) {
    appAlert(e.message);
  }
}

// --- Boot ---

async function loadHealth() {
  try {
    const h = await api("/api/health");
    if (h.hwaccel) state.hwaccel = h.hwaccel;
  } catch {}
}

async function loadSettings() {
  try { state.settings = await api("/api/settings"); } catch {}
}

function wire() {
  $("#librarySelect").addEventListener("change", (e) => {
    const val = e.target.value || "";
    state.currentFolder = null;
    state.currentFolderDrilldown = false;
    // Beim Library-Wechsel alle Filter + Suche zurücksetzen, damit nicht ein
    // Filter aus der alten Bibliothek den neuen Katalog versteckt.
    resetFilters();
    state.collectionsView = false;
    state.currentCollection = null;
    state.playlistsView = false;
    state.homeView = false;
    if (val.startsWith("pl:")) {
      const pid = Number(val.slice(3)) || null;
      if (pid) enterPlaylist(pid); else state.currentPlaylist = null;
      state.currentLibrary = null;
    } else if (val.startsWith("lib:")) {
      state.currentLibrary = Number(val.slice(4)) || null;
      state.currentPlaylist = null;
    }
    // "col:" ist raus aus dem Dropdown — Sammlungen haben jetzt einen eigenen
    // Topbar-Button (#collectionsBtn) neben Home und Playlists.
    loadItems();
  });
  $("#searchInput").addEventListener("input", debounce(loadItems, 200));
  $("#searchClear").addEventListener("click", () => {
    $("#searchInput").value = "";
    $("#searchInput").focus();
    loadItems();
  });
  $("#sortSelect").addEventListener("change", () => {
    state.sortDir = "";
    updateSortDirIcon();
    persistSortForContext();
    loadItems();
  });
  $("#sortDirBtn").addEventListener("click", () => {
    const current = effectiveSortDir();
    state.sortDir = current === "asc" ? "desc" : "asc";
    updateSortDirIcon();
    persistSortForContext();
    loadItems();
  });
  updateSortDirIcon();
  $("#watchedFilter").addEventListener("change", loadItems);
  setupResolutionDropdown();
  $("#flatViewBtn").addEventListener("click", () => {
    state.flatView = !state.flatView;
    $("#flatViewBtn").classList.toggle("active", state.flatView);
    try { localStorage.setItem("flatView", state.flatView ? "1" : "0"); } catch {}
    loadItems();
  });
  $("#seasonViewBtn").addEventListener("click", () => {
    // Toggle je nach Kontext:
    //  - In Show-Ordner: per-Folder-Override setzen (0/1), explizit
    //  - In Library-Root: library-weiter Default
    const next = !seasonViewEffective();
    try {
      if (state.currentFolder) {
        // pro-Serie-Override: explizit "1" oder "0" (nicht entfernen —
        // sonst greift wieder der Library-Default)
        localStorage.setItem(`seasonView:${state.currentLibrary || 0}:${state.currentFolder}`, next ? "1" : "0");
      } else {
        localStorage.setItem(`seasonView:lib:${state.currentLibrary || 0}`, next ? "1" : "0");
      }
    } catch {}
    state.seasonView = next;
    state.currentSeason = null;
    $("#seasonViewBtn").classList.toggle("active", state.seasonView);
    loadItems();
  });
  $("#selectModeBtn").addEventListener("click", () => setSelectionMode(!state.selectionMode));
  $("#bulkSelectAll").addEventListener("click", selectAllVisible);
  $("#bulkSelectNone").addEventListener("click", () => setSelectionMode(false));
  $("#bulkFavorite").addEventListener("click", bulkSetFavorite);
  $("#bulkWatched").addEventListener("click", bulkSetWatched);
  $("#bulkPlaylist").addEventListener("click", bulkAddToPlaylist);
  // Merge-Aktion lebt jetzt im Zahnrad-Menü (data-action="merge"),
  // der alte Topbar-Button wurde entfernt.
  $("#bulkDownload").addEventListener("click", bulkDownload);
  $("#bulkDelete").addEventListener("click", bulkDelete);

  // Cancel-Buttons in den Status-Bars (delegiert, weil sie per innerHTML
  // rerendert werden)
  $("#scanStatus").addEventListener("click", (e) => {
    if (e.target.closest('[data-action="cancel-scan"]')) cancelScanRun();
  });
  $("#trickplayStatus").addEventListener("click", (e) => {
    if (e.target.closest('[data-action="cancel-trickplay"]')) cancelTrickplayRun();
  });

  // Trickplay-Manager-Tabs + Delete-All
  document.querySelectorAll("#trickplayDialog [data-tp-tab]").forEach(btn => {
    btn.addEventListener("click", () => {
      state.tpTab = btn.dataset.tpTab;
      refreshTrickplayManager();
    });
  });
  $("#tpDeleteAll").addEventListener("click", deleteAllTrickplay);
  $("#tpRetryFailed").addEventListener("click", retryFailedTrickplay);
  const tpShowFailed = $("#tpShowFailed");
  if (tpShowFailed) tpShowFailed.addEventListener("click", openTrickplayFailedView);
  $("#shuffleBtn").addEventListener("click", playRandom);
  $("#scanBtn").addEventListener("click", () => {
    // Inside einem Ordner: Default ist ordner-gescopt. Am Library-Root: gesamte Library.
    startScan(state.currentFolder ? "folder" : "incremental");
  });
  $("#scanMenuBtn").addEventListener("click", (e) => {
    e.stopPropagation();
    // Ordner-spezifische Einträge nur sichtbar, wenn wir in einem Ordner sind
    const inFolder = !!state.currentFolder;
    $("#scanMenu").querySelectorAll('[data-scope="folder"]').forEach(b => {
      b.style.display = inFolder ? "" : "none";
      if (inFolder) {
        const f = state.currentFolder.split("/").pop();
        if (b.dataset.scan === "folder") b.textContent = `„${f}" (inkrementell)`;
        else b.textContent = `„${f}" (force)`;
      }
    });
    $("#scanMenu").classList.toggle("hidden");
  });
  $("#scanMenu").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-scan]");
    if (!btn) return;
    $("#scanMenu").classList.add("hidden");
    startScan(btn.dataset.scan);
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#scanMenu, #scanMenuBtn")) {
      $("#scanMenu").classList.add("hidden");
    }
  });
  // Einstellungs-Dropdown: vereint Settings / Libraries / Users (admin)
  $("#settingsBtn").addEventListener("click", (e) => {
    e.stopPropagation();
    $("#settingsMenu").classList.toggle("hidden");
  });
  $("#settingsMenu").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-action]");
    if (!btn) return;
    $("#settingsMenu").classList.add("hidden");
    switch (btn.dataset.action) {
      case "settings":  openSettings(); break;
      case "libraries": openManage(); break;
      case "users":     openUsersManager(); break;
      case "trickplay": openTrickplayManager(); break;
      case "pathsearch": openPathSearch(); break;
      case "missing": openMissingDialog(); break;
      case "refreshallmeta": runRefreshAllMetadata(); break;
    }
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#settingsMenu, #settingsBtn")) {
      $("#settingsMenu").classList.add("hidden");
    }
  });
  // Home/Sammlungen/Playlists/Library-Wechsel laufen über die pinned Lib-Nav
  // (renderLibNav). Die alten Topbar-Icon-Buttons sind raus.
  $("#playlistForm").addEventListener("submit", handleNewPlaylist);
  $("#userForm").addEventListener("submit", handleNewUser);
  $("#passwordForm").addEventListener("submit", handleMyPassword);
  $("#detailAddPlaylist").addEventListener("click", openAddToPlaylist);
  $("#quickCreatePlaylistForm").addEventListener("submit", handleQuickCreatePlaylist);
  $("#libForm").addEventListener("submit", handleAddLibrary);
  $("#browseBtn").addEventListener("click", () => openBrowse($("#libPathInput").value || "/media"));
  $("#browseChoose").addEventListener("click", chooseBrowse);
  $("#settingsForm").addEventListener("submit", saveSettings);
  $("#bufRange").addEventListener("input", (e) => { $("#bufVal").textContent = e.target.value; });
  $("#startBufRange").addEventListener("input", (e) => { $("#startBufVal").textContent = e.target.value; });
  $("#tpInterval").addEventListener("input", (e) => { $("#tpIntervalVal").textContent = e.target.value; });
  $("#playerClose").addEventListener("click", closePlayer);
  $("#playerDialog").addEventListener("close", () => {
    // Letzte Position merken, bevor der Player verworfen wird.
    if (state.vjs && state.currentItem) {
      try {
        const cur = state.vjs.currentTime();
        if (isFinite(cur) && cur >= 5) {
          const abs = cur + ((state.playback && state.playback.virtualOffset) || 0);
          api(`/api/items/${state.currentItem.id}/resume`, {
            method: "PUT",
            body: JSON.stringify({ positionSec: abs }),
          }).catch(() => {});
        }
      } catch {}
    }
    disposePlayer();
  });
  setupDialogDrag();
  $("#modeSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value || "orig";
    applyPlayback(state.currentItem, mode, profile);
  });
  $("#profileSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value;
    const audio = $("#audioSelect").value;
    applyPlayback(state.currentItem, mode, profile, audio ? Number(audio) : null);
  });
  $("#audioSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value;
    const audio = $("#audioSelect").value;
    applyPlayback(state.currentItem, mode, profile, audio ? Number(audio) : null);
  });
  $("#deinterlaceSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value;
    const audio = $("#audioSelect").value;
    const dei = $("#deinterlaceSelect").value;
    applyPlayback(state.currentItem, mode, profile, audio ? Number(audio) : null, dei);
  });
  $("#subSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    // Untertitel wechseln — Player selbst bleibt, nur Text-Track umschalten
    const vjs = state.vjs;
    if (!vjs) return;
    // alle Remote-Tracks entfernen, dann ggf. den gewählten neu setzen
    const existing = vjs.remoteTextTracks();
    for (let i = existing.length - 1; i >= 0; i--) {
      vjs.removeRemoteTextTrack(existing[i]);
    }
    const choice = $("#subSelect").value;
    if (choice) {
      const streams = (state.playback && state.playback.streams) || [];
      const sub = streams.find(s => String(s.index) === choice);
      const label = (sub && sub.title) || (sub && sub.language && sub.language.toUpperCase()) || "Untertitel";
      vjs.addRemoteTextTrack({
        kind: "subtitles",
        src: `/api/subtitle/${state.currentItem.id}/${choice}.vtt`,
        srclang: (sub && sub.language) || "und",
        label: label,
        default: true,
      }, false);
    }
  });
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-close]");
    if (btn) btn.closest("dialog").close();
  });
  // Detail-Dialog-Buttons
  $("#detailPlay").addEventListener("click", () => {
    $("#detailDialog").close();
    if (state.currentItem) openPlayer(state.currentItem);
  });
  $("#detailMatch").addEventListener("click", () => {
    if (state.currentItem) {
      $("#detailDialog").close();
      openMatchItem(state.currentItem);
    }
  });
  $("#detailEditMeta").addEventListener("click", openEditMetaDialog);
  $("#editMetaForm").addEventListener("submit", handleEditMetaSubmit);
  $("#editMetaPoster").addEventListener("click", openPosterPicker);
  $("#posterUploadForm").addEventListener("submit", handlePosterUpload);
  $("#detailConfirm").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const next = !state.currentItem.metadataConfirmed;
    try {
      await api(`/api/items/${state.currentItem.id}/confirm`, {
        method: "PUT",
        body: JSON.stringify({ confirmed: next }),
      });
      state.currentItem.metadataConfirmed = next;
      updateConfirmBtn();
    } catch (e) { appAlert("Fehler: " + e.message); }
  });
  $("#detailRefreshMeta").addEventListener("click", async () => {
    if (!state.currentItem) return;
    if (!state.currentItem.metadataId) {
      appAlert("Dieses Item hat keine TMDB-Zuordnung. Nutze zuerst „🔍 Manuell zuordnen\".");
      return;
    }
    const btn = $("#detailRefreshMeta");
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = "⏳";
    try {
      await api(`/api/items/${state.currentItem.id}/refresh-metadata`, { method: "POST" });
      // Item neu laden, damit Plot/Genres/Runtime/Cast/Poster aktualisiert sind
      const fresh = await api(`/api/items/${state.currentItem.id}`);
      invalidateItemsCache();
      openDetail(fresh);
      showToast("Metadaten frisch geladen", { kind: "success" });
    } catch (e) {
      appAlert("Fehler beim Neuladen: " + e.message);
    } finally {
      btn.disabled = false;
      btn.textContent = orig;
    }
  });
  $("#detailNFO").addEventListener("click", async () => {
    if (!state.currentItem) return;
    if (!(await appConfirm(
      "Eine .nfo-Datei neben der Videodatei schreiben?\n\n" +
      "Plex und Jellyfin nutzen diese Datei als Metadaten-Override — die " +
      "Videodatei selbst wird nicht verändert, nur eine Sidecar-Datei " +
      "angelegt.\n\nFunktion nur für Items, deren Zuordnung du bestätigt hast."
    ))) return;
    try {
      const res = await api(`/api/items/${state.currentItem.id}/write-nfo`, { method: "POST" });
      const list = (res.written || []).map(p => "• " + p).join("\n");
      appAlert("NFO geschrieben:\n\n" + (list || "(keine Dateien)"));
    } catch (e) { appAlert("Fehler: " + e.message); }
  });
  $("#detailDownload").addEventListener("click", () => {
    if (!state.currentItem) return;
    // Browser-Download via Location-Change (Cookie wird mitgeschickt)
    window.location.href = `/api/download/${state.currentItem.id}`;
  });
  $("#detailDelete").addEventListener("click", async () => {
    const it = state.currentItem;
    if (!it) return;
    const name = (it.metadata && it.metadata.title) || it.title;
    if (!(await appConfirm(`Datei "${name}" UNWIEDERRUFLICH vom Server löschen?\n\nPfad: ${it.path}\n\nDies kann nicht rückgängig gemacht werden.`))) return;
    if (!(await appConfirm(`Wirklich sicher? Die Datei wird für IMMER gelöscht.`))) return;
    try {
      await api(`/api/items/${it.id}`, { method: "DELETE" });
      $("#detailDialog").close();
      loadItems();
    } catch (e) { appAlert(e.message); }
  });
  $("#detailWatched").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const it = state.currentItem;
    const newState = !it.watched;
    try {
      await api(`/api/items/${it.id}/watched`, { method: "PUT", body: JSON.stringify({ watched: newState }) });
      it.watched = newState;
      updateDetailWatchedBtn();
      loadItems();
    } catch (e) { appAlert(e.message); }
  });
  $("#detailFavorite").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const it = state.currentItem;
    const newState = !it.favorite;
    try {
      await api(`/api/items/${it.id}/favorite`, { method: "PUT", body: JSON.stringify({ favorite: newState }) });
      it.favorite = newState;
      updateDetailFavBtn();
      loadItems();
    } catch (e) { appAlert(e.message); }
  });
  $("#matchImdbForm").addEventListener("submit", handleMatchImdb);
  // Match-Dialog
  $("#matchForm").addEventListener("submit", handleMatchSearch);
  // TMDB Settings
  $("#tmdbRunBtn").addEventListener("click", runEnrich);
  $("#tmdbClearBtn").addEventListener("click", clearTMDBKey);
  $("#omdbClearBtn").addEventListener("click", clearOMDbKey);
}

function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

async function checkAuth() {
  try {
    const r = await fetch("/api/auth/status");
    const d = await r.json();
    if (d.setup || !d.loggedIn) {
      location.href = "/login.html";
      return false;
    }
    state.me = { username: d.username, isAdmin: d.isAdmin };
    return true;
  } catch {
    location.href = "/login.html";
    return false;
  }
}

(async function boot() {
  if (!await checkAuth()) return;
  try { state.flatView = localStorage.getItem("flatView") === "1"; } catch {}
  wire();
  // Alphabet-Leiste automatisch nach jedem Grid-Render aktualisieren.
  const gridEl = $("#grid");
  if (gridEl && window.MutationObserver) {
    new MutationObserver(() => updateAlphaSidebar()).observe(gridEl, { childList: true });
  }
  // Anfangsbuchstaben-Filter: ✕-Button im Banner hebt den Filter wieder auf.
  const afClose = $("#alphaFilterClose");
  if (afClose) afClose.addEventListener("click", () => setAlphaFilter(null));
  // Topbar ist fixed → Body braucht ein padding-top in Topbar-Höhe. Höhe
  // ändert sich beim Wrapping (viele Buttons → 2 Zeilen), deshalb live
  // nachführen.
  const topbar = document.querySelector(".topbar");
  if (topbar) {
    const syncTop = () => document.documentElement.style.setProperty("--topbar-h", topbar.offsetHeight + "px");
    syncTop();
    if (window.ResizeObserver) new ResizeObserver(syncTop).observe(topbar);
    window.addEventListener("resize", syncTop);
  }
  // Lib-Nav ist ebenfalls fixed (klebt unter der Topbar) → eigene Höhe
  // tracken, damit der Body-Padding-Top beide Höhen einrechnet und Inhalt
  // nicht hinter der Nav verschwindet.
  const libNavEl = document.querySelector("#libNav");
  if (libNavEl) {
    const syncNav = () => document.documentElement.style.setProperty("--lib-nav-h", libNavEl.offsetHeight + "px");
    syncNav();
    if (window.ResizeObserver) new ResizeObserver(syncNav).observe(libNavEl);
    window.addEventListener("resize", syncNav);
  }
  $("#flatViewBtn").classList.toggle("active", state.flatView);
  await Promise.all([loadHealth(), loadSettings(), loadLibraries()]);
  // Beim ersten Laden: Startseite zeigen. loadLibraries() setzt implizit
  // die erste Library als aktuell — wir überschreiben, damit der User die
  // Home-Kacheln als Landing-Page sieht. Ein Klick auf eine Library im
  // Dropdown bringt ihn sofort in den klassischen Grid-View.
  state.homeView = true;
  state.currentLibrary = null;
  state.currentPlaylist = null;
  $("#librarySelect").value = "";
  loadItems();
  checkScanActive();
  // Falls der User die Seite während eines laufenden Bulk-Refresh neu lädt,
  // direkt Status pollen, damit der Fortschritts-Toast wieder erscheint.
  api("/api/enrich/refresh-all-status").then(st => {
    if (st && st.running) pollRefreshAllStatus();
  }).catch(() => {});
  checkTrickplayWorker();
  setInterval(checkScanActive, 30000);
  setInterval(checkTrickplayWorker, 30000);
  renderUserMenu();
  // Google-Cast SDK initialisieren — registriert sich beim Cast-Framework
  // sobald `cast_sender.js` geladen ist und der Receiver-Discovery startet.
  initCastFramework();
})();
