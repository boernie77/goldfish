package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// musicItemTag: eine Zeile aus `items` für die Gruppierung — nur die für
// GroupMusicAlbums relevanten Tag-Felder + id/rel_path.
type musicItemTag struct {
	id                            int64
	relPath, artist, album, genre string
}

// GroupMusicAlbums läuft am Ende jedes Musik-Library-Scans (Scanner.run,
// analog zum enricher.EnrichAllFoldersNow-Trigger für TV-Bibliotheken).
//
// Gruppierung seit 2026-09-05 PRIMÄR über den physischen Elternordner der
// Datei, NICHT mehr direkt über das rohe (artist,album)-Tag-Paar — User-
// Report: "Das Phantom der Oper" zerfiel in eine Kachel PRO TRACK. Root
// Cause per Live-Diagnose verifiziert: die Datei hatte KEIN "album_artist"-
// Tag, und das "artist"-Tag enthielt pro Track eine ANDERE Kombination der
// beteiligten Sänger/Darsteller (z. B. "Peter Hofmann, Andrew Lloyd Webber,
// ..." bei Track 5, "Thomas Schulze" bei Track 7) — bei Compilations/
// Musicals/Klassik-Sammlungen ist das "artist"-Tag pro Track strukturell
// unzuverlässig für die Gruppierung, selbst wenn "album" konsistent ist.
// Der physische Ordner ist das robustere Signal: eine Ordner-Gruppe ist so
// gut wie immer EIN Album. `canonicalAlbumFields` bestimmt daraus einen
// EINZELNEN Artist-/Album-/Genre-Wert für die ganze Gruppe (uneinheitlicher
// Artist -> "Verschiedene Interpreten" statt eines zufällig "gewinnenden"
// Einzelnamens; fehlender Album-Tag -> Ordnername als Fallback-Titel).
//
// Dateien direkt im Bibliotheks-Root (kein Unterordner) haben keinen
// gemeinsamen Ordner, der mehrere Tracks bündeln könnte — die behalten das
// alte reine (artist,album)-Tag-Verhalten (siehe musicGroupKey).
func (s *Store) GroupMusicAlbums(libraryID int64) error {
	rows, err := s.db.Query(
		`SELECT id, rel_path, artist, album, genre FROM items
		 WHERE library_id = ? AND (artist != '' OR album != '')`,
		libraryID,
	)
	if err != nil {
		return err
	}
	var items []musicItemTag
	for rows.Next() {
		var it musicItemTag
		if err := rows.Scan(&it.id, &it.relPath, &it.artist, &it.album, &it.genre); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()

	groups := map[string][]musicItemTag{}
	for _, it := range items {
		key := musicGroupKey(it.relPath, it.artist, it.album)
		groups[key] = append(groups[key], it)
	}

	for _, g := range groups {
		artist, album, genre := canonicalAlbumFields(g)
		if artist == "" && album == "" {
			continue
		}
		if _, err := s.db.Exec(
			`INSERT INTO music_albums(library_id, artist, album, genre) VALUES(?, ?, ?, ?)
			 ON CONFLICT(library_id, artist, album) DO UPDATE SET
			   genre = CASE WHEN music_albums.genre = '' AND excluded.genre != '' THEN excluded.genre ELSE music_albums.genre END`,
			libraryID, artist, album, genre,
		); err != nil {
			return err
		}
		var albumID int64
		if err := s.db.QueryRow(
			`SELECT id FROM music_albums WHERE library_id = ? AND artist = ? AND album = ?`,
			libraryID, artist, album,
		).Scan(&albumID); err != nil {
			return err
		}
		placeholders := make([]string, len(g))
		args := make([]any, 0, len(g)+1)
		args = append(args, albumID)
		for i, it := range g {
			placeholders[i] = "?"
			args = append(args, it.id)
		}
		if _, err := s.db.Exec(
			fmt.Sprintf(`UPDATE items SET music_album_id = ? WHERE id IN (%s)`, strings.Join(placeholders, ",")),
			args...,
		); err != nil {
			return err
		}
	}
	// Verwaiste Alben-Zeilen aufräumen (kein Item zeigt mehr drauf) — jeder
	// Wechsel der Gruppierungslogik (z. B. dieser Ordner-statt-Artist-Umbau
	// vom 2026-09-05) lässt die vorherigen Zeilen sonst als "Karteileichen"
	// zurück. Das ist NICHT nur kosmetisch: ohne dieses DELETE tauchten sie
	// in ListMusicAlbums als sichtbare "0 Titel"-Kacheln auf (User-Report),
	// die ursprüngliche Annahme "kein Cleanup nötig, kosmetisch irrelevant"
	// war falsch. `user_music_album_favorites` hat ON DELETE CASCADE, ein
	// Favorit auf einer verwaisten Zeile verschwindet also automatisch mit.
	if _, err := s.db.Exec(
		`DELETE FROM music_albums WHERE library_id = ? AND id NOT IN (
			SELECT DISTINCT music_album_id FROM items WHERE music_album_id IS NOT NULL
		)`,
		libraryID,
	); err != nil {
		return err
	}
	return nil
}

// musicGroupKey: physischer Elternordner ist die primäre Gruppierungs-
// Identität (siehe GroupMusicAlbums-Kommentar). Root-Level-Dateien ohne
// Unterordner (kein "/" in relPath) fallen auf das alte (artist,album)-
// Tag-Paar zurück, weil es dort keinen gemeinsamen Ordner gibt.
func musicGroupKey(relPath, artist, album string) string {
	if idx := strings.LastIndex(relPath, "/"); idx > 0 {
		return "dir:" + relPath[:idx]
	}
	return "tag:" + artist + "\x00" + album
}

// canonicalAlbumFields bestimmt EINEN Artist-/Album-/Genre-Wert für eine
// ganze Gruppe (i. d. R. ein physischer Ordner). Mehrere unterschiedliche
// Artist-Werte innerhalb der Gruppe (Compilation/Musical/Klassik, siehe
// GroupMusicAlbums-Kommentar) ergeben "Verschiedene Interpreten" statt
// eines zufällig "gewinnenden" Einzelnamens. Fehlt jeder Album-Tag in der
// Gruppe, wird der letzte Ordnername als Titel verwendet.
func canonicalAlbumFields(g []musicItemTag) (artist, album, genre string) {
	albumCounts := map[string]int{}
	artistSet := map[string]bool{}
	for _, it := range g {
		if it.album != "" {
			albumCounts[it.album]++
		}
		if it.artist != "" {
			artistSet[it.artist] = true
		}
		if genre == "" && it.genre != "" {
			genre = it.genre
		}
	}
	album = mostCommonString(albumCounts)
	if album == "" && len(g) > 0 {
		album = folderDisplayName(g[0].relPath)
	}
	switch len(artistSet) {
	case 0:
		artist = ""
	case 1:
		for a := range artistSet {
			artist = a
		}
	default:
		artist = "Verschiedene Interpreten"
	}
	return artist, album, genre
}

// mostCommonString liefert den häufigsten Wert; bei Gleichstand gewinnt der
// lexikografisch kleinste (deterministisch, unabhängig von Map-Iteration).
func mostCommonString(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best := keys[0]
	for _, k := range keys[1:] {
		if counts[k] > counts[best] {
			best = k
		}
	}
	return best
}

// folderDisplayName liefert den letzten Ordnernamen des physischen
// Elternordners einer Datei (z. B. "a/b/Album XY/track.mp3" -> "Album XY").
func folderDisplayName(relPath string) string {
	dir := relPath
	if idx := strings.LastIndex(dir, "/"); idx > 0 {
		dir = dir[:idx]
	}
	if idx := strings.LastIndex(dir, "/"); idx >= 0 {
		dir = dir[idx+1:]
	}
	return dir
}

// ListMusicAlbums liefert alle Alben einer Bibliothek inkl. Track-Anzahl,
// sortiert nach Artist dann Album (COLLATE NATSORT, siehe CLAUDE.md
// "NATSORT-Collation" — NICHT auf "NATURAL" umbenennen, reserviertes Wort).
// `userID` <= 0 lässt Favorite auf false (z.B. interne Aufrufer ohne
// User-Kontext) — analog dem watched/favorite-Muster bei ListItems.
func (s *Store) ListMusicAlbums(libraryID, userID int64) ([]model.MusicAlbum, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.library_id, a.artist, a.album, a.year, a.genre, a.cover_source,
		       (SELECT COUNT(*) FROM items i WHERE i.music_album_id = a.id) AS track_count,
		       EXISTS(SELECT 1 FROM user_music_album_favorites f WHERE f.album_id = a.id AND f.user_id = ?)
		FROM music_albums a
		WHERE a.library_id = ?
		  AND EXISTS(SELECT 1 FROM items i WHERE i.music_album_id = a.id)
		ORDER BY a.artist COLLATE NATSORT, a.album COLLATE NATSORT`, userID, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.MusicAlbum
	for rows.Next() {
		var a model.MusicAlbum
		var fav int
		if err := rows.Scan(&a.ID, &a.LibraryID, &a.Artist, &a.Album, &a.Year, &a.Genre, &a.CoverSource, &a.TrackCount, &fav); err != nil {
			return nil, err
		}
		a.Favorite = fav == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetMusicAlbum liefert ein einzelnes Album, nil wenn nicht gefunden.
func (s *Store) GetMusicAlbum(id, userID int64) (*model.MusicAlbum, error) {
	var a model.MusicAlbum
	var fav int
	err := s.db.QueryRow(`
		SELECT id, library_id, artist, album, year, genre, cover_source,
		       EXISTS(SELECT 1 FROM user_music_album_favorites f WHERE f.album_id = music_albums.id AND f.user_id = ?)
		FROM music_albums WHERE id = ?`, userID, id,
	).Scan(&a.ID, &a.LibraryID, &a.Artist, &a.Album, &a.Year, &a.Genre, &a.CoverSource, &fav)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Favorite = fav == 1
	return &a, nil
}

// SetMusicAlbumFavorite setzt/entfernt den Album-Favoriten-Status für einen
// User (user_music_album_favorites) — Pendant zu SetFavorite für einzelne
// Titel (user_item_state.favorite). User-Anfrage 2026-09-04: "Favoriten
// will ich für Alben als auch für einzelne Songs erstellen können".
func (s *Store) SetMusicAlbumFavorite(userID, albumID int64, favorite bool) error {
	if favorite {
		_, err := s.db.Exec(
			`INSERT INTO user_music_album_favorites(user_id, album_id) VALUES(?, ?)
			 ON CONFLICT(user_id, album_id) DO NOTHING`,
			userID, albumID,
		)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM user_music_album_favorites WHERE user_id = ? AND album_id = ?`,
		userID, albumID,
	)
	return err
}

