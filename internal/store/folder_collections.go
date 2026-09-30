package store

import (
	"database/sql"
	"time"
)

// Ordner-Sammlungen (seit 2026-09-30, User-Wunsch Tatort): neben den
// TMDB-Film-Sammlungen kann ein Admin einen Bibliotheks-Ordner als eigene
// Kachel unter „Sammlungen" anlegen. Die Kachel öffnet den Ordner IMMER in der
// Ordner-Ansicht — so bleibt z. B. der Tatort nach Kommissar-Unterordnern
// sortiert, obwohl er unter „Serien" nach TMDB-Staffeln (Jahren) läuft.
//
// Im gemeinsamen /api/collections-Listing tragen Ordner-Sammlungen eine
// NEGATIVE ID (-folder_collections.id), damit sie nie mit TMDB-Sammlungen
// kollidieren und ältere Apps sie über die bestehenden Endpunkte
// (/collections/{id}/items, /poster/collection/{id}) trotzdem sinnvoll öffnen:
// sie bekommen dort alle Folgen flach bzw. das Cover des Ordners.

// FolderCollection ist die gespeicherte Zeile.
type FolderCollection struct {
	ID        int64
	LibraryID int64
	Folder    string
	Name      string
}

// SetFolderCollection legt eine Ordner-Sammlung an (Upsert über Library+Ordner)
// und liefert ihre ID.
func (s *Store) SetFolderCollection(libraryID int64, folder, name string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO folder_collections (library_id, folder, name, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(library_id, folder) DO UPDATE SET name = excluded.name
		RETURNING id`, libraryID, folder, name, time.Now()).Scan(&id)
	return id, err
}

// DeleteFolderCollection entfernt die Ordner-Sammlung (Dateien/Items bleiben).
func (s *Store) DeleteFolderCollection(libraryID int64, folder string) error {
	_, err := s.db.Exec(`DELETE FROM folder_collections WHERE library_id = ? AND folder = ?`, libraryID, folder)
	return err
}

// GetFolderCollection liefert eine Ordner-Sammlung per ID (nil = gibt es nicht).
func (s *Store) GetFolderCollection(id int64) (*FolderCollection, error) {
	var fc FolderCollection
	err := s.db.QueryRow(`SELECT id, library_id, folder, name FROM folder_collections WHERE id = ?`, id).
		Scan(&fc.ID, &fc.LibraryID, &fc.Folder, &fc.Name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &fc, nil
}

// FindFolderCollection liefert die Ordner-Sammlung zu Library+Ordner (nil = keine).
func (s *Store) FindFolderCollection(libraryID int64, folder string) (*FolderCollection, error) {
	var fc FolderCollection
	err := s.db.QueryRow(`SELECT id, library_id, folder, name FROM folder_collections WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&fc.ID, &fc.LibraryID, &fc.Folder, &fc.Name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &fc, nil
}

// ListFolderCollections liefert die Ordner-Sammlungen als Collection-Einträge
// für das gemeinsame Listing — nur solche, in denen der User (ACL/FSK) mindestens
// ein Item sieht. MovieCount = sichtbare Dateien im Ordner (rekursiv).
func (s *Store) ListFolderCollections(userID int64, isAdmin bool, maxAgeRating int) ([]Collection, error) {
	aclSQL, aclArgs := s.itemVisibilityClause("i.library_id", "i.metadata_id", userID, isAdmin, maxAgeRating)
	q := `
		SELECT fc.id, fc.library_id, fc.folder, fc.name,
		       (SELECT COUNT(*) FROM items i
		         WHERE i.library_id = fc.library_id
		           AND i.rel_path LIKE REPLACE(REPLACE(REPLACE(fc.folder, '\', '\\'), '%', '\%'), '_', '\_') || '/%' ESCAPE '\'
		           AND ` + aclSQL + `) AS cnt,
		       COALESCE(fm.metadata_id, 0),
		       COALESCE(m.poster_path, ''),
		       fc.created_at,
		       COALESCE((SELECT fn.drilldown FROM folder_nav fn WHERE fn.library_id = fc.library_id AND fn.folder = fc.folder), 0)
		FROM folder_collections fc
		LEFT JOIN folder_metadata fm ON fm.library_id = fc.library_id AND fm.folder = fc.folder
		LEFT JOIN metadata m ON m.id = fm.metadata_id
		ORDER BY fc.name COLLATE NOCASE`
	rows, err := s.db.Query(q, aclArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Collection
	for rows.Next() {
		var c Collection
		var id int64
		var drill int
		if err := rows.Scan(&id, &c.LibraryID, &c.Folder, &c.Name, &c.MovieCount,
			&c.FallbackMetaID, &c.PosterPath, &c.UpdatedAt, &drill); err != nil {
			return nil, err
		}
		if c.MovieCount == 0 {
			continue
		}
		c.ID = -id
		c.Kind = "folder"
		c.Drilldown = drill == 1
		out = append(out, c)
	}
	return out, rows.Err()
}
