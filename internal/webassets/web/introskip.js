// introskip.js — Admin-Dialog für die automatische Vorspann-Erkennung.
//
// Reihenfolge in index.html: … → matching → whisper → introskip → app.
//
// Kombiniert drei Teile in einem Dialog (#introSkipDialog):
//   1. Globaler An/Aus-Schalter (GET/PUT /api/introskip/settings)
//   2. Flache Serien-Ordner-Liste einer TV-Bibliothek mit Checkbox pro Zeile
//      — PUT sofort bei Klick (kein "Übernehmen"-Schritt, anders als der
//      rekursive Shuffle-Scope-Dialog: eine Serie ist immer ein
//      Top-Level-Ordner, kein Baum nötig).
//   3. Job-Status-Tabs done/pending/failed, Aufbau analog zum
//      Trickplay-Manager in matching.js (.tp-tab-Klassen wiederverwendet).

async function openIntroSkipDialog() {
  const tvLibs = (state.libraries || []).filter(l => l.kind === "tv");
  const sel = $("#introSkipLibrarySelect");
  if (!tvLibs.length) {
    sel.innerHTML = `<option value="">Keine TV-Bibliothek vorhanden</option>`;
    $("#introSkipFolderList").innerHTML = `<li class="introskip-empty">Keine TV-Bibliothek vorhanden.</li>`;
  } else {
    sel.innerHTML = tvLibs.map(l => `<option value="${l.id}">${escapeHTML(l.name)}</option>`).join("");
    if (!sel.dataset.wired) {
      sel.dataset.wired = "1";
      sel.addEventListener("change", () => renderIntroSkipFolderList(Number(sel.value)));
    }
    await renderIntroSkipFolderList(Number(sel.value));
  }

  try {
    const settings = await api("/api/introskip/settings");
    $("#introSkipGlobalToggle").checked = !!settings.enabled;
  } catch (e) {
    appAlert("Konnte Einstellungen nicht laden: " + e.message);
  }

  state.introSkipTab = state.introSkipTab || "pending";
  await refreshIntroSkipJobTab();
  wireIntroSkipDialogOnce();
  $("#introSkipDialog").showModal();
}

function wireIntroSkipDialogOnce() {
  const dlg = $("#introSkipDialog");
  if (dlg.dataset.wired) return;
  dlg.dataset.wired = "1";

  $("#introSkipGlobalToggle").addEventListener("change", async (e) => {
    const enabled = e.target.checked;
    try {
      await api("/api/introskip/settings", { method: "PUT", body: JSON.stringify({ enabled }) });
      showToast(enabled ? "Intro-Erkennung aktiviert" : "Intro-Erkennung deaktiviert", { kind: "success" });
    } catch (err) {
      appAlert(err.message);
      e.target.checked = !enabled;
    }
  });

  document.querySelectorAll("[data-introskip-tab]").forEach(btn => {
    btn.addEventListener("click", () => {
      state.introSkipTab = btn.dataset.tpTab;
      refreshIntroSkipJobTab();
    });
  });

  $("#introSkipRetryFailedBtn").addEventListener("click", async () => {
    try {
      const res = await api("/api/introskip/retry-failed", { method: "POST" });
      showToast(`${res.reset} Job(s) erneut eingereiht`, { kind: "success" });
      await refreshIntroSkipJobTab();
    } catch (e) {
      appAlert(e.message);
    }
  });

  $("#introSkipSelectAllBtn").addEventListener("click", () => setAllIntroSkipFolders(true));
  $("#introSkipSelectNoneBtn").addEventListener("click", () => setAllIntroSkipFolders(false));
}

