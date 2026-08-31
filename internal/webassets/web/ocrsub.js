// ocrsub.js — Admin-Dialog für die OCR-Untertitel-Erzeugung.
//
// Reihenfolge in index.html: … → introskip → ocrsub → app.
//
// Bild-Untertitel (PGS/VOBSUB) werden per Tesseract-OCR im Hintergrund in
// einblendbare WebVTT-Text-Untertitel gewandelt. Opt-in pro Bibliothek
// (folder="" = ganze Lib). Struktur analog introskip.js (globaler Toggle +
// Auswahl-Liste + Job-Status-Tabs, .tp-tab-Klassen wiederverwendet).

function openOCRSubDialog() {
  state.ocrSubTab = state.ocrSubTab || "pending";
  const dlg = document.getElementById("ocrSubDialog");
  if (!dlg) {
    console.error("openOCRSubDialog: #ocrSubDialog nicht im DOM");
    if (typeof appAlert === "function") appAlert("OCR-Dialog fehlt (Seite neu laden).");
    return;
  }
  try {
    wireOCRSubDialogOnce();
    dlg.showModal();
  } catch (e) {
    console.error("openOCRSubDialog:", e);
    if (typeof appAlert === "function") appAlert("OCR-Dialog-Fehler: " + (e && e.message || e));
    return;
  }
  // Inhalt asynchron nachladen — Fehler dürfen den offenen Dialog nicht killen.
  Promise.resolve().then(refreshOCRSubStatus).catch(e => console.error("ocrsub status:", e));
  Promise.resolve().then(renderOCRSubLibraryList).catch(e => console.error("ocrsub libs:", e));
  Promise.resolve().then(refreshOCRSubJobTab).catch(e => console.error("ocrsub jobs:", e));
  // Solange der Dialog offen ist, Status alle 5 s aktualisieren.
  clearInterval(state._ocrSubPoll);
  state._ocrSubPoll = setInterval(() => {
    if (!dlg.open) { clearInterval(state._ocrSubPoll); return; }
    refreshOCRSubStatus().catch(() => {});
    refreshOCRSubJobTab().catch(() => {});
  }, 5000);
}

function wireOCRSubDialogOnce() {
  const dlg = $("#ocrSubDialog");
  if (dlg.dataset.wired) return;
  dlg.dataset.wired = "1";

  dlg.addEventListener("close", () => clearInterval(state._ocrSubPoll));

  $("#ocrSubGlobalToggle").addEventListener("change", async (e) => {
    const enabled = e.target.checked;
    try {
      await api("/api/ocrsubs/settings", { method: "PUT", body: JSON.stringify({ enabled }) });
      showToast(enabled ? "OCR-Verarbeitung aktiviert" : "OCR-Verarbeitung deaktiviert", { kind: "success" });
    } catch (err) {
      appAlert(err.message);
      e.target.checked = !enabled;
    }
  });

  $("#ocrSubRunAllBtn").addEventListener("click", async () => {
    const btn = $("#ocrSubRunAllBtn");
    btn.disabled = true;
    try {
      const res = await api("/api/ocrsubs/run", { method: "POST" });
      showToast(`${res.queued} Datei(en) in die Warteschlange gestellt`, { kind: "success" });
      await refreshOCRSubJobTab();
      await refreshOCRSubStatus();
    } catch (e) {
      appAlert(e.message);
    } finally {
      btn.disabled = false;
    }
  });

  $("#ocrSubRetryFailedBtn").addEventListener("click", async () => {
    try {
      const res = await api("/api/ocrsubs/retry-failed", { method: "POST" });
      showToast(`${res.retried} Auftrag/Aufträge erneut eingereiht`, { kind: "success" });
      await refreshOCRSubJobTab();
    } catch (e) {
      appAlert(e.message);
    }
  });

  document.querySelectorAll("[data-ocrsub-tab]").forEach(btn => {
    btn.addEventListener("click", () => {
      state.ocrSubTab = btn.dataset.tpTab;
      refreshOCRSubJobTab();
    });
  });
}

async function refreshOCRSubStatus() {
  let st;
  try { st = await api("/api/ocrsubs/status"); } catch { return; }
  $("#ocrSubGlobalToggle").checked = !!st.enabled;
  $("#ocrSubToolWarn").style.display = st.toolMissing ? "" : "none";
  $("#ocrSubGlobalToggle").disabled = !!st.toolMissing;
  $("#ocrSubRunAllBtn").disabled = !!st.toolMissing;
  const c = st.counts || {};
  const parts = [];
  if (c.pending) parts.push(`${c.pending} in Warteschlange`);
  if (c.running) parts.push(`${c.running} läuft`);
  if (c.done) parts.push(`${c.done} fertig`);
  if (c.failed) parts.push(`${c.failed} Fehler`);
  let line = parts.join(" · ") || "Keine Aufträge.";
  if (st.running && st.currentTitle) {
    line = `⚙ Läuft: ${escapeHTML(st.currentTitle)}` + (parts.length ? ` — ${parts.join(" · ")}` : "");
  }
  $("#ocrSubStatusLine").innerHTML = line;
}

