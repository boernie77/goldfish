// player-trickplay.js -- Trickplay-Hover-Thumbnails-Plugin, aus player.js
// ausgelagert (Schritt 5 der Modularisierung, siehe CLAUDE.md "Code-Review
// 2026-09-06"). Reine Funktionsverschiebung, keine Logik-/Signaturaenderung.
//
// Reihenfolge in index.html: ... cards -> views -> grid -> player ->
// player-trickplay -> admin -> ...

// --- Trickplay-Hover-Thumbnails (eigenes Plugin) ---
// Parst eine WebVTT-Datei mit Cues wie "sprite.jpg#xywh=X,Y,W,H" und blendet
// beim Hovern über die Video.js-Progress-Bar das passende Sprite-Bild ein.

const trickplayState = new WeakMap(); // vjs-Instanz → { cues, el, spriteUrl, cleanup }

async function attachTrickplayHover(vjs, vttUrl) {
  detachTrickplayHover(vjs);
  let txt;
  try {
    const res = await fetch(vttUrl, { credentials: "same-origin" });
    if (!res.ok) return;
    txt = await res.text();
  } catch (e) { return; }
  const cues = parseThumbVTT(txt);
  if (!cues.length) return;
  const base = vttUrl.replace(/[^/]+$/, "");
  const spriteUrl = base + "sprite.jpg";

  const cb = vjs.getChild("controlBar");
  const pc = cb && cb.getChild("progressControl");
  if (!pc || !pc.el()) return;
  const pcEl = pc.el();

  const preview = document.createElement("div");
  preview.className = "trickplay-preview";
  preview.style.cssText = "position:absolute;bottom:100%;margin-bottom:8px;pointer-events:none;display:none;border:2px solid #fff;border-radius:3px;background:#000 no-repeat;box-shadow:0 2px 12px rgba(0,0,0,0.6);z-index:2;";
  pcEl.appendChild(preview);

  const firstCue = cues[0];
  const tileW = firstCue.w || 160;
  const tileH = firstCue.h || 90;
  preview.style.width = tileW + "px";
  preview.style.height = tileH + "px";
  preview.style.backgroundImage = `url(${spriteUrl})`;

  const onMove = (ev) => {
    const dur = vjs.duration();
    if (!dur || !isFinite(dur)) { preview.style.display = "none"; return; }
    const rect = pcEl.getBoundingClientRect();
    const x = ev.clientX - rect.left;
    if (x < 0 || x > rect.width) { preview.style.display = "none"; return; }
    const t = (x / rect.width) * dur;
    const cue = findCue(cues, t);
    if (!cue) { preview.style.display = "none"; return; }
    preview.style.backgroundPosition = `-${cue.x}px -${cue.y}px`;
    preview.style.display = "block";
    let left = x - tileW / 2;
    if (left < 0) left = 0;
    if (left + tileW > rect.width) left = rect.width - tileW;
    preview.style.left = left + "px";
  };
  const onLeave = () => { preview.style.display = "none"; };

  pcEl.addEventListener("mousemove", onMove);
  pcEl.addEventListener("mouseleave", onLeave);

  trickplayState.set(vjs, {
    cleanup: () => {
      pcEl.removeEventListener("mousemove", onMove);
      pcEl.removeEventListener("mouseleave", onLeave);
      try { preview.remove(); } catch {}
    },
  });
}

function detachTrickplayHover(vjs) {
  if (!vjs) return;
  const s = trickplayState.get(vjs);
  if (s && s.cleanup) s.cleanup();
  trickplayState.delete(vjs);
}

function parseThumbVTT(text) {
  const cues = [];
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/(\d+):(\d+):(\d+)[.,](\d+)\s*-->\s*(\d+):(\d+):(\d+)[.,](\d+)/);
    if (!m) continue;
    const start = (+m[1]) * 3600 + (+m[2]) * 60 + (+m[3]) + (+m[4]) / 1000;
    const end   = (+m[5]) * 3600 + (+m[6]) * 60 + (+m[7]) + (+m[8]) / 1000;
    const payload = (lines[i + 1] || "").trim();
    const xm = payload.match(/#xywh=(\d+),(\d+),(\d+),(\d+)/);
    if (!xm) continue;
    cues.push({ start, end, x: +xm[1], y: +xm[2], w: +xm[3], h: +xm[4] });
  }
  return cues;
}

function findCue(cues, t) {
  // Binärsuche: cues sind sortiert nach start
  let lo = 0, hi = cues.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const c = cues[mid];
    if (t < c.start) hi = mid - 1;
    else if (t >= c.end) lo = mid + 1;
    else return c;
  }
  return cues[Math.max(0, Math.min(cues.length - 1, lo - 1))];
}
