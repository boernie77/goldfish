// whisper.js — KI-Untertitel-Generierung (Admin-Funktionen).
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views →
//   grid → player → admin → playlists → scan → matching → whisper → app
//
// Public Functions (alle global):
//   openWhisperDialog()         — öffnet Einstellungs-Dialog (Admin-Menü)
//   initSubGenBtn(item)         — verdrahtet 🎤-Button im Detail-Dialog
//   refreshSubGenPopover(item)  — aktualisiert Job-Status-Liste
//   startWhisperGlobalPoll()    — globaler Hintergrund-Poll (aus app.js aufgerufen)

// ============================================================
// Glocke / Benachrichtigungen
// ============================================================

const BELL_STORAGE_KEY = "gf_notifications";
const BELL_MAX = 50;

function bellLoad() {
  try { return JSON.parse(localStorage.getItem(BELL_STORAGE_KEY) || "[]"); } catch { return []; }
}
function bellSave(items) {
  localStorage.setItem(BELL_STORAGE_KEY, JSON.stringify(items.slice(0, BELL_MAX)));
}

function bellAdd(icon, title, sub) {
  const items = bellLoad();
  items.unshift({ icon, title, sub, time: Date.now(), unread: true });
  bellSave(items);
  renderBell();
}

function renderBell() {
  const items = bellLoad();
  const unread = items.filter(i => i.unread).length;
  const badge = $("#bellBadge");
  if (badge) {
    badge.textContent = unread > 9 ? "9+" : String(unread);
    badge.classList.toggle("hidden", unread === 0);
  }
  const list = $("#bellList");
  if (!list) return;
  if (!items.length) {
    list.innerHTML = `<div class="bell-empty">Keine Benachrichtigungen</div>`;
    return;
  }
  list.innerHTML = items.map(it => {
    const t = new Date(it.time);
    const timeStr = `${t.getHours().toString().padStart(2,"0")}:${t.getMinutes().toString().padStart(2,"0")}`;
    return `<div class="bell-item${it.unread ? " bell-item--unread" : ""}">
      <span class="bell-icon">${it.icon}</span>
      <div class="bell-body">
        <div class="bell-title">${escapeHTML(it.title)}</div>
        ${it.sub ? `<div class="bell-sub">${escapeHTML(it.sub)}</div>` : ""}
      </div>
      <span class="bell-time">${timeStr}</span>
    </div>`;
  }).join("");
}

function initBell() {
  renderBell();
  const btn = $("#bellBtn");
  const drop = $("#bellDropdown");
  if (!btn || !drop) return;
  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    drop.classList.toggle("hidden");
    if (!drop.classList.contains("hidden")) {
      // Alle als gelesen markieren
      const items = bellLoad().map(i => ({ ...i, unread: false }));
      bellSave(items);
      renderBell();
      renderBell(); // re-render list with unread-off
    }
  });
  const clearBtn = $("#bellClearBtn");
  if (clearBtn) clearBtn.addEventListener("click", () => {
    bellSave([]);
    renderBell();
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#bellDropdown, #bellBtn")) drop.classList.add("hidden");
  });
}

// ============================================================
// Globaler Whisper-Status-Poll (Hintergrund)
// ============================================================

let whisperGlobalPollTimer = null;
let whisperLastKnownJobs = {}; // itemId+lang → status

function startWhisperGlobalPoll() {
  if (whisperGlobalPollTimer) return;
  whisperGlobalPollTimer = setInterval(tickWhisperPoll, 5000);
}

async function tickWhisperPoll() {
  try {
    const st = await api("/api/whisper/status");
    updateWhisperStatusBar(st);
    checkWhisperJobCompletions();
  } catch (e) { /* ignore */ }
}

function updateWhisperStatusBar(st) {
  const bar = $("#whisperStatus");
  if (!bar) return;
  if (!st.running && st.queue === 0) {
    bar.classList.add("hidden");
    return;
  }
  bar.classList.remove("hidden");
  const phaseLabel = { transcribe: "Transkribiere", translate: "Übersetze" }[st.phase] || "Verarbeite";
  const pct = st.progressPct > 0 ? ` ${st.progressPct}%` : "";
  const queue = st.queue > 1 ? ` · ${st.queue - 1} weitere in Warteschlange` : "";
  bar.textContent = `🎤 ${phaseLabel}: ${st.currentTitle || "…"}${pct}${queue}`;
}

