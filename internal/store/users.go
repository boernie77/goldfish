package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"golang.org/x/crypto/bcrypt"
)

// --- Users ---

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, is_admin, max_age_rating, can_download, created_at
		FROM users ORDER BY username COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.User{}
	for rows.Next() {
		var u model.User
		var isAdmin, canDownload int
		var maxAge sql.NullInt64
		if err := rows.Scan(&u.ID, &u.Username, &isAdmin, &maxAge, &canDownload, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.IsAdmin = isAdmin == 1
		u.CanDownload = canDownload == 1
		if maxAge.Valid {
			v := int(maxAge.Int64)
			u.MaxAgeRating = &v
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) GetUser(id int64) (*model.User, error) {
	var u model.User
	var isAdmin, canDownload int
	var maxAge sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, username, is_admin, max_age_rating, can_download, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &isAdmin, &maxAge, &canDownload, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	u.CanDownload = canDownload == 1
	if maxAge.Valid {
		v := int(maxAge.Int64)
		u.MaxAgeRating = &v
	}
	return &u, nil
}

func (s *Store) GetUserByName(username string) (*model.User, string, error) {
	var u model.User
	var isAdmin, canDownload int
	var maxAge sql.NullInt64
	var hash string
	err := s.db.QueryRow(
		`SELECT id, username, is_admin, max_age_rating, can_download, created_at, password_hash FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &isAdmin, &maxAge, &canDownload, &u.CreatedAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	u.IsAdmin = isAdmin == 1
	u.CanDownload = canDownload == 1
	if maxAge.Valid {
		v := int(maxAge.Int64)
		u.MaxAgeRating = &v
	}
	return &u, hash, nil
}

// SetUserMaxAgeRating setzt die Altersbeschränkung. nil = keine Beschränkung.
func (s *Store) SetUserMaxAgeRating(userID int64, max *int) error {
	if max == nil {
		_, err := s.db.Exec(`UPDATE users SET max_age_rating = NULL WHERE id = ?`, userID)
		return err
	}
	_, err := s.db.Exec(`UPDATE users SET max_age_rating = ? WHERE id = ?`, *max, userID)
	return err
}

// SetUserCanDownload erlaubt/verbietet Datei-Downloads für diesen User
// (Detail-Dialog + Bulk-Download). Admins ignorieren den Wert (siehe
// requireDownloadAllowed in internal/api).
func (s *Store) SetUserCanDownload(userID int64, allowed bool) error {
	v := 0
	if allowed {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE users SET can_download = ? WHERE id = ?`, v, userID)
	return err
}

func (s *Store) CreateUser(username, password string, isAdmin bool) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	flag := 0
	if isAdmin {
		flag = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO users(username, password_hash, is_admin) VALUES(?, ?, ?)`,
		username, string(hash), flag)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetUserPassword(userID int64, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID)
	return err
}

func (s *Store) SetUserAdmin(userID int64, isAdmin bool) error {
	flag := 0
	if isAdmin {
		flag = 1
	}
	_, err := s.db.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, flag, userID)
	return err
}

func (s *Store) DeleteUser(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

// VerifyPassword vergleicht ein Passwort mit dem gespeicherten Hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GetUserByOIDCSubject sucht den User der mit diesem OIDC-Subject verknüpft ist.
// Liefert (nil, nil) wenn keiner verknüpft ist.
func (s *Store) GetUserByOIDCSubject(sub string) (*model.User, error) {
	var u model.User
	var isAdmin, canDownload int
	var maxAge sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, username, is_admin, max_age_rating, can_download, created_at FROM users WHERE oidc_subject = ?`, sub,
	).Scan(&u.ID, &u.Username, &isAdmin, &maxAge, &canDownload, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	u.CanDownload = canDownload == 1
	if maxAge.Valid {
		v := int(maxAge.Int64)
		u.MaxAgeRating = &v
	}
	return &u, nil
}

// GetUserByNameCI: case-insensitive username-Lookup. Wird beim ersten OIDC-Login
// als Fallback genutzt, wenn noch keine Verknüpfung existiert.
func (s *Store) GetUserByNameCI(username string) (*model.User, error) {
	var u model.User
	var isAdmin, canDownload int
	var maxAge sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, username, is_admin, max_age_rating, can_download, created_at FROM users WHERE username = ? COLLATE NOCASE`, username,
	).Scan(&u.ID, &u.Username, &isAdmin, &maxAge, &canDownload, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	u.CanDownload = canDownload == 1
	if maxAge.Valid {
		v := int(maxAge.Int64)
		u.MaxAgeRating = &v
	}
	return &u, nil
}

// SetUserOIDCSubject verknüpft einen Goldfish-User mit einem OIDC-Subject.
func (s *Store) SetUserOIDCSubject(userID int64, sub string) error {
	_, err := s.db.Exec(`UPDATE users SET oidc_subject = ? WHERE id = ?`, sub, userID)
	return err
}

// --- Sessions ---

// NewSessionToken erzeugt einen 32-Byte zufälligen Token, URL-sicher kodiert.
func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) CreateSession(userID int64, ttl time.Duration) (model.Session, error) {
	token, err := NewSessionToken()
	if err != nil {
		return model.Session{}, err
	}
	expires := time.Now().Add(ttl)
	if _, err := s.db.Exec(
		`INSERT INTO sessions(token, user_id, expires_at) VALUES(?, ?, ?)`,
		token, userID, expires,
	); err != nil {
		return model.Session{}, err
	}
	return model.Session{Token: token, UserID: userID, ExpiresAt: expires}, nil
}

// GetSession gibt User zurück, falls Token gültig und nicht abgelaufen.
func (s *Store) GetSession(token string) (*model.User, error) {
	if token == "" {
		return nil, nil
	}
	var u model.User
	var isAdmin, canDownload int
	var maxAge sql.NullInt64
	var expires time.Time
	// Bugfix 2026-09-02: diese Query hat vorher max_age_rating GAR NICHT
	// mitgeladen — jeder per currentUser(r) geladene User hatte dadurch
	// IMMER MaxAgeRating==nil, egal was in der DB stand. Alle FSK-Checks
	// (requireAgeAllowed, ListItems-Filter, Collections) hängen an genau
	// diesem Feld — die Altersfreigabe-Sperre griff dadurch bei KEINER
	// eingeloggten Session, sie wurde nur beim allerersten Login-Query
	// (GetUserByName, dort korrekt) kurz korrekt gesetzt und danach nie
	// wieder gelesen.
	err := s.db.QueryRow(`
		SELECT u.id, u.username, u.is_admin, u.max_age_rating, u.can_download, u.created_at, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ?`, token,
	).Scan(&u.ID, &u.Username, &isAdmin, &maxAge, &canDownload, &u.CreatedAt, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(expires) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return nil, nil
	}
	u.IsAdmin = isAdmin == 1
	u.CanDownload = canDownload == 1
	if maxAge.Valid {
		v := int(maxAge.Int64)
		u.MaxAgeRating = &v
	}
	return &u, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// GCSessions löscht abgelaufene Sessions.
func (s *Store) GCSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now())
	return err
}

// --- Library Access ---

func (s *Store) UserLibraryAccess(userID int64) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT library_id FROM user_library_access WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetUserLibraryAccess setzt die Access-Liste für einen User (ersetzt bestehende).
func (s *Store) SetUserLibraryAccess(userID int64, libIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM user_library_access WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, lid := range libIDs {
		if _, err := tx.Exec(
			`INSERT INTO user_library_access(user_id, library_id) VALUES(?, ?)`,
			userID, lid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserHasExplicitLibraryACL: true, sobald für den User MINDESTENS eine
// user_library_access-Zeile existiert. War bis 2026-09-02 auch für Admins
// wirksam (User-Wunsch 2026-08-31: "auch bei Admins will ich Bibliotheken
// ausblenden können") — auf User-Wunsch 2026-09-02 wieder zurückgenommen:
// eine neu angelegte Bibliothek tauchte für einen Admin mit eigener ACL-Zeile
// nirgends mehr auf (auch nicht im "Bibliotheken verwalten"-Dialog), was beim
// Anlegen einer Test-/Demo-Bibliothek zu genau der verwirrenden "existiert
// angeblich nicht"-Situation führte. Sichtbarkeits-Personalisierung für den
// eigenen täglichen Gebrauch gibt es dafür bereits zweckgebunden über
// `user_home_prefs`/`user_nav_prefs` ("🏠 Startseite anpassen") — ACL bleibt
// jetzt ausschließlich ein Werkzeug, um NICHT-Admin-Usern Zugriff zu
// entziehen. Diese Funktion wird nur noch für Non-Admins ausgewertet.
func (s *Store) UserHasExplicitLibraryACL(userID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM user_library_access WHERE user_id = ?`, userID).Scan(&n)
	return n > 0, err
}

// UserHasLibraryAccess: Admin sieht IMMER alles (siehe Kommentar bei
// UserHasExplicitLibraryACL). Non-Admin → nur über ACL, ES SEI DENN die
// Library steht in `forceAdminOnlyLibraries` (siehe hardening.go) — das
// überstimmt jede ACL-Zeile, egal was in der Benutzerverwaltung eingestellt
// wird.
func (s *Store) UserHasLibraryAccess(userID, libID int64, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	if s.isForceAdminOnlyLibrary(libID) {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM user_library_access WHERE user_id = ? AND library_id = ?`,
		userID, libID).Scan(&n)
	return n > 0, err
}

// WatchedItemBasic: minimale Item-Info für den Gesehen-Sync-Backfill (siehe
// watch_links.go) — spart einen GetItemFor-Call pro Item.
type WatchedItemBasic struct {
	ItemID     int64
	LibraryID  int64
	MetadataID int64
}

// WatchedItemsBasic liefert alle vom User als gesehen markierten Items (nur
// die für ACL/FSK-Prüfung nötigen Felder). Für den Gesehen-Sync-Backfill
// zwischen verknüpften Usern (`internal/api/watch_links.go`).
func (s *Store) WatchedItemsBasic(userID int64) ([]WatchedItemBasic, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, COALESCE(i.metadata_id, 0)
		FROM user_item_state us
		JOIN items i ON i.id = us.item_id
		WHERE us.user_id = ? AND us.watched = 1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WatchedItemBasic
	for rows.Next() {
		var w WatchedItemBasic
		if err := rows.Scan(&w.ItemID, &w.LibraryID, &w.MetadataID); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// --- Per-User Watched/Favorite ---

func (s *Store) SetWatchedFor(userID, itemID int64, watched bool) error {
	if watched {
		_, err := s.db.Exec(`
			INSERT INTO user_item_state(user_id, item_id, watched, watched_at)
			VALUES(?, ?, 1, ?)
			ON CONFLICT(user_id, item_id) DO UPDATE SET watched=1, watched_at=excluded.watched_at
		`, userID, itemID, time.Now())
		return err
	}
	// Gesehen wieder entfernen → kompletter Reset: Resume-Position mit
	// rausnehmen, damit der Film nicht wieder in der „Fortsetzen"-Spalte
	// auftaucht. Entspricht der UX-Erwartung: einmal Gesehen markiert und
	// wieder entfernt = auf Werkszustand.
	_, err := s.db.Exec(`
		INSERT INTO user_item_state(user_id, item_id, watched, watched_at, resume_pos_sec, last_played_at)
		VALUES(?, ?, 0, NULL, NULL, NULL)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			watched=0,
			watched_at=NULL,
			resume_pos_sec=NULL,
			last_played_at=NULL
	`, userID, itemID)
	return err
}

// SetResumePosition speichert die aktuelle Wiedergabe-Position für späteres
// „Fortsetzen ab …". 0 oder Werte < 30s werden als „von vorne" interpretiert
// und löschen den Marker.
func (s *Store) SetResumePosition(userID, itemID int64, posSec float64) error {
	if posSec < 5 {
		_, err := s.db.Exec(`
			INSERT INTO user_item_state(user_id, item_id, resume_pos_sec)
			VALUES(?, ?, NULL)
			ON CONFLICT(user_id, item_id) DO UPDATE SET resume_pos_sec=NULL
		`, userID, itemID)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO user_item_state(user_id, item_id, resume_pos_sec)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET resume_pos_sec=excluded.resume_pos_sec
	`, userID, itemID, posSec)
	return err
}

// GetResumePosition liefert die gespeicherte Wiedergabe-Position (0 = keine).
func (s *Store) GetResumePosition(userID, itemID int64) (float64, error) {
	var p sql.NullFloat64
	err := s.db.QueryRow(
		`SELECT resume_pos_sec FROM user_item_state WHERE user_id = ? AND item_id = ?`,
		userID, itemID).Scan(&p)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !p.Valid {
		return 0, nil
	}
	return p.Float64, nil
}

// TouchLastPlayed setzt user_item_state.last_played_at = now. Erstellt den
// Per-User-Zustand bei Bedarf. Wird beim Öffnen des Players aufgerufen.
func (s *Store) TouchLastPlayed(userID, itemID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO user_item_state(user_id, item_id, last_played_at)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET last_played_at=excluded.last_played_at
	`, userID, itemID, time.Now())
	return err
}

func (s *Store) SetFavoriteFor(userID, itemID int64, favorite bool) error {
	if favorite {
		_, err := s.db.Exec(`
			INSERT INTO user_item_state(user_id, item_id, favorite, favorited_at)
			VALUES(?, ?, 1, ?)
			ON CONFLICT(user_id, item_id) DO UPDATE SET favorite=1, favorited_at=excluded.favorited_at
		`, userID, itemID, time.Now())
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO user_item_state(user_id, item_id, favorite, favorited_at)
		VALUES(?, ?, 0, NULL)
		ON CONFLICT(user_id, item_id) DO UPDATE SET favorite=0, favorited_at=NULL
	`, userID, itemID)
	return err
}

