// admin.js — Admin-only-Bereiche der App.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views → grid → player → admin → app
//
// Public Functions: User-Menü + Admin-Panel, Manage Libraries (CRUD,
// Multi-Path, ACL, Home-Visibility, Trickplay-Folder-Toggle), Path-Browser
// (admin-only unter /media), Settings (Buffer-Sec, TMDB/OMDb-Keys,
// HW-Accel, Trickplay-Intervall).
//
// Referenziert state, api, appAlert/appConfirm/appPrompt, loadItems,
// invalidateItemsCache und diverse render*-Funktionen aus anderen Modulen
// — Aufrufe zur Laufzeit.

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
  $("#settingsMenuAutoScan").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuTrickplay").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuWhisper").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuIntroSkip").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuPathSearch").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuMissing").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuRefreshAllMeta").classList.toggle("hidden", !state.me.isAdmin);
  // Das gesamte Zahnrad-Menü ist Admin-only: alle übrigen Einträge (Settings,
  // Bibliotheken) sind ebenfalls administrative Aktionen. Für reguläre User
  // wird der Button komplett ausgeblendet.
  $("#settingsBtn").classList.toggle("hidden", !state.me.isAdmin);

  // Server-Version im Menü-Fuß (aus /api/health). Einmalig, still bei Fehler.
  const verEl = $("#drawerVersion");
  if (verEl && verEl.textContent === "—") {
    fetch("/api/health").then(r => r.json()).then(h => {
      if (h && h.version) verEl.textContent = "v" + h.version;
    }).catch(() => {});
  }
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
            <button type="button" class="user-btn danger user-del-btn" title="Benutzer löschen">${ICON_TRASH_SVG} Löschen</button>
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

// --- Manage Libraries ---