async function checkWhisperJobCompletions() {
  // Nur prüfen wenn wir aktive Jobs kennen
  if (!Object.keys(whisperLastKnownJobs).length) {
    // Beim ersten Aufruf: alle pending/running Jobs merken
    try {
      const st = await api("/api/whisper/status");
      if (st.currentItemId) whisperLastKnownJobs[`${st.currentItemId}`] = "running";
    } catch {}
    return;
  }
  // Für alle bekannten Items prüfen ob Jobs fertig geworden sind
  for (const key of Object.keys(whisperLastKnownJobs)) {
    const itemId = key.split(":")[0];
    try {
      const jobs = await api(`/api/items/${itemId}/subtitle-jobs`);
      for (const job of (jobs || [])) {
        const jobKey = `${job.itemId}:${job.language}`;
        const prev = whisperLastKnownJobs[jobKey];
        if ((prev === "pending" || prev === "running") && job.status === "done") {
          bellAdd("✅", `Untertitel fertig: ${langLabel(job.language)}`,
            `${job.language.toUpperCase()} · ${new Date().toLocaleDateString("de-DE")}`);
          showToast(`🎤 ${langLabel(job.language)}-Untertitel fertig`, { kind: "success", duration: 5000 });
        }
        if ((prev === "pending" || prev === "running") && job.status === "failed") {
          bellAdd("❌", `Untertitel fehlgeschlagen: ${langLabel(job.language)}`, job.error || "");
          showToast(`🎤 Fehler: ${langLabel(job.language)}`, { kind: "error", duration: 6000 });
        }
        whisperLastKnownJobs[jobKey] = job.status;
      }
    } catch {}
  }
}

// Wird aus dem Job-Trigger aufgerufen um Items zu tracken
function whisperTrackJob(itemId, lang) {
  whisperLastKnownJobs[`${itemId}:${lang}`] = "pending";
  startWhisperGlobalPoll();
}

// --- Einstellungs-Dialog (Admin) ---

async function openWhisperDialog() {
  const dlg = $("#whisperDialog");
  if (!dlg) return;

  // Aktuelle Settings laden
  try {
    const cfg = await api("/api/whisper/settings");
    const modelSel = $("#whisperModelSel");
    if (modelSel) {
      // Exakter Modell-Name aus Settings
      const modelName = cfg.modelName || "ggml-small";
      for (const opt of modelSel.options) {
        if (opt.value === modelName) { modelSel.value = modelName; break; }
      }
    }
    // Haken
    const radio = document.querySelector(`input[name="translationBackend"][value="${cfg.backend || "none"}"]`);
    if (radio) radio.checked = true;
    // API-Keys werden vom Server NIE zurückgegeben (sonst würde der maskierte
    // String beim nächsten Save den echten Key überschreiben). Stattdessen
    // bool deeplKeySet/libreKeySet → Placeholder im leeren Feld.
    const deeplInput = $("#tbDeeplKey");
    if (deeplInput) {
      deeplInput.value = "";
      deeplInput.placeholder = cfg.deeplKeySet
        ? "(gespeichert — leer lassen zum Behalten)"
        : "API-Key eintragen";
    }
    if (cfg.libreUrl) $("#tbLibreUrl").value = cfg.libreUrl;
    const libreInput = $("#tbLibreKey");
    if (libreInput) {
      libreInput.value = "";
      libreInput.placeholder = cfg.libreKeySet
        ? "(gespeichert — leer lassen zum Behalten)"
        : "(optional)";
    }
    updateWhisperBackendFields(cfg.backend || "none");
    // Modell-Status zeigen
    const statusEl = $("#whisperDownloadStatus");
    if (statusEl) {
      statusEl.textContent = cfg.modelAvailable
        ? `✓ Modell „${cfg.modelName}" ist installiert.`
        : `⚠ Kein Modell installiert — bitte herunterladen.`;
    }
  } catch (e) {
    console.warn("whisper settings load:", e);
  }

  // Radio-Buttons verdrahten (einmalig)
  if (!dlg.dataset.wired) {
    dlg.dataset.wired = "1";
    document.querySelectorAll("input[name='translationBackend']").forEach(r => {
      r.addEventListener("change", () => updateWhisperBackendFields(r.value));
    });

    // Download-Button
    const dlBtn = $("#whisperDownloadBtn");
    if (dlBtn) dlBtn.addEventListener("click", doWhisperDownload);

    // Einstellungen speichern
    const saveBtn = $("#whisperSaveSettingsBtn");
    if (saveBtn) saveBtn.addEventListener("click", saveWhisperSettings);
  }

  dlg.showModal();
}

