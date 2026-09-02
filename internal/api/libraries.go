package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	// ?all=1 (nur Admin): ungefilterte Gesamtliste — für den ACL-Editor, der
	// jede Bibliothek zum An-/Abhaken zeigen muss, auch wenn der bearbeitende
	// Admin selbst eine eingeschränkte ACL hat.
	if r.URL.Query().Get("all") == "1" && me.IsAdmin {
		all, err := s.Store.ListLibraries()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if all == nil {
			all = []model.Library{}
		}
		writeJSON(w, 200, all)
		return
	}
	libs, err := s.Store.ListLibrariesForUser(me.ID, me.IsAdmin)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if libs == nil {
		libs = []model.Library{}
	}
	writeJSON(w, 200, libs)
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Path = strings.TrimSpace(body.Path)
	if body.Name == "" || body.Path == "" {
		writeError(w, 400, "name und path sind pflicht")
		return
	}
	kind := model.LibraryKind(body.Kind)
	switch kind {
	case model.KindMovies, model.KindTV, model.KindPrivate:
	case "":
		kind = model.KindPrivate
	default:
		writeError(w, 400, "ungültiger kind (movies|tv|private)")
		return
	}
	info, err := os.Stat(body.Path)
	if err != nil {
		writeError(w, 400, "Pfad nicht erreichbar: "+err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, 400, "Pfad ist kein Verzeichnis")
		return
	}
	id, err := s.Store.CreateLibrary(body.Name, body.Path, kind)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	lib, _ := s.Store.GetLibrary(id)
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "library_create", fmt.Sprintf("%q (%s, %s)", body.Name, kind, body.Path))
	}
	writeJSON(w, 201, lib)
}

// --- Multi-Path API ---

func (s *Server) listLibraryPaths(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	paths, err := s.Store.LibraryPaths(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if paths == nil {
		paths = []string{}
	}
	writeJSON(w, 200, paths)
}

func (s *Server) addLibraryPath(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	body.Path = strings.TrimSpace(body.Path)
	if body.Path == "" {
		writeError(w, 400, "path fehlt")
		return
	}
	info, err := os.Stat(body.Path)
	if err != nil {
		writeError(w, 400, "Pfad nicht erreichbar: "+err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, 400, "Pfad ist kein Verzeichnis")
		return
	}
	if err := s.Store.AddLibraryPath(id, body.Path); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) deleteLibraryPath(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, 400, "path fehlt")
		return
	}
	if err := s.Store.DeleteLibraryPath(id, path); err != nil {
		if errors.Is(err, store.ErrLastLibraryPath) {
			writeError(w, 400, err.Error())
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) updateLibraryKind(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	kind := model.LibraryKind(body.Kind)
	switch kind {
	case model.KindMovies, model.KindTV, model.KindPrivate:
	default:
		writeError(w, 400, "ungültiger kind")
		return
	}
	if err := s.Store.UpdateLibraryKind(id, kind); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if s.Enrich != nil {
		s.Enrich.Trigger()
	}
	lib, _ := s.Store.GetLibrary(id)
	writeJSON(w, 200, lib)
}

// libraryStats liefert Gesamtzahl aller Items einer Bibliothek (rekursiv inkl. aller Unterordner).
// Optionaler Query-Parameter `folder=` zählt nur Items in diesem Unterordner.
func (s *Server) libraryStats(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, id) {
		return
	}
	folder := r.URL.Query().Get("folder")
	total, err := s.Store.CountItems(id, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp := map[string]int{"totalItems": total}
	// Auf Library-Root: Anzahl Top-Level-Folder mitliefern. Für TV-Libs ist
	// das die Serien-Anzahl, für Movies-Libs die Genre-/Sub-Folder. Frontend
	// entscheidet pro Library-Kind, wie es das Label formatiert.
	if folder == "" {
		if folders, err := s.Store.TopLevelFolders(id); err == nil {
			resp["folderCount"] = len(folders)
		}
	}
	writeJSON(w, 200, resp)
}

// libraryStatDetail liefert die detaillierte Statistik (Auflösung/Filetyp/
// Länge-Verteilung + Gesamtgröße/-laufzeit) für den "📊 Statistik"-Menüpunkt.
// Scope wie überall sonst: kein `folder` = ganze Bibliothek, sonst rekursiv
// ab diesem Unterordner (Konvention "nur nach unten flach", siehe CLAUDE.md).
// Rein aggregierende SQL-Query im Store — keine zusätzliche Last für den
// normalen Grid-Betrieb, läuft nur wenn der Dialog geöffnet wird.
func (s *Server) libraryStatDetail(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, id) {
		return
	}
	folder := r.URL.Query().Get("folder")
	detail, err := s.Store.GetLibraryStatDetail(id, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, detail)
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lib, _ := s.Store.GetLibrary(id)
	if err := s.Store.DeleteLibrary(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if me := currentUser(r); me != nil && lib != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "library_delete", fmt.Sprintf("%q", lib.Name))
	}
	w.WriteHeader(204)
}
