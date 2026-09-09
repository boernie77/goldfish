package store

import (
	"strings"
)

// SetScanExcludedFolder aktiviert/deaktiviert den Scan-Ausschluss für einen
// Ordner. Zeilen-Existenz = ausgeschlossen (analog trickplay_folders/
// intro_skip_folders). folder="" schließt die GESAMTE Bibliothek von jedem
// Scan aus — bewusst erlaubt (z.B. eine externe Platte, die man temporär
// komplett pausieren will, ohne die Bibliothek zu löschen).
func (s *Store) SetScanExcludedFolder(libraryID int64, folder string, excluded bool) error {
	if excluded {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO scan_excluded_folders (library_id, folder) VALUES (?, ?)`, libraryID, folder)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM scan_excluded_folders WHERE library_id = ? AND folder = ?`, libraryID, folder)
	return err
}

// ListScanExcludedFolders liefert alle ausgeschlossenen Ordner einer Bibliothek.
func (s *Store) ListScanExcludedFolders(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT folder FROM scan_excluded_folders WHERE library_id = ? ORDER BY folder`, libraryID)
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

// IsRelPathExcluded prüft, ob ein rel_path unter einem der ausgeschlossenen
// Ordner liegt (exakter Treffer ODER echtes Unterverzeichnis — "Foo2" darf
// nicht fälschlich unter ausgeschlossenem "Foo" fallen, daher der "/"-Check
// statt reinem strings.HasPrefix).
func IsRelPathExcluded(relPath string, excluded []string) bool {
	relPath = strings.Trim(relPath, "/")
	for _, ex := range excluded {
		ex = strings.Trim(ex, "/")
		if ex == "" || relPath == ex || strings.HasPrefix(relPath, ex+"/") {
			return true
		}
	}
	return false
}

// ItemPathsUnderFolders liefert die absoluten Disk-Pfade (items.path) aller
// bereits in der DB stehenden Items einer Bibliothek, deren rel_path unter
// einem der übergebenen Ordner liegt. Wird vom Scanner genutzt, um diese
// Pfade VOR dem Orphan-Cleanup ins "keep"-Set aufzunehmen — ausgeschlossene
// Ordner werden nie gewalkt, ohne das würden ihre Items beim nächsten Scan
// fälschlich als verwaist gelöscht.
func (s *Store) ItemPathsUnderFolders(libraryID int64, folders []string) ([]string, error) {
	if len(folders) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT path, rel_path FROM items WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var path, relPath string
		if err := rows.Scan(&path, &relPath); err != nil {
			return nil, err
		}
		if IsRelPathExcluded(relPath, folders) {
			out = append(out, path)
		}
	}
	return out, rows.Err()
}
