package store

import (
	"database/sql"
	"math/rand"
	"time"
)

// ActivityEntry: ein Eintrag im Aktivitäts-Protokoll (Zahnrad-Menü → "📜
// Protokoll"). Bewusst EIN Eintrag pro sinnvoller Handlung — ein
// Trickplay-/OCR-/Scan-Lauf erzeugt genau eine Zeile für den ganzen Lauf,
// nicht eine pro verarbeiteter Datei. Siehe LogActivity-Aufrufstellen in
// internal/api/*.go für die vollständige Liste protokollierter Aktionen.
type ActivityEntry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	UserID   int64     `json:"userId,omitempty"`
	Username string    `json:"username"`
	Category string    `json:"category"` // "auth" | "playback" | "admin" | "job"
	Action   string    `json:"action"`
	Detail   string    `json:"detail"`
}

// LogActivity schreibt einen Protokoll-Eintrag. userID=0/username="" für
// System-/nicht zugeordnete Events (z.B. fehlgeschlagener Login mit
// unbekanntem Benutzernamen — dann steht der versuchte Name im Detail-Text).
// Fehler beim Schreiben werden vom Aufrufer bewusst nur geloggt, nie als
// Blocker behandelt (Protokoll ist Komfort-/Diagnose-Feature, analog NFO-Write).
func (s *Store) LogActivity(userID int64, username, category, action, detail string) error {
	_, err := s.db.Exec(
		`INSERT INTO activity_log (user_id, username, category, action, detail) VALUES (?, ?, ?, ?, ?)`,
		nullIfZero(userID), username, category, action, detail,
	)
	// Günstige, seltene Aufräumaktion statt eigenem Hintergrund-Worker:
	// ~1 von 200 Schreibvorgängen räumt Einträge älter als 180 Tage weg.
	if err == nil && rand.Intn(200) == 0 {
		_, _ = s.db.Exec(`DELETE FROM activity_log WHERE at < datetime('now', '-180 days')`)
	}
	return err
}

func nullIfZero(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// ActivityLogFilter: alle Felder optional (Zero-Value = kein Filter).
type ActivityLogFilter struct {
	Category string
	BeforeID int64 // Pagination: nur Einträge mit id < BeforeID (0 = ab neuestem)
	Limit    int   // Default 100, Cap 500
}

func (s *Store) ListActivityLog(f ActivityLogFilter) ([]ActivityEntry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT id, at, user_id, username, category, action, detail FROM activity_log WHERE 1=1`
	var args []any
	if f.Category != "" {
		q += ` AND category = ?`
		args = append(args, f.Category)
	}
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]ActivityEntry, 0, limit)
	for rows.Next() {
		var e ActivityEntry
		var uid sql.NullInt64
		if err := rows.Scan(&e.ID, &e.At, &uid, &e.Username, &e.Category, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		e.UserID = uid.Int64
		out = append(out, e)
	}
	return out, rows.Err()
}
