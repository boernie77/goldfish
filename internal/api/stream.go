package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/ocrsub"
	"github.com/boernie77/goldfish/internal/playback"
	"github.com/boernie77/goldfish/internal/whisper"
)

// resolveDeinterlace übersetzt den Query-Param `deinterlace=auto|on|off`
// (default auto) zusammen mit dem erkannten Interlaced-Status zu einem
// effektiven Bool für die ffmpeg-Filter-Chain.
func resolveDeinterlace(param string, isInterlaced bool) bool {
	switch param {
	case "on":
		return true
	case "off":
		return false
	default: // "auto" oder leer
		return isInterlaced
	}
}

// playbackInfo tells the client which mode to use and which URL to load.
// Query ?mode=auto|direct|transcode erlaubt einen Override.
func (s *Server) playbackInfo(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}
	// 🔴→✅ Kein automatisches "play"-Log mehr HIER (Bug, gefixt 2026-09-11,
	// User-Report: "Jetzt habe ich aber 2x Wiedergabe gestartet im
	// Protokoll stehen!"): dieser Endpoint dient ZWEI Zwecken — der
	// tatsächliche Wiedergabe-Start (`player.js applyPlayback`/`PlayerView
	// .setUp`) UND das reine Vorab-Laden der Stream-Liste fürs Detail-
	// Dialog-Dropdown (Ton/Untertitel/Qualität, `ItemDetailView.loadStreams`
	// bzw. Browser-Äquivalent) — BEIDE riefen denselben `GET /api/playback/
	// {id}` auf, ein Öffnen des Detail-Dialogs (ohne je auf Play zu tippen)
	// erzeugte dadurch schon einen "Wiedergabe gestartet"-Eintrag, und ein
	// tatsächliches Abspielen direkt danach einen zweiten. Das Logging ist
	// jetzt client-getriggert (siehe `playbackStart` unten) — symmetrisch
	// zu `playbackStop`/`playbackError`, die aus demselben Grund schon
	// eigene Endpoints sind statt am GET mitzuhängen.

	q := r.URL.Query()
	profile := q.Get("profile")
	if profile == "" {
		profile = "orig"
	}
	profileObj := playback.ProfileByID(profile)
	audio := q.Get("audio")

	auto := playback.Decide(it)
	var chosen playback.Decision
	switch q.Get("mode") {
	case "direct":
		chosen = playback.Decision{Mode: playback.ModeDirectPlay, Reason: "manuell: Direct Play"}
	case "transcode":
		chosen = playback.Decision{Mode: playback.ModeTranscode, Reason: "manuell: Transcode"}
	default:
		// Auto-Modus: Profil wirkt als Qualitäts-Cap — wenn das Item das Limit
		// überschreitet, wird Transcode erzwungen; sonst normal entschieden.
		chosen = playback.DecideWithCap(it, profileObj)
	}
	// Bei Direct Play für mp4/mov/m4v zusätzlich prüfen, ob das `moov`-Atom
	// vor dem `mdat`-Atom liegt. Sonst können Browser den Stream nicht
	// progressive dekodieren („Cannot parse metadata"). In dem Fall
	// auf Transcode umschalten — Auto-Modus, nicht aber wenn der User
	// explizit „direct" gewählt hat (er kennt sein Symptom dann).
	if chosen.Mode == playback.ModeDirectPlay && q.Get("mode") != "direct" {
		if streamable, perr := playback.IsMP4Streamable(it.Path); perr == nil && !streamable {
			chosen = playback.Decision{
				Mode:   playback.ModeTranscode,
				Reason: "MP4 nicht streamfähig (moov-Atom am Dateiende) — Transcode angewendet",
			}
		}
	}

	// Stream-Liste dazupacken (Client zeigt Audio/Subtitle-Dropdown)
	streams, _ := s.Store.ItemStreams(it.ID)
	it.Streams = streams // damit IsInterlaced / Decide korrekt arbeiten

	// KI-generierte Untertitel anhängen: VTT-Datei auf Disk ist der Wahrheits-Anker,
	// nicht der DB-Status (ein fehlgeschlagener Retry löscht die alte Datei nicht).
	if genJobs, err := s.Store.ListItemSubtitles(it.ID); err == nil {
		for i, job := range genJobs {
			vttPath := whisper.VTTPath(s.ConfigDir, it.ID, job.Language)
			if job.Status != "done" {
				if _, statErr := os.Stat(vttPath); statErr != nil {
					continue // kein Status "done" und keine Datei → überspringen
				}
			}
			streams = append(streams, model.ItemStream{
				Index:    2000 + i,
				Type:     "subtitle",
				Codec:    "webvtt-generated",
				Language: job.Language,
				Title:    whisperSubStreamLabel(job.Language),
			})
		}
	}
	// OCR-Untertitel (Bild-Untertitel → Text, internal/ocrsub): eine
	// {lang}-ocr.vtt pro erkannte Sprache. Wahrheits-Anker ist die Datei.
	for oi, lang := range []string{"de", "en", "it", "fr", "es", "nl", "pt"} {
		if _, statErr := os.Stat(ocrsub.VTTPath(s.ConfigDir, it.ID, lang)); statErr != nil {
			continue
		}
		streams = append(streams, model.ItemStream{
			Index:    2100 + oi,
			Type:     "subtitle",
			Codec:    "webvtt-ocr",
			Language: lang,
			Title:    "📝 " + whisperLangLabel(lang) + " (OCR)",
		})
	}
	// Sidecar-Untertitel: Untertitel-DATEIEN neben der Videodatei
	// (`Film.de.vtt` von yt-dlp, `Film.en.srt` aus einem Rip). Der Scanner
	// erfasst nur eingebettete Spuren, deshalb hier zur Abfragezeit suchen —
	// siehe internal/api/subtitles_sidecar.go. Index + Sortierung müssen zu
	// `subtitleVTT` passen, das dieselbe Liste erneut aufbaut.
	for si, sc := range findSidecarSubs(it.Path) {
		streams = append(streams, model.ItemStream{
			Index:    sidecarIndexBase + si,
			Type:     "subtitle",
			Codec:    "webvtt-sidecar",
			Language: sc.Language,
			Title:    sidecarSubLabel(sc),
			IsForced: sc.Forced,
		})
	}
	isInterlaced := playback.IsInterlaced(it)
	deinterlaceParam := q.Get("deinterlace")
	if deinterlaceParam == "" {
		deinterlaceParam = "auto"
	}
	deinterlaceActive := resolveDeinterlace(deinterlaceParam, isInterlaced)

	resp := map[string]any{
		"mode":              chosen.Mode,
		"reason":            chosen.Reason,
		"autoMode":          auto.Mode,
		"autoReason":        auto.Reason,
		"item":              it,
		"profile":           profile,
		"profiles":          playback.Profiles,
		"streams":           streams,
		"interlaced":        isInterlaced,
		"deinterlace":       deinterlaceParam,  // gewählter Modus (auto/on/off)
		"deinterlaceActive": deinterlaceActive, // ob ffmpeg ihn tatsächlich anwendet
	}
	switch chosen.Mode {
	case playback.ModeDirectPlay:
		resp["url"] = "/api/stream/" + strconv.FormatInt(it.ID, 10)
	case playback.ModeTranscode:
		u := "/api/transcode/" + strconv.FormatInt(it.ID, 10) + "/index.m3u8?profile=" + profile
		if audio != "" {
			u += "&audio=" + audio
		}
		if deinterlaceParam != "auto" {
			u += "&deinterlace=" + deinterlaceParam
		}
		resp["url"] = u
	}
	writeJSON(w, 200, resp)
}

