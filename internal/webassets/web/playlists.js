// playlists.js — User-Playlists + globale Zufallswiedergabe.
//
// Reihenfolge in index.html:
//   helpers → dialogs → api → cast → player-components → cards → views → grid → player → admin → playlists → app
//
// Public Functions (alle global):
//   Playlists:
//     loadPlaylists, openPlaylistsManager, addToPlaylistViaDialog,
//     openAddToPlaylist, …
//   Shuffle:
//     enterShuffleMode, exitShuffleMode, shufflePrev, shuffleNext
//
// state.shuffleMode + state.shuffleHistory + state.shuffleIdx werden in
// app.js (state-Init) gehalten; die Funktionen hier mutieren sie.

// --- Playlists ---

async function openPlaylistsManager() {
  await loadLibraries(); // refresh
  const ul = $("#playlistsList");
  ul.innerHTML = "";
  if (!state.playlists.length) {
    ul.innerHTML = `<li><em>Noch keine Playlists.</em></li>`;
  }
  for (const p of state.playlists) {
    const li = document.createElement("li");
    li.className = "lib-row";
    const head = document.createElement("div");
    head.className = "lib-row-head";
    head.innerHTML = `<div><strong>${escapeHTML(p.name)}</strong> <small style="color:#9ca3af">${p.itemCount} Video${p.itemCount===1?"":"s"}</small></div>`;
    const actions = document.createElement("div");
    actions.style.display = "flex"; actions.style.gap = "6px";
    const open = document.createElement("button");
    open.textContent = "Öffnen";
    open.addEventListener("click", () => {
      enterPlaylist(p.id);
      state.currentLibrary = null;
      state.currentFolder = null;
      $("#playlistsDialog").close();
      loadLibraries().then(loadItems);
    });
    actions.appendChild(open);
    const rename = document.createElement("button");
    rename.textContent = "Umbenennen";
    rename.addEventListener("click", async () => {
      const newName = await appPrompt("Neuer Name:", p.name);
      if (!newName || newName === p.name) return;
      try {
        await api(`/api/playlists/${p.id}`, { method: "PUT", body: JSON.stringify({ name: newName }) });
        openPlaylistsManager();
      } catch (e) { appAlert(e.message); }
    });
    actions.appendChild(rename);
    const del = document.createElement("button");
    del.textContent = "Löschen";
    del.classList.add("danger");
    del.addEventListener("click", async () => {
      if (!(await appConfirm(`Playlist "${p.name}" löschen?`))) return;
      await api(`/api/playlists/${p.id}`, { method: "DELETE" });
      if (state.currentPlaylist == p.id) {
        state.currentPlaylist = null;
        state.currentLibrary = state.libraries[0] ? state.libraries[0].id : null;
      }
      openPlaylistsManager();
      loadItems();
    });
    actions.appendChild(del);
    head.appendChild(actions);
    li.appendChild(head);
    ul.appendChild(li);
  }
  $("#playlistsDialog").showModal();
}

async function handleNewPlaylist(e) {
  e.preventDefault();
  const form = e.target;
  const data = Object.fromEntries(new FormData(form));
  try {
    await api("/api/playlists", { method: "POST", body: JSON.stringify({ name: data.name }) });
    form.reset();
    openPlaylistsManager();
  } catch (err) { appAlert(err.message); }
}

