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

// ICON_TRASH_SVG / ICON_FILM_SVG: Feather-Icon-Style-SVGs statt der Emoji
// 🗑/🎞. Grund: beide Codepoints (U+1F5D1, U+1F39E) haben laut Unicode
// Emoji_Presentation=No — Browser routen sie ohne Variation-Selector NICHT
// zuverlässig auf die Farb-Emoji-Schriftart. Selbst MIT VS16 (️) zeigte
// Chrome/macOS in der Praxis weiterhin eine leere Tofu-Box mit Hex-Codepoint
// (Last-Resort-Font) — offenbar ein Blink-internes Fallback-Problem für
// diese speziellen Codepoints, nicht bloß ein Presentation-Property-Thema.
// SVG mit stroke="currentColor" umgeht das komplett und erbt Farbe/Opacity
// von den bestehenden Button-Styles unverändert.
const ICON_TRASH_SVG = '<svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-0.15em" aria-hidden="true"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path><line x1="10" y1="11" x2="10" y2="17"></line><line x1="14" y1="11" x2="14" y2="17"></line></svg>';
const ICON_FILM_SVG = '<svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-0.15em" aria-hidden="true"><rect x="2" y="2" width="20" height="20" rx="2.18" ry="2.18"></rect><line x1="7" y1="2" x2="7" y2="22"></line><line x1="17" y1="2" x2="17" y2="22"></line><line x1="2" y1="12" x2="22" y2="12"></line><line x1="2" y1="7" x2="7" y2="7"></line><line x1="2" y1="17" x2="7" y2="17"></line><line x1="17" y1="17" x2="22" y2="17"></line><line x1="17" y1="7" x2="22" y2="7"></line></svg>';

// escapeHTML: minimal-sichere HTML-Escape fuer Strings, die wir in
// innerHTML interpolieren. Nicht fuer Attribute (dort waeren noch andere
// Codepoints relevant) — fuer unseren Use-Case (Titel/Texte als Body-Text)
// reicht der Standard-Satz.
function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, c => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}