async function renderOCRSubLibraryList() {
  const list = $("#ocrSubFolderList");
  list.innerHTML = `<li class="introskip-empty">Lädt…</li>`;
  let libs = [];
  try { libs = await api("/api/ocrsubs/folders"); }
  catch (e) { list.innerHTML = `<li class="introskip-empty">Fehler: ${escapeHTML(e.message)}</li>`; return; }
  if (!Array.isArray(libs)) libs = [];
  if (!libs.length) {
    list.innerHTML = `<li class="introskip-empty">Keine Bibliotheken.</li>`;
    return;
  }
  const kindIcon = { movies: "🎬", tv: "📺", private: "🔒" };
  list.innerHTML = libs.map(l => `
    <li class="introskip-folder-row" data-lib="${l.libraryId}">
      <label><input type="checkbox" ${l.enabled ? "checked" : ""}> ${kindIcon[l.kind] || "📁"} ${escapeHTML(l.name)}</label>
    </li>`).join("");
  if (!list.dataset.wired) {
    list.dataset.wired = "1";
    list.addEventListener("change", async (e) => {
      const cb = e.target.closest('input[type="checkbox"]');
      if (!cb) return;
      const row = cb.closest(".introskip-folder-row");
      const libId = Number(row.dataset.lib);
      cb.disabled = true;
      try {
        await api("/api/ocrsubs/folders", {
          method: "PUT",
          body: JSON.stringify({ libraryId: libId, folder: "", enabled: cb.checked }),
        });
        showToast(cb.checked ? "Bibliothek aktiviert — Backlog wird eingereiht" : "Bibliothek deaktiviert", { kind: "success" });
        await refreshOCRSubJobTab();
        await refreshOCRSubStatus();
      } catch (err) {
        appAlert(err.message);
        cb.checked = !cb.checked;
      } finally {
        cb.disabled = false;
      }
    });
  }
}

async function refreshOCRSubJobTab() {
  document.querySelectorAll("[data-ocrsub-tab]").forEach(b => {
    b.classList.toggle("primary", b.dataset.tpTab === state.ocrSubTab);
  });
  const logEl = $("#ocrSubJobList");
  let jobs = [];
  try { jobs = await api(`/api/ocrsubs/log?status=${state.ocrSubTab}`); }
  catch (e) { logEl.innerHTML = `<div class="trickplay-log-entry"><em style="color:#6b7280">Fehler: ${escapeHTML(e.message)}</em></div>`; return; }
  if (!Array.isArray(jobs)) jobs = [];
  if (!jobs.length) {
    logEl.innerHTML = `<div class="trickplay-log-entry"><em style="color:#6b7280">Keine Einträge.</em></div>`;
    return;
  }
  logEl.innerHTML = jobs.map(j => {
    const name = j.relPath || j.title || `Item ${j.itemId}`;
    let langs = "";
    if (j.langs) {
      // Bei fertigen Jobs: pro Sprache ein Link auf die erzeugte VTT (Diagnose).
      const parts = j.langs.split(",").map(l => l.trim()).filter(Boolean).map(l =>
        state.ocrSubTab === "done"
          ? `<a href="/api/ocr-subtitle/${j.itemId}/${encodeURIComponent(l)}.vtt" target="_blank" rel="noopener">${escapeHTML(l)}</a>`
          : escapeHTML(l)
      );
      langs = ` → ${parts.join(", ")}`;
    }
    const err = (state.ocrSubTab === "failed" && j.error)
      ? `<div class="err">✗ ${escapeHTML(j.error)}</div>` : "";
    const retryBtn = (state.ocrSubTab === "failed" || state.ocrSubTab === "done")
      ? `<button type="button" class="tp-row-retry" data-ocrsub-retry="${j.itemId}" title="Erneut">↻</button>` : "";
    return `<div class="trickplay-log-entry ${state.ocrSubTab}">
      <div class="tp-row-main">
        <span class="tp-row-path">${escapeHTML(name)}${langs}</span>
        ${err}
      </div>
      ${retryBtn}
    </div>`;
  }).join("");
  logEl.querySelectorAll("[data-ocrsub-retry]").forEach(btn => {
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try {
        await api(`/api/ocrsubs/items/${btn.dataset.ocrsubRetry}/retry`, { method: "POST" });
        await refreshOCRSubJobTab();
      } catch (e) {
        appAlert(e.message);
        btn.disabled = false;
      }
    });
  });
}
