// Vanilla JS frontend. Video.js (gebundelt) als Player, HLS via Video.js-VHS.

const $ = (s, root = document) => root.querySelector(s);

const state = {
  libraries: [],
  playlists: [],
  currentLibrary: null,
  currentPlaylist: null, // wenn gesetzt, wird eine Playlist angezeigt statt einer Library
  currentFolder: null,   // null = Library-Root, sonst Pfad wie "a/Siterips"
  currentFolderDrilldown: false, // ob der aktive Folder Subfolder statt flacher Items zeigt
  playQueue: [],         // für auto-next in Playlists: die Items in Reihenfolge
  playQueueIdx: -1,      // aktueller Index innerhalb der Queue
  shuffleMode: false,    // wenn true, sind Prev/Next-Buttons im Player aktiv
  shuffleHistory: [],    // bereits abgespielte Items in Reihenfolge
  shuffleIdx: -1,        // aktuelle Position in shuffleHistory
  settings: { bufferSeconds: 30, startBufferSeconds: 0, trickplayIntervalSec: 10 },
  hwaccel: { enabled: false, device: "", driver: "" },
  scanPoll: null,
  vjs: null,
  currentItem: null,
  playback: null,
  browseAt: "/media",
  personFilter: null,       // {tmdbId, name} wenn Person-Filter aktiv
  personFilterBackup: null, // zwischengespeicherter Lib/Folder-Kontext
  personFilterShow: null,   // {folder, libraryId, episodes} wenn innerhalb einer Serie im Person-Filter
  transcodePollTimer: null, // setInterval-Handle für Transcode-Progress-Polling
  flatView: false,          // wenn true: loadItems überspringt Ordner-Navigation
  selectionMode: false,     // Bulk-Auswahl aktiv
  selection: new Set(),     // Set von item-IDs in der aktuellen Auswahl
  lastRenderedItems: [],    // Referenz auf zuletzt gerenderte Items (für „Alle auswählen")
  loadSeq: 0,               // Sequenz-Zähler für loadItems — verhindert, dass veraltete async-Responses das Grid überschreiben
  sortDir: "",              // "asc" | "desc" | "" (Default) — Richtungs-Override
  resBuckets: new Set(),    // ausgewählte Auflösungs-Buckets ("4k","1080p",…) — Multi-Select
  collectionsView: false,   // true = Collections-Root-Ansicht (Liste aller Sammlungen)
  currentCollection: null,  // {id, name} wenn eine einzelne Collection geöffnet ist
  playlistsView: false,     // true = Playlists-Root (Kacheln aller Playlists)
  lastSortContextKey: "",   // merkt, wann Sortierung neu aus localStorage zu laden ist
  pendingResumeSec: 0,      // beim nächsten applyPlayback angewandte Resume-Position
  playlistReturnNav: null,  // Snapshot der Nav vor Playlist-Öffnen — Close-Button stellt ihn wieder her
  startBufferTimer: null,   // setInterval-Handle für den Pre-Buffer-Gate
  scrollPositions: new Map(), // navKey → scrollY für Zurück-Navigation
  lastNavKey: null,            // navKey der zuletzt gerenderten Ansicht
  alphaFilter: null,           // null oder "A".."Z"|"#" — Anfangsbuchstaben-Filter via Sidebar-Klick
  moveContext: null,           // {mode:"single",item} oder {mode:"bulk",ids,libId} während #moveDialog offen ist
  shuffleFolders: [],          // [{libraryId,libraryName,folder,label}] — Ordner-Scoping für Zufallswiedergabe (leer = aktueller Kontext)
};

// captureNavSnapshot: merkt sich die aktuelle Nav-Position (Library, Folder,
// Home-View, Collection, Person-Filter). Nur setzen, wenn wir noch NICHT in
// einer Playlist-Ansicht sind — damit das erste Verlassen der „echten" Nav
// den Rücksprungs-Punkt festhält. Späteres Wechseln zwischen Playlist-Root
// und einzelnen Playlists überschreibt ihn nicht.
function captureNavSnapshot() {
  if (state.playlistsView || state.currentPlaylist) return;
  state.playlistReturnNav = {
    currentLibrary: state.currentLibrary,
    currentFolder: state.currentFolder,
    currentSeason: state.currentSeason,
    currentFolderDrilldown: state.currentFolderDrilldown,
    homeView: state.homeView,
    collectionsView: state.collectionsView,
    currentCollection: state.currentCollection,
    personFilter: state.personFilter,
  };
}

// enterPlaylist: öffnet eine einzelne Playlist. Snapshot nur setzen, falls
// wir von außerhalb des Playlist-Bereichs reinkommen.
function enterPlaylist(playlistID) {
  captureNavSnapshot();
  state.currentPlaylist = playlistID;
}

// enterPlaylistsView: öffnet die Playlist-Root (Kacheln aller Playlists).
// Snapshot speichern, damit der ✕-Button in der Root auch dort zur vorherigen
// echten Nav zurückführt.
function enterPlaylistsView() {
  captureNavSnapshot();
  state.playlistsView = true;
  state.currentPlaylist = null;
  state.currentLibrary = null;
  state.collectionsView = false;
  state.currentCollection = null;
  state.homeView = false;
}

// exitPlaylist: stellt den vorher gemerkten Nav-Kontext wieder her. Funktioniert
// sowohl aus einer einzelnen Playlist als auch aus der Playlist-Root-Ansicht.
// Wenn keiner gespeichert ist (Direktzugriff oder Reload), fällt auf Home zurück.
function exitPlaylist() {
  const nav = state.playlistReturnNav;
  state.currentPlaylist = null;
  state.playlistsView = false;
  state.playlistReturnNav = null;
  if (!nav) {
    // Fallback: Home
    state.homeView = true;
    state.currentLibrary = null;
    state.currentFolder = null;
    loadItems();
    return;
  }
  state.currentLibrary = nav.currentLibrary;
  state.currentFolder = nav.currentFolder;
  state.currentSeason = nav.currentSeason;
  state.currentFolderDrilldown = nav.currentFolderDrilldown;
  state.homeView = nav.homeView;
  state.collectionsView = nav.collectionsView;
  state.currentCollection = nav.currentCollection;
  state.personFilter = nav.personFilter;
  const sel = $("#librarySelect");
  if (sel) {
    if (state.currentLibrary) sel.value = "lib:" + state.currentLibrary;
    else sel.value = "";
  }
  loadItems();
}

// api(), apiGetCached(), invalidateItemsCache() liegen in api.js.

// fmtDuration/fmtSize/fmtDate liegen in helpers.js (gleiche window-Scope,
// via separatem <script>-Tag VOR app.js geladen).

// applyNaturalTitleSort: re-sortiert items mit "natural" / "human" Vergleich,
// damit Zahlen im Titel als Werte sortiert werden (1, 2, …, 9, 10, 11) statt
// lexikographisch (1, 10, 11, …, 2, 3). SQLite kann das nativ nicht.
// Greift nur, wenn das Sort-Dropdown auf "title" steht. Die Server-Sortierung
// liefert eine stabile Reihenfolge — wir verfeinern sie nur.
function applyNaturalTitleSort(items, opts) {
  if (!Array.isArray(items)) return items;
  const dir = (opts && opts.dir) || effectiveSortDir();
  const sortMode = (opts && opts.sort) || currentSortMode();
  if (sortMode !== "title") return items;
  const collator = new Intl.Collator("de", { numeric: true, sensitivity: "base" });
  const titleOf = (it) => {
    if (it && it.metadata && it.metadata.title) return it.metadata.title;
    return (it && it.title) || "";
  };
  const sign = dir === "desc" ? -1 : 1;
  items.sort((a, b) => sign * collator.compare(titleOf(a), titleOf(b)));
  return items;
}

// groupVariants: fasst Items mit gleichem metadataId zu Gruppen zusammen. Repräsentant
// pro Gruppe = Item mit höchster Auflösung (dann größter Bitrate). Alle Geschwister
// werden am Repräsentanten unter `_variants` abgelegt (für Detail-Dropdown).
// Items ohne metadataId bleiben unverändert.
function groupVariants(items) {
  const groups = new Map();
  const out = [];
  for (const it of items) {
    // variantSplit: Item wurde bewusst aus der Gruppierung genommen (siehe
    // "🔀 Als eigene Kacheln trennen" im Detail-Dialog) — immer als eigene
    // Kachel behandeln, obwohl es dieselbe metadataId wie Geschwister hat.
    if (!it.metadataId || it.variantSplit) { out.push(it); continue; }
    const key = it.metadataId;
    if (!groups.has(key)) {
      const rep = { ...it, _variants: [it] };
      groups.set(key, rep);
      out.push(rep);
    } else {
      const g = groups.get(key);
      g._variants.push(it);
      // Besseren Repräsentanten übernehmen (höher aufgelöst, sonst größere Bitrate)
      if ((it.height || 0) > (g.height || 0) ||
          ((it.height || 0) === (g.height || 0) && (it.bitrateKbps || 0) > (g.bitrateKbps || 0))) {
        // Werte übernehmen, aber _variants und Basis-ID beibehalten
        Object.assign(g, it, { _variants: g._variants });
      }
    }
  }
  return out;
}

