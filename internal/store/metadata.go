// metadata.go -- TMDB-Metadaten-CRUD + Folder-Metadata-Zuordnung, aus
// sqlite.go ausgelagert (Schritt 3 der Modularisierung, siehe CLAUDE.md
// "Code-Review 2026-09-06"). Reine Funktionsverschiebung, keine
// Logik-/Signaturaenderung.

package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

func (s *Store) UpsertMetadata(m *model.Metadata) (int64, error) {
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = time.Now()
	}
	// RETURNING id liefert sowohl beim INSERT als auch beim ON-CONFLICT-UPDATE-Pfad
	// garantiert die korrekte metadata-rowid. LastInsertId() ist hier nicht zuverlässig,
	// weil SQLite-Connections gepoolt sind und die letzte Insert-ID aus einer anderen
	// Tabelle stammen kann → FK-Fehler beim späteren SetItemMetadata.
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO metadata(tmdb_type, tmdb_id, parent_id, title, original_title, year, release_date,
			overview, rating, genres, runtime_min, poster_path, backdrop_path, season, episode, imdb_id,
			age_rating, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tmdb_type, tmdb_id, season, episode) DO UPDATE SET
			parent_id=excluded.parent_id,
			title=excluded.title,
			original_title=excluded.original_title,
			year=excluded.year,
			release_date=excluded.release_date,
			overview=excluded.overview,
			rating=excluded.rating,
			genres=excluded.genres,
			runtime_min=excluded.runtime_min,
			poster_path=excluded.poster_path,
			backdrop_path=excluded.backdrop_path,
			imdb_id=excluded.imdb_id,
			-- age_rating wird bei TMDB-Upserts NICHT überschrieben: manuelle
			-- User-Edits haben Vorrang, TMDB liefert es sowieso nicht zuverlässig.
			updated_at=excluded.updated_at
		RETURNING id
	`,
		m.TMDBType, m.TMDBID, nullInt(m.ParentID), m.Title, m.OriginalTitle, nullInt(int64(m.Year)),
		nullTime(m.ReleaseDate), m.Overview, m.Rating, m.Genres, m.RuntimeMin,
		m.PosterPath, m.BackdropPath, m.Season, m.Episode, m.IMDBID, m.AgeRating, m.UpdatedAt,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	m.ID = id
	return id, nil
}

func (s *Store) GetMetadata(id int64) (*model.Metadata, error) {
	return s.scanMetadataRow(s.db.QueryRow(
		`SELECT id, tmdb_type, tmdb_id, COALESCE(parent_id,0), title, COALESCE(original_title,''),
			COALESCE(year,0), release_date, COALESCE(overview,''), COALESCE(rating,0), COALESCE(genres,''),
			COALESCE(runtime_min,0), COALESCE(poster_path,''), COALESCE(backdrop_path,''),
			COALESCE(season,0), COALESCE(episode,0), COALESCE(imdb_id,''), COALESCE(age_rating,''), updated_at
		FROM metadata WHERE id = ?`, id))
}

func (s *Store) GetMetadataByTMDB(tmdbType string, tmdbID int64, season, episode int) (*model.Metadata, error) {
	return s.scanMetadataRow(s.db.QueryRow(
		`SELECT id, tmdb_type, tmdb_id, COALESCE(parent_id,0), title, COALESCE(original_title,''),
			COALESCE(year,0), release_date, COALESCE(overview,''), COALESCE(rating,0), COALESCE(genres,''),
			COALESCE(runtime_min,0), COALESCE(poster_path,''), COALESCE(backdrop_path,''),
			COALESCE(season,0), COALESCE(episode,0), COALESCE(imdb_id,''), COALESCE(age_rating,''), updated_at
		FROM metadata WHERE tmdb_type = ? AND tmdb_id = ? AND season = ? AND episode = ?`,
		tmdbType, tmdbID, season, episode))
}

// ListMetadataIDsForRefresh liefert alle Metadata-IDs mit gesetzter TMDB-ID,
// sortiert: zuerst Filme/Shows, dann Episoden (damit Parent-Shows beim
// Bulk-Refresh schon aktualisiert sind, wenn Episoden refreshed werden).
func (s *Store) ListMetadataIDsForRefresh() ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT id FROM metadata
		WHERE tmdb_id > 0
		ORDER BY CASE tmdb_type WHEN 'movie' THEN 0 WHEN 'tv' THEN 1 WHEN 'episode' THEN 2 ELSE 3 END, id
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

// MetadataMissingAgeRating liefert IDs/Type/TMDB-IDs aller Movie- und TV-
// Metadaten OHNE age_rating. Wird vom FSK-Backfill genutzt, um nachträglich
// per TMDB-Cert nachzuziehen. Episodes übergehen wir — die FSK steht bei TMDB
// nur auf Show-Ebene und wird auch bei Anzeige als Parent-Fallback gelesen.
func (s *Store) MetadataMissingAgeRating() (ids []int64, types []string, tmdbIDs []int64, err error) {
	rows, err := s.db.Query(`
		SELECT id, tmdb_type, tmdb_id
		FROM metadata
		WHERE tmdb_type IN ('movie','tv')
		  AND COALESCE(age_rating,'') = ''
		  AND tmdb_id > 0
		ORDER BY id
	`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, tmdbID int64
		var t string
		if err := rows.Scan(&id, &t, &tmdbID); err != nil {
			return nil, nil, nil, err
		}
		ids = append(ids, id)
		types = append(types, t)
		tmdbIDs = append(tmdbIDs, tmdbID)
	}
	return ids, types, tmdbIDs, rows.Err()
}

// SetMetadataPosterPath aktualisiert poster_path eines Metadata-Eintrags.
// Wird vom Poster-Edit-Endpoint genutzt, wenn der User ein eigenes Poster
// hochlädt oder ein anderes TMDB-Poster auswählt.
func (s *Store) SetMetadataPosterPath(id int64, path string) error {
	_, err := s.db.Exec(`UPDATE metadata SET poster_path = ?, updated_at = ? WHERE id = ?`,
		path, time.Now(), id)
	return err
}

// SetMetadataAgeRatingIfEmpty schreibt eine FSK aus TMDB nur, wenn noch
// keine manuelle Vergabe existiert. Dadurch überschreibt der TMDB-Fetch
// keine User-Edits.
func (s *Store) SetMetadataAgeRatingIfEmpty(id int64, ageRating string) error {
	if ageRating == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE metadata SET age_rating = ? WHERE id = ? AND COALESCE(age_rating,'') = ''`, ageRating, id)
	return err
}

