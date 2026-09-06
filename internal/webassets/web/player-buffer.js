// player-buffer.js -- Startpuffer-Gate, Buffer-Overlay-Anzeige und
// Pause-Prefetch fuer Transcode-Wiedergabe, aus player.js ausgelagert
// (Schritt 5 der Modularisierung, siehe CLAUDE.md "Code-Review 2026-09-06").
// Reine Funktionsverschiebung, keine Logik-/Signaturaenderung.
//
// Reihenfolge in index.html: ... player-transcode-seek -> player-buffer ->
// admin -> ...

// Variante, die beim Progress-Endpoint explizit `start=` mitgibt.
function startBufferDisplayWithStart(item, mode, profile, audioIdx, startSec) {
  const orig = state.playback;
  // temporärer Override: Start-Param in die URL des Progress-Endpoints einfließen
  // lassen. Wir wrappen startBufferDisplay indem wir den URL-Builder dort ersetzen.
  state.__transcodeStartOverride = startSec;
  startBufferDisplay(item, mode, profile, audioIdx);
  void orig;
}

// applyStartBufferGate pausiert den Player zu Beginn, bis mindestens
// `startBufferSeconds` Sekunden vorgeladen sind. Zeigt währenddessen ein
// Overlay mit Fortschritt + Skip-Button.
//
// Wichtig für HLS-Transcode: Video.js/VHS lädt bei einer EVENT-Playlist
// Segmente nahe `currentTime`. Ohne expliziten Seek auf 0 würde VHS
// irgendwo in der Playlist puffern (oder gar nicht), und buffered() am
// currentTime wäre leer. Darum forcen wir currentTime(0) beim Start
// UND beim Release — sonst springt der Player an die Live-Kante und
// der Film beginnt mittendrin.
function applyStartBufferGate(vjs) {
  if (!vjs) return;
  clearStartBufferGate();
  const target = Number(state.settings && state.settings.startBufferSeconds) || 0;
  if (target <= 0) return;

  const overlay = $("#prebufferOverlay");
  const bar = overlay && overlay.querySelector(".prebuffer-bar");
  const text = overlay && overlay.querySelector(".prebuffer-text");
  const skip = overlay && overlay.querySelector("#prebufferSkip");

  const wantedPos = (state.playback && state.playback.startWantedSec) || 0;
  const isTranscode = state.playback && state.playback.mode === "transcode";
  console.log("[gate] start — mode=%s wantedPos=%s target=%s", state.playback && state.playback.mode, wantedPos, target);

  // Progress-URL für Transcode: ffmpeg-Position ist DER zuverlässige Puffer-
  // Indikator, nicht der Client-Buffer. VHS bufffert im Pause-Zustand absichtlich
  // nur ein Segment ahead — mehr liefert GOAL_BUFFER_LENGTH nicht, solange
  // nicht gespielt wird. Wir messen stattdessen, wie weit der Server bereits
  // transcodiert hat.
  const progressURL = () => {
    const p = new URLSearchParams({ profile: (state.playback && state.playback.profile) || "orig" });
    const aIdx = state.playback && state.playback.audioIdx;
    if (typeof aIdx === "number" && aIdx >= 0) p.set("audio", String(aIdx));
    if (state.playback && state.playback.virtualOffset) {
      p.set("start", String(Math.floor(state.playback.virtualOffset)));
    }
    return `/api/transcode/${state.currentItem && state.currentItem.id}/progress?${p}`;
  };

  let gateActive = true;
  let releasing = false;

  const seekToWanted = () => {
    try {
      if (Math.abs((vjs.currentTime() || 0) - wantedPos) > 0.5) {
        vjs.currentTime(wantedPos);
      }
    } catch {}
  };

  const release = (reason) => {
    if (!gateActive) return;
    gateActive = false;
    releasing = true;
    vjs.off("play", onPlayGate);
    clearStartBufferGate();
    if (overlay) overlay.classList.add("hidden");
    // VOR dem play() einmal auf Soll-Position snappen — dann play().
    seekToWanted();
    try {
      const pp = vjs.play();
      if (pp && typeof pp.catch === "function") pp.catch(() => {});
    } catch {}
    if (reason) showToast(`Start: ${reason}`, { kind: "success", duration: 1500 });
  };

  // Repeat-Pause-Listener — wird erst AFTER dem Initial-Kick-off aktiviert,
  // verhindert unerwünschtes Weiterspielen (z.B. durch Video.js-Autoplay).
  // `onSeeked`-Schleife wurde entfernt: die hat VHS' internen Segment-Loader
  // nach einem Segment blockiert (Seek-Loop). Wir seeken nur EINMAL beim
  // Pausieren und verlassen uns darauf, dass VHS die Goal-Buffer-Logik
  // alleine abarbeitet.
  const onPlayGate = () => {
    if (!gateActive || releasing) return;
    try { vjs.pause(); } catch {}
  };

  // VHS lädt im Pause-Zustand KEINE Segmente — Goal-Buffer greift erst nach
  // dem ersten Segment-Append. Darum: anspielen bis der erste Frame da ist,
  // dann pausieren und zurück auf wantedPos. Wir warten auf `canplay` /
  // `progress` statt auf ein festes Timeout — bei einer frisch gestarteten
  // ffmpeg-Session dauert es oft mehrere Sekunden, bis das erste Segment
  // verfügbar ist.
  if (overlay) {
    overlay.classList.remove("hidden");
    if (bar) bar.style.width = "0%";
    if (text) text.textContent = `0 / ${target} s`;
  }
  if (skip) skip.onclick = () => release("manuell gestartet");

  // Effektives Ziel auf den Haupt-Buffer cappen — VHS lädt nicht über
  // GOAL_BUFFER_LENGTH hinaus, also wäre ein höheres Ziel nie erreichbar.
  const goalBuffer = Number(state.settings && state.settings.bufferSeconds) || 30;
  const cappedTarget = Math.min(target, Math.max(5, goalBuffer - 2));

  let kicked = false;
  const startPolling = () => {
    if (!gateActive || kicked) return;
    kicked = true;
    vjs.off("canplay", onFirstReady);
    vjs.off("progress", onFirstReady);
    if (readyTimer) clearTimeout(readyTimer);
    try { vjs.pause(); } catch {}
    // Einmalig auf wantedPos seeken. Danach NICHT mehr aggressiv snappen —
    // sonst blockieren die seeked-Events VHS' Segment-Loader (Symptom:
    // Buffer bleibt bei ~5 s = genau ein Segment hängen).
    seekToWanted();
    vjs.on("play", onPlayGate);
    console.log("[gate] kicked off, pipeline aktiv — polling startet");
  };
  const onFirstReady = () => {
    const br = vjs.buffered();
    // Nur akzeptieren wenn echter Buffer vorhanden ist (nicht [0,0])
    if (br.length > 0 && br.end(br.length - 1) > 0.5) startPolling();
  };
  vjs.on("canplay", onFirstReady);
  vjs.on("progress", onFirstReady);
  // Safety-Fallback: nach 5 s sicher pausieren, auch wenn kein Event kam.
  const readyTimer = setTimeout(startPolling, 5000);

  // Kick-off: Play triggert VHS-Segment-Loader.
  try {
    const pp = vjs.play();
    if (pp && typeof pp.catch === "function") pp.catch((err) => {
      console.warn("[gate] play() abgelehnt:", err && err.message);
      // Falls Autoplay geblockt ist: kein Kick-off möglich. Polling starten
      // und hoffen dass der User manuell „Jetzt starten" drückt.
      startPolling();
    });
  } catch (e) {
    console.warn("[gate] play() throw:", e);
    startPolling();
  }

  // Hilfsfunktion: beste „Buffer ahead"-Schätzung. Primär die Range, die
  // wantedPos enthält. Falls keine: größte Range im Stream (Fallback — kommt
  // vor wenn VHS noch an der Live-Edge lädt statt am Anfang).
  const computeAhead = () => {
    const br = vjs.buffered();
    let atWanted = 0;
    let anywhere = 0;
    for (let i = 0; i < br.length; i++) {
      const s = br.start(i), e = br.end(i);
      if (s <= wantedPos + 0.5 && e >= wantedPos) {
        atWanted = Math.max(atWanted, e - wantedPos);
      }
      anywhere = Math.max(anywhere, e - s);
    }
    return { atWanted, anywhere };
  };

  // Für Transcode: Server-Progress ist die Gate-Metrik. VHS pre-bufffert
  // im Pause-Zustand nur 1 Segment (~5 s) — also messen wir stattdessen,
  // wie weit ffmpeg-seitig bereits transcodiert wurde. Wenn Server +60 s
  // produziert hat, ist smooth Playback gesichert; Client lädt dann beim
  // Play nach Bedarf.
  // Für Direct Play: Client-buffered() wächst während Pause normal
  // (progressives mp4), darum bleibt's hier beim Browser-Buffer.
  let tickCount = 0;
  let serverAhead = 0;
  let lastServerFetch = 0;

  state.startBufferTimer = setInterval(async () => {
    if (!gateActive) return;
    if (!state.vjs || (state.vjs.isDisposed && state.vjs.isDisposed())) {
      gateActive = false;
      clearStartBufferGate();
      return;
    }
    const dur = vjs.duration() || 0;
    const effective = dur > 0 && dur / 2 < cappedTarget
      ? Math.max(2, Math.floor(dur / 2))
      : cappedTarget;

    let atWanted = 0;
    let anywhere = 0;

    if (isTranscode) {
      // Server-Progress-Fetch alle 800 ms (Poll-Loop selbst läuft 400 ms).
      const now = Date.now();
      if (now - lastServerFetch > 800) {
        lastServerFetch = now;
        try {
          const r = await api(progressURL());
          const pos = Number(r.positionSec) || 0;
          const start = Number(r.startSec) || 0;
          serverAhead = Math.max(0, pos - start);
        } catch {}
      }
      atWanted = serverAhead;
    } else {
      const a = computeAhead();
      atWanted = a.atWanted;
      anywhere = a.anywhere;
    }

    const display = atWanted;

    if (tickCount++ % 2 === 0) {
      console.log("[gate] tick mode=%s server=%s atWanted=%s target=%s paused=%s",
        isTranscode ? "transcode" : "direct",
        serverAhead.toFixed(1), atWanted.toFixed(1), effective, vjs.paused());
    }

    if (bar) bar.style.width = `${Math.min(100, (display / effective) * 100)}%`;
    if (text) text.textContent = `${Math.round(display)} / ${effective} s`;

    if (atWanted >= effective) {
      release(`${Math.round(atWanted)} s gepuffert`);
    }
  }, 400);
}

