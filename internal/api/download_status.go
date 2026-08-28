package api

import (
	"net/http"
	"path/filepath"

	"github.com/boernie77/goldfish/internal/download"
)

// downloadCompatStatus: GET /api/download/{id}/compat-status
//
// Liefert den Fortschritt der server-seitigen Formatanpassung (`?compat=1`),
// damit die App „wird vorbereitet … X %" anzeigen kann, statt minutenlang auf
// einen stummen Download zu warten. Ist eine Anpassung nötig und läuft noch
// nicht (und ist nicht gecacht), wird sie hier angestoßen — der Client muss also
// nur pollen: `state` durchläuft `preparing` → `ready` (oder `error`), bei
// `ready` dann `GET /api/download/{id}?compat=1` holen (dann sofort aus dem Cache).
func (s *Server) downloadCompatStatus(w http.ResponseWriter, r *http.Request) {
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

	cacheDir := filepath.Join(s.ConfigDir, "cache", "downloads")
	p := download.Status(cacheDir, it.ID, it.Path, it.Container, it.VideoCodec, it.AudioCodec)
	if p.State == "idle" {
		p = download.StartPrep(s.HW, cacheDir, it.ID, it.Path, it.Container, it.VideoCodec, it.AudioCodec)
	}
	writeJSON(w, 200, p)
}