// UpdateMetadataManual schreibt manuelle Edits vom Admin zurück. Nur die Felder,
// die der User editieren kann — KEIN tmdb_id/tmdb_type/parent_id (Integrität).
func (s *Store) UpdateMetadataManual(id int64, title, originalTitle string, year int,
	releaseDate time.Time, overview string, rating float64, runtimeMin int,
	genres, ageRating string) error {
	_, err := s.db.Exec(`
		UPDATE metadata SET
			title=?, original_title=?, year=?, release_date=?, overview=?, rating=?,
			runtime_min=?, genres=?, age_rating=?, updated_at=?
		WHERE id=?
	`, title, originalTitle, nullInt(int64(year)), nullTime(releaseDate), overview, rating,
		runtimeMin, genres, ageRating, time.Now(), id)
	return err
}

func (s *Store) scanMetadataRow(row *sql.Row) (*model.Metadata, error) {
	var m model.Metadata
	var rd sql.NullTime
	err := row.Scan(&m.ID, &m.TMDBType, &m.TMDBID, &m.ParentID, &m.Title, &m.OriginalTitle,
		&m.Year, &rd, &m.Overview, &m.Rating, &m.Genres, &m.RuntimeMin, &m.PosterPath,
		&m.BackdropPath, &m.Season, &m.Episode, &m.IMDBID, &m.AgeRating, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rd.Valid {
		m.ReleaseDate = rd.Time
	}
	return &m, nil
}

func (s *Store) SetItemMetadata(itemID, metadataID int64) error {
	if metadataID == 0 {
		// Unmatch → auch Confirmed-Flag + Range zurücksetzen (Zuordnung ist weg)
		_, err := s.db.Exec(`UPDATE items SET metadata_id = NULL, metadata_confirmed = 0, episode_end = 0 WHERE id = ?`, itemID)
		return err
	}
	_, err := s.db.Exec(`UPDATE items SET metadata_id = ? WHERE id = ?`, metadataID, itemID)
	return err
}

// SetItemMetadataConfirmed markiert (oder entfernt) die Bestätigung einer
// TMDB-Zuordnung. Bestätigte Items tauchen nicht mehr in der „Verdächtige
// Zuordnungen"-Liste auf und werden von Scan/Enricher nicht überschrieben.
func (s *Store) SetItemMetadataConfirmed(itemID int64, confirmed bool) error {
	v := 0
	if confirmed {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE items SET metadata_confirmed = ? WHERE id = ?`, v, itemID)
	return err
}

// SetItemVariantSplit nimmt ein Item aus der automatischen ×N-Varianten-
// Gruppierung heraus (split=true) oder legt es wieder mit Geschwister-Items
// gleicher metadata_id zusammen (split=false). Ändert NICHT die metadata_id
// selbst — es bleibt derselbe Film, nur die Anzeige als eigene vs. gruppierte
// Kachel wird umgeschaltet.
func (s *Store) SetItemVariantSplit(itemID int64, split bool) error {
	v := 0
	if split {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE items SET variant_split = ? WHERE id = ?`, v, itemID)
	return err
}

// ConfirmItemMatch kombiniert SetItemMetadata + Auto-Confirm — für den Fall
// eines expliziten manuellen Match-Calls. Der User ordnet eine TMDB-ID zu →
// wir nehmen an, dass dies eine bewusste Entscheidung ist.
func (s *Store) ConfirmItemMatch(itemID, metadataID int64) error {
	_, err := s.db.Exec(`UPDATE items SET metadata_id = ?, metadata_confirmed = 1 WHERE id = ?`, metadataID, itemID)
	return err
}

// SetItemEpisodeEnd schreibt die Ende-Episodennummer einer Doppelfolge
// (S07E23E24 → episode_end=24). 0 bedeutet keine Range.
func (s *Store) SetItemEpisodeEnd(itemID int64, episodeEnd int) error {
	if episodeEnd < 0 {
		episodeEnd = 0
	}
	_, err := s.db.Exec(`UPDATE items SET episode_end = ? WHERE id = ?`, episodeEnd, itemID)
	return err
}

// EpisodeBackfillRow: ID + Pfad eines gematchten Episoden-Items, das noch
// nie auf Range-Erkennung geprüft wurde. Vom einmaligen Startup-Backfill
// benutzt, der den Dateiname parsed und episode_end befüllt.
type EpisodeBackfillRow struct {
	ID   int64
	Path string
}

// EpisodeItemsForRangeBackfill liefert alle gematchten Episoden-Items mit
// `episode_end=0`, deren Dateiname mindestens zwei 'E' enthält — das ist der
// günstige Prefilter für potenzielle Doppelfolgen (S07E23E24.mkv). Items ohne
// zweites 'E' sind garantiert keine Range und werden übersprungen.
func (s *Store) EpisodeItemsForRangeBackfill() ([]EpisodeBackfillRow, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.path
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		WHERE COALESCE(i.episode_end, 0) = 0
		  AND i.path LIKE '%E%E%'
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EpisodeBackfillRow
	for rows.Next() {
		var r EpisodeBackfillRow
		if err := rows.Scan(&r.ID, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListConfirmedItems liefert alle Items mit metadata_confirmed=1 inkl. Metadata.
// Wird vom „NFO retroaktiv schreiben"-Admin-Endpoint genutzt.
func (s *Store) ListConfirmedItems() ([]model.Item, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(i.trickplay_status, '')
		FROM items i
		WHERE COALESCE(i.metadata_confirmed, 0) = 1
		  AND i.metadata_id IS NOT NULL
		ORDER BY i.id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var released sql.NullString
		var hasThumb int
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title,
			&it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps,
			&it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&it.TrickplayStatus); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		it.MetadataConfirmed = true
		it.ReleasedAt = parseDBTime(released.String)
		if it.ReleasedAt.IsZero() {
			it.ReleasedAt = it.ModTime
		}
		out = append(out, it)
	}
	s.attachMetadata(out)
	return out, rows.Err()
}

func (s *Store) SetFolderMetadata(libraryID int64, folder string, metadataID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO folder_metadata(library_id, folder, metadata_id)
		VALUES(?,?,?)
		ON CONFLICT(library_id, folder) DO UPDATE SET metadata_id = excluded.metadata_id
	`, libraryID, folder, nullInt(metadataID))
	return err
}

func (s *Store) GetFolderMetadataID(libraryID int64, folder string) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRow(
		`SELECT metadata_id FROM folder_metadata WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id.Int64, err
}

// FolderMetadataRowExists prüft, ob für diesen Ordner ÜBERHAUPT schon eine
// `folder_metadata`-Zeile existiert — unabhängig davon, ob `metadata_id`
// gesetzt oder NULL ist. Wichtig, um "noch nie versucht" (keine Zeile) von
// "bewusst unmatched" (Zeile mit NULL — entweder weil TMDB nichts fand, ODER
// weil ein Admin die Zuordnung per "🚫 Zuordnung entfernen" bewusst gelöscht
// hat) zu unterscheiden. `GetFolderMetadataID` allein kann das NICHT: beide
// Fälle liefern dort `0` zurück (User-Report 2026-09-06: "Terra X" wurde
// nach dem Entfernen der Zuordnung binnen Minuten vom periodischen
// 5-Minuten-Enrichment-Worker automatisch wieder gematcht, weil `matchItem`
// nur auf `showMetaID == 0` prüfte, nicht auf "gab es schon einen Versuch").
func (s *Store) FolderMetadataRowExists(libraryID int64, folder string) (bool, error) {
	var exists int
	err := s.db.QueryRow(
		`SELECT 1 FROM folder_metadata WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// PendingItems liefert Items ohne Metadata-Zuordnung für den Enrichment-Worker.
func (s *Store) PendingItems(limit int) ([]model.Item, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container,
			COALESCE(i.video_codec,''), COALESCE(i.audio_codec,''), i.width, i.height, i.duration_sec,
			i.size_bytes, i.bitrate_kbps, COALESCE(i.thumb_path,''), i.has_thumb, i.mod_time,
			i.released_at, i.added_at, l.kind
		FROM items i JOIN libraries l ON l.id = i.library_id
		WHERE i.metadata_id IS NULL AND l.kind IN ('movies','tv')
		ORDER BY i.added_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb int
		var released sql.NullTime
		var kind string
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container,
			&it.VideoCodec, &it.AudioCodec, &it.Width, &it.Height, &it.DurationSec, &it.SizeBytes,
			&it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &kind); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		if released.Valid {
			it.ReleasedAt = released.Time
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PendingFolder repräsentiert einen Top-Level-Ordner in einer TV-Bibliothek, der
// noch keine Show-Metadata hat.
type PendingFolder struct {
	LibraryID int64
	Folder    string
}

// PendingFolders liefert bis zu `limit` Top-Level-Ordner einer TV-Library,
// für die noch keine Show-Metadata zugeordnet wurde.
func (s *Store) PendingFolders(limit int) ([]PendingFolder, error) {
	// `fm.folder IS NULL` (NICHT `fm.metadata_id IS NULL`!) ist die einzig
	// korrekte Bedingung für "noch nie versucht": bei einem LEFT JOIN ist
	// `fm.folder` nur dann NULL, wenn GAR KEINE folder_metadata-Zeile
	// existiert — `fm.metadata_id` dagegen ist AUCH NULL, wenn eine Zeile
	// existiert, TMDB aber nichts fand (matchShow setzt dann bewusst NULL,
	// "damit wir nicht endlos retry'en") ODER ein Admin die Zuordnung über
	// "🚫 Zuordnung entfernen" gelöscht hat. Mit der alten Bedingung wurde
	// GENAU DAS "nicht endlos retry'en" durch DIESE Query systematisch
	// unterlaufen: jeder 5-minütliche Worker-Lauf griff sich einen so
	// "unmatched" markierten Ordner erneut und matchte ihn sofort neu (User-
	// Report 2026-09-06: "Terra X" war Minuten nach dem manuellen Entfernen
	// der Zuordnung schon wieder — diesmal auf eine andere, ebenfalls
	// falsche Show — gematcht). `folder_metadata` hat PRIMARY KEY
	// (library_id, folder), beide NOT NULL — `fm.folder` ist deshalb ein
	// zuverlässiger "Zeile existiert überhaupt"-Indikator.
	rows, err := s.db.Query(`
		SELECT DISTINCT i.library_id, SUBSTR(i.rel_path, 1, INSTR(i.rel_path, '/')-1) AS folder
		FROM items i
		JOIN libraries l ON l.id = i.library_id
		LEFT JOIN folder_metadata fm
		  ON fm.library_id = i.library_id
		  AND fm.folder = SUBSTR(i.rel_path, 1, INSTR(i.rel_path, '/')-1)
		WHERE INSTR(i.rel_path, '/') > 0
		  AND l.kind = 'tv'
		  AND fm.folder IS NULL
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PendingFolder
	for rows.Next() {
		var r PendingFolder
		if err := rows.Scan(&r.LibraryID, &r.Folder); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