function updateWhisperBackendFields(backend) {
  $("#tbDeeplFields").classList.toggle("hidden", backend !== "deepl");
  $("#tbLibreFields").classList.toggle("hidden", backend !== "libretranslate");
}

async function doWhisperDownload() {
  const modelSel = $("#whisperModelSel");
  const model = modelSel ? modelSel.value : "ggml-small";
  const btn = $("#whisperDownloadBtn");
  const statusEl = $("#whisperDownloadStatus");
  const bar = $("#whisperDownloadBar");
  const barInner = $("#whisperDownloadBarInner");
  if (btn) { btn.disabled = true; btn.textContent = "Starte Download…"; }
  if (statusEl) statusEl.textContent = "Download gestartet…";
  if (bar) bar.classList.remove("hidden");

  try {
    await api("/api/whisper/download-model", {
      method: "POST",
      body: JSON.stringify({ model }),
    });
    // Polling
    const poll = setInterval(async () => {
      try {
        const st = await api("/api/whisper/download-status");
        if (st.error) {
          clearInterval(poll);
          if (statusEl) statusEl.textContent = "Fehler: " + st.error;
          if (btn) { btn.disabled = false; btn.textContent = "⬇ Herunterladen"; }
          return;
        }
        if (barInner) barInner.style.width = `${st.progress}%`;
        if (statusEl) statusEl.textContent = `Lade ${model}… ${st.progress}%`;
        if (st.done) {
          clearInterval(poll);
          if (statusEl) statusEl.textContent = `✓ ${model} erfolgreich installiert.`;
          if (bar) bar.classList.add("hidden");
          if (btn) { btn.disabled = false; btn.textContent = "⬇ Herunterladen"; }
        }
      } catch (e) {
        clearInterval(poll);
        if (statusEl) statusEl.textContent = "Polling-Fehler: " + e.message;
        if (btn) { btn.disabled = false; btn.textContent = "⬇ Herunterladen"; }
      }
    }, 2000);
  } catch (e) {
    if (statusEl) statusEl.textContent = "Fehler: " + e.message;
    if (btn) { btn.disabled = false; btn.textContent = "⬇ Herunterladen"; }
  }
}

async function saveWhisperSettings() {
  const backend = document.querySelector("input[name='translationBackend']:checked");
  const body = {
    backend: backend ? backend.value : "none",
    deeplKey: ($("#tbDeeplKey") || {}).value || "",
    libreUrl: ($("#tbLibreUrl") || {}).value || "",
    libreKey: ($("#tbLibreKey") || {}).value || "",
  };
  const saveBtn = $("#whisperSaveSettingsBtn");
  const statusEl = $("#whisperSaveStatus");
  if (saveBtn) saveBtn.disabled = true;
  try {
    await api("/api/whisper/settings", { method: "PUT", body: JSON.stringify(body) });
    if (statusEl) {
      statusEl.textContent = "✓ Gespeichert";
      setTimeout(() => { statusEl.textContent = ""; }, 2500);
    }
  } catch (e) {
    if (statusEl) statusEl.textContent = "Fehler: " + e.message;
  } finally {
    if (saveBtn) saveBtn.disabled = false;
  }
}

// --- 🎤-Button im Detail-Dialog ---

let subGenPollTimer = null;
// Aktuell im Detail-Dialog gezeigtes Item. Sprach-Click-Listener wird einmalig
// gewired (s. dataset.wired) — würde er das Item via Closure capturen, würde
// jeder spätere Detail-Open mit anderem Item den ALTEN Listener belassen und
// der Klick „Italienisch" auf einem YouTube-Video würde fälschlich Untertitel
// für das vorher geöffnete Item generieren. Stattdessen lesen wir hier immer
// das aktuelle Item.
let currentSubGenItem = null;

