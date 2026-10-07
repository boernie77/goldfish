package store

import "strings"

// SetDeleteProtection schaltet den Löschschutz für eine Bibliothek
// (folder == "") oder einen Ordner (rel_path, rekursiv) ein/aus.
func (s *Store) SetDeleteProtection(libraryID int64, folder string, enabled bool) error {
	if enabled {
		_, err := s.db.Exec(
			`INSERT OR IGNORE INTO delete_protection(library_id, folder) VALUES(?, ?)`,
			libraryID, folder)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM delete_protection WHERE library_id = ? AND folder = ?`,
		libraryID, folder)
	return err
}

// DeleteProtectionFolders liefert alle geschützten Ordner einer Bibliothek
// ("" = ganze Bibliothek).
func (s *Store) DeleteProtectionFolders(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT folder FROM delete_protection WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteProtected sagt, ob relPath (Datei oder Ordner) in einer geschützten
// Bibliothek bzw. unter einem geschützten Ordner liegt. Gibt zusätzlich den
// treffenden Schutz-Ordner zurück ("" = ganze Bibliothek).
func (s *Store) DeleteProtected(libraryID int64, relPath string) (bool, string, error) {
	folders, err := s.DeleteProtectionFolders(libraryID)
	if err != nil {
		return false, "", err
	}
	for _, f := range folders {
		if f == "" || relPath == f || strings.HasPrefix(relPath, f+"/") {
			return true, f, nil
		}
	}
	return false, "", nil
}

// AllDeleteProtections liefert alle Schutz-Einträge je Bibliothek-ID.
func (s *Store) AllDeleteProtections() (map[int64][]string, error) {
	rows, err := s.db.Query(`SELECT library_id, folder FROM delete_protection ORDER BY library_id, folder`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var f string
		if err := rows.Scan(&id, &f); err != nil {
			return nil, err
		}
		out[id] = append(out[id], f)
	}
	return out, rows.Err()
}
