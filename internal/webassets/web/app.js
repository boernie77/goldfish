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

// api(), apiGetCached(), invalidateItemsCache() liegen in api.js.

// fmtDuration/fmtSize/fmtDate liegen in helpers.js (gleiche window-Scope,
// via separatem <script>-Tag VOR app.js geladen).

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

// Staffel-Ansicht-Einstellungen (3-stufig):
//  - pro Serie:  seasonView:<libID>:<folder> = "1" / "0"  (setzt Default außer Kraft)
//  - pro Library: seasonView:lib:<libID>         = "1" / "0"  (Default für alle Serien)
// Effective = pro Serie if set, else library-Default, else false.
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

// resLabel liegt in helpers.js.

// App-Dialoge (appAlert/appConfirm/appPrompt/showToast) liegen in dialogs.js
// und werden via separatem <script>-Tag VOR app.js geladen.

// escapeHTML liegt in helpers.js.

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

// loadItems + loadItemsBody liegen in grid.js.


// renderHomeView (Startseite) und Staffel-Ansicht liegen in views.js.
// renderRangeContinuationCard ist auch dort (Doppelfolgen-Slot).


// renderCard und Verwandte (renderPlaylistCard, renderFolderCard,
// renderCollectionCard, renderPersonShowCard, hidePartButton,
// openMissingMovieDialog) liegen in cards.js.

// Player + Detail-View + Trickplay-Hover + Buffer-Gate liegen in player.js.

// User-Menue + Admin-Panel liegen in admin.js.

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

// Manage Libraries + Path Browser + Settings liegen in admin.js.

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