// openAddToPlaylist öffnet den „Zu Playlist hinzufügen"-Dialog mit Liste aller
// vorhandenen Playlists plus Quick-Create-Formular. Arbeitet sowohl für ein
// einzelnes Item (Detail-Dialog) als auch für Bulk-Auswahl.
//
// opts.itemIDs: optionales Array von Item-IDs. Ohne → fällt auf
//   [state.currentItem.id] zurück. Mit Array → Bulk-Modus: Klick auf eine
//   Playlist hängt alle IDs an, Quick-Create erstellt neue Playlist und hängt
//   alle IDs rein.
async function openAddToPlaylist(opts = {}) {
  const itemIDs = Array.isArray(opts.itemIDs) && opts.itemIDs.length
    ? opts.itemIDs.slice()
    : (state.currentItem ? [state.currentItem.id] : []);
  if (!itemIDs.length) return;
  state.playlistAddContext = { itemIDs, isBulk: itemIDs.length > 1 };

  const isBulk = state.playlistAddContext.isBulk;
  // Im Bulk-Modus zeigen wir keine ✓-Markierung (wäre uneinheitlich, wenn
  // manche der Items in der Playlist sind und andere nicht).
  const allPL = await api("/api/playlists");
  const currentIDs = isBulk ? [] : await api(`/api/items/${itemIDs[0]}/playlists`);
  const idSet = new Set(currentIDs);
  const ul = $("#addToPlaylistList");
  ul.innerHTML = "";
  if (!allPL.length) {
    ul.innerHTML = `<li><em style="color:#6b7280">Noch keine Playlist angelegt — nutze das Formular unten.</em></li>`;
  }
  const h2 = $("#addToPlaylistDialog h2");
  if (h2) h2.textContent = isBulk
    ? `${itemIDs.length} Videos zu Playlist hinzufügen`
    : "Zu Playlist hinzufügen";
  for (const p of allPL) {
    const li = document.createElement("li");
    const inSet = !isBulk && idSet.has(p.id);
    li.innerHTML = `${inSet ? "✓ " : ""}<strong>${escapeHTML(p.name)}</strong> <small style="color:#6b7280">(${p.itemCount})</small>`;
    li.addEventListener("click", async () => {
      try {
        if (isBulk) {
          await addItemsToPlaylistBulk(p, itemIDs);
          $("#addToPlaylistDialog").close();
          setSelectionMode(false);
          return;
        }
        if (inSet) {
          await api(`/api/playlists/${p.id}/items/${itemIDs[0]}`, { method: "DELETE" });
          showToast(`Aus „${p.name}" entfernt`, { kind: "success" });
        } else {
          const res = await api(`/api/playlists/${p.id}/items`, {
            method: "POST",
            body: JSON.stringify({ itemId: itemIDs[0] }),
          });
          if (res && res.added) {
            showToast(`Zu „${p.name}" hinzugefügt`, { kind: "success" });
          } else {
            showToast(`Ist bereits in „${p.name}"`);
          }
        }
        // Nach erfolgter Aktion Dialog schliessen — User will nach dem
        // Hinzufuegen/Entfernen weiter zum eigentlichen Inhalt zurueck.
        // Das ✕-Kreuz oben bleibt fuer den „doch nicht"-Fall verfuegbar.
        $("#addToPlaylistDialog").close();
      } catch (e) { appAlert(e.message); }
    });
    ul.appendChild(li);
  }
  $("#addToPlaylistDialog").showModal();
}

// Hilfsfunktion: hängt mehrere Items an eine bestehende Playlist und zeigt
// einen Sammel-Toast mit neu/duplikat/Fehler-Zählung.
async function addItemsToPlaylistBulk(pl, itemIDs) {
  let added = 0, dup = 0, fails = 0;
  for (const id of itemIDs) {
    try {
      const res = await api(`/api/playlists/${pl.id}/items`, {
        method: "POST",
        body: JSON.stringify({ itemId: id }),
      });
      if (res && res.added) added++; else dup++;
    } catch { fails++; }
  }
  const parts = [`${added} zu „${pl.name}" hinzugefügt`];
  if (dup) parts.push(`${dup} bereits drin`);
  if (fails) parts.push(`${fails} Fehler`);
  showToast(parts.join(", "), { kind: fails ? "error" : "success", duration: 4000 });
}

async function handleQuickCreatePlaylist(e) {
  e.preventDefault();
  const ctx = state.playlistAddContext;
  const itemIDs = (ctx && ctx.itemIDs) || (state.currentItem ? [state.currentItem.id] : []);
  if (!itemIDs.length) return;
  const form = e.target;
  const data = Object.fromEntries(new FormData(form));
  try {
    const pl = await api("/api/playlists", { method: "POST", body: JSON.stringify({ name: data.name }) });
    if (itemIDs.length > 1) {
      await addItemsToPlaylistBulk(pl, itemIDs);
    } else {
      await api(`/api/playlists/${pl.id}/items`, {
        method: "POST",
        body: JSON.stringify({ itemId: itemIDs[0] }),
      });
      showToast(`Neue Playlist „${pl.name}" mit Video erstellt`, { kind: "success" });
    }
    form.reset();
    $("#addToPlaylistDialog").close();
    if (ctx && ctx.isBulk) setSelectionMode(false);
  } catch (err) { appAlert(err.message); }
}

// --- Zufällige Wiedergabe ---

