package store

import (
	"database/sql"
	"errors"
	"time"
)

// --- Ordner-Opt-in (intro_skip_folders) ---
//
// Analog zu trickplay_folders: Zeilen-Existenz = aktiviert. folder=="" (ganze
// Bibliothek) wird hier UND im API-Handler zurückgewiesen — die Intro-
// Erkennung ist bewusst nur pro einzelnem Serien-Ordner aktivierbar.

// SetIntroSkipFolder aktiviert oder deaktiviert die Intro-Erkennung für
// einen Serien-Ordner. folder=="" wird abgelehnt (kein Bibliotheks-weiter
// Schalter). Bestehende season-Einschränkung bleibt beim Wieder-Aktivieren
// erhalten (kein Reset auf "alle Staffeln").
func (s *Store) SetIntroSkipFolder(libraryID int64, folder string, enabled bool) error {
	if folder == "" {
		return errors.New("intro-erkennung: folder darf nicht leer sein (nur einzelne Serien-Ordner)")
	}
	if enabled {
		_, err := s.db.Exec(
			`INSERT OR IGNORE INTO intro_skip_folders(library_id, folder) VALUES(?, ?)`,
			libraryID, folder)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM intro_skip_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder)
	return err
}

// SetIntroSkipFolderSeason schränkt einen bereits aktivierten Serien-Ordner
// auf EINE Staffel ein (season>0) oder hebt die Einschränkung wieder auf
// (season==0, alle Staffeln). Für kontrolliertes Testen des Algorithmus an
// einer einzelnen Staffel, ohne gleich die ganze Show zu verarbeiten.
func (s *Store) SetIntroSkipFolderSeason(libraryID int64, folder string, season int) error {
	_, err := s.db.Exec(
		`UPDATE intro_skip_folders SET season = ? WHERE library_id = ? AND folder = ?`,
		season, libraryID, folder)
	return err
}

