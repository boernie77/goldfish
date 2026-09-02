// cards.js — alle Card-Render-Funktionen + zugehoerige Helper.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → app
//
// Public Functions (alle im window-Scope):
//   renderPlaylistCard, renderMissingPartCard, renderPersonShowCard,
//   renderCollectionCard, renderFolderCard, renderCard,
//   openMissingMovieDialog, hidePartButton
//
// Plus interner Helper:
//   createMissingMovieDialog (DOM-Setup fuer Missing-Part-Dialog)
//
// Diese Funktionen referenzieren globale Funktionen aus app.js (loadItems,
// openDetail, openPersonView, toggleWatchedOnCard, toggleFavoriteOnCard,
// state, …) — kein Problem, da sie erst bei User-Interaktion aufgerufen
// werden. Zum Zeitpunkt des Aufrufs ist app.js voll geladen und state
// initialisiert.

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
  // Noch nicht erschienene Teile (kein Datum oder Datum in der Zukunft)
  // zeigen "Bald" statt "Fehlt" — sie zählen serverseitig auch nicht gegen
  // die "✓ komplett"-Markierung der Sammlung, siehe ListCollections.
  const isUnreleased = !p.releaseDate || p.releaseDate > new Date().toISOString().slice(0, 10);
  const badgeLabel = isUnreleased ? "Bald" : "Fehlt";
  const badgeTitle = isUnreleased ? "Noch nicht erschienen" : "Film nicht in der Bibliothek";
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${imgUrl}">
      <span class="missing-badge${isUnreleased ? " missing-badge--upcoming" : ""}" title="${badgeTitle}">${badgeLabel}</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(p.title)}">${escapeHTML(p.title)}</div>
      <div class="card-meta">
        ${year ? `<span>${year}</span>` : ""}
        <span style="color:#94a3b8">${badgeLabel}</span>
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
// Episoden. Klick navigiert NICHT zum vollen Serien-Ordner (bei vielen
// Folgen und nur wenigen Gastauftritten unübersichtlich, User-Anfrage
// 2026-08-18) — stattdessen bleibt der Person-Filter aktiv und zeigt nur
// die schon geladenen Episoden dieser Show mit dem Schauspieler
// (state.personFilterShow, kein zusätzlicher Server-Call nötig).
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
    state.personFilterShow = { folder: show.folder, libraryId: show.libraryId, episodes: show.episodes };
    loadItems();
  };
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  return el;
}

// renderSearchShowCard: Sammelkachel pro Serie in Such-Ergebnissen. Klick
// öffnet den Serien-Ordner (wie der data-show-link-Klick auf einer
// Episoden-Kachel) — anders als renderPersonShowCard, die im Person-Filter
// bleibt.
function renderSearchShowCard(show) {
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
      <span class="folder-count">${show.count} Treffer</span>
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
    state.personFilterShow = null;
    state.currentLibrary = show.libraryId;
    state.currentFolder = show.folder;
    state.currentFolderDrilldown = false;
    const si = $("#searchInput"); if (si) si.value = "";
    const ls = $("#librarySelect"); if (ls) ls.value = "lib:" + show.libraryId;
    loadItems();
  };
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  return el;
}

