// matching.js — Admin-Workflows rund um Metadaten + Matching.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views →
//   grid → player → admin → playlists → scan → matching → app
//
// Public Functions (alle global):
//   Refresh-All-Metadata: runRefreshAllMetadata, pollRefreshAllStatus
//   Missing-Movies/-Episodes-Export (Radarr/Sonarr-Bridge):
//     openMissingDialog, runMissingEpisodesScan, renderMissingPreview
//   Datei-/Pfad-Suche (Admin-Diagnose):
//     openPathSearch, runPathSearch
//   Trickplay-Manager (Admin):
//     openTrickplayManager, refreshTrickplayManager,
//     cancelTrickplayRun, cancelScanRun, retryFailedTrickplay,
//     openTrickplayFailedView, exitTrickplayFailedView, deleteAllTrickplay
//   Manuelles TMDB-Matching + Metadata-Edit:
//     openEditMetaDialog, openMatchDialog, applyMetadataMatch, …

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

async function loadMissingMovies() {
  const refreshBtn = $("#missingMoviesRefresh");
  if (refreshBtn) { refreshBtn.disabled = true; refreshBtn.textContent = "Lade…"; }
  $("#missingMoviesStatus").textContent = "Wird geladen…";
  $("#missingMoviesPreview").innerHTML = "";
  let movies = [];
  try {
    movies = await api("/api/missing/movies");
  } catch (e) {
    $("#missingMoviesStatus").textContent = "Fehler: " + e.message;
    if (refreshBtn) { refreshBtn.disabled = false; refreshBtn.textContent = "↻ Aktualisieren"; }
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
  if (refreshBtn) { refreshBtn.disabled = false; refreshBtn.textContent = "↻ Aktualisieren"; }
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
}

async function openMissingDialog() {
  const dlg = $("#missingDialog");
  if (!dlg) return;
  dlg.showModal();

  // 1) Filme — sofort laden; Aktualisieren-Button verdrahten (einmalig)
  const refreshBtn = $("#missingMoviesRefresh");
  if (refreshBtn && !refreshBtn.dataset.wired) {
    refreshBtn.dataset.wired = "1";
    refreshBtn.addEventListener("click", loadMissingMovies);
  }
  await loadMissingMovies();

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
  const runBtn = $("#missingEpRun");
  const origText = runBtn ? runBtn.textContent : "Jetzt prüfen";
  if (runBtn) { runBtn.disabled = true; runBtn.textContent = "Prüfe…"; }
  $("#missingEpStatus").textContent = "Prüfe (kann dauern, fragt TMDB pro Show)…";
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
    if (runBtn) { runBtn.disabled = false; runBtn.textContent = origText; }
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

