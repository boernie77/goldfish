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
	COALESCE(i.trickplay_status, '')
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
		var watchedAt, favoritedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title,
			&it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps,
			&it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		it.Watched = watched == 1
		it.Favorite = favorite == 1
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
