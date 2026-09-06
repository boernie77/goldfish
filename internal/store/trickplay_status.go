// trickplay_status.go -- Trickplay-Status/-Job-Verwaltung, aus sqlite.go
// ausgelagert (Schritt 3 der Modularisierung, siehe CLAUDE.md "Code-Review
// 2026-09-06"). Reine Funktionsverschiebung, keine Logik-/Signaturaenderung.

package store

import (
	"database/sql"

	"github.com/boernie77/goldfish/internal/model"
)

// SetTrickplayFolder aktiviert oder deaktiviert Trickplay-Generierung für einen Ordner.
// Deaktivieren löscht nur die Markierung, die generierten Dateien bleiben auf Platte
// (können über einen separaten Cleanup entfernt werden).
func (s *Store) SetTrickplayFolder(libraryID int64, folder string, enabled bool) error {
	if enabled {
		_, err := s.db.Exec(
			`INSERT OR IGNORE INTO trickplay_folders(library_id, folder) VALUES(?, ?)`,
			libraryID, folder)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM trickplay_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder)
	return err
}

// TrickplayFolderEnabled prüft ob ein Ordner aktiviert ist.
func (s *Store) TrickplayFolderEnabled(libraryID int64, folder string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM trickplay_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&n)
	return n > 0, err
}

// SetItemTrickplay setzt den Status eines Items ("pending" | "done" | "failed" | "").
func (s *Store) SetItemTrickplay(itemID int64, status string) error {
	_, err := s.db.Exec(`UPDATE items SET trickplay_status = ? WHERE id = ?`, status, itemID)
	return err
}

