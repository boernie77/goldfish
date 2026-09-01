package api

import (
	"encoding/json"
	"net/http"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

func (s *Server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	pls, err := s.Store.ListPlaylistsForUser(me.ID, me.IsAdmin)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if pls == nil {
		pls = []model.Playlist{}
	}
	writeJSON(w, 200, pls)
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	name, err := store.NormalizePlaylistName(body.Name)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	id, err := s.Store.CreatePlaylist(me.ID, name)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	pl, _ := s.Store.GetPlaylist(id)
	writeJSON(w, 201, pl)
}

// requirePlaylistAccess prüft, ob der aktuelle User auf eine Playlist zugreifen
// darf. STRIKT pro Besitzer — auch Admins dürfen NICHT auf die Playlist eines
// ANDEREN Users zugreifen (Playlists sind private Kuratierung, kein
// Bibliotheks-Zugriffsrecht; ein früherer Admin-Bypass hier war ein echter
// Cross-User-Datenleck, User-Report 2026-09-02). Einzige Ausnahme:
// eigentümerlose Legacy-Playlists (owner==0, aus der Zeit vor Playlists-pro-
// User) bleiben admin-only verwaltbar — das ist verwaistes Altsystem-Datum
// ohne fremden Besitzer, kein Cross-User-Zugriff.
func (s *Server) requirePlaylistAccess(w http.ResponseWriter, r *http.Request, plID int64) bool {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return false
	}
	owner, err := s.Store.PlaylistOwner(plID)
	if err != nil {
		writeError(w, 500, err.Error())
		return false
	}
	if owner == 0 {
		if !me.IsAdmin {
			writeError(w, 403, "kein Zugriff")
			return false
		}
		return true
	}
	if owner != me.ID {
		writeError(w, 403, "kein Zugriff auf diese Playlist")
		return false
	}
	return true
}

func (s *Server) renamePlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requirePlaylistAccess(w, r, id) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	name, err := store.NormalizePlaylistName(body.Name)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.Store.RenamePlaylist(id, name); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requirePlaylistAccess(w, r, id) {
		return
	}
	if err := s.Store.DeletePlaylist(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listPlaylistItems(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requirePlaylistAccess(w, r, id) {
		return
	}
	var userID int64
	if me := currentUser(r); me != nil {
		userID = me.ID
	}
	q := r.URL.Query()
	items, err := s.Store.PlaylistItems(id, q.Get("sort"), q.Get("dir"), userID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []model.Item{}
	}
	writeJSON(w, 200, items)
}

func (s *Server) addPlaylistItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requirePlaylistAccess(w, r, id) {
		return
	}
	var body struct {
		ItemID int64 `json:"itemId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.ItemID == 0 {
		writeError(w, 400, "itemId fehlt")
		return
	}
	added, err := s.Store.AddToPlaylist(id, body.ItemID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// `added=false` bedeutet: Item war schon in der Playlist. Kein Fehler, der
	// Client zeigt dann einen „Ist bereits drin"-Hinweis.
	writeJSON(w, 200, map[string]any{"added": added})
}

func (s *Server) removePlaylistItem(w http.ResponseWriter, r *http.Request) {
	pid, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige playlist id")
		return
	}
	if !s.requirePlaylistAccess(w, r, pid) {
		return
	}
	iid, err := pathInt(r, "itemId")
	if err != nil {
		writeError(w, 400, "ungültige item id")
		return
	}
	if err := s.Store.RemoveFromPlaylist(pid, iid); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) reorderPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requirePlaylistAccess(w, r, id) {
		return
	}
	var body struct {
		ItemIDs []int64 `json:"itemIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.ReorderPlaylist(id, body.ItemIDs); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// playlistsForItem: in welchen EIGENEN Playlists steckt dieses Item schon?
// (für die Checkmarks im "Zu Playlist hinzufügen"-Dialog)
func (s *Server) playlistsForItem(w http.ResponseWriter, r *http.Request) {
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
	ids, err := s.Store.PlaylistsForItem(id, me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if ids == nil {
		ids = []int64{}
	}
	writeJSON(w, 200, ids)
}