// fmtClock formatiert Sekunden als "mm:ss" bzw. "h:mm:ss" für Protokoll-Texte.
func fmtClock(sec float64) string {
	if sec < 0 || sec != sec { // NaN-Guard
		sec = 0
	}
	total := int(sec + 0.5)
	h, rem := total/3600, total%3600
	m, s := rem/60, rem%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// playbackStart: POST /api/playback/{id}/start — client-getriggertes "play"-
// Log (Bug-Fix 2026-09-11, siehe Kommentar in `playbackInfo`: der GET-
// Endpoint dort wird auch vom reinen Detail-Dialog-Metadaten-Prefetch
// aufgerufen, ein automatisches Log dort erzeugte Duplikate). Wird NUR vom
// tatsächlichen Play-Auslöser aufgerufen (`player.js applyPlayback`,
// `PlayerView.setUp`, `MusicPlayerEngine`), nie vom reinen Stream-Info-Fetch.
func (s *Server) playbackStart(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "playback", "play", it.Title, deviceLabel(r))
	}
	w.WriteHeader(204)
}

type playbackStopRequest struct {
	// "ended" = natürliches Ende (Video zu Ende gelaufen), "closed" = User hat
	// den Player manuell geschlossen/verlassen, bevor es zu Ende war.
	Reason      string  `json:"reason"`
	PositionSec float64 `json:"positionSec"`
	DurationSec float64 `json:"durationSec"`
}