// silentlyRefreshItem holt ein Item frisch vom Server und ersetzt die
// passende Kachel im Grid in-place — ohne loadItems() oder Re-Render
// des ganzen Grids. Damit bleibt die Scroll-Position erhalten.
//
// Wird genutzt nach Confirm (kann Auto-Rename triggern), nach dem
// manuellen Rename-Button und ueberall wo eine Item-Aenderung die
// sichtbare Kachel veraendert (Pfad, Title, …).
//
// Wenn die Kachel nicht im DOM ist (Item gehoert zu einem anderen
// Folder/Filter), wird trotzdem state.lastRenderedItems gepatcht —
// sodass z. B. die Bulk-Selection auf den frischen Daten arbeitet.
async function silentlyRefreshItem(id) {
  let fresh;
  try {
    fresh = await api(`/api/items/${id}`);
  } catch (e) {
    console.warn("silentlyRefreshItem:", e.message);
    return null;
  }
  if (!fresh) return null;
  // state.lastRenderedItems patchen, damit kuenftige Re-Renders + Bulk-
  // Aktionen auf frischen Daten arbeiten.
  if (Array.isArray(state.lastRenderedItems)) {
    const idx = state.lastRenderedItems.findIndex(x => x.id === fresh.id);
    if (idx >= 0) state.lastRenderedItems[idx] = fresh;
  }
  // Kachel im DOM ersetzen, falls sichtbar.
  const oldCard = document.querySelector(`[data-item-id="${fresh.id}"]`);
  if (oldCard && typeof renderCard === "function") {
    const newCard = renderCard(fresh);
    if (newCard) {
      oldCard.replaceWith(newCard);
    }
  }
  return fresh;
}

// cardFileName: letztes Pfad-Segment (Dateiname) einer Variante. Bei gemergten
// Kacheln wird die Anzahl der Varianten daneben angezeigt.
function cardFileName(it) {
  const name = (it.relPath || it.path || "").split("/").filter(Boolean).pop() || "";
  if (it._variants && it._variants.length > 1) {
    return `${name}  (+${it._variants.length - 1} weitere)`;
  }
  return name;
}

// cardFolderPath: Ordnerpfad ohne Dateinamen — für die Duplikate-Ansicht, wo
// der Speicherort die relevante Info ist (Titel/Dateiname stehen schon
// darüber). Leerer String bei Root-Dateien ohne Unterordner.
function cardFolderPath(it) {
  const segs = (it.relPath || it.path || "").split("/").filter(Boolean);
  segs.pop();
  return segs.join("/");
}

// variantLabel: Beschreibung einer Datei-Variante für den Dropdown.
// Enthält Dateiname + technische Details, damit man auch bei gleicher
// Auflösung/Größe noch unterscheiden kann, welche Datei gemeint ist.
function variantLabel(v) {
  const fileName = (v.relPath || v.path || "").split("/").filter(Boolean).pop() || "";
  const parts = [];
  parts.push((v.container || "").toUpperCase() || "?");
  const r = resLabel(v);
  if (r) parts.push(r);
  if (v.sizeBytes) parts.push(fmtSize(v.sizeBytes));
  if (v.bitrateKbps) parts.push(`${Math.round(v.bitrateKbps / 100) / 10} Mbps`);
  if (v.videoCodec) parts.push(v.videoCodec.toUpperCase());
  if (v.audioCodec) parts.push(v.audioCodec.toUpperCase());
  if (v.watched) parts.push("✓ gesehen");
  const tech = parts.join(" · ");
  return fileName ? `${fileName}  —  ${tech}` : tech;
}

// Admin-Diagnostics (Refresh-Metadata, Missing-Export, Path-Search,
// Trickplay-Manager) liegen in matching.js.


// „Favoriten-Modus" und „Unmatched-Modus" sind jetzt Einträge im Sort-Dropdown,
// keine separaten Filter mehr. Die Helfer kapseln das Lookup, damit der Rest
// des Codes unverändert „favorite=yes / match=unmatched"-Semantik nutzen kann.
function currentFavoriteMode() {
  return $("#sortSelect").value === "favorites" ? "yes" : "";
}
function currentMatchMode() {
  return $("#sortSelect").value === "unmatched" ? "unmatched" : "";
}
function currentInterlacedMode() {
  return $("#sortSelect").value === "interlaced" ? "yes" : "";
}
// Gibt einen für die Items-API gültigen Sortier-Wert zurück. Die View-Modi
// „favorites" / „unmatched" / „duplicates" / „interlaced" sind rein UI-
// Schalter und dürfen nicht als Sort-Key an den Server gehen — sonst
// ignoriert SQLite sie und fällt auf Default-Order zurück.
function currentSortMode() {
  const v = $("#sortSelect").value || "title";
  if (v === "favorites" || v === "unmatched" || v === "duplicates" || v === "suspicious" || v === "interlaced" || v === "namedupes" || v === "shuffle") return "title";
  return v;
}
// Fisher-Yates Shuffle für client-seitige Zufalls-Sortierung — Server liefert
// in „natürlicher" Reihenfolge, wir würfeln nach Erhalt. Vorteil gegenüber
// SQL ORDER BY RANDOM(): keine 60k-Items-Vollscan-Latenz.
function shuffleInPlace(arr) {
  if (!Array.isArray(arr)) return arr;
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const tmp = arr[i]; arr[i] = arr[j]; arr[j] = tmp;
  }
  return arr;
}

// Setzt alle Suche/Filter-Kontrollen zurück (beim Library-Wechsel).
function resetFilters() {
  const inputs = ["#searchInput", "#watchedFilter", "#ratingFilter", "#sortSelect"];
  for (const sel of inputs) {
    const el = $(sel);
    if (!el) continue;
    if (el.tagName === "SELECT") {
      el.selectedIndex = 0;
    } else {
      el.value = "";
    }
  }
  // Auflösungs-Buckets ebenfalls leeren
  state.resBuckets.clear();
  document.querySelectorAll('#resDropdown input[type="checkbox"]').forEach(cb => { cb.checked = false; });
  updateResDropdownLabel();
  $("#searchClear").classList.add("hidden");
  // Person-Filter ebenfalls aufheben
  state.personFilter = null;
  state.personFilterShow = null;
  state.personFilterBackup = null;
  // Bulk-Auswahl raus
  if (state.selectionMode) setSelectionMode(false);
}

// --- Dialog-Drag ---

// Macht den Player-Dialog per Drag am Header verschiebbar. Bricht ab, wenn
// auf einem interaktiven Element geklickt wird.
function setupDialogDrag() {
  const dlg = $("#playerDialog");
  if (!dlg) return;
  const head = dlg.querySelector(".player-head");
  if (!head) return;
  let sX = 0, sY = 0, sL = 0, sT = 0, dragging = false;
  head.addEventListener("mousedown", (e) => {
    if (e.target.closest("button, select, input, label, a")) return;
    const rect = dlg.getBoundingClientRect();
    sX = e.clientX; sY = e.clientY;
    sL = rect.left; sT = rect.top;
    // Auto-Zentrierung aufheben, absolute Position setzen
    dlg.style.margin = "0";
    dlg.style.left = sL + "px";
    dlg.style.top = sT + "px";
    dragging = true;
    e.preventDefault();
  });
  window.addEventListener("mousemove", (e) => {
    if (!dragging) return;
    const nl = sL + (e.clientX - sX);
    const nt = sT + (e.clientY - sY);
    // leicht im Viewport klemmen
    const maxL = window.innerWidth - 80;
    const maxT = window.innerHeight - 40;
    dlg.style.left = Math.max(-dlg.offsetWidth + 200, Math.min(maxL, nl)) + "px";
    dlg.style.top = Math.max(0, Math.min(maxT, nt)) + "px";
  });
  window.addEventListener("mouseup", () => { dragging = false; });
}

// --- Alphabet-Sidebar (rechter Schnellsprung/Filter) ---

// Per-Ordner-Toggle: localStorage `alphaSidebar:<libID>:<folder|root>` = "0"
// blendet die Leiste für GENAU diesen Ordner aus. Fehlt der Key → sichtbar
// (Default an). Analog zu sortStorageKey()/seasonView-Keys, aber eigener
// Namespace, da die Sidebar unabhängig vom Sort-Feld ist.
function alphaSidebarStorageKey() {
  if (!state.currentLibrary) return null;
  return `alphaSidebar:${state.currentLibrary}:${state.currentFolder || "root"}`;
}

function alphaSidebarHiddenHere() {
  const key = alphaSidebarStorageKey();
  if (!key) return false;
  try { return localStorage.getItem(key) === "0"; } catch { return false; }
}

