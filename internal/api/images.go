// images.go -- Bilder in Bibliotheken mit images_enabled: Schalter, Listen,
// Original und Vorschau. Zugriff läuft wie bei Videos über die Bibliotheks-ACL
// (requireLibAccess), damit die Mehrbenutzer-Trennung gilt.
package api

import (
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/boernie77/goldfish/internal/scanner"
	"github.com/boernie77/goldfish/internal/store"
)

// setLibraryImagesEnabled schaltet das Einlesen von Bildern pro Bibliothek.
// Admin-only. Body: {"enabled": bool}
func (s *Server) setLibraryImagesEnabled(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetLibraryImagesEnabled(id, body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// listLibraryImages: GET /api/libraries/{id}/images?folder=<angezeigter Pfad>
// Liefert die Bilder, die direkt in diesem Ordner liegen ("" = Bibliotheks-Root).
func (s *Server) listLibraryImages(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, id) {
		return
	}
	lib, err := s.Store.GetLibrary(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if lib == nil || !lib.ImagesEnabled {
		writeJSON(w, 200, []any{})
		return
	}
	aliases, _ := s.Store.ListFolderAliases(id)
	folder := store.ResolveAlias(aliases, strings.Trim(r.URL.Query().Get("folder"), "/"))
	imgs, err := s.Store.ListImagesInFolder(id, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, imgs)
}

// imageForRequest lädt das Bild und prüft Zugriff + Schalter.
func (s *Server) imageForRequest(w http.ResponseWriter, r *http.Request) (path string, ok bool) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return "", false
	}
	im, err := s.Store.GetImage(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return "", false
	}
	if im == nil {
		writeError(w, 404, "nicht gefunden")
		return "", false
	}
	if !s.requireLibAccess(w, r, im.LibraryID) {
		return "", false
	}
	lib, err := s.Store.GetLibrary(im.LibraryID)
	if err != nil || lib == nil || !lib.ImagesEnabled {
		writeError(w, 404, "nicht gefunden")
		return "", false
	}
	return im.Path, true
}

// getImageFile: GET /api/images/{id}/file — Originaldatei (Range/ETag via ServeContent).
func (s *Server) getImageFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.imageForRequest(w, r)
	if !ok {
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeError(w, 404, "Datei nicht gefunden")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(p))); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}

// getImageThumb: GET /api/images/{id}/thumb — Vorschau (480 px), bei Bedarf erzeugt.
func (s *Server) getImageThumb(w http.ResponseWriter, r *http.Request) {
	p, ok := s.imageForRequest(w, r)
	if !ok {
		return
	}
	out := scanner.ImageThumbPath(filepath.Join(s.ConfigDir, "thumbs"), p)
	if err := scanner.MakeImageThumb(r.Context(), p, out); err != nil {
		// Vorschau nicht möglich (z. B. defektes Bild): Original als Fallback.
		s.getImageFile(w, r)
		return
	}
	f, err := os.Open(out)
	if err != nil {
		writeError(w, 404, "Vorschau nicht gefunden")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "thumb.jpg", st.ModTime(), f)
}

// mergeImageFolders hängt Ordner, in denen (auch tiefer) Bilder liegen, an die
// Ordnerliste: vorhandene Video-Ordner bekommen imageCount/thumbImageId, reine
// Bild-Ordner werden ergänzt. Nur auf Wunsch (?images=1) — die nativen Apps
// kennen Bilder noch nicht und sollen keine leeren Ordner sehen.
func (s *Server) mergeImageFolders(libID int64, realParent string, folders []store.Folder) []store.Folder {
	lib, err := s.Store.GetLibrary(libID)
	if err != nil || lib == nil || !lib.ImagesEnabled {
		return folders
	}
	imgFolders, err := s.Store.ImageSubfolders(libID, realParent)
	if err != nil || len(imgFolders) == 0 {
		return folders
	}
	idx := map[string]int{}
	for i, f := range folders {
		idx[f.Name] = i
	}
	for _, ig := range imgFolders {
		if i, ok := idx[ig.Name]; ok {
			folders[i].ImageCount = ig.ImageCount
			folders[i].ThumbImageID = ig.ThumbImageID
			continue
		}
		folders = append(folders, store.Folder{
			Name: ig.Name, ImageCount: ig.ImageCount, ThumbImageID: ig.ThumbImageID, AddedAt: ig.AddedAt,
			Drilldown: true, // reine Bild-Ordner: Klick zeigt Unterordner + Bilder dieser Ebene
		})
	}
	return folders
}
