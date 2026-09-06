// player-transcode-seek.js -- Transcode-Seek (Capture-Handler + Session-
// Restart) + absolute Zeit-/Progress-Anzeige waehrend Transcode-Wiedergabe,
// aus player.js ausgelagert (Schritt 5 der Modularisierung, siehe CLAUDE.md
// "Code-Review 2026-09-06"). Reine Funktionsverschiebung, keine
// Logik-/Signaturaenderung.
//
// Reihenfolge in index.html: ... player -> player-trickplay ->
// player-transcode-seek -> player-buffer -> admin -> ...

// Transcode-Progress: alle 2s den Server fragen, bis ffmpeg fertig ist oder
// der Player geschlossen wird. Zeigt "+N s" Puffer-Abstand.
// Hält Zeit-/Progress-Anzeige absolut, obwohl die HLS-Session tech-seitig nach
// Seek-Restart bei 0 beginnt. Läuft auf requestAnimationFrame, damit Video.js'
// eigene SeekBar-Updates uns nicht ständig überschreiben.
function syncTranscodeDisplays(vjs) {
  if (!vjs || !vjs.el) return;
  // Re-Entry-Schutz: bei Player-Reuse (Shuffle-Next, restartTranscodeAt) ruft
  // applyPlayback erneut auf — früher startete jedes Mal ein WEITERER RAF-
  // Loop, alte liefen parallel weiter. Symptom: konkatenierte Zeit-Strings
  // („6:500:000:010:020:031:39…") oder verschwundene Status-Leiste, weil
  // mehrere Loops in dieselben (oder duplizierten) Display-Spans schreiben.
  if (vjs._transcodeDisplaysActive) return;
  vjs._transcodeDisplaysActive = true;
  const root = vjs.el();
  // Video.js' eigene TimeDisplay-Updates ausschalten, sobald wir im Transcode-
  // Modus sind — sonst schreiben Video.js und unser RAF-Loop abwechselnd Text
  // in die Elemente → Flackern. Original-Methode bleibt erhalten für Direct Play.
  const cb = vjs.getChild("controlBar");
  if (cb) {
    for (const name of ["currentTimeDisplay", "durationDisplay", "remainingTimeDisplay"]) {
      const c = cb.getChild(name);
      if (!c || typeof c.updateContent !== "function" || c._patchedForTranscode) continue;
      const orig = c.updateContent.bind(c);
      c.updateContent = (ev) => {
        if (state.playback && state.playback.mode === "transcode") return; // wir übernehmen
        return orig(ev);
      };
      c._patchedForTranscode = true;
    }
    // Genauso die SeekBar: Video.js aktualisiert die `.vjs-play-progress`-
    // Breite selbst (timeupdate + eigener RAF), rechnet dabei aber gegen die
    // wachsende EVENT-Playlist-/Live-Dauer statt der forcierten Filmlaenge.
    // Parallel setzt unser RAF-Loop unten dieselbe Breite auf den korrekten
    // (absoluten) Wert → die beiden schreiben abwechselnd unterschiedliche
    // Positionen → Fortschrittsbalken flackert waehrend der Wiedergabe. Im
    // Transcode-Modus deshalb Video.js' SeekBar-Update aussetzen, unser RAF
    // uebernimmt; Direct Play laeuft unveraendert ueber Video.js.
    const pc = cb.getChild("progressControl");
    const sb = pc && pc.getChild("seekBar");
    if (sb && typeof sb.update === "function" && !sb._patchedForTranscode) {
      const origUpdate = sb.update.bind(sb);
      sb.update = () => {
        if (state.playback && state.playback.mode === "transcode") return;
        return origUpdate();
      };
      sb._patchedForTranscode = true;
    }
  }
  // Display-Span-Update OHNE textContent — sonst loggt Video.js
  // „TimeDisplay#updateTextnode_: Prevented replacement of text node element"
  // und akkumuliert TextNodes nebeneinander („6:500:000:010:020:031:39…").
  // Grund: Video.js' TimeDisplay hält eine interne Referenz auf den ersten
  // Text-Node. textContent ersetzt alle Children → Referenz wird ungültig →
  // beim nächsten Internal-Update macht Video.js appendChild statt
  // replaceChild. Workaround: ersten TextNode behalten, dessen nodeValue
  // setzen; alle weiteren TextNodes entfernen.
  const setText = (el, text) => {
    let first = null;
    for (let i = el.childNodes.length - 1; i >= 0; i--) {
      const c = el.childNodes[i];
      if (c.nodeType !== Node.TEXT_NODE) continue;
      if (first) el.removeChild(c);
      else first = c;
    }
    if (first) {
      if (first.nodeValue !== text) first.nodeValue = text;
    } else {
      el.appendChild(document.createTextNode(text));
    }
  };
  const setAll = (sel, text) => {
    const list = root.querySelectorAll(sel);
    for (let i = 0; i < list.length; i++) setText(list[i], text);
  };
  let rafId = 0;
  const tick = () => {
    if (!state.vjs || state.vjs !== vjs || (typeof vjs.isDisposed === "function" && vjs.isDisposed())) {
      vjs._transcodeDisplaysActive = false;
      return; // Loop beendet sich selbst
    }
    if (state.playback && state.playback.mode === "transcode") {
      const offset = state.playback.virtualOffset || 0;
      const total = (state.currentItem && state.currentItem.durationSec) || 0;
      if (total > 0) {
        const absCur = Math.max(0, vjs.currentTime() + offset);
        setAll(".vjs-current-time-display", formatPlayerTime(absCur));
        setAll(".vjs-duration-display", formatPlayerTime(total));
        setAll(".vjs-remaining-time-display", "-" + formatPlayerTime(Math.max(0, total - absCur)));
        const progs = root.querySelectorAll(".vjs-play-progress");
        for (let i = 0; i < progs.length; i++) {
          progs[i].style.width = Math.min(100, (absCur / total) * 100) + "%";
          const tt = progs[i].querySelector(".vjs-time-tooltip");
          if (tt) tt.textContent = formatPlayerTime(absCur);
        }
        const seekable = vjs.seekable();
        const seekableEnd = seekable && seekable.length ? seekable.end(0) : 0;
        const absLoad = seekableEnd + offset;
        const loads = root.querySelectorAll(".vjs-load-progress");
        for (let i = 0; i < loads.length; i++) {
          loads[i].style.width = Math.min(100, (absLoad / total) * 100) + "%";
        }
      }
    }
    rafId = requestAnimationFrame(tick);
  };
  rafId = requestAnimationFrame(tick);
  vjs.on("dispose", () => {
    if (rafId) cancelAnimationFrame(rafId);
    vjs._transcodeDisplaysActive = false;
  });
}