async function openManage() {
  await loadLibraries();
  const ul = $("#libList");
  ul.innerHTML = "";
  if (!state.libraries.length) {
    ul.innerHTML = `<li><em>Noch keine Bibliotheken angelegt.</em></li>`;
  }
  // Reihenfolge nach Drag/Up-Down zum Server schicken.
  async function persistOrder() {
    const ids = Array.from(ul.querySelectorAll("li.lib-row"))
      .map(li => Number(li.dataset.libId))
      .filter(id => id > 0);
    try {
      await api(`/api/libraries/order`, { method: "PUT", body: JSON.stringify({ ids }) });
      await loadLibraries();
    } catch (e) { appAlert(e.message); }
  }
  for (let idx = 0; idx < state.libraries.length; idx++) {
    const l = state.libraries[idx];
    const li = document.createElement("li");
    li.classList.add("lib-row");
    li.dataset.libId = String(l.id);
    const kindLabel = l.kind === "movies" ? "Filme" : l.kind === "tv" ? "Serien" : "Privat";

    // Zwei-Zeilen-Layout fuer die schmale 520px-Dialog-Breite:
    //   Zeile 1: Name + Kind-Select + Loeschen-Icon  (gross/lesbar)
    //   Zeile 2: ▲▼ + Toggle-Pillen                  (kompakt)
    // Beide Container haben flex-wrap, damit bei sehr schmalen Viewports
    // (z.B. Tablet hochkant) nichts mehr aus dem Dialog rausragt.
    const header = document.createElement("div");
    header.className = "lib-row-head";

    const topRow = document.createElement("div");
    topRow.className = "lib-row-top";

    const nameBox = document.createElement("div");
    nameBox.className = "lib-name";
    nameBox.innerHTML = `<strong>${escapeHTML(l.name)}</strong>`;
    topRow.appendChild(nameBox);

    const kindSel = document.createElement("select");
    kindSel.className = "lib-kind-select";
    kindSel.title = "Bibliothekstyp";
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
    topRow.appendChild(kindSel);

    const del = document.createElement("button");
    del.className = "danger lib-del";
    del.title = "Bibliothek löschen";
    del.innerHTML = ICON_TRASH_SVG;
    del.addEventListener("click", async () => {
      if (!(await appConfirm(`Bibliothek "${l.name}" löschen?`))) return;
      await api(`/api/libraries/${l.id}`, { method: "DELETE" });
      await loadLibraries();
      openManage();
      loadItems();
    });
    topRow.appendChild(del);

    header.appendChild(topRow);

    // Zeile 2: Reorder + Toggles
    const toolbar = document.createElement("div");
    toolbar.className = "lib-toolbar";

    const upBtn = document.createElement("button");
    upBtn.className = "icon-btn";
    upBtn.textContent = "▲";
    upBtn.title = "Nach oben verschieben";
    upBtn.disabled = idx === 0;
    upBtn.addEventListener("click", async () => {
      const prev = li.previousElementSibling;
      if (prev && prev.classList.contains("lib-row")) {
        ul.insertBefore(li, prev);
        await persistOrder();
        openManage();
      }
    });
    const downBtn = document.createElement("button");
    downBtn.className = "icon-btn";
    downBtn.textContent = "▼";
    downBtn.title = "Nach unten verschieben";
    downBtn.disabled = idx === state.libraries.length - 1;
    downBtn.addEventListener("click", async () => {
      const next = li.nextElementSibling;
      if (next && next.classList.contains("lib-row")) {
        ul.insertBefore(next, li);
        await persistOrder();
        openManage();
      }
    });
    toolbar.appendChild(upBtn);
    toolbar.appendChild(downBtn);

    const onHomeLabel = document.createElement("label");
    onHomeLabel.className = "lib-toggle";
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
    toolbar.appendChild(onHomeLabel);

    // Card-Layout-Toggle nur bei Privat-Libs anzeigen — bei Filme/Serien
    // ist sowieso der Titel oben (Kanal-Layout greift dort nicht).
    if (l.kind === "private") {
      const layoutLabel = document.createElement("label");
      layoutLabel.className = "lib-toggle";
      layoutLabel.title = "Top-Zeile: Ordner/Kanal statt Titel (YouTube-Style)";
      const layoutBox = document.createElement("input");
      layoutBox.type = "checkbox";
      layoutBox.checked = l.channelLabelOnTop !== false;
      layoutBox.addEventListener("change", async () => {
        try {
          await api(`/api/libraries/${l.id}/channel-label-on-top`, {
            method: "PUT",
            body: JSON.stringify({ channelLabelOnTop: layoutBox.checked }),
          });
          await loadLibraries();
        } catch (e) { appAlert(e.message); }
      });
      layoutLabel.appendChild(layoutBox);
      layoutLabel.appendChild(document.createTextNode(" 🏷 Ordner oben"));
      toolbar.appendChild(layoutLabel);
    }

    header.appendChild(toolbar);
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
  $("#autoRenameToggle").checked = !!state.settings.autoRenameConfirmedMovies;
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
    autoRenameConfirmedMovies: !!$("#autoRenameToggle").checked,
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

// --- Verschieben (Einzel- oder Bulk) ---
//
// Nutzt dieselbe rename_history-Infrastruktur wie der Auto-Rename: ein Move
// ist serverseitig einfach ein Rename mit geändertem Verzeichnisanteil
// (internal/api/admin_rename.go executeMove). Ziel-Ordner-Auswahl als
// Baumstruktur (alle vorhandenen Ordnerpfade der Ziel-Library), Klick auf
// eine Zeile übernimmt den Pfad ins Textfeld; neue Ordnernamen bleiben frei
// eintippbar.

let moveTreeFolders = [];      // Ordnerpfade der aktuell im Dialog gewählten Ziel-Library
let moveTreeExpanded = new Set(); // Set von Pfaden, die im Baum aufgeklappt sind

// buildFolderTree: flache Pfadliste ("a", "a/Urlaub", "a/Urlaub/2024", …) zu
// einem verschachtelten Objektbaum {children:{name:{path,children:{...}}}}.
function buildFolderTree(paths) {
  const root = { children: {} };
  for (const p of paths) {
    const segs = p.split("/").filter(Boolean);
    let node = root;
    const acc = [];
    for (const seg of segs) {
      acc.push(seg);
      const key = acc.join("/");
      node.children = node.children || {};
      if (!node.children[seg]) node.children[seg] = { name: seg, path: key, children: null };
      node = node.children[seg];
    }
  }
  return root;
}

function renderMoveTreeChildren(node, selectedPath) {
  if (!node.children) return "";
  const keys = Object.keys(node.children).sort((a, b) => a.localeCompare(b, "de", { numeric: true, sensitivity: "base" }));
  return keys.map(k => {
    const child = node.children[k];
    const hasChildren = !!(child.children && Object.keys(child.children).length);
    const isSelected = child.path === selectedPath;
    // Vorfahren des ausgewählten Pfads immer aufklappen, damit der aktuelle
    // Ordner beim Öffnen/Auswählen sichtbar ist, nicht in eingeklappten Ästen versteckt.
    const isAncestor = !!selectedPath && selectedPath.startsWith(child.path + "/");
    const expanded = hasChildren && (moveTreeExpanded.has(child.path) || isAncestor);
    return `
      <li class="move-tree-item">
        <div class="move-tree-row ${isSelected ? "is-selected" : ""}" data-path="${escapeHTML(child.path)}">
          <button type="button" class="move-tree-toggle ${hasChildren ? (expanded ? "is-open" : "") : "move-tree-toggle--leaf"}" ${hasChildren ? `data-toggle="${escapeHTML(child.path)}"` : ""}>▸</button>
          <span class="move-tree-label">📁 ${escapeHTML(k)}</span>
        </div>
        ${hasChildren ? `<ul class="move-tree-list" style="${expanded ? "" : "display:none"}">${renderMoveTreeChildren(child, selectedPath)}</ul>` : ""}
      </li>`;
  }).join("");
}

function renderMoveTree(selectedPath) {
  const container = $("#moveFolderTree");
  const tree = buildFolderTree(moveTreeFolders);
  const rootRow = `
    <div class="move-tree-row ${!selectedPath ? "is-selected" : ""}" data-path="">
      <button type="button" class="move-tree-toggle move-tree-toggle--leaf"></button>
      <span class="move-tree-label">🏠 (Bibliotheks-Wurzel)</span>
    </div>`;
  const childrenHtml = renderMoveTreeChildren(tree, selectedPath);
  container.innerHTML = moveTreeFolders.length
    ? `${rootRow}<ul class="move-tree-list">${childrenHtml}</ul>`
    : `${rootRow}<div class="move-tree-empty">Noch keine Unterordner in dieser Bibliothek.</div>`;
  if (!container.dataset.wired) {
    container.dataset.wired = "1";
    container.addEventListener("click", (e) => {
      const toggle = e.target.closest("[data-toggle]");
      if (toggle) {
        e.stopPropagation();
        const path = toggle.dataset.toggle;
        if (moveTreeExpanded.has(path)) moveTreeExpanded.delete(path);
        else moveTreeExpanded.add(path);
        renderMoveTree($("#moveFolderInput").value.trim());
        return;
      }
      const row = e.target.closest(".move-tree-row");
      if (row) {
        $("#moveFolderInput").value = row.dataset.path || "";
        renderMoveTree(row.dataset.path || "");
      }
    });
    // Manuell getipptes Ziel im Baum mit-highlighten/aufklappen.
    $("#moveFolderInput").addEventListener("input", (e) => {
      renderMoveTree(e.target.value.trim());
    });
  }
}

// loadMoveFolderList: lädt alle Ordnerpfade der gewählten Ziel-Bibliothek neu
// und baut den Baum — initial und bei jedem Wechsel des Ziel-Bibliotheks-Dropdowns.
async function loadMoveFolderList(libId, selectedPath) {
  moveTreeFolders = [];
  moveTreeExpanded = new Set();
  try {
    moveTreeFolders = await api(`/api/libraries/${libId}/all-folders`);
  } catch {}
  renderMoveTree(selectedPath || "");
}

async function openMoveDialog(ctx) {
  state.moveContext = ctx;
  const dlg = $("#moveDialog");
  const title = $("#moveDialogTitle");
  const hint = $("#moveDialogHint");
  const input = $("#moveFolderInput");
  const libSelect = $("#moveLibrarySelect");
  let libId, currentFolder = "";
  if (ctx.mode === "single") {
    const it = ctx.item;
    libId = it.libraryId;
    title.textContent = "Verschieben";
    hint.textContent = (it.metadata && it.metadata.title) || it.title || it.relPath || "";
    const segs = (it.relPath || "").split("/").filter(Boolean);
    segs.pop();
    currentFolder = segs.join("/");
  } else {
    // Alle ausgewählten Items müssen aus derselben Quell-Library kommen —
    // Verschieben liest den Ordnerpfad relativ zu genau einem Root.
    const selectedItems = (state.lastRenderedItems || []).filter(it => ctx.ids.includes(it.id));
    const libIds = new Set(selectedItems.map(it => it.libraryId));
    if (libIds.size > 1) {
      appAlert("Verschieben funktioniert nur mit Items aus derselben Quell-Bibliothek — die Auswahl enthält Items aus mehreren Bibliotheken.");
      return;
    }
    libId = selectedItems[0] ? selectedItems[0].libraryId : state.currentLibrary;
    title.textContent = `${ctx.ids.length} Videos verschieben`;
    hint.textContent = `${ctx.ids.length} ausgewählte Datei${ctx.ids.length === 1 ? "" : "en"}`;
    currentFolder = state.currentFolder || "";
  }
  state.moveContext.libId = libId;
  // Ziel-Bibliothek: Dropdown mit allen Libraries, Default = Quell-Library
  // (bleibt normalerweise gleich — Ziel-Wechsel ist der Ausnahmefall).
  libSelect.innerHTML = state.libraries.map(l =>
    `<option value="${l.id}" ${l.id == libId ? "selected" : ""}>${libIcon(l)} ${escapeHTML(l.name)}</option>`
  ).join("");
  libSelect.onchange = () => {
    input.value = ""; // Ordner der Quell-Library ergibt in einer anderen Lib meist keinen Sinn
    loadMoveFolderList(Number(libSelect.value), "");
  };
  input.value = currentFolder;
  await loadMoveFolderList(libId, currentFolder);
  dlg.showModal();
  input.focus();
  input.select();
}

async function handleMoveSubmit(e) {
  e.preventDefault();
  const ctx = state.moveContext;
  if (!ctx) return;
  const targetFolder = $("#moveFolderInput").value.trim();
  const targetLibraryId = Number($("#moveLibrarySelect").value);
  const submitBtn = e.target.querySelector('button[type="submit"]');
  submitBtn.disabled = true;
  try {
    if (ctx.mode === "single") {
      await api(`/api/items/${ctx.item.id}/move`, {
        method: "POST",
        body: JSON.stringify({ targetFolder, targetLibraryId }),
      });
      showToast("Verschoben", { kind: "success" });
      $("#moveDialog").close();
      $("#detailDialog").close();
      invalidateItemsCache();
      loadItems();
    } else {
      const res = await api(`/api/items/move`, {
        method: "POST",
        body: JSON.stringify({ ids: ctx.ids, targetFolder, targetLibraryId }),
      });
      $("#moveDialog").close();
      if (res.failed > 0) {
        showToast(`${res.moved} verschoben, ${res.failed} fehlgeschlagen`, { kind: res.moved ? "info" : "error" });
      } else {
        showToast(`${res.moved} verschoben`, { kind: "success" });
      }
      setSelectionMode(false);
      invalidateItemsCache();
      loadItems();
    }
  } catch (err) {
    appAlert("Verschieben fehlgeschlagen: " + err.message);
  } finally {
    submitBtn.disabled = false;
  }
}

// --- Auto-Rename: Umbenennungen-Manager ---
//
// Liste aller Datei-Umbenennungen, einzeln rueckgaengig machbar, plus
// CSV-Export und „Alle bestaetigten umbenennen"-Bulk-Button. Nutzt die
// /api/admin/renames-Endpoints (admin-only).

// --- Statistik ---

// openStatistikDialog: Scope ist immer der Kontext, aus dem der User das
// Menü öffnet — state.currentLibrary + state.currentFolder (gleiche
// Konvention wie der Folder-gescopte Scan-Button: im Library-Root zählt die
// ganze Bibliothek, in einem Unterordner nur dessen Inhalt rekursiv).
async function openStatistikDialog() {
  const dlg = $("#statistikDialog");
  const scopeEl = $("#statistikScope");
  const body = $("#statistikBody");
  if (!state.currentLibrary) {
    scopeEl.textContent = "";
    body.innerHTML = `<div class="hint">Bitte zuerst eine Bibliothek öffnen — die Statistik bezieht sich immer auf die aktuell geöffnete Bibliothek oder den aktuellen Ordner.</div>`;
    dlg.showModal();
    return;
  }
  const lib = (state.libraries || []).find(l => l.id == state.currentLibrary);
  const folder = state.currentFolder || "";
  scopeEl.textContent = folder
    ? `${lib ? lib.name : "Bibliothek"} → ${folder}`
    : (lib ? lib.name : "Bibliothek") + " (gesamt)";
  body.innerHTML = `<div class="hint">Lade…</div>`;
  dlg.showModal();
  let d;
  try {
    const q = folder ? `?folder=${encodeURIComponent(folder)}` : "";
    d = await api(`/api/libraries/${state.currentLibrary}/stats-detail${q}`);
  } catch (e) {
    body.innerHTML = `<div class="hint">Fehler: ${escapeHTML(e.message)}</div>`;
    return;
  }
  body.innerHTML = renderStatistikBody(d);
}

function renderStatBarSection(title, buckets) {
  if (!buckets || !buckets.length) return "";
  const max = Math.max(...buckets.map(b => b.count));
  const rows = buckets.map(b => {
    const pct = max > 0 ? Math.round((b.count / max) * 100) : 0;
    return `
      <div class="stat-bar-row" style="display:flex;align-items:center;gap:8px;margin:4px 0">
        <div style="width:90px;font-size:12px;color:#94a3b8;text-align:right;flex-shrink:0">${escapeHTML(b.label)}</div>
        <div style="flex:1;background:#1e293b;border-radius:4px;overflow:hidden;height:18px">
          <div style="width:${pct}%;background:#3b82f6;height:100%"></div>
        </div>
        <div style="width:44px;font-size:12px;text-align:right;flex-shrink:0">${b.count}</div>
      </div>`;
  }).join("");
  return `<h3 style="margin:16px 0 4px;font-size:14px">${escapeHTML(title)}</h3>${rows}`;
}

// Gesamtlaufzeit einer Bibliothek/eines Ordners kann tausende Stunden sein —
// fmtDuration() (für Player-Zeitanzeigen gedacht, "H:MM:SS") wäre hier
// unleserlich. Eigenes Format "X Std Y Min".
function fmtTotalDuration(sec) {
  const total = Math.round(sec || 0);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  if (h === 0) return `${m} Min`;
  return `${h} Std ${m} Min`;
}

function renderStatistikBody(d) {
  if (!d.totalCount) {
    return `<div class="hint">Keine Dateien in diesem Bereich.</div>`;
  }
  const totals = `
    <div class="row" style="gap:20px;margin-bottom:8px">
      <div><strong>${d.totalCount}</strong> Datei${d.totalCount === 1 ? "" : "en"}</div>
      <div><strong>${fmtSize(d.totalSizeBytes)}</strong></div>
      <div><strong>${fmtTotalDuration(d.totalDurationSec)}</strong> Gesamtlaufzeit</div>
    </div>`;
  return totals
    + renderStatBarSection("Auflösung", d.byResolution)
    + renderStatBarSection("Filetyp", d.byContainer)
    + renderStatBarSection("Länge", d.byDuration);
}

async function openRenamesManager() {
  $("#renamesDialog").showModal();
  await refreshRenamesManager();
}

async function refreshRenamesManager() {
  const body = $("#renamesBody");
  body.innerHTML = `<div class="hint">Lade…</div>`;
  let list;
  try {
    list = await api("/api/admin/renames");
  } catch (e) {
    body.innerHTML = `<div class="hint">Fehler: ${escapeHTML(e.message)}</div>`;
    return;
  }
  if (!list.length) {
    body.innerHTML = `<div class="hint">Noch keine Umbenennungen protokolliert.</div>`;
    return;
  }
  // Tabelle bauen. Spalten: Datum · Trigger · Vorher → Nachher · Status · Aktion
  const rows = list.map(e => {
    const dt = new Date(e.renamedAt).toLocaleString("de-DE");
    const oldBase = (e.oldPath || "").split("/").pop();
    const newBase = (e.newPath || "").split("/").pop();
    const undone = e.undoneAt && e.undoneAt !== "0001-01-01T00:00:00Z";
    const status = undone
      ? `<span style="color:#94a3b8">↩ rückgängig</span>`
      : `<span style="color:#22c55e">aktiv</span>`;
    const action = undone
      ? ""
      : `<button type="button" class="rename-undo-btn" data-id="${e.id}" title="Rückgängig">↩</button>`;
    const triggerLabel = ({auto: "auto", manual: "manuell", bulk: "bulk", move: "verschoben"})[e.triggeredBy] || e.triggeredBy;
    return `
      <tr>
        <td style="white-space:nowrap;font-size:12px">${escapeHTML(dt)}</td>
        <td style="font-size:12px"><span class="rename-trigger rename-trigger-${escapeHTML(e.triggeredBy)}">${escapeHTML(triggerLabel)}</span></td>
        <td style="font-size:12px"><div style="color:#94a3b8">${escapeHTML(oldBase)}</div><div>→ ${escapeHTML(newBase)}</div></td>
        <td style="font-size:12px">${status}</td>
        <td>${action}</td>
      </tr>`;
  }).join("");
  body.innerHTML = `
    <table style="width:100%;border-collapse:collapse">
      <thead>
        <tr style="text-align:left;color:#94a3b8;border-bottom:1px solid #334">
          <th style="padding:6px 4px">Datum</th>
          <th style="padding:6px 4px">Auslöser</th>
          <th style="padding:6px 4px">Datei</th>
          <th style="padding:6px 4px">Status</th>
          <th style="padding:6px 4px"></th>
        </tr>
      </thead>
      <tbody>${rows}</tbody>
    </table>`;
  // Click-Handler fuer Undo-Buttons (Event-Delegation auf body).
  body.querySelectorAll(".rename-undo-btn").forEach(btn => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.id;
      if (!(await appConfirm("Umbenennung rückgängig machen? Datei wird zum alten Namen zurückbenannt."))) return;
      try {
        await api(`/api/admin/renames/${id}/undo`, { method: "POST" });
        showToast("Rückgängig gemacht", { kind: "success" });
        await refreshRenamesManager();
        invalidateItemsCache();
        loadItems();
      } catch (e) { appAlert("Undo fehlgeschlagen: " + e.message); }
    });
  });
}