// SetItemRatingFor setzt die persönliche Sternebewertung (0–3) eines Users
// für ein Item. rating wird auf 0..3 geklemmt.
func (s *Store) SetItemRatingFor(userID, itemID int64, rating int) error {
	if rating < 0 {
		rating = 0
	}
	if rating > 3 {
		rating = 3
	}
	_, err := s.db.Exec(`
		INSERT INTO user_item_state(user_id, item_id, rating)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET rating=excluded.rating
	`, userID, itemID, rating)
	return err
}

// MigrateLegacyPlaylistOwners setzt user_id auf allen Playlists ohne Besitzer.
func (s *Store) MigrateLegacyPlaylistOwners(userID int64) error {
	_, err := s.db.Exec(`UPDATE playlists SET user_id = ? WHERE user_id IS NULL`, userID)
	return err
}

// MigrateLegacyItemStateToUser: beim ersten Setup werden die globalen items.watched/favorite
// auf den Initial-Admin übertragen, damit keine History verloren geht.
func (s *Store) MigrateLegacyItemStateToUser(userID int64) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO user_item_state(user_id, item_id, watched, watched_at, favorite, favorited_at)
		SELECT ?, id, watched, watched_at, favorite, favorited_at FROM items
		WHERE watched = 1 OR favorite = 1
	`, userID)
	return err
}

// ListLibrariesForUser filtert Libraries basierend auf ACL. Admin sieht immer
// alle (siehe Kommentar bei UserHasExplicitLibraryACL).
func (s *Store) ListLibrariesForUser(userID int64, isAdmin bool) ([]model.Library, error) {
	if isAdmin {
		return s.ListLibraries()
	}
	exclSQL, exclArgs := s.forceAdminOnlyExclusionSQL("l.id")
	args := []any{userID}
	args = append(args, exclArgs...)
	rows, err := s.db.Query(`
		SELECT l.id, l.name, l.path, l.kind, l.created_at
		FROM libraries l
		JOIN user_library_access a ON a.library_id = l.id
		WHERE a.user_id = ? AND `+exclSQL+`
		ORDER BY l.name
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Library{}
	for rows.Next() {
		var l model.Library
		var kind string
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &kind, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Kind = model.LibraryKind(kind)
		out = append(out, l)
	}
	return out, rows.Err()
}
