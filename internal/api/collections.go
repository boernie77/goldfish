package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// listCollections liefert alle Sammlungen, die mindestens einen Film in der
// Bibliothek haben.
func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	var userID int64
	var isAdmin bool
	var maxAgeRating int
	if me := currentUser(r); me != nil {
		userID = me.ID
		isAdmin = me.IsAdmin
		// FSK-Fix 2026-09-02 (gleicher Tag wie der ACL-Fund): Sammlungen müssen
		// dieselbe Altersgrenze respektieren wie das normale Grid — sonst sieht
		// ein eingeschränkter Account FSK-18-Filme über den Sammlungs-Umweg.
		if !me.IsAdmin && me.MaxAgeRating != nil {
			maxAgeRating = *me.MaxAgeRating
		}
	}
	cs, err := s.Store.ListCollections(userID, isAdmin, maxAgeRating)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Ordner-Sammlungen (negative IDs, kind="folder") alphabetisch einsortieren.
	fcs, err := s.Store.ListFolderCollections(userID, isAdmin, maxAgeRating)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if len(fcs) > 0 {
		cs = append(cs, fcs...)
		sort.SliceStable(cs, func(i, j int) bool { return strings.ToLower(cs[i].Name) < strings.ToLower(cs[j].Name) })
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
	var maxAgeRating int
	if !me.IsAdmin && me.MaxAgeRating != nil {
		maxAgeRating = *me.MaxAgeRating
	}
	if id < 0 {
		// Ordner-Sammlung: für Clients ohne Ordner-Ansicht alle Dateien des
		// Ordners flach (rekursiv), gleiche ACL/FSK-Regeln wie das Grid.
		fc, err := s.Store.GetFolderCollection(-id)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if fc == nil {
			writeError(w, 404, "Sammlung nicht gefunden")
			return
		}
		if !s.requireLibAccess(w, r, fc.LibraryID) {
			return
		}
		items, err := s.Store.ListItems(store.ItemFilter{LibraryID: fc.LibraryID, Folder: fc.Folder, Sort: "filename",
			UserID: me.ID, IsAdmin: me.IsAdmin, MaxAgeRating: maxAgeRating})
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if items == nil {
			items = []model.Item{}
		}
		writeJSON(w, 200, items)
		return
	}
	parts, err := s.Store.GetCollectionParts(id, me.ID, me.IsAdmin, maxAgeRating)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if len(parts) > 0 {
		writeJSON(w, 200, parts)
		return
	}
	// Fallback für alte Sammlungen, deren parts noch nicht gefetcht sind.
	items, err := s.Store.ListItemsInCollection(id, me.ID, me.IsAdmin, maxAgeRating)
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
	if id < 0 {
		s.serveFolderCollectionPoster(w, r, -id)
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

// serveFolderCollectionPoster: Cover einer Ordner-Sammlung = Poster der
// Serien-Zuordnung des Ordners (folder_metadata), sonst Platzhalter.
func (s *Server) serveFolderCollectionPoster(w http.ResponseWriter, r *http.Request, fcID int64) {
	fc, err := s.Store.GetFolderCollection(fcID)
	if err != nil || fc == nil || s.Enrich == nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	metaID, _ := s.Store.GetFolderMetadataID(fc.LibraryID, fc.Folder)
	meta, _ := s.Store.GetMetadata(metaID)
	p := ""
	if meta != nil {
		p = s.Enrich.EnsurePosterCached(r.Context(), meta)
	}
	f, err := os.Open(p)
	if p == "" || err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, filepath.Base(p), info.ModTime(), f)
}

// getFolderCollection (für jeden User): ist Library+Ordner als Sammlung
// angelegt? Query: libraryId, folder. Antwort {"enabled": bool, "id": -n}.
func (s *Server) getFolderCollection(w http.ResponseWriter, r *http.Request) {
	libID, _ := strconv.ParseInt(r.URL.Query().Get("libraryId"), 10, 64)
	folder := r.URL.Query().Get("folder")
	fc, err := s.Store.FindFolderCollection(libID, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if fc == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "id": -fc.ID, "name": fc.Name})
}

// setFolderCollection (Admin): Ordner als Sammlung an-/abmelden.
// Body: {"libraryId": n, "folder": "Tatort", "enabled": true, "name": "Tatort"}
// name optional, Default = Ordnername (letzter Pfadteil).
func (s *Server) setFolderCollection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LibraryID int64  `json:"libraryId"`
		Folder    string `json:"folder"`
		Enabled   bool   `json:"enabled"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	body.Folder = strings.Trim(body.Folder, "/")
	if body.LibraryID <= 0 || body.Folder == "" {
		writeError(w, 400, "libraryId und folder erforderlich")
		return
	}
	if !body.Enabled {
		if err := s.Store.DeleteFolderCollection(body.LibraryID, body.Folder); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		w.WriteHeader(204)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = body.Folder[strings.LastIndex(body.Folder, "/")+1:]
	}
	id, err := s.Store.SetFolderCollection(body.LibraryID, body.Folder, name)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "id": -id, "name": name})
}

// getTMDBSeason (Admin, seit 1.4.59): reicht die TMDB-Folgenliste einer
// Staffel durch (Titel, Nummer, Ausstrahlung). Werkzeug für Sammel-
// Zuordnungen, bei denen der Dateiname nicht zu TMDB passt — erster Einsatz:
// Tatort (Dateien nach fortlaufender Nummer, TMDB nach Jahr/Staffel). Der
// Abgleich läuft beim Admin im Browser, zugeordnet wird danach über den
// normalen Weg POST /items/{id}/metadata.
func (s *Server) getTMDBSeason(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	tid, err1 := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	season, err2 := strconv.Atoi(chi.URLParam(r, "season"))
	if err1 != nil || err2 != nil || tid <= 0 || season < 0 {
		writeError(w, 400, "ungültige tmdbId/season")
		return
	}
	sea, err := s.Enrich.Client().GetSeason(r.Context(), tid, season)
	if err != nil {
		writeError(w, 502, "TMDB: "+err.Error())
		return
	}
	writeJSON(w, 200, sea)
}