// appendSearchResultCards hängt Such-Treffer ans Fragment: Episoden werden pro
// Serie (libraryId + erstes rel_path-Segment) zu EINER Sammelkachel
// zusammengefasst (User-Wunsch, wie in der App), alles andere (Filme,
// Privatvideos) bleibt eine normale Kachel. Die Treffer werden nach Bibliothek
// gruppiert und in der Reihenfolge der Library-Reiter ausgegeben (Reihenfolge
// von state.libraries), jede Gruppe mit einer Zwischenüberschrift.
// Rückgabe: sichtbare Kachel-Anzahl (ohne Überschriften).
function appendSearchResultCards(frag, items) {
  // Pro Library ein Bucket: { rest: [...items], shows: Map<folder, showObj> }
  const buckets = new Map();
  const bucketFor = (libID) => {
    let b = buckets.get(libID);
    if (!b) { b = { rest: [], shows: new Map() }; buckets.set(libID, b); }
    return b;
  };
  for (const it of items || []) {
    const b = bucketFor(it.libraryId);
    const isEp = it.metadata && it.metadata.tmdbType === "episode";
    const rel = (it.relPath || "").split("/");
    if (isEp && rel.length > 1) {
      let s = b.shows.get(rel[0]);
      if (!s) {
        s = {
          libraryId: it.libraryId,
          folder: rel[0],
          showParentId: (it.metadata && it.metadata.parentId) || 0,
          fallbackThumbId: it.id,
          count: 0,
        };
        b.shows.set(rel[0], s);
      }
      s.count++;
    } else {
      b.rest.push(it);
    }
  }

  // Ausgabe-Reihenfolge der Libraries = Reiter-Reihenfolge; unbekannte
  // Library-IDs (sollte nicht vorkommen) hinten anhängen.
  const libOrder = (state.libraries || []).map(l => l.id);
  const orderedLibIDs = [
    ...libOrder.filter(id => buckets.has(id)),
    ...Array.from(buckets.keys()).filter(id => !libOrder.includes(id)),
  ];
  const libName = (id) => {
    const l = (state.libraries || []).find(x => x.id == id);
    return l ? l.name : "Weitere";
  };

  let total = 0;
  const multiLib = orderedLibIDs.length > 1;
  for (const libID of orderedLibIDs) {
    const b = buckets.get(libID);
    const mergedRest = groupVariants(b.rest);
    const showList = Array.from(b.shows.values())
      .sort((a, c) => a.folder.localeCompare(c.folder, "de"));
    const n = mergedRest.length + showList.length;
    if (!n) continue;
    if (multiLib) {
      const h = document.createElement("div");
      h.className = "person-section-title";
      h.textContent = libName(libID) + " \u00b7 " + n;
      frag.appendChild(h);
    }
    for (const m of mergedRest) frag.appendChild(renderCard(m));
    for (const s of showList) frag.appendChild(renderSearchShowCard(s));
    total += n;
  }
  return total;
}

// renderPersonHeader: Info-Block über den Schauspieler (Foto + Lebensdaten +
// Biografie mit „mehr"-Toggle). person: { tmdbId, name, biography, birthday,
// deathday, placeOfBirth, knownForDepartment }.
function renderPersonHeader(person) {
  const el = document.createElement("div");
  el.className = "person-header";
  const facts = [];
  if (person.knownForDepartment) {
    const dep = person.knownForDepartment === "Acting" ? "Schauspiel" : person.knownForDepartment;
    facts.push(dep);
  }
  const y = (d) => (d && d.length >= 4 ? d.slice(0, 4) : "");
  if (person.birthday) {
    facts.push(person.deathday
      ? `${y(person.birthday)}–${y(person.deathday)}`
      : `geb. ${fmtDate(person.birthday)}`);
  }
  if (person.placeOfBirth) facts.push(person.placeOfBirth);
  const bio = (person.biography || "").trim();
  const bioShort = bio.length > 320 ? bio.slice(0, 320).replace(/\s+\S*$/, "") + "…" : bio;
  el.innerHTML = `
    <div class="person-header-photo" style="background-image:url('/api/person/${person.tmdbId}/profile')"></div>
    <div class="person-header-body">
      <h2>${escapeHTML(person.name || "")}</h2>
      ${facts.length ? `<div class="person-header-facts">${facts.map(f => `<span>${escapeHTML(f)}</span>`).join("")}</div>` : ""}
      ${bio ? `<p class="person-header-bio">${escapeHTML(bioShort)}${
        bio.length > bioShort.length ? ` <button type="button" class="link-btn person-bio-more">mehr</button>` : ""
      }</p>` : ""}
    </div>`;
  if (bio.length > bioShort.length) {
    const p = el.querySelector(".person-header-bio");
    el.querySelector(".person-bio-more").addEventListener("click", () => {
      p.textContent = bio;
    });
  }
  return el;
}

