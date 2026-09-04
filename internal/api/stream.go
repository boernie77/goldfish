package api

import (
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
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "playback", "play", it.Title)
	}

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
		"deinterlace":       deinterlaceParam, // gewählter Modus (auto/on/off)
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
	sess, err := s.Playback.StartOrGet(it.ID, it.Path, profile, audioIdx, startSec, deinterlace, it.VideoCodec == "")
	if err != nil {
		writeError(w, 500, "ffmpeg start: "+err.Error())
		return
	}
	if err := sess.WaitForPlaylist(20 * time.Second); err != nil {
		writeError(w, 500, "playlist: "+err.Error())
		return
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
	// keine Segment-Requests mehr rein. Nach 5 Min Pause killt der GC-Loop
	// dann die ffmpeg-Session; beim Fortsetzen spielt der Client noch den
	// Restbuffer, dann 404 auf ein nicht mehr existierendes Segment →
	// Wiedergabe bricht ab. Mit Touch hier bleibt eine offene Player-Session
	// beliebig lange am Leben (GC greift erst wieder, wenn der Player-Dialog
	// geschlossen wird und stopTranscodeProgress() den Poll-Timer stoppt).
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
	sess, err := s.Playback.StartOrGet(it.ID, it.Path, profile, audioIdx, startSec, deinterlace, it.VideoCodec == "")
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	sess.Touch()
	path := filepath.Join(sess.Dir, seg)
	w.Header().Set("Content-Type", "video/mp2t")
	// Einmal geschriebene Segmente ändern sich für die Lebensdauer der Session
	// nicht mehr — der Browser darf sie cachen. max-age deckt sich mit dem
	// GC-Idle-Timeout (5 min, siehe gcLoop in playback/ffmpeg.go): ein Segment
	// kann in diesem Fenster garantiert noch von derselben Session bedient
	// werden. Ermöglicht das Pause-Prefetching in player.js — der Browser
	// lädt bereits transkodierte, aber noch nicht abgespielte Segmente
	// während der Pause vor, ohne bei Resume erneut über die Leitung zu müssen.
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, path)
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
