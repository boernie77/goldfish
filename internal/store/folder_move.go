package store

// FolderItem: ein Item unterhalb eines Ordners (für das Verschieben ganzer Ordner).
type FolderItem struct {
	ID      int64
	RelPath string
}

// ItemsUnderFolder liefert alle Items, deren rel_path unter `folder` liegt
// (rekursiv, alle Unterordner).
func (s *Store) ItemsUnderFolder(libraryID int64, folder string) ([]FolderItem, error) {
	rows, err := s.db.Query(
		`SELECT id, rel_path FROM items WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\' ORDER BY rel_path`,
		libraryID, escapeLike(folder)+"/%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FolderItem
	for rows.Next() {
		var it FolderItem
		if err := rows.Scan(&it.ID, &it.RelPath); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// CopyFolderSettings übernimmt Serien-Zuordnung (folder_metadata) und
// Drilldown-Flag eines verschobenen Ordners auf seinen neuen Pfad, sofern dort
// noch nichts steht. Best-effort; der alte Eintrag bleibt unberührt.
func (s *Store) CopyFolderSettings(oldLib int64, oldFolder string, newLib int64, newFolder string) error {
	if _, err := s.db.Exec(
		`INSERT OR IGNORE INTO folder_metadata(library_id, folder, metadata_id)
		 SELECT ?, ?, metadata_id FROM folder_metadata WHERE library_id = ? AND folder = ?`,
		newLib, newFolder, oldLib, oldFolder); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO folder_nav(library_id, folder, drilldown)
		 SELECT ?, ?, drilldown FROM folder_nav WHERE library_id = ? AND folder = ?`,
		newLib, newFolder, oldLib, oldFolder)
	return err
}
