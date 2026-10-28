package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

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
	// Anzeige-Verschiebungen (folder_alias): angezeigter Pfad → echter Pfad.
	aliases, _ := s.Store.ListFolderAliases(id)
	realParent := store.ResolveAlias(aliases, parent)
	folders, err := s.Store.SubfoldersAtFiltered(id, realParent, onlyUnmatched)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if folders == nil {
		folders = []store.Folder{}
	}
	if len(aliases) > 0 {
		folders = s.applyFolderAliases(id, aliases, parent, realParent, onlyUnmatched, folders)
	}
	writeJSON(w, 200, folders)
}

// applyFolderAliases blendet verschobene Ordner am Originalplatz aus, hängt sie
// unter ihrem Anzeige-Ordner ein und schreibt Pfade beim Blick in einen
// Alias-Ordner auf den angezeigten Pfad um.
func (s *Server) applyFolderAliases(libID int64, aliases []store.FolderAlias, parent, realParent string, onlyUnmatched bool, folders []store.Folder) []store.Folder {
	moved := map[string]bool{}
	for _, a := range aliases {
		moved[a.Folder] = true
	}
	out := make([]store.Folder, 0, len(folders))
	for _, f := range folders {
		if moved[f.Name] {
			continue // erscheint nur noch unter dem Anzeige-Ordner
		}
		if realParent != parent && strings.HasPrefix(f.Name, realParent+"/") {
			f.Name = parent + f.Name[len(realParent):]
		}
		out = append(out, f)
	}
	for _, a := range aliases {
		if a.Parent != parent {
			continue
		}
		dir := ""
		if i := strings.LastIndex(a.Folder, "/"); i >= 0 {
			dir = a.Folder[:i]
		}
		siblings, err := s.Store.SubfoldersAtFiltered(libID, dir, onlyUnmatched)
		if err != nil {
			continue
		}
		for _, f := range siblings {
			if f.Name == a.Folder {
				f.Name = a.Virtual()
				out = append(out, f)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// setFolderAlias hängt einen Ordner NUR in der Anzeige unter einen anderen Ordner
// derselben Bibliothek (Dateien bleiben unverändert). parent leer = zurück an den
// Originalplatz. Body: {"folder": "DaphneDare", "parent": "Siterips"}
func (s *Server) setFolderAlias(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder string `json:"folder"`
		Parent string `json:"parent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	body.Folder = strings.Trim(body.Folder, "/")
	body.Parent = strings.Trim(body.Parent, "/")
	aliases, _ := s.Store.ListFolderAliases(id)
	// Der Client schickt den angezeigten Pfad; ein bereits verschobener Ordner
	// wird über seinen angezeigten Pfad auf den echten zurückgeführt.
	body.Folder = store.ResolveAlias(aliases, body.Folder)
	if body.Folder == "" {
		writeError(w, 400, "folder fehlt")
		return
	}
	if body.Parent == "" || body.Parent == dirOf(body.Folder) {
		if err := s.Store.DeleteFolderAlias(id, body.Folder); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		w.WriteHeader(204)
		return
	}
	real := store.ResolveAlias(aliases, body.Parent)
	if body.Parent == body.Folder || strings.HasPrefix(body.Parent+"/", body.Folder+"/") || strings.HasPrefix(real+"/", body.Folder+"/") {
		writeError(w, 400, "ein Ordner kann nicht in sich selbst angezeigt werden")
		return
	}
	if err := s.Store.SetFolderAlias(id, body.Folder, body.Parent); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