// setAllIntroSkipFolders: aktiviert/deaktiviert ALLE aktuell angezeigten
// Serien der gewählten Bibliothek auf einmal — bequemer Sammel-Button, der
// unter der Haube trotzdem pro Serie einzeln PUTtet (kein Bibliotheks-
// weiter Schalter im Datenmodell, siehe CLAUDE.md "Intro-Erkennung":
// Aktivierung bleibt strikt pro Serien-Ordner, hier wird nur die
// Handarbeit "jede Checkbox einzeln anklicken" gebündelt erledigt).
async function setAllIntroSkipFolders(enabled) {
  const libId = Number($("#introSkipLibrarySelect").value);
  if (!libId) return;
  const rows = Array.from($("#introSkipFolderList").querySelectorAll(".introskip-folder-row"));
  const toToggle = rows.filter(row => {
    const cb = row.querySelector('input[type="checkbox"]');
    return cb && cb.checked !== enabled;
  });
  if (!toToggle.length) return;
  const btn = enabled ? $("#introSkipSelectAllBtn") : $("#introSkipSelectNoneBtn");
  btn.disabled = true;
  try {
    const results = await Promise.allSettled(toToggle.map(row =>
      api(`/api/libraries/${libId}/introskip`, {
        method: "PUT",
        body: JSON.stringify({ folder: row.dataset.folder, enabled }),
      })
    ));
    const failed = results.filter(r => r.status === "rejected").length;
    showToast(
      failed
        ? `${toToggle.length - failed} von ${toToggle.length} Serien ${enabled ? "aktiviert" : "deaktiviert"}, ${failed} Fehler`
        : `${toToggle.length} Serie(n) ${enabled ? "aktiviert" : "deaktiviert"}`,
      { kind: failed ? "error" : "success" }
    );
  } finally {
    btn.disabled = false;
    await renderIntroSkipFolderList(libId, true);
    await refreshIntroSkipJobTab();
  }
}

