// grid.js — loadItems + loadItemsBody: das Items-Grid-Loading.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → grid → app
//
// Public Functions:
//   loadItems()           — Einstiegspunkt: liest aktuellen UI-State aus,
//                           dispatcht an die richtige Branch (Library /
//                           Playlist / Home / Sammlungen / Person-Filter /
//                           Staffel-Ansicht / Suspicious / Duplicates / …).
//   loadItemsBody()       — die eigentliche grosse Switch-Logik mit
//                           Branches je Anzeige-Modus.
//
// Referenziert state und globale Funktionen aus app.js (renderCard,
// renderFolderCard, renderHomeView, applyNaturalTitleSort, attachVariantCounts,
// groupVariants, restoreSortForContext, currentSortMode, currentMatchMode, …)
// — Aufrufe finden zur Laufzeit nach Boot statt, also OK.

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
  // anderen Library/Ordner keinen Sinn mehr.
  // NUR State + Banner zurücksetzen, NICHT setAlphaFilter() rufen: das würde
  // synchron applyAlphaFilter() auf das noch alte, gerade verlassene Grid
  // anwenden und kurz alle vorher ausgeblendeten Kacheln wieder einblenden
  // (sichtbares Aufflackern der vollen Übersicht), bevor der asynchrone
  // Sprung ins Ziel (z. B. eine über die Buchstaben-Sidebar gewählte Serie)
  // überhaupt fertig geladen ist. Die neuen Kacheln bekommen ihren Filter-
  // Zustand ohnehin frisch über den applyAlphaFilter()-Aufruf im finally-Block.
  if (state.lastNavKey && state.lastNavKey !== targetKey && state.alphaFilter) {
    state.alphaFilter = null;
    const banner = $("#alphaFilterBanner");
    if (banner) banner.classList.add("hidden");
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
    // Mindestlänge 2 — bei 1 Buchstaben matcht die Suche zu viel (LIKE %h%
    // gegen alle Libraries plus Schauspieler-EXISTS-Subquery), das Ergebnis
    // ist nicht hilfreich und der Roundtrip mehrere Sekunden.
    if (sq.length >= 2) {
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
      // Sortierung wie in jeder anderen Ansicht ueber #sortSelect steuerbar
      // (Default ohne Auswahl: manuelle playlist_items.position, siehe
      // Store.PlaylistItems). currentSortMode() mappt "shuffle" bereits auf
      // "title" — client-seitig danach gewuerfelt wie ueberall sonst auch.
      const params = new URLSearchParams();
      params.set("sort", currentSortMode());
      const dir = effectiveSortDir();
      if (dir) params.set("dir", dir);
      items = await api(`/api/playlists/${state.currentPlaylist}/items?${params}`);
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
    if ($("#sortSelect").value === "shuffle") shuffleInPlace(items);
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
    // Innerhalb einer Show-Sammelkachel: die Episoden stehen schon fest (kein
    // erneuter Server-Call), nur die zur Person passenden Folgen dieser Serie.
    if (state.personFilterShow) {
      const showState = state.personFilterShow;
      renderBreadcrumb({ personFilterShow: showState });
      grid.innerHTML = "";
      const mergedEpisodes = groupVariants(showState.episodes);
      state.lastRenderedItems = mergedEpisodes;
      const eg = document.createElement("div");
      eg.className = "subview-grid";
      for (const ep of mergedEpisodes) eg.appendChild(renderCard(ep));
      grid.appendChild(eg);
      return;
    }
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
          episodes: [],
        });
      }
      const entry = showsMap.get(key);
      entry.count++;
      entry.episodes.push(ep);
    }
    const shows = Array.from(showsMap.values()).sort((a, b) => a.folder.localeCompare(b.folder));
    for (const s of shows) {
      s.episodes.sort((a, b) => {
        const sa = (a.metadata && a.metadata.season) || 0, sb = (b.metadata && b.metadata.season) || 0;
        if (sa !== sb) return sa - sb;
        return ((a.metadata && a.metadata.episode) || 0) - ((b.metadata && b.metadata.episode) || 0);
      });
    }

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

  // "Datei in anderem Ordner" (Dropdown-Option): Items, deren Dateiname + Größe
  // (±2 KB) auch in einem ANDEREN Ordner derselben Library vorkommen. Kachel
  // bekommt einen ⧉-Badge mit dem/den anderen Pfad(en). Scoped auf den aktuellen
  // Ordner (falls in einem), sonst ganze Library.
  if ($("#sortSelect").value === "namedupes" && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    const p = new URLSearchParams({});
    if (state.currentFolder) p.set("folder", state.currentFolder);
    let items = [];
    try {
      items = await apiGetCached(`/api/libraries/${state.currentLibrary}/name-dupes?${p}`);
    } catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    if (searchQ) {
      const q = searchQ.toLowerCase();
      items = items.filter(it => (it.title || "").toLowerCase().includes(q)
        || (it.relPath || "").toLowerCase().includes(q)
        || (it.dupeOtherPaths || []).some(pp => pp.toLowerCase().includes(q)));
    }
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    // Nach Dateiname gruppieren, damit gleichnamige Treffer nebeneinander stehen.
    items.sort((a, b) => {
      const fa = (a.relPath || "").split("/").pop().toLowerCase();
      const fb = (b.relPath || "").split("/").pop().toLowerCase();
      return fa.localeCompare(fb) || (a.relPath || "").localeCompare(b.relPath || "");
    });
    renderBreadcrumb({ searchCount: items.length, nameDupesView: true });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine gleichnamigen Dateien in einem anderen Ordner gefunden. ✓</div>`;
      return;
    }
    state.lastRenderedItems = items;
    const frag = document.createDocumentFragment();
    items.forEach(it => frag.appendChild(renderCard(it)));
    grid.innerHTML = "";
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
    const currentLib = state.libraries.find(l => l.id == state.currentLibrary);
    const currentKind = currentLib ? currentLib.kind : null;
    // Privat-Libraries haben normalerweise keine TMDB/Custom-Zuordnung
    // (metadata_id meist NULL) — die metadata_id-basierte Erkennung unten
    // findet dort praktisch nie etwas. Stattdessen Datei-basiert erkennen
    // (gleiche Größe + Laufzeit = mit hoher Sicherheit dieselbe Datei
    // zweimal vorhanden), gescoped auf DIESE Library + aktuellen Ordner
    // (Scope "nur nach unten flach", wie die anderen Flat-Sorts).
    const isFileDupes = currentKind === "private";
    let items = [];
    if (isFileDupes) {
      const p = new URLSearchParams({
        libraryId: state.currentLibrary,
        fileDuplicates: "yes",
        sort: "title",
        dir: "asc",
      });
      if (state.currentFolder) p.set("folder", state.currentFolder);
      if (searchQ) p.set("search", searchQ);
      $("#searchClear").classList.toggle("hidden", searchQ === "");
      try { items = await apiGetCached(`/api/items?${p}`); }
      catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
      if (stale()) return;
      items.sort((a, b) => {
        if (a.sizeBytes !== b.sizeBytes) return a.sizeBytes - b.sizeBytes;
        return (a.relPath || "").localeCompare(b.relPath || "");
      });
    } else {
      // Duplikate-Modus (Filme/Serien): zeige alle Versionen eines duplizierten
      // Items als eigene Kachel — aber nur aus Bibliotheken mit demselben
      // „kind" wie die gerade aktive (Bluray + Filme zusammen wenn beide
      // movies, Serien separat). Sonst würde z.B. in Bluray ein Episoden-
      // Duplikat aus „Serien" mit auftauchen. Bewusst library-übergreifend,
      // nicht folder-gescoped (Filme/Serien haben keine sinnvolle
      // "gleicher Ordner"-Grenze für Duplikate).
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
      try { items = await apiGetCached(`/api/items?${p}`); }
      catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
      if (stale()) return;
      // Filter auf Libraries gleichen Kinds (siehe Kommentar oben).
      items = items.filter(it => sameKindLibIds.has(it.libraryId));
      // Server zählt Duplikate global — wenn zwei Versionen library-übergreifend
      // existieren aber durch den Kind-Filter rausfallen, ist eine ggf. allein.
      // Solche „nicht-mehr-doppelten" Items entfernen, damit keine 1er-Kacheln
      // im Duplikate-View landen.
      const cnt = new Map();
      for (const it of items) cnt.set(it.metadataId, (cnt.get(it.metadataId) || 0) + 1);
      items = items.filter(it => (cnt.get(it.metadataId) || 0) >= 2);
      items.sort((a, b) => {
        if (a.metadataId !== b.metadataId) return a.metadataId - b.metadataId;
        return (a.relPath || "").localeCompare(b.relPath || "");
      });
    }
    renderBreadcrumb({ searchCount: items.length, duplicatesView: true, duplicatesKind: currentKind, fileDupes: isFileDupes });
    if (!items.length) {
      grid.innerHTML = `<div class="empty">Keine Duplikate gefunden.</div>`;
      return;
    }
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

  // Flache Sort-Modi: "Zuletzt abgespielt", "Zuletzt hinzugefügt" und "Laufzeit".
  // Alle drei ignorieren die Ordner-STRUKTUR (keine Folder-Kacheln) und zeigen
  // eine flache Liste. SCOPE: nur nach unten flach — im Library-Root die ganze
  // Library, in einem Unterordner NUR dessen Inhalt (rekursiv), nicht library-
  // weit hochziehen. Server sortiert (bei played zusätzlich Filter auf
  // last_played_at IS NOT NULL) und filtert `folder` rekursiv via LIKE.
  const FLAT_SORTS = new Set(["played", "added", "duration"]);
  const flatSort = $("#sortSelect").value;
  if (FLAT_SORTS.has(flatSort) && state.currentLibrary) {
    const searchQ = $("#searchInput").value.trim();
    const p = new URLSearchParams({
      libraryId: state.currentLibrary,
      sort: flatSort,
      dir: effectiveSortDir(),
    });
    // In einem Unterordner: nur diesen Ordner (rekursiv) flach zeigen.
    if (state.currentFolder) p.set("folder", state.currentFolder);
    if (searchQ) p.set("search", searchQ);
    const watched = $("#watchedFilter").value; if (watched) p.set("watched", watched);
    applyResolutionFilter(p);
    $("#searchClear").classList.toggle("hidden", searchQ === "");
    let items = [];
    try { items = await apiGetCached(`/api/items?${p}`); }
    catch (e) { if (!stale()) grid.innerHTML = `<div class="empty">Fehler: ${escapeHTML(e.message)}</div>`; return; }
    if (stale()) return;
    renderBreadcrumb({ searchCount: items.length, flatSortView: flatSort });
    if (!items.length) {
      const scope = state.currentFolder ? "in diesem Ordner" : "in dieser Bibliothek";
      const msg = flatSort === "played" ? `Noch nichts abgespielt ${scope}.` : `Keine Videos ${scope}.`;
      grid.innerHTML = `<div class="empty">${msg}</div>`;
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
