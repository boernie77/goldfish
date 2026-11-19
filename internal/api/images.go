// images.go -- Bilder in Bibliotheken mit images_enabled: Schalter, Listen,
// Original und Vorschau. Zugriff läuft wie bei Videos über die Bibliotheks-ACL
// (requireLibAccess), damit die Mehrbenutzer-Trennung gilt.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/rename"
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

type imageMovePlan struct {
	Image  *model.Image
	Target string
}

// planImageMoves: Bilder unterhalb der gewählten Ordner mit ihrem Zielordner
// (gleiche Struktur wie planMoves für Videos).
func (s *Server) planImageMoves(libID int64, folders []string, targetFolder string) []imageMovePlan {
	targetFolder = strings.Trim(strings.TrimSpace(targetFolder), "/")
	var out []imageMovePlan
	for _, raw := range folders {
		src := s.Store.ResolveFolderAlias(libID, strings.Trim(raw, "/"))
		if src == "" || targetFolder == src || strings.HasPrefix(targetFolder+"/", src+"/") {
			continue
		}
		ims, err := s.Store.ImagesUnderFolder(libID, src)
		if err != nil {
			continue
		}
		parent := ""
		if i := strings.LastIndex(src, "/"); i >= 0 {
			parent = src[:i+1]
		}
		for i := range ims {
			dir := path.Dir(strings.TrimPrefix(ims[i].RelPath, parent))
			if dir == "." {
				dir = ""
			}
			im := ims[i]
			out = append(out, imageMovePlan{Image: &im, Target: path.Join(targetFolder, dir)})
		}
	}
	return out
}

// executeImageMove verschiebt eine Bilddatei samt DB-Zeile (Vorschau wird neu erzeugt).
func (s *Server) executeImageMove(im *model.Image, targetLib int64, targetFolder, targetRoot string, allowCross bool) (string, int) {
	pseudo := &model.Item{LibraryID: im.LibraryID, Path: im.Path, RelPath: im.RelPath}
	destLib, newDir, targetFolder, msg, code := s.resolveMoveTarget(pseudo, targetLib, targetFolder, targetRoot)
	if code != 0 {
		return msg, code
	}
	base := filepath.Base(im.Path)
	if filepath.Join(newDir, base) == im.Path {
		return "", 0
	}
	resolved := rename.ResolveConflict(newDir, base, im.Path)
	if resolved == "" {
		return "Zielordner voll — alle Namens-Varianten belegt", 409
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return "Zielordner konnte nicht angelegt werden: " + err.Error(), 500
	}
	if err := rename.RenameOnDisk(im.Path, resolved, allowCross); err != nil {
		if errors.Is(err, rename.ErrCrossDevice) {
			return "CROSS_DEVICE", 409
		}
		return "Verschieben auf Disk fehlgeschlagen: " + err.Error(), 500
	}
	if err := s.Store.MoveImage(im.ID, destLib, resolved, filepath.ToSlash(filepath.Join(targetFolder, filepath.Base(resolved)))); err != nil {
		_ = rename.RenameOnDisk(resolved, im.Path, true)
		return "DB-Update fehlgeschlagen: " + err.Error(), 500
	}
	_ = os.Remove(scanner.ImageThumbPath(filepath.Join(s.ConfigDir, "thumbs"), im.Path))
	return "", 0
}

// deleteImageFolders: POST /api/libraries/{id}/images/delete-folders
// Body {folders:[...], includeVideos:bool, dryRun:bool}. Löscht die Bilddateien
// (und mit includeVideos auch alle Videos) unterhalb der Ordner von der Platte,
// danach leer gewordene Ordner. dryRun zählt nur. Admin-only, Löschschutz gilt.
func (s *Server) deleteImageFolders(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, libID) {
		return
	}
	var body struct {
		Folders       []string `json:"folders"`
		IncludeVideos bool     `json:"includeVideos"`
		DryRun        bool     `json:"dryRun"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Folders) == 0 {
		writeError(w, 400, "keine Ordner angegeben")
		return
	}
	deleted, videos, skipped, failed := 0, 0, 0, 0
	for _, raw := range body.Folders {
		folder := s.Store.ResolveFolderAlias(libID, strings.Trim(raw, "/"))
		if folder == "" {
			continue
		}
		ims, err := s.Store.ImagesUnderFolder(libID, folder)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		var vids []store.FolderItem
		if body.IncludeVideos {
			if vids, err = s.Store.ItemsUnderFolder(libID, folder); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
		if body.DryRun {
			deleted += len(ims)
			videos += len(vids)
			continue
		}
		dirs := map[string]bool{}
		var root string
		for _, im := range ims {
			if prot, _, err := s.Store.DeleteProtected(im.LibraryID, im.RelPath); err == nil && prot {
				skipped++
				continue
			}
			if err := os.Remove(im.Path); err != nil && !os.IsNotExist(err) {
				failed++
				continue
			}
			_ = os.Remove(scanner.ImageThumbPath(filepath.Join(s.ConfigDir, "thumbs"), im.Path))
			_ = s.Store.DeleteImage(im.ID)
			deleted++
			dirs[filepath.Dir(im.Path)] = true
			root = strings.TrimSuffix(filepath.ToSlash(im.Path), "/"+filepath.ToSlash(im.RelPath))
		}
		for _, v := range vids {
			it, err := s.Store.GetItem(v.ID)
			if err != nil || it == nil {
				failed++
				continue
			}
			if err := s.deleteItemFilesAndRow(it); err != nil {
				if errors.Is(err, errDeleteProtected) {
					skipped++
				} else {
					failed++
				}
				continue
			}
			videos++
			dirs[filepath.Dir(it.Path)] = true
			root = strings.TrimSuffix(filepath.ToSlash(it.Path), "/"+filepath.ToSlash(it.RelPath))
		}
		if root != "" {
			top := root + "/" + folder
			for d := range dirs {
				d = filepath.ToSlash(d)
				for len(d) >= len(top) && strings.HasPrefix(d, top) {
					if os.Remove(filepath.FromSlash(d)) != nil { // nicht leer → stehen lassen
						break
					}
					d = filepath.ToSlash(filepath.Dir(d))
				}
			}
		}
	}
	if !body.DryRun {
		if me := currentUser(r); me != nil {
			_ = s.Store.LogActivity(me.ID, me.Username, "admin", "folder_delete",
				fmt.Sprintf("%d Videos, %d Bilder gelöscht in %d Ordner(n)", videos, deleted, len(body.Folders)), deviceLabel(r))
		}
	}
	writeJSON(w, 200, map[string]int{"deleted": deleted, "videos": videos, "skipped": skipped, "failed": failed})
}