function initSubGenBtn(item) {
  const btn = $("#detailSubGen");
  if (!btn) return;

  // Nur für Admins sichtbar
  if (!state.me || !state.me.isAdmin) {
    btn.classList.add("hidden");
    return;
  }
  btn.classList.remove("hidden");

  // WICHTIG: aktuelles Item für die einmal-gewireten Listener bereitstellen.
  currentSubGenItem = item;

  // Popover-Inhalt initial füllen
  refreshSubGenPopover(item);

  // Klick auf 🎤-Button: Popover toggeln. btn.onclick wird bei jedem Aufruf
  // überschrieben, hier ist eine Closure auf `item` OK.
  btn.onclick = (e) => {
    e.stopPropagation();
    const pop = $("#subGenPopover");
    if (!pop) return;
    pop.classList.toggle("hidden");
    if (!pop.classList.contains("hidden")) {
      refreshSubGenPopover(currentSubGenItem);
    }
  };

  // Klicks auf Sprach-Buttons (einmalig wiren — addEventListener würde sich
  // sonst pro Detail-Open stapeln)
  const pop = $("#subGenPopover");
  if (pop && !pop.dataset.wired) {
    pop.dataset.wired = "1";
    pop.querySelectorAll(".sub-gen-lang-btn").forEach(langBtn => {
      langBtn.addEventListener("click", async () => {
        const it = currentSubGenItem;
        if (!it) return;
        const lang = langBtn.dataset.lang;
        langBtn.disabled = true;
        langBtn.textContent = "⏳ " + langBtn.textContent.replace(/^⏳ /, "");
        try {
          await api(`/api/items/${it.id}/generate-subtitle`, {
            method: "POST",
            body: JSON.stringify({ language: lang }),
          });
          showToast(`Untertitel (${langLabel(lang)}) in Warteschlange`, { kind: "success" });
          whisperTrackJob(it.id, lang);
          refreshSubGenPopover(it);
          startSubGenPolling(it);
        } catch (err) {
          showToast("Fehler: " + err.message, { kind: "error" });
          langBtn.disabled = false;
        }
      });
    });
    // Klick außerhalb schließt Popover
    document.addEventListener("click", (e) => {
      if (!e.target.closest("#subGenPopover") && !e.target.closest("#detailSubGen")) {
        pop.classList.add("hidden");
      }
    }, { capture: true });
  }
}

async function refreshSubGenPopover(item) {
  const el = $("#subGenJobs");
  if (!el) return;
  try {
    const jobs = await api(`/api/items/${item.id}/subtitle-jobs`);
    if (!jobs || !jobs.length) {
      el.innerHTML = "<div class='hint'>Noch keine generierten Untertitel.</div>";
      return;
    }
    el.innerHTML = jobs.map(j => {
      const statusIcon = { done: "✓", pending: "⏳", running: "⚙", failed: "✗" }[j.status] || "?";
      const statusClass = `sub-job-status--${j.status}`;
      const deleteBtn = j.status === "done" || j.status === "failed"
        ? `<button type="button" class="sub-job-delete" data-lang="${j.language}" title="Entfernen">✕</button>`
        : "";
      return `<div class="sub-job-row">
        <span class="${statusClass}">${statusIcon}</span>
        <span class="sub-job-lang">${langLabel(j.language)}</span>
        <span class="hint sub-job-err">${j.error ? escapeHTML(j.error) : ""}</span>
        ${deleteBtn}
      </div>`;
    }).join("");

    // Delete-Handler
    el.querySelectorAll(".sub-job-delete").forEach(del => {
      del.addEventListener("click", async () => {
        const lang = del.dataset.lang;
        try {
          await api(`/api/items/${item.id}/subtitle/${lang}`, { method: "DELETE" });
          refreshSubGenPopover(item);
          showToast(`Untertitel (${langLabel(lang)}) entfernt`, { kind: "info" });
        } catch (err) {
          showToast("Fehler: " + err.message, { kind: "error" });
        }
      });
    });

    // Laufende/Pending Jobs → Polling starten
    const active = jobs.some(j => j.status === "pending" || j.status === "running");
    if (active) startSubGenPolling(item);

  } catch (e) {
    el.innerHTML = `<div class="hint">Fehler: ${escapeHTML(e.message)}</div>`;
  }
}

function startSubGenPolling(item) {
  if (subGenPollTimer) return; // läuft bereits
  subGenPollTimer = setInterval(async () => {
    // Wenn Dialog geschlossen ist, aufhören
    if (!$("#detailDialog").open) {
      stopSubGenPolling();
      return;
    }
    const jobs = await api(`/api/items/${item.id}/subtitle-jobs`).catch(() => []);
    const active = (jobs || []).some(j => j.status === "pending" || j.status === "running");
    refreshSubGenPopover(item);
    if (!active) stopSubGenPolling();
  }, 5000);
}

function stopSubGenPolling() {
  if (subGenPollTimer) { clearInterval(subGenPollTimer); subGenPollTimer = null; }
}

function langLabel(code) {
  return { de: "Deutsch", en: "Englisch", it: "Italienisch" }[code] || code;
}