function clearStartBufferGate() {
  if (state.startBufferTimer) {
    clearInterval(state.startBufferTimer);
    state.startBufferTimer = null;
  }
  const overlay = $("#prebufferOverlay");
  if (overlay) overlay.classList.add("hidden");
}

// --- Pause-Prefetch (Transcode) ---
//
// Video.js/VHS laedt im Pause-Zustand bewusst nur ~1 Segment voraus (siehe
// Kommentar in applyStartBufferGate). Der ffmpeg-Prozess auf dem
// Server transkodiert aber unabhaengig vom Player-Zustand weiter — das
// "Server +Ns"-Overlay zeigt genau diesen Vorsprung an. Waehrend der Pause
// pollen wir die m3u8-Playlist und laden neu erschienene Segmente per
// eigenem fetch() in den Browser-HTTP-Cache (die Segment-Route liefert dafuer
// `Cache-Control: max-age=300`, siehe stream.go). VHS bedient sich beim
// Weiterspielen daraus, ohne erneut ueber eine ggf. langsame Leitung zu muessen.
let pausePrefetchTimer = null;
let pausePrefetchSeen = null;

function stopPausePrefetch() {
  if (pausePrefetchTimer) {
    clearInterval(pausePrefetchTimer);
    pausePrefetchTimer = null;
  }
  pausePrefetchSeen = null;
}

