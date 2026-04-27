package api

import (
	"net/http"
	"os"
)

func (s *Server) getThumb(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil || !it.HasThumb {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	f, err := os.Open(it.ThumbPath)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, it.ThumbPath, info.ModTime(), f)
}
