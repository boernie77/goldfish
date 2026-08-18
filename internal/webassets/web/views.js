// views.js — Home-Startseite + Staffel-Ansicht-Render.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views → grid → app
//
// Public Functions (alle global):
//   renderHomeView         — Startseite (Fortsetzen / Als nächstes / Zuletzt hinzugefügt)
//   renderRangeContinuationCard — schmaler Stub fuer Doppelfolgen-Slot
//   plus Staffel-Ansicht-Helfer (Show-Header, Season-Cards, „⚠ Episoden
//   neu zuordnen", „↻ TMDB neu laden", Trickplay-Poll innerhalb der Show)
//
// Referenziert renderCard (cards.js), state (app.js), api (api.js),
// loadItems (grid.js), openDetail/openMissingMovieDialog (cards.js bzw.
// app.js). Aufrufe finden zur Laufzeit nach Boot statt — Globals sind
// dann da.

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

  // Globaler Streifen: ein Titel, eine flache Reihe Kacheln aus allen
  // Libraries. Items werden vor dem Render gemerged (groupVariants).
  const renderGlobalStrip = (parent, title, items) => {
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

  // Layout-Variante seit 2026-05-10: Library-uebergreifende „Fortsetzen"
  // und „Als naechstes"-Streifen ganz oben, dann pro Library „Zuletzt
  // hinzugefuegt" — in der Reihenfolge des Topbar-Dropdowns.
  // continue ist server-seitig nach last_played_at DESC sortiert pro Lib,
  // wir mergen + re-sortieren cross-library; nextUp analog nach added_at.
  const allContinue = [];
  const allNextUp = [];
  for (const sec of sections) {
    if (Array.isArray(sec.continue)) allContinue.push(...sec.continue);
    if (Array.isArray(sec.nextUp))   allNextUp.push(...sec.nextUp);
  }
  // Cross-library Sort: continue nach lastPlayedAt desc (faellt auf addedAt
  // zurueck), nextUp nach addedAt desc.
  const tsKey = (it, prefer) => {
    const v = (it && it[prefer]) || (it && it.addedAt) || "";
    return v;
  };
  allContinue.sort((a, b) => tsKey(b, "lastPlayedAt").localeCompare(tsKey(a, "lastPlayedAt")));
  allNextUp.sort((a, b) => tsKey(b, "addedAt").localeCompare(tsKey(a, "addedAt")));
  // Cap: 24 / 24 — sonst werden die Streifen unuebersichtlich lang.
  renderGlobalStrip(wrap, "▶ Fortsetzen", allContinue.slice(0, 24));
  renderGlobalStrip(wrap, "📺 Als nächstes", allNextUp.slice(0, 24));

  // Pro Library: nur „Zuletzt hinzugefuegt", in Library-Reihenfolge.
  const flatAll = [...allContinue, ...allNextUp];
  for (const sec of sections) {
    if (!sec.recent || !sec.recent.length) continue;
    const lib = sec.library || {};
    const libBlock = document.createElement("div");
    libBlock.className = "home-lib-block";
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
    renderGlobalStrip(libBlock, "🆕 Zuletzt hinzugefügt", sec.recent);
    wrap.appendChild(libBlock);
    flatAll.push(...sec.recent);
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
    // Owned-Episoden mit itemId aus dem Server-Response extrahieren — die
    // brauchen wir fuer den Bulk-Watched-Toggle. Duplikate via itemIds[].
    const ownedIds = [];
    for (const ep of (sn.episodes || [])) {
      if (ep.owned && ep.itemIds && ep.itemIds.length) {
        for (const id of ep.itemIds) {
          if (!ownedIds.includes(id)) ownedIds.push(id);
        }
      } else if (ep.owned && ep.itemId) {
        if (!ownedIds.includes(ep.itemId)) ownedIds.push(ep.itemId);
      }
    }
    const canMark = ownedIds.length > 0;
    // "Komplett gesehen" wenn alle owned Episoden watched sind. Server
    // liefert sn.watchedCount; wenn nicht vorhanden, fallback auf 0 →
    // Button bleibt grau bis Reload.
    const allWatched = canMark && (sn.watchedCount || 0) >= sn.ownedCount;
    const btnTitle = allWatched
      ? "Staffel ist komplett als gesehen markiert — klicken zum Aufheben"
      : "Ganze Staffel als gesehen markieren";
    const btnClass = allWatched ? "season-mark-watched is-on" : "season-mark-watched";
    el.innerHTML = `
      <div class="thumb">
        <img class="thumb-img" loading="lazy" decoding="async" alt="" src="${poster}">
        <span class="folder-count">${sn.ownedCount}/${sn.total} Folgen</span>
        ${canMark ? `<button class="${btnClass}" title="${btnTitle}" data-ids="${ownedIds.join(",")}" data-all-watched="${allWatched ? "1" : "0"}">✓</button>` : ""}
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
    // Bulk-Mark-Watched: pro Staffel-Card. Hilft beim "schon eh die ganze
    // Serie gesehen, alle Episoden auf einmal als gesehen markieren".
    const markBtn = el.querySelector(".season-mark-watched");
    if (markBtn) {
      markBtn.addEventListener("click", async (ev) => {
        ev.stopPropagation();
        const ids = ownedIds.slice();
        const seasonLabel = sn.name || ("Staffel " + sn.seasonNumber);
        // Toggle: wenn alle schon gesehen → als ungesehen markieren, sonst
        // als gesehen. Confirm fragt den passenden Text.
        const turningOff = allWatched;
        const targetWatched = !turningOff;
        const verb = turningOff ? "als UNgesehen" : "als gesehen";
        const ok = await appConfirm(`${ids.length} Folge${ids.length === 1 ? "" : "n"} der "${escapeHTML(seasonLabel)}" ${verb} markieren?`);
        if (!ok) return;
        markBtn.disabled = true;
        markBtn.textContent = "…";
        let okCnt = 0, failCnt = 0;
        for (const id of ids) {
          try {
            await api(`/api/items/${id}/watched`, {
              method: "PUT",
              body: JSON.stringify({ watched: targetWatched }),
            });
            okCnt++;
          } catch { failCnt++; }
        }
        if (failCnt === 0) showToast(`${okCnt} Folgen ${verb} markiert.`, { kind: "success" });
        else showToast(`${okCnt} markiert, ${failCnt} fehlgeschlagen.`, { kind: "error" });
        // Reload via Refresh der Staffel-View — markBtn re-rendert mit
        // aktuellen owned/watched-Counts (und gruener Farbe wenn alles).
        loadItems();
      });
    }
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
    if (opts.personFilterShow) {
      // Innerhalb einer Show-Sammelkachel: ← geht zurück zur Personen-Übersicht
      // (Filme+Serien-Kacheln), NICHT den Person-Filter ganz verlassen.
      back.title = "Zurück zur Übersicht";
      back.addEventListener("click", () => { state.personFilterShow = null; loadItems(); });
    } else {
      back.title = "Person-Filter aufheben";
      back.addEventListener("click", clearPersonView);
    }
    bc.appendChild(back);
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = opts.personFilterShow
      ? `🎭 ${state.personFilter.name} · 📺 ${opts.personFilterShow.folder}`
      : "🎭 " + state.personFilter.name;
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
    cur.innerHTML = `${ICON_FILM_SVG} Trickplay-Fehler`;
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

  if (opts.flatSortView) {
    const labels = {
      played:   "🕘 Zuletzt abgespielt",
      added:    "🆕 Zuletzt hinzugefügt",
      duration: "⏱ Nach Laufzeit",
    };
    const label = labels[opts.flatSortView] || "Sortiert";
    const lib = state.libraries.find(l => l.id == state.currentLibrary);
    const libName = lib ? lib.name : "";
    // Zurück-Button: nur in einem Unterordner (im Library-Root gibt's nichts,
    // wohin man zurückgehen könnte). Gleiche "eine Ebene hoch"-Logik wie die
    // normale Ordner-Navigation weiter unten.
    if (state.currentFolder) {
      const back = document.createElement("button");
      back.className = "back-btn";
      back.title = "Eine Ebene zurück";
      back.textContent = "←";
      back.addEventListener("click", () => {
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
    }
    // Scope anzeigen: im Library-Root die Lib, in einem Unterordner der Ordner
    // (der Flat-Sort wirkt dann nur nach unten in diesem Ordner, nicht library-weit).
    let where = libName ? `${libIcon(lib)} ${libName}` : "";
    if (state.currentFolder) {
      const folderName = state.currentFolder.split("/").filter(Boolean).pop() || state.currentFolder;
      where = where ? `${where} / ${folderName}` : folderName;
    }
    const cur = document.createElement("span");
    cur.className = "current";
    cur.textContent = where ? `${label} in ${where}` : label;
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
    // Zwei Varianten: fileDupes = Datei-basiert, gescoped auf DIESE Library +
    // aktuellen Ordner (braucht Zurück-Pfeil wie die Flat-Sorts). Sonst =
    // metadata_id-basiert, library-übergreifend über alle Libs gleichen Kinds
    // (kein sinnvoller "zurück"-Zielort, kein Pfeil).
    if (opts.fileDupes && state.currentFolder) {
      const back = document.createElement("button");
      back.className = "back-btn";
      back.title = "Eine Ebene zurück";
      back.textContent = "←";
      back.addEventListener("click", () => {
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
    }
    const kindLabel = opts.duplicatesKind === "movies" ? "Filme"
      : opts.duplicatesKind === "tv" ? "Serien"
      : opts.duplicatesKind === "private" ? "Privatvideos"
      : "Bibliotheken";
    const cur = document.createElement("span");
    cur.className = "current";
    if (opts.fileDupes) {
      const lib = state.libraries.find(l => l.id == state.currentLibrary);
      const libName = lib ? lib.name : "";
      let where = libName ? `${libIcon(lib)} ${libName}` : "";
      if (state.currentFolder) {
        const folderName = state.currentFolder.split("/").filter(Boolean).pop() || state.currentFolder;
        where = where ? `${where} / ${folderName}` : folderName;
      }
      cur.textContent = `⧉ Duplikate in ${where}`;
    } else {
      cur.textContent = `⧉ Duplikate (alle ${kindLabel})`;
    }
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
