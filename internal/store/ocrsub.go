package store

import (
	"database/sql"
	"strings"
	"time"
)

// BitmapSubCodecs: Untertitel-Codecs, die sich NICHT nach WebVTT/Text
// konvertieren lassen und deshalb per OCR verarbeitet werden.
var BitmapSubCodecs = []string{
	"hdmv_pgs_subtitle", "pgssub", "pgs",
	"dvd_subtitle", "dvdsub",
	"dvb_subtitle", "dvbsub",
	"xsub",
}

// pgsSubCodecs: nur die von `pgsrip` verarbeitbaren PGS-Varianten. VOBSUB /
// DVB brauchen ein anderes Werkzeug → gar nicht erst einreihen.
var pgsSubCodecs = []string{"hdmv_pgs_subtitle", "pgssub", "pgs"}

func pgsSubInClause() string {
	qs := make([]string, len(pgsSubCodecs))
	for i := range qs {
		qs[i] = "?"
	}
	return strings.Join(qs, ",")
}
func pgsSubArgs() []any {
	a := make([]any, len(pgsSubCodecs))
	for i, c := range pgsSubCodecs {
		a[i] = c
	}
	return a
}

func bitmapSubInClause() string {
	qs := make([]string, len(BitmapSubCodecs))
	for i := range qs {
		qs[i] = "?"
	}
	return strings.Join(qs, ",")
}
func bitmapSubArgs() []any {
	a := make([]any, len(BitmapSubCodecs))
	for i, c := range BitmapSubCodecs {
		a[i] = c
	}
	return a
}

// --- ocr_sub_folders (Opt-in) ---

// SetOCRSubFolder aktiviert/deaktiviert die OCR-Untertitel-Erzeugung für einen
// Ordner. folder="" = ganze Bibliothek.
func (s *Store) SetOCRSubFolder(libraryID int64, folder string, enabled bool) error {
	if enabled {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO ocr_sub_folders(library_id, folder) VALUES(?, ?)`, libraryID, folder)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM ocr_sub_folders WHERE library_id = ? AND folder = ?`, libraryID, folder)
	return err
}