async function runBulkRenameConfirmed() {
  if (!(await appConfirm(
    `ALLE bestätigten Filme umbenennen?\n\n` +
    `Jede Datei wird zu 'Titel (Jahr).ext' umgeschrieben. Jede einzelne ` +
    `Aktion landet in der Historie und kann dort rückgängig gemacht werden. ` +
    `Episoden werden NICHT angefasst. Auch Bibliotheken vom Typ TV oder Privat bleiben unberührt.`
  ))) return;
  showToast("Bulk-Umbenennung läuft…", { kind: "info", duration: 2000 });
  try {
    const r = await api("/api/admin/rename-all-confirmed", { method: "POST" });
    let msg = `Bulk-Rename fertig.\n\nGesamt: ${r.total}\nUmbenannt: ${r.renamed}\nÜbersprungen (schon korrekt): ${r.skipped}\nFehler: ${r.failed}`;
    if (r.failures && r.failures.length) {
      msg += "\n\nErste Fehler:\n• " + r.failures.slice(0, 10).join("\n• ");
    }
    appAlert(msg);
    await refreshRenamesManager();
    invalidateItemsCache();
    loadItems();
  } catch (e) { appAlert("Bulk-Rename fehlgeschlagen: " + e.message); }
}

function downloadRenamesCSV() {
  // Cookie-Auth: einfach Browser-Download via Location-Change.
  window.location.href = "/api/admin/renames.csv";
}