// SetItemTrickplayError setzt Status + optionale Fehlermeldung (wird bei "done" gelöscht).
func (s *Store) SetItemTrickplayError(itemID int64, status, errMsg string) error {
	if status == "failed" {
		_, err := s.db.Exec(
			`UPDATE items SET trickplay_status = ?, trickplay_error = ? WHERE id = ?`,
			status, errMsg, itemID,
		)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE items SET trickplay_status = ?, trickplay_error = NULL WHERE id = ?`,
		status, itemID,
	)
	return err
}

// ResetFailedTrickplayStatus setzt alle Items mit status=failed wieder auf leer,
// damit der Worker sie erneut probiert (z. B. nach verbessertem Filter).
func (s *Store) ResetFailedTrickplayStatus() (int, error) {
	res, err := s.db.Exec(
		`UPDATE items SET trickplay_status = '', trickplay_error = NULL WHERE trickplay_status = 'failed'`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResetAllTrickplayStatus setzt bei allen Items trickplay_status zurück auf "".
// Wird vom "Trickplay komplett löschen"-Admin-Flow aufgerufen, nachdem die
// Dateien auf Platte entfernt wurden.
func (s *Store) ResetAllTrickplayStatus() error {
	_, err := s.db.Exec(`UPDATE items SET trickplay_status = '', trickplay_error = NULL`)
	return err
}

// ResetStuckPendingTrickplay setzt Items, die auf "pending" hängen, zurück
// auf leer — damit sie beim nächsten Worker-Lauf wieder als Kandidaten
// auftauchen. Stuck-Pending entstehen, wenn der Worker mid-run abbricht
// (Container-Restart, Cancel, Crash): das Item wird auf "pending" markiert
// bevor ffmpeg startet, aber der Status nie auf "done"/"failed" finalisiert.
func (s *Store) ResetStuckPendingTrickplay() (int, error) {
	res, err := s.db.Exec(
		`UPDATE items SET trickplay_status = '', trickplay_error = NULL WHERE trickplay_status = 'pending'`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// TrickplayLogEntry wird vom Admin-Log-Viewer angezeigt.
type TrickplayLogEntry struct {
	ID        int64  `json:"id"`
	LibraryID int64  `json:"libraryId"`
	Path      string `json:"path"`
	RelPath   string `json:"relPath"`
	Title     string `json:"title"`
	Error     string `json:"error,omitempty"`
}

// CountTrickplayByStatus liefert die globale Aufteilung pro Status („"/"pending"/
// "done"/"failed") über alle Libraries — für die Health-Diagnose.
func (s *Store) CountTrickplayByStatus() (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(trickplay_status,'') AS st, COUNT(*) FROM items GROUP BY st
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ListItemsByTrickplayStatus liefert alle Items mit dem angegebenen Status,
// inklusive Pfad und Fehlermeldung. Für den Admin-Log-Viewer.
func (s *Store) ListItemsByTrickplayStatus(status string) ([]TrickplayLogEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, library_id, path, rel_path, title, COALESCE(trickplay_error, '')
		FROM items
		WHERE trickplay_status = ?
		ORDER BY path
	`, status)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TrickplayLogEntry
	for rows.Next() {
		var r TrickplayLogEntry
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.Path, &r.RelPath, &r.Title, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FolderTrickplayStatus zählt Items eines Ordners nach Trickplay-Status.
type TrickplayStatus struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

func (s *Store) FolderTrickplayStatus(libraryID int64, folder string) (TrickplayStatus, error) {
	var st TrickplayStatus
	var rows *sql.Rows
	var err error
	if folder == "" {
		// Library-weit: alle Items der Bibliothek
		rows, err = s.db.Query(
			`SELECT COALESCE(trickplay_status, '') FROM items WHERE library_id = ?`,
			libraryID)
	} else {
		rows, err = s.db.Query(
			`SELECT COALESCE(trickplay_status, '') FROM items
			 WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\'`,
			libraryID, escapeLike(folder)+"/%")
	}
	if err != nil {
		return st, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return st, err
		}
		st.Total++
		switch status {
		case "done":
			st.Done++
		case "pending":
			st.Pending++
		case "failed":
			st.Failed++
		}
	}
	return st, rows.Err()
}

// PendingTrickplayItems liefert Items in aktivierten Ordnern, die noch kein Trickplay haben.
// Spezialfall: tf.folder = ” aktiviert Trickplay für die gesamte Bibliothek.
//
// Sortierung: Folder-für-Folder. Der Top-Level-Ordner mit dem neuesten Item
// kommt zuerst, alle seine pending-Items werden komplett abgearbeitet bevor
// der nächste Folder dran ist. Innerhalb eines Folders nach added_at DESC
// (neuestes zuerst). Bei Items direkt in der Library-Root gilt rel_path als
// eigener „Folder".
func (s *Store) PendingTrickplayItems(limit int) ([]model.Item, error) {
	rows, err := s.db.Query(`
		WITH pending AS (
			SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container,
				i.video_codec, i.audio_codec, i.width, i.height, i.duration_sec,
				i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb, i.mod_time,
				i.added_at,
				CASE
					WHEN INSTR(i.rel_path, '/') > 0
					THEN SUBSTR(i.rel_path, 1, INSTR(i.rel_path, '/') - 1)
					ELSE i.rel_path
				END AS top_folder
			FROM items i
			JOIN trickplay_folders tf
			  ON tf.library_id = i.library_id
			  AND (tf.folder = '' OR i.rel_path LIKE (tf.folder || '/%') ESCAPE '\')
			JOIN libraries l ON l.id = i.library_id AND l.kind != 'music'
			WHERE COALESCE(i.trickplay_status,'') = ''
			  AND i.duration_sec > 0
		)
		SELECT id, library_id, path, rel_path, title, container,
			COALESCE(video_codec,''), COALESCE(audio_codec,''), width, height, duration_sec,
			size_bytes, bitrate_kbps, COALESCE(thumb_path,''), has_thumb, mod_time
		FROM pending
		ORDER BY MAX(added_at) OVER (PARTITION BY library_id, top_folder) DESC,
		         library_id, top_folder,
		         added_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb int
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container,
			&it.VideoCodec, &it.AudioCodec, &it.Width, &it.Height, &it.DurationSec, &it.SizeBytes,
			&it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListOrphanTrickplayItems liefert Items mit gesetztem trickplay_status, die
// aber NICHT (mehr) in einem aktivierten Ordner liegen. Werden beim Worker-Run
// aufgeräumt (Dateien + Status). Relikte aus früheren Versionen ohne Folder-Check.
func (s *Store) ListOrphanTrickplayItems() ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT i.id FROM items i
		WHERE COALESCE(i.trickplay_status,'') != ''
		  AND NOT EXISTS (
			SELECT 1 FROM trickplay_folders tf
			WHERE tf.library_id = i.library_id
			  AND (tf.folder = '' OR i.rel_path LIKE (tf.folder || '/%') ESCAPE '\')
		  )
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListTrickplayFolders liefert alle aktivierten Ordner einer Bibliothek.
func (s *Store) ListTrickplayFolders(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT folder FROM trickplay_folders WHERE library_id = ? ORDER BY folder`,
		libraryID)
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