// IntroSkipFolderSeason liefert die Staffel-Einschränkung eines aktivierten
// Ordners (0 = alle Staffeln, keine Zeile = nicht aktiviert → 0,false).
func (s *Store) IntroSkipFolderSeason(libraryID int64, folder string) (int, bool, error) {
	var season int
	err := s.db.QueryRow(
		`SELECT season FROM intro_skip_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&season)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return season, true, nil
}

func (s *Store) IntroSkipFolderEnabled(libraryID int64, folder string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM intro_skip_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&n)
	return n > 0, err
}

// ListIntroSkipFolders liefert alle aktivierten Serien-Ordner einer Library.
func (s *Store) ListIntroSkipFolders(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT folder FROM intro_skip_folders WHERE library_id = ? ORDER BY folder`,
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

// ListAllIntroSkipFolders liefert alle aktivierten (library_id, folder)-Paare
// über alle Libraries hinweg — für EnqueueStaleFolders im Worker.
func (s *Store) ListAllIntroSkipFolders() ([]FolderSelector, error) {
	rows, err := s.db.Query(`SELECT library_id, folder FROM intro_skip_folders ORDER BY library_id, folder`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FolderSelector
	for rows.Next() {
		var sel FolderSelector
		if err := rows.Scan(&sel.LibraryID, &sel.Folder); err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	return out, rows.Err()
}

// --- Job-Tabelle (intro_skip_jobs) ---
//
// Ein Job = ein Serien-Ordner (nicht pro Episode), da die Erkennung immer
// alle Episoden einer Show gemeinsam vergleicht.

type IntroSkipJob struct {
	ID              int64      `json:"id"`
	LibraryID       int64      `json:"libraryId"`
	Folder          string     `json:"folder"`
	Status          string     `json:"status"`
	EpisodesTotal   int        `json:"episodesTotal"`
	EpisodesMatched int        `json:"episodesMatched"`
	Error           string     `json:"error"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
}

// UpsertIntroSkipJob legt einen Job an oder setzt ihn zurück auf 'pending',
// wenn er zuvor 'failed' war (analog UpsertSubtitleJob). Ein bereits
// laufender oder fertiger Job wird NICHT durch erneutes Aktivieren
// unterbrochen/verworfen.
func (s *Store) UpsertIntroSkipJob(libraryID int64, folder string) error {
	_, err := s.db.Exec(`
		INSERT INTO intro_skip_jobs(library_id, folder, status)
		VALUES(?, ?, 'pending')
		ON CONFLICT(library_id, folder) DO UPDATE SET
			status = 'pending', error = NULL
		WHERE status = 'failed'
	`, libraryID, folder)
	return err
}

// ForceRetryIntroSkipJob setzt einen Job UNBEDINGT zurück auf 'pending' —
// anders als UpsertIntroSkipJob (das nur 'failed'-Jobs zurücksetzt und
// bereits erfolgreich fertige Jobs beim bloßen Ordner-Toggle unangetastet
// lässt). Für den expliziten ↻-Retry-Button im Admin-Dialog: der User will
// eine Neuanalyse auch dann erzwingen können, wenn der letzte Lauf zwar
// technisch "done" war, aber inhaltlich unbrauchbar ist (z.B. 0 Treffer
// durch inzwischen angepasste Erkennungs-Schwellenwerte).
func (s *Store) ForceRetryIntroSkipJob(libraryID int64, folder string) error {
	_, err := s.db.Exec(`
		INSERT INTO intro_skip_jobs(library_id, folder, status)
		VALUES(?, ?, 'pending')
		ON CONFLICT(library_id, folder) DO UPDATE SET
			status = 'pending', error = NULL
	`, libraryID, folder)
	return err
}

// ListPendingIntroSkipJobs liefert nur Jobs, deren Ordner AKTUELL noch in
// intro_skip_folders aktiviert ist. Ohne diesen JOIN würde der Worker auch
// längst deaktivierte Serien abarbeiten, deren Job-Zeile beim Deaktivieren
// nicht gelöscht wird (nur die intro_skip_folders-Zeile verschwindet) —
// real beobachtet: nach dem "alle deaktivieren außer Chuck"-Backfill
// standen noch ~30 alte pending-Jobs anderer Serien in der Tabelle, die der
// Worker nach Chuck stur weiterverarbeitet hätte.
func (s *Store) ListPendingIntroSkipJobs(limit int) ([]IntroSkipJob, error) {
	rows, err := s.db.Query(`
		SELECT j.id, j.library_id, j.folder, j.status, j.episodes_total, j.episodes_matched, COALESCE(j.error,''), j.finished_at
		FROM intro_skip_jobs j
		WHERE j.status = 'pending'
		  AND EXISTS (SELECT 1 FROM intro_skip_folders f WHERE f.library_id = j.library_id AND f.folder = j.folder)
		ORDER BY j.id ASC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanIntroSkipJobs(rows)
}

func (s *Store) ListIntroSkipJobsByStatus(status string) ([]IntroSkipJob, error) {
	rows, err := s.db.Query(`
		SELECT id, library_id, folder, status, episodes_total, episodes_matched, COALESCE(error,''), finished_at
		FROM intro_skip_jobs WHERE status = ? ORDER BY id DESC
	`, status)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanIntroSkipJobs(rows)
}

// GetIntroSkipJob liefert den Job für einen Ordner (falls vorhanden).
func (s *Store) GetIntroSkipJob(libraryID int64, folder string) (*IntroSkipJob, error) {
	row := s.db.QueryRow(`
		SELECT id, library_id, folder, status, episodes_total, episodes_matched, COALESCE(error,''), finished_at
		FROM intro_skip_jobs WHERE library_id = ? AND folder = ?
	`, libraryID, folder)
	var j IntroSkipJob
	var finishedAt sql.NullTime
	err := row.Scan(&j.ID, &j.LibraryID, &j.Folder, &j.Status, &j.EpisodesTotal, &j.EpisodesMatched, &j.Error, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		j.FinishedAt = &finishedAt.Time
	}
	return &j, nil
}

func (s *Store) SetIntroSkipJobRunning(id int64) error {
	_, err := s.db.Exec(`UPDATE intro_skip_jobs SET status='running', started_at=? WHERE id=?`, time.Now(), id)
	return err
}

func (s *Store) SetIntroSkipJobDone(id int64, episodesTotal, episodesMatched int) error {
	_, err := s.db.Exec(`
		UPDATE intro_skip_jobs
		SET status='done', episodes_total=?, episodes_matched=?, finished_at=?, error=NULL
		WHERE id=?
	`, episodesTotal, episodesMatched, time.Now(), id)
	return err
}

func (s *Store) SetIntroSkipJobFailed(id int64, errMsg string) error {
	_, err := s.db.Exec(`UPDATE intro_skip_jobs SET status='failed', finished_at=?, error=? WHERE id=?`, time.Now(), errMsg, id)
	return err
}

// ResetRunningIntroSkipJobs: Crash-Recovery beim Worker-Start, analog
// ResetRunningSubtitleJobs.
func (s *Store) ResetRunningIntroSkipJobs() error {
	_, err := s.db.Exec(`UPDATE intro_skip_jobs SET status='failed', error='unterbrochen (Neustart)' WHERE status='running'`)
	return err
}

func (s *Store) RetryFailedIntroSkipJobs() (int, error) {
	res, err := s.db.Exec(`UPDATE intro_skip_jobs SET status='pending', error=NULL WHERE status='failed'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanIntroSkipJobs(rows *sql.Rows) ([]IntroSkipJob, error) {
	var out []IntroSkipJob
	for rows.Next() {
		var j IntroSkipJob
		var finishedAt sql.NullTime
		if err := rows.Scan(&j.ID, &j.LibraryID, &j.Folder, &j.Status, &j.EpisodesTotal, &j.EpisodesMatched, &j.Error, &finishedAt); err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			j.FinishedAt = &finishedAt.Time
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// --- Ergebnis pro Episode ---

// SetItemIntroRange schreibt die erkannte Vorspann-Zeitspanne für eine
// Episode UND markiert sie als analysiert (intro_checked_at). start/end ==
// nil bedeutet "analysiert, aber kein Intro gefunden" — bewusst kein
// Fehlerzustand, verhindert aber via intro_checked_at einen erneuten
// Analyse-Versuch bei jedem Rescan (analog metadata.cast_fetched_at).
func (s *Store) SetItemIntroRange(itemID int64, start, end *float64) error {
	_, err := s.db.Exec(
		`UPDATE items SET intro_start_sec = ?, intro_end_sec = ?, intro_checked_at = ? WHERE id = ?`,
		start, end, time.Now(), itemID)
	return err
}

// ResetIntroDataForFolder löscht intro_start_sec/intro_end_sec/
// intro_checked_at für ALLE Episoden eines Ordners — unabhängig vom
// aktuellen Wert. Für den v2-Outlier-Backfill (cmd/goldfish/main.go): die
// v1-Fassung filterte nur nachträglich am schon gespeicherten Ergebnis
// herum, was bei manchen Serien versehentlich die RICHTIGEN Treffer
// löschte statt der falschen (Mehrheits-Bias, siehe CLAUDE.md). Diese
// Werte sind damit unwiederbringlich weg — einziger sauberer Ausweg ist
// eine vollständige Neuanalyse (Reset + erneuter Job-Lauf mit dem
// korrigierten Cluster-Algorithmus).
func (s *Store) ResetIntroDataForFolder(libraryID int64, folder string) error {
	_, err := s.db.Exec(`
		UPDATE items SET intro_start_sec = NULL, intro_end_sec = NULL, intro_checked_at = NULL
		WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\'
	`, libraryID, escapeLike(folder)+"/%")
	return err
}

// IntroSkipEpisodeDetail: eine Episode mit ihrem Intro-Erkennungs-Status —
// für die aufklappbare Episoden-Liste im Admin-Dialog (pro Job-Tab).
type IntroSkipEpisodeDetail struct {
	ItemID   int64    `json:"itemId"`
	Title    string   `json:"title"`
	Season   int      `json:"season,omitempty"`
	Episode  int      `json:"episode,omitempty"`
	Checked  bool     `json:"checked"`
	StartSec *float64 `json:"startSec,omitempty"`
	EndSec   *float64 `json:"endSec,omitempty"`
}

// IntroSkipEpisodeDetails liefert alle Episoden eines Ordners mit ihrem
// aktuellen Erkennungs-Status, sortiert nach Staffel/Episode (unmatched
// Items ohne Episode-Metadata landen an dessen Ende, per COALESCE auf
// einen hohen Platzhalterwert).
func (s *Store) IntroSkipEpisodeDetails(libraryID int64, folder string) ([]IntroSkipEpisodeDetail, error) {
	rows, err := s.db.Query(`
		SELECT i.id, COALESCE(NULLIF(m.title, ''), i.title),
		       COALESCE(m.season, 0), COALESCE(m.episode, 0),
		       i.intro_checked_at IS NOT NULL, i.intro_start_sec, i.intro_end_sec
		FROM items i
		LEFT JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		WHERE i.library_id = ? AND i.rel_path LIKE ? ESCAPE '\'
		ORDER BY COALESCE(m.season, 999999), COALESCE(m.episode, 999999), i.title COLLATE NATSORT
	`, libraryID, escapeLike(folder)+"/%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []IntroSkipEpisodeDetail
	for rows.Next() {
		var d IntroSkipEpisodeDetail
		var start, end sql.NullFloat64
		if err := rows.Scan(&d.ItemID, &d.Title, &d.Season, &d.Episode, &d.Checked, &start, &end); err != nil {
			return nil, err
		}
		if start.Valid {
			v := start.Float64
			d.StartSec = &v
		}
		if end.Valid {
			v := end.Float64
			d.EndSec = &v
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// HasUnanalyzedEpisodes prüft, ob im Ordner mindestens eine Episode liegt,
// die intro_checked_at noch nicht gesetzt hat — für EnqueueStaleFolders nach
// jedem Scan (neue Episoden einer bereits aktivierten Show). season>0
// schränkt auf eine einzelne Staffel ein (0 = alle Staffeln).
func (s *Store) HasUnanalyzedEpisodes(libraryID int64, folder string, season int) (bool, error) {
	q := `
		SELECT COUNT(*) FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		WHERE i.library_id = ? AND i.rel_path LIKE ? ESCAPE '\' AND i.intro_checked_at IS NULL`
	args := []any{libraryID, escapeLike(folder) + "/%"}
	if season > 0 {
		q += ` AND m.season = ?`
		args = append(args, season)
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n > 0, err
}