// --- Auto-Scan-Einstellungen (mehrere Aufgaben) ---

// Arbeitskopie der Aufgabenliste — wird beim Speichern an den Server gesendet.
let autoScanTasks = [];
let autoScanLibs  = [];

async function openAutoScan() {
  const dlg = $("#autoScanDialog");
  if (!dlg) return;

  // Libraries laden
  try {
    autoScanLibs = await apiGetCached("/api/libraries");
  } catch { autoScanLibs = []; }

  // Aufgaben laden
  try {
    const data = await api("/api/settings/autoscan");
    autoScanTasks = Array.isArray(data) ? data : [];
  } catch { autoScanTasks = []; }

  autoScanRenderTasks();

  // Event-Listener einmalig verdrahten
  if (!dlg._asWired) {
    dlg._asWired = true;
    $("#autoScanAddBtn").addEventListener("click", autoScanAddTask);
    $("#autoScanSaveBtn").addEventListener("click", saveAutoScan);
  }

  if (!dlg.open) dlg.showModal();
}

function autoScanRenderTasks() {
  const list = $("#autoScanTaskList");
  if (!list) return;
  list.innerHTML = "";
  if (autoScanTasks.length === 0) {
    list.innerHTML = '<p style="color:var(--muted);font-size:.85em">Noch keine Aufgaben. Klicke „+ Aufgabe hinzufügen".</p>';
    return;
  }
  autoScanTasks.forEach((task, idx) => {
    list.appendChild(autoScanTaskCard(task, idx));
  });
}

