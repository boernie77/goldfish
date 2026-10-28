package store

import (
	"sort"
	"strings"
)

// FolderAlias: ein Ordner wird NUR in der Darstellung unter einen anderen
// Ordner derselben Bibliothek gehängt. Die Dateien bleiben physisch und in
// items.rel_path unverändert. Folder = echter Pfad (z. B. "DaphneDare"),
// Parent = Anzeige-Ordner (z. B. "Siterips"; leer = Bibliotheks-Wurzel).
type FolderAlias struct {
	Folder string `json:"folder"`
	Parent string `json:"parent"`
}

// Virtual ist der Pfad, unter dem der Ordner jetzt angezeigt wird.
func (a FolderAlias) Virtual() string {
	base := a.Folder
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if a.Parent == "" {
		return base
	}
	return a.Parent + "/" + base
}

// ListFolderAliases liefert alle Anzeige-Verschiebungen einer Bibliothek.
func (s *Store) ListFolderAliases(libraryID int64) ([]FolderAlias, error) {
	rows, err := s.db.Query(`SELECT folder, parent FROM folder_alias WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FolderAlias
	for rows.Next() {
		var a FolderAlias
		if err := rows.Scan(&a.Folder, &a.Parent); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetFolderAlias legt die Anzeige-Verschiebung an bzw. ersetzt sie.
func (s *Store) SetFolderAlias(libraryID int64, folder, parent string) error {
	_, err := s.db.Exec(
		`INSERT INTO folder_alias(library_id, folder, parent) VALUES(?, ?, ?)
		 ON CONFLICT(library_id, folder) DO UPDATE SET parent = excluded.parent`,
		libraryID, folder, parent)
	return err
}

// DeleteFolderAlias hebt die Anzeige-Verschiebung auf (Ordner steht wieder am Originalplatz).
func (s *Store) DeleteFolderAlias(libraryID int64, folder string) error {
	_, err := s.db.Exec(`DELETE FROM folder_alias WHERE library_id = ? AND folder = ?`, libraryID, folder)
	return err
}

// ResolveFolderAlias übersetzt einen angezeigten Pfad in den echten rel_path-
// Pfad ("Siterips/DaphneDare/S1" → "DaphneDare/S1"). Pfade ohne Alias bleiben
// unverändert. Fehler werden wie "kein Alias" behandelt.
func (s *Store) ResolveFolderAlias(libraryID int64, p string) string {
	if p == "" {
		return p
	}
	aliases, err := s.ListFolderAliases(libraryID)
	if err != nil || len(aliases) == 0 {
		return p
	}
	return ResolveAlias(aliases, p)
}

// ResolveAlias: reine Funktion zu ResolveFolderAlias (testbar). Der längste
// passende Anzeige-Pfad gewinnt.
func ResolveAlias(aliases []FolderAlias, p string) string {
	sort.Slice(aliases, func(i, j int) bool { return len(aliases[i].Virtual()) > len(aliases[j].Virtual()) })
	for _, a := range aliases {
		v := a.Virtual()
		if p == v {
			return a.Folder
		}
		if strings.HasPrefix(p, v+"/") {
			return a.Folder + p[len(v):]
		}
	}
	return p
}
