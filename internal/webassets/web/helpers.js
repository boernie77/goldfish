// helpers.js — kleine, reine Hilfsfunktionen ohne App-State-Abhaengigkeit.
//
// Wird VOR app.js geladen, damit die Funktionen im window-Scope verfuegbar
// sind, bevor app.js sie nutzt. Reihenfolge in index.html: helpers.js →
// dialogs.js → app.js.

// Formatiert Sekunden als "M:SS" oder "H:MM:SS".
function fmtDuration(sec) {
  sec = Math.round(sec || 0);
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

// Formatiert Byte-Anzahl als "1.5 GB" o.ä.
function fmtSize(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, n = bytes || 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 ? 1 : 0)} ${units[i]}`;
}

// Formatiert ISO-Date als deutsches Tagesdatum.
// `isNaN(date)` ist hier intentional — coerced Date zu NaN bei „Invalid Date";
// Number.isNaN waere streng und wuerde immer false liefern.
function fmtDate(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleDateString("de-DE", { year: "numeric", month: "2-digit", day: "2-digit" });
}

// resLabel: Aufloesungs-Bucket fuer ein Item (4K/2K/1080p/720p/…).
// Cinemascope-Filme (2.40:1) sind bei 1080p-Quelle nur ~800 Pixel hoch,
// aber volle 1920 breit. Daher: max(height, width*9/16) als effektive Hoehe —
// damit landen 1920x800-Filme im 1080p-Bucket.
function resLabel(it) {
  if (!it) return "";
  const w = it.width || 0;
  const h = it.height || 0;
  const effH = Math.max(h, Math.round(w * 9 / 16));
  if (effH >= 2000) return "4K";
  if (effH >= 1400) return "2K";
  if (effH >= 1000) return "1080p";
  if (effH >= 700) return "720p";
  if (effH >= 540) return "576p";
  if (effH >= 440) return "480p";
  if (effH >= 300) return "360p";
  if (effH > 0) return effH + "p";
  return "";
}

// escapeHTML: minimal-sichere HTML-Escape fuer Strings, die wir in
// innerHTML interpolieren. Nicht fuer Attribute (dort waeren noch andere
// Codepoints relevant) — fuer unseren Use-Case (Titel/Texte als Body-Text)
// reicht der Standard-Satz.
function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, c => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}