function autoScanTaskCard(task, idx) {
  const card = document.createElement("div");
  card.className = "autoscan-task-card";
  card.style.cssText = "border:1px solid var(--border);border-radius:8px;padding:12px;display:flex;flex-direction:column;gap:8px;position:relative";

  // Enabled-Toggle + Löschen in einer Zeile
  const head = document.createElement("div");
  head.style.cssText = "display:flex;align-items:center;gap:8px";
  const chk = document.createElement("input");
  chk.type = "checkbox"; chk.checked = !!task.enabled;
  chk.title = "Aufgabe aktiv"; chk.style.cssText = "width:15px;height:15px;cursor:pointer";
  chk.addEventListener("change", () => { autoScanTasks[idx].enabled = chk.checked; });
  const lbl = document.createElement("span");
  lbl.style.cssText = "font-weight:600;flex:1";
  lbl.textContent = `Aufgabe ${idx + 1}`;
  const del = document.createElement("button");
  del.innerHTML = ICON_TRASH_SVG; del.title = "Aufgabe löschen";
  del.style.cssText = "background:none;border:none;cursor:pointer;opacity:.6;font-size:1em;padding:2px 4px";
  del.addEventListener("click", () => { autoScanTasks.splice(idx, 1); autoScanRenderTasks(); });
  head.append(chk, lbl, del);
  card.appendChild(head);

  // Zeitplan
  const schedRow = document.createElement("div");
  schedRow.style.cssText = "display:flex;gap:8px;align-items:center;flex-wrap:wrap";
  const modeSel = autoScanModeSelect(task, idx);
  schedRow.appendChild(modeSel.container);
  card.appendChild(schedRow);

  // Zeitfeld (wird vom modeSel befüllt/versteckt)
  const timeRow = modeSel.timeRow;
  card.appendChild(timeRow);

  // Library + Scan-Typ
  const botRow = document.createElement("div");
  botRow.style.cssText = "display:flex;gap:8px;align-items:center;flex-wrap:wrap";

  const libSel = autoScanLibSelect(task, idx);
  botRow.appendChild(libSel);

  const forceLbl = document.createElement("label");
  forceLbl.style.cssText = "display:flex;align-items:center;gap:5px;cursor:pointer;font-size:.85em;white-space:nowrap";
  const forceChk = document.createElement("input");
  forceChk.type = "checkbox"; forceChk.checked = !!task.force;
  forceChk.addEventListener("change", () => { autoScanTasks[idx].force = forceChk.checked; });
  forceLbl.append(forceChk, document.createTextNode("Vollständig (force)"));
  botRow.appendChild(forceLbl);

  card.appendChild(botRow);

  // Zusammenfassung
  const sum = document.createElement("div");
  sum.style.cssText = "font-size:.8em;color:var(--muted)";
  sum.textContent = autoScanTaskSummary(task);
  card.appendChild(sum);

  return card;
}

