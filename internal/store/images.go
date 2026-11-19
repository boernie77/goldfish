// images.go -- Bilddateien in Bibliotheken mit images_enabled (Tabelle `images`).
// Bewusst getrennt von `items`: so sehen Zufall, Statistik, Trickplay, Untertitel,
// Dubletten usw. nie Bilder.
package store

import (
	"database/sql"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// ImageFolder ist ein direkter Unterordner mit Bildern (analog Folder für Videos).
type ImageFolder struct {
	Name         string `json:"name"` // voller relativer Pfad
	ImageCount   int    `json:"imageCount"`
	ThumbImageID int64  `json:"thumbImageId"`
	AddedAt      string `json:"addedAt,omitempty"`
}

// ImageModTime liefert die gespeicherte mtime eines Bildes (für inkrementelle Scans).
func (s *Store) ImageModTime(path string) (time.Time, bool, error) {
	var mt sql.NullTime
	err := s.db.QueryRow(`SELECT mod_time FROM images WHERE path = ?`, path).Scan(&mt)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return mt.Time, true, nil
}

// UpsertImage legt ein Bild an oder aktualisiert es (Schlüssel: path).
func (s *Store) UpsertImage(im *model.Image) error {
	_, err := s.db.Exec(`INSERT INTO images (library_id, path, rel_path, title, width, height, size_bytes, mod_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET library_id=excluded.library_id, rel_path=excluded.rel_path,
			title=excluded.title, width=excluded.width, height=excluded.height,
			size_bytes=excluded.size_bytes, mod_time=excluded.mod_time`,
		im.LibraryID, im.Path, im.RelPath, im.Title, im.Width, im.Height, im.SizeBytes, im.ModTime)
	return err
}

// GetImage liefert ein Bild samt Disk-Pfad (Path ist nicht in der JSON-Ausgabe).
func (s *Store) GetImage(id int64) (*model.Image, error) {
	var im model.Image
	var mt, added sql.NullTime
	err := s.db.QueryRow(`SELECT id, library_id, path, rel_path, title, width, height, size_bytes, mod_time, added_at
		FROM images WHERE id = ?`, id).
		Scan(&im.ID, &im.LibraryID, &im.Path, &im.RelPath, &im.Title, &im.Width, &im.Height, &im.SizeBytes, &mt, &added)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	im.ModTime, im.AddedAt = mt.Time, added.Time
	return &im, nil
}

// imageDir liefert den Ordner eines rel_path ("" = Bibliotheks-Root).
func imageDir(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

// ListImagesInFolder liefert die Bilder, die DIREKT in folder liegen ("" = Root),
// natürlich sortiert nach Dateiname.
func (s *Store) ListImagesInFolder(libraryID int64, folder string) ([]model.Image, error) {
	rows, err := s.db.Query(`SELECT id, library_id, rel_path, title, width, height, size_bytes, mod_time, added_at
		FROM images WHERE library_id = ? ORDER BY title COLLATE NATSORT, id`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Image{}
	for rows.Next() {
		var im model.Image
		var mt, added sql.NullTime
		if err := rows.Scan(&im.ID, &im.LibraryID, &im.RelPath, &im.Title, &im.Width, &im.Height, &im.SizeBytes, &mt, &added); err != nil {
			return nil, err
		}
		if imageDir(im.RelPath) != folder {
			continue
		}
		im.ModTime, im.AddedAt = mt.Time, added.Time
		out = append(out, im)
	}
	return out, rows.Err()
}

// ImageSubfolders listet die direkten Unterordner von parent ("" = Root), in denen
// (auch tiefer) Bilder liegen — für Ordner, die KEIN Video enthalten und deshalb
// von der Item-Ordnerliste nicht erfasst werden.
func (s *Store) ImageSubfolders(libraryID int64, parent string) ([]ImageFolder, error) {
	prefix := ""
	if parent != "" {
		prefix = parent + "/"
	}
	rows, err := s.db.Query(`SELECT id, rel_path, added_at FROM images WHERE library_id = ? ORDER BY title COLLATE NATSORT, id`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byName := map[string]*ImageFolder{}
	var order []string
	for rows.Next() {
		var id int64
		var rel string
		var added sql.NullString
		if err := rows.Scan(&id, &rel, &added); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(rel, prefix) {
			continue
		}
		rest := rel[len(prefix):]
		slash := strings.Index(rest, "/")
		if slash <= 0 {
			continue // liegt direkt in parent
		}
		name := prefix + rest[:slash]
		f, ok := byName[name]
		if !ok {
			f = &ImageFolder{Name: name, ThumbImageID: id}
			byName[name] = f
			order = append(order, name)
		}
		f.ImageCount++
		if added.Valid && added.String > f.AddedAt {
			f.AddedAt = added.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	strSort(order)
	out := make([]ImageFolder, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// DeleteImagesNotInSet entfernt Bilder, die nicht mehr auf der Platte liegen.
// folder="" = ganze Bibliothek, sonst nur unterhalb von folder. Pfade unter
// `spare` (Scan-Ausschlüsse, werden nie gewalkt) bleiben erhalten.
func (s *Store) DeleteImagesNotInSet(libraryID int64, folder string, keep map[string]struct{}, spare []string) (int, error) {
	rows, err := s.db.Query(`SELECT id, path, rel_path FROM images WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, err
	}
	var del []int64
	for rows.Next() {
		var id int64
		var p, rel string
		if err := rows.Scan(&id, &p, &rel); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if folder != "" && rel != folder && !strings.HasPrefix(rel, folder+"/") {
			continue
		}
		if _, ok := keep[p]; ok {
			continue
		}
		if len(spare) > 0 && IsRelPathExcluded(rel, spare) {
			continue
		}
		del = append(del, id)
	}
	_ = rows.Close()
	for _, id := range del {
		if _, err := s.db.Exec(`DELETE FROM images WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(del), nil
}

// CountImages zählt die Bilder einer Bibliothek.
func (s *Store) CountImages(libraryID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM images WHERE library_id = ?`, libraryID).Scan(&n)
	return n, err
}

// ImagesUnderFolder liefert alle Bilder unterhalb von folder (rekursiv), samt Disk-Pfad.
func (s *Store) ImagesUnderFolder(libraryID int64, folder string) ([]model.Image, error) {
	rows, err := s.db.Query(`SELECT id, library_id, path, rel_path, title, size_bytes FROM images
		WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\' ORDER BY rel_path`, libraryID, escapeLike(folder)+"/%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Image
	for rows.Next() {
		var im model.Image
		if err := rows.Scan(&im.ID, &im.LibraryID, &im.Path, &im.RelPath, &im.Title, &im.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

// MoveImage schreibt Pfad/Bibliothek eines Bildes nach einem Verschieben auf der Platte um.
func (s *Store) MoveImage(id, libraryID int64, newPath, newRel string) error {
	_, err := s.db.Exec(`UPDATE images SET library_id = ?, path = ?, rel_path = ? WHERE id = ?`, libraryID, newPath, newRel, id)
	return err
}

// DeleteImage entfernt den DB-Eintrag eines Bildes.
func (s *Store) DeleteImage(id int64) error {
	_, err := s.db.Exec(`DELETE FROM images WHERE id = ?`, id)
	return err
}
