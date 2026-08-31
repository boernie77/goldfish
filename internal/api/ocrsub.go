package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/boernie77/goldfish/internal/ocrsub"
)

// serveOCRSubtitle: GET /api/ocr-subtitle/{id}/{lang}.vtt
func (s *Server) serveOCRSubtitle(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lang := chi.URLParam(r, "lang")
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	vttPath := ocrsub.VTTPath(s.ConfigDir, id, lang)
	f, err := os.Open(vttPath)
	if err != nil {
		writeError(w, 404, "OCR-Untertitel nicht gefunden")
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, vttPath, info.ModTime(), f)
}

// ocrSubStatus: Live-Zustand + Zähler für das Admin-Dialog.
func (s *Server) ocrSubStatus(w http.ResponseWriter, _ *http.Request) {
	if s.OCRSub == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "toolMissing": true})
		return
	}
	writeJSON(w, 200, s.OCRSub.Status())
}

func (s *Server) ocrSubSetEnabled(w http.ResponseWriter, r *http.Request) {
	if s.OCRSub == nil {
		writeError(w, 503, "OCR-Untertitel nicht verfügbar")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.OCRSub.SetEnabled(body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": body.Enabled})
}

// ocrSubListFolders: alle Bibliotheken + ob sie für OCR aktiviert sind
// (folder="" pro Library). Der User wählt hier "Filme"/"Serien" etc.
func (s *Server) ocrSubListFolders(w http.ResponseWriter, _ *http.Request) {
	libs, err := s.Store.ListLibraries()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	active, _ := s.Store.ListOCRSubFolders()
	activeLib := map[int64]bool{}
	for _, f := range active {
		if f.Folder == "" {
			activeLib[f.LibraryID] = true
		}
	}
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		out = append(out, map[string]any{
			"libraryId": l.ID, "name": l.Name, "kind": l.Kind,
			"enabled": activeLib[l.ID],
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) ocrSubSetFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LibraryID int64  `json:"libraryId"`
		Folder    string `json:"folder"`
		Enabled   bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.LibraryID == 0 {
		writeError(w, 400, "libraryId fehlt")
		return
	}
	if err := s.Store.SetOCRSubFolder(body.LibraryID, body.Folder, body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Aktivierung reiht sofort den Backlog dieser Bibliothek ein.
	if body.Enabled && s.OCRSub != nil {
		go func() { _, _ = s.OCRSub.EnqueueBacklogAndRun() }()
	}
	w.WriteHeader(204)
}

func (s *Server) ocrSubLog(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "done"
	}
	rows, err := s.Store.ListOCRSubJobs(status)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rows)
}

// ocrSubRunAll: "alle jetzt erzeugen" — Backlog aller aktivierten Bibliotheken
// einreihen + Worker triggern.
func (s *Server) ocrSubRunAll(w http.ResponseWriter, _ *http.Request) {
	if s.OCRSub == nil {
		writeError(w, 503, "OCR-Untertitel nicht verfügbar")
		return
	}
	n, err := s.OCRSub.EnqueueBacklogAndRun()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"queued": n})
}

func (s *Server) ocrSubRetryFailed(w http.ResponseWriter, _ *http.Request) {
	// Erst den Müll aus der ersten (zu breiten) Enqueue-Runde entfernen:
	// failed-Jobs für Items ohne PGS-Stream (VOBSUB/DVB) werden nie klappen.
	purged, _ := s.Store.PurgeNonPGSOCRSubJobs()
	n, err := s.Store.RetryFailedOCRSubJobs()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if s.OCRSub != nil {
		s.OCRSub.Trigger()
	}
	writeJSON(w, 200, map[string]any{"retried": n, "purged": purged})
}

func (s *Server) ocrSubRetryItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if err := s.Store.ForceRetryOCRSubJob(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if s.OCRSub != nil {
		s.OCRSub.Trigger()
	}
	w.WriteHeader(204)
}