// Nach jedem Render aufgerufen. Zeigt die A-Z-Leiste immer, solange Items/
// Folder da sind und der User sie für diesen Ordner nicht ausgeblendet hat —
// wirkt als Filter (nicht Scroll-Sprung), macht daher bei jeder Sortierung Sinn.
function updateAlphaSidebar() {
  const bar = $("#alphaSidebar");
  if (!bar) return;
  const items = state.lastRenderedItems || [];
  // Folder-Kacheln (z. B. Serien-Ordner in TV-Library-Root oder Show-Ordner
  // in Movies-Library) zählen für die Alphabet-Sidebar mit. Sonst hätte die
  // Hauptseite einer TV-Library keine Buchstabenleiste, weil dort nur
  // Folder gerendert werden, keine Items.
  const folders = state.lastRenderedFolders || [];
  const totalCount = items.length + folders.length;
  // Collections-Root hat keine Library (state.currentLibrary === null dort)
  // und damit auch keinen Storage-Key für den Per-Ordner-Toggle — bleibt wie
  // bisher unconditional sichtbar, kein Toggle-Button.
  const collectionsRoot = !!(state.collectionsView && !state.currentCollection);
  const toggleBtn = $("#alphaToggleBtn");
  const eligible = (!!state.currentLibrary || collectionsRoot) && totalCount > 0;
  const toggleable = !!state.currentLibrary; // Collections-Root: kein Toggle
  if (toggleBtn) toggleBtn.classList.toggle("hidden", !eligible || !toggleable);
  const hiddenHere = toggleable && alphaSidebarHiddenHere();
  if (toggleBtn) {
    toggleBtn.classList.toggle("active", !hiddenHere);
    toggleBtn.title = hiddenHere
      ? "Buchstabenleiste für diesen Ordner einblenden"
      : "Buchstabenleiste für diesen Ordner ausblenden";
  }
  if (!eligible || hiddenHere) {
    bar.classList.add("hidden");
    bar.innerHTML = "";
    document.body.classList.remove("has-alpha-sidebar");
    return;
  }
  document.body.classList.add("has-alpha-sidebar");
  // Vorhandene Anfangsbuchstaben bestimmen — Folders + Items.
  const present = new Set();
  const collect = (title) => {
    const t = (title || "").replace(/^\W+/, "").toUpperCase();
    if (!t) return;
    const ch = t[0];
    if (ch >= "A" && ch <= "Z") present.add(ch);
    else if (/^\d/.test(t)) present.add("#");
  };
  for (const f of folders) collect(folderDisplayTitle(f));
  for (const it of items) collect((it.metadata && it.metadata.title) || it.title);
  const letters = ["#", ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("")];
  bar.innerHTML = "";
  for (const ch of letters) {
    const btn = document.createElement("button");
    btn.textContent = ch;
    btn.type = "button";
    if (!present.has(ch)) btn.disabled = true;
    else btn.addEventListener("click", () => jumpToLetter(ch));
    bar.appendChild(btn);
  }
  bar.classList.remove("hidden");
}

// jumpToLetter: vom Sidebar-Klick aufgerufen. Setzt einen Anfangsbuchstaben-
// Filter — die Kacheln, die nicht mit diesem Buchstaben starten, werden via
// .alpha-hidden-Klasse weggeblendet. Nochmaliger Klick auf denselben
// Buchstaben hebt den Filter wieder auf (Toggle). Schließen geht außerdem
// über den ✕-Chip im Breadcrumb (renderBreadcrumb fügt ihn ein).
function jumpToLetter(ch) {
  setAlphaFilter(state.alphaFilter === ch ? null : ch);
}

function setAlphaFilter(ch) {
  state.alphaFilter = ch || null;
  applyAlphaFilter();
  // Aktiv-Markierung in der Sidebar nachziehen (ohne den ganzen Bar zu rebuilden).
  const bar = $("#alphaSidebar");
  if (bar) {
    for (const btn of bar.querySelectorAll("button")) {
      btn.classList.toggle("is-active", !!ch && btn.textContent === ch);
    }
  }
  // Banner ein-/ausblenden — sitzt zwischen Breadcrumb und Grid und ist
  // immer sichtbar, wenn der Filter aktiv ist (egal in welcher View).
  const banner = $("#alphaFilterBanner");
  const letter = $("#alphaFilterLetter");
  if (banner && letter) {
    if (ch) {
      letter.textContent = ch;
      banner.classList.remove("hidden");
    } else {
      banner.classList.add("hidden");
    }
  }
}

// applyAlphaFilter: läuft nach jedem Render und verbirgt Kacheln, deren
// Anfangsbuchstabe nicht zum aktiven Filter passt. Liest den Titel aus
// `.card-title` der gerade gerenderten Kachel — funktioniert sowohl für
// Folder- als auch Item-Cards, ohne dass beide Render-Funktionen
// einzelne data-Attribute setzen müssen.
function applyAlphaFilter() {
  const grid = $("#grid");
  if (!grid) return;
  const ch = state.alphaFilter;
  for (const card of grid.querySelectorAll(".card")) {
    if (!ch) {
      card.classList.remove("alpha-hidden");
      continue;
    }
    const titleEl = card.querySelector(".card-title");
    const t = (titleEl ? titleEl.textContent : "").replace(/^\W+/, "").toUpperCase();
    let match;
    if (ch === "#") match = /^\d/.test(t);
    else match = !!t && t.startsWith(ch);
    card.classList.toggle("alpha-hidden", !match);
  }
}

// --- Bulk-Selection ---

function setSelectionMode(on) {
  state.selectionMode = on;
  document.body.classList.toggle("selection-mode", on);
  $("#selectModeBtn").classList.toggle("active", on);
  if (!on) {
    state.selection.clear();
    updateBulkBar();
    // Kartenstyling zurücksetzen
    document.querySelectorAll(".card.selected").forEach(c => c.classList.remove("selected"));
    document.querySelectorAll(".card-select").forEach(c => c.textContent = "");
  } else {
    updateBulkBar();
  }
}

function toggleSelection(item) {
  if (state.selection.has(item.id)) {
    state.selection.delete(item.id);
  } else {
    state.selection.add(item.id);
  }
  const card = document.querySelector(`.card[data-item-id="${item.id}"]`);
  if (card) {
    const on = state.selection.has(item.id);
    card.classList.toggle("selected", on);
    const box = card.querySelector(".card-select");
    if (box) box.textContent = on ? "✓" : "";
  }
  updateBulkBar();
}

function updateBulkBar() {
  const bar = $("#bulkBar");
  if (!bar) return;
  if (!state.selectionMode) { bar.classList.add("hidden"); return; }
  bar.classList.remove("hidden");
  $("#bulkCount").textContent = `${state.selection.size} ausgewählt`;
}

function selectAllVisible() {
  for (const it of state.lastRenderedItems || []) {
    if (it && it.id) state.selection.add(it.id);
  }
  // Alle Kacheln visuell markieren
  document.querySelectorAll(".card").forEach(c => {
    const id = Number(c.dataset.itemId);
    if (id && state.selection.has(id)) {
      c.classList.add("selected");
      const b = c.querySelector(".card-select");
      if (b) b.textContent = "✓";
    }
  });
  updateBulkBar();
}

function selectedItems() {
  return (state.lastRenderedItems || []).filter(it => state.selection.has(it.id));
}

async function bulkSetFavorite() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  let fails = 0;
  for (const id of ids) {
    try {
      await api(`/api/items/${id}/favorite`, { method: "PUT", body: JSON.stringify({ favorite: true }) });
    } catch { fails++; }
  }
  if (fails) appAlert(`${fails} von ${ids.length} fehlgeschlagen.`);
  setSelectionMode(false);
  loadItems();
}

async function bulkSetWatched() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  let fails = 0;
  for (const id of ids) {
    try {
      await api(`/api/items/${id}/watched`, { method: "PUT", body: JSON.stringify({ watched: true }) });
    } catch { fails++; }
  }
  if (fails) appAlert(`${fails} von ${ids.length} fehlgeschlagen.`);
  setSelectionMode(false);
  loadItems();
}

async function bulkAddToPlaylist() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  // Wiederverwendung des Detail-Dialogs: zeigt die Liste aller Playlists UND
  // das Quick-Create-Formular, sodass auch aus der Bulk-Auswahl direkt eine
  // neue Playlist mit den gewählten Videos erstellt werden kann.
  await openAddToPlaylist({ itemIDs: ids });
}

async function bulkDownload() {
  const items = selectedItems();
  if (!items.length) return;
  if (!(await appConfirm(`${items.length} Dateien herunterladen? Dein Browser blockiert evtl. mehrfache Downloads — bestätige die Nachfrage.`))) return;
  // Zeitversetzt triggern, damit Browser nicht als Popup-Spam blockt
  items.forEach((it, i) => {
    setTimeout(() => {
      const a = document.createElement("a");
      a.href = `/api/download/${it.id}`;
      a.download = "";
      document.body.appendChild(a);
      a.click();
      a.remove();
    }, i * 400);
  });
}