// playbackStop: POST /api/playback/{id}/stop — Gegenstück zu playbackStart's
// "play"-Log-Eintrag (User-Wunsch 2026-09-11: "nicht nur Wiedergabe
// gestartet, sondern auch beendet"). Der Server kann das Ende einer
// Wiedergabe nicht selbst erkennen (HTTP ist zustandslos, ein Transcode-
// Session-Timeout sagt nur "5 Minuten kein Request mehr", nicht "der User
// hat bewusst gestoppt") — die Clients (Browser/Apple-App) melden es aktiv,
// wenn `ended` feuert oder der Player geschlossen wird. Best-effort: ein
// Client, der abstürzt oder die Verbindung verliert, meldet nie einen Stop —
// das ist eine bewusste Grenze (kein Ersatz für eine Session-Heartbeat-
// Architektur), reicht aber für den Protokoll-Zweck ("wer hat wann womit
// aufgehört zu schauen") deutlich weiter als reines "play"-Logging.
func (s *Server) playbackStop(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	var body playbackStopRequest
	_ = json.NewDecoder(r.Body).Decode(&body) // best-effort, leerer Body ist ok
	reasonLabel := "geschlossen"
	if body.Reason == "ended" {
		reasonLabel = "zu Ende"
	}
	detail := it.Title
	if body.DurationSec > 0 {
		detail = fmt.Sprintf("%s (%s von %s, %s)", it.Title, fmtClock(body.PositionSec), fmtClock(body.DurationSec), reasonLabel)
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "playback", "stop", detail, deviceLabel(r))
	}
	// Laufende Transcode-Session(en) dieses Items sofort beenden, statt bis
	// zu 30 Min. auf den Idle-GC zu warten — ffmpeg encodiert ohne
	// Gegendruck vom Client so schnell wie moeglich weiter, eine „vergessene"
	// Session kostet also volle Last, obwohl niemand mehr zusieht.
	s.Playback.StopAllForItem(it.ID)
	w.WriteHeader(204)
}

type playbackErrorRequest struct {
	Message string `json:"message"`
}