// renderPersonFilmCard: ausgegraute Kachel für einen Filmografie-Eintrag, der
// NICHT in der Bibliothek ist. credit: { tmdbId, mediaType, title, year, character, posterPath }.
function renderPersonFilmCard(credit) {
  const el = document.createElement("article");
  el.className = "card card--poster person-film-missing";
  const img = credit.posterPath
    ? `https://image.tmdb.org/t/p/w342${credit.posterPath}`
    : "/placeholder.svg";
  el.innerHTML = `
    <div class="thumb" style="background-image:url('${img}')">
      <span class="missing-badge" title="Nicht in der Bibliothek">nicht vorhanden</span>
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(credit.title)}">${escapeHTML(credit.title)}</div>
      <div class="card-meta">
        ${credit.year ? `<span>${credit.year}</span>` : ""}
        ${credit.mediaType === "tv" ? `<span>📺 Serie</span>` : ""}
        ${credit.character ? `<span class="person-film-role">als ${escapeHTML(credit.character)}</span>` : ""}
      </div>
    </div>`;
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
    imgUrl = `/api/poster/metadata/${f.metadataId}?v=${encodeURIComponent(f.metadata.posterPath)}`;
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
    // Cache-Busting via ?v=<posterPath>: die Poster-URL hängt nur an der metadataId, die
    // sich bei einem Re-Match NICHT ändert — obwohl die Datei dahinter (anderer TMDB-
    // posterPath) eine andere ist. Ohne den Query-Param kann der Browser (oder ein
    // Client-Cache) nach einer Neuzuordnung noch das alte Poster unter derselben URL
    // zeigen (User-Bericht 2026-08-19: "American Fighter" zeigte ein falsch zugeschnitten
    // wirkendes Poster, reproduzierbar sowohl hier als auch in der nativen Mac-App).
    imgUrl = `/api/poster/metadata/${it.metadataId}?v=${encodeURIComponent(it.metadata.posterPath)}`;
    if (!isEpisode) {
      title = it.metadata.title || it.title;
      if (it.metadata.year) subtitle = String(it.metadata.year);
    }
  } else {
    imgUrl = it.hasThumb ? `/api/thumb/${it.id}` : "/placeholder.svg";
  }
  // Private Libraries (YouTube-Channels, Urlaubsordner, etc.): Top-Zeile zeigt
  // den Kanal/Top-Folder (relPath[0]) — der Dateiname/Titel kommt unten in der
  // card-filename-Zeile (CSS macht ihn dort etwas dicker). Per-Lib via
  // Library-Manager-Checkbox abschaltbar (channelLabelOnTop=false).
  const channelTop = itLib && itLib.kind === "private" && itLib.channelLabelOnTop !== false;
  if (channelTop && !isEpisode) {
    const rel = (it.relPath || "").split("/").filter(Boolean);
    if (rel.length > 1) {
      title = rel[0];
    }
  }
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
    tp = `<span class="tp-badge" title="Trickplay vorhanden">${ICON_FILM_SVG}</span>`;
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
  // ⧉-Badge: nur im "Datei in anderem Ordner"-Filter gesetzt — gleichnamige,
  // ähnlich große Datei(en) an anderer Stelle derselben Library.
  const dupePaths = Array.isArray(it.dupeOtherPaths) ? it.dupeOtherPaths : [];
  const dupeBadge = dupePaths.length
    ? `<span class="dupe-badge" title="Auch vorhanden als:&#10;${escapeHTML(dupePaths.join("\n"))}">⧉${dupePaths.length > 1 ? " ×" + dupePaths.length : ""}</span>`
    : "";
  // Persönliche Sterne-Bewertung (Privat-Libs, user_item_state.rating 1..3).
  // Overlay oben rechts unter dem Rating-Slot (top:66 right:6 laut CLAUDE.md-
  // Koordinatentabelle). Nur wenn > 0.
  const userRating = (itLib && itLib.kind === "private" && (it.rating || 0) > 0)
    ? `<span class="user-rating-badge" title="${it.rating} Sterne">${"★".repeat(it.rating)}</span>`
    : "";
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
      ${dupeBadge}
      ${userRating}
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
        ${it.metadataConfirmed ? `<span class="confirmed-tick" title="Zuordnung bestätigt">✓</span>` : ""}
        ${released && !subtitle && !episodeName ? `<span>${released}</span>` : ""}
      </div>
      <div class="card-filename" title="${escapeHTML(it.relPath || it.path || "")}">${escapeHTML(channelTop ? (it.title || cardFileName(it)) : cardFileName(it))}</div>
      ${sortVal === "duplicates" ? `<div class="card-duppath" title="${escapeHTML(it.relPath || it.path || "")}">${escapeHTML(cardFolderPath(it))}</div>` : ""}
      ${dupePaths.length ? `<div class="card-duppath" title="${escapeHTML(dupePaths.join("\n"))}">↳ auch: ${(() => {
        const ownDir = (() => { const r = it.relPath || it.path || ""; const i = r.lastIndexOf("/"); return i > 0 ? r.slice(0, i) : ""; })();
        // Wenn ein Zwilling im GLEICHEN Ordner liegt (typisch bei "≈ Ähnliche
        // Dateinamen"), den Dateinamen zeigen statt des identischen Ordnerpfads.
        return escapeHTML(dupePaths.map(p => {
          const i = p.lastIndexOf("/");
          const dir = i > 0 ? p.slice(0, i) : "";
          return dir === ownDir ? p.slice(i + 1) : (dir || "(Wurzel)");
        }).join(" · "));
      })()}</div>` : ""}
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
  state.personFilterShow = null;
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