async function bulkDelete() {
  const ids = Array.from(state.selection);
  if (!ids.length) return;
  if (!(await appConfirm(`${ids.length} Dateien endgültig löschen (inkl. Datei auf Disk)?`))) return;
  let fails = 0;
  for (const id of ids) {
    try {
      await api(`/api/items/${id}?deleteFile=true`, { method: "DELETE" });
    } catch { fails++; }
  }
  if (fails) appAlert(`${fails} von ${ids.length} fehlgeschlagen.`);
  setSelectionMode(false);
  loadItems();
}

// Staffel-Ansicht-Einstellungen (3-stufig):
//  - pro Serie:  seasonView:<libID>:<folder> = "1" / "0"  (setzt Default außer Kraft)
//  - pro Library: seasonView:lib:<libID>         = "1" / "0"  (Default für alle Serien)
// Effective = pro Serie if set, else library-Default, else false.
function seasonViewEffective() {
  try {
    if (state.currentFolder) {
      const perFolder = localStorage.getItem(`seasonView:${state.currentLibrary || 0}:${state.currentFolder}`);
      if (perFolder === "1") return true;
      if (perFolder === "0") return false;
    }
    // Default: AN für TV-Bibliotheken (Serien sollen automatisch in der
    // Staffel-Ansicht öffnen), außer der User hat sie für diese Library
    // explizit ausgeschaltet (gespeichert als "0"). Vorher war der Default
    // "0"/aus, bis man einmal manuell umschaltete.
    return localStorage.getItem(`seasonView:lib:${state.currentLibrary || 0}`) !== "0";
  } catch { return true; }
}

function sortStorageKey() {
  if (state.currentLibrary) {
    const folder = state.currentFolder || "";
    return `sort:lib:${state.currentLibrary}:${folder}`;
  }
  if (state.collectionsView) return "sort:collections";
  if (state.currentPlaylist) return `sort:playlist:${state.currentPlaylist}`;
  if (state.playlistsView) return "sort:playlists";
  if (state.personFilter) return "sort:person";
  return "";
}

// Pseudo-Filter-Modi (im Sort-Dropdown technisch ein "Sort", semantisch
// aber globale Filter): nicht per-Folder persistieren — sonst bekommt der
// User beim erneuten Öffnen des Ordners überraschend „Nur unmatched".
// Gleichzeitig sollen sie beim Folder-Wechsel NICHT durch die per-Folder-
// Default-Sortierung ersetzt werden — sonst verliert man den Filter sofort
// beim Reingehen und sieht alle Items, statt der unmatched/duplicates/etc.
const PSEUDO_FILTER_MODES = new Set([
  "unmatched", "favorites", "duplicates", "suspicious", "interlaced", "namedupes",
]);

// Sort-Modi, die eine flache, library-weite Liste zeigen (Ordner-Struktur
// ignorieren). Werden pro Library/Folder gespeichert — ABER NUR in einem
// Kontext, der sowieso nie Unterordner-Kacheln zeigt. Kacheln erscheinen
// laut Navigationskonzept ausschließlich im Library-Root (state.currentFolder
// === null) oder in einem Ordner mit aktiviertem Drilldown
// (state.currentFolderDrilldown); jeder normale Unterordner zeigt ohnehin
// IMMER eine flache Dateiliste (siehe grid.js "Standard: Subfolder ohne
// Drilldown"). Dort ist "flach sortiert" == "normal", also unproblematisch
// zu speichern. Im Root/Drilldown-Fall würde die gespeicherte flache Sortierung
// die Ordner-Kacheln beim Wiederbetreten dauerhaft verstecken — das war der
// ursprüngliche Grund, warum diese Sorts früher nie gespeichert wurden.
const FLAT_LIBRARY_SORTS = new Set(["played", "added", "duration"]);

function currentContextShowsFolderTiles() {
  return state.currentFolder === null || !!state.currentFolderDrilldown;
}

function persistSortForContext() {
  const key = sortStorageKey();
  if (!key) return;
  const s = $("#sortSelect").value;
  if (PSEUDO_FILTER_MODES.has(s)) return;
  if (FLAT_LIBRARY_SORTS.has(s) && currentContextShowsFolderTiles()) return;
  const d = state.sortDir;
  try {
    if (!s && !d) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify({ sort: s, dir: d }));
  } catch {}
}

function restoreSortForContext() {
  // Aktiven Pseudo-Filter beibehalten — global gedacht, nicht per-Folder.
  if (PSEUDO_FILTER_MODES.has($("#sortSelect").value)) return;
  const key = sortStorageKey();
  if (!key) return;
  try {
    const raw = localStorage.getItem(key);
    let parsed = null;
    if (raw) { try { parsed = JSON.parse(raw); } catch {} }
    // Falls sich der Ordner seit dem Speichern geändert hat (z. B. Drilldown
    // nachträglich aktiviert) und jetzt Unterordner-Kacheln zeigt: einen
    // gespeicherten Flat-Sort NICHT übernehmen, sonst blieben die Kacheln
    // dauerhaft versteckt. Wie "kein Sort gespeichert" behandeln.
    if (parsed && parsed.sort && FLAT_LIBRARY_SORTS.has(parsed.sort) && currentContextShowsFolderTiles()) {
      parsed = null;
    }
    if (!raw || !parsed || !parsed.sort) {
      // Kein (verwendbarer) gespeicherter Sort für diesen Kontext → Library-spezifischer Default.
      // Private Libraries (YouTube-Downloads, Urlaubsvideos etc.) sollen nach
      // „Veröffentlicht" absteigend sortiert sein — Chronologie ist dort die
      // sinnvolle Reihenfolge. Alle anderen bleiben auf "title" aufsteigend.
      const lib = state.libraries && state.libraries.find(l => l.id == state.currentLibrary);
      if (lib && lib.kind === "private") {
        // Chronologisch aufsteigend: älteste zuerst (bei YouTube/Urlaubsvideos
        // üblicherweise die Wunsch-Reihenfolge). `effectiveSortDir` würde für
        // "released" sonst "desc" liefern, deshalb explizit "asc" setzen.
        $("#sortSelect").value = "released";
        state.sortDir = "asc";
      } else {
        $("#sortSelect").value = "title";
        state.sortDir = "";
      }
      updateSortDirIcon();
      return;
    }
    const { sort, dir } = parsed;
    if (sort) $("#sortSelect").value = sort;
    state.sortDir = dir || "";
    updateSortDirIcon();
  } catch {}
}

// Standard-Richtung je Sort-Feld. "title" wird natürlich aufsteigend sortiert,
// zeitbezogene Felder absteigend (neueste zuerst).
function effectiveSortDir() {
  if (state.sortDir === "asc" || state.sortDir === "desc") return state.sortDir;
  const s = $("#sortSelect").value;
  return s === "title" || s === "episode" ? "asc" : "desc";
}

function updateSortDirIcon() {
  const btn = $("#sortDirBtn");
  if (!btn) return;
  // Modi ohne sinnvolle Richtung: Pseudo-Filter (Favoriten/Duplikate/etc.)
  // und „Zufällig". Button wird ausgegraut + disabled, damit der User nicht
  // erwartet dass ein Klick was bewirkt.
  const v = $("#sortSelect").value || "title";
  const directionless = (v === "shuffle" || v === "favorites" || v === "unmatched"
                       || v === "duplicates" || v === "suspicious" || v === "interlaced"
                       || v === "namedupes");
  if (directionless) {
    btn.disabled = true;
    btn.classList.add("is-disabled");
    btn.textContent = v === "shuffle" ? "🎲" : "—";
    btn.title = v === "shuffle" ? "Zufällige Reihenfolge — keine Sortierrichtung" : "Sortierrichtung in diesem Modus nicht relevant";
    return;
  }
  btn.disabled = false;
  btn.classList.remove("is-disabled");
  btn.textContent = effectiveSortDir() === "asc" ? "⬆" : "⬇";
  btn.title = effectiveSortDir() === "asc" ? "Aufsteigend · klick für absteigend" : "Absteigend · klick für aufsteigend";
}

// Multi-Select-Auflösungs-Filter: alle ausgewählten Buckets als `bucket=xxx`
// an den Server übergeben. Backend ORed die Höhen-Ranges.
function applyResolutionFilter(params) {
  for (const b of state.resBuckets) params.append("bucket", b);
}

// Persönliche-Sternebewertung-Filter (`#ratingFilter`): unrated/min1/min2/exact3
// als `rating=` an /api/items. Additiv, wirkt in der normalen Grid-Ansicht wie
// der Gesehen-Filter.
function applyRatingFilter(params) {
  const v = $("#ratingFilter") && $("#ratingFilter").value;
  if (v) params.set("rating", v);
}
function currentRatingFilter() {
  return ($("#ratingFilter") && $("#ratingFilter").value) || "";
}