// randomParams: baut die URL-Parameter für /api/items/random aus dem aktuellen Kontext.
// Beruecksichtigt: Library + Folder, aktive Playlist, Person-Filter (Schauspieler),
// Such-Query, Watched-/Favorite-/Match-Filter, Aufloesungs-Buckets.
function randomParams() {
  const params = new URLSearchParams();
  if (state.currentPlaylist) {
    // Playlist-Kontext: nur Items aus dieser Playlist. libraryId ist hier
    // irrelevant — Playlists sind library-uebergreifend.
    params.set("playlistId", state.currentPlaylist);
  } else if (state.personFilter && state.personFilter.tmdbId) {
    // Person-Filter: alle Videos mit diesem Schauspieler, library-uebergreifend.
    params.set("personId", state.personFilter.tmdbId);
  } else if (state.shuffleFolders && state.shuffleFolders.length) {
    // Manuelle Ordner-Auswahl (shuffleScopeDialog): library-uebergreifend
    // kombinierbar, hat Vorrang vor dem aktuell geoeffneten Bibliotheks-/
    // Ordner-Kontext, solange eine Auswahl aktiv ist.
    for (const sel of state.shuffleFolders) {
      params.append("folderSel", `${sel.libraryId}:${sel.folder}`);
    }
  } else if (state.currentLibrary) {
    params.set("libraryId", state.currentLibrary);
    if (state.currentFolder !== null) params.set("folder", state.currentFolder);
  }
  const search = $("#searchInput").value.trim();
  if (search) params.set("search", search);
  const watched = $("#watchedFilter").value; if (watched) params.set("watched", watched);
  const fav = currentFavoriteMode(); if (fav) params.set("favorite", fav);
  const match = currentMatchMode(); if (match) params.set("match", match);
  // Auflösungs-Filter (Multi-Select-Buckets)
  applyResolutionFilter(params);
  return params;
}

async function playRandom() {
  // Mindestens einer der Kontexte muss aktiv sein: Library, Playlist,
  // Person-Filter oder eine manuelle Ordner-Auswahl. Sonst gibt es nichts,
  // woraus zufaellig gewaehlt werden kann.
  if (!state.currentLibrary && !state.currentPlaylist &&
      !(state.personFilter && state.personFilter.tmdbId) &&
      !(state.shuffleFolders && state.shuffleFolders.length)) {
    appAlert("Bitte erst eine Bibliothek, Playlist oder einen Schauspieler-Filter waehlen.");
    return;
  }
  try {
    const item = await api(`/api/items/random?${randomParams()}`);
    state.shuffleMode = true;
    state.shuffleHistory = [item];
    state.shuffleIdx = 0;
    openPlayer(item, { fromShuffle: true });
  } catch (e) {
    appAlert("Kein zufälliges Video gefunden: " + e.message);
  }
}

async function shuffleNext() {
  if (!state.shuffleMode) return;
  // Wenn wir in der History zurückgesprungen sind, vorwärts innerhalb der History
  if (state.shuffleIdx < state.shuffleHistory.length - 1) {
    state.shuffleIdx++;
    openPlayer(state.shuffleHistory[state.shuffleIdx], { fromShuffle: true });
    return;
  }
  // Sonst: neues Zufalls-Item holen und anhängen
  try {
    const item = await api(`/api/items/random?${randomParams()}`);
    state.shuffleHistory.push(item);
    state.shuffleIdx = state.shuffleHistory.length - 1;
    openPlayer(item, { fromShuffle: true });
  } catch (e) {
    appAlert("Kein weiteres Zufallsvideo: " + e.message);
  }
}

function shufflePrev() {
  if (!state.shuffleMode || state.shuffleIdx <= 0) return;
  state.shuffleIdx--;
  openPlayer(state.shuffleHistory[state.shuffleIdx], { fromShuffle: true });
}

// --- Zufallswiedergabe: Ordner-Auswahl (shuffleScopeDialog) ---
//
// Erlaubt, die Zufallswiedergabe auf eine frei kombinierbare Auswahl aus
// Ordnern/Unterordnern (auch aus verschiedenen Ordnern oder Bibliotheken)
// zu beschränken, statt nur auf die aktuell geöffnete Library/Ordner.
// Persistiert in state.shuffleFolders + localStorage "shuffleFolders".
//
// Der Ordner-Baum wird lazy geladen (ein Level pro Expand-Klick) über
// GET /api/libraries/{id}/folders?parent=… — dieselbe Route wie die normale
// Ordner-Navigation, funktioniert also auch für Non-Admin-User (anders als
// /all-folders, das admin-only ist und im Verschieben-Dialog genutzt wird).

