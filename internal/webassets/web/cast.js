// cast.js — Google-Cast-Integration (Chromecast / FireTV+AirScreen / Cast-TVs).
//
// Reihenfolge in index.html: helpers → dialogs → api → cast → app.
// Greift auf den globalen `state` und `appAlert/showToast` zu — also nach
// dialogs.js und vor app.js laden.
//
// Public Functions:
//   initCastFramework()  — vom boot() in app.js aufgerufen, einmal pro Page-Load
//   startCastSession()   — vom CastButton-Click; zeigt Geraete-Picker + lädt Media
//
// Hintergrund:
// - Cast-Sender-SDK laedt async aus index.html (cast_sender.js).
// - SDK ruft window.__onGCastApiAvailable(isAvailable) auf, wenn fertig.
// - Stub in index.html speichert das Result, wir lesen es hier aus.
// - Bei Connect: AppleTV unterstuetzt KEIN Google-Cast. Dafuer AirPlay.

// Das Cast-Framework laedt asynchron (Script in index.html). Sobald es
// verfuegbar ist, initialisieren wir es mit dem Default-Media-Receiver
// (CC1AD845) — das ist Googles offizielle App, die direkte Stream-URLs
// (HLS, MP4) abspielt. Eigener Receiver waere moeglich, aber unnoetig: fuer
// einfaches Streaming reicht der Default.
function initCastFramework() {
  // Setup-Function: laeuft sobald wir wissen, ob Cast verfuegbar ist.
  // Idempotent — kann sowohl aus dem fruehen __onGCastApiAvailable-Pfad
  // (Stub in index.html) als auch aus einem spaeten Callback aufgerufen werden.
  const setup = (isAvailable) => {
    if (!isAvailable || !window.cast || !window.cast.framework) {
      console.log("[cast] not available in this browser (Firefox? kein Chromium?)");
      return;
    }
    try {
      const ctx = window.cast.framework.CastContext.getInstance();
      ctx.setOptions({
        receiverApplicationId: window.chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
        autoJoinPolicy: window.chrome.cast.AutoJoinPolicy.ORIGIN_SCOPED,
      });
      state.castReady = true;
      // Sichtbarkeit aller schon registrierten Cast-Buttons aktualisieren.
      if (state.vjs) {
        const cb = state.vjs.getChild("controlBar");
        const btn = cb && cb.getChild("CastButton");
        if (btn) btn.show();
      }
      console.log("[cast] framework ready");
    } catch (e) {
      console.warn("[cast] init failed", e);
    }
  };
  // Race-Handling: Wenn das Cast-SDK aus dem Browser-Cache schneller fertig
  // ist als app.js, hat der index.html-Stub das Ergebnis bereits gespeichert.
  // Sonst registrieren wir uns als Callback fuer den spaeteren SDK-Ready-Event.
  if (window.__castSdkReady) {
    setup(window.__castSdkAvailable);
  } else {
    window.__onCastSdkResult = setup;
  }
}

// startCastSession: vom CastButton-Click. Holt einen Cast-Session-Token vom
// Server (via /api/auth/cast-token, 4 h TTL), baut die Stream-URL mit
// `?session=<token>` und startet die Cast-Session am Default-Receiver.
async function startCastSession() {
  console.log("[cast] CastButton clicked", {
    castReady: state.castReady,
    castGlobal: !!window.cast,
    castFramework: !!(window.cast && window.cast.framework),
    chromeCast: !!(window.chrome && window.chrome.cast),
    currentItem: state.currentItem && state.currentItem.id,
  });
  if (!state.castReady || !window.cast || !window.cast.framework) {
    appAlert('Cast-Framework ist nicht bereit. Konsole öffnen (Cmd-Opt-I) und nach "[cast]" suchen.');
    return;
  }
  if (!state.currentItem) return;
  const item = state.currentItem;
  let token = state.castToken || "";
  try {
    if (!token) {
      const r = await api("/api/auth/cast-token", { method: "POST" });
      token = r && r.token;
      if (token) state.castToken = token;
    }
  } catch (e) {
    appAlert("Cast-Token konnte nicht erstellt werden: " + e.message);
    return;
  }
  if (!token) return;
  // Stream-URL: bei Direct Play unsere /api/stream/{id}-Route, bei Transcode
  // die HLS-Playlist (Cast unterstuetzt HLS nativ via Default-Receiver).
  const mode = (state.playback && state.playback.mode) || "direct";
  const sep = (u) => u.includes("?") ? "&" : "?";
  let url, contentType;
  if (mode === "transcode") {
    const profile = (state.playback && state.playback.profile) || "orig";
    const audioIdx = state.playback && state.playback.audioIdx;
    url = `${location.origin}/api/transcode/${item.id}/index.m3u8?profile=${encodeURIComponent(profile)}`;
    if (typeof audioIdx === "number" && audioIdx >= 0) url += `&audio=${audioIdx}`;
    url += `&session=${encodeURIComponent(token)}`;
    contentType = "application/vnd.apple.mpegurl";
  } else {
    url = `${location.origin}/api/stream/${item.id}${sep(`/api/stream/${item.id}`)}session=${encodeURIComponent(token)}`;
    contentType = "video/mp4";
  }
  try {
    const ctx = window.cast.framework.CastContext.getInstance();
    await ctx.requestSession();
    const session = ctx.getCurrentSession();
    if (!session) return;
    const mediaInfo = new window.chrome.cast.media.MediaInfo(url, contentType);
    mediaInfo.metadata = new window.chrome.cast.media.GenericMediaMetadata();
    const md = item.metadata;
    mediaInfo.metadata.title = (md && md.title) || item.title;
    if (md && md.posterPath && item.metadataId) {
      mediaInfo.metadata.images = [
        new window.chrome.cast.Image(`${location.origin}/api/poster/metadata/${item.metadataId}`),
      ];
    }
    const request = new window.chrome.cast.media.LoadRequest(mediaInfo);
    // Wenn der lokale Player bereits laeuft: an aktueller Position weiterspielen.
    if (state.vjs && typeof state.vjs.currentTime === "function") {
      const cur = state.vjs.currentTime() || 0;
      const offset = (state.playback && state.playback.virtualOffset) || 0;
      request.currentTime = Math.max(0, cur + offset);
      // Lokal pausieren — Cast uebernimmt jetzt.
      try { state.vjs.pause(); } catch {}
    }
    await session.loadMedia(request);
    showToast("Cast gestartet — Wiedergabe läuft am gewählten Gerät", { kind: "success", duration: 3500 });
  } catch (e) {
    if (e && e.code === "cancel") return; // User hat das Geraete-Picker-Modal abgebrochen
    const code = e && e.code;
    let msg;
    if (code === "session_error" || code === "receiver_unavailable") {
      msg = `Kein Cast-Gerät gefunden.\n\nGoogle-Cast funktioniert mit Chromecast, FireTV (mit 'AirScreen'-App) oder Smart-TVs mit eingebautem Cast (Sony Bravia, Vizio …).\n\nApple TV unterstützt kein Google-Cast — dafür AirPlay aus Safari nutzen.`;
    } else if (code === "timeout") {
      msg = `Cast-Verbindung Timeout — Gerät reagiert nicht. Im selben WLAN? Neu starten?`;
    } else {
      msg = "Cast-Fehler: " + (e && e.description || code || e);
    }
    appAlert(msg);
  }
}
