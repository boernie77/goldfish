package store

import (
	"database/sql"
	"errors"
	"time"
)

type SubtitleJob struct {
	ID          int64      `json:"id"`
	ItemID      int64      `json:"itemId"`
	Language    string     `json:"language"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	GeneratedAt *time.Time `json:"generatedAt,omitempty"`
}

func (s *Store) UpsertSubtitleJob(itemID int64, lang string) error {
	_, err := s.db.Exec(`
		INSERT INTO generated_subtitles(item_id, language, status)
		VALUES(?, ?, 'pending')
		ON CONFLICT(item_id, language) DO UPDATE SET
			status = 'pending', error = NULL
		WHERE status = 'failed'
	`, itemID, lang)
	return err
}

// ResetSubtitleJobForRegen setzt einen fertigen Job zurück (explizites Neu-Generieren).
func (s *Store) ResetSubtitleJobForRegen(itemID int64, lang string) error {
	_, err := s.db.Exec(`
		UPDATE generated_subtitles SET status='pending', error=NULL
		WHERE item_id=? AND language=?
	`, itemID, lang)
	return err
}

func (s *Store) GetSubtitleJob(itemID int64, lang string) (*SubtitleJob, error) {
	row := s.db.QueryRow(`
		SELECT id, item_id, language, status, COALESCE(error,''), generated_at
		FROM generated_subtitles WHERE item_id = ? AND language = ?
	`, itemID, lang)
	return scanSubtitleJob(row)
}

func (s *Store) ListItemSubtitles(itemID int64) ([]SubtitleJob, error) {
	rows, err := s.db.Query(`
		SELECT id, item_id, language, status, COALESCE(error,''), generated_at
		FROM generated_subtitles WHERE item_id = ?
	`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleJob
	for rows.Next() {
		j, err := scanSubtitleJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) ListPendingSubtitleJobs(limit int) ([]SubtitleJob, error) {
	rows, err := s.db.Query(`
		SELECT gs.id, gs.item_id, gs.language, gs.status, COALESCE(gs.error,''), gs.generated_at
		FROM generated_subtitles gs
		WHERE gs.status = 'pending'
		ORDER BY gs.id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleJob
	for rows.Next() {
		j, err := scanSubtitleJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) SetSubtitleJobRunning(id int64) error {
	_, err := s.db.Exec(`UPDATE generated_subtitles SET status='running' WHERE id=?`, id)
	return err
}

func (s *Store) SetSubtitleJobDone(id int64) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE generated_subtitles SET status='done', generated_at=?, error=NULL WHERE id=?`, now, id)
	return err
}

func (s *Store) SetSubtitleJobFailed(id int64, errMsg string) error {
	_, err := s.db.Exec(`UPDATE generated_subtitles SET status='failed', error=? WHERE id=?`, errMsg, id)
	return err
}

func (s *Store) ResetRunningSubtitleJobs() error {
	_, err := s.db.Exec(`UPDATE generated_subtitles SET status='failed', error='unterbrochen (Neustart)' WHERE status='running'`)
	return err
}

func (s *Store) DeleteSubtitleJob(itemID int64, lang string) error {
	_, err := s.db.Exec(`DELETE FROM generated_subtitles WHERE item_id=? AND language=?`, itemID, lang)
	return err
}

func (s *Store) CountPendingSubtitleJobs() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM generated_subtitles WHERE status IN ('pending','running')`).Scan(&n)
	return n, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSubtitleJob(row scanner) (*SubtitleJob, error) {
	var j SubtitleJob
	var genAt sql.NullTime
	err := row.Scan(&j.ID, &j.ItemID, &j.Language, &j.Status, &j.Error, &genAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if genAt.Valid {
		j.GeneratedAt = &genAt.Time
	}
	return &j, nil
}
