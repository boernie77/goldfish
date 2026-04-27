package store

import (
	"database/sql"
	"errors"
	"time"
)

// Collection beschreibt eine TMDB-Sammlung (James Bond, Star Wars, …).
type Collection struct {
	ID           int64     `json:"id"`
	TMDBID       int64     `json:"tmdbId"`
	Name         string    `json:"name"`
	PosterPath   string    `json:"posterPath,omitempty"`
	BackdropPath string    `json:"backdropPath,omitempty"`
	MovieCount   int       `json:"movieCount"`
	// PartCount: alle bei TMDB gelisteten Filme der Sammlung (inkl. fehlender).
	// HiddenCount: davon per User ausgeblendete Parts. Daraus kann der Client
	// den Vollständigkeits-Indikator bilden: complete = movieCount >= partCount - hiddenCount.
	PartCount   int `json:"partCount,omitempty"`
	HiddenCount int `json:"hiddenCount,omitempty"`
	// Fallback-Metadata-ID: einer der Filme dieser Sammlung, dessen Poster als
	// Kachelbild genutzt werden kann, wenn die Sammlung selbst kein Poster hat.
	FallbackMetaID int64     `json:"fallbackMetaId,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// UpsertCollection legt eine Sammlung an oder aktualisiert sie. Liefert die
// interne ID.
func (s *Store) UpsertCollection(tmdbID int64, name, poster, backdrop string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO collections(tmdb_id, name, poster_path, backdrop_path, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(tmdb_id) DO UPDATE SET
			name = excluded.name,
			poster_path = excluded.poster_path,
			backdrop_path = excluded.backdrop_path,
			updated_at = excluded.updated_at
		RETURNING id
	`, tmdbID, name, poster, backdrop, time.Now()).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// SetMetadataCollection verknüpft eine Metadata mit einer Collection-ID (0 = aus).
func (s *Store) SetMetadataCollection(metadataID, collectionID int64) error {
	if collectionID == 0 {
		_, err := s.db.Exec(`UPDATE metadata SET collection_id = NULL WHERE id = ?`, metadataID)
		return err
	}
	_, err := s.db.Exec(`UPDATE metadata SET collection_id = ? WHERE id = ?`, collectionID, metadataID)
	return err
}

