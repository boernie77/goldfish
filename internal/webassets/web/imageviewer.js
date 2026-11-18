// imageviewer.js -- Bilder in Bibliotheken mit "🖼 Bilder"-Schalter (images_enabled):
// Kachel im Grid + Vollbild-Betrachter (Pfeiltasten, Wischen, Diashow).
// Läuft über den gemeinsamen window-Scope (kein ES-Modul), siehe goldfish-web-ui.

function renderImageCard(im, idx, list) {
  const el = document.createElement("article");
  el.className = "card image-card";
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  el.dataset.imageId = im.id;
  const dim = im.width && im.height ? `${im.width}×${im.height}` : "";
  el.innerHTML = `
    <div class="thumb">
      <img class="thumb-img" loading="lazy" decoding="async" alt="" src="/api/images/${im.id}/thumb">
    </div>
    <div class="card-body">
      <div class="card-title" title="${escapeHTML(im.title)}">${escapeHTML(im.title)}</div>
      <div class="card-meta"><span>🖼 ${escapeHTML(dim)}</span></div>
    </div>`;
  const open = () => openImageViewer(list, idx);
  el.addEventListener("click", open);
  el.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  return el;
}

const imageViewerState = { list: [], idx: 0, timer: null, el: null, touchX: null };

function ensureImageViewer() {
  if (imageViewerState.el) return imageViewerState.el;
  const el = document.createElement("div");
  el.id = "imageViewer";
  el.className = "image-viewer hidden";
  el.innerHTML = `
    <div class="iv-top">
      <span class="iv-count"></span>
      <span class="iv-title"></span>
      <button type="button" class="iv-btn iv-play" title="Diashow (Leertaste)">▶</button>
      <a class="iv-btn iv-orig" target="_blank" rel="noopener" title="Original öffnen">⤢</a>
      <button type="button" class="iv-btn iv-close" title="Schließen (Esc)">✕</button>
    </div>
    <button type="button" class="iv-nav iv-prev" title="Zurück (←)">‹</button>
    <img class="iv-img" alt="">
    <button type="button" class="iv-nav iv-next" title="Weiter (→)">›</button>`;
  document.body.appendChild(el);
  el.querySelector(".iv-close").addEventListener("click", closeImageViewer);
  el.querySelector(".iv-prev").addEventListener("click", () => stepImageViewer(-1));
  el.querySelector(".iv-next").addEventListener("click", () => stepImageViewer(1));
  el.querySelector(".iv-play").addEventListener("click", toggleImageSlideshow);
  // Klick auf den leeren Hintergrund schließt, Klick auf das Bild nicht.
  el.addEventListener("click", (e) => { if (e.target === el) closeImageViewer(); });
  el.addEventListener("touchstart", (e) => { imageViewerState.touchX = e.touches[0].clientX; }, { passive: true });
  el.addEventListener("touchend", (e) => {
    const x0 = imageViewerState.touchX;
    imageViewerState.touchX = null;
    if (x0 == null) return;
    const dx = e.changedTouches[0].clientX - x0;
    if (Math.abs(dx) > 50) stepImageViewer(dx < 0 ? 1 : -1);
  }, { passive: true });
  imageViewerState.el = el;
  return el;
}

function imageViewerIsOpen() {
  return !!(imageViewerState.el && !imageViewerState.el.classList.contains("hidden"));
}

function showImageViewerAt(i) {
  const s = imageViewerState;
  if (!s.list.length) return;
  s.idx = (i + s.list.length) % s.list.length;
  const im = s.list[s.idx];
  const el = s.el;
  el.querySelector(".iv-img").src = `/api/images/${im.id}/file`;
  el.querySelector(".iv-count").textContent = `${s.idx + 1} / ${s.list.length}`;
  el.querySelector(".iv-title").textContent = im.title;
  el.querySelector(".iv-orig").href = `/api/images/${im.id}/file`;
  // Nachbarn vorladen, damit das Blättern ohne Wartezeit klappt.
  for (const d of [1, -1]) {
    const n = s.list[(s.idx + d + s.list.length) % s.list.length];
    if (n && n.id !== im.id) new Image().src = `/api/images/${n.id}/file`;
  }
}

function stepImageViewer(d) { showImageViewerAt(imageViewerState.idx + d); }

function toggleImageSlideshow() {
  const s = imageViewerState;
  const btn = s.el.querySelector(".iv-play");
  if (s.timer) {
    clearInterval(s.timer);
    s.timer = null;
    btn.textContent = "▶";
  } else {
    s.timer = setInterval(() => stepImageViewer(1), 4000);
    btn.textContent = "⏸";
  }
}

function openImageViewer(list, idx) {
  const s = imageViewerState;
  s.list = list || [];
  ensureImageViewer().classList.remove("hidden");
  document.body.classList.add("iv-open");
  showImageViewerAt(idx || 0);
}

function closeImageViewer() {
  const s = imageViewerState;
  if (s.timer) { clearInterval(s.timer); s.timer = null; }
  if (!s.el) return;
  s.el.querySelector(".iv-play").textContent = "▶";
  s.el.querySelector(".iv-img").removeAttribute("src");
  s.el.classList.add("hidden");
  document.body.classList.remove("iv-open");
}

document.addEventListener("keydown", (e) => {
  if (!imageViewerIsOpen()) return;
  if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeImageViewer(); }
  else if (e.key === "ArrowRight") { e.preventDefault(); stepImageViewer(1); }
  else if (e.key === "ArrowLeft") { e.preventDefault(); stepImageViewer(-1); }
  else if (e.key === " ") { e.preventDefault(); toggleImageSlideshow(); }
}, true);
