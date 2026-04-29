// api.js — fetch-Wrapper + Items-List-Cache.
//
// Wird VOR app.js geladen (helpers → dialogs → api → app).
//
// Funktionen im window-Scope:
//   api(path, opts)             — fetch-Wrapper mit JSON-Parsing + 401-Redirect
//   apiGetCached(path)          — GET mit 30 s TTL-Cache (nur fuer Array-Listen)
//   invalidateItemsCache()      — Cache leeren (auto bei Mutationen)

// Cache fuer GET /api/items?... — haelt die letzten 5 Listenantworten im Speicher.
// Bei Mutationen (watched/favorite/delete) wird der Cache invalidiert.
const itemsCache = new Map(); // key → { ts, data }
const ITEMS_CACHE_LIMIT = 5;
const ITEMS_CACHE_TTL_MS = 30_000; // 30s

function itemsCacheGet(path) {
  const e = itemsCache.get(path);
  if (!e) return null;
  if (Date.now() - e.ts > ITEMS_CACHE_TTL_MS) { itemsCache.delete(path); return null; }
  return e.data;
}
function itemsCacheSet(path, data) {
  if (itemsCache.size >= ITEMS_CACHE_LIMIT) {
    const firstKey = itemsCache.keys().next().value;
    itemsCache.delete(firstKey);
  }
  itemsCache.set(path, { ts: Date.now(), data });
}
function invalidateItemsCache() { itemsCache.clear(); }

async function apiGetCached(path) {
  const hit = itemsCacheGet(path);
  if (hit) return hit;
  const data = await api(path);
  // Nur Listen-Responses cachen
  if (Array.isArray(data)) itemsCacheSet(path, data);
  return data;
}

async function api(path, opts = {}) {
  // Mutationen invalidieren den Items-Cache
  if (opts.method && opts.method !== "GET") invalidateItemsCache();
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...opts,
  });
  if (res.status === 401) {
    // Session abgelaufen → zum Login leiten
    location.href = "/login.html";
    throw new Error("nicht angemeldet");
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = await res.json();
      if (j.error) msg = j.error;
    } catch {}
    throw new Error(msg);
  }
  if (res.status === 204) return null;
  return res.json();
}
