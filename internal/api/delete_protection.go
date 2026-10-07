package api

import (
	"encoding/json"
	"net/http"
)

// getDeleteProtection liefert den Löschschutz-Status für Bibliothek bzw.
// Ordner (?folder=, leer = ganze Bibliothek): "protected" = wirksam (auch
// geerbt), "own" = genau dieser Bereich hat den Schalter, "from" = der
// schützende Bereich ("" = Bibliothek).
func (s *Server) getDeleteProtection(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	folder := r.URL.Query().Get("folder")
	prot, from, err := s.Store.DeleteProtected(id, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	folders, err := s.Store.DeleteProtectionFolders(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	own := false
	for _, f := range folders {
		if f == folder {
			own = true
		}
	}
	writeJSON(w, 200, map[string]any{"protected": prot, "own": own, "from": from})
}

// setDeleteProtection schaltet den Löschschutz ein/aus. Admin-only.
// Body: {"folder": "", "enabled": bool}
func (s *Server) setDeleteProtection(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
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
	if err := s.Store.SetDeleteProtection(id, body.Folder, body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if me := currentUser(r); me != nil {
		state := "aus"
		if body.Enabled {
			state = "an"
		}
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "delete_protection",
			"Löschschutz "+state+": "+body.Folder, deviceLabel(r))
	}
	w.WriteHeader(204)
}
