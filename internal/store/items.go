// items.go -- Item-CRUD, ListItems (Haupt-Query), Suche, Metadata-Anreicherung,
// aus sqlite.go ausgelagert (Schritt 7 der Modularisierung, siehe CLAUDE.md
// "Code-Review 2026-09-06"). Reine Funktionsverschiebung, keine
// Logik-/Signaturaenderung.
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// CountItems zählt Items einer Bibliothek. folder=="" → alle Items der Lib,
// folder!="" → rekursiv in diesem Unterordner.
func (s *Store) CountItems(libraryID int64, folder string) (int, error) {
	var n int
	if folder == "" {
		err := s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE library_id = ?`, libraryID).Scan(&n)
		return n, err
	}
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\'`,
		libraryID, escapeLike(folder)+"/%").Scan(&n)
	return n, err
}

func (s *Store) UpsertItem(it *model.Item) error {
	_, err := s.db.Exec(`
		INSERT INTO items(library_id, path, rel_path, title, container, video_codec, audio_codec, width, height, duration_sec, size_bytes, bitrate_kbps, thumb_path, has_thumb, mod_time, released_at, artist, album, track_no, genre, year)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			library_id=excluded.library_id,
			rel_path=excluded.rel_path,
			title=excluded.title,
			container=excluded.container,
			video_codec=excluded.video_codec,
			audio_codec=excluded.audio_codec,
			width=excluded.width,
			height=excluded.height,
			duration_sec=excluded.duration_sec,
			size_bytes=excluded.size_bytes,
			bitrate_kbps=excluded.bitrate_kbps,
			thumb_path=excluded.thumb_path,
			has_thumb=excluded.has_thumb,
			mod_time=excluded.mod_time,
			released_at=excluded.released_at,
			artist=excluded.artist,
			album=excluded.album,
			track_no=excluded.track_no,
			genre=excluded.genre,
			year=excluded.year
	`,
		it.LibraryID, it.Path, it.RelPath, it.Title, it.Container, it.VideoCodec, it.AudioCodec,
		it.Width, it.Height, it.DurationSec, it.SizeBytes, it.BitrateKbps, it.ThumbPath, boolToInt(it.HasThumb), it.ModTime, it.ReleasedAt,
		it.Artist, it.Album, it.TrackNo, it.Genre, it.Year,
	)
	return err
}

// ItemIDByPath liefert die Item-ID zu einem absoluten Pfad, 0 wenn nicht gefunden.
func (s *Store) ItemIDByPath(path string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM items WHERE path = ?`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) GetItemModTime(path string) (time.Time, bool, error) {
	var mt sql.NullTime
	err := s.db.QueryRow(`SELECT mod_time FROM items WHERE path = ?`, path).Scan(&mt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return mt.Time, true, nil
}

// DeleteItem entfernt ein Item aus der DB. ON DELETE CASCADE in verknüpften Tabellen
// (item_streams, user_item_state, playlist_items) räumt alles mit auf.
func (s *Store) DeleteItem(id int64) error {
	_, err := s.db.Exec(`DELETE FROM items WHERE id = ?`, id)
	return err
}

// ListItemPathsNotInSet liefert die Datei-Pfade aller DB-Items einer Library,
// deren Pfad NICHT im keep-Set vorkommt. Wird vom Scanner aufgerufen, BEVOR
// gelöscht wird, um eine Per-Folder-Removed-Statistik bauen zu können.
func (s *Store) ListItemPathsNotInSet(libraryID int64, keep map[string]struct{}) ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM items WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if _, ok := keep[p]; !ok {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

func (s *Store) DeleteItemsNotInSet(libraryID int64, keep map[string]struct{}) (int, error) {
	rows, err := s.db.Query(`SELECT id, path FROM items WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, err
	}
	type row struct {
		id   int64
		path string
	}
	var toDelete []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if _, ok := keep[r.path]; !ok {
			toDelete = append(toDelete, r)
		}
	}
	_ = rows.Close()
	for _, r := range toDelete {
		if _, err := s.db.Exec(`DELETE FROM items WHERE id = ?`, r.id); err != nil {
			return 0, err
		}
	}
	return len(toDelete), nil
}