function setupResolutionDropdown() {
  const btn = $("#resDropdownBtn");
  const panel = $("#resDropdown");
  if (!btn || !panel) return;
  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = panel.classList.toggle("hidden");
    btn.setAttribute("aria-expanded", String(!open));
  });
  panel.addEventListener("click", (e) => e.stopPropagation());
  document.addEventListener("click", () => {
    if (!panel.classList.contains("hidden")) {
      panel.classList.add("hidden");
      btn.setAttribute("aria-expanded", "false");
    }
  });
  panel.querySelectorAll('input[type="checkbox"][data-bucket]').forEach(cb => {
    cb.addEventListener("change", () => {
      const b = cb.dataset.bucket;
      if (cb.checked) state.resBuckets.add(b);
      else state.resBuckets.delete(b);
      updateResDropdownLabel();
      loadItems();
    });
  });
  updateResDropdownLabel();
}

function updateResDropdownLabel() {
  const btn = $("#resDropdownBtn");
  if (!btn) return;
  const n = state.resBuckets.size;
  if (n === 0) {
    btn.textContent = "Alle ▾";
  } else if (n <= 2) {
    btn.textContent = Array.from(state.resBuckets).join(", ") + " ▾";
  } else {
    btn.textContent = `${n} Auflösungen ▾`;
  }
}

async function toggleWatchedOnCard(item, btn) {
  const newState = !item.watched;
  btn.disabled = true;
  try {
    await api(`/api/items/${item.id}/watched`, {
      method: "PUT",
      body: JSON.stringify({ watched: newState }),
    });
    item.watched = newState;
    btn.classList.toggle("is-on", newState);
    btn.title = newState ? "Als ungesehen markieren" : "Als gesehen markieren";
    // Card-Dimming auf "watched"-Klasse halten
    const card = btn.closest(".card");
    if (card) card.classList.toggle("watched", newState);
  } catch (e) {
    appAlert(e.message);
  } finally {
    btn.disabled = false;
  }
}

async function toggleFavoriteOnCard(item, btn) {
  const newState = !item.favorite;
  btn.disabled = true;
  try {
    await api(`/api/items/${item.id}/favorite`, {
      method: "PUT",
      body: JSON.stringify({ favorite: newState }),
    });
    item.favorite = newState;
    btn.classList.toggle("is-on", newState);
    btn.textContent = newState ? "♥" : "♡";
    btn.title = newState ? "Aus Favoriten entfernen" : "Zu Favoriten hinzufügen";
  } catch (e) {
    appAlert(e.message);
  } finally {
    btn.disabled = false;
  }
}

// resLabel liegt in helpers.js.

// App-Dialoge (appAlert/appConfirm/appPrompt/showToast) liegen in dialogs.js
// und werden via separatem <script>-Tag VOR app.js geladen.

// escapeHTML liegt in helpers.js.

// --- Libraries ---

async function loadLibraries() {
  [state.libraries, state.playlists] = await Promise.all([
    api("/api/libraries"),
    api("/api/playlists").catch(() => []),
  ]);
  const sel = $("#librarySelect");
  sel.innerHTML = "";
  if (state.libraries.length === 0 && state.playlists.length === 0) {
    sel.innerHTML = `<option value="">— keine Quelle —</option>`;
    state.currentLibrary = null;
    state.currentPlaylist = null;
    return;
  }
  if (state.libraries.length) {
    const og = document.createElement("optgroup");
    og.label = "Bibliotheken";
    for (const l of state.libraries) {
      const o = document.createElement("option");
      o.value = "lib:" + l.id;
      o.textContent = l.name;
      og.appendChild(o);
    }
    sel.appendChild(og);
  }
  // Playlists werden NICHT ins Library-Dropdown eingebaut — eigener Button
  // im Header zeigt die Playlist-Übersicht.
  // Aktuelle Auswahl wiederherstellen oder Default setzen
  let currentVal = "";
  if (state.currentPlaylist && state.playlists.find(p => p.id == state.currentPlaylist)) {
    currentVal = "pl:" + state.currentPlaylist;
  } else if (state.currentLibrary && state.libraries.find(l => l.id == state.currentLibrary)) {
    currentVal = "lib:" + state.currentLibrary;
  } else if (state.libraries.length) {
    currentVal = "lib:" + state.libraries[0].id;
    state.currentLibrary = state.libraries[0].id;
    state.currentPlaylist = null;
  } else if (state.playlists.length) {
    currentVal = "pl:" + state.playlists[0].id;
    state.currentPlaylist = state.playlists[0].id;
    state.currentLibrary = null;
  }
  sel.value = currentVal;
  renderLibNav();
}

// goHomeView / goCollectionsView / goLibraryView wechseln in den jeweiligen
// Bereich und resetten die Navigation. Werden sowohl von der pinned Lib-Nav
// als auch aus diversen Codepfaden (Home-Kachel, Search-Result-Klick, …) aufgerufen.
function goHomeView() {
  state.homeView = true;
  state.currentLibrary = null;
  state.currentFolder = null;
  state.currentPlaylist = null;
  state.collectionsView = false;
  state.currentCollection = null;
  state.playlistsView = false;
  state.personFilter = null;
  state.personFilterShow = null;
  const sel = $("#librarySelect"); if (sel) sel.value = "";
  resetFilters();
  loadItems();
}
function goCollectionsView() {
  state.collectionsView = true;
  state.currentCollection = null;
  state.currentLibrary = null;
  state.currentFolder = null;
  state.currentPlaylist = null;
  state.playlistsView = false;
  state.homeView = false;
  state.personFilter = null;
  state.personFilterShow = null;
  const sel = $("#librarySelect"); if (sel) sel.value = "";
  resetFilters();
  loadItems();
}
function goPlaylistsView() {
  enterPlaylistsView();
  const sel = $("#librarySelect"); if (sel) sel.value = "";
  loadItems();
}
function goLibraryView(libId) {
  state.currentFolder = null;
  state.currentFolderDrilldown = null;
  state.currentSeason = null;
  state.collectionsView = false;
  state.currentCollection = null;
  state.playlistsView = false;
  state.currentPlaylist = null;
  state.homeView = false;
  state.personFilter = null;
  state.personFilterShow = null;
  state.currentLibrary = Number(libId) || null;
  const sel = $("#librarySelect");
  if (sel) sel.value = state.currentLibrary ? "lib:" + state.currentLibrary : "";
  resetFilters();
  loadItems();
}

// libIcon: Emoji-Icon pro Library-Kind. Gemeinsam genutzt von Lib-Nav und
// Breadcrumb, damit Icon und Name immer konsistent sind.
function libIcon(lib) {
  if (!lib) return "📁";
  if (lib.kind === "movies") return "🎬";
  if (lib.kind === "tv") return "📺";
  if (lib.kind === "private") return "🎥";
  return "📁";
}

// renderLibNav baut die zentrale, sticky Buttons-Leiste mit allen Quellen:
// Startseite · [eine Library pro Button] · Sammlungen · Playlists.
// Aktive Auswahl wird hervorgehoben. Wird nach loadLibraries() und nach jedem
// Navigationswechsel (in renderBreadcrumb) gerufen.
function renderLibNav() {
  const nav = $("#libNav");
  if (!nav) return;
  nav.innerHTML = "";

  const make = (label, isActive, onClick, title) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "lib-nav-btn" + (isActive ? " is-active" : "");
    b.textContent = label;
    if (title) b.title = title;
    b.addEventListener("click", onClick);
    return b;
  };
  const sep = () => { const s = document.createElement("span"); s.className = "lib-nav-sep"; return s; };

  // Startseite
  nav.appendChild(make("🏠 Startseite", !!state.homeView, goHomeView, "Startseite"));

  // Pro Library ein Button
  const libs = state.libraries || [];
  if (libs.length) nav.appendChild(sep());
  for (const l of libs) {
    const active = !state.homeView && !state.collectionsView && !state.playlistsView
      && !state.currentPlaylist && !state.personFilter && state.currentLibrary == l.id;
    nav.appendChild(make(`${libIcon(l)} ${l.name}`, active, () => goLibraryView(l.id), l.name));
  }

  // Sammlungen + Playlists
  nav.appendChild(sep());
  nav.appendChild(make("📚 Sammlungen", !!state.collectionsView, goCollectionsView, "Sammlungen"));
  nav.appendChild(make("📋 Playlists", !!(state.playlistsView || state.currentPlaylist), goPlaylistsView, "Playlists"));
}