// ListCollections liefert alle Sammlungen, zu denen wir mindestens einen Film
// in der Bibliothek haben. Inklusive Anzahl der zugeordneten Filme + einer
// Fallback-Metadata-ID, deren Poster als Kachelbild dienen kann, falls die
// Sammlung selbst (noch) kein Poster hat.
func (s *Store) ListCollections(userID int64) ([]Collection, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.tmdb_id, c.name, COALESCE(c.poster_path,''),
		       COALESCE(c.backdrop_path,''),
		       (SELECT COUNT(DISTINCT m.id)
		          FROM items i
		          JOIN metadata m ON m.id = i.metadata_id
		         WHERE m.collection_id = c.id) AS movie_count,
		       (SELECT COUNT(*) FROM collection_parts cp WHERE cp.collection_id = c.id) AS part_count,
		       (SELECT COUNT(*) FROM hidden_collection_parts hcp
		          WHERE hcp.collection_id = c.id AND hcp.user_id = ?) AS hidden_count,
		       COALESCE(
		         (SELECT m.id FROM metadata m
		            WHERE m.collection_id = c.id
		              AND m.poster_path IS NOT NULL AND m.poster_path <> ''
		            ORDER BY COALESCE(m.year, 9999), m.id
		            LIMIT 1),
		         0
		       ) AS fallback_meta_id,
		       c.updated_at
		FROM collections c
		WHERE EXISTS (
			SELECT 1 FROM items i
			JOIN metadata m ON m.id = i.metadata_id
			WHERE m.collection_id = c.id
		)
		AND (
			c.parts_fetched_at IS NULL
			OR (SELECT COUNT(*) FROM collection_parts cp WHERE cp.collection_id = c.id) >= 2
		)
		ORDER BY c.name COLLATE NOCASE
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.TMDBID, &c.Name, &c.PosterPath, &c.BackdropPath,
			&c.MovieCount, &c.PartCount, &c.HiddenCount, &c.FallbackMetaID, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListItemsInCollection liefert alle Items, deren Metadata zu der Collection gehört.
// Per-User-Zustand (watched/favorite) wird via JOIN auf user_item_state mitgegeben.
// Wiederverwendet das bestehende ListItems-Schema über einen Wrapper.
func (s *Store) ListItemsInCollection(collectionID, userID int64) ([]any, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container,
		       COALESCE(i.video_codec,''), COALESCE(i.audio_codec,''),
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps,
		       COALESCE(i.thumb_path,''), i.has_thumb, i.mod_time, i.released_at, i.added_at,
		       COALESCE(i.metadata_id, 0),
		       COALESCE(us.watched, 0), us.watched_at, COALESCE(us.favorite, 0), us.favorited_at,
		       COALESCE(i.trickplay_status, ''),
		       m.title, COALESCE(m.year,0), COALESCE(m.poster_path,''), m.tmdb_type, COALESCE(m.rating,0)
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE m.collection_id = ?
		ORDER BY COALESCE(m.release_date, m.year), m.title COLLATE NOCASE
	`, userID, collectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []any
	for rows.Next() {
		var r struct {
			ID, LibraryID, MetadataID                                int64
			Path, RelPath, Title, Container, VideoCodec, AudioCodec  string
			Width, Height, BitrateKbps                               int
			DurationSec                                              float64
			SizeBytes                                                int64
			ThumbPath                                                string
			HasThumb, Watched, Favorite                              int
			ModTime, AddedAt                                         time.Time
			ReleasedAt                                               sql.NullTime
			WatchedAt, FavoritedAt                                   sql.NullTime
			TrickplayStatus                                          string
			MetaTitle                                                string
			MetaYear                                                 int
			MetaPosterPath, MetaTmdbType                             string
			MetaRating                                               float64
		}
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.Path, &r.RelPath, &r.Title,
			&r.Container, &r.VideoCodec, &r.AudioCodec, &r.Width, &r.Height,
			&r.DurationSec, &r.SizeBytes, &r.BitrateKbps, &r.ThumbPath, &r.HasThumb,
			&r.ModTime, &r.ReleasedAt, &r.AddedAt, &r.MetadataID,
			&r.Watched, &r.WatchedAt, &r.Favorite, &r.FavoritedAt,
			&r.TrickplayStatus,
			&r.MetaTitle, &r.MetaYear, &r.MetaPosterPath, &r.MetaTmdbType, &r.MetaRating,
		); err != nil {
			return nil, err
		}
		rel := r.ReleasedAt.Time
		out = append(out, map[string]any{
			"id":              r.ID,
			"libraryId":       r.LibraryID,
			"path":            r.Path,
			"relPath":         r.RelPath,
			"title":           r.Title,
			"container":       r.Container,
			"videoCodec":      r.VideoCodec,
			"audioCodec":      r.AudioCodec,
			"width":           r.Width,
			"height":          r.Height,
			"durationSec":     r.DurationSec,
			"sizeBytes":       r.SizeBytes,
			"bitrateKbps":     r.BitrateKbps,
			"hasThumb":        r.HasThumb == 1,
			"modTime":         r.ModTime,
			"releasedAt":      rel,
			"addedAt":         r.AddedAt,
			"metadataId":      r.MetadataID,
			"watched":         r.Watched == 1,
			"favorite":        r.Favorite == 1,
			"trickplayStatus": r.TrickplayStatus,
			"metadata": map[string]any{
				"id":         r.MetadataID,
				"title":      r.MetaTitle,
				"year":       r.MetaYear,
				"posterPath": r.MetaPosterPath,
				"tmdbType":   r.MetaTmdbType,
				"rating":     r.MetaRating,
			},
		})
	}
	return out, rows.Err()
}

// ReplaceCollectionParts ersetzt die parts-Liste einer Sammlung komplett
// (DELETE + INSERT). Wird vom Enrichment-Worker aufgerufen, nachdem /collection/{id}
// abgerufen wurde.
func (s *Store) ReplaceCollectionParts(collectionID int64, parts []CollectionPartRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM collection_parts WHERE collection_id = ?`, collectionID); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := tx.Exec(`
			INSERT INTO collection_parts(collection_id, tmdb_movie_id, title, release_date, poster_path, ord)
			VALUES(?,?,?,?,?,?)
		`, collectionID, p.TMDBMovieID, p.Title, p.ReleaseDate, p.PosterPath, p.Ord); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE collections SET parts_fetched_at = ? WHERE id = ?`, time.Now(), collectionID); err != nil {
		return err
	}
	return tx.Commit()
}

// MissingMovieRow: ein Collection-Part, den der User nicht besitzt und nicht
// ausgeblendet hat. Wird für den Export nach Radarr/Sonarr genutzt.
type MissingMovieRow struct {
	TMDBMovieID    int64  `json:"tmdbId"`
	Title          string `json:"title"`
	ReleaseDate    string `json:"releaseDate,omitempty"`
	CollectionName string `json:"collectionName"`
	CollectionID   int64  `json:"collectionId"`
}

// ListMissingMovies liefert alle Collection-Parts, die der User NICHT im
// Bestand hat UND nicht per hidden_collection_parts ausgeblendet sind.
// Pure SQL — keine TMDB-Calls nötig, weil alle Parts bereits beim Enrichment
// in `collection_parts` persistiert wurden.
func (s *Store) ListMissingMovies(userID int64) ([]MissingMovieRow, error) {
	rows, err := s.db.Query(`
		SELECT cp.tmdb_movie_id, cp.title, COALESCE(cp.release_date,''),
		       c.name, c.id
		FROM collection_parts cp
		JOIN collections c ON c.id = cp.collection_id
		WHERE NOT EXISTS (
			SELECT 1 FROM metadata m
			JOIN items i ON i.metadata_id = m.id
			WHERE m.tmdb_type = 'movie'
			  AND m.tmdb_id = cp.tmdb_movie_id
		)
		AND NOT EXISTS (
			SELECT 1 FROM hidden_collection_parts hcp
			WHERE hcp.user_id = ?
			  AND hcp.collection_id = cp.collection_id
			  AND hcp.tmdb_movie_id = cp.tmdb_movie_id
		)
		ORDER BY c.name COLLATE NOCASE, cp.ord
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MissingMovieRow
	for rows.Next() {
		var r MissingMovieRow
		if err := rows.Scan(&r.TMDBMovieID, &r.Title, &r.ReleaseDate, &r.CollectionName, &r.CollectionID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CollectionPartRow entspricht einem Eintrag in collection_parts.
type CollectionPartRow struct {
	TMDBMovieID int64
	Title       string
	ReleaseDate string
	PosterPath  string
	Ord         int
}

// GetCollectionParts liefert alle Parts einer Sammlung in Reihenfolge.
// Für Parts, die der User besitzt, wird das komplette Item-Record mitgeliefert
// (damit die UI die normale Film-Kachel rendern kann: Watched-Toggle,
// Format-Badge, Auflösung, Laufzeit, …). Fehlende Parts bekommen nur die
// TMDB-Basisinfos.
func (s *Store) GetCollectionParts(collectionID, userID int64) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT cp.tmdb_movie_id, cp.title, cp.release_date, cp.poster_path, cp.ord,
		       i.id, i.library_id, i.path, i.rel_path, i.title,
		       i.container, COALESCE(i.video_codec,''), COALESCE(i.audio_codec,''),
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps,
		       i.has_thumb, i.mod_time, i.released_at, i.added_at,
		       COALESCE(i.metadata_id, 0), COALESCE(i.trickplay_status,''),
		       COALESCE(us.watched,0), us.watched_at,
		       COALESCE(us.favorite,0), us.favorited_at,
		       m.title, COALESCE(m.year,0), COALESCE(m.poster_path,''), m.tmdb_type, COALESCE(m.rating,0),
		       CASE WHEN hcp.tmdb_movie_id IS NULL THEN 0 ELSE 1 END AS hidden
		FROM collection_parts cp
		LEFT JOIN metadata m
		       ON m.tmdb_type='movie' AND m.tmdb_id = cp.tmdb_movie_id
		LEFT JOIN items i ON i.metadata_id = m.id
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		LEFT JOIN hidden_collection_parts hcp
		       ON hcp.user_id = ?
		      AND hcp.collection_id = cp.collection_id
		      AND hcp.tmdb_movie_id = cp.tmdb_movie_id
		WHERE cp.collection_id = ?
		ORDER BY cp.ord, cp.release_date, cp.title COLLATE NOCASE
	`, userID, userID, collectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	// Wenn es mehrere Varianten pro Metadata gibt (Merge-Fall), wählt die Query
	// mehrere Rows zurück — wir nehmen pro tmdbMovieId nur den ersten ("höchste
	// Auflösung zuerst" über die cp-Order + Item-Ordnung) und markieren die Part
	// dennoch als owned.
	seen := map[int64]bool{}
	for rows.Next() {
		var (
			tmdbMovieID int64
			partTitle   string
			partRelDate string
			partPoster  string
			ord         int

			itID sql.NullInt64
			libID sql.NullInt64
			path, relPath, itTitle, container, vc, ac sql.NullString
			width, height, bitrate sql.NullInt64
			duration sql.NullFloat64
			sizeBytes sql.NullInt64
			hasThumb sql.NullInt64
			modTime, addedAt sql.NullTime
			releasedAt sql.NullTime
			metaID sql.NullInt64
			tpStatus sql.NullString
			watched, favorite sql.NullInt64
			watchedAt, favoritedAt sql.NullTime

			mTitle, mPoster, mTmdbType sql.NullString
			mYear sql.NullInt64
			mRating sql.NullFloat64
			hidden int
		)
		if err := rows.Scan(
			&tmdbMovieID, &partTitle, &partRelDate, &partPoster, &ord,
			&itID, &libID, &path, &relPath, &itTitle,
			&container, &vc, &ac,
			&width, &height, &duration, &sizeBytes, &bitrate,
			&hasThumb, &modTime, &releasedAt, &addedAt,
			&metaID, &tpStatus,
			&watched, &watchedAt, &favorite, &favoritedAt,
			&mTitle, &mYear, &mPoster, &mTmdbType, &mRating, &hidden,
		); err != nil {
			return nil, err
		}
		if seen[tmdbMovieID] {
			continue
		}
		seen[tmdbMovieID] = true

		entry := map[string]any{
			"tmdbMovieId": tmdbMovieID,
			"title":       partTitle,
			"releaseDate": partRelDate,
			"posterPath":  partPoster,
			"ord":         ord,
			"owned":       itID.Valid,
			"hidden":      hidden == 1,
		}
		if itID.Valid {
			// Füllen wie ein normales Item-Objekt, damit die UI renderCard()
			// ohne Sonderbehandlung verwenden kann.
			it := map[string]any{
				"id":              itID.Int64,
				"libraryId":       libID.Int64,
				"path":            path.String,
				"relPath":         relPath.String,
				"title":           itTitle.String,
				"container":       container.String,
				"videoCodec":      vc.String,
				"audioCodec":      ac.String,
				"width":           width.Int64,
				"height":          height.Int64,
				"durationSec":     duration.Float64,
				"sizeBytes":       sizeBytes.Int64,
				"bitrateKbps":     bitrate.Int64,
				"hasThumb":        hasThumb.Int64 == 1,
				"watched":         watched.Int64 == 1,
				"favorite":        favorite.Int64 == 1,
				"trickplayStatus": tpStatus.String,
				"metadataId":      metaID.Int64,
				"metadata": map[string]any{
					"id":         metaID.Int64,
					"title":      mTitle.String,
					"year":       mYear.Int64,
					"posterPath": mPoster.String,
					"tmdbType":   mTmdbType.String,
					"rating":     mRating.Float64,
				},
			}
			if modTime.Valid {
				it["modTime"] = modTime.Time
			}
			if releasedAt.Valid {
				it["releasedAt"] = releasedAt.Time
			}
			if addedAt.Valid {
				it["addedAt"] = addedAt.Time
			}
			entry["item"] = it
			entry["itemId"] = itID.Int64
			entry["metadataId"] = metaID.Int64
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// ResetAllCollectionParts setzt parts_fetched_at=NULL bei allen Collections,
// damit der Enricher sie beim nächsten Lauf erneut von TMDB abfragt.
// Vorhandene collection_parts werden dabei NICHT gelöscht — sie werden einfach
// mit ReplaceCollectionParts frisch überschrieben, wenn die Antwort da ist.
func (s *Store) ResetAllCollectionParts() error {
	_, err := s.db.Exec(`UPDATE collections SET parts_fetched_at = NULL`)
	return err
}

// CollectionsNeedingPartsFetch liefert Collections, bei denen parts_fetched_at
// noch nicht gesetzt ist. Vom Enrichment-Backfill genutzt.
func (s *Store) CollectionsNeedingPartsFetch(limit int) ([]Collection, error) {
	rows, err := s.db.Query(`
		SELECT id, tmdb_id, name FROM collections
		WHERE parts_fetched_at IS NULL
		ORDER BY id
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.TMDBID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MoviesNeedingCollectionCheck liefert Movie-Metadata (nur `tmdb_type='movie'`),
// bei denen `collection_checked_at` noch nicht gesetzt ist — also Kandidaten
// für den Collection-Backfill.
func (s *Store) MoviesNeedingCollectionCheck(limit int) ([]struct {
	ID     int64
	TMDBID int64
}, error) {
	rows, err := s.db.Query(`
		SELECT id, tmdb_id FROM metadata
		WHERE tmdb_type = 'movie' AND collection_checked_at IS NULL
		ORDER BY id
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []struct {
		ID     int64
		TMDBID int64
	}
	for rows.Next() {
		var r struct {
			ID     int64
			TMDBID int64
		}
		if err := rows.Scan(&r.ID, &r.TMDBID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkCollectionChecked setzt collection_checked_at=now.
func (s *Store) MarkCollectionChecked(metadataID int64) error {
	_, err := s.db.Exec(
		`UPDATE metadata SET collection_checked_at = ? WHERE id = ?`,
		time.Now(), metadataID,
	)
	return err
}

// GetCollection liefert eine Sammlung anhand ihrer internen ID.
func (s *Store) GetCollection(id int64) (*Collection, error) {
	var c Collection
	err := s.db.QueryRow(`
		SELECT id, tmdb_id, name, COALESCE(poster_path,''), COALESCE(backdrop_path,''), updated_at
		FROM collections WHERE id = ?
	`, id).Scan(&c.ID, &c.TMDBID, &c.Name, &c.PosterPath, &c.BackdropPath, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// HideCollectionPart markiert einen Sammlungs-Part als vom User ausgeblendet.
// Idempotent — doppelte Inserts werden ignoriert.
func (s *Store) HideCollectionPart(userID, collectionID, tmdbMovieID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO hidden_collection_parts(user_id, collection_id, tmdb_movie_id)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id, collection_id, tmdb_movie_id) DO NOTHING
	`, userID, collectionID, tmdbMovieID)
	return err
}

// UnhideCollectionPart entfernt das Hidden-Flag.
func (s *Store) UnhideCollectionPart(userID, collectionID, tmdbMovieID int64) error {
	_, err := s.db.Exec(`
		DELETE FROM hidden_collection_parts
		WHERE user_id = ? AND collection_id = ? AND tmdb_movie_id = ?
	`, userID, collectionID, tmdbMovieID)
	return err
}
