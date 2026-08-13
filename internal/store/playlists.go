package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
)

// ListPlaylistsForUser liefert Playlists, deren user_id dem angegebenen User entspricht
// (oder NULL = legacy, wird nur von Admin gesehen).
func (s *Store) ListPlaylistsForUser(userID int64, isAdmin bool) ([]model.Playlist, error) {
	var q string
	var args []any
	// Sub-Query liefert das erste Item (nach position) dieser Playlist, das ein
	// TMDB-Poster ODER ein eigenes Thumbnail hat — als Cover für die Kachel.
	posterCols := `
		(SELECT pi2.item_id FROM playlist_items pi2
		   JOIN items it ON it.id = pi2.item_id
		   LEFT JOIN metadata m ON m.id = it.metadata_id
		  WHERE pi2.playlist_id = p.id
		    AND (
		      (m.poster_path IS NOT NULL AND m.poster_path <> '') OR
		      it.has_thumb = 1
		    )
		  ORDER BY pi2.position LIMIT 1) AS poster_item,
		(SELECT it.metadata_id FROM playlist_items pi2
		   JOIN items it ON it.id = pi2.item_id
		   LEFT JOIN metadata m ON m.id = it.metadata_id
		  WHERE pi2.playlist_id = p.id
		    AND m.poster_path IS NOT NULL AND m.poster_path <> ''
		  ORDER BY pi2.position LIMIT 1) AS poster_meta`
	if isAdmin {
		q = `
			SELECT p.id, p.name, p.created_at, COUNT(pi.item_id) AS cnt,
			       ` + posterCols + `
			FROM playlists p
			LEFT JOIN playlist_items pi ON pi.playlist_id = p.id
			GROUP BY p.id, p.name, p.created_at
			ORDER BY p.name COLLATE NOCASE`
	} else {
		q = `
			SELECT p.id, p.name, p.created_at, COUNT(pi.item_id) AS cnt,
			       ` + posterCols + `
			FROM playlists p
			LEFT JOIN playlist_items pi ON pi.playlist_id = p.id
			WHERE p.user_id = ?
			GROUP BY p.id, p.name, p.created_at
			ORDER BY p.name COLLATE NOCASE`
		args = append(args, userID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Playlist{}
	for rows.Next() {
		var p model.Playlist
		var posterItem, posterMeta sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.ItemCount, &posterItem, &posterMeta); err != nil {
			return nil, err
		}
		if posterItem.Valid {
			p.PosterItemID = posterItem.Int64
		}
		if posterMeta.Valid {
			p.PosterMetadataID = posterMeta.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPlaylist(id int64) (*model.Playlist, error) {
	var p model.Playlist
	err := s.db.QueryRow(`
		SELECT p.id, p.name, p.created_at, COUNT(pi.item_id)
		FROM playlists p
		LEFT JOIN playlist_items pi ON pi.playlist_id = p.id
		WHERE p.id = ?
		GROUP BY p.id, p.name, p.created_at
	`, id).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.ItemCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &p, err
}

func (s *Store) CreatePlaylist(userID int64, name string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO playlists(name, user_id) VALUES(?, ?)`, name, userID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetPlaylistOwner prüft, ob ein User eine Playlist bearbeiten darf (Besitzer oder Admin).
func (s *Store) PlaylistOwner(playlistID int64) (int64, error) {
	var ownerID sql.NullInt64
	err := s.db.QueryRow(`SELECT user_id FROM playlists WHERE id = ?`, playlistID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if !ownerID.Valid {
		return 0, err
	}
	return ownerID.Int64, err
}

func (s *Store) RenamePlaylist(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE playlists SET name = ? WHERE id = ?`, name, id)
	return err
}

func (s *Store) DeletePlaylist(id int64) error {
	_, err := s.db.Exec(`DELETE FROM playlists WHERE id = ?`, id)
	return err
}

// AddToPlaylist fügt ein Item ans Ende der Playlist an. Rückgabe-Flag `added`
// ist true bei erfolgreicher Neueinfügung, false wenn das Item schon drin war
// (INSERT OR IGNORE hat RowsAffected=0). Der Aufrufer kann das nutzen, um dem
// User einen „Ist schon drin"-Hinweis zu geben.
func (s *Store) AddToPlaylist(playlistID, itemID int64) (bool, error) {
	var maxPos sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MAX(position) FROM playlist_items WHERE playlist_id = ?`,
		playlistID).Scan(&maxPos); err != nil {
		return false, err
	}
	pos := int64(0)
	if maxPos.Valid {
		pos = maxPos.Int64 + 1
	}
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO playlist_items(playlist_id, item_id, position) VALUES(?,?,?)`,
		playlistID, itemID, pos)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) RemoveFromPlaylist(playlistID, itemID int64) error {
	_, err := s.db.Exec(
		`DELETE FROM playlist_items WHERE playlist_id = ? AND item_id = ?`,
		playlistID, itemID)
	return err
}

// ReorderPlaylist setzt die Reihenfolge anhand der übergebenen itemID-Liste.
// Items, die nicht in der Liste vorkommen, bleiben unverändert (kein Löschen).
func (s *Store) ReorderPlaylist(playlistID int64, itemIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for pos, id := range itemIDs {
		if _, err := tx.Exec(
			`UPDATE playlist_items SET position = ? WHERE playlist_id = ? AND item_id = ?`,
			pos, playlistID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PlaylistItems liefert die Items einer Playlist in der gespeicherten Reihenfolge,
// angereichert mit Metadata.
// PlaylistItems liefert die Items einer Playlist. sort/dir steuern optional
// die Reihenfolge (dieselben Werte wie ItemFilter.Sort/SortDir in ListItems:
// title/added/released/duration/resolution/rating/played). Leer/unbekannt →
// Default = manuelle Reihenfolge (playlist_items.position), das ist auch die
// Reihenfolge, die Drag-Reorder (ReorderPlaylist) und Auto-Next voraussetzen.
// userID bestimmt, aus wessen user_item_state watched/favorite/last_played_at
// gelesen werden (0 = keine — dann bleiben die Spalten auf 0/NULL).
func (s *Store) PlaylistItems(playlistID int64, sort, dir string, userID int64) ([]model.Item, error) {
	q := `
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(us.watched, 0), us.watched_at, COALESCE(us.favorite, 0), us.favorited_at,
		       COALESCE(i.trickplay_status, ''),
		       COALESCE(i.variant_split, 0)
		FROM playlist_items pi
		JOIN items i ON i.id = pi.item_id
		LEFT JOIN metadata m ON m.id = i.metadata_id
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE pi.playlist_id = ?`
	asc := dir == "asc"
	switch sort {
	case "duration":
		if asc {
			q += ` ORDER BY i.duration_sec ASC`
		} else {
			q += ` ORDER BY i.duration_sec DESC`
		}
	case "resolution":
		if asc {
			q += ` ORDER BY MAX(i.height, (i.width * 9 / 16)) ASC, i.bitrate_kbps ASC`
		} else {
			q += ` ORDER BY MAX(i.height, (i.width * 9 / 16)) DESC, i.bitrate_kbps DESC`
		}
	case "rating":
		ratingExpr := `COALESCE(NULLIF(m.rating,0), NULLIF((SELECT mp.rating FROM metadata mp WHERE mp.id = m.parent_id),0), 0)`
		if asc {
			q += ` ORDER BY (` + ratingExpr + `) = 0, (` + ratingExpr + `) ASC`
		} else {
			q += ` ORDER BY (` + ratingExpr + `) = 0, (` + ratingExpr + `) DESC`
		}
	case "added":
		if asc {
			q += ` ORDER BY i.added_at ASC`
		} else {
			q += ` ORDER BY i.added_at DESC`
		}
	case "released":
		releaseExpr := `COALESCE(
			NULLIF(m.release_date, ''),
			CASE WHEN COALESCE(m.year, 0) > 0 THEN printf('%d-01-01', m.year) END,
			(SELECT mp.release_date FROM metadata mp WHERE mp.id = m.parent_id AND mp.release_date != ''),
			i.released_at,
			i.mod_time
		)`
		if asc {
			q += ` ORDER BY ` + releaseExpr + ` ASC`
		} else {
			q += ` ORDER BY ` + releaseExpr + ` DESC`
		}
	case "played":
		if asc {
			q += ` ORDER BY us.last_played_at IS NULL, us.last_played_at ASC`
		} else {
			q += ` ORDER BY us.last_played_at IS NULL, us.last_played_at DESC`
		}
	case "title":
		if dir == "desc" {
			q += ` ORDER BY COALESCE(NULLIF(m.title, ''), i.title) COLLATE NATSORT DESC`
		} else {
			q += ` ORDER BY COALESCE(NULLIF(m.title, ''), i.title) COLLATE NATSORT`
		}
	default:
		q += ` ORDER BY pi.position`
	}
	rows, err := s.db.Query(q, userID, playlistID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Item{}
	for rows.Next() {
		var it model.Item
		var hasThumb, watched, favorite, variantSplit int
		var released sql.NullString
		var watchedAt, favoritedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb,
			&it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus, &variantSplit); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		it.Watched = watched == 1
		it.VariantSplit = variantSplit == 1
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
	return out, nil
}

// PlaylistsForItem gibt zurück, in welchen Playlists ein Item enthalten ist.
func (s *Store) PlaylistsForItem(itemID int64) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT playlist_id FROM playlist_items WHERE item_id = ?`, itemID)
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

// normalizePlaylistName erlaubt einfache Validierung zentral.
func NormalizePlaylistName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name darf nicht leer sein")
	}
	if len(name) > 80 {
		return "", fmt.Errorf("name zu lang (max 80 Zeichen)")
	}
	return name, nil
}
