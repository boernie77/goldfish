package api

import (
	"net/http"
	"os"
	"strconv"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/go-chi/chi/v5"
)

// getMetadataCast liefert die Cast-Liste einer Metadata. Für Episoden wird
// zusätzlich die Haupt-Cast-Liste des Parent-Shows vorangestellt.
func (s *Server) getMetadataCast(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	meta, err := s.Store.GetMetadata(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if meta == nil {
		writeError(w, 404, "Metadata nicht gefunden")
		return
	}

	out := []model.CastMember{}
	// Bei Episoden: Haupt-Cast aus Parent-Show holen.
	if meta.TMDBType == "episode" && meta.ParentID > 0 {
		parent, err := s.Store.GetMetadataCast(meta.ParentID)
		if err == nil {
			out = append(out, parent...)
		}
	}
	own, err := s.Store.GetMetadataCast(meta.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Für Episoden Haupt-Rolle aus Show + Gäste aus Episode; sonst nur own.
	if meta.TMDBType == "episode" {
		for _, c := range own {
			if c.Role == "guest" {
				out = append(out, c)
			}
		}
	} else {
		out = own
	}

	// Duplikate vermeiden (selbe Person einmal als main, einmal als guest)
	seen := map[int64]bool{}
	uniq := make([]model.CastMember, 0, len(out))
	for _, c := range out {
		if seen[c.PersonID] {
			continue
		}
		seen[c.PersonID] = true
		uniq = append(uniq, c)
	}
	writeJSON(w, 200, uniq)
}

// getPerson liefert Grunddaten zu einer Person (TMDB-ID).
func (s *Server) getPerson(w http.ResponseWriter, r *http.Request) {
	tmdbID, err := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	if err != nil {
		writeError(w, 400, "ungültige tmdbId")
		return
	}
	p, err := s.Store.GetPersonByTMDB(tmdbID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if p == nil {
		writeError(w, 404, "Person nicht bekannt")
		return
	}
	writeJSON(w, 200, p)
}

// getPersonProfile liefert das gecachte Profilbild (oder Placeholder).
func (s *Server) getPersonProfile(w http.ResponseWriter, r *http.Request) {
	tmdbID, err := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	p, err := s.Store.GetPersonByTMDB(tmdbID)
	if err != nil || p == nil || p.ProfilePath == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	path := s.Enrich.EnsureProfileCached(r.Context(), tmdbID, p.ProfilePath)
	if path == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, path, info.ModTime(), f)
}