// ListOCRSubFolders liefert alle aktivierten (library_id, folder)-Paare.
func (s *Store) ListOCRSubFolders() ([]FolderSelector, error) {
	rows, err := s.db.Query(`SELECT library_id, folder FROM ocr_sub_folders ORDER BY library_id, folder`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FolderSelector
	for rows.Next() {
		var f FolderSelector
		if err := rows.Scan(&f.LibraryID, &f.Folder); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- ocr_sub_jobs ---

type OCRSubJob struct {
	ID     int64  `json:"id"`
	ItemID int64  `json:"itemId"`
	Status string `json:"status"`
	Langs  string `json:"langs"`
	Error  string `json:"error"`
}

// OCRSubJobRow ergänzt den Job um Anzeige-Infos für die UI-Liste.
type OCRSubJobRow struct {
	OCRSubJob
	Title      string `json:"title"`
	RelPath    string `json:"relPath"`
	LibraryID  int64  `json:"libraryId"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// UpsertOCRSubJob legt einen pending-Job für ein Item an (oder setzt einen
// failed-Job zurück auf pending). done/running bleiben unangetastet.
func (s *Store) UpsertOCRSubJob(itemID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO ocr_sub_jobs(item_id, status) VALUES(?, 'pending')
		ON CONFLICT(item_id) DO UPDATE SET status='pending', error=NULL
		WHERE ocr_sub_jobs.status = 'failed'
	`, itemID)
	return err
}

// ForceRetryOCRSubJob setzt einen Job IMMER auf pending (auch done).
func (s *Store) ForceRetryOCRSubJob(itemID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO ocr_sub_jobs(item_id, status) VALUES(?, 'pending')
		ON CONFLICT(item_id) DO UPDATE SET status='pending', error=NULL, langs='', finished_at=NULL
	`, itemID)
	return err
}

func (s *Store) ResetRunningOCRSubJobs() error {
	_, err := s.db.Exec(`UPDATE ocr_sub_jobs SET status='pending' WHERE status='running'`)
	return err
}

// DeleteOCRSubJob entfernt einen Job komplett — genutzt, wenn das Item gar
// keinen PGS-Untertitel hat (VOBSUB/DVB) und nie verarbeitbar sein wird.
func (s *Store) DeleteOCRSubJob(itemID int64) error {
	_, err := s.db.Exec(`DELETE FROM ocr_sub_jobs WHERE item_id = ?`, itemID)
	return err
}

// PurgeNonPGSOCRSubJobs löscht alle failed-Jobs, deren Item keinen PGS-Stream
// hat — Aufräumen des Backlogs aus der ersten (zu breiten) Enqueue-Runde.
func (s *Store) PurgeNonPGSOCRSubJobs() (int64, error) {
	q := `DELETE FROM ocr_sub_jobs WHERE status='failed' AND item_id NOT IN (
		SELECT item_id FROM item_streams
		WHERE type='subtitle' AND LOWER(codec) IN (` + pgsSubInClause() + `)
	)`
	res, err := s.db.Exec(q, pgsSubArgs()...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) SetOCRSubJobRunning(itemID int64) error {
	_, err := s.db.Exec(`UPDATE ocr_sub_jobs SET status='running', started_at=?, error=NULL WHERE item_id=?`, time.Now(), itemID)
	return err
}

func (s *Store) SetOCRSubJobDone(itemID int64, langs string) error {
	_, err := s.db.Exec(`UPDATE ocr_sub_jobs SET status='done', langs=?, error=NULL, finished_at=? WHERE item_id=?`, langs, time.Now(), itemID)
	return err
}

func (s *Store) SetOCRSubJobFailed(itemID int64, msg string) error {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	_, err := s.db.Exec(`UPDATE ocr_sub_jobs SET status='failed', error=?, finished_at=? WHERE item_id=?`, msg, time.Now(), itemID)
	return err
}

func (s *Store) RetryFailedOCRSubJobs() (int64, error) {
	res, err := s.db.Exec(`UPDATE ocr_sub_jobs SET status='pending', error=NULL WHERE status='failed'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NextPendingOCRSubJob zieht EINEN pending-Job, dessen Item in einem aktivierten
// ocr_sub_folders-Scope liegt. ok=false → nichts zu tun.
func (s *Store) NextPendingOCRSubJob() (*OCRSubJob, bool, error) {
	row := s.db.QueryRow(`
		SELECT j.id, j.item_id, j.status, j.langs
		FROM ocr_sub_jobs j
		JOIN items i ON i.id = j.item_id
		JOIN ocr_sub_folders f ON f.library_id = i.library_id
		  AND (f.folder = '' OR i.rel_path LIKE f.folder || '/%')
		WHERE j.status = 'pending'
		ORDER BY j.id
		LIMIT 1
	`)
	var j OCRSubJob
	if err := row.Scan(&j.ID, &j.ItemID, &j.Status, &j.Langs); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &j, true, nil
}

// EnqueueOCRSubBacklog reiht ALLE Items in aktivierten Ordnern ein, die einen
// Bild-Untertitel-Stream haben und noch keinen Job. Liefert die Anzahl neu
// eingereihter Jobs.
func (s *Store) EnqueueOCRSubBacklog() (int, error) {
	q := `
		INSERT INTO ocr_sub_jobs(item_id, status)
		SELECT DISTINCT i.id, 'pending'
		FROM items i
		JOIN ocr_sub_folders f ON f.library_id = i.library_id
		  AND (f.folder = '' OR i.rel_path LIKE f.folder || '/%')
		JOIN item_streams st ON st.item_id = i.id
		WHERE st.type = 'subtitle' AND LOWER(st.codec) IN (` + pgsSubInClause() + `)
		  AND NOT EXISTS (SELECT 1 FROM ocr_sub_jobs j WHERE j.item_id = i.id)
	`
	res, err := s.db.Exec(q, pgsSubArgs()...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// BitmapSubStream ist ein per OCR zu verarbeitender Untertitel-Stream eines Items.
type BitmapSubStream struct {
	Index    int
	Codec    string
	Language string
}

// ItemBitmapSubStreams liefert alle Bild-Untertitel-Streams eines Items.
func (s *Store) ItemBitmapSubStreams(itemID int64) ([]BitmapSubStream, error) {
	q := `SELECT stream_index, LOWER(codec), COALESCE(language,'')
	      FROM item_streams
	      WHERE item_id = ? AND type = 'subtitle' AND LOWER(codec) IN (` + bitmapSubInClause() + `)
	      ORDER BY stream_index`
	args := append([]any{itemID}, bitmapSubArgs()...)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []BitmapSubStream
	for rows.Next() {
		var b BitmapSubStream
		if err := rows.Scan(&b.Index, &b.Codec, &b.Language); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListOCRSubJobs liefert Jobs eines Status mit Item-Titel/Pfad für die UI.
func (s *Store) ListOCRSubJobs(status string) ([]OCRSubJobRow, error) {
	rows, err := s.db.Query(`
		SELECT j.id, j.item_id, j.status, j.langs, COALESCE(j.error,''),
		       COALESCE(i.title,''), COALESCE(i.rel_path,''), i.library_id,
		       COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', j.finished_at), '')
		FROM ocr_sub_jobs j
		JOIN items i ON i.id = j.item_id
		WHERE j.status = ?
		ORDER BY j.finished_at DESC, j.id DESC
		LIMIT 500
	`, status)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []OCRSubJobRow{} // nie nil → JSON [] statt null
	for rows.Next() {
		var r OCRSubJobRow
		if err := rows.Scan(&r.ID, &r.ItemID, &r.Status, &r.Langs, &r.Error,
			&r.Title, &r.RelPath, &r.LibraryID, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountOCRSubJobsByStatus für den Live-Status.
func (s *Store) CountOCRSubJobsByStatus() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM ocr_sub_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	m := map[string]int{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
