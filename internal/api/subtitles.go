package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
)

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
