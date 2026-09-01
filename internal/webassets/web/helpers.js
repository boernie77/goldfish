// helpers.js — kleine, reine Hilfsfunktionen ohne App-State-Abhaengigkeit.
//
// Wird VOR app.js geladen, damit die Funktionen im window-Scope verfuegbar
// sind, bevor app.js sie nutzt. Reihenfolge in index.html: helpers.js →
// dialogs.js → app.js.

// normalizeModalLayout + showModal-Patch (2026-09-02): "Kopf/Fuß immer
// sichtbar" war ursprünglich per CSS position:sticky gelöst (siehe .modal-
// close/.modal h2/.modal .row-Regeln in style.css) — funktioniert in
// Chrome/Safari, aber `position: sticky` innerhalb eines <dialog>-Elements
// ist ein bekannter Firefox-Bug (der Sticky-Container "sieht" den <dialog>-
// eigenen Scroll nicht zuverlässig, Kopf/Fuß bleiben einfach im normalen
// Fluss und scrollen weg — User-Report 2026-09-02, reproduzierbar in
// Firefox, in Chrome nicht reproduzierbar).
// Fix OHNE sticky: verwandelt einen erkannten .modal-Dialog EINMALIG (beim
// ersten showModal()) in echtes Flex-Layout — Kopf und Fuß werden aus dem
// scrollenden Bereich HERAUSGELÖST in eigene, nicht scrollende Flex-Items,
// dazwischen liegt ein <div class="modal-scroll"> mit overflow-y:auto. Das
// ist plain CSS-Flexbox + normales overflow, keine Sticky-Positionierung
// nötig — funktioniert dadurch identisch in jedem Browser.
// Erkennung rein strukturell, damit KEIN einzelnes Dialog-Markup angefasst
// werden muss:
//   Kopf  = optionales führendes <button class="modal-close"> + folgendes <h2>
//   Fuß   = letztes Kind mit .row/.modal-actions — ODER, wenn das letzte
//           Kind ein <form> ist, dessen letztes Kind mit .row/.modal-actions
//           (die Buttons bekommen dabei form="<id>", damit type=submit
//           weiter dasselbe <form> submitted, obwohl sie optisch als Fuß
//           AUSSERHALB des <form>-Elements landen)
// Dialoge ohne erkennbaren Kopf (kein <h2> als erstes/zweites Element)
// werden übersprungen und behalten das alte sticky-Verhalten als Fallback
// (betrifft z. B. detailDialog mit seiner Sonderstruktur).
(function () {
  function normalizeModalLayout(dlg) {
    if (!dlg.classList || !dlg.classList.contains("modal") || dlg.classList.contains("app-dialog")) return;
    if (dlg.dataset.modalFlex) return;
    const kids = Array.from(dlg.children);
    if (kids.length < 2) return;

    let i = 0;
    if (kids[i] && kids[i].classList.contains("modal-close")) i++;
    if (!(kids[i] && kids[i].tagName === "H2")) return; // kein erkennbarer Kopf → unangetastet lassen
    i++;
    const headerKids = kids.slice(0, i);
    let rest = kids.slice(i);
    if (!rest.length) return;

    let footerEl = null, footerForm = null;
    const last = rest[rest.length - 1];
    if (last.classList && (last.classList.contains("row") || last.classList.contains("modal-actions"))) {
      footerEl = last;
      rest = rest.slice(0, -1);
    } else if (last.tagName === "FORM") {
      const fk = Array.from(last.children);
      const flast = fk[fk.length - 1];
      if (flast && flast.classList && (flast.classList.contains("row") || flast.classList.contains("modal-actions"))) {
        footerEl = flast;
        footerForm = last; // bleibt vorerst in `rest`, footerEl wird gleich rausgelöst
      }
    }

    const headerDiv = document.createElement("div");
    headerDiv.className = "modal-header-flex";
    for (const el of headerKids) headerDiv.appendChild(el);

    const scrollDiv = document.createElement("div");
    scrollDiv.className = "modal-scroll";
    for (const el of rest) scrollDiv.appendChild(el);

    dlg.appendChild(headerDiv);
    dlg.appendChild(scrollDiv);

    if (footerEl) {
      if (footerForm) {
        if (!footerForm.id) footerForm.id = "modalForm_" + Math.random().toString(36).slice(2, 9);
        Array.from(footerEl.querySelectorAll("button[type=submit]"))
          .forEach(b => b.setAttribute("form", footerForm.id));
      }
      dlg.appendChild(footerEl); // holt footerEl aus scrollDiv/footerForm heraus
    }

    dlg.classList.add("modal-flex");
    dlg.dataset.modalFlex = "1";
  }
  window.normalizeModalLayout = normalizeModalLayout;

  const nativeShowModal = HTMLDialogElement.prototype.showModal;
  HTMLDialogElement.prototype.showModal = function (...args) {
    try { normalizeModalLayout(this); } catch (e) { /* Fallback: altes sticky-CSS greift weiter */ }
    return nativeShowModal.apply(this, args);
  };
})();

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