// navKey: stabiler String, der den aktuellen Grid-Kontext beschreibt.
// Wird für scrollPositions (Map: navKey → scrollY) genutzt, damit die Seite
// beim Zurück-Navigieren wieder an der vorherigen Y-Position landet.
function navKey() {
  if (state.homeView) return "home";
  if (state.collectionsView) return state.currentCollection ? "col:" + state.currentCollection.id : "col-root";
  if (state.playlistsView && !state.currentPlaylist) return "pl-root";
  if (state.currentPlaylist) return "pl:" + state.currentPlaylist;
  if (state.personFilter) return "person:" + state.personFilter.tmdbId;
  if (state.currentLibrary) {
    const f = state.currentFolder || "";
    const s = state.currentSeason != null ? ":s" + state.currentSeason : "";
    return "lib:" + state.currentLibrary + ":" + f + s;
  }
  return "root";
}

// loadItems + loadItemsBody liegen in grid.js.


// renderHomeView (Startseite) und Staffel-Ansicht liegen in views.js.
// renderRangeContinuationCard ist auch dort (Doppelfolgen-Slot).


// renderCard und Verwandte (renderPlaylistCard, renderFolderCard,
// renderCollectionCard, renderPersonShowCard, hidePartButton,
// openMissingMovieDialog) liegen in cards.js.

// Player + Detail-View + Trickplay-Hover + Buffer-Gate liegen in player.js.

// User-Menue + Admin-Panel liegen in admin.js.

// Playlists + Zufaellige Wiedergabe liegen in playlists.js.

// Scan + Globale Trickplay-Statusleiste liegen in scan.js.

// Manage Libraries + Path Browser + Settings liegen in admin.js.

// Manuelles Matching liegt in matching.js.

// --- Boot ---

async function loadHealth() {
  try {
    const h = await api("/api/health");
    if (h.hwaccel) state.hwaccel = h.hwaccel;
  } catch {}
}

async function loadSettings() {
  try { state.settings = await api("/api/settings"); } catch {}
  // Auto-Scan-Status für Menü-Subtitle laden (nicht blockierend)
  try {
    const asTasks = await api("/api/settings/autoscan");
    if (typeof updateAutoScanMenuSub === "function") updateAutoScanMenuSub(asTasks);
  } catch {}
}

