// Goldfish PWA Service Worker
// Strategie:
//   - HTML / JS / CSS / Manifest → Network-First (Cache nur als Offline-Fallback).
//     Damit greift jeder Deploy SOFORT, auch wenn der SW schon registriert ist.
//   - Bilder / SVG / Fonts (selten geändert) → Cache-First mit Network-Fallback.
//   - API / Streams / Poster / Thumb → vom SW unangetastet (Network-Only durch
//     Pass-Through, weil Range-Requests + Auth-Cookies sonst brechen).
//
// Cache-Version bumpen wenn die Strategie sich ändert — das löscht alte Caches.

const CACHE_NAME = "goldfish-v2";

const PRECACHE_URLS = [
  "/",
  "/index.html",
  "/login.html",
  "/style.css",
  "/favicon.svg",
  "/manifest.webmanifest",
  "/icon-192.png",
  "/icon-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) =>
      Promise.allSettled(PRECACHE_URLS.map((u) => cache.add(u)))
    )
  );
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

// Network-First: JS, CSS, HTML, Manifest. Browser-Cache deaktiviert (no-cache),
// daher revalidiert der Server bei jedem Request → ETag/304 → kaum Overhead.
function isAppShell(url) {
  if (url.pathname === "/" || url.pathname === "/index.html" || url.pathname === "/login.html") return true;
  if (url.pathname.endsWith(".js")) return true;
  if (url.pathname.endsWith(".css")) return true;
  if (url.pathname.endsWith(".webmanifest")) return true;
  return false;
}

// Cache-First: Bilder, Fonts, SVG (unveränderlich, server-seitig 7-Tage-Cache).
function isImmutableAsset(url) {
  if (url.pathname.endsWith(".svg")) return true;
  if (url.pathname.endsWith(".png")) return true;
  if (url.pathname.endsWith(".woff2")) return true;
  if (url.pathname.endsWith(".woff")) return true;
  if (url.pathname.endsWith(".ttf")) return true;
  return false;
}

// API/Streams/Poster/Thumb dürfen NIE durch den SW — Range-Requests, Auth, Live-Daten.
function shouldBypass(url) {
  if (url.pathname.startsWith("/api/")) return true;
  if (url.pathname.startsWith("/stream/")) return true;
  if (url.pathname.startsWith("/transcode/")) return true;
  if (url.pathname.startsWith("/poster/")) return true;
  if (url.pathname.startsWith("/thumb/")) return true;
  return false;
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (shouldBypass(url)) return;

  if (isAppShell(url)) {
    // Network-First: Server hat das letzte Wort, Cache nur als Offline-Notnagel.
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res && res.ok && res.status === 200 && res.type === "basic") {
            const clone = res.clone();
            caches.open(CACHE_NAME).then((c) => c.put(req, clone)).catch(() => {});
          }
          return res;
        })
        .catch(() => caches.match(req).then((c) => c || Response.error()))
    );
    return;
  }

  if (isImmutableAsset(url)) {
    // Cache-First: erst Cache, sonst Netzwerk + nachcachen.
    event.respondWith(
      caches.match(req).then((cached) => {
        if (cached) return cached;
        return fetch(req)
          .then((res) => {
            if (res && res.ok && res.status === 200 && res.type === "basic") {
              const clone = res.clone();
              caches.open(CACHE_NAME).then((c) => c.put(req, clone)).catch(() => {});
            }
            return res;
          })
          .catch(() => Response.error());
      })
    );
  }
});
