// images.go -- Bilddateien in Bibliotheken mit images_enabled einlesen.
// Läuft als eigener, schlanker Durchgang nach dem Video-Scan (siehe run()):
// Bilder landen in der Tabelle `images`, NICHT in `items`.
package scanner

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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

// thumbSem begrenzt gleichzeitige Vorschau-Erzeugungen.
var thumbSem = make(chan struct{}, 2)

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
	// Höchstens zwei ffmpeg-Aufrufe gleichzeitig: ein Grid mit hunderten Fotos
	// fragt viele Vorschauen auf einmal an, der Host (Stabilitätsgrenze!) darf
	// davon nicht überrollt werden.
	select {
	case thumbSem <- struct{}{}:
		defer func() { <-thumbSem }()
	case <-ctx.Done():
		return ctx.Err()
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

var videoStemExts = []string{".mp4", ".mkv", ".mov", ".avi", ".wmv", ".flv", ".m4v", ".ts", ".iso"}

// Typische Namen von Begleitbildern (Poster/Cover/Vorschau), keine Galeriebilder.
var sidecarBase = map[string]bool{
	"poster": true, "folder": true, "cover": true, "fanart": true, "backdrop": true, "thumb": true,
	"thumbnail": true, "banner": true, "logo": true, "clearart": true, "landscape": true, "disc": true,
	"background": true, "front": true, "back": true, "movie": true, "show": true, "keyart": true,
}

// isSidecarImage erkennt Bilder, die zu einem Video gehören: "film.mp4.jpg",
// "film.jpg" neben "film.mkv", sowie Poster/Folder/Cover-Dateien und
// "season01-poster.jpg". Sonst würden tausende Video-Vorschaubilder der
// Bibliothek als "Bilder" erscheinen. `siblings` = kleingeschriebene Dateinamen
// des Ordners.
func isSidecarImage(path string, siblings map[string]bool) bool {
	name := strings.ToLower(filepath.Base(path))
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if sidecarBase[stem] || strings.HasSuffix(stem, "-poster") || strings.HasSuffix(stem, "-thumb") ||
		strings.HasSuffix(stem, "-fanart") || strings.HasSuffix(stem, "-landscape") {
		return true
	}
	for _, ve := range videoStemExts {
		// "film.mp4.jpg": der Stamm endet selbst auf eine Video-Endung.
		if strings.HasSuffix(stem, ve) {
			return true
		}
		// "film.jpg" neben "film.mkv"
		if siblings[stem+ve] {
			return true
		}
	}
	return false
}

// scanImages: Bilder der Bibliothek einlesen und verwaiste entfernen.
// folder="" = ganze Bibliothek, sonst nur dieser Unterordner (wie beim Video-Scan).
func (sc *Scanner) scanImages(ctx context.Context, lib model.Library, paths []string, force bool, folder string, excluded []string) error {
	keep := map[string]struct{}{}
	added := 0
	// Dateinamen je Ordner einmal lesen (für die Begleitbild-Erkennung).
	dirCache := map[string]map[string]bool{}
	siblingsOf := func(dir string) map[string]bool {
		if m, ok := dirCache[dir]; ok {
			return m
		}
		m := map[string]bool{}
		if es, err := os.ReadDir(dir); err == nil {
			for _, e := range es {
				if !e.IsDir() {
					m[strings.ToLower(e.Name())] = true
				}
			}
		}
		dirCache[dir] = m
		return m
	}
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
			if isSidecarImage(path, siblingsOf(filepath.Dir(path))) {
				return nil
			}
			keep[path] = struct{}{}
			if mt, exists, _ := sc.store.ImageModTime(path); !force && exists && mt.Equal(info.ModTime()) {
				return nil
			}
			// Bewusst nur Datei-Metadaten: kein ffprobe/ffmpeg pro Bild (bei
			// zehntausenden Fotos dauerte das Stunden). Vorschauen entstehen beim
			// ersten Ansehen (api/images.go), Maße bleiben 0.
			im := &model.Image{
				LibraryID: lib.ID,
				Path:      path,
				RelPath:   filepath.ToSlash(rel),
				Title:     strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
				SizeBytes: info.Size(),
				ModTime:   info.ModTime(),
			}
			if err := sc.store.UpsertImage(im); err != nil {
				log.Printf("[scan] image upsert %s: %v", path, err)
				return nil
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