function wire() {
  $("#librarySelect").addEventListener("change", (e) => {
    const val = e.target.value || "";
    state.currentFolder = null;
    state.currentFolderDrilldown = false;
    // Beim Library-Wechsel alle Filter + Suche zurücksetzen, damit nicht ein
    // Filter aus der alten Bibliothek den neuen Katalog versteckt.
    resetFilters();
    state.collectionsView = false;
    state.currentCollection = null;
    state.playlistsView = false;
    state.homeView = false;
    if (val.startsWith("pl:")) {
      const pid = Number(val.slice(3)) || null;
      if (pid) enterPlaylist(pid); else state.currentPlaylist = null;
      state.currentLibrary = null;
    } else if (val.startsWith("lib:")) {
      state.currentLibrary = Number(val.slice(4)) || null;
      state.currentPlaylist = null;
    }
    // "col:" ist raus aus dem Dropdown — Sammlungen haben jetzt einen eigenen
    // Topbar-Button (#collectionsBtn) neben Home und Playlists.
    loadItems();
  });
  // 500 ms Debounce — Suche ist bei großen Libraries server-seitig teuer
  // (LIKE+EXISTS auf Cast). Unter dem Wert feuert jeder Tastendruck einen
  // neuen Request, der den vorigen überholt — UI fühlt sich „eingefroren" an.
  $("#searchInput").addEventListener("input", debounce(loadItems, 500));
  $("#searchClear").addEventListener("click", () => {
    $("#searchInput").value = "";
    $("#searchInput").focus();
    loadItems();
  });
  $("#sortSelect").addEventListener("change", () => {
    state.sortDir = "";
    updateSortDirIcon();
    persistSortForContext();
    loadItems();
  });
  $("#sortDirBtn").addEventListener("click", () => {
    const current = effectiveSortDir();
    state.sortDir = current === "asc" ? "desc" : "asc";
    updateSortDirIcon();
    persistSortForContext();
    loadItems();
  });
  updateSortDirIcon();
  $("#watchedFilter").addEventListener("change", loadItems);
  { const rf = $("#ratingFilter"); if (rf) rf.addEventListener("change", loadItems); }
  setupResolutionDropdown();
  $("#flatViewBtn").addEventListener("click", () => {
    state.flatView = !state.flatView;
    $("#flatViewBtn").classList.toggle("active", state.flatView);
    try { localStorage.setItem("flatView", state.flatView ? "1" : "0"); } catch {}
    loadItems();
  });
  $("#seasonViewBtn").addEventListener("click", () => {
    // Toggle je nach Kontext:
    //  - In Show-Ordner: per-Folder-Override setzen (0/1), explizit
    //  - In Library-Root: library-weiter Default
    const next = !seasonViewEffective();
    try {
      if (state.currentFolder) {
        // pro-Serie-Override: explizit "1" oder "0" (nicht entfernen —
        // sonst greift wieder der Library-Default)
        localStorage.setItem(`seasonView:${state.currentLibrary || 0}:${state.currentFolder}`, next ? "1" : "0");
      } else {
        localStorage.setItem(`seasonView:lib:${state.currentLibrary || 0}`, next ? "1" : "0");
      }
    } catch {}
    state.seasonView = next;
    state.currentSeason = null;
    $("#seasonViewBtn").classList.toggle("active", state.seasonView);
    loadItems();
  });
  $("#selectModeBtn").addEventListener("click", () => setSelectionMode(!state.selectionMode));
  $("#alphaToggleBtn").addEventListener("click", () => {
    const key = alphaSidebarStorageKey();
    if (!key) return;
    const next = alphaSidebarHiddenHere();
    try {
      if (next) localStorage.removeItem(key); // wieder einblenden → Default (an)
      else localStorage.setItem(key, "0");
    } catch {}
    updateAlphaSidebar();
  });
  $("#bulkSelectAll").addEventListener("click", selectAllVisible);
  $("#bulkSelectNone").addEventListener("click", () => setSelectionMode(false));
  $("#bulkFavorite").addEventListener("click", bulkSetFavorite);
  $("#bulkWatched").addEventListener("click", bulkSetWatched);
  $("#bulkPlaylist").addEventListener("click", bulkAddToPlaylist);
  // Merge-Aktion lebt jetzt im Zahnrad-Menü (data-action="merge"),
  // der alte Topbar-Button wurde entfernt.
  $("#bulkDownload").addEventListener("click", bulkDownload);
  $("#bulkDelete").addEventListener("click", bulkDelete);

  // Cancel-Buttons in den Status-Bars (delegiert, weil sie per innerHTML
  // rerendert werden)
  $("#scanStatus").addEventListener("click", (e) => {
    if (e.target.closest('[data-action="cancel-scan"]')) cancelScanRun();
  });
  $("#trickplayStatus").addEventListener("click", (e) => {
    if (e.target.closest('[data-action="cancel-trickplay"]')) cancelTrickplayRun();
  });

  // Trickplay-Manager-Tabs + Delete-All
  document.querySelectorAll("#trickplayDialog [data-tp-tab]").forEach(btn => {
    btn.addEventListener("click", () => {
      state.tpTab = btn.dataset.tpTab;
      refreshTrickplayManager();
    });
  });
  $("#tpDeleteAll").addEventListener("click", deleteAllTrickplay);
  $("#tpRetryFailed").addEventListener("click", retryFailedTrickplay);
  const tpShowFailed = $("#tpShowFailed");
  if (tpShowFailed) tpShowFailed.addEventListener("click", openTrickplayFailedView);
  $("#shuffleBtn").addEventListener("click", playRandom);
  $("#shuffleScopeBtn").addEventListener("click", openShuffleScopeDialog);
  $("#shuffleScopeApply").addEventListener("click", applyShuffleScope);
  $("#shuffleScopeReset").addEventListener("click", resetShuffleScope);
  $("#scanBtn").addEventListener("click", () => {
    // Inside einem Ordner: Default ist ordner-gescopt. Am Library-Root: gesamte Library.
    startScan(state.currentFolder ? "folder" : "incremental");
  });
  $("#scanMenuBtn").addEventListener("click", (e) => {
    e.stopPropagation();
    // Ordner-spezifische Einträge nur sichtbar, wenn wir in einem Ordner sind
    const inFolder = !!state.currentFolder;
    $("#scanMenu").querySelectorAll('[data-scope="folder"]').forEach(b => {
      b.style.display = inFolder ? "" : "none";
      if (inFolder) {
        const f = state.currentFolder.split("/").pop();
        if (b.dataset.scan === "folder") b.textContent = `„${f}" (inkrementell)`;
        else b.textContent = `„${f}" (force)`;
      }
    });
    $("#scanMenu").classList.toggle("hidden");
  });
  $("#scanMenu").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-scan]");
    if (!btn) return;
    $("#scanMenu").classList.add("hidden");
    startScan(btn.dataset.scan);
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#scanMenu, #scanMenuBtn")) {
      $("#scanMenu").classList.add("hidden");
    }
  });
  // Settings Side Drawer
  function openDrawer() {
    $("#settingsMenu").classList.add("open");
    $("#settingsMenu").setAttribute("aria-hidden", "false");
    $("#drawerOverlay").classList.remove("hidden");
    requestAnimationFrame(() => $("#drawerOverlay").classList.add("active"));
  }
  function closeDrawer() {
    $("#settingsMenu").classList.remove("open");
    $("#settingsMenu").setAttribute("aria-hidden", "true");
    $("#drawerOverlay").classList.remove("active");
    setTimeout(() => $("#drawerOverlay").classList.add("hidden"), 250);
  }
  $("#settingsBtn").addEventListener("click", (e) => {
    e.stopPropagation();
    $("#settingsMenu").classList.contains("open") ? closeDrawer() : openDrawer();
  });
  $("#drawerClose").addEventListener("click", closeDrawer);
  $("#drawerOverlay").addEventListener("click", closeDrawer);
  // Escape-Taste schließt Drawer
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && $("#settingsMenu").classList.contains("open")) closeDrawer();
  });
  $("#settingsMenu").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-action]");
    if (!btn) return;
    closeDrawer();
    switch (btn.dataset.action) {
      case "settings":  openSettings(); break;
      case "libraries": openManage(); break;
      case "users":     openUsersManager(); break;
      case "trickplay": openTrickplayManager(); break;
      case "whisper":   openWhisperDialog(); break;
      case "introskip": openIntroSkipDialog(); break;
      case "ocrsubs": openOCRSubDialog(); break;
      case "pathsearch": openPathSearch(); break;
      case "missing": openMissingDialog(); break;
      case "refreshallmeta": runRefreshAllMetadata(); break;
      case "renames": openRenamesManager(); break;
      case "autoscan": openAutoScan(); break;
      case "statistik": openStatistikDialog(); break;
    }
  });
  // Home/Sammlungen/Playlists/Library-Wechsel laufen über die pinned Lib-Nav
  // (renderLibNav). Die alten Topbar-Icon-Buttons sind raus.
  $("#playlistForm").addEventListener("submit", handleNewPlaylist);
  $("#userForm").addEventListener("submit", handleNewUser);
  $("#passwordForm").addEventListener("submit", handleMyPassword);
  $("#detailAddPlaylist").addEventListener("click", openAddToPlaylist);
  $("#quickCreatePlaylistForm").addEventListener("submit", handleQuickCreatePlaylist);
  $("#libForm").addEventListener("submit", handleAddLibrary);
  $("#browseBtn").addEventListener("click", () => openBrowse($("#libPathInput").value || "/media"));
  $("#browseChoose").addEventListener("click", chooseBrowse);
  $("#settingsForm").addEventListener("submit", saveSettings);
  $("#bufRange").addEventListener("input", (e) => { $("#bufVal").textContent = e.target.value; });
  $("#startBufRange").addEventListener("input", (e) => { $("#startBufVal").textContent = e.target.value; });
  $("#tpInterval").addEventListener("input", (e) => { $("#tpIntervalVal").textContent = e.target.value; });
  $("#playerClose").addEventListener("click", closePlayer);
  $("#playerDialog").addEventListener("close", () => {
    // Letzte Position merken, bevor der Player verworfen wird.
    if (state.vjs && state.currentItem) {
      try {
        const cur = state.vjs.currentTime();
        if (isFinite(cur) && cur >= 5) {
          const abs = cur + ((state.playback && state.playback.virtualOffset) || 0);
          api(`/api/items/${state.currentItem.id}/resume`, {
            method: "PUT",
            body: JSON.stringify({ positionSec: abs }),
          }).catch(() => {});
        }
      } catch {}
    }
    disposePlayer();
  });
  setupDialogDrag();
  $("#modeSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value || "orig";
    applyPlayback(state.currentItem, mode, profile);
  });
  $("#profileSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value;
    const audio = $("#audioSelect").value;
    applyPlayback(state.currentItem, mode, profile, audio ? Number(audio) : null);
  });
  $("#audioSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value;
    const audio = $("#audioSelect").value;
    applyPlayback(state.currentItem, mode, profile, audio ? Number(audio) : null);
  });
  $("#deinterlaceSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    const mode = $("#modeSelect").value;
    const profile = $("#profileSelect").value;
    const audio = $("#audioSelect").value;
    const dei = $("#deinterlaceSelect").value;
    applyPlayback(state.currentItem, mode, profile, audio ? Number(audio) : null, dei);
  });
  $("#subSelect").addEventListener("change", () => {
    if (!state.currentItem) return;
    // Untertitel wechseln — Player selbst bleibt, nur Text-Track umschalten
    const vjs = state.vjs;
    if (!vjs) return;
    // alle Remote-Tracks entfernen, dann ggf. den gewählten neu setzen
    const existing = vjs.remoteTextTracks();
    for (let i = existing.length - 1; i >= 0; i--) {
      vjs.removeRemoteTextTrack(existing[i]);
    }
    const choice = $("#subSelect").value;
    if (choice) {
      const streams = (state.playback && state.playback.streams) || [];
      const sub = streams.find(s => String(s.index) === choice);
      const label = (sub && sub.title) || (sub && sub.language && sub.language.toUpperCase()) || "Untertitel";
      vjs.addRemoteTextTrack({
        kind: "subtitles",
        src: `/api/subtitle/${state.currentItem.id}/${choice}.vtt`,
        srclang: (sub && sub.language) || "und",
        label: label,
        default: true,
      }, false);
    }
  });
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-close]");
    if (btn) btn.closest("dialog").close();
  });
  // Detail-Dialog-Buttons
  $("#detailPlay").addEventListener("click", () => {
    $("#detailDialog").close();
    if (state.currentItem) openPlayer(state.currentItem);
  });
  $("#detailMatch").addEventListener("click", () => {
    if (state.currentItem) {
      $("#detailDialog").close();
      openMatchItem(state.currentItem);
    }
  });
  $("#detailEditMeta").addEventListener("click", openEditMetaDialog);
  $("#editMetaForm").addEventListener("submit", handleEditMetaSubmit);
  $("#editMetaPoster").addEventListener("click", openPosterPicker);
  $("#posterUploadForm").addEventListener("submit", handlePosterUpload);
  $("#detailConfirm").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const next = !state.currentItem.metadataConfirmed;
    const id = state.currentItem.id;
    try {
      await api(`/api/items/${id}/confirm`, {
        method: "PUT",
        body: JSON.stringify({ confirmed: next }),
      });
      // Server kann beim Confirm einen Auto-Rename ausgeloest haben (Setting
      // auto_rename_confirmed_movies). Item neu holen, damit der lokale State
      // den ggf. neuen Pfad/Dateinamen kennt — dann lokale Felder + Kachel im
      // Grid lautlos aktualisieren (kein loadItems, Scroll-Position bleibt).
      const fresh = await silentlyRefreshItem(id);
      if (fresh && state.currentItem && state.currentItem.id === id) {
        // Variants-Patch beibehalten, falls der Aufrufer mit gemergten Items kam.
        const carriedVariants = state.currentItem._variants;
        state.currentItem = fresh;
        if (carriedVariants) state.currentItem._variants = carriedVariants;
        const fh = $("#detailFileHint");
        if (fh) fh.innerHTML = fileHintHTML(fresh);
      } else if (state.currentItem && state.currentItem.id === id) {
        // Fallback wenn der Refetch fehlschlaegt: zumindest den Confirmed-Flag lokal.
        state.currentItem.metadataConfirmed = next;
      }
      updateConfirmBtn();
    } catch (e) { appAlert("Fehler: " + e.message); }
  });
  $("#detailRefreshMeta").addEventListener("click", async () => {
    if (!state.currentItem) return;
    if (!state.currentItem.metadataId) {
      appAlert("Dieses Item hat keine TMDB-Zuordnung. Nutze zuerst „🔍 Manuell zuordnen\".");
      return;
    }
    const btn = $("#detailRefreshMeta");
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = "⏳";
    try {
      await api(`/api/items/${state.currentItem.id}/refresh-metadata`, { method: "POST" });
      // Item neu laden, damit Plot/Genres/Runtime/Cast/Poster aktualisiert sind
      const fresh = await api(`/api/items/${state.currentItem.id}`);
      invalidateItemsCache();
      openDetail(fresh);
      showToast("Metadaten frisch geladen", { kind: "success" });
    } catch (e) {
      appAlert("Fehler beim Neuladen: " + e.message);
    } finally {
      btn.disabled = false;
      btn.textContent = orig;
    }
  });
  $("#detailNFO").addEventListener("click", async () => {
    if (!state.currentItem) return;
    if (!(await appConfirm(
      "Eine .nfo-Datei neben der Videodatei schreiben?\n\n" +
      "Plex und Jellyfin nutzen diese Datei als Metadaten-Override — die " +
      "Videodatei selbst wird nicht verändert, nur eine Sidecar-Datei " +
      "angelegt.\n\nFunktion nur für Items, deren Zuordnung du bestätigt hast."
    ))) return;
    try {
      const res = await api(`/api/items/${state.currentItem.id}/write-nfo`, { method: "POST" });
      const list = (res.written || []).map(p => "• " + p).join("\n");
      appAlert("NFO geschrieben:\n\n" + (list || "(keine Dateien)"));
    } catch (e) { appAlert("Fehler: " + e.message); }
  });
  // Rename-Button (🏷) im Detail-Dialog. Holt zuerst Preview, zeigt Bestaetigung
  // mit dem Ziel-Namen, fuehrt dann das Rename aus. items.path wird serverseitig
  // mit-aktualisiert, damit Wiedergabe direkt mit dem neuen Namen funktioniert.
  $("#detailRename").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const it = state.currentItem;
    let preview;
    try {
      preview = await api(`/api/items/${it.id}/rename-preview`);
    } catch (e) { appAlert("Vorschau fehlgeschlagen: " + e.message); return; }
    if (!preview.canRename) {
      appAlert("Umbenennen nicht möglich: " + (preview.reason || "unbekannt"));
      return;
    }
    if (preview.alreadyOK) {
      appAlert("Datei trägt bereits den Wunschnamen.");
      return;
    }
    if (!(await appConfirm(
      `Datei umbenennen?\n\nVon:  ${it.path.split("/").pop()}\nNach: ${preview.targetBase}\n\n` +
      "Die Aktion wird in der Umbenennen-Historie protokolliert und kann " +
      "dort einzeln rückgängig gemacht werden."
    ))) return;
    try {
      const res = await api(`/api/items/${it.id}/rename`, { method: "POST" });
      // Lautlos refresh: Detail-Dialog zeigt neuen Pfad, Kachel im Grid wird
      // in-place ersetzt — ohne loadItems(), damit die Scroll-Position bleibt.
      const fresh = await silentlyRefreshItem(it.id);
      if (fresh && state.currentItem && state.currentItem.id === it.id) {
        state.currentItem = fresh;
        const fh = $("#detailFileHint");
        if (fh) fh.innerHTML = fileHintHTML(fresh);
      }
      showToast("Umbenannt zu: " + res.newBase, { kind: "success" });
      invalidateItemsCache();
    } catch (e) { appAlert("Umbenennen fehlgeschlagen: " + e.message); }
  });
  $("#detailDownload").addEventListener("click", () => {
    if (!state.currentItem) return;
    // Browser-Download via Location-Change (Cookie wird mitgeschickt)
    window.location.href = `/api/download/${state.currentItem.id}`;
  });
  $("#detailDelete").addEventListener("click", async () => {
    const it = state.currentItem;
    if (!it) return;
    const name = (it.metadata && it.metadata.title) || it.title;
    if (!(await appConfirm(`Datei "${name}" UNWIEDERRUFLICH vom Server löschen?\n\nPfad: ${it.path}\n\nDies kann nicht rückgängig gemacht werden.`))) return;
    if (!(await appConfirm(`Wirklich sicher? Die Datei wird für IMMER gelöscht.`))) return;
    try {
      await api(`/api/items/${it.id}`, { method: "DELETE" });
      $("#detailDialog").close();
      loadItems();
    } catch (e) { appAlert(e.message); }
  });
  $("#detailMove").addEventListener("click", () => {
    if (!state.currentItem) return;
    openMoveDialog({ mode: "single", item: state.currentItem });
  });
  $("#bulkMove").addEventListener("click", () => {
    if (!state.selection.size) return;
    openMoveDialog({ mode: "bulk", ids: Array.from(state.selection) });
  });
  $("#moveForm").addEventListener("submit", handleMoveSubmit);
  $("#detailWatched").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const it = state.currentItem;
    const newState = !it.watched;
    try {
      await api(`/api/items/${it.id}/watched`, { method: "PUT", body: JSON.stringify({ watched: newState }) });
      it.watched = newState;
      updateDetailWatchedBtn();
      loadItems();
    } catch (e) { appAlert(e.message); }
  });
  $("#detailFavorite").addEventListener("click", async () => {
    if (!state.currentItem) return;
    const it = state.currentItem;
    const newState = !it.favorite;
    try {
      await api(`/api/items/${it.id}/favorite`, { method: "PUT", body: JSON.stringify({ favorite: newState }) });
      it.favorite = newState;
      updateDetailFavBtn();
      loadItems();
    } catch (e) { appAlert(e.message); }
  });
  $("#matchImdbForm").addEventListener("submit", handleMatchImdb);
  // Match-Dialog
  $("#matchForm").addEventListener("submit", handleMatchSearch);
  // TMDB Settings
  $("#tmdbRunBtn").addEventListener("click", runEnrich);
  $("#tmdbClearBtn").addEventListener("click", clearTMDBKey);
  $("#omdbClearBtn").addEventListener("click", clearOMDbKey);
  // Renames-Manager-Buttons
  $("#renamesReloadBtn").addEventListener("click", refreshRenamesManager);
  $("#renamesCsvBtn").addEventListener("click", downloadRenamesCSV);
  $("#renamesBulkBtn").addEventListener("click", runBulkRenameConfirmed);
}

