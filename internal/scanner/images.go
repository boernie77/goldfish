// images.go -- Bilddateien in Bibliotheken mit images_enabled einlesen.
// Läuft als eigener, schlanker Durchgang nach dem Video-Scan (siehe run()):
// Bilder landen in der Tabelle `images`, NICHT in `items`.
package scanner

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// ImageExt: unterstützte Bildformate (alles, was Browser direkt anzeigen können;
// HEIC bewusst nicht).
var ImageExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

// IsImagePath prüft die Dateiendung.
func IsImagePath(p string) bool { return ImageExt[strings.ToLower(filepath.Ext(p))] }

// ImageThumbPath ist der Cache-Pfad der Vorschau (480 px breit) zu einer Bilddatei.
func ImageThumbPath(thumbDir, srcPath string) string {
	sum := sha1.Sum([]byte("img:" + srcPath))
	return filepath.Join(thumbDir, "img_"+hex.EncodeToString(sum[:])+".jpg")
}

// MakeImageThumb erzeugt die Vorschau per ffmpeg (EXIF-Drehung wird von ffmpeg
// automatisch angewendet). Existiert sie schon und ist neuer als das Original,
// passiert nichts.
func MakeImageThumb(ctx context.Context, srcPath, outPath string) error {
	if si, err := os.Stat(srcPath); err == nil {
		if oi, err := os.Stat(outPath); err == nil && oi.Size() > 0 && oi.ModTime().After(si.ModTime()) {
			return nil
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := outPath + ".tmp.jpg"
	cmd := exec.CommandContext(cctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-i", srcPath, "-frames:v", "1", "-vf", "scale='min(480,iw)':-2", "-q:v", "5", tmp)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, outPath)
}

// probeImageSize liest Breite/Höhe per ffprobe (funktioniert für alle Formate).
func probeImageSize(ctx context.Context, path string) (int, int) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ffprobe", "-v", "quiet", "-print_format", "json",
		"-select_streams", "v:0", "-show_entries", "stream=width,height", path).Output()
	if err != nil {
		return 0, 0
	}
	var r struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &r) != nil || len(r.Streams) == 0 {
		return 0, 0
	}
	return r.Streams[0].Width, r.Streams[0].Height
}

// scanImages: Bilder der Bibliothek einlesen und verwaiste entfernen.
// folder="" = ganze Bibliothek, sonst nur dieser Unterordner (wie beim Video-Scan).
func (sc *Scanner) scanImages(ctx context.Context, lib model.Library, paths []string, force bool, folder string, excluded []string) error {
	keep := map[string]struct{}{}
	added := 0
	for _, root := range paths {
		startDir := root
		if folder != "" {
			scoped := filepath.Join(root, folder)
			if st, err := os.Stat(scoped); err != nil || !st.IsDir() {
				continue
			}
			startDir = scoped
		}
		err := filepath.WalkDir(startDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // einzelne Fehler überspringen, Walk fortsetzen
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil //nolint:nilerr
			}
			if d.IsDir() {
				if len(excluded) > 0 && rel != "." && store.IsRelPathExcluded(rel, excluded) {
					return filepath.SkipDir
				}
				return nil
			}
			if !IsImagePath(path) || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil //nolint:nilerr
			}
			keep[path] = struct{}{}
			if mt, exists, _ := sc.store.ImageModTime(path); !force && exists && mt.Equal(info.ModTime()) {
				return nil
			}
			w, h := probeImageSize(ctx, path)
			im := &model.Image{
				LibraryID: lib.ID,
				Path:      path,
				RelPath:   filepath.ToSlash(rel),
				Title:     strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
				Width:     w,
				Height:    h,
				SizeBytes: info.Size(),
				ModTime:   info.ModTime(),
			}
			if err := sc.store.UpsertImage(im); err != nil {
				log.Printf("[scan] image upsert %s: %v", path, err)
				return nil
			}
			if sc.thumbDir != "" {
				_ = MakeImageThumb(ctx, path, ImageThumbPath(sc.thumbDir, path))
			}
			added++
			return nil
		})
		if err != nil {
			return err
		}
	}
	removed, err := sc.store.DeleteImagesNotInSet(lib.ID, folder, keep, excluded)
	if err != nil {
		return err
	}
	if added > 0 || removed > 0 {
		log.Printf("[scan] lib=%d folder=%q Bilder: %d neu/aktualisiert, %d entfernt", lib.ID, folder, added, removed)
	}
	return nil
}