function startPausePrefetch(vjs) {
  stopPausePrefetch();
  if (!state.playback || state.playback.mode !== "transcode") return;
  const item = state.currentItem;
  if (!item) return;
  pausePrefetchSeen = new Set();
  const params = new URLSearchParams({ profile: state.playback.profile || "orig" });
  const aIdx = state.playback.audioIdx;
  if (aIdx !== undefined && aIdx !== null && aIdx >= 0) params.set("audio", String(aIdx));
  const off = state.playback.virtualOffset;
  if (off && off > 0) params.set("start", String(Math.floor(off)));
  const dei = state.playback.deinterlace;
  if (dei && dei !== "auto") params.set("deinterlace", dei);
  const playlistUrl = `/api/transcode/${item.id}/index.m3u8?${params}`;

  const tick = async () => {
    // Sicherheitsnetz: Player kann zwischen Timer-Ticks weitergespielt oder
    // geschlossen worden sein, ohne dass stopPausePrefetch() zwischenzeitlich
    // gegriffen hat (z.B. Source-Wechsel ohne "play"-Event).
    if (!state.vjs || state.vjs !== vjs || !vjs.paused()) { stopPausePrefetch(); return; }
    let text;
    try {
      const res = await fetch(playlistUrl, { credentials: "same-origin" });
      if (!res.ok) return;
      text = await res.text();
    } catch { return; }
    const segUrls = text.split("\n")
      .map(l => l.trim())
      .filter(l => l && !l.startsWith("#"));
    for (const rel of segUrls) {
      if (!pausePrefetchSeen || !vjs.paused()) return;
      let abs;
      try { abs = new URL(rel, location.href).toString(); } catch { continue; }
      if (pausePrefetchSeen.has(abs)) continue;
      pausePrefetchSeen.add(abs);
      try { await fetch(abs, { credentials: "same-origin" }); } catch { /* still */ }
    }
  };
  tick();
  pausePrefetchTimer = setInterval(tick, 4000);
}

