package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// cancelTrickplay stoppt den aktuell laufenden Trickplay-Durchlauf.
func (s *Server) cancelTrickplay(w http.ResponseWriter, _ *http.Request) {
	if s.Trickplay == nil {
		writeError(w, 503, "Trickplay nicht initialisiert")
		return
	}
	s.Trickplay.Cancel()
	w.WriteHeader(204)
}

// retryFailedTrickplay setzt alle Items mit status=failed zurück auf "", damit der
// Worker sie erneut probiert (nach verbessertem Filter / neuem ffmpeg-Config).
func (s *Server) retryFailedTrickplay(w http.ResponseWriter, r *http.Request) {
	if s.Trickplay == nil {
		writeError(w, 503, "Trickplay nicht initialisiert")
		return
	}
	n, err := s.Store.ResetFailedTrickplayStatus()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.Trickplay.Trigger()
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "job", "trickplay_retry_failed", fmt.Sprintf("%d Fehler zurückgesetzt", n))
	}
	writeJSON(w, 200, map[string]any{"reset": n})
}

// retryItemTrickplay setzt ein einzelnes Item zurück (egal welcher Status) und
// stösst den Worker an. Wird vom Trickplay-Manager-Dialog aus einem ↻-Button
// pro Zeile genutzt — damit der User nicht „Alle Fehler erneut versuchen"
// drücken muss, wenn er nur eine bestimmte Datei wieder testen will.
func (s *Server) retryItemTrickplay(w http.ResponseWriter, r *http.Request) {
	if s.Trickplay == nil {
		writeError(w, 503, "Trickplay nicht initialisiert")
		return
	}
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
	if err := s.Store.SetItemTrickplayError(id, "", ""); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.Trickplay.Trigger()
	w.WriteHeader(204)
}

// deleteAllTrickplay entfernt alle generierten Trickplay-Dateien + resettet Status.
func (s *Server) deleteAllTrickplay(w http.ResponseWriter, r *http.Request) {
	if s.Trickplay == nil {
		writeError(w, 503, "Trickplay nicht initialisiert")
		return
	}
	// Laufenden Job stoppen, damit nicht nebenbei neu generiert wird.
	s.Trickplay.Cancel()
	if err := s.Trickplay.DeleteAll(); err != nil {
		writeError(w, 500, "delete files: "+err.Error())
		return
	}
	if err := s.Store.ResetAllTrickplayStatus(); err != nil {
		writeError(w, 500, "reset db: "+err.Error())
		return
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "job", "trickplay_delete_all", "")
	}
	w.WriteHeader(204)
}

// trickplayLog liefert alle Items nach Trickplay-Status (done|failed|pending) für
// den Admin-Log-Viewer. Ohne status-Param: done + failed (pending ausgeblendet).
func (s *Server) trickplayLog(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	allowed := map[string]bool{"done": true, "failed": true, "pending": true}
	if !allowed[status] {
		writeError(w, 400, "status=done|failed|pending erforderlich")
		return
	}
	items, err := s.Store.ListItemsByTrickplayStatus(status)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if items == nil {
		writeJSON(w, 200, []any{})
		return
	}
	writeJSON(w, 200, items)
}

// trickplayWorkerStatus liefert den globalen Zustand des Trickplay-Workers.
func (s *Server) trickplayWorkerStatus(w http.ResponseWriter, _ *http.Request) {
	if s.Trickplay == nil {
		writeJSON(w, 200, map[string]any{"running": false})
		return
	}
	writeJSON(w, 200, s.Trickplay.Status())
}

// listTrickplayFolders liefert pro Bibliothek die aktivierten Ordner + Status.
func (s *Server) listTrickplayFolders(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	folders, err := s.Store.ListTrickplayFolders(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type entry struct {
		Folder  string `json:"folder"`
		Total   int    `json:"total"`
		Done    int    `json:"done"`
		Pending int    `json:"pending"`
		Failed  int    `json:"failed"`
	}
	out := []entry{}
	for _, f := range folders {
		st, err := s.Store.FolderTrickplayStatus(libID, f)
		if err != nil {
			continue
		}
		out = append(out, entry{
			Folder: f, Total: st.Total, Done: st.Done,
			Pending: st.Pending, Failed: st.Failed,
		})
	}
	writeJSON(w, 200, out)
}

// setTrickplayFolder aktiviert oder deaktiviert Trickplay für einen Ordner.
func (s *Server) setTrickplayFolder(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder  string `json:"folder"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	// folder == "" ist zulässig und bedeutet "ganze Bibliothek".
	if err := s.Store.SetTrickplayFolder(libID, body.Folder, body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if body.Enabled && s.Trickplay != nil {
		s.Trickplay.Trigger()
	}
	w.WriteHeader(204)
}

// folderTrickplayStatus liefert nur den Status eines einzelnen Ordners (für Polling).
func (s *Server) folderTrickplayStatus(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	// folder == "" ist zulässig: bedeutet "ganze Bibliothek"
	folder := r.URL.Query().Get("folder")
	enabled, _ := s.Store.TrickplayFolderEnabled(libID, folder)
	st, err := s.Store.FolderTrickplayStatus(libID, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"folder":  folder,
		"enabled": enabled,
		"total":   st.Total,
		"done":    st.Done,
		"pending": st.Pending,
		"failed":  st.Failed,
	})
}

// trickplayVTT serviert das WebVTT-Manifest eines Items.
func (s *Server) trickplayVTT(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if s.Trickplay == nil {
		writeError(w, 503, "Trickplay nicht initialisiert")
		return
	}
	path := s.Trickplay.VTTFile(id)
	if path == "" {
		writeError(w, 404, "keine Trickplay-Daten")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "text/vtt")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, path, info.ModTime(), f)
}

// trickplaySprite serviert das Sprite-Sheet.
func (s *Server) trickplaySprite(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if s.Trickplay == nil {
		writeError(w, 503, "Trickplay nicht initialisiert")
		return
	}
	path := s.Trickplay.SpriteFile(id)
	if path == "" {
		writeError(w, 404, "keine Trickplay-Daten")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, path, info.ModTime(), f)
}
