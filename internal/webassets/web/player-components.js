// player-components.js — Video.js Custom Button Definitions.
//
// Reihenfolge in index.html: helpers → dialogs → api → cast → player-components → app.
//
// Diese Datei stellt nur EINE Funktion bereit: ensurePlayerComponents().
// Sie wird beim Player-Open in app.js aufgerufen, definiert die Button-
// Subklassen und registriert sie einmal pro Page-Load bei Video.js.
//
// Vorteil gegenueber externen DOM-Buttons: bleiben im Fullscreen-Modus
// sichtbar und werden von Video.js' Hover-Autohide korrekt mitgesteuert.
//
// Buttons:
//   Skip30Back/Forward — native vjs-icon-replay-30 / vjs-icon-forward-30
//   ShufflePrev/Next   — Zufalls-Wiedergabe-Navigation
//   FavoriteButton     — togglet items/:id/favorite, rotes Herz wenn an
//   PlaylistButton     — oeffnet „Zu Playlist hinzufuegen"-Dialog
//   DeleteButton       — Admin-only, doppelt bestaetigt, loescht Datei
//   CastButton         — vom Cast-Framework gesteuert (state.castReady)
//   AirPlayButton      — Safari/iOS, webkitShowPlaybackTargetPicker()
//
// Die Klassen referenzieren globale Funktionen aus app.js (skipPlayer,
// shufflePrev, shuffleNext, openAddToPlaylist, closePlayer,
// updatePlayerButtons, updateDetailFavBtn, loadItems) und Globals aus
// dialogs.js (appAlert, appConfirm, showToast), api.js (api,
// invalidateItemsCache), cast.js (startCastSession). Da die Klassen-
// Methoden erst zur Laufzeit aufgerufen werden, reicht es, dass die
// Globals zum CALL-Zeitpunkt verfuegbar sind — was bei einem geoeffneten
// Player-Dialog garantiert ist.
function ensurePlayerComponents() {
  if (!window.videojs || state.playerComponentsRegistered) return;
  const Button = window.videojs.getComponent("Button");
  class Skip30Back extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("30 Sekunden zurück");
      // Native Video.js-Icon-Klassen — gleiche Schrift, Groesse, Hoehe wie der
      // Play-Button. Eigene Glyph-CSS-Overrides wuerden das brechen.
      this.addClass("vjs-skip-backward-30");
      this.addClass("vjs-icon-replay-30");
    }
    handleClick() { skipPlayer(-30); }
  }
  class Skip30Forward extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("30 Sekunden vor");
      this.addClass("vjs-skip-forward-30");
      this.addClass("vjs-icon-forward-30");
    }
    handleClick() { skipPlayer(30); }
  }
  class ShufflePrev extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Vorheriges Zufalls-Video");
      this.addClass("vjs-shuffle-prev");
    }
    handleClick() { shufflePrev(); }
  }
  class ShuffleNext extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Nächstes Zufalls-Video");
      this.addClass("vjs-shuffle-next");
    }
    handleClick() { shuffleNext(); }
  }
  class FavoriteButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Zu Favoriten hinzufügen");
      this.addClass("vjs-favorite");
    }
    handleClick() {
      const it = state.currentItem;
      if (!it) return;
      const newState = !it.favorite;
      api(`/api/items/${it.id}/favorite`, { method: "PUT", body: JSON.stringify({ favorite: newState }) })
        .then(() => {
          it.favorite = newState;
          updatePlayerButtons();
          updateDetailFavBtn();
          loadItems();
        })
        .catch(e => appAlert(e.message));
    }
  }
  class PlaylistButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Zu Playlist hinzufügen");
      this.addClass("vjs-addplaylist");
    }
    handleClick() {
      const player = this.player_;
      const open = () => { try { openAddToPlaylist(); } catch (e) { console.warn(e); } };
      if (player && typeof player.isFullscreen === "function" && player.isFullscreen()) {
        const p = player.exitFullscreen();
        if (p && typeof p.then === "function") p.then(open, open);
        else setTimeout(open, 60);
      } else {
        open();
      }
    }
  }
  // Admin-Loeschbutton in der Control-Bar (neben PlaylistButton). Dezent
  // gehalten: Grauton im Idle, rotet erst bei Hover. Doppelte Bestaetigung.
  class DeleteButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Datei löschen");
      this.addClass("vjs-delete");
    }
    handleClick() {
      const player = this.player_;
      const run = async () => {
        const it = state.currentItem;
        if (!it) return;
        const name = (it.metadata && it.metadata.title) || it.title;
        if (!(await appConfirm(`Datei "${name}" UNWIEDERRUFLICH vom Server löschen?\n\nPfad: ${it.path}\n\nDies kann nicht rückgängig gemacht werden.`))) return;
        if (!(await appConfirm(`Wirklich sicher? Die Datei wird für IMMER gelöscht.`))) return;
        try {
          await api(`/api/items/${it.id}?deleteFile=true`, { method: "DELETE" });
          closePlayer();
          invalidateItemsCache();
          loadItems();
          showToast("Datei gelöscht", { kind: "success" });
        } catch (e) { appAlert(e.message); }
      };
      if (player && typeof player.isFullscreen === "function" && player.isFullscreen()) {
        const p = player.exitFullscreen();
        if (p && typeof p.then === "function") p.then(run, run);
        else setTimeout(run, 60);
      } else {
        run();
      }
    }
  }
  // Cast-Button fuer Chromecast / FireTV / Smart-TVs mit Google Cast.
  // Initialisierung des Cast-Frameworks laeuft in initCastFramework() —
  // wird einmal beim Browser-Start aufgerufen und setzt state.castReady,
  // sobald das Framework verfuegbar ist.
  class CastButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Auf Gerät streamen");
      this.addClass("vjs-cast-button");
      // Bis Cast-Framework bereit ist: Button ausblenden.
      if (!state.castReady) this.hide();
    }
    handleClick(e) {
      if (e && typeof e.preventDefault === "function") e.preventDefault();
      if (e && typeof e.stopPropagation === "function") e.stopPropagation();
      startCastSession();
    }
  }
  // AirPlay-Button fuer Safari/iOS. Die HTML-Attribute `airplay="allow"`
  // alleine zeigen das AirPlay-Icon nur an den NATIVEN Browser-Controls.
  // Da Video.js die Controls ueberlagert, brauchen wir einen Custom-Button,
  // der `webkitShowPlaybackTargetPicker()` triggert. Sichtbarkeit folgt
  // dem `webkitplaybacktargetavailabilitychanged`-Event am <video>.
  class AirPlayButton extends Button {
    constructor(player, options) {
      super(player, options);
      this.controlText("Auf AirPlay-Gerät streamen");
      this.addClass("vjs-airplay-button");
      this.hide();
      player.ready(() => {
        const v = player.tech_ && player.tech_.el_;
        if (!v || typeof v.webkitShowPlaybackTargetPicker !== "function") return;
        const onAvail = (e) => {
          if (e.availability === "available") this.show(); else this.hide();
        };
        v.addEventListener("webkitplaybacktargetavailabilitychanged", onAvail);
      });
    }
    handleClick() {
      const v = this.player().tech_ && this.player().tech_.el_;
      if (v && typeof v.webkitShowPlaybackTargetPicker === "function") {
        v.webkitShowPlaybackTargetPicker();
      }
    }
  }
  if (!window.videojs.getComponent("ShufflePrev")) {
    window.videojs.registerComponent("Skip30Back", Skip30Back);
    window.videojs.registerComponent("Skip30Forward", Skip30Forward);
    window.videojs.registerComponent("ShufflePrev", ShufflePrev);
    window.videojs.registerComponent("ShuffleNext", ShuffleNext);
    window.videojs.registerComponent("FavoriteButton", FavoriteButton);
    window.videojs.registerComponent("PlaylistButton", PlaylistButton);
    window.videojs.registerComponent("DeleteButton", DeleteButton);
    window.videojs.registerComponent("CastButton", CastButton);
    window.videojs.registerComponent("AirPlayButton", AirPlayButton);
  }
  state.playerComponentsRegistered = true;
}