function startBufferDisplay(item, mode, profile, audioIdx) {
  stopTranscodeProgress();
  const el = $("#transcodeAhead");
  if (!el) return;
  el.classList.remove("hidden");
  const title = el.querySelector(".ta-title");
  const stats = el.querySelector(".ta-stats");
  if (title) title.textContent = item.title || (item.relPath || "").split("/").pop();
  if (stats) stats.textContent = "…";
  const isTranscode = mode === "transcode";
  let url = null;
  if (isTranscode) {
    // Progress-URL muss EXAKT denselben Session-Key liefern wie die laufende
    // Playback-Session: profile + audio + start + deinterlace. Ohne start
    // sucht der Server bei start=0 — bei Resume- oder Seek-Restart matcht
    // das nicht und `StartOrGet` spawnt eine zweite ffmpeg-Instanz parallel,
    // die mit der eigentlichen Wiedergabe um die iGPU konkurriert. Daher
    // hier ALLE relevanten Parameter an die URL haengen.
    const params = new URLSearchParams({ profile: profile || "orig" });
    if (audioIdx !== undefined && audioIdx !== null && audioIdx >= 0) {
      params.set("audio", String(audioIdx));
    }
    const off = state.playback && state.playback.virtualOffset;
    if (off && off > 0) params.set("start", String(Math.floor(off)));
    const dei = state.playback && state.playback.deinterlace;
    if (dei && dei !== "auto") params.set("deinterlace", dei);
    url = `/api/transcode/${item.id}/progress?${params}`;
  }
  const clientBuffer = () => {
    if (!state.vjs) return 0;
    const cur = state.vjs.currentTime();
    const br = state.vjs.buffered();
    for (let i = 0; i < br.length; i++) {
      if (br.start(i) <= cur + 0.1 && br.end(i) >= cur) {
        return Math.max(0, br.end(i) - cur);
      }
    }
    return 0;
  };
  // Aktuelle Render-Auflösung des Video-Elements (beim Transcode = Output
  // von ffmpeg, beim Direct Play = Quell-Auflösung). Fallback auf Profil-
  // Label, falls das Video-Element noch keine Metadaten hat.
  const currentPlayingRes = () => {
    const root = state.vjs && state.vjs.el ? state.vjs.el() : null;
    const vid = root ? root.querySelector("video") : null;
    const w = vid && vid.videoWidth ? vid.videoWidth : 0;
    const h = vid && vid.videoHeight ? vid.videoHeight : 0;
    if (w || h) return resLabel({ width: w, height: h });
    // Fallback anhand Profil
    if (isTranscode && profile && profile !== "orig") return profile;
    return resLabel(item);
  };
  const setStats = (text) => { if (stats) stats.textContent = text; };
  const setClass = (...cls) => {
    // Nur die Status-Marker (behind/low) togglen — alles andere behalten.
    // Wichtig: --docked darf NICHT überschrieben werden, sonst fällt das
    // Overlay zurück in die absolute Position oben rechts.
    el.classList.remove("behind", "low");
    cls.forEach(c => c && el.classList.add(c));
  };
  // Lokaler Flag: sobald der Transcode fertig ist, fällt der Poll in den
  // Buffer-only-Modus zurück. Timer läuft weiter, damit das Overlay auch
  // nach Transcode-Ende bei jedem Mouse-Move aktuelle Werte zeigt.
  let transcodeDone = false;
  const poll = async () => {
    const clientAhead = clientBuffer();
    const res = currentPlayingRes();
    const resChip = res ? `${res} · ` : "";
    if (!isTranscode || transcodeDone) {
      setStats(`${resChip}Buffer +${Math.round(clientAhead)} s`);
      setClass(clientAhead < 1 ? "behind" : (clientAhead < 5 ? "low" : null));
      return;
    }
    try {
      const d = await api(url);
      const pos = Number(d.positionSec || 0);
      const cur = state.vjs ? state.vjs.currentTime() : 0;
      const serverAhead = Math.max(0, pos - cur);
      if (d.done) {
        transcodeDone = true;
        setStats(`${resChip}Buffer +${Math.round(clientAhead)} s`);
        setClass(clientAhead < 1 ? "behind" : (clientAhead < 5 ? "low" : null));
        return;
      }
      const sign = serverAhead >= 1 ? "+" : "";
      setStats(`${resChip}Server ${sign}${Math.round(serverAhead)} s · Buffer +${Math.round(clientAhead)} s`);
      setClass(serverAhead < 1 ? "behind" : (clientAhead < 5 ? "low" : null));
    } catch (e) {
      // Poll-Fehler — stumm
    }
  };
  poll();
  // Timer alle 1s: in Transcode-Mode zieht das Server-Progress nach, sonst
  // reine Buffer-Anzeige. 1s fühlt sich bei Mouse-Move responsiver an als 2s.
  state.transcodePollTimer = setInterval(poll, 1000);
}

