package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// bitmapSubCodecs: Bild-basierte Untertitel lassen sich nicht nach WebVTT
// (Text) konvertieren — ffmpegs `-c:s webvtt` scheitert. Der Client muss dann
// KI-Untertitel (Whisper) erzeugen oder Direct Play mit PGS-fähigem Player.
var bitmapSubCodecs = map[string]bool{
	"hdmv_pgs_subtitle": true, "pgssub": true, "pgs": true,
	"dvd_subtitle": true, "dvdsub": true,
	"dvb_subtitle": true, "dvbsub": true,
	"xsub": true,
}

// probeSubtitleCodec liest den codec_name eines einzelnen Streams per ffprobe.
func probeSubtitleCodec(path, idx string) string {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", idx,
		"-show_entries", "stream=codec_name", "-of", "json", path).Output()
	if err != nil {
		return ""
	}
	var parsed struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &parsed) != nil || len(parsed.Streams) == 0 {
		return ""
	}
	return strings.ToLower(parsed.Streams[0].CodecName)
}

// subtitleVTT extrahiert (cached) einen Subtitle-Stream eines Items als WebVTT.
// URL: /api/subtitle/{id}/{streamIdx}.vtt
func (s *Server) subtitleVTT(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	idxStr := chi.URLParam(r, "idx")
	if _, err := strconv.Atoi(idxStr); err != nil {
		writeError(w, 400, "ungültiger Stream-Index")
		return
	}

	// Cache unter $SubsDir/{itemID}/{idx}.vtt
	cacheDir := filepath.Join(s.SubsDir, strconv.FormatInt(id, 10))
	_ = os.MkdirAll(cacheDir, 0o755)
	out := filepath.Join(cacheDir, idxStr+".vtt")

	if _, err := os.Stat(out); err != nil {
		// Bild-Untertitel (PGS/VOBSUB/DVB) können nicht nach WebVTT — sofort
		// mit klarem Status ablehnen statt ffmpeg minutenlang scheitern zu lassen.
		if codec := probeSubtitleCodec(it.Path, idxStr); bitmapSubCodecs[codec] {
			log.Printf("[subtitle] item %d stream %s: Bild-Untertitel (%s) — kann nicht nach WebVTT", id, idxStr, codec)
			writeError(w, 415, "Bild-Untertitel ("+codec+") kann nicht als Text ausgeliefert werden")
			return
		}
		// Erstmalig extrahieren
		cmd := exec.CommandContext(r.Context(), "ffmpeg",
			"-hide_banner", "-loglevel", "error", "-y",
			"-i", it.Path,
			"-map", "0:"+idxStr,
			"-c:s", "webvtt",
			out,
		)
		if err := cmd.Run(); err != nil {
			// Aufräumen + Fehler
			_ = os.Remove(out)
			log.Printf("[subtitle] item %d stream %s: WebVTT-Extraktion fehlgeschlagen: %v", id, idxStr, err)
			writeError(w, 500, "Untertitel-Extraktion fehlgeschlagen: "+err.Error())
			return
		}
	}
	f, err := os.Open(out)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, out, info.ModTime(), f)
}