// playbackError: POST /api/playback/{id}/error — protokolliert einen
// Wiedergabe-Fehler vom Client aus (User-Wunsch 2026-09-11: "wenn zum
// Beispiel ein Video abbricht"). Der Server sieht viele Fehlerklassen selbst
// nie (z. B. ein Netzwerkabbruch beim Client, ein Decode-Fehler im Browser-
// `<video>`-Element) — nur der Client weiß zuverlässig, dass die Wiedergabe
// gerade fehlgeschlagen ist.
func (s *Server) playbackError(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	var body playbackErrorRequest
	_ = json.NewDecoder(r.Body).Decode(&body)
	msg := strings.TrimSpace(body.Message)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	// Serverseitigen Zustand JETZT festhalten. Der Client meldet nur eine
	// generische Fehlermeldung ("Internal data stream error" o. ae.) — ob
	// dahinter eine tote ffmpeg-Session, eine leere Playlist oder gar keine
	// Session steckt, ist wenige Minuten spaeter nicht mehr feststellbar
	// (der GC raeumt ab). Deshalb hier und nicht erst bei der Auswertung.
	diag := "n/a"
	if s.Playback != nil {
		diag = s.Playback.DiagnoseItem(it.ID)
	}
	log.Printf("[playback] FEHLER item=%d %q client=%q geraet=%q | %s",
		it.ID, it.Title, msg, deviceLabel(r), diag)

	detail := it.Title
	if msg != "" {
		detail = fmt.Sprintf("%s — %s", it.Title, msg)
	}
	// Der Zustand wandert auch ins Protokoll: Container-Logs rotieren, die
	// activity_log-Zeile bleibt 180 Tage und ist damit die verlaesslichere
	// Quelle, wenn der Fehler erst Tage spaeter untersucht wird.
	detail = fmt.Sprintf("%s [server: %s]", detail, diag)

	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "playback", "error", detail, deviceLabel(r))
	}
	w.WriteHeader(204)
}

// streamDirect serves the raw file with HTTP range support.
func (s *Server) streamDirect(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}
	f, err := os.Open(it.Path)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", mimeForExt(filepath.Ext(it.Path)))
	// Direct Play hat kein Session-Konzept wie Transcode (dessen Touch()
	// bereits playback.TouchActivity() mitzieht) — hier direkt markieren,
	// damit Scanner/Trickplay/Introskip/OCR wissen, dass gerade etwas
	// angesehen wird (siehe internal/playback/activity.go).
	playback.TouchActivity()
	http.ServeContent(w, r, filepath.Base(it.Path), info.ModTime(), f)
}

func mimeForExt(ext string) string {
	// filepath.Ext liefert die Extension case-preserving zurück. Files mit
	// `.MP4` o. ä. groß-/gemischtgeschrieben würden sonst auf
	// application/octet-stream fallen → Browser bricht Direct Play mit
	// „MIME-Typ nicht unterstützt" ab. Lowercase'n bevor wir matchen.
	switch strings.ToLower(ext) {
	case ".mp4", ".mov", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".wmv":
		return "video/x-ms-wmv"
	// 🔴 Bug 2026-09-04: fehlte komplett — Musik-Direct-Play (mimeForExt kannte
	// nur Video-Extensions) lief dadurch mit Content-Type
	// "application/octet-stream" vom Server. Browser lehnen es ab, ein
	// <video>/<audio>-Element mit diesem MIME-Type überhaupt zu decodieren
	// (kein MIME-Sniffing für Medienelemente) — das Element blieb dauerhaft
	// bei readyState=0 hängen, live per DevTools verifiziert (fetch selbst
	// war mit ~80ms sofort da, das Element startete trotzdem nie). Erklärt
	// vermutlich den kompletten "lange Verzögerung"-Bug abschließend.
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".m4b":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	}
	return "application/octet-stream"
}

