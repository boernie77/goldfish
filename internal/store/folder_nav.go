package store

import (
	"database/sql"
	"strings"
)

// SetFolderDrilldown speichert, ob ein Ordner als Navigations-Ebene agiert
// (true = Unterordner werden als Kacheln angezeigt statt alles flach zu listen).
func (s *Store) SetFolderDrilldown(libraryID int64, folder string, enabled bool) error {
	if enabled {
		_, err := s.db.Exec(
			`INSERT INTO folder_nav(library_id, folder, drilldown) VALUES(?, ?, 1)
			 ON CONFLICT(library_id, folder) DO UPDATE SET drilldown = 1`,
			libraryID, folder)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM folder_nav WHERE library_id = ? AND folder = ?`,
		libraryID, folder)
	return err
}

// FolderDrilldown liest den aktuellen Wert (false = default / flach).
func (s *Store) FolderDrilldown(libraryID int64, folder string) (bool, error) {
	var d int
	err := s.db.QueryRow(
		`SELECT drilldown FROM folder_nav WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&d)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return d == 1, err
}

// drilldownMap lädt alle drilldown-Flags dieser Library in ein Set.
func (s *Store) drilldownMap(libraryID int64) (map[string]bool, error) {
	rows, err := s.db.Query(
		`SELECT folder FROM folder_nav WHERE library_id = ? AND drilldown = 1`,
		libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	m := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		m[f] = true
	}
	return m, rows.Err()
}

// SubfoldersAt listet die direkten Unterordner eines Parent-Pfads auf.
// parent="" entspricht der Library-Root (wie TopLevelFolders).
// Die zurückgegebenen Folder haben jeweils den vollen Pfad im Name-Feld
// (z.B. "a/Siterips/KanalX") plus Drilldown-Flag.
func (s *Store) SubfoldersAt(libraryID int64, parent string) ([]Folder, error) {
	return s.SubfoldersAtFiltered(libraryID, parent, false)
}

// SubfoldersAtFiltered wie SubfoldersAt, listet bei onlyUnmatched=true nur
// Folder, in denen mindestens ein Item mit metadata_id IS NULL liegt. Greift
// der Filter „Ohne TMDB-Zuordnung" auf TV-Libraries: komplett ungemappte
// Serien-Folder werden so im Library-Root sichtbar.
func (s *Store) SubfoldersAtFiltered(libraryID int64, parent string, onlyUnmatched bool) ([]Folder, error) {
	if parent == "" {
		folders, err := s.topLevelFolders(libraryID, onlyUnmatched)
		if err != nil {
			return nil, err
		}
		return s.annotateDrilldown(libraryID, folders)
	}
	prefix := parent + "/"
	q := `SELECT rel_path, id, has_thumb, added_at FROM items
		 WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\'`
	if onlyUnmatched {
		q += ` AND metadata_id IS NULL`
	}
	rows, err := s.db.Query(q, libraryID, escapeLike(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type acc struct {
		count   int
		thumbID int64
		addedAt string
	}
	byFolder := map[string]*acc{}
	order := []string{}
	for rows.Next() {
		var rp string
		var id int64
		var hasThumb int
		var addedAt sql.NullString
		if err := rows.Scan(&rp, &id, &hasThumb, &addedAt); err != nil {
			return nil, err
		}
		rest := rp[len(prefix):]
		slash := strings.Index(rest, "/")
		if slash <= 0 {
			// Direkt im parent-Ordner (kein Unterordner) → hier ignorieren,
			// diese Items kommen später über den ListItems-Aufruf.
			continue
		}
		segment := rest[:slash]
		a, ok := byFolder[segment]
		if !ok {
			a = &acc{}
			byFolder[segment] = a
			order = append(order, segment)
		}
		a.count++
		if hasThumb == 1 && a.thumbID == 0 {
			a.thumbID = id
		}
		// MAX(added_at) über alle Items dieses Unterordners — Strings sind hier
		// bereits ISO-8601-artig sortierbar (SQLite DATETIME-Default-Format).
		if addedAt.Valid && addedAt.String > a.addedAt {
			a.addedAt = addedAt.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sortieren und Folder-Objekte bauen
	strSort(order)
	out := make([]Folder, 0, len(order))
	for _, seg := range order {
		a := byFolder[seg]
		out = append(out, Folder{
			Name:        prefix + seg, // voller Pfad
			ItemCount:   a.count,
			ThumbItemID: a.thumbID,
			AddedAt:     a.addedAt,
		})
	}
	return s.annotateDrilldown(libraryID, out)
}

// annotateDrilldown ergänzt Drilldown-Flag und optional Folder-Metadata.
func (s *Store) annotateDrilldown(libraryID int64, folders []Folder) ([]Folder, error) {
	dd, err := s.drilldownMap(libraryID)
	if err != nil {
		return folders, err
	}
	for i := range folders {
		folders[i].Drilldown = dd[folders[i].Name]
	}
	return folders, nil
}

// strSort sortiert case-insensitive (wie SQL COLLATE NOCASE).
func strSort(xs []string) {
	// einfache insertion sort für kleine Listen
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && strings.ToLower(xs[j]) < strings.ToLower(xs[j-1]); j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
