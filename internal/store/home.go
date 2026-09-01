package store

import (
	"database/sql"
	"fmt"

	"github.com/boernie77/goldfish/internal/model"
)

const homeItemCols = `
	i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
	i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps,
	COALESCE(i.thumb_path,''), i.has_thumb, i.mod_time, i.released_at, i.added_at,
	COALESCE(i.metadata_id, 0),
	COALESCE(us.watched, 0), us.watched_at, COALESCE(us.favorite, 0), us.favorited_at,
	COALESCE(i.trickplay_status, ''),
	COALESCE(i.variant_split, 0)
`

func (s *Store) scanHomeItems(rows *sql.Rows) ([]model.Item, error) {
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var released sql.NullString
		var hasThumb int
		var watched int
		var favorite int
		var variantSplit int
		var watchedAt, favoritedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title,
			&it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps,
			&it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus, &variantSplit); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		it.Watched = watched == 1
		it.Favorite = favorite == 1
		it.VariantSplit = variantSplit == 1
		if watchedAt.Valid {
			it.WatchedAt = watchedAt.Time
		}
		if favoritedAt.Valid {
			it.FavoritedAt = favoritedAt.Time
		}
		it.ReleasedAt = parseDBTime(released.String)
		if it.ReleasedAt.IsZero() {
			it.ReleasedAt = it.ModTime
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.attachMetadata(out)
	s.attachVariantCounts(out)
	return out, nil
}

// HomeContinueForLibrary — Continue-Watching pro Library.
func (s *Store) HomeContinueForLibrary(userID, libraryID int64, limit int) ([]model.Item, error) {
	if limit <= 0 {
		limit = 10
	}
	q := fmt.Sprintf(`
		SELECT %s
		FROM items i
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE i.library_id = ?
		  AND us.resume_pos_sec > 0
		  AND COALESCE(us.watched, 0) = 0
		ORDER BY us.last_played_at IS NULL, us.last_played_at DESC, us.watched_at DESC
		LIMIT ?
	`, homeItemCols)
	rows, err := s.db.Query(q, userID, libraryID, limit)
	if err != nil {
		return nil, err
	}
	return s.scanHomeItems(rows)
}

// HomeNextUpForLibrary — nächste ungesehene Episode pro Serie in dieser Library.
// Nur sinnvoll für TV-Libraries; andere liefern leer.
func (s *Store) HomeNextUpForLibrary(userID, libraryID int64, limit int) ([]model.Item, error) {
	if limit <= 0 {
		limit = 10
	}
	q := fmt.Sprintf(`
		WITH watched_shows AS (
			SELECT em.parent_id AS show_id
			FROM items ei
			JOIN metadata em ON em.id = ei.metadata_id AND em.tmdb_type = 'episode'
			JOIN user_item_state eus ON eus.item_id = ei.id AND eus.user_id = ?
			WHERE ei.library_id = ? AND em.parent_id IS NOT NULL AND eus.watched = 1
			GROUP BY em.parent_id
		),
		candidates AS (
			SELECT i.id AS item_id, m.parent_id AS show_id, m.season, m.episode,
			       ROW_NUMBER() OVER (PARTITION BY m.parent_id ORDER BY m.season, m.episode) AS rn
			FROM items i
			JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
			LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
			WHERE i.library_id = ?
			  AND COALESCE(us.watched, 0) = 0
			  AND m.parent_id IN (SELECT show_id FROM watched_shows)
		)
		SELECT %s
		FROM candidates c
		JOIN items i ON i.id = c.item_id
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE c.rn = 1
		ORDER BY i.added_at DESC
		LIMIT ?
	`, homeItemCols)
	rows, err := s.db.Query(q, userID, libraryID, userID, libraryID, userID, limit)
	if err != nil {
		return nil, err
	}
	return s.scanHomeItems(rows)
}