// ListMusicAlbumTracks liefert alle Tracks eines Albums, sortiert nach
// track_no (Fallback Titel bei fehlender Nummer). userID steuert den
// per-User-Favoriten/Zuletzt-gehört-Join — fehlte ursprünglich komplett
// (Bug, gefixt 2026-09-04): ein in der Album-Kachelansicht favorisierter
// Track zeigte den Herz-Button danach zwar sofort aktiv (optimistisches
// DOM-Update in toggleFavoriteOnCard), aber ein erneuter Album-Fetch (z. B.
// nach Sortierungswechsel) lieferte `favorite` NIE mit — das Herz sprang
// wieder auf "nicht favorisiert" zurück, obwohl der DB-Wert korrekt gesetzt
// war.
func (s *Store) ListMusicAlbumTracks(albumID, userID int64) ([]model.Item, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(i.artist, ''), COALESCE(i.album, ''), COALESCE(i.track_no, 0), COALESCE(i.music_album_id, 0),
		       COALESCE(us.favorite, 0), us.last_played_at
		FROM items i
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE i.music_album_id = ?
		ORDER BY i.track_no, i.title COLLATE NATSORT`, userID, albumID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb, favorite int
		var released sql.NullString
		var lastPlayedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb,
			&it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&it.Artist, &it.Album, &it.TrackNo, &it.MusicAlbumID,
			&favorite, &lastPlayedAt); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		it.Favorite = favorite == 1
		if lastPlayedAt.Valid {
			v := lastPlayedAt.Time
			it.LastPlayedAt = &v
		}
		it.ReleasedAt = parseDBTime(released.String)
		if it.ReleasedAt.IsZero() {
			it.ReleasedAt = it.ModTime
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PendingAlbumsForCoverExtraction liefert ALLE Alben einer Library ohne Cover
// (cover_source=”), OHNE den artist!=album-Ausschluss von PendingMusicAlbums
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

// PendingMusicAlbums liefert Alben ohne Cover (cover_source=”) mit
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

// FixMusicCoverArtVideoCodec räumt items auf, die VOR dem Scanner-Fix vom
// 2026-09-04 gescannt wurden: ffprobe listet ein eingebettetes Cover in
// MP3/FLAC/M4A-Dateien (ID3-APIC o.ä.) als eigenen "video"-Stream
// (disposition.attached_pic=1), der Scanner setzte daraufhin fälschlich
// VideoCodec/Width/Height — playback.Decide() hielt die Datei dadurch für
// ein Video und erzwang einen unnötigen HLS-Transcode (lange Verzögerung +
// kaputte Wiedergabe, User-Bericht 2026-09-04). Ein erneuter ffprobe-Call
// pro Datei wäre für den Backfill zu teuer — stattdessen ein gezielter
// Codec-Namens-Heuristik-Filter: echte Musik-Videocodecs (h264 etc.) kommen
// in dieser Bibliothek praktisch nie vor, Bild-Codecs (mjpeg/png/bmp/gif)
// eindeutig NIE als echtes Video.
func (s *Store) FixMusicCoverArtVideoCodec() (int, error) {
	res, err := s.db.Exec(`
		UPDATE items SET video_codec = '', width = 0, height = 0
		WHERE library_id IN (SELECT id FROM libraries WHERE kind = 'music')
		  AND video_codec IN ('mjpeg', 'png', 'bmp', 'gif', 'tiff', 'ppm', 'webp')
	`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
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