function autoScanModeSelect(task, idx) {
  const container = document.createElement("div");
  container.style.cssText = "display:flex;gap:6px;align-items:center";

  const modeSel = document.createElement("select");
  modeSel.style.cssText = "padding:4px 6px;border-radius:5px;border:1px solid var(--border);background:var(--surface);color:var(--text);font-size:.85em";
  [["daily","Täglich"],["every","Alle X Stunden"],["weekly","Wöchentlich"]].forEach(([v,l]) => {
    const o = document.createElement("option"); o.value = v; o.textContent = l; modeSel.appendChild(o);
  });
  const parts = (task.schedule || "daily:03:00").split(":");
  modeSel.value = parts[0] || "daily";
  container.appendChild(modeSel);

  // Zeitfeld-Container — Inhalt wird je nach Modus gewechselt
  const timeRow = document.createElement("div");
  timeRow.style.cssText = "display:flex;gap:6px;align-items:center;flex-wrap:wrap";

  const rebuildTime = (mode) => {
    timeRow.innerHTML = "";
    if (mode === "daily") {
      const p = task.schedule.startsWith("daily:") ? task.schedule.split(":") : ["daily","3","0"];
      const inp = document.createElement("input"); inp.type = "time";
      inp.value = `${(p[1]||"3").padStart(2,"0")}:${(p[2]||"0").padStart(2,"0")}`;
      inp.style.cssText = "padding:4px;border-radius:5px;border:1px solid var(--border);background:var(--surface);color:var(--text);font-size:.85em";
      inp.addEventListener("change", () => {
        const t = inp.value.split(":");
        autoScanTasks[idx].schedule = `daily:${parseInt(t[0],10)}:${parseInt(t[1],10)}`;
      });
      timeRow.appendChild(inp);
    } else if (mode === "every") {
      const hSel = document.createElement("select");
      hSel.style.cssText = modeSel.style.cssText;
      [[1,"1 Stunde"],[2,"2 Stunden"],[3,"3 Stunden"],[4,"4 Stunden"],[6,"6 Stunden"],[8,"8 Stunden"],[12,"12 Stunden"]].forEach(([v,l]) => {
        const o = document.createElement("option"); o.value = v; o.textContent = l; hSel.appendChild(o);
      });
      const cur = task.schedule.startsWith("every:") ? parseInt(task.schedule.split(":")[1]) : 6;
      hSel.value = cur;
      hSel.addEventListener("change", () => { autoScanTasks[idx].schedule = `every:${hSel.value}h`; });
      timeRow.appendChild(hSel);
    } else if (mode === "weekly") {
      const dowSel = document.createElement("select");
      dowSel.style.cssText = modeSel.style.cssText;
      [["mon","Mo"],["tue","Di"],["wed","Mi"],["thu","Do"],["fri","Fr"],["sat","Sa"],["sun","So"]].forEach(([v,l]) => {
        const o = document.createElement("option"); o.value = v; o.textContent = l; dowSel.appendChild(o);
      });
      const wp = task.schedule.startsWith("weekly:") ? task.schedule.split(":") : ["weekly","sun","3","0"];
      dowSel.value = wp[1] || "sun";
      const inp = document.createElement("input"); inp.type = "time";
      inp.value = `${(wp[2]||"3").padStart(2,"0")}:${(wp[3]||"0").padStart(2,"0")}`;
      inp.style.cssText = modeSel.style.cssText.replace("select","input");
      const sync = () => {
        const t = inp.value.split(":");
        autoScanTasks[idx].schedule = `weekly:${dowSel.value}:${parseInt(t[0],10)}:${parseInt(t[1],10)}`;
      };
      dowSel.addEventListener("change", sync); inp.addEventListener("change", sync);
      timeRow.append(dowSel, inp);
    }
  };

  modeSel.addEventListener("change", () => { rebuildTime(modeSel.value); });
  rebuildTime(modeSel.value);

  return { container, timeRow };
}

