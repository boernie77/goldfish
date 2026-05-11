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
  $("#settingsMenuTrickplay").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuWhisper").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuPathSearch").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuMissing").classList.toggle("hidden", !state.me.isAdmin);
  $("#settingsMenuRefreshAllMeta").classList.toggle("hidden", !state.me.isAdmin);
  // Das gesamte Zahnrad-Menü ist Admin-only: alle übrigen Einträge (Settings,
  // Bibliotheken) sind ebenfalls administrative Aktionen. Für reguläre User
  // wird der Button komplett ausgeblendet.
  $("#settingsBtn").classList.toggle("hidden", !state.me.isAdmin);
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
            <button type="button" class="user-btn danger user-del-btn" title="Benutzer löschen">🗑 Löschen</button>
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

    // Header-Zeile
    const header = document.createElement("div");
    header.className = "lib-row-head";
    header.innerHTML = `<div><strong>${escapeHTML(l.name)}</strong> <span class="lib-kind">${kindLabel}</span></div>`;

    const actions = document.createElement("div");
    actions.style.display = "flex";
    actions.style.gap = "6px";

    // ▲▼ zum Verschieben innerhalb der Liste — schreibt nach jedem Klick
    // die neue Reihenfolge an /api/libraries/order zurueck und reloadet.
    const upBtn = document.createElement("button");
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
    actions.appendChild(upBtn);
    actions.appendChild(downBtn);
    const kindSel = document.createElement("select");
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
    actions.appendChild(kindSel);
    // Startseiten-Sichtbarkeit
    const onHomeLabel = document.createElement("label");
    onHomeLabel.className = "lib-on-home";
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
    actions.appendChild(onHomeLabel);
    // Card-Layout-Toggle nur bei Privat-Libs anzeigen — bei Filme/Serien
    // ist sowieso der Titel oben (Kanal-Layout greift dort nicht).
    if (l.kind === "private") {
      const layoutLabel = document.createElement("label");
      layoutLabel.className = "lib-on-home";
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
      actions.appendChild(layoutLabel);
    }
    const del = document.createElement("button");
    del.textContent = "Bibliothek löschen";
    del.classList.add("danger");
    del.addEventListener("click", async () => {
      if (!(await appConfirm(`Bibliothek "${l.name}" löschen?`))) return;
      await api(`/api/libraries/${l.id}`, { method: "DELETE" });
      await loadLibraries();
      openManage();
      loadItems();
    });
    actions.appendChild(del);
    header.appendChild(actions);
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

// --- Auto-Rename: Umbenennungen-Manager ---
//
// Liste aller Datei-Umbenennungen, einzeln rueckgaengig machbar, plus
// CSV-Export und „Alle bestaetigten umbenennen"-Bulk-Button. Nutzt die
// /api/admin/renames-Endpoints (admin-only).

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
    const triggerLabel = ({auto: "auto", manual: "manuell", bulk: "bulk"})[e.triggeredBy] || e.triggeredBy;
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

