package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// deleteItem löscht ein Item vom Filesystem UND aus der Datenbank. Admin-only.
// Zusätzlich werden Thumbnail, Trickplay-Daten und Subtitle-Cache entfernt.
func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil || !me.IsAdmin {
		writeError(w, 403, "Administrator erforderlich")
		return
	}
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
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}

	// 1. Video-Datei vom Filesystem entfernen.
	if err := os.Remove(it.Path); err != nil && !os.IsNotExist(err) {
		writeError(w, 500, "Datei konnte nicht gelöscht werden: "+err.Error()+
			" (ist /media read-only gemountet? Im Compose-File ':ro' entfernen.)")
		return
	}

	// 2. Thumbnail
	if it.ThumbPath != "" {
		_ = os.Remove(it.ThumbPath)
	}
	// 3. Trickplay-Verzeichnis
	if s.Trickplay != nil {
		s.Trickplay.Delete(id)
	}
	// 4. Subtitle-Cache
	if s.SubsDir != "" {
		_ = os.RemoveAll(filepath.Join(s.SubsDir, strconv.FormatInt(id, 10)))
	}
	// 5. DB-Eintrag — CASCADE räumt verknüpfte Tabellen auf
	if err := s.Store.DeleteItem(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// downloadItem serviert eine Videodatei als Download (Content-Disposition: attachment).
// Alle authentifizierten User mit Library-ACL dürfen herunterladen.
func (s *Server) downloadItem(w http.ResponseWriter, r *http.Request) {
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
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}

	f, err := os.Open(it.Path)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()

	filename := filepath.Base(it.Path)
	// RFC 5987 für UTF-8-Dateinamen (inkl. Umlaute etc.)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
			sanitizeASCII(filename), url.PathEscape(filename)))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, filename, info.ModTime(), f)
}

// sanitizeASCII ersetzt Nicht-ASCII-Zeichen durch '_', um einen sicheren
// fallback-Dateinamen für alte Browser zu geben.
func sanitizeASCII(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '"' || c >= 0x80 {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
