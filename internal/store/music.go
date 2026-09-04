package store

import (
	"database/sql"
	"errors"

	"github.com/boernie77/goldfish/internal/model"
)

// --- Musik-Bibliotheken (music_albums) ---
//
// Kanonische Artist/Album-Identität kommt aus den Tag-Werten der Items
// (scanner.probeItem liest ID3/FLAC/Vorbis-Tags), NICHT aus der metadata-
// Tabelle — die ist auf TMDB-int-IDs zugeschnitten. Ordnerstruktur bleibt
// die normale Browse-Navigation (ListItems mit folder-Filter funktioniert
// für Musik-Items unverändert); music_albums ist NUR die zusätzliche
// Album-Gruppierung für die Kachel-Ansicht.

// GroupMusicAlbums läuft am Ende jedes Musik-Library-Scans (Scanner.run,
// analog zum enricher.EnrichAllFoldersNow-Trigger für TV-Bibliotheken).
// Legt für jede (artist,album)-Kombination der Library eine music_albums-
// Zeile an (falls noch nicht vorhanden) und verknüpft alle passenden Items.
func (s *Store) GroupMusicAlbums(libraryID int64) error {
	rows, err := s.db.Query(
		`SELECT DISTINCT artist, album FROM items WHERE library_id = ? AND (artist != '' OR album != '')`,
		libraryID,
	)
	if err != nil {
		return err
	}
	type pair struct{ artist, album string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.artist, &p.album); err != nil {
			_ = rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()

	for _, p := range pairs {
		if _, err := s.db.Exec(
			`INSERT INTO music_albums(library_id, artist, album) VALUES(?, ?, ?)
			 ON CONFLICT(library_id, artist, album) DO NOTHING`,
			libraryID, p.artist, p.album,
		); err != nil {
			return err
		}
		var albumID int64
		if err := s.db.QueryRow(
			`SELECT id FROM music_albums WHERE library_id = ? AND artist = ? AND album = ?`,
			libraryID, p.artist, p.album,
		).Scan(&albumID); err != nil {
			return err
		}
		if _, err := s.db.Exec(
			`UPDATE items SET music_album_id = ? WHERE library_id = ? AND artist = ? AND album = ?`,
			albumID, libraryID, p.artist, p.album,
		); err != nil {
			return err
		}
	}
	return nil
}

// ListMusicAlbums liefert alle Alben einer Bibliothek inkl. Track-Anzahl,
// sortiert nach Artist dann Album (COLLATE NATSORT, siehe CLAUDE.md
// "NATSORT-Collation" — NICHT auf "NATURAL" umbenennen, reserviertes Wort).
func (s *Store) ListMusicAlbums(libraryID int64) ([]model.MusicAlbum, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.library_id, a.artist, a.album, a.year, a.genre, a.cover_source,
		       (SELECT COUNT(*) FROM items i WHERE i.music_album_id = a.id) AS track_count
		FROM music_albums a
		WHERE a.library_id = ?
		ORDER BY a.artist COLLATE NATSORT, a.album COLLATE NATSORT`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.MusicAlbum
	for rows.Next() {
		var a model.MusicAlbum
		if err := rows.Scan(&a.ID, &a.LibraryID, &a.Artist, &a.Album, &a.Year, &a.Genre, &a.CoverSource, &a.TrackCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetMusicAlbum liefert ein einzelnes Album, nil wenn nicht gefunden.
func (s *Store) GetMusicAlbum(id int64) (*model.MusicAlbum, error) {
	var a model.MusicAlbum
	err := s.db.QueryRow(`
		SELECT id, library_id, artist, album, year, genre, cover_source
		FROM music_albums WHERE id = ?`, id,
	).Scan(&a.ID, &a.LibraryID, &a.Artist, &a.Album, &a.Year, &a.Genre, &a.CoverSource)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListMusicAlbumTracks liefert alle Tracks eines Albums, sortiert nach
// track_no (Fallback Titel bei fehlender Nummer).
func (s *Store) ListMusicAlbumTracks(albumID int64) ([]model.Item, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(i.artist, ''), COALESCE(i.album, ''), COALESCE(i.track_no, 0), COALESCE(i.music_album_id, 0)
		FROM items i
		WHERE i.music_album_id = ?
		ORDER BY i.track_no, i.title COLLATE NATSORT`, albumID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb int
		var released sql.NullString
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb,
			&it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&it.Artist, &it.Album, &it.TrackNo, &it.MusicAlbumID); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		it.ReleasedAt = parseDBTime(released.String)
		if it.ReleasedAt.IsZero() {
			it.ReleasedAt = it.ModTime
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PendingAlbumsForCoverExtraction liefert ALLE Alben einer Library ohne Cover
// (cover_source=''), OHNE den artist!=album-Ausschluss von PendingMusicAlbums
// — Cover-Extraktion aus der Audiodatei selbst funktioniert auch für den
// Ordner-Fallback-Fall (embedded Picture kennt keine Tags), nur die spätere
// MusicBrainz-Suche braucht verlässliche Artist/Album-Strings. Für den
// Scanner-Aufruf direkt nach GroupMusicAlbums.
func (s *Store) PendingAlbumsForCoverExtraction(libraryID int64) ([]model.MusicAlbum, error) {
	rows, err := s.db.Query(`
		SELECT id, library_id, artist, album, year, genre, cover_source
		FROM music_albums WHERE library_id = ? AND cover_source = ''`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.MusicAlbum
	for rows.Next() {
		var a model.MusicAlbum
		if err := rows.Scan(&a.ID, &a.LibraryID, &a.Artist, &a.Album, &a.Year, &a.Genre, &a.CoverSource); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FirstTrackPathForAlbum liefert den Datei-Pfad des ersten Tracks eines
// Albums (für die Cover-Extraktion — Cover gehört zum Album, nicht zum
// einzelnen Track, daher genügt eine Datei pro Album).
func (s *Store) FirstTrackPathForAlbum(albumID int64) (string, error) {
	var path string
	err := s.db.QueryRow(
		`SELECT path FROM items WHERE music_album_id = ? ORDER BY track_no LIMIT 1`, albumID,
	).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return path, err
}

// PendingMusicAlbums liefert Alben ohne Cover (cover_source='') mit
// nicht-leerem Artist UND Album. Der Ordner-Fallback-Fall (Scanner setzt bei
// fehlenden Tags beide Felder auf denselben Ordnernamen, siehe
// GroupMusicAlbums-Aufrufer) wird über `artist != album` ausgeschlossen —
// eine MusicBrainz-Suche mit reinem Ordnernamen wäre zu unzuverlässig.
// Für den periodischen MusicWorker-Lauf.
func (s *Store) PendingMusicAlbums(limit int) ([]model.MusicAlbum, error) {
	rows, err := s.db.Query(`
		SELECT id, library_id, artist, album, year, genre, cover_source
		FROM music_albums
		WHERE cover_source = '' AND artist != '' AND album != '' AND artist != album
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.MusicAlbum
	for rows.Next() {
		var a model.MusicAlbum
		if err := rows.Scan(&a.ID, &a.LibraryID, &a.Artist, &a.Album, &a.Year, &a.Genre, &a.CoverSource); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetMusicAlbumCover markiert ein Album als versorgt (source = "embedded" |
// "coverart_archive"), unabhängig davon ob tatsächlich ein Cover gefunden
// wurde — verhindert Endlos-Retries im Worker (analog metadata.cast_fetched_at).
func (s *Store) SetMusicAlbumCover(albumID int64, source, mbReleaseID string) error {
	_, err := s.db.Exec(
		`UPDATE music_albums SET cover_source = ?, mb_release_id = ?, cover_fetched_at = CURRENT_TIMESTAMP WHERE id = ?`,
		source, mbReleaseID, albumID,
	)
	return err
}