// transcodePlaylist starts (or reuses) an ffmpeg session and serves its playlist.
func (s *Server) transcodePlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}

	startSec := 0.0
	if v := r.URL.Query().Get("start"); v != "" {
		startSec, _ = strconv.ParseFloat(v, 64)
	}
	profile := playback.ProfileByID(r.URL.Query().Get("profile"))
	audioIdx := -1
	if v := r.URL.Query().Get("audio"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			audioIdx = n
		}
	}

	// Deinterlace-Resolver: Item-Streams einmal mit-laden, dann gemäß Param entscheiden
	{
		streams, _ := s.Store.ItemStreams(it.ID)
		it.Streams = streams
	}
	deinterlace := resolveDeinterlace(r.URL.Query().Get("deinterlace"), playback.IsInterlaced(it))
	// fresh=1: bestehende Session zwingt-stop, damit eine neue ffmpeg-Session
	// mit leerem Playlist-Stand startet. Wird vom „Von Anfang"-Pfad genutzt —
	// sonst bekommt der Browser eine Playlist, in der schon ein paar hundert
	// Sekunden Material liegen, und springt nicht zur Position 0.
	//
	// IDEMPOTENZ via Manager.ConsumeFresh: VHS laedt die EVENT-Playlist
	// periodisch (alle ~targetDuration ≈ 4 s) mit DERSELBEN URL inkl.
	// fresh=1. Ein Age-Threshold reicht NICHT als Idempotenz — sobald die
	// Session aelter als 4 s ist, killt jede VHS-Reload sie erneut →
	// Wiedergabe stottert sichtbar (Server-Buffer pendelt zwischen 0 und
	// hoch). ConsumeFresh sperrt den Stop-Weg fuer 60 s pro Session-Key,
	// nachdem ihn der erste Request ausgeloest hat — VHS-Reloads sind dann
	// no-op und die laufende Session bleibt am Leben.
	if r.URL.Query().Get("fresh") == "1" {
		// `_t` ist der per-Player-Open-eindeutige Token aus dem Frontend
		// (Date.now()). VHS-Reloads behalten denselben Token, ein neuer
		// Open generiert einen neuen — so bleibt das Killen+Neustarten
		// gebunden an echte User-Actions, nicht an Wallclock-Fenster.
		freshToken := r.URL.Query().Get("_t")
		if s.Playback.ConsumeFresh(it.ID, profile, audioIdx, startSec, deinterlace, freshToken) {
			s.Playback.StopSession(it.ID, profile, audioIdx, startSec, deinterlace)
		}
	}
	// Wer startet hier eigentlich einen Transcode? Die `[transcode] start`-Zeile
	// in internal/playback kennt weder Benutzer noch Geraet — das Manager-Paket
	// sieht keinen Request. Genau das fehlte am 2026-09-14, als eine ueber
	// 40 Minuten laufende 4K-Session niemandem zuzuordnen war: ein Client, der
	// `POST /playback/{id}/start` nicht ruft (aeltere App-Staende), hinterlaesst
	// sonst ueberhaupt keine Spur. Deshalb hier mitloggen — und NUR, wenn
	// wirklich eine neue Session entsteht, nicht bei jedem VHS-Playlist-Reload
	// derselben Session (sonst waere das Log im Sekundentakt zu.)
	if s.Playback.LookupSession(it.ID, profile, audioIdx, startSec, deinterlace) == nil {
		who := "unbekannt"
		if me := currentUser(r); me != nil {
			who = me.Username
		}
		log.Printf("[transcode] neue session item=%d %q benutzer=%s geraet=%q",
			it.ID, it.Title, who, deviceLabel(r))
	}
	sess, err := s.Playback.StartOrGet(it.ID, it.Path, profile, audioIdx, startSec, deinterlace, it.VideoCodec == "", it.Height)
	if err != nil {
		// Limit erreicht: 503 statt 500 — das ist ein temporaerer Zustand,
		// kein Serverfehler. Der Text wird im Player direkt angezeigt, muss
		// also fuer Endnutzer verstaendlich sein (nicht „ffmpeg start: …").
		if errors.Is(err, playback.ErrTooManySessions) {
			w.Header().Set("Retry-After", "30")
			writeError(w, 503, "Der Server ist gerade ausgelastet — es laufen zu viele "+
				"gleichzeitige Umwandlungen. Bitte in einem Moment erneut versuchen.")
			return
		}
		// Direkt nach einem gemeldeten Client-Stop (z. B. dem Wiedergabe-Ende):
		// die Player holen am Ende einer EVENT-Playlist von sich aus die
		// Playlist erneut und treffen dabei ins 3-Sekunden-Sperrfenster. Mit 500
		// wurde daraus ein modaler Abspielfehler („Stream-Fehler (-16847) … HTTP
		// 500", User-Report macOS 2026-09-18 genau am Folgen-Ende). 503 +
		// Retry-After ist die korrekte Antwort für einen temporären Zustand —
		// dieselbe Begründung wie beim Limit oben.
		if errors.Is(err, playback.ErrStoppedRecently) {
			w.Header().Set("Retry-After", "3")
			writeError(w, 503, "Die Wiedergabe wurde gerade beendet — bitte einen "+
				"Moment warten.")
			return
		}
		writeError(w, 500, "ffmpeg start: "+err.Error())
		return
	}
	if err := sess.WaitForPlaylist(20 * time.Second); err != nil {
		// Rueckfall-Stufen: seit 2026-09-14 dekodiert der VAAPI-Pfad per
		// Grafikeinheit. Scheitert die an dieser Datei, gibt ffmpeg sofort
		// auf, ohne je eine Playlist zu schreiben — ohne diesen zweiten
		// Versuch waere die Wiedergabe damit tot. NUR bei ErrFFmpegDiedEarly:
		// ein blosser Zeitueberlauf heisst, dass ffmpeg noch arbeitet, da
		// wuerde ein Neustart nur schaden.
		//
		// Zwei Stufen, in dieser Reihenfolge (siehe Manager.RetryWithFallback):
		// erst CPU-Decode + Grafikeinheit-Encode (2026-09-22, Faktor 3,5
		// weniger CPU als der reine Software-Weg), dann vollstaendig per CPU.
		// Die Schleife endet spaetestens nach der letzten Stufe — eine
		// Endlosschleife bei einer wirklich kaputten Datei ist damit aus.
		for tries := 0; tries < 2 && sess.CanFallback(); tries++ {
			if !errors.Is(err, playback.ErrFFmpegDiedEarly) {
				break
			}
			retry, rerr := s.Playback.RetryWithFallback(sess)
			if rerr != nil {
				break
			}
			sess = retry
			err = sess.WaitForPlaylist(20 * time.Second)
		}
		if err != nil {
			writeError(w, 500, "playlist: "+err.Error())
			return
		}
	}

	// Playlist einlesen und Segment-URIs um die Query-Parameter ergänzen.
	// Ohne das verlieren die relativen seg*.ts-Requests unsere Query-Werte und
	// landen auf der Default-Session (startSec=0) → Video spielt von vorn.
	raw, err := os.ReadFile(filepath.Join(sess.Dir, "index.m3u8"))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	q := r.URL.RawQuery
	var out strings.Builder
	// Eine führende `#EXT-X-DISCONTINUITY`-Zeile (bevor das erste Segment
	// auftaucht) lässt VHS einen „Gap" am Playlist-Anfang annehmen → keine
	// Buffer-Akkumulation bei currentTime=0. Die Markierung ist kosmetisch
	// von ffmpeg mitgeliefert (abhängig von hls_flags-Kombis), funktional
	// unnötig bei frischen Sessions. Deshalb streichen wir sie bis zum ersten
	// echten Segment raus.
	seenSegment := false
	for _, line := range strings.Split(string(raw), "\n") {
		tl := strings.TrimSpace(line)
		if !seenSegment && tl == "#EXT-X-DISCONTINUITY" {
			continue // führende Discontinuity überspringen
		}
		if tl == "" || strings.HasPrefix(tl, "#") {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		// Erste nicht-Kommentar-Zeile = Segment-URI
		seenSegment = true
		if q != "" && !strings.Contains(line, "?") {
			out.WriteString(line)
			out.WriteByte('?')
			out.WriteString(q)
			out.WriteByte('\n')
		} else {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(out.String()))
}

// transcodeProgress liefert, bis zu welcher Quelldatei-Sekunde ffmpeg bereits
// transcodiert hat. Der Client nutzt das zusammen mit der aktuellen Wiedergabe-
// Position, um einen "+N s"-Puffer-Indicator anzuzeigen.
func (s *Server) transcodeProgress(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}
	startSec := 0.0
	if v := r.URL.Query().Get("start"); v != "" {
		startSec, _ = strconv.ParseFloat(v, 64)
	}
	profile := playback.ProfileByID(r.URL.Query().Get("profile"))
	audioIdx := -1
	if v := r.URL.Query().Get("audio"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			audioIdx = n
		}
	}
	// Deinterlace-Resolver: Item-Streams einmal mit-laden, dann gemäß Param entscheiden
	{
		streams, _ := s.Store.ItemStreams(it.ID)
		it.Streams = streams
	}
	deinterlace := resolveDeinterlace(r.URL.Query().Get("deinterlace"), playback.IsInterlaced(it))
	// LOOKUP, nicht StartOrGet: ein Progress-Poll mit nicht ganz exakt
	// passenden Parametern darf NIE eine neue Session erzeugen — sonst laufen
	// zwei ffmpeg-Instanzen parallel (eine fuer Playback, eine fuer Progress),
	// streiten sich um VAAPI/CPU, Wiedergabe stottert. Wenn keine Session da
	// ist, antworten wir mit positionSec=0; der Client zeigt dann Server +0.
	sess := s.Playback.LookupSession(it.ID, profile, audioIdx, startSec, deinterlace)
	if sess == nil {
		writeJSON(w, 200, map[string]any{
			"positionSec": 0.0,
			"done":        false,
			"startSec":    startSec,
			"noSession":   true,
		})
		return
	}
	// Touch() ist hier essentiell: der Client pollt /progress durchgehend,
	// auch während der Player pausiert ist (kein Gate auf vjs.paused() in
	// player.js). Ohne Touch hier hält NICHTS die Session während einer
	// Pause am Leben — nur transcodeSegment touched, und bei Pause kommen
	// keine Segment-Requests mehr rein. Nach playback.sessionIdleTimeout
	// (siehe dort — 30 Min, war früher 5 Min) killt der GC-Loop dann die
	// ffmpeg-Session; beim Fortsetzen spielt der Client noch den Restbuffer,
	// dann 404 auf ein nicht mehr existierendes Segment → Wiedergabe bricht
	// ab. Mit Touch hier bleibt eine offene Player-Session beliebig lange am
	// Leben (GC greift erst wieder, wenn der Player-Dialog geschlossen wird
	// und stopTranscodeProgress() den Poll-Timer stoppt).
	sess.Touch()
	pos, err := sess.Position()
	if err != nil {
		pos = 0
	}
	writeJSON(w, 200, map[string]any{
		"positionSec": pos,
		"done":        sess.Done(),
		"startSec":    sess.StartSec,
	})
}