// Rückwärtskompatibler Alias, falls noch jemand die alte Funktion ruft.
function startTranscodeProgress(item, profile, audioIdx) {
  return startBufferDisplay(item, "transcode", profile, audioIdx);
}

// Beim Transcode hat VHS keine stabile Gesamtlänge. Wir setzen die aus ffprobe
// bekannte Filmdauer als duration-Cache. Bei jeder tech-seitigen durationchange
// re-applyen wir, damit VHS sie nicht mit der wachsenden seekable.end überschreibt.
const forcedDurationState = new WeakMap(); // vjs → { total, onChange }

function forcePlayerDuration(vjs, totalSec) {
  if (!vjs || !totalSec || totalSec <= 0) return;
  releasePlayerDuration(vjs);
  let applying = false;
  const apply = () => {
    if (applying) return;
    const cur = vjs.duration();
    if (cur !== totalSec) {
      applying = true;
      try { vjs.duration(totalSec); } catch {}
      applying = false;
    }
  };
  vjs.on("durationchange", apply);
  vjs.on("loadedmetadata", apply);
  vjs.on("loadeddata", apply);
  apply();
  forcedDurationState.set(vjs, { total: totalSec, apply });
}

function releasePlayerDuration(vjs) {
  if (!vjs) return;
  const s = forcedDurationState.get(vjs);
  if (s && s.apply) {
    vjs.off("durationchange", s.apply);
    vjs.off("loadedmetadata", s.apply);
    vjs.off("loadeddata", s.apply);
  }
  forcedDurationState.delete(vjs);
}

// Stoppt nur den Poll-Timer, lässt aber das Overlay sichtbar.
// So bleibt die Buffer/Auflösungs-Anzeige nach Transcode-Ende weiter bestehen.
function stopTranscodeProgress() {
  if (state.transcodePollTimer) {
    clearInterval(state.transcodePollTimer);
    state.transcodePollTimer = null;
  }
}
// Beim Player-Close / Source-Wechsel komplett ausblenden.
function hideBufferOverlay() {
  stopTranscodeProgress();
  const el = document.getElementById("transcodeAhead");
  if (el) el.classList.add("hidden");
}

// positionBufferOverlay verschiebt das Buffer-/Stats-Overlay je nach
// Fullscreen-Status. Im Fullscreen sitzt es absolut positioniert oben rechts
// im Video.js-Root (inkl. Title-Zeile). Im eingebetteten Modus wandert es in
// den Player-Footer und wird als kompakter Inline-Streifen dargestellt — so
// liegt es AUSSERHALB vom Bild und der Titel wird unterdrückt (steht ohnehin
// im Dialog-Header).
function positionBufferOverlay(vjs) {
  const overlay = document.getElementById("transcodeAhead");
  if (!overlay) return;
  const inFullscreen = vjs && typeof vjs.isFullscreen === "function" && vjs.isFullscreen();
  if (inFullscreen) {
    overlay.classList.remove("transcode-ahead--docked");
    if (vjs && overlay.parentElement !== vjs.el()) vjs.el().appendChild(overlay);
  } else {
    overlay.classList.add("transcode-ahead--docked");
    const wrap = document.querySelector(".player-wrap");
    const meta = document.getElementById("playerMeta");
    if (wrap && meta && overlay.parentElement !== wrap) {
      wrap.insertBefore(overlay, meta);
    }
  }
}
