package api

import (
	"net/http"
	"path/filepath"

	"github.com/boernie77/goldfish/internal/download"
	"github.com/boernie77/goldfish/internal/playback"
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

	// "Optimierte Downloads" — derselbe optionale `&profile=`-Parameter wie
	// beim eigentlichen Download-Endpoint, siehe `delete_download.go
	// downloadItem`. Muss hier UND dort identisch aufgelöst werden, sonst
	// würde die Status-Abfrage einen anderen Cache-Pfad prüfen als der
	// spätere Download tatsächlich anfordert.
	profile := playback.ProfileByID(r.URL.Query().Get("profile"))
	cacheDir := filepath.Join(s.ConfigDir, "cache", "downloads")
	p := download.Status(cacheDir, it.ID, it.Path, it.Container, it.VideoCodec, it.AudioCodec, profile, it.Height, it.BitrateKbps)
	if p.State == "idle" {
		p = download.StartPrep(s.HW, cacheDir, it.ID, it.Path, it.Container, it.VideoCodec, it.AudioCodec, profile, it.Height, it.BitrateKbps)
	}
	writeJSON(w, 200, p)
}
