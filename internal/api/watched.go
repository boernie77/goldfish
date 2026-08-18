package api

import (
	"encoding/json"
	"net/http"
)

func (s *Server) setFavorite(w http.ResponseWriter, r *http.Request) {
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
	it, _ := s.Store.GetItem(id)
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	var body struct {
		Favorite bool `json:"favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetFavoriteFor(me.ID, id, body.Favorite); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) setWatched(w http.ResponseWriter, r *http.Request) {
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
	it, _ := s.Store.GetItem(id)
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	var body struct {
		Watched bool `json:"watched"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetWatchedFor(me.ID, id, body.Watched); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.propagateWatchedToLinkedPartners(me.ID, it, body.Watched)
	w.WriteHeader(204)
}

// setResumePos speichert die aktuelle Wiedergabe-Position (vom Player auf Pause/Close).
func (s *Server) setResumePos(w http.ResponseWriter, r *http.Request) {
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
	it, _ := s.Store.GetItem(id)
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	var body struct {
		PositionSec float64 `json:"positionSec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetResumePosition(me.ID, id, body.PositionSec); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// getResumePos liefert die gespeicherte Wiedergabe-Position, 0 wenn keine.
func (s *Server) getResumePos(w http.ResponseWriter, r *http.Request) {
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
	it, _ := s.Store.GetItem(id)
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	pos, err := s.Store.GetResumePosition(me.ID, id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"positionSec": pos})
}

// touchPlayed setzt last_played_at für das Item auf jetzt (pro User). Wird vom
// Client beim Öffnen des Players aufgerufen.
func (s *Server) touchPlayed(w http.ResponseWriter, r *http.Request) {
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
	it, _ := s.Store.GetItem(id)
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if err := s.Store.TouchLastPlayed(me.ID, id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}
