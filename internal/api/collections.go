package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// listCollections liefert alle Sammlungen, die mindestens einen Film in der
// Bibliothek haben.
func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	var userID int64
	var isAdmin bool
	if me := currentUser(r); me != nil {
		userID = me.ID
		isAdmin = me.IsAdmin
	}
	cs, err := s.Store.ListCollections(userID, isAdmin)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cs == nil {
		writeJSON(w, 200, []any{})
		return
	}
	writeJSON(w, 200, cs)
}

// collectionItems liefert alle Parts einer Sammlung — sowohl vorhandene als
// auch fehlende Filme (als Placeholder mit `owned:false`).
// Fallback: wenn Parts noch nicht gefetcht, alte Item-Liste zurück.
func (s *Server) collectionItems(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	parts, err := s.Store.GetCollectionParts(id, me.ID, me.IsAdmin)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if len(parts) > 0 {
		writeJSON(w, 200, parts)
		return
	}
	// Fallback für alte Sammlungen, deren parts noch nicht gefetcht sind.
	items, err := s.Store.ListItemsInCollection(id, me.ID, me.IsAdmin)
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

// hideCollectionPart markiert einen Part als vom aktuellen User ausgeblendet.
// Z.B. Home Alone 3 in der Allein-zu-Haus-Sammlung, weil es keinen Kevin gibt.
func (s *Server) hideCollectionPart(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	cid, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige collection id")
		return
	}
	tid, err := strconv.ParseInt(chi.URLParam(r, "tmdbMovieId"), 10, 64)
	if err != nil {
		writeError(w, 400, "ungültige tmdbMovieId")
		return
	}
	if err := s.Store.HideCollectionPart(me.ID, cid, tid); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) unhideCollectionPart(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	cid, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige collection id")
		return
	}
	tid, err := strconv.ParseInt(chi.URLParam(r, "tmdbMovieId"), 10, 64)
	if err != nil {
		writeError(w, 400, "ungültige tmdbMovieId")
		return
	}
	if err := s.Store.UnhideCollectionPart(me.ID, cid, tid); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// getTMDBMovieDetail liefert TMDB-Metadaten + Cast für einen Film, den der
// User NICHT besitzt (z.B. fehlende Parts in einer Sammlung). Genutzt vom
// Frontend, um auch für „Fehlt"-Kacheln einen Detail-Dialog zu bauen.
func (s *Server) getTMDBMovieDetail(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	tid, err := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	if err != nil {
		writeError(w, 400, "ungültige tmdbId")
		return
	}
	client := s.Enrich.Client()
	ctx := r.Context()
	m, err := client.GetMovie(ctx, tid)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	credits, _ := client.GetMovieCredits(ctx, tid) // Cast optional
	out := map[string]any{
		"tmdbId":        m.ID,
		"title":         m.Title,
		"originalTitle": m.OriginalTitle,
		"year":          yearFromStr(m.ReleaseDate),
		"releaseDate":   m.ReleaseDate,
		"overview":      m.Overview,
		"rating":        m.VoteAverage,
		"runtimeMin":    m.Runtime,
		"posterPath":    m.PosterPath,
		"backdropPath":  m.BackdropPath,
		"imdbId":        m.IMDBID,
	}
	cast := make([]map[string]any, 0, 15)
	for i, c := range credits {
		if i >= 15 {
			break
		}
		cast = append(cast, map[string]any{
			"tmdbId":      c.ID,
			"name":        c.Name,
			"character":   c.Character,
			"profilePath": c.ProfilePath,
		})
	}
	out["cast"] = cast
	writeJSON(w, 200, out)
}

// getCollectionPoster serviert das gecachte Collection-Poster. Fehlt es noch im
// Cache, wird es synchron von TMDB geladen — sonst müsste der Browser pro Kachel
// erst einen Redirect zum Placeholder folgen.
func (s *Server) getCollectionPoster(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	c, err := s.Store.GetCollection(id)
	if err != nil || c == nil || c.PosterPath == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	p := s.Enrich.EnsureCollectionPosterCached(r.Context(), id, c.PosterPath)
	if p == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, filepath.Base(p), info.ModTime(), f)
}
