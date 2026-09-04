package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// listAlbums liefert alle Alben einer Musik-Bibliothek (Kachel-Ansicht,
// zusätzlich zur normalen Ordner-Browse-Navigation über /api/items).
func (s *Server) listAlbums(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, libID) {
		return
	}
	me := currentUser(r)
	var userID int64
	if me != nil {
		userID = me.ID
	}
	albums, err := s.Store.ListMusicAlbums(libID, userID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, albums)
}

// getAlbum liefert Album-Detail + Tracks (sortiert nach Track-Nummer).
func (s *Server) getAlbum(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	me := currentUser(r)
	var userID int64
	if me != nil {
		userID = me.ID
	}
	album, err := s.Store.GetMusicAlbum(id, userID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if album == nil {
		writeError(w, 404, "Album nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, album.LibraryID) {
		return
	}
	tracks, err := s.Store.ListMusicAlbumTracks(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"album":  album,
		"tracks": tracks,
	})
}

// setAlbumFavorite togglet den Album-Favoriten-Status für den aktuellen User
// (user_music_album_favorites) — Pendant zu PUT /api/items/{id}/favorite für
// einzelne Titel.
func (s *Server) setAlbumFavorite(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	album, err := s.Store.GetMusicAlbum(id, me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if album == nil {
		writeError(w, 404, "Album nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, album.LibraryID) {
		return
	}
	var body struct {
		Favorite bool `json:"favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiger Body")
		return
	}
	if err := s.Store.SetMusicAlbumFavorite(me.ID, id, body.Favorite); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"favorite": body.Favorite})
}

// getAlbumCover liefert das gecachte Album-Cover (embedded oder Cover-Art-
// Archive, siehe scanner.extractAlbumCovers / MusicWorker) oder einen
// Placeholder, falls (noch) keins vorhanden ist. Gleiches Handler-Muster wie
// getPoster — Datei liegt im selben Cache-Verzeichnis (Server.PosterDir),
// Namenskonvention "album_<id>.jpg" (siehe scanner.extractAlbumCovers).
func (s *Server) getAlbumCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	if s.PosterDir == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	path := filepath.Join(s.PosterDir, fmt.Sprintf("album_%d.jpg", id))
	f, err := os.Open(path)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, path, info.ModTime(), f)
}