function formatPlayerTime(sec) {
  sec = Math.floor(sec || 0);
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  const pad = (n) => (n < 10 ? "0" + n : "" + n);
  if (h > 0) return `${h}:${pad(m)}:${pad(s)}`;
  return `${m}:${pad(s)}`;
}

// Seek-Restart für Transcode: wenn der User in der Progress-Bar jenseits des
// bereits transkodierten Bereichs klickt, würde Video.js den Ziel-Wert an den
// seekable-Range clampen ("nur ein paar Sekunden vorwärts"). Stattdessen fangen
// wir den Klick ab (Capture-Phase am progressControl) und starten die
// Transcode-Session an der gewünschten Position neu. Klicks innerhalb des
// transkodierten Bereichs leiten wir mit Offset-Korrektur an vjs.currentTime weiter.
function attachSeekRestart(vjs) {
  const cb = vjs.getChild("controlBar");
  const pc = cb && cb.getChild("progressControl");
  if (!pc || !pc.el) return;
  const pcEl = pc.el();
  const handler = (ev) => {
    if (!state.playback || state.playback.mode !== "transcode") return;
    const holder = pcEl.querySelector(".vjs-progress-holder") || pcEl;
    const rect = holder.getBoundingClientRect();
    if (rect.width <= 0) return;
    const cx = ev.clientX !== undefined
      ? ev.clientX
      : (ev.touches && ev.touches[0] ? ev.touches[0].clientX : undefined);
    if (cx === undefined) return;
    if (cx < rect.left || cx > rect.right) return;
    const x = Math.max(0, Math.min(rect.width, cx - rect.left));
    const ratio = x / rect.width;
    const total = (state.currentItem && state.currentItem.durationSec) || 0;
    if (total <= 0) return;
    // Ziel = absolute Zeit in der Original-Datei (Progress-Bar zeigt 0..total).
    const absoluteTarget = ratio * total;
    const offset = state.playback.virtualOffset || 0;
    const seekable = vjs.seekable();
    const seekableEnd = seekable && seekable.length ? seekable.end(0) : 0;
    // Absolutes Ende des bereits Transkodierten
    const absSeekableEnd = seekableEnd + offset;
    ev.stopImmediatePropagation();
    ev.preventDefault();
    if (absoluteTarget >= offset && absoluteTarget <= absSeekableEnd + 3) {
      // Ziel liegt in der aktuellen Session → tech-seitig mit Offset seeken
      try { vjs.currentTime(absoluteTarget - offset); } catch {}
    } else {
      // Außerhalb → neue Session am Ziel starten
      restartTranscodeAt(absoluteTarget);
    }
  };
  pcEl.addEventListener("mousedown", handler, true);
  pcEl.addEventListener("pointerdown", handler, true);
  pcEl.addEventListener("touchstart", handler, true);
}

async function restartTranscodeAt(absoluteStart) {
  const item = state.currentItem;
  const vjs = state.vjs;
  if (!item || !vjs) return;
  const info = state.playback;
  const profile = info.profile || "orig";
  const audioIdx = info.audioIdx;
  const params = new URLSearchParams({
    profile,
    start: String(Math.floor(absoluteStart)),
  });
  if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
    params.set("audio", String(audioIdx));
  }
  const newUrl = `/api/transcode/${item.id}/index.m3u8?${params}`;
  // State aktualisieren: virtualOffset für Progress-Anzeige + Display-Berechnung.
  state.playback.url = newUrl;
  state.playback.virtualOffset = absoluteStart;
  // Forced Duration bleibt total; Progress-Bar wird per syncTranscodeDisplays
  // manuell auf die absolute Position gesetzt.
  forcePlayerDuration(vjs, item.durationSec || 0);
  // Buffer-Overlay mit neuer Session neu starten (Progress-Polling mit neuem start)
  stopTranscodeProgress();
  // Alter Prefetch zielt noch auf die alte Session-URL (anderer start=) — verwerfen.
  stopPausePrefetch();
  // Neue Source laden
  vjs.src({ src: newUrl, type: "application/vnd.apple.mpegurl" });
  vjs.one("loadedmetadata", () => {
    vjs.play().catch(() => {});
    // Buffer-Anzeige neu starten (Transcode-Progress liest die neue Session)
    startBufferDisplayWithStart(item, "transcode", profile, audioIdx, Math.floor(absoluteStart));
  });
}