let shuffleScopePending = [];   // Arbeitskopie von state.shuffleFolders, während der Dialog offen ist
let shuffleTreeCache = {};      // `${libId}:${parent}` → Folder[] (vermeidet Re-Fetch beim Auf-/Zuklappen)
let shuffleTreeExpanded = new Set(); // Set von `${libId}:${path}` — aufgeklappte Knoten im Dialog

async function openShuffleScopeDialog() {
  if (!state.libraries || !state.libraries.length) {
    appAlert("Keine Bibliothek verfügbar.");
    return;
  }
  shuffleScopePending = state.shuffleFolders.map(x => ({ ...x }));
  shuffleTreeCache = {};
  shuffleTreeExpanded = new Set();
  const sel = $("#shuffleScopeLibrarySelect");
  sel.innerHTML = state.libraries.map(l => `<option value="${l.id}">${escapeHTML(l.name)}</option>`).join("");
  const preferred = (state.currentLibrary && state.libraries.some(l => l.id == state.currentLibrary))
    ? state.currentLibrary : state.libraries[0].id;
  sel.value = preferred;
  if (!sel.dataset.wired) {
    sel.dataset.wired = "1";
    sel.addEventListener("change", () => renderShuffleScopeTree());
  }
  renderShuffleScopeChips();
  await renderShuffleScopeTree();
  $("#shuffleScopeDialog").showModal();
}

async function fetchShuffleTreeChildren(libId, parent) {
  const key = `${libId}:${parent}`;
  if (shuffleTreeCache[key]) return shuffleTreeCache[key];
  try {
    const folders = await api(`/api/libraries/${libId}/folders?parent=${encodeURIComponent(parent)}`);
    shuffleTreeCache[key] = folders || [];
  } catch {
    shuffleTreeCache[key] = [];
  }
  return shuffleTreeCache[key];
}

async function renderShuffleTreeLevel(libId, parent) {
  const folders = await fetchShuffleTreeChildren(libId, parent);
  if (!folders.length) return "";
  const rows = await Promise.all(folders.map(async (f) => {
    const path = f.name;
    const checked = shuffleScopePending.some(x => x.libraryId === libId && x.folder === path);
    const key = `${libId}:${path}`;
    const expanded = shuffleTreeExpanded.has(key);
    const label = path.split("/").pop();
    const childrenHtml = expanded
      ? `<ul class="move-tree-list">${await renderShuffleTreeLevel(libId, path)}</ul>`
      : "";
    return `
      <li class="move-tree-item">
        <div class="move-tree-row ${checked ? "is-checked" : ""}" data-lib="${libId}" data-path="${escapeHTML(path)}">
          <button type="button" class="move-tree-toggle ${expanded ? "is-open" : ""}" data-lib="${libId}" data-toggle="${escapeHTML(path)}">▸</button>
          <label class="move-tree-label"><input type="checkbox" data-lib="${libId}" data-path="${escapeHTML(path)}" ${checked ? "checked" : ""}> 📁 ${escapeHTML(label)}</label>
        </div>
        ${childrenHtml}
      </li>`;
  }));
  return rows.join("");
}

async function renderShuffleScopeTree() {
  const libId = Number($("#shuffleScopeLibrarySelect").value);
  const container = $("#shuffleScopeTree");
  const rootChecked = shuffleScopePending.some(x => x.libraryId === libId && x.folder === "");
  const childrenHtml = await renderShuffleTreeLevel(libId, "");
  const rootRow = `
    <div class="move-tree-row ${rootChecked ? "is-checked" : ""}" data-lib="${libId}" data-path="">
      <button type="button" class="move-tree-toggle move-tree-toggle--leaf"></button>
      <label class="move-tree-label"><input type="checkbox" data-lib="${libId}" data-path="" ${rootChecked ? "checked" : ""}> 🏠 (ganze Bibliothek)</label>
    </div>`;
  container.innerHTML = childrenHtml
    ? `${rootRow}<ul class="move-tree-list">${childrenHtml}</ul>`
    : `${rootRow}<div class="move-tree-empty">Keine Unterordner in dieser Bibliothek.</div>`;
  if (!container.dataset.wired) {
    container.dataset.wired = "1";
    container.addEventListener("click", handleShuffleTreeToggle);
    container.addEventListener("change", handleShuffleTreeCheck);
  }
}