// renderIntroSkipFolderList baut die Ordnerliste per innerHTML komplett neu
// auf — das setzt scrollTop des Listen-Containers implizit auf 0 zurück
// (Browser-Verhalten bei innerHTML-Ersetzung). Jeder Checkbox-Klick/Retry
// ruft diese Funktion danach erneut auf, wodurch der Dialog bei jeder
// Auswahl nach oben sprang (User-Feedback 2026-08-13). preserveScroll=true
// merkt sich list.scrollTop vor dem Neuaufbau und stellt ihn danach wieder
// her.
async function renderIntroSkipFolderList(libId, preserveScroll) {
  const list = $("#introSkipFolderList");
  const savedScroll = preserveScroll ? list.scrollTop : 0;
  if (!libId) {
    list.innerHTML = "";
    return;
  }
  list.innerHTML = `<li class="introskip-empty">Lädt…</li>`;
  let folders = [];
  let enabled = [];
  try {
    [folders, enabled] = await Promise.all([
      api(`/api/libraries/${libId}/folders?parent=`),
      api(`/api/libraries/${libId}/introskip`),
    ]);
  } catch (e) {
    list.innerHTML = `<li class="introskip-empty">Fehler: ${escapeHTML(e.message)}</li>`;
    return;
  }
  const statusByFolder = {};
  for (const e of enabled) statusByFolder[e.folder] = e;

  if (!folders.length) {
    list.innerHTML = `<li class="introskip-empty">Keine Serien-Ordner in dieser Bibliothek.</li>`;
    return;
  }
  const icons = { done: "✓", running: "⚙", pending: "⏳", failed: "✗" };
  list.innerHTML = folders.map(f => {
    const st = statusByFolder[f.name];
    let badge = "";
    let retryBtn = "";
    if (st && st.jobStatus) {
      const icon = icons[st.jobStatus] || "";
      const detail = st.jobStatus === "done" ? ` ${st.episodesMatched}/${st.episodesTotal}` : "";
      badge = `<span class="introskip-badge introskip-badge--${escapeHTML(st.jobStatus)}" title="${escapeHTML(st.error || "")}">${icon}${detail}</span>`;
      // Retry direkt neben dem Status-Badge — nicht nur unten im Job-Tab,
      // sonst findet man den Button nicht (genau hier schaut man zuerst
      // nach "0 von N Treffern").
      if (st.jobStatus === "done" || st.jobStatus === "failed") {
        retryBtn = `<button type="button" class="tp-row-retry" data-introskip-folder-retry title="Neu analysieren">↻</button>`;
      }
    }
    // Staffel-Feld nur sichtbar, wenn die Serie aktiviert ist — season=0
    // bedeutet "alle Staffeln" (Default), leeres Feld zeigt das als
    // Platzhalter statt einer irreführenden 0.
    const seasonField = st
      ? `<input type="number" class="introskip-season-input" min="0" placeholder="alle"
           value="${st.season ? st.season : ""}" title="Nur diese Staffel analysieren (leer = alle Staffeln)">`
      : "";
    return `<li class="introskip-folder-row" data-folder="${escapeHTML(f.name)}">
      <label><input type="checkbox" ${st ? "checked" : ""}> 📺 ${escapeHTML(f.name)}</label>
      <span class="introskip-folder-trailing">${seasonField}${badge}${retryBtn}</span>
    </li>`;
  }).join("");
  if (preserveScroll) list.scrollTop = savedScroll;

  if (!list.dataset.wired) {
    list.dataset.wired = "1";
    list.addEventListener("click", async (e) => {
      const retry = e.target.closest("[data-introskip-folder-retry]");
      if (!retry) return;
      const row = retry.closest(".introskip-folder-row");
      const folder = row.dataset.folder;
      const curLibId = Number($("#introSkipLibrarySelect").value);
      retry.disabled = true;
      try {
        await api(`/api/introskip/folders/${curLibId}/retry`, {
          method: "POST",
          body: JSON.stringify({ folder }),
        });
        showToast(`"${folder}" wird neu analysiert`, { kind: "success" });
        await renderIntroSkipFolderList(curLibId, true);
        await refreshIntroSkipJobTab();
      } catch (err) {
        appAlert(err.message);
        retry.disabled = false;
      }
    });
    list.addEventListener("change", async (e) => {
      const cb = e.target.closest('input[type="checkbox"]');
      const seasonInput = e.target.closest(".introskip-season-input");
      const curLibId = Number($("#introSkipLibrarySelect").value);
      if (seasonInput) {
        const row = seasonInput.closest(".introskip-folder-row");
        const folder = row.dataset.folder;
        const season = Number(seasonInput.value) || 0;
        seasonInput.disabled = true;
        try {
          await api(`/api/libraries/${curLibId}/introskip`, {
            method: "PUT",
            body: JSON.stringify({ folder, enabled: true, season }),
          });
          showToast(season ? `"${folder}" auf Staffel ${season} beschränkt` : `"${folder}": alle Staffeln`, { kind: "success" });
        } catch (err) {
          appAlert(err.message);
        } finally {
          seasonInput.disabled = false;
        }
        return;
      }
      if (!cb) return;
      const row = cb.closest(".introskip-folder-row");
      const folder = row.dataset.folder;
      cb.disabled = true;
      try {
        await api(`/api/libraries/${curLibId}/introskip`, {
          method: "PUT",
          body: JSON.stringify({ folder, enabled: cb.checked }),
        });
        await renderIntroSkipFolderList(curLibId, true);
        await refreshIntroSkipJobTab();
      } catch (err) {
        appAlert(err.message);
        cb.checked = !cb.checked;
        cb.disabled = false;
      }
    });
  }
}

