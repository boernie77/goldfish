// scan.js — Scan-Aktionen + globale Statusleisten (Scan + Trickplay).
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views →
//   grid → player → admin → playlists → scan → app
//
// Public Functions:
//   Scan-Aktionen + Status:
//     startScan, startScanAll, cancelScan, scanStatusBar (statusbar-Renderer
//     fuer „Scanne … x/y"-Bottom-Leiste mit Progress + Cancel)
//   Globale Trickplay-Statusleiste:
//     checkTrickplayWorker (Polling), trickplayStatusBar (Renderer mit
//     „Trickplay … (item.title)"-Anzeige + Cancel-Button)

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
  // Stop a pending hide-timeout from a recently finished scan. Sonst
  // blendet es den Balken mitten im neuen Scan wieder weg.
  if (state.scanHideTimer) { clearTimeout(state.scanHideTimer); state.scanHideTimer = null; }
  // Sofortiger Placeholder, damit der Balken nicht den ALTEN Status
  // (z. B. „✓ Scan fertig …" vom vorigen Lauf) zeigt, bis der erste Poll
  // ~1 s spaeter mit echten Daten reinkommt.
  bar.innerHTML = `
    <div class="statusbar-row">
      <div class="statusbar-text">Scan startet…</div>
    </div>
  `;
  bar.classList.remove("hidden");

  // finalize wird nach Single- oder All-Scan einmalig zur Auflösung gerufen.
  const finalize = () => {
    clearInterval(state.scanPoll);
    state.scanPoll = null;
    state.scanHideTimer = setTimeout(() => { bar.classList.add("hidden"); state.scanHideTimer = null; }, 4000);
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
        clearInterval(state.scanPoll);
        state.scanPoll = null;
        state.scanHideTimer = setTimeout(() => { bar.classList.add("hidden"); state.scanHideTimer = null; }, 4000);
        if (st.lastSummary) showScanSummary(st.lastSummary);
        loadItems();
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