function autoScanLibSelect(task, idx) {
  const sel = document.createElement("select");
  sel.style.cssText = "padding:4px 6px;border-radius:5px;border:1px solid var(--border);background:var(--surface);color:var(--text);font-size:.85em;flex:1;min-width:0";
  const all = document.createElement("option"); all.value = "0"; all.textContent = "Alle Bibliotheken"; sel.appendChild(all);
  for (const l of autoScanLibs) {
    const o = document.createElement("option"); o.value = l.id; o.textContent = l.name; sel.appendChild(o);
  }
  sel.value = task.libraryId ?? 0;
  sel.addEventListener("change", () => { autoScanTasks[idx].libraryId = parseInt(sel.value, 10) || 0; });
  return sel;
}

function autoScanTaskSummary(task) {
  if (!task.enabled) return "⏸ Deaktiviert";
  const sched = autoScanScheduleLabel(task.schedule);
  const lib = task.libraryId === 0 ? "Alle Bibliotheken" : (autoScanLibs.find(l => l.id === task.libraryId)?.name || `Lib ${task.libraryId}`);
  const type = task.force ? "vollständig" : "inkrementell";
  return `⏱ ${sched} · ${lib} · ${type}`;
}

function autoScanAddTask() {
  const maxId = autoScanTasks.reduce((m, t) => Math.max(m, t.id || 0), 0);
  autoScanTasks.push({ id: maxId + 1, enabled: true, schedule: "daily:3:0", libraryId: 0, force: false });
  autoScanRenderTasks();
}