async function refreshIntroSkipJobTab() {
  document.querySelectorAll("[data-introskip-tab]").forEach(b => {
    b.classList.toggle("primary", b.dataset.tpTab === state.introSkipTab);
  });
  const logEl = $("#introSkipJobList");
  let jobs = [];
  try {
    jobs = await api(`/api/introskip/log?status=${state.introSkipTab}`);
  } catch (e) {
    logEl.innerHTML = `<div class="trickplay-log-entry"><em style="color:#6b7280">Fehler: ${escapeHTML(e.message)}</em></div>`;
    return;
  }
  if (!jobs.length) {
    logEl.innerHTML = `<div class="trickplay-log-entry"><em style="color:#6b7280">Keine Einträge.</em></div>`;
    return;
  }
  logEl.innerHTML = jobs.map(j => {
    const err = (state.introSkipTab === "failed" && j.error)
      ? `<div class="err">✗ ${escapeHTML(j.error)}</div>` : "";
    const detail = j.status === "done" ? ` (${j.episodesMatched}/${j.episodesTotal} Episoden)` : "";
    // Retry auch im "Fertig"-Tab anbieten (nicht nur "Fehler"): ein Job kann
    // technisch erfolgreich abgeschlossen sein, inhaltlich aber unbrauchbar
    // (z.B. 0 Treffer) — der User soll eine Neuanalyse erzwingen können,
    // ohne den Ordner erst ab- und wieder anzuwählen.
    const retryBtn = (state.introSkipTab === "failed" || state.introSkipTab === "done")
      ? `<button type="button" class="tp-row-retry" data-introskip-retry-lib="${j.libraryId}" data-introskip-retry-folder="${escapeHTML(j.folder)}" title="Neu analysieren">↻</button>`
      : "";
    return `<div class="introskip-job-wrap">
      <div class="trickplay-log-entry ${state.introSkipTab}">
        <div class="tp-row-main">
          <button type="button" class="introskip-expand-toggle" data-introskip-expand-lib="${j.libraryId}" data-introskip-expand-folder="${escapeHTML(j.folder)}" title="Episoden anzeigen">▸</button>
          <span class="tp-row-path">${escapeHTML(j.folder)}${detail}</span>
          ${err}
        </div>
        ${retryBtn}
      </div>
      <div class="introskip-episode-list hidden"></div>
    </div>`;
  }).join("");
  logEl.querySelectorAll("[data-introskip-retry-lib]").forEach(btn => {
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try {
        await api(`/api/introskip/folders/${btn.dataset.introskipRetryLib}/retry`, {
          method: "POST",
          body: JSON.stringify({ folder: btn.dataset.introskipRetryFolder }),
        });
        await refreshIntroSkipJobTab();
      } catch (e) {
        appAlert(e.message);
        btn.disabled = false;
      }
    });
  });
  logEl.querySelectorAll("[data-introskip-expand-lib]").forEach(btn => {
    btn.addEventListener("click", () => toggleIntroSkipEpisodeList(btn));
  });
}

// toggleIntroSkipEpisodeList: klappt die Episoden-Liste einer Serie auf/zu
// (in allen drei Job-Tabs nutzbar — der Endpoint liefert unabhängig vom
// Job-Status den aktuellen Stand jeder Episode). Lädt erst beim ersten
// Aufklappen (Lazy), cached das Ergebnis nicht — Retry kann sich zwischen
// zwei Aufklapp-Vorgängen geändert haben.
async function toggleIntroSkipEpisodeList(btn) {
  const wrap = btn.closest(".introskip-job-wrap");
  const list = wrap.querySelector(".introskip-episode-list");
  const expanded = !list.classList.contains("hidden");
  if (expanded) {
    list.classList.add("hidden");
    btn.textContent = "▸";
    return;
  }
  btn.textContent = "▾";
  list.classList.remove("hidden");
  list.innerHTML = `<div class="introskip-episode-row"><em style="color:#6b7280">Lädt…</em></div>`;
  const libId = btn.dataset.introskipExpandLib;
  const folder = btn.dataset.introskipExpandFolder;
  try {
    const episodes = await api(`/api/libraries/${libId}/introskip/episodes?folder=${encodeURIComponent(folder)}`);
    if (!episodes.length) {
      list.innerHTML = `<div class="introskip-episode-row"><em style="color:#6b7280">Keine Episoden gefunden.</em></div>`;
      return;
    }
    list.innerHTML = episodes.map(e => {
      const se = (e.season || e.episode) ? `S${String(e.season).padStart(2, "0")}E${String(e.episode).padStart(2, "0")} ` : "";
      let status;
      if (e.startSec != null && e.endSec != null) {
        status = `<span class="introskip-episode-status ok">✓ ${fmtDuration(e.startSec)}–${fmtDuration(e.endSec)}</span>`;
      } else if (e.checked) {
        status = `<span class="introskip-episode-status none">– kein Treffer</span>`;
      } else {
        status = `<span class="introskip-episode-status pending">⏳ noch nicht geprüft</span>`;
      }
      return `<div class="introskip-episode-row">
        <span class="introskip-episode-title">${se}${escapeHTML(e.title)}</span>
        ${status}
      </div>`;
    }).join("");
  } catch (e) {
    list.innerHTML = `<div class="introskip-episode-row"><em style="color:#6b7280">Fehler: ${escapeHTML(e.message)}</em></div>`;
  }
}