// UnmatchTVDuplicateEpisodes setzt `metadata_id=NULL` für alle Items in TV-Libraries,
// bei denen derselbe `metadata_id` auf mehr als `threshold` Items zeigt.
// Das passiert typischerweise, wenn der Parser aus kryptischen Dateinamen (z. B.
// Release-Hashes mit vielen 3-stelligen Zahlen) fälschlich alle auf DIESELBE
// Episode gematcht hat. Unmatch ermöglicht das nachträgliche saubere Re-Matching
// mit der verbesserten Parser-Logik.
// Rückgabewert: Anzahl unmatched Items.
func (s *Store) UnmatchTVDuplicateEpisodes(threshold int) (int, error) {
	res, err := s.db.Exec(`
		UPDATE items SET metadata_id = NULL
		WHERE library_id IN (SELECT id FROM libraries WHERE kind = 'tv')
		  AND metadata_id IN (
			SELECT metadata_id FROM items
			WHERE metadata_id IS NOT NULL
			  AND library_id IN (SELECT id FROM libraries WHERE kind = 'tv')
			GROUP BY library_id, metadata_id
			HAVING COUNT(*) > ?
		  )
	`, threshold)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnmatchEpisodesInFolder setzt metadata_id=NULL für alle Items in einem
// Top-Level-Ordner einer TV-Library, deren aktuelle Metadata eine Episode
// einer ANDEREN Show ist als die neu zugeordnete Show (showTMDBID). Wird
// nach manueller Show-Zuordnung aufgerufen, damit der Enricher die Folgen
// gegen die neue Show neu matchen kann. Items mit NULL oder bereits passender
// Show bleiben unangetastet.
// Gibt die Anzahl unmatched Items zurück.
func (s *Store) UnmatchEpisodesInFolder(libraryID int64, folder string, showTMDBID int64) (int, error) {
	pat := escapeLike(folder) + "/%"
	res, err := s.db.Exec(`
		UPDATE items SET metadata_id = NULL
		WHERE library_id = ?
		  AND rel_path LIKE ? ESCAPE '\'
		  AND COALESCE(metadata_confirmed, 0) = 0
		  AND metadata_id IN (
			SELECT m.id FROM metadata m
			LEFT JOIN metadata parent ON parent.id = m.parent_id
			WHERE m.tmdb_type = 'episode'
			  AND COALESCE(parent.tmdb_id, 0) <> ?
		  )
	`, libraryID, pat, showTMDBID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnmatchAllEpisodesInFolder setzt metadata_id=NULL für ALLE Episoden-Items
// in einem Top-Level-Ordner — unabhängig davon, ob sie bestätigt waren oder
// zu welcher Show sie gehören. Wird von der „Episoden neu zuordnen"-Aktion
// in der Staffel-Ansicht aufgerufen, wenn der Enricher die Folgen systematisch
// falsch gemappt hat (z.B. Off-by-One). Auch metadata_confirmed wird zurückgesetzt,
// weil bestätigte aber falsche Zuordnungen sonst weiter bestehen blieben.
// Gibt die Anzahl unmatched Items zurück.
func (s *Store) UnmatchAllEpisodesInFolder(libraryID int64, folder string) (int, error) {
	pat := escapeLike(folder) + "/%"
	res, err := s.db.Exec(`
		UPDATE items SET metadata_id = NULL, metadata_confirmed = 0, episode_end = 0
		WHERE library_id = ?
		  AND rel_path LIKE ? ESCAPE '\'
		  AND metadata_id IN (
			SELECT m.id FROM metadata m WHERE m.tmdb_type = 'episode'
		  )
	`, libraryID, pat)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) ListItems(f ItemFilter) ([]model.Item, error) {
	// LEFT JOIN metadata nur für Sort=episode benötigt, aber der Join ist harmlos.
	// watched/favorite kommen aus user_item_state (pro-User-Zustand); ohne UserID
	// werden die Spalten auf 0/NULL gesetzt (z.B. für den Enrichment-Worker).
	q := `SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
	       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb, i.mod_time, i.released_at, i.added_at,
	       COALESCE(i.metadata_id, 0),
	       COALESCE(us.watched, 0), us.watched_at, COALESCE(us.favorite, 0), us.favorited_at,
	       COALESCE(i.trickplay_status, ''),
	       COALESCE(i.episode_end, 0),
	       COALESCE(i.variant_split, 0),
	       COALESCE(us.rating, 0),
	       COALESCE(i.artist, ''), COALESCE(i.album, ''), COALESCE(i.track_no, 0), COALESCE(i.music_album_id, 0),
	       COALESCE(i.genre, ''), COALESCE(i.year, 0),
	       us.last_played_at
	      FROM items i
	      LEFT JOIN metadata m ON m.id = i.metadata_id
	      LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
	      WHERE 1=1`
	args := []any{f.UserID}
	if len(f.Folders) > 0 {
		// Multi-Ordner-Filter: ODER-Verknüpfung aus (library_id[, rel_path-Präfix])
		// über beliebig viele, auch verschiedene Libraries hinweg. Ersetzt
		// LibraryID/LibraryIDs/Folder komplett (siehe switch f.Folder unten).
		var or []string
		for _, sel := range f.Folders {
			if sel.Folder == "" {
				or = append(or, "i.library_id = ?")
				args = append(args, sel.LibraryID)
			} else {
				or = append(or, "(i.library_id = ? AND i.rel_path LIKE ? ESCAPE '\\')")
				args = append(args, sel.LibraryID, escapeLike(sel.Folder)+"/%")
			}
		}
		if len(or) > 0 {
			q += ` AND (` + strings.Join(or, " OR ") + `)`
		}
	} else if len(f.LibraryIDs) > 0 {
		// Mehrere Libraries (virtuelle Zusammenlegung): IN-Klausel
		ph := make([]string, len(f.LibraryIDs))
		for i, id := range f.LibraryIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		q += ` AND i.library_id IN (` + strings.Join(ph, ",") + `)`
	} else if f.LibraryID > 0 {
		q += ` AND i.library_id = ?`
		args = append(args, f.LibraryID)
	}
	// Sicherheitslücke gefunden 2026-08-22 (User: "beim Benutzer Börnie werden bei der
	// Startseiten-Suche Treffer aus Bibliotheken angezeigt, auf die er gar keinen Zugriff
	// hat"): die obige Library-Eingrenzung (Folders/LibraryIDs/LibraryID) griff nur, wenn
	// der Aufrufer sie explizit gesetzt hat — eine library-übergreifende Suche (Home-View,
	// kein libraryId-Query-Param) lief für Non-Admins komplett UNGEFILTERT über ALLE
	// Bibliotheken der DB, nicht nur die per user_library_access erlaubten. Betraf sowohl
	// `listItems` (Suche) als auch `randomItem` (🎲 Zufall ohne Scope) — beide rufen diese
	// Funktion auf. Fix hier statt einzeln in jedem Handler: ein einziger, nicht zu
	// umgehender Sperrpunkt für JEDEN nicht-Admin-Aufruf mit echter UserID, unabhängig
	// davon, ob/wie der Aufrufer schon eingeschränkt hat (rein additiv, kein Widerspruch
	// zu einer bereits vorhandenen, engeren Einschränkung oben).
	if f.UserID > 0 && !f.IsAdmin {
		// Admin sieht immer alles (siehe Kommentar bei UserHasExplicitLibraryACL
		// in users.go — die Admin-Selbsteinschränkung vom 31.08. wurde am
		// 02.09. zurückgenommen, weil neue Bibliotheken für einen Admin mit
		// eigener ACL-Zeile nirgends mehr auftauchten).
		q += ` AND i.library_id IN (SELECT library_id FROM user_library_access WHERE user_id = ?)`
		args = append(args, f.UserID)
		// forceAdminOnlyLibraries (hardening.go) überstimmt jede ACL-Zeile —
		// no-op, solange nichts konfiguriert ist.
		exclSQL, exclArgs := s.forceAdminOnlyExclusionSQL("i.library_id")
		q += ` AND ` + exclSQL
		args = append(args, exclArgs...)
	}
	if f.Search != "" {
		pattern := "%" + f.Search + "%"
		// Suche auf Schauspielernamen ist teuer (LIKE auf people.name = Full-
		// Scan, plus EXISTS pro Item). Erst ab 3 Zeichen mit dazunehmen —
		// bei 1-2 Buchstaben sind die Cast-Treffer eh nicht hilfreich.
		if len(f.Search) >= 3 {
			q += ` AND (
				i.title LIKE ?
				OR COALESCE(m.title, '') LIKE ?
				OR i.artist LIKE ?
				OR i.album LIKE ?
				OR EXISTS (
					SELECT 1 FROM metadata_cast mc
					JOIN people p ON p.id = mc.person_id
					WHERE p.name LIKE ?
					  AND (mc.metadata_id = i.metadata_id
					       OR mc.metadata_id = (SELECT parent_id FROM metadata WHERE id = i.metadata_id))
				)
			)`
			args = append(args, pattern, pattern, pattern, pattern, pattern)
		} else {
			q += ` AND (i.title LIKE ? OR COALESCE(m.title, '') LIKE ? OR i.artist LIKE ? OR i.album LIKE ?)`
			args = append(args, pattern, pattern, pattern, pattern)
		}
	}
	if !f.DateFrom.IsZero() {
		q += ` AND COALESCE(i.released_at, i.mod_time) >= ?`
		args = append(args, f.DateFrom)
	}
	if !f.DateTo.IsZero() {
		end := time.Date(f.DateTo.Year(), f.DateTo.Month(), f.DateTo.Day(), 23, 59, 59, 999999999, f.DateTo.Location())
		q += ` AND COALESCE(i.released_at, i.mod_time) <= ?`
		args = append(args, end)
	}
	if len(f.Folders) == 0 {
		switch f.Folder {
		case "":
			// kein Filter
		case "/":
			q += ` AND INSTR(i.rel_path, '/') = 0`
		default:
			q += ` AND i.rel_path LIKE ? ESCAPE '\'`
			args = append(args, escapeLike(f.Folder)+"/%")
		}
	}
	switch f.Watched {
	case "yes":
		q += ` AND COALESCE(us.watched, 0) = 1`
	case "no":
		q += ` AND COALESCE(us.watched, 0) = 0`
	}
	if f.Favorite == "yes" {
		q += ` AND COALESCE(us.favorite, 0) = 1`
	}
	switch f.RatingFilter {
	case "unrated":
		q += ` AND COALESCE(us.rating, 0) = 0`
	case "min1":
		q += ` AND COALESCE(us.rating, 0) >= 1`
	case "min2":
		q += ` AND COALESCE(us.rating, 0) >= 2`
	case "exact3":
		q += ` AND COALESCE(us.rating, 0) >= 3`
	}
	switch f.MatchState {
	case "unmatched":
		q += ` AND i.metadata_id IS NULL`
	case "unconfirmed":
		// "Alle Unbestätigten" (User-Wunsch 2026-09-06, vor allem für Filme):
		// Items MIT TMDB-Zuordnung, die aber nie über den ✅-Button bestätigt
		// wurden — bewusst NICHT dasselbe wie "unmatched" (das sind Items OHNE
		// jede Zuordnung, dafür gibt es bereits den eigenen Filter). Deckt sich
		// nicht mit "⚠ Verdächtige Zuordnungen" (Token-Overlap-Heuristik) —
		// hier zählt einzig, ob ✅ je gedrückt wurde, unabhängig davon, wie
		// plausibel die Zuordnung aussieht.
		q += ` AND i.metadata_id IS NOT NULL AND COALESCE(i.metadata_confirmed, 0) = 0`
	}
	if f.MetadataID > 0 {
		q += ` AND i.metadata_id = ?`
		args = append(args, f.MetadataID)
	}
	if f.TrickplayStatus != "" {
		q += ` AND COALESCE(i.trickplay_status, '') = ?`
		args = append(args, f.TrickplayStatus)
	}
	if f.DupesOnly {
		// Items, deren metadata_id global (über alle Libraries hinweg) mehrfach
		// vorkommt — konsistent zum globalen ×N-Badge aus attachVariantCounts.
		// Vorher war die Subquery library-scoped, dadurch wurden Filme NICHT
		// als Duplikat erkannt, deren zweite Version in einer anderen Library
		// lag (typisch: Bluray + Filme parallel) — obwohl die Kachel im
		// Standard-View ×2 zeigte. Library-Filter passiert weiterhin im
		// äußeren WHERE (libraryId), nur die Duplikat-Erkennung ist global.
		q += ` AND i.metadata_id IS NOT NULL AND i.metadata_id IN (
			SELECT metadata_id FROM items
			WHERE metadata_id IS NOT NULL
			GROUP BY metadata_id HAVING COUNT(*) > 1
		)`
	}
	if f.FileDupesOnly {
		// Gleicher Scope wie die äußere Query (Library/Libraries + Folder),
		// damit ein "Duplikat" nur zählt, wenn beide Kopien im gerade
		// betrachteten Bereich liegen (Library-Root = ganze Lib, Unterordner =
		// nur dessen Inhalt rekursiv — Konvention wie bei den Flat-Sorts).
		scope := ` WHERE size_bytes > 0 AND duration_sec > 0`
		var scopeArgs []any
		if len(f.LibraryIDs) > 0 {
			ph := make([]string, len(f.LibraryIDs))
			for i, id := range f.LibraryIDs {
				ph[i] = "?"
				scopeArgs = append(scopeArgs, id)
			}
			scope += ` AND library_id IN (` + strings.Join(ph, ",") + `)`
		} else if f.LibraryID > 0 {
			scope += ` AND library_id = ?`
			scopeArgs = append(scopeArgs, f.LibraryID)
		}
		if f.Folder != "" {
			scope += ` AND rel_path LIKE ? ESCAPE '\'`
			scopeArgs = append(scopeArgs, escapeLike(f.Folder)+"/%")
		}
		q += ` AND i.size_bytes > 0 AND i.duration_sec > 0 AND (i.size_bytes || ':' || i.duration_sec) IN (
			SELECT size_bytes || ':' || duration_sec FROM items` + scope + `
			GROUP BY size_bytes, duration_sec HAVING COUNT(*) > 1
		)`
		args = append(args, scopeArgs...)
	}
	if f.MinHeight > 0 {
		q += ` AND i.height >= ?`
		args = append(args, f.MinHeight)
	}
	if f.MaxHeight > 0 {
		q += ` AND i.height <= ?`
		args = append(args, f.MaxHeight)
	}
	if len(f.ResBuckets) > 0 {
		// Effektive Höhe: max(height, width*9/16). Damit landen Cinemascope-
		// Filme (1920×800) im 1080p-Bucket statt im 720p-Bucket, basierend
		// auf der horizontalen Auflösung.
		type rng struct{ min, max int }
		buckets := map[string]rng{
			"4k": {2000, 0}, "2k": {1400, 1999},
			"1080p": {1000, 1399}, "720p": {700, 999},
			"576p": {540, 699}, "540p": {500, 539},
			"480p": {440, 499}, "360p": {0, 439},
		}
		var or []string
		const effH = "MAX(i.height, (i.width * 9 / 16))"
		for _, b := range f.ResBuckets {
			r, ok := buckets[b]
			if !ok {
				continue
			}
			switch {
			case r.min > 0 && r.max > 0:
				or = append(or, "("+effH+" >= ? AND "+effH+" <= ?)")
				args = append(args, r.min, r.max)
			case r.min > 0:
				or = append(or, "("+effH+" >= ?)")
				args = append(args, r.min)
			case r.max > 0:
				or = append(or, "("+effH+" <= ?)")
				args = append(args, r.max)
			}
		}
		if len(or) > 0 {
			q += ` AND (` + strings.Join(or, " OR ") + `)`
		}
	}
	if len(f.Genres) > 0 {
		var or []string
		for _, g := range f.Genres {
			or = append(or, "(m.genres LIKE ? ESCAPE '\\' OR i.genre = ?)")
			args = append(args, "%\""+escapeLike(g)+"\"%", g)
		}
		if len(or) > 0 {
			q += ` AND (` + strings.Join(or, " OR ") + `)`
		}
	}
	if f.PersonTMDB > 0 {
		// Person-Filter: EXISTS in metadata_cast mit Match auf Item-Metadata
		// oder (bei Episoden) auf Parent-Show-Metadata. Person-ID wird intern
		// über people.tmdb_id aufgelöst.
		q += ` AND EXISTS (
			SELECT 1 FROM metadata_cast mc
			JOIN people p ON p.id = mc.person_id
			WHERE p.tmdb_id = ?
			  AND (mc.metadata_id = i.metadata_id
			       OR mc.metadata_id = (SELECT parent_id FROM metadata WHERE id = i.metadata_id))
		)`
		args = append(args, f.PersonTMDB)
	}
	if f.PlaylistID > 0 {
		// Playlist-Filter (fuer Shuffle in Playlist-Ansicht). Per EXISTS, damit
		// pro Item nur eine Zeile zurueckkommt — bei JOIN wuerden Items, die in
		// mehreren Playlists liegen, dupliziert werden.
		q += ` AND EXISTS (
			SELECT 1 FROM playlist_items pi
			WHERE pi.item_id = i.id AND pi.playlist_id = ?
		)`
		args = append(args, f.PlaylistID)
	}
	if f.MusicAlbumID > 0 {
		q += ` AND i.music_album_id = ?`
		args = append(args, f.MusicAlbumID)
	}
	if f.ExcludeAudiobooks {
		q += ` AND i.container != 'm4b'`
	}
	if f.MaxAgeRating > 0 {
		// FSK-Filter: Items mit numerisch höherer age_rating als das User-
		// Limit werden ausgeblendet. age_rating ist TEXT ("0", "6", "12", ...),
		// für numerischen Vergleich in INTEGER casten. Leere age_rating
		// (unbekannt) bleibt sichtbar — Filter greift nur auf explizit hoch
		// markierte Inhalte. Bei Episoden zählt die Parent-Show-FSK als
		// Fallback, falls die Episode selbst keine hat.
		q += ` AND COALESCE(
			NULLIF((SELECT CAST(age_rating AS INTEGER) FROM metadata WHERE id = i.metadata_id AND age_rating <> ''), 0),
			NULLIF((SELECT CAST(p.age_rating AS INTEGER) FROM metadata m
			          LEFT JOIN metadata p ON p.id = m.parent_id
			         WHERE m.id = i.metadata_id AND p.age_rating IS NOT NULL AND p.age_rating <> ''), 0),
			0
		) <= ?`
		args = append(args, f.MaxAgeRating)
	}
	// "Zuletzt abgespielt" impliziert: nur Items mit einem tatsächlichen
	// last_played_at. Items ohne Play-Historie ans Ende zu hängen wäre für
	// das UI-Flat-View unbrauchbar — dort sollen ausschließlich abgespielte
	// Videos erscheinen.
	if f.Sort == "played" {
		q += ` AND us.last_played_at IS NOT NULL`
	}
	// Interlaced-Filter: nur Items mit mindestens einem Video-Stream, dessen
	// field_order auf Halbbilder hinweist (tt/bb/tb/bt). „progressive" und
	// „unknown" / leer werden ausgeschlossen. Setzt voraus, dass der Scanner
	// das Feld bereits gefüllt hat (Force-Scan für Bestand).
	if f.Interlaced {
		q += ` AND EXISTS (
			SELECT 1 FROM item_streams s
			WHERE s.item_id = i.id
			  AND s.type = 'video'
			  AND s.field_order IS NOT NULL
			  AND s.field_order <> ''
			  AND s.field_order <> 'progressive'
			  AND s.field_order <> 'unknown'
		)`
	}
	// Richtung: "asc" flippt die natürliche Sortierreihenfolge (nur bei stabilen Sorts
	// wirksam; bei "random" ignoriert).
	asc := f.SortDir == "asc"
	desc := f.SortDir == "desc"
	switch f.Sort {
	case "duration":
		if asc {
			q += ` ORDER BY i.duration_sec ASC`
		} else {
			q += ` ORDER BY i.duration_sec DESC`
		}
	case "resolution":
		// Effektive Höhe (max(height, width*9/16)) — siehe Bucket-Filter.
		// Default desc: höchste Auflösung zuerst.
		if asc {
			q += ` ORDER BY MAX(i.height, (i.width * 9 / 16)) ASC, i.bitrate_kbps ASC`
		} else {
			q += ` ORDER BY MAX(i.height, (i.width * 9 / 16)) DESC, i.bitrate_kbps DESC`
		}
	case "rating":
		// TMDB-Bewertung (metadata.rating). Items ohne Metadata oder mit
		// Rating=0 (nicht gewertet) ans Ende, damit interessante Filme oben
		// stehen. Bei Episoden hängen wir uns an die Parent-Show-Bewertung,
		// da die Episode-Bewertung in TMDB kaum gepflegt ist.
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
		// Bevorzugt das TMDB-Release-Datum (m.release_date) — das ist auch
		// was die Kachel als Jahr zeigt. Fallback auf m.year (manche
		// Metadata-Einträge haben nur das Jahr, kein volles Datum), dann
		// auf i.released_at (ffprobe creation_time / yt-dlp DATE-Tag),
		// schließlich i.mod_time. Für Episoden zusätzlich Parent-Show-
		// release-Date als Fallback, damit alle Folgen einer Show
		// chronologisch nahe beieinander stehen wenn die Episoden-
		// Metadata kein Datum hat.
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
	case "episode":
		if desc {
			q += ` ORDER BY COALESCE(m.season, -1) DESC, COALESCE(m.episode, -1) DESC, i.title COLLATE NOCASE DESC`
		} else {
			q += ` ORDER BY COALESCE(m.season, 999999), COALESCE(m.episode, 999999), i.title COLLATE NOCASE`
		}
	case "played":
		// Zuletzt abgespielt: aus user_item_state.last_played_at (pro User).
		// Noch nie angespielt → nach hinten (desc) bzw. nach vorne (asc).
		if asc {
			q += ` ORDER BY us.last_played_at IS NULL, us.last_played_at ASC`
		} else {
			q += ` ORDER BY us.last_played_at IS NULL, us.last_played_at DESC`
		}
	case "random":
		// Zufällige Reihenfolge. Mit LIMIT 20, damit ORDER BY RANDOM() nicht bei
		// großen Bibliotheken alle Zeilen sortieren muss.
		q += ` ORDER BY RANDOM() LIMIT 20`
	case "artist":
		// Nur für Musik-Bibliotheken relevant (Sort-Dropdown zeigt diese Option
		// nur bei kind=music, siehe grid.js). Album+Track-Nr als Sekundär-/
		// Tertiär-Schlüssel, damit ein Künstler mit mehreren Alben nicht
		// durcheinandergewürfelt wird.
		if desc {
			q += ` ORDER BY i.artist COLLATE NATSORT DESC, i.album COLLATE NATSORT DESC, i.track_no DESC`
		} else {
			q += ` ORDER BY i.artist COLLATE NATSORT, i.album COLLATE NATSORT, i.track_no`
		}
	case "album":
		if desc {
			q += ` ORDER BY i.album COLLATE NATSORT DESC, i.track_no DESC`
		} else {
			q += ` ORDER BY i.album COLLATE NATSORT, i.track_no`
		}
	case "filename":
		// Sortiert IMMER nach dem physischen Dateinamen (i.title), nie nach
		// einem eventuellen TMDB-Titel (m.title) — anders als der Default-Fall
		// unten. User-Anfrage 2026-09-05: Serien wie "Tatort", deren Dateinamen
		// Jahr+Episodennummer tragen (z.B. "Tatort - 1094 - ..."), aber deren
		// einzelne Folgen oft NICHT TMDB-episode-gematcht sind, sollen sich
		// zuverlässig chronologisch nach genau diesem Namensschema sortieren
		// lassen — unabhängig davon, ob einzelne Dateien in derselben Liste
		// zufällig doch einen TMDB-Titel bekommen haben (das würde bei
		// "title"/Default sonst die Reihenfolge durchbrechen). NATSORT sortiert
		// die eingebettete Nummer numerisch, nicht lexikografisch.
		if desc {
			q += ` ORDER BY i.title COLLATE NATSORT DESC`
		} else {
			q += ` ORDER BY i.title COLLATE NATSORT`
		}
	default:
		// Wenn TMDB-Metadata existiert, nach dem angezeigten Titel sortieren,
		// sonst nach items.title. Sonst kommen Dateinamen mit Release-Präfix
		// ("a-complete-unknown-1080p") unerwartet vor "Alex" o. ä.
		if desc {
			q += ` ORDER BY COALESCE(NULLIF(m.title, ''), i.title) COLLATE NATSORT DESC`
		} else {
			q += ` ORDER BY COALESCE(NULLIF(m.title, ''), i.title) COLLATE NATSORT`
		}
	}
	// Frueher: bei aktiver Suche LIMIT 300 (LIKE+EXISTS-Cast-Query ist
	// quadratisch). Auf Wunsch des Users entfernt — Suche liefert jetzt ALLE
	// Treffer. Bei sehr kurzen Queries kann das viele Zeilen sein.
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb, watched, favorite, variantSplit int
		var released sql.NullString
		var watchedAt, favoritedAt, lastPlayedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus, &it.EpisodeEnd, &variantSplit, &it.Rating,
			&it.Artist, &it.Album, &it.TrackNo, &it.MusicAlbumID, &it.Genre, &it.Year, &lastPlayedAt); err != nil {
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
		if lastPlayedAt.Valid {
			t := lastPlayedAt.Time
			it.LastPlayedAt = &t
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

// SearchItemsByPath sucht Items, deren Pfad/Dateiname den Suchstring enthält.
// Unabhängig von TMDB-Matching — ideal um falsch zugeordnete Dateien zu
// finden. Liefert maximal `limit` Treffer.
func (s *Store) SearchItemsByPath(q string, limit int) ([]model.Item, error) {
	if limit <= 0 {
		limit = 200
	}
	pat := "%" + escapeLike(strings.ReplaceAll(q, "\\", "\\\\")) + "%"
	rows, err := s.db.Query(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(i.trickplay_status, '')
		FROM items i
		WHERE i.rel_path LIKE ? ESCAPE '\' OR i.path LIKE ? ESCAPE '\' OR i.title LIKE ? ESCAPE '\'
		ORDER BY i.rel_path COLLATE NOCASE
		LIMIT ?
	`, pat, pat, pat, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var released, modT sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title,
			&it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps,
			&it.ThumbPath, &it.HasThumb, &modT, &released, &it.AddedAt,
			&it.MetadataID, &it.TrickplayStatus); err != nil {
			return nil, err
		}
		if modT.Valid {
			it.ModTime = modT.Time
		}
		if released.Valid {
			it.ReleasedAt = released.Time
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.attachMetadata(out)
	return out, nil
}

// attachMetadata lädt die verlinkten metadata-Einträge zu einer Item-Liste.
func (s *Store) attachMetadata(items []model.Item) {
	ids := map[int64]struct{}{}
	for _, it := range items {
		if it.MetadataID > 0 {
			ids[it.MetadataID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	q := `SELECT id, tmdb_type, tmdb_id, COALESCE(parent_id,0), title, COALESCE(original_title,''),
		COALESCE(year,0), release_date, COALESCE(overview,''), COALESCE(rating,0), COALESCE(genres,''),
		COALESCE(runtime_min,0), COALESCE(poster_path,''), COALESCE(backdrop_path,''),
		COALESCE(season,0), COALESCE(episode,0), COALESCE(imdb_id,''), COALESCE(age_rating,''), updated_at
		FROM metadata WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		// Bewusst kein Logging hier — das store-Package loggt grundsätzlich
		// nie selbst (kein einziges "log"-Import im ganzen Package, Logging
		// ist Aufgabe der Aufrufer). attachMetadata ist reine Anreicherung:
		// schlägt der JOIN fehl, bleiben die Items ohne Poster/Titel-Override
		// nutzbar (Fallback auf den rohen Dateinamen), kein harter Fehler.
		return
	}
	defer func() { _ = rows.Close() }()
	byID := map[int64]*model.Metadata{}
	for rows.Next() {
		var m model.Metadata
		var rd sql.NullTime
		if err := rows.Scan(&m.ID, &m.TMDBType, &m.TMDBID, &m.ParentID, &m.Title, &m.OriginalTitle,
			&m.Year, &rd, &m.Overview, &m.Rating, &m.Genres, &m.RuntimeMin, &m.PosterPath,
			&m.BackdropPath, &m.Season, &m.Episode, &m.IMDBID, &m.AgeRating, &m.UpdatedAt); err != nil {
			continue
		}
		if rd.Valid {
			m.ReleaseDate = rd.Time
		}
		byID[m.ID] = &m
	}
	for i := range items {
		if items[i].MetadataID > 0 {
			if m := byID[items[i].MetadataID]; m != nil {
				items[i].Metadata = m
			}
		}
	}
}

// attachVariantCounts setzt für jedes Item mit metadata_id die Anzahl aller
// Items, die sich diese metadata_id teilen (= dieses Item + Geschwister).
// Damit kann das Frontend den ×N-Badge auch dann anzeigen, wenn das
// Geschwister in einer anderen Library liegt und im aktuellen Render nicht
// vorkommt. Globale Zählung (kein ACL-Filter): für Admins exakt, für
// non-Admin-User ggf. leicht überzählt — wird als weicher Hint akzeptiert,
// die ACL-gefilterte Variants-Liste liefert ohnehin der Variants-Endpoint.
// Items mit count<=1 bekommen 0 (Badge bleibt aus).
func (s *Store) attachVariantCounts(items []model.Item) {
	ids := map[int64]struct{}{}
	for _, it := range items {
		if it.MetadataID > 0 {
			ids[it.MetadataID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	// variant_split-Items zählen nicht mit — sie sind bewusst aus der
	// Gruppierung genommen und sollen weder selbst einen ×N-Badge zeigen
	// noch die Zählung der verbleibenden Geschwister aufblähen.
	rows, err := s.db.Query(`
		SELECT metadata_id, COUNT(*) FROM items
		WHERE metadata_id IN (`+strings.Join(placeholders, ",")+`) AND COALESCE(variant_split, 0) = 0
		GROUP BY metadata_id`, args...)
	if err != nil {
		// Bewusst kein Logging — siehe attachMetadata-Kommentar oben. Schlägt
		// der Query fehl, bleibt einfach der ×N-Badge aus, kein harter Fehler.
		return
	}
	defer func() { _ = rows.Close() }()
	counts := map[int64]int{}
	for rows.Next() {
		var mid int64
		var n int
		if err := rows.Scan(&mid, &n); err != nil {
			continue
		}
		if n > 1 {
			counts[mid] = n
		}
	}
	for i := range items {
		if items[i].MetadataID > 0 && !items[i].VariantSplit {
			if n, ok := counts[items[i].MetadataID]; ok {
				items[i].VariantCount = n
			}
		}
	}
}

// escapeLike escaped \, %, _ für LIKE-Pattern (Escape-Char: Backslash).
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// DistinctYears liefert alle Jahre (released_at / mod_time) als sortierte Liste, absteigend.
func (s *Store) DistinctYears(libraryID int64) ([]int, error) {
	q := `SELECT DISTINCT CAST(strftime('%Y', COALESCE(released_at, mod_time)) AS INTEGER) AS y
	      FROM items WHERE 1=1`
	args := []any{}
	if libraryID > 0 {
		q += ` AND library_id = ?`
		args = append(args, libraryID)
	}
	q += ` AND y IS NOT NULL ORDER BY y DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var years []int
	for rows.Next() {
		var y sql.NullInt64
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		if y.Valid && y.Int64 > 0 {
			years = append(years, int(y.Int64))
		}
	}
	return years, rows.Err()
}

func (s *Store) GetItem(id int64) (*model.Item, error) { return s.GetItemFor(0, id) }

// GetItemFor liefert ein Item inkl. per-User-Zustand (watched/favorite aus user_item_state).
// userID=0 liefert globale Defaults (für Worker-Kontext).
func (s *Store) GetItemFor(userID, id int64) (*model.Item, error) {
	var it model.Item
	var hasThumb, watched, favorite, variantSplit int
	var released sql.NullString
	var watchedAt, favoritedAt sql.NullTime
	var confirmed int
	var introStart, introEnd sql.NullFloat64
	err := s.db.QueryRow(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(i.metadata_confirmed, 0),
		       COALESCE(us.watched, 0), us.watched_at, COALESCE(us.favorite, 0), us.favorited_at,
		       COALESCE(i.trickplay_status, ''),
		       COALESCE(i.episode_end, 0),
		       COALESCE(i.variant_split, 0),
		       i.intro_start_sec, i.intro_end_sec,
		       COALESCE(us.rating, 0),
		       COALESCE(i.artist, ''), COALESCE(i.album, ''), COALESCE(i.track_no, 0), COALESCE(i.music_album_id, 0),
		       COALESCE(i.genre, ''), COALESCE(i.year, 0)
		FROM items i
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE i.id = ?`, userID, id).
		Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&confirmed,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus, &it.EpisodeEnd, &variantSplit,
			&introStart, &introEnd, &it.Rating,
			&it.Artist, &it.Album, &it.TrackNo, &it.MusicAlbumID, &it.Genre, &it.Year)
	it.MetadataConfirmed = confirmed == 1
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
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
	if introStart.Valid {
		v := introStart.Float64
		it.IntroStartSec = &v
	}
	if introEnd.Valid {
		v := introEnd.Float64
		it.IntroEndSec = &v
	}
	if it.MetadataID > 0 {
		if m, _ := s.GetMetadata(it.MetadataID); m != nil {
			it.Metadata = m
		}
		single := []model.Item{it}
		s.attachVariantCounts(single)
		it.VariantCount = single[0].VariantCount
	}
	// Streams mitliefern — UI braucht u.a. das field_order der Video-Streams,
	// um den 🪤-Interlaced-Hinweis im Detail-Dialog zu zeigen, und das Player-
	// Dropdown nutzt sie für Audio-/Subtitle-Auswahl.
	if streams, err := s.ItemStreams(it.ID); err == nil {
		it.Streams = streams
	}
	return &it, nil
}

// SetWatched markiert ein Item als gesehen/ungesehen.
func (s *Store) SetWatched(itemID int64, watched bool) error {
	if watched {
		_, err := s.db.Exec(`UPDATE items SET watched = 1, watched_at = ? WHERE id = ?`, time.Now(), itemID)
		return err
	}
	_, err := s.db.Exec(`UPDATE items SET watched = 0, watched_at = NULL WHERE id = ?`, itemID)
	return err
}

// SetFavorite markiert ein Item als Favorit.
func (s *Store) SetFavorite(itemID int64, favorite bool) error {
	if favorite {
		_, err := s.db.Exec(`UPDATE items SET favorite = 1, favorited_at = ? WHERE id = ?`, time.Now(), itemID)
		return err
	}
	_, err := s.db.Exec(`UPDATE items SET favorite = 0, favorited_at = NULL WHERE id = ?`, itemID)
	return err
}