// HomeRecentForLibrary — zuletzt hinzugefügt pro Library. Bei TV-Libraries
// zeigen wir nur die jüngste Episode pro Serie (kompakter).
func (s *Store) HomeRecentForLibrary(userID, libraryID int64, limit int) ([]model.Item, error) {
	if limit <= 0 {
		limit = 20
	}
	q := fmt.Sprintf(`
		SELECT %s
		FROM items i
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		LEFT JOIN metadata m ON m.id = i.metadata_id
		WHERE i.library_id = ?
		  AND i.id IN (
			SELECT MAX(ii.id)
			FROM items ii
			LEFT JOIN metadata mm ON mm.id = ii.metadata_id
			WHERE ii.library_id = ?
			GROUP BY COALESCE(mm.parent_id, ii.id)
		)
		ORDER BY i.added_at DESC
		LIMIT ?
	`, homeItemCols)
	rows, err := s.db.Query(q, userID, libraryID, libraryID, limit)
	if err != nil {
		return nil, err
	}
	return s.scanHomeItems(rows)
}

// SetLibraryOnHome togglet die Startseiten-Sichtbarkeit einer Library.
func (s *Store) SetLibraryOnHome(libraryID int64, onHome bool) error {
	flag := 0
	if onHome {
		flag = 1
	}
	_, err := s.db.Exec(`UPDATE libraries SET on_home = ? WHERE id = ?`, flag, libraryID)
	return err
}

// UserHomePref ist der pro-User-Override für eine Library: Sichtbarkeit auf
// der Startseite UND Reihenfolge der Streifen dort (beides unabhängig von
// libraries.on_home/sort_order, die weiterhin den Default für User ohne
// eigene Zeile liefern).
type UserHomePref struct {
	OnHome bool
	Order  int
}

// GetUserHomePrefs liefert die pro-User-Overrides als Map library_id → Pref.
// Nur explizit gesetzte Zeilen sind enthalten; für alle übrigen Libraries
// gelten die globalen libraries.on_home/sort_order-Defaults.
func (s *Store) GetUserHomePrefs(userID int64) (map[int64]UserHomePref, error) {
	rows, err := s.db.Query(`SELECT library_id, on_home, sort_order FROM user_home_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UserHomePref{}
	for rows.Next() {
		var libID int64
		var onHome, order int
		if err := rows.Scan(&libID, &onHome, &order); err != nil {
			return nil, err
		}
		out[libID] = UserHomePref{OnHome: onHome == 1, Order: order}
	}
	return out, rows.Err()
}

// SetUserHomePref setzt (Upsert) den pro-User-Sichtbarkeits-Override für eine
// Library. sort_order bleibt bei einem bestehenden Datensatz unangetastet;
// bei einer neuen Zeile wird der aktuelle globale sort_order als Startwert
// übernommen (kein NULL-Sentinel nötig — bis zur ersten expliziten
// Umsortierung via SetUserHomeOrder verhält sich das identisch zum Fallback
// auf den globalen Wert).
func (s *Store) SetUserHomePref(userID, libraryID int64, onHome bool) error {
	flag := 0
	if onHome {
		flag = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_home_prefs (user_id, library_id, on_home, sort_order)
		VALUES (?, ?, ?, COALESCE((SELECT sort_order FROM libraries WHERE id = ?), 0))
		ON CONFLICT(user_id, library_id) DO UPDATE SET on_home = excluded.on_home`,
		userID, libraryID, flag, libraryID)
	return err
}

// SetUserHomeOrder schreibt die pro-User-Reihenfolge der Startseiten-Streifen.
// `orderedLibraryIDs` ist die komplette, vom User per Drag/▲▼ sortierte
// Liste — jede enthaltene Library bekommt ihren Index als sort_order.
// on_home bleibt beim bestehenden Wert (falls schon eine Zeile existiert)
// bzw. übernimmt sonst den aktuellen globalen Default.
func (s *Store) SetUserHomeOrder(userID int64, orderedLibraryIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, libID := range orderedLibraryIDs {
		if _, err := tx.Exec(`
			INSERT INTO user_home_prefs (user_id, library_id, on_home, sort_order)
			VALUES (?, ?, COALESCE((SELECT on_home FROM libraries WHERE id = ?), 1), ?)
			ON CONFLICT(user_id, library_id) DO UPDATE SET sort_order = excluded.sort_order`,
			userID, libID, libID, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