// transcodeSegment serves an individual HLS segment.
func (s *Server) transcodeSegment(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	seg := chi.URLParam(r, "seg")
	if !isSafeSegment(seg) {
		writeError(w, 400, "ungültiges Segment")
		return
	}

	// Reuse any session for this item (startSec=0 by default).
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}
	startSec := 0.0
	if v := r.URL.Query().Get("start"); v != "" {
		startSec, _ = strconv.ParseFloat(v, 64)
	}
	profile := playback.ProfileByID(r.URL.Query().Get("profile"))
	audioIdx := -1
	if v := r.URL.Query().Get("audio"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			audioIdx = n
		}
	}
	// Deinterlace-Resolver: Item-Streams einmal mit-laden, dann gemäß Param entscheiden
	{
		streams, _ := s.Store.ItemStreams(it.ID)
		it.Streams = streams
	}
	deinterlace := resolveDeinterlace(r.URL.Query().Get("deinterlace"), playback.IsInterlaced(it))
	// LOOKUP, nicht StartOrGet (gefixt 2026-09-13, Regression aus dem
	// "andere Sessions desselben Items stoppen"-Fix in Manager.StartOrGet):
	// ein Segment-Request darf NIE eine neue Session erzeugen. Nach einem
	// Seek/Resume können noch ein paar bereits vom Client in die Warteschlange
	// gestellte, ALTE Segment-Requests (mit dem VORHERIGEN start=) eintreffen,
	// nachdem die alte Session schon (korrekt) gestoppt wurde — mit
	// StartOrGet erzeugte das dort eine neue, ungewollte Session, die dann
	// via der neuen "Sessions desselben Items stoppen"-Logik sofort die
	// gerade erst gestartete ECHTE Session killte. Client fragt dieses
	// Segment gleich danach wieder an → dieselbe neue Session entsteht
	// erneut → killt wieder die echte → Ping-Pong bis zum Timeout (User-
	// Report: Stream-Fehler -16847 "HTTP 500", Log zeigte alternierende
	// Session-Starts/-Stopps im Sekundentakt). Ein 404 auf eine wirklich
	// veraltete Segment-Anfrage ist dagegen harmlos — der Client hat diese
	// Session ohnehin verlassen.
	sess := s.Playback.LookupSession(it.ID, profile, audioIdx, startSec, deinterlace)
	if sess == nil {
		writeError(w, 404, "keine laufende Transcode-Session")
		return
	}
	sess.Touch()
	path := filepath.Join(sess.Dir, seg)
	// Kurze Wartetoleranz, falls ffmpeg das Segment noch nicht fertig
	// geschrieben hat (User-Report 2026-09-15: Stream-Fehler -12938/HTTP 404
	// direkt nach Session-Start, Diagnose zeigte `ffmpeg_laeuft=true,
	// playlist=false, segmente=0` — die Session existiert und arbeitet, nur
	// das erste Segment war noch nicht auf Disk). Ohne das schlägt jede
	// Anfrage, die knapp vor dem ersten geschriebenen Segment eintrifft,
	// sofort fehl statt kurz zu warten — betrifft vor allem VAAPI-Encoder-
	// Anlaufzeit bei 4K-Quellen. Bricht NICHT die 2026-09-13-Fixes: es wird
	// keine neue Session erzeugt/gestoppt, nur auf eine Datei einer bereits
	// laufenden gewartet. Für eine wirklich veraltete Session (Datei kommt
	// nie) bleibt es beim harmlosen 404 nach Ablauf der Frist.
	if !waitForSegmentFile(r.Context(), path, 4*time.Second) {
		writeError(w, 404, "Segment noch nicht bereit")
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	// Einmal geschriebene Segmente ändern sich für die Lebensdauer der Session
	// nicht mehr — der Browser darf sie cachen. max-age deckt sich mit
	// playback.sessionIdleTimeout (30 min, siehe gcLoop in playback/ffmpeg.go,
	// war früher 5 min): ein Segment kann in diesem Fenster garantiert noch
	// von derselben Session bedient werden. Ermöglicht das Pause-Prefetching
	// in player.js — der Browser lädt bereits transkodierte, aber noch nicht
	// abgespielte Segmente während der Pause vor, ohne bei Resume erneut über
	// die Leitung zu müssen.
	w.Header().Set("Cache-Control", "private, max-age=1800")
	http.ServeFile(w, r, path)
}

// waitForSegmentFile pollt, ob path existiert — bis zu timeout, alle 100ms.
// Bricht sofort ab, wenn der Client die Anfrage abbricht (Context-Cancel).
func waitForSegmentFile(ctx context.Context, path string, timeout time.Duration) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
	}
	return false
}

func isSafeSegment(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' {
			return false
		}
	}
	return true
}
