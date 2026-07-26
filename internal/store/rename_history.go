package store

import (
	"database/sql"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// ListConfirmedMovies: alle Items mit metadata_confirmed=1, deren verlinkte
// metadata einen tmdb_type='movie' hat. Wird vom Bulk-Rename genutzt, um
// alle Kandidaten in einem Schwung zu bearbeiten. Items werden mit
// eingebettetem Metadata zurueckgeliefert (nicht nur metadata_id).
func (s *Store) ListConfirmedMovies() ([]model.Item, error) {
	rows, err := s.db.Query(`
		SELECT i.id FROM items i
		JOIN metadata m ON m.id = i.metadata_id
		WHERE i.metadata_confirmed = 1 AND m.tmdb_type = 'movie'
		ORDER BY i.id`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := make([]model.Item, 0, len(ids))
	for _, id := range ids {
		it, err := s.GetItem(id)
		if err != nil {
			return nil, err
		}
		if it != nil {
			out = append(out, *it)
		}
	}
	return out, nil
}

// RenameHistoryEntry: ein Eintrag aus der rename_history-Tabelle.
type RenameHistoryEntry struct {
	ID           int64     `json:"id"`
	ItemID       int64     `json:"itemId"`
	OldPath      string    `json:"oldPath"`
	NewPath      string    `json:"newPath"`
	OldRelPath   string    `json:"oldRelPath"`
	NewRelPath   string    `json:"newRelPath"`
	OldLibraryID int64     `json:"oldLibraryId,omitempty"`
	NewLibraryID int64     `json:"newLibraryId,omitempty"`
	RenamedAt    time.Time `json:"renamedAt"`
	UndoneAt     time.Time `json:"undoneAt,omitempty"`
	TriggeredBy  string    `json:"triggeredBy"` // "auto" | "manual" | "bulk" | "move"
}

// InsertRenameHistory + items.path-Update in einer Transaktion. Schlaegt der
// Update fehl, wird der History-Insert zurueckgerollt — DB bleibt konsistent.
// Caller muss os.Rename ZUVOR durchgefuehrt haben.
func (s *Store) RecordRename(itemID int64, oldPath, newPath, oldRelPath, newRelPath, triggeredBy string) (int64, error) {
	return s.RecordMove(itemID, oldPath, newPath, oldRelPath, newRelPath, 0, 0, triggeredBy)
}

// RecordMove ist RecordRename + optionaler library_id-Wechsel (Bibliotheks-
// übergreifendes Verschieben, seit 2026-07-12). oldLibraryID/newLibraryID
// gleich (oder beide 0) → kein library_id-Update, verhält sich wie
// RecordRename. Caller muss os.Rename ZUVOR durchgefuehrt haben.
func (s *Store) RecordMove(itemID int64, oldPath, newPath, oldRelPath, newRelPath string, oldLibraryID, newLibraryID int64, triggeredBy string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	libChanged := newLibraryID != 0 && oldLibraryID != 0 && newLibraryID != oldLibraryID
	histOldLib, histNewLib := int64(0), int64(0)
	if libChanged {
		histOldLib, histNewLib = oldLibraryID, newLibraryID
	}
	res, err := tx.Exec(`INSERT INTO rename_history
		(item_id, old_path, new_path, old_rel_path, new_rel_path, old_library_id, new_library_id, renamed_at, triggered_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, oldPath, newPath, oldRelPath, newRelPath, histOldLib, histNewLib, time.Now().UTC(), triggeredBy)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if libChanged {
		if _, err := tx.Exec(`UPDATE items SET path = ?, rel_path = ?, library_id = ? WHERE id = ?`,
			newPath, newRelPath, newLibraryID, itemID); err != nil {
			return 0, err
		}
	} else {
		if _, err := tx.Exec(`UPDATE items SET path = ?, rel_path = ? WHERE id = ?`,
			newPath, newRelPath, itemID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// MarkRenameUndone: setzt undone_at und schreibt items.path (+ ggf.
// library_id) zurueck auf den alten Stand. Caller muss os.Rename (Reverse)
// zuvor durchgefuehrt haben.
func (s *Store) MarkRenameUndone(historyID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var entry RenameHistoryEntry
	if err := tx.QueryRow(`SELECT id, item_id, old_path, new_path, old_rel_path, new_rel_path, old_library_id, new_library_id
		FROM rename_history WHERE id = ?`, historyID).Scan(
		&entry.ID, &entry.ItemID, &entry.OldPath, &entry.NewPath, &entry.OldRelPath, &entry.NewRelPath,
		&entry.OldLibraryID, &entry.NewLibraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE rename_history SET undone_at = ? WHERE id = ?`,
		time.Now().UTC(), historyID); err != nil {
		return err
	}
	if entry.NewLibraryID != 0 && entry.OldLibraryID != 0 {
		if _, err := tx.Exec(`UPDATE items SET path = ?, rel_path = ?, library_id = ? WHERE id = ?`,
			entry.OldPath, entry.OldRelPath, entry.OldLibraryID, entry.ItemID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE items SET path = ?, rel_path = ? WHERE id = ?`,
			entry.OldPath, entry.OldRelPath, entry.ItemID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetRenameHistory holt einen Eintrag (fuer Undo).
func (s *Store) GetRenameHistory(id int64) (*RenameHistoryEntry, error) {
	var e RenameHistoryEntry
	var undone sql.NullTime
	err := s.db.QueryRow(`SELECT id, item_id, old_path, new_path, old_rel_path, new_rel_path,
		old_library_id, new_library_id, renamed_at, undone_at, triggered_by FROM rename_history WHERE id = ?`, id).Scan(
		&e.ID, &e.ItemID, &e.OldPath, &e.NewPath, &e.OldRelPath, &e.NewRelPath,
		&e.OldLibraryID, &e.NewLibraryID, &e.RenamedAt, &undone, &e.TriggeredBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if undone.Valid {
		e.UndoneAt = undone.Time
	}
	return &e, nil
}

// ListRenameHistory: alle Eintraege, neueste zuerst. limit=0 → alle.
func (s *Store) ListRenameHistory(limit int) ([]RenameHistoryEntry, error) {
	q := `SELECT id, item_id, old_path, new_path, old_rel_path, new_rel_path,
		old_library_id, new_library_id, renamed_at, undone_at, triggered_by FROM rename_history
		ORDER BY renamed_at DESC`
	args := []any{}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RenameHistoryEntry
	for rows.Next() {
		var e RenameHistoryEntry
		var undone sql.NullTime
		if err := rows.Scan(&e.ID, &e.ItemID, &e.OldPath, &e.NewPath,
			&e.OldRelPath, &e.NewRelPath, &e.OldLibraryID, &e.NewLibraryID, &e.RenamedAt, &undone, &e.TriggeredBy); err != nil {
			return nil, err
		}
		if undone.Valid {
			e.UndoneAt = undone.Time
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
