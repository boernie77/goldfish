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
        openAddToPlaylist({ itemIDs });
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
function randomParams() {
  const params = new URLSearchParams();
  if (state.currentLibrary) params.set("libraryId", state.currentLibrary);
  if (state.currentFolder !== null) params.set("folder", state.currentFolder);
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
  if (!state.currentLibrary) {
    appAlert("Keine Bibliothek gewählt.");
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