async function handleShuffleTreeToggle(e) {
  const toggle = e.target.closest("[data-toggle]");
  if (!toggle) return;
  e.stopPropagation();
  const libId = Number(toggle.dataset.lib);
  const path = toggle.dataset.toggle;
  const key = `${libId}:${path}`;
  if (shuffleTreeExpanded.has(key)) shuffleTreeExpanded.delete(key);
  else shuffleTreeExpanded.add(key);
  await renderShuffleScopeTree();
}

function handleShuffleTreeCheck(e) {
  const cb = e.target.closest('input[type="checkbox"]');
  if (!cb) return;
  const libId = Number(cb.dataset.lib);
  const path = cb.dataset.path;
  toggleShuffleFolderSelection(libId, path, cb.checked);
  const row = cb.closest(".move-tree-row");
  if (row) row.classList.toggle("is-checked", cb.checked);
  renderShuffleScopeChips();
}

function toggleShuffleFolderSelection(libId, path, checked) {
  if (checked) {
    if (shuffleScopePending.some(x => x.libraryId === libId && x.folder === path)) return;
    const lib = state.libraries.find(l => l.id === libId);
    const libName = lib ? lib.name : `Lib ${libId}`;
    shuffleScopePending.push({
      libraryId: libId,
      libraryName: libName,
      folder: path,
      label: path === "" ? `${libName} (ganze Bibliothek)` : `${libName} / ${path}`,
    });
  } else {
    shuffleScopePending = shuffleScopePending.filter(x => !(x.libraryId === libId && x.folder === path));
  }
}

function renderShuffleScopeChips() {
  const box = $("#shuffleScopeChips");
  if (!shuffleScopePending.length) {
    box.innerHTML = `<span class="shuffle-chip-empty">Keine Auswahl — aktueller Kontext wird verwendet.</span>`;
    return;
  }
  box.innerHTML = shuffleScopePending.map((x, i) =>
    `<span class="shuffle-chip">${escapeHTML(x.label)}<button type="button" data-idx="${i}" title="Entfernen">✕</button></span>`
  ).join("");
  if (!box.dataset.wired) {
    box.dataset.wired = "1";
    box.addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-idx]");
      if (!btn) return;
      const idx = Number(btn.dataset.idx);
      const removed = shuffleScopePending[idx];
      shuffleScopePending.splice(idx, 1);
      renderShuffleScopeChips();
      if (removed && removed.libraryId === Number($("#shuffleScopeLibrarySelect").value)) {
        await renderShuffleScopeTree();
      }
    });
  }
}

function applyShuffleScope() {
  state.shuffleFolders = shuffleScopePending.map(x => ({ ...x }));
  try { localStorage.setItem("shuffleFolders", JSON.stringify(state.shuffleFolders)); } catch {}
  updateShuffleScopeIndicator();
  $("#shuffleScopeDialog").close();
  showToast(
    state.shuffleFolders.length
      ? `Zufallswiedergabe auf ${state.shuffleFolders.length} Ordner beschränkt`
      : "Zufallswiedergabe-Auswahl zurückgesetzt",
    { kind: "success" }
  );
}

async function resetShuffleScope() {
  shuffleScopePending = [];
  renderShuffleScopeChips();
  await renderShuffleScopeTree();
}

// updateShuffleScopeIndicator: spiegelt eine aktive Ordner-Auswahl in Titel +
// Hervorhebung der Shuffle-Buttons — Aufruf beim Boot (persistierte Auswahl)
// und nach jedem Übernehmen/Zurücksetzen.
function updateShuffleScopeIndicator() {
  const n = state.shuffleFolders.length;
  const scopeBtn = $("#shuffleScopeBtn");
  if (scopeBtn) {
    scopeBtn.classList.toggle("active", n > 0);
    scopeBtn.title = n > 0
      ? `Zufallswiedergabe: ${n} Ordner ausgewählt (Klick zum Ändern)`
      : "Ordner für Zufallswiedergabe auswählen";
  }
  const shuffleBtn = $("#shuffleBtn");
  if (shuffleBtn) {
    shuffleBtn.title = n > 0 ? `Zufällige Wiedergabe (${n} Ordner ausgewählt)` : "Zufällige Wiedergabe";
  }
}