async function saveAutoScan() {
  try {
    const result = await api("/api/settings/autoscan", { method: "PUT", body: JSON.stringify(autoScanTasks) });
    autoScanTasks = Array.isArray(result) ? result : autoScanTasks;
    updateAutoScanMenuSub(autoScanTasks);
    showToast("Auto-Scan gespeichert ✓", { kind: "success" });
    $("#autoScanDialog").close();
  } catch (e) {
    appAlert("Fehler beim Speichern: " + e.message);
  }
}

function autoScanScheduleLabel(schedule) {
  if (!schedule) return "–";
  const parts = schedule.split(":");
  const days = { mon:"Montag", tue:"Dienstag", wed:"Mittwoch", thu:"Donnerstag",
                 fri:"Freitag", sat:"Samstag", sun:"Sonntag" };
  if (parts[0] === "daily" && parts.length >= 3)
    return `Täglich um ${parts[1].padStart(2,"0")}:${parts[2].padStart(2,"0")} Uhr`;
  if (parts[0] === "every" && parts.length >= 2) {
    const h = parseInt(parts[1]);
    return `Alle ${h} Stunde${h === 1 ? "" : "n"}`;
  }
  if (parts[0] === "weekly" && parts.length >= 4)
    return `${(days[parts[1]] || parts[1])}s um ${parts[2].padStart(2,"0")}:${parts[3].padStart(2,"0")} Uhr`;
  return schedule;
}

function updateAutoScanMenuSub(tasks) {
  const el = $("#autoScanMenuSub");
  if (!el) return;
  const active = Array.isArray(tasks) ? tasks.filter(t => t.enabled).length : 0;
  el.textContent = active > 0 ? `✓ ${active} aktive Aufgabe${active === 1 ? "" : "n"}` : "Zeitgesteuerte Bibliotheks-Scans";
}

