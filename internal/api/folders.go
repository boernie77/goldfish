package api

import (
	"encoding/json"
	"net/http"

	"github.com/boernie77/goldfish/internal/store"
)

// setFolderDrilldown aktiviert/deaktiviert die Navigations-Zwischenebene für einen Ordner.
// Body: {"folder": "a/Siterips", "drilldown": true}
func (s *Server) setFolderDrilldown(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder    string `json:"folder"`
		Drilldown bool   `json:"drilldown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.Folder == "" {
		writeError(w, 400, "folder fehlt")
		return
	}
	if err := s.Store.SetFolderDrilldown(id, body.Folder, body.Drilldown); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// listGenres liefert alle verfügbaren Genre-Werte einer Bibliothek — für den
// Genre-Picker (User-Wunsch 2026-09-06). Gescoped auf genau diese Library
// (Store.ListGenresForLibrary wählt intern die passende Quelle je nach
// kind), damit z.B. eine Musik-Bibliothek nur Musik-Genres zeigt und keine
// Filme-Genres aus einer anderen Bibliothek hineinmischt.
func (s *Server) listGenres(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, id) {
		return
	}
	genres, err := s.Store.ListGenresForLibrary(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if genres == nil {
		genres = []string{}
	}
	writeJSON(w, 200, map[string]any{"genres": genres})
}

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, id) {
		return
	}
	parent := r.URL.Query().Get("parent")
	onlyUnmatched := r.URL.Query().Get("match") == "unmatched"
	folders, err := s.Store.SubfoldersAtFiltered(id, parent, onlyUnmatched)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if folders == nil {
		folders = []store.Folder{}
	}
	writeJSON(w, 200, folders)
}