function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

async function checkAuth() {
  try {
    const r = await fetch("/api/auth/status");
    const d = await r.json();
    if (d.setup || !d.loggedIn) {
      location.href = "/login.html";
      return false;
    }
    state.me = { username: d.username, isAdmin: d.isAdmin };
    return true;
  } catch {
    location.href = "/login.html";
    return false;
  }
}

(async function boot() {
  if (!await checkAuth()) return;
  try { state.flatView = localStorage.getItem("flatView") === "1"; } catch {}
  try {
    const raw = localStorage.getItem("shuffleFolders");
    if (raw) state.shuffleFolders = JSON.parse(raw) || [];
  } catch {}
  wire();
  updateShuffleScopeIndicator();
  // Alphabet-Leiste automatisch nach jedem Grid-Render aktualisieren.
  const gridEl = $("#grid");
  if (gridEl && window.MutationObserver) {
    new MutationObserver(() => updateAlphaSidebar()).observe(gridEl, { childList: true });
  }
  // Anfangsbuchstaben-Filter: ✕-Button im Banner hebt den Filter wieder auf.
  const afClose = $("#alphaFilterClose");
  if (afClose) afClose.addEventListener("click", () => setAlphaFilter(null));
  // Topbar ist fixed → Body braucht ein padding-top in Topbar-Höhe. Höhe
  // ändert sich beim Wrapping (viele Buttons → 2 Zeilen), deshalb live
  // nachführen.
  const topbar = document.querySelector(".topbar");
  if (topbar) {
    const syncTop = () => document.documentElement.style.setProperty("--topbar-h", topbar.offsetHeight + "px");
    syncTop();
    if (window.ResizeObserver) new ResizeObserver(syncTop).observe(topbar);
    window.addEventListener("resize", syncTop);
  }
  // Lib-Nav ist ebenfalls fixed (klebt unter der Topbar) → eigene Höhe
  // tracken, damit der Body-Padding-Top beide Höhen einrechnet und Inhalt
  // nicht hinter der Nav verschwindet.
  const libNavEl = document.querySelector("#libNav");
  if (libNavEl) {
    const syncNav = () => document.documentElement.style.setProperty("--lib-nav-h", libNavEl.offsetHeight + "px");
    syncNav();
    if (window.ResizeObserver) new ResizeObserver(syncNav).observe(libNavEl);
    window.addEventListener("resize", syncNav);
  }
  $("#flatViewBtn").classList.toggle("active", state.flatView);
  await Promise.all([loadHealth(), loadSettings(), loadLibraries()]);
  // Beim ersten Laden: Startseite zeigen. loadLibraries() setzt implizit
  // die erste Library als aktuell — wir überschreiben, damit der User die
  // Home-Kacheln als Landing-Page sieht. Ein Klick auf eine Library im
  // Dropdown bringt ihn sofort in den klassischen Grid-View.
  state.homeView = true;
  state.currentLibrary = null;
  state.currentPlaylist = null;
  $("#librarySelect").value = "";
  loadItems();
  checkScanActive();
  // Falls der User die Seite während eines laufenden Bulk-Refresh neu lädt,
  // direkt Status pollen, damit der Fortschritts-Toast wieder erscheint.
  api("/api/enrich/refresh-all-status").then(st => {
    if (st && st.running) pollRefreshAllStatus();
  }).catch(() => {});
  checkTrickplayWorker();
  setInterval(checkScanActive, 30000);
  setInterval(checkTrickplayWorker, 30000);
  renderUserMenu();
  if (typeof initBell === "function") initBell();
  if (typeof startWhisperGlobalPoll === "function") startWhisperGlobalPoll();
  // Google-Cast SDK initialisieren — registriert sich beim Cast-Framework
  // sobald `cast_sender.js` geladen ist und der Receiver-Discovery startet.
  initCastFramework();
})();
