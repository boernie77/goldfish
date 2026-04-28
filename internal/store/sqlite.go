package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	baseStmts := []string{
		`CREATE TABLE IF NOT EXISTS libraries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			path TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL DEFAULT 'private',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS metadata (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tmdb_type TEXT NOT NULL,
			tmdb_id INTEGER NOT NULL,
			parent_id INTEGER REFERENCES metadata(id) ON DELETE SET NULL,
			title TEXT NOT NULL,
			original_title TEXT,
			year INTEGER,
			release_date DATETIME,
			overview TEXT,
			rating REAL,
			genres TEXT,
			runtime_min INTEGER,
			poster_path TEXT,
			backdrop_path TEXT,
			season INTEGER,
			episode INTEGER,
			imdb_id TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tmdb_type, tmdb_id, season, episode)
		)`,
		`CREATE INDEX IF NOT EXISTS metadata_tmdb_idx ON metadata(tmdb_type, tmdb_id)`,
		`CREATE INDEX IF NOT EXISTS metadata_parent_idx ON metadata(parent_id)`,
		`CREATE TABLE IF NOT EXISTS folder_metadata (
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			folder TEXT NOT NULL,
			metadata_id INTEGER REFERENCES metadata(id) ON DELETE SET NULL,
			PRIMARY KEY (library_id, folder)
		)`,
		`CREATE TABLE IF NOT EXISTS library_paths (
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			path TEXT NOT NULL UNIQUE,
			PRIMARY KEY (library_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS trickplay_folders (
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			folder TEXT NOT NULL,
			PRIMARY KEY (library_id, folder)
		)`,
		`CREATE TABLE IF NOT EXISTS folder_nav (
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			folder TEXT NOT NULL,
			drilldown INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (library_id, folder)
		)`,
		`CREATE TABLE IF NOT EXISTS playlists (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS playlist_items (
			playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			PRIMARY KEY (playlist_id, item_id)
		)`,
		`CREATE INDEX IF NOT EXISTS playlist_items_pos_idx ON playlist_items(playlist_id, position)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id)`,
		`CREATE TABLE IF NOT EXISTS user_library_access (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, library_id)
		)`,
		`CREATE TABLE IF NOT EXISTS item_streams (
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			stream_index INTEGER NOT NULL,
			type TEXT NOT NULL,
			codec TEXT,
			language TEXT,
			title TEXT,
			channels INTEGER,
			is_default INTEGER NOT NULL DEFAULT 0,
			is_forced INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (item_id, stream_index)
		)`,
		`CREATE INDEX IF NOT EXISTS item_streams_item_idx ON item_streams(item_id)`,
		`CREATE TABLE IF NOT EXISTS user_item_state (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			watched INTEGER NOT NULL DEFAULT 0,
			watched_at DATETIME,
			favorite INTEGER NOT NULL DEFAULT 0,
			favorited_at DATETIME,
			PRIMARY KEY (user_id, item_id)
		)`,
		`CREATE INDEX IF NOT EXISTS user_item_state_watched_idx ON user_item_state(user_id, watched)`,
		`CREATE INDEX IF NOT EXISTS user_item_state_favorite_idx ON user_item_state(user_id, favorite)`,
		`CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			path TEXT NOT NULL UNIQUE,
			rel_path TEXT NOT NULL,
			title TEXT NOT NULL,
			container TEXT,
			video_codec TEXT,
			audio_codec TEXT,
			width INTEGER,
			height INTEGER,
			duration_sec REAL,
			size_bytes INTEGER,
			bitrate_kbps INTEGER,
			thumb_path TEXT,
			has_thumb INTEGER DEFAULT 0,
			mod_time DATETIME,
			released_at DATETIME,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS items_library_idx ON items(library_id)`,
		`CREATE INDEX IF NOT EXISTS items_title_idx ON items(title)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS people (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tmdb_id INTEGER NOT NULL UNIQUE,
			name TEXT NOT NULL,
			profile_path TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS metadata_cast (
			metadata_id INTEGER NOT NULL REFERENCES metadata(id) ON DELETE CASCADE,
			person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
			character TEXT,
			role TEXT NOT NULL DEFAULT 'main',
			ord INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (metadata_id, person_id, role)
		)`,
		`CREATE INDEX IF NOT EXISTS metadata_cast_meta_idx ON metadata_cast(metadata_id, ord)`,
		`CREATE INDEX IF NOT EXISTS metadata_cast_person_idx ON metadata_cast(person_id)`,
		`CREATE TABLE IF NOT EXISTS collections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tmdb_id INTEGER NOT NULL UNIQUE,
			name TEXT NOT NULL,
			poster_path TEXT,
			backdrop_path TEXT,
			overview TEXT,
			parts_fetched_at DATETIME,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Alle Filme einer Sammlung laut TMDB (auch die, die der User nicht hat).
		// Wird angezeigt mit Fehlt-Badge, damit man die Sammlung vollständig sieht.
		`CREATE TABLE IF NOT EXISTS collection_parts (
			collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			tmdb_movie_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			release_date TEXT,
			poster_path TEXT,
			ord INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (collection_id, tmdb_movie_id)
		)`,
		`CREATE INDEX IF NOT EXISTS collection_parts_col_idx ON collection_parts(collection_id, ord)`,
		// Per-User Ausblenden einzelner Sammlungs-Parts, z.B. Home Alone 3 in der
		// Kevin-Sammlung. Wird via UI-Button gesetzt und kann wieder aufgehoben
		// werden.
		`CREATE TABLE IF NOT EXISTS hidden_collection_parts (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			tmdb_movie_id INTEGER NOT NULL,
			hidden_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, collection_id, tmdb_movie_id)
		)`,
	}
	for _, q := range baseStmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Schema-Evolution: fehlende Spalten idempotent nachziehen
	addCol := func(table, col, def string) error {
		q := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, def)
		if _, err := s.db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate %s.%s: %w", table, col, err)
		}
		return nil
	}
	if err := addCol("items", "metadata_confirmed", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addCol("items", "released_at", "DATETIME"); err != nil {
		return err
	}
	if err := addCol("items", "metadata_id", "INTEGER REFERENCES metadata(id) ON DELETE SET NULL"); err != nil {
		return err
	}
	if err := addCol("items", "watched", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addCol("items", "watched_at", "DATETIME"); err != nil {
		return err
	}
	if err := addCol("items", "favorite", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addCol("items", "favorited_at", "DATETIME"); err != nil {
		return err
	}
	// Trickplay-Status: "" = nicht aktiviert/generiert, "pending", "done", "failed"
	if err := addCol("items", "trickplay_status", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Trickplay-Fehlermeldung bei status="failed" (ffmpeg-stderr o. ä.)
	if err := addCol("items", "trickplay_error", "TEXT"); err != nil {
		return err
	}
	// Pro-User "zuletzt abgespielt" — wird beim Öffnen des Players gesetzt.
	if err := addCol("user_item_state", "last_played_at", "DATETIME"); err != nil {
		return err
	}
	// TMDB-Collection-Zuordnung (z. B. alle James Bond Filme)
	if err := addCol("metadata", "collection_id", "INTEGER REFERENCES collections(id) ON DELETE SET NULL"); err != nil {
		return err
	}
	// Markiert, wann zuletzt versucht wurde, Cast für diese Metadata zu laden.
	// Auch wenn TMDB keine Cast-Daten liefert, setzen wir das Feld, damit wir
	// nicht in jedem Backfill-Lauf denselben leeren Abruf wiederholen.
	if err := addCol("metadata", "cast_fetched_at", "DATETIME"); err != nil {
		return err
	}
	// Analog: Marker für Collection-Check (belongs_to_collection von TMDB).
	if err := addCol("metadata", "collection_checked_at", "DATETIME"); err != nil {
		return err
	}
	// Resume-Position pro User+Item — bei Pause/Close gesetzt, beim Öffnen abgefragt.
	if err := addCol("user_item_state", "resume_pos_sec", "REAL"); err != nil {
		return err
	}
	// Collections-Felder (idempotent nachziehen, falls Tabelle schon existiert).
	if err := addCol("collections", "overview", "TEXT"); err != nil {
		return err
	}
	if err := addCol("collections", "parts_fetched_at", "DATETIME"); err != nil {
		return err
	}
	// Pro-User-Playlist: existing rows bekommen user_id=NULL (interpretiert als "alt")
	if err := addCol("playlists", "user_id", "INTEGER REFERENCES users(id) ON DELETE CASCADE"); err != nil {
		return err
	}
	if err := addCol("libraries", "kind", "TEXT NOT NULL DEFAULT 'private'"); err != nil {
		return err
	}
	if err := addCol("libraries", "on_home", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	// Doppelfolgen: "S07E23E24.mkv" wird auf E23 gematcht; episode_end trägt die
	// letzte Episode der Range (24). 0 = keine Range. Staffel-Ansicht markiert
	// E23 UND E24 als owned (gleiches Item).
	if err := addCol("items", "episode_end", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// Altersfreigabe (FSK): "", "0", "6", "12", "16", "18". Leer = nicht gesetzt.
	// Manuell editierbar im Metadata-Dialog. Wirkt zusammen mit users.max_age_rating.
	if err := addCol("metadata", "age_rating", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Pro-User Altersgrenze: NULL = keine Beschränkung. Sonst max erlaubte FSK
	// (0/6/12/16/18). Items mit höherem age_rating werden ausgeblendet und
	// Playback-Endpoints liefern 403.
	if err := addCol("users", "max_age_rating", "INTEGER"); err != nil {
		return err
	}
	// OIDC-Subject-Claim (z.B. Email aus Authentik). Nullable, partial-unique:
	// nur gesetzte Werte müssen unique sein, NULL bleibt für lokale Logins.
	if err := addCol("users", "oidc_subject", "TEXT"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc_subject ON users(oidc_subject) WHERE oidc_subject IS NOT NULL`); err != nil {
		return err
	}
	// field_order pro Video-Stream — leer/„progressive" = Bild ok, sonst
	// interlaced (tt/bb/tb/bt). Wird vom Detail-Dialog als „🪤 Interlaced"-
	// Hinweis genutzt; künftiger Deinterlace-Filter im Transcode-Pfad liest
	// dasselbe Feld.
	if err := addCol("item_streams", "field_order", "TEXT"); err != nil {
		return err
	}
	// Indizes erst nach ALTER
	idxStmts := []string{
		`CREATE INDEX IF NOT EXISTS items_released_idx ON items(released_at)`,
		`CREATE INDEX IF NOT EXISTS items_metadata_idx ON items(metadata_id)`,
		`CREATE INDEX IF NOT EXISTS items_watched_idx ON items(library_id, watched)`,
		// Prefix-LIKE auf rel_path nutzt diesen Index → schnelle Folder-Queries
		`CREATE INDEX IF NOT EXISTS items_lib_relpath_idx ON items(library_id, rel_path)`,
		// Sort-Queries nutzen diese Indizes
		`CREATE INDEX IF NOT EXISTS items_lib_added_idx ON items(library_id, added_at)`,
		`CREATE INDEX IF NOT EXISTS items_lib_duration_idx ON items(library_id, duration_sec)`,
		`CREATE INDEX IF NOT EXISTS items_lib_height_idx ON items(library_id, height)`,
		// user_item_state-Abfragen (per-User Sort/Filter)
		`CREATE INDEX IF NOT EXISTS user_item_state_last_played_idx ON user_item_state(user_id, last_played_at)`,
		// Collections — für ListCollections/fallback_meta_id-Subqueries
		`CREATE INDEX IF NOT EXISTS metadata_collection_idx ON metadata(collection_id)`,
	}
	// Backfill: Für jede existierende Library wird ihr "path" in library_paths gespiegelt
	// (falls noch nicht vorhanden). So funktionieren bestehende Bibliotheken out-of-the-box.
	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO library_paths(library_id, path)
		SELECT id, path FROM libraries
	`); err != nil {
		return fmt.Errorf("migrate library_paths backfill: %w", err)
	}
	for _, q := range idxStmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate index: %w", err)
		}
	}
	return nil
}

// --- Libraries ---

func (s *Store) ListLibraries() ([]model.Library, error) {
	rows, err := s.db.Query(`SELECT id, name, path, kind, COALESCE(on_home, 1), created_at FROM libraries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Library
	for rows.Next() {
		var l model.Library
		var kind string
		var onHome int
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &kind, &onHome, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Kind = model.LibraryKind(kind)
		l.OnHome = onHome == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetLibrary(id int64) (*model.Library, error) {
	var l model.Library
	var kind string
	var onHome int
	err := s.db.QueryRow(`SELECT id, name, path, kind, COALESCE(on_home, 1), created_at FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Path, &kind, &onHome, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	l.Kind = model.LibraryKind(kind)
	l.OnHome = onHome == 1
	return &l, err
}

func (s *Store) CreateLibrary(name, path string, kind model.LibraryKind) (int64, error) {
	if kind == "" {
		kind = model.KindPrivate
	}
	res, err := s.db.Exec(`INSERT INTO libraries(name, path, kind) VALUES(?, ?, ?)`, name, path, string(kind))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	// Primary-Path auch in library_paths eintragen
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO library_paths(library_id, path) VALUES(?, ?)`, id, path); err != nil {
		return 0, err
	}
	return id, nil
}

// LibraryPaths liefert alle zur Library zugeordneten Quellordner.
func (s *Store) LibraryPaths(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM library_paths WHERE library_id = ? ORDER BY path`, libraryID)
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
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddLibraryPath fügt einen zusätzlichen Quellordner hinzu.
func (s *Store) AddLibraryPath(libraryID int64, path string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO library_paths(library_id, path) VALUES(?, ?)`, libraryID, path)
	return err
}

// DeleteLibraryPath entfernt einen Quellordner. Items aus diesem Pfad werden beim
// nächsten Scan als "weg" erkannt und aus der DB entfernt.
func (s *Store) DeleteLibraryPath(libraryID int64, path string) error {
	_, err := s.db.Exec(`DELETE FROM library_paths WHERE library_id = ? AND path = ?`, libraryID, path)
	return err
}

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

func (s *Store) UpdateLibraryKind(id int64, kind model.LibraryKind) error {
	_, err := s.db.Exec(`UPDATE libraries SET kind = ? WHERE id = ?`, string(kind), id)
	return err
}

func (s *Store) DeleteLibrary(id int64) error {
	_, err := s.db.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	return err
}

// --- Items ---

func (s *Store) UpsertItem(it *model.Item) error {
	_, err := s.db.Exec(`
		INSERT INTO items(library_id, path, rel_path, title, container, video_codec, audio_codec, width, height, duration_sec, size_bytes, bitrate_kbps, thumb_path, has_thumb, mod_time, released_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
			released_at=excluded.released_at
	`,
		it.LibraryID, it.Path, it.RelPath, it.Title, it.Container, it.VideoCodec, it.AudioCodec,
		it.Width, it.Height, it.DurationSec, it.SizeBytes, it.BitrateKbps, it.ThumbPath, boolToInt(it.HasThumb), it.ModTime, it.ReleasedAt,
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

// ListTVFoldersForLibrary liefert ALLE Top-Level-Folder einer TV-Library, die
// mindestens ein gematchten Episoden-Item enthalten. Genutzt vom Missing-Export
// (fehlende Folgen pro Show).
func (s *Store) ListTVFoldersForLibrary(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT substr(i.rel_path, 1, instr(i.rel_path, '/') - 1) AS folder
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		WHERE i.library_id = ? AND i.rel_path LIKE '%/%'
		ORDER BY folder
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// ListTVFoldersWithUnmatched liefert alle Top-Level-Folder einer TV-Library, die
// mindestens ein Item ohne Metadata enthalten. Wird für den Auto-Backlog-Enricher
// nach Scan-Ende genutzt.
func (s *Store) ListTVFoldersWithUnmatched(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT substr(rel_path, 1, instr(rel_path, '/') - 1) AS folder
		FROM items
		WHERE library_id = ? AND metadata_id IS NULL AND rel_path LIKE '%/%'
		ORDER BY folder
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// ListItemPathsInFolderNotInSet liefert Orphan-Pfade in einem Folder-Scope,
// genutzt vom Scanner-Summary für Detail-Listen.
func (s *Store) ListItemPathsInFolderNotInSet(libraryID int64, folder string, keep map[string]struct{}) ([]string, error) {
	prefix := strings.TrimSuffix(folder, "/") + "/"
	rows, err := s.db.Query(
		`SELECT path FROM items WHERE library_id = ? AND (rel_path = ? OR rel_path LIKE ?)`,
		libraryID, folder, prefix+"%",
	)
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

// DeleteItemsInFolderNotInSet entfernt Orphan-Items nur innerhalb des angegebenen
// rel_path-Unterbaums. Dateien in Geschwister-Ordnern bleiben unangetastet.
// Verwendet für folder-gescopte Scans.
func (s *Store) DeleteItemsInFolderNotInSet(libraryID int64, folder string, keep map[string]struct{}) (int, error) {
	prefix := strings.TrimSuffix(folder, "/") + "/"
	rows, err := s.db.Query(
		`SELECT id, path FROM items WHERE library_id = ? AND (rel_path = ? OR rel_path LIKE ?)`,
		libraryID, folder, prefix+"%",
	)
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

// ItemFilter fasst optionale Filter für ListItems zusammen.
type ItemFilter struct {
	LibraryID int64
	Search    string
	Sort      string
	SortDir   string // "asc" | "desc" | "" (= Default-Richtung des Sort-Feldes)
	DateFrom  time.Time
	DateTo    time.Time
	Folder    string
	Watched   string
	Favorite  string
	MatchState string
	DupesOnly  bool  // true = nur Items mit mehrfach vergebener metadata_id
	PersonTMDB int64 // 0 = aus; sonst nur Items, deren Metadata (oder Parent-Show bei Episoden) diese Person listet
	MinHeight  int   // 0 = aus; sonst nur Items mit height >= MinHeight
	MaxHeight  int   // 0 = aus; sonst nur Items mit height <= MaxHeight (exakter Bucket über Min+Max)
	ResBuckets []string // Multi-Select-Auflösungs-Filter: 4k/2k/1080p/720p/576p/540p/480p/360p; mehrere → OR
	Interlaced bool  // true = nur Items, deren Video-Stream field_order ∉ {progressive, unknown, ""}
	UserID    int64 // 0 = ungesetzt (Worker-Kontext); sonst pro-User-Zustand laden
	// MaxAgeRating: wenn > 0, werden Items mit metadata.age_rating > Max
	// ausgeblendet. 0 = keine Beschränkung (Admin-Default).
	MaxAgeRating int
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
	       COALESCE(i.episode_end, 0)
	      FROM items i
	      LEFT JOIN metadata m ON m.id = i.metadata_id
	      LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
	      WHERE 1=1`
	args := []any{f.UserID}
	if f.LibraryID > 0 {
		q += ` AND i.library_id = ?`
		args = append(args, f.LibraryID)
	}
	if f.Search != "" {
		// Suche trifft Titel ODER Schauspielername. Bei Episoden zusätzlich
		// Schauspieler der Parent-Show (Hauptcast der Serie), damit z. B. die
		// Suche nach einem Hauptdarsteller auch alle Episoden findet.
		q += ` AND (
			i.title LIKE ?
			OR COALESCE(m.title, '') LIKE ?
			OR EXISTS (
				SELECT 1 FROM metadata_cast mc
				JOIN people p ON p.id = mc.person_id
				WHERE p.name LIKE ?
				  AND (mc.metadata_id = i.metadata_id
				       OR mc.metadata_id = (SELECT parent_id FROM metadata WHERE id = i.metadata_id))
			)
		)`
		pattern := "%" + f.Search + "%"
		args = append(args, pattern, pattern, pattern)
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
	switch f.Folder {
	case "":
		// kein Filter
	case "/":
		q += ` AND INSTR(i.rel_path, '/') = 0`
	default:
		q += ` AND i.rel_path LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(f.Folder)+"/%")
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
	if f.MatchState == "unmatched" {
		q += ` AND i.metadata_id IS NULL`
	}
	if f.DupesOnly {
		// Nur Items, deren metadata_id in der gleichen Library mehrfach vorkommt.
		q += ` AND i.metadata_id IS NOT NULL AND i.metadata_id IN (
			SELECT metadata_id FROM items
			WHERE library_id = i.library_id AND metadata_id IS NOT NULL
			GROUP BY metadata_id HAVING COUNT(*) > 1
		)`
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
		if asc {
			q += ` ORDER BY COALESCE(i.released_at, i.mod_time) ASC`
		} else {
			q += ` ORDER BY COALESCE(i.released_at, i.mod_time) DESC`
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
	default:
		// Wenn TMDB-Metadata existiert, nach dem angezeigten Titel sortieren,
		// sonst nach items.title. Sonst kommen Dateinamen mit Release-Präfix
		// ("a-complete-unknown-1080p") unerwartet vor "Alex" o. ä.
		if desc {
			q += ` ORDER BY COALESCE(NULLIF(m.title, ''), i.title) COLLATE NOCASE DESC`
		} else {
			q += ` ORDER BY COALESCE(NULLIF(m.title, ''), i.title) COLLATE NOCASE`
		}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb, watched, favorite int
		var released sql.NullString
		var watchedAt, favoritedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus, &it.EpisodeEnd); err != nil {
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

// escapeLike escaped \, %, _ für LIKE-Pattern (Escape-Char: Backslash).
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Folder beschreibt einen virtuellen Unterordner innerhalb einer Bibliothek.
type Folder struct {
	Name        string          `json:"name"` // voller relativer Pfad (z.B. "a/Siterips")
	ItemCount   int             `json:"itemCount"`
	ThumbItemID int64           `json:"thumbItemId"`
	MetadataID  int64           `json:"metadataId,omitempty"`
	Metadata    *model.Metadata `json:"metadata,omitempty"`
	Drilldown   bool            `json:"drilldown"` // true = Klick zeigt Subfolder statt flacher Liste
}

// TopLevelFolders liefert alle direkten Unterordner einer Bibliothek mit Item-Zählung.
// Bei TV-Bibliotheken wird zusätzlich die Show-Metadata aus folder_metadata angehängt.
func (s *Store) TopLevelFolders(libraryID int64) ([]Folder, error) {
	return s.topLevelFolders(libraryID, false)
}

// topLevelFolders mit optionalem „nur unmatched"-Filter: zählt nur Items mit
// metadata_id IS NULL und liefert nur Folder, die mindestens ein solches Item
// enthalten. Wird vom Filter „Ohne TMDB-Zuordnung" für TV-Libraries genutzt,
// damit komplett ungemappte Serien als Folder-Kachel auftauchen.
func (s *Store) topLevelFolders(libraryID int64, onlyUnmatched bool) ([]Folder, error) {
	itemFilter := ""
	if onlyUnmatched {
		itemFilter = " AND metadata_id IS NULL"
	}
	// Wichtig: erst aggregieren, dann mit folder_metadata joinen — sonst führt der LEFT JOIN
	// vor dem GROUP BY zu Ambiguität beim "folder"-Alias in SQLite und alle Items landen
	// im selben Bucket.
	rows, err := s.db.Query(`
		SELECT f.folder, f.cnt, f.thumb_id, fm.metadata_id
		FROM (
			SELECT
				SUBSTR(rel_path, 1, INSTR(rel_path, '/')-1) AS folder,
				library_id,
				COUNT(*) AS cnt,
				MIN(CASE WHEN has_thumb=1 THEN id ELSE NULL END) AS thumb_id
			FROM items
			WHERE library_id = ? AND INSTR(rel_path, '/') > 0`+itemFilter+`
			GROUP BY folder
		) f
		LEFT JOIN folder_metadata fm
		  ON fm.library_id = f.library_id AND fm.folder = f.folder
		ORDER BY f.folder COLLATE NOCASE
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Folder{}
	var metaIDs []int64
	for rows.Next() {
		var f Folder
		var thumb, meta sql.NullInt64
		if err := rows.Scan(&f.Name, &f.ItemCount, &thumb, &meta); err != nil {
			return nil, err
		}
		if thumb.Valid {
			f.ThumbItemID = thumb.Int64
		}
		if meta.Valid && meta.Int64 > 0 {
			f.MetadataID = meta.Int64
			metaIDs = append(metaIDs, meta.Int64)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Metadata für Folder nachladen
	if len(metaIDs) > 0 {
		byID := map[int64]*model.Metadata{}
		for _, id := range metaIDs {
			if m, _ := s.GetMetadata(id); m != nil {
				byID[id] = m
			}
		}
		for i := range out {
			if out[i].MetadataID > 0 {
				out[i].Metadata = byID[out[i].MetadataID]
			}
		}
	}
	return out, nil
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
	var hasThumb, watched, favorite int
	var released sql.NullString
	var watchedAt, favoritedAt sql.NullTime
	var confirmed int
	err := s.db.QueryRow(`
		SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container, i.video_codec, i.audio_codec,
		       i.width, i.height, i.duration_sec, i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb,
		       i.mod_time, i.released_at, i.added_at, COALESCE(i.metadata_id, 0),
		       COALESCE(i.metadata_confirmed, 0),
		       COALESCE(us.watched, 0), us.watched_at, COALESCE(us.favorite, 0), us.favorited_at,
		       COALESCE(i.trickplay_status, ''),
		       COALESCE(i.episode_end, 0)
		FROM items i
		LEFT JOIN user_item_state us ON us.item_id = i.id AND us.user_id = ?
		WHERE i.id = ?`, userID, id).
		Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container, &it.VideoCodec, &it.AudioCodec,
			&it.Width, &it.Height, &it.DurationSec, &it.SizeBytes, &it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime, &released, &it.AddedAt, &it.MetadataID,
			&confirmed,
			&watched, &watchedAt, &favorite, &favoritedAt, &it.TrickplayStatus, &it.EpisodeEnd)
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
	if it.MetadataID > 0 {
		if m, _ := s.GetMetadata(it.MetadataID); m != nil {
			it.Metadata = m
		}
	}
	// Streams mitliefern — UI braucht u.a. das field_order der Video-Streams,
	// um den 🪤-Interlaced-Hinweis im Detail-Dialog zu zeigen, und das Player-
	// Dropdown nutzt sie für Audio-/Subtitle-Auswahl.
	if streams, err := s.ItemStreams(it.ID); err == nil {
		it.Streams = streams
	}
	return &it, nil
}

// --- Trickplay ---

// SetTrickplayFolder aktiviert oder deaktiviert Trickplay-Generierung für einen Ordner.
// Deaktivieren löscht nur die Markierung, die generierten Dateien bleiben auf Platte
// (können über einen separaten Cleanup entfernt werden).
func (s *Store) SetTrickplayFolder(libraryID int64, folder string, enabled bool) error {
	if enabled {
		_, err := s.db.Exec(
			`INSERT OR IGNORE INTO trickplay_folders(library_id, folder) VALUES(?, ?)`,
			libraryID, folder)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM trickplay_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder)
	return err
}

// TrickplayFolderEnabled prüft ob ein Ordner aktiviert ist.
func (s *Store) TrickplayFolderEnabled(libraryID int64, folder string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM trickplay_folders WHERE library_id = ? AND folder = ?`,
		libraryID, folder).Scan(&n)
	return n > 0, err
}

// SetItemTrickplay setzt den Status eines Items ("pending" | "done" | "failed" | "").
func (s *Store) SetItemTrickplay(itemID int64, status string) error {
	_, err := s.db.Exec(`UPDATE items SET trickplay_status = ? WHERE id = ?`, status, itemID)
	return err
}

// SetItemTrickplayError setzt Status + optionale Fehlermeldung (wird bei "done" gelöscht).
func (s *Store) SetItemTrickplayError(itemID int64, status, errMsg string) error {
	if status == "failed" {
		_, err := s.db.Exec(
			`UPDATE items SET trickplay_status = ?, trickplay_error = ? WHERE id = ?`,
			status, errMsg, itemID,
		)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE items SET trickplay_status = ?, trickplay_error = NULL WHERE id = ?`,
		status, itemID,
	)
	return err
}

// ResetFailedTrickplayStatus setzt alle Items mit status=failed wieder auf leer,
// damit der Worker sie erneut probiert (z. B. nach verbessertem Filter).
func (s *Store) ResetFailedTrickplayStatus() (int, error) {
	res, err := s.db.Exec(
		`UPDATE items SET trickplay_status = '', trickplay_error = NULL WHERE trickplay_status = 'failed'`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResetAllTrickplayStatus setzt bei allen Items trickplay_status zurück auf "".
// Wird vom "Trickplay komplett löschen"-Admin-Flow aufgerufen, nachdem die
// Dateien auf Platte entfernt wurden.
func (s *Store) ResetAllTrickplayStatus() error {
	_, err := s.db.Exec(`UPDATE items SET trickplay_status = '', trickplay_error = NULL`)
	return err
}

// ResetStuckPendingTrickplay setzt Items, die auf "pending" hängen, zurück
// auf leer — damit sie beim nächsten Worker-Lauf wieder als Kandidaten
// auftauchen. Stuck-Pending entstehen, wenn der Worker mid-run abbricht
// (Container-Restart, Cancel, Crash): das Item wird auf "pending" markiert
// bevor ffmpeg startet, aber der Status nie auf "done"/"failed" finalisiert.
func (s *Store) ResetStuckPendingTrickplay() (int, error) {
	res, err := s.db.Exec(
		`UPDATE items SET trickplay_status = '', trickplay_error = NULL WHERE trickplay_status = 'pending'`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// TrickplayLogEntry wird vom Admin-Log-Viewer angezeigt.
type TrickplayLogEntry struct {
	ID        int64  `json:"id"`
	LibraryID int64  `json:"libraryId"`
	Path      string `json:"path"`
	RelPath   string `json:"relPath"`
	Title     string `json:"title"`
	Error     string `json:"error,omitempty"`
}

// CountTrickplayByStatus liefert die globale Aufteilung pro Status („"/"pending"/
// "done"/"failed") über alle Libraries — für die Health-Diagnose.
func (s *Store) CountTrickplayByStatus() (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(trickplay_status,'') AS st, COUNT(*) FROM items GROUP BY st
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ListItemsByTrickplayStatus liefert alle Items mit dem angegebenen Status,
// inklusive Pfad und Fehlermeldung. Für den Admin-Log-Viewer.
func (s *Store) ListItemsByTrickplayStatus(status string) ([]TrickplayLogEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, library_id, path, rel_path, title, COALESCE(trickplay_error, '')
		FROM items
		WHERE trickplay_status = ?
		ORDER BY path
	`, status)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TrickplayLogEntry
	for rows.Next() {
		var r TrickplayLogEntry
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.Path, &r.RelPath, &r.Title, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FolderTrickplayStatus zählt Items eines Ordners nach Trickplay-Status.
type TrickplayStatus struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

func (s *Store) FolderTrickplayStatus(libraryID int64, folder string) (TrickplayStatus, error) {
	var st TrickplayStatus
	var rows *sql.Rows
	var err error
	if folder == "" {
		// Library-weit: alle Items der Bibliothek
		rows, err = s.db.Query(
			`SELECT COALESCE(trickplay_status, '') FROM items WHERE library_id = ?`,
			libraryID)
	} else {
		rows, err = s.db.Query(
			`SELECT COALESCE(trickplay_status, '') FROM items
			 WHERE library_id = ? AND rel_path LIKE ? ESCAPE '\'`,
			libraryID, escapeLike(folder)+"/%")
	}
	if err != nil {
		return st, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return st, err
		}
		st.Total++
		switch status {
		case "done":
			st.Done++
		case "pending":
			st.Pending++
		case "failed":
			st.Failed++
		}
	}
	return st, rows.Err()
}

// PendingTrickplayItems liefert Items in aktivierten Ordnern, die noch kein Trickplay haben.
// Spezialfall: tf.folder = '' aktiviert Trickplay für die gesamte Bibliothek.
//
// Sortierung: Folder-für-Folder. Der Top-Level-Ordner mit dem neuesten Item
// kommt zuerst, alle seine pending-Items werden komplett abgearbeitet bevor
// der nächste Folder dran ist. Innerhalb eines Folders nach added_at DESC
// (neuestes zuerst). Bei Items direkt in der Library-Root gilt rel_path als
// eigener „Folder".
func (s *Store) PendingTrickplayItems(limit int) ([]model.Item, error) {
	rows, err := s.db.Query(`
		WITH pending AS (
			SELECT i.id, i.library_id, i.path, i.rel_path, i.title, i.container,
				i.video_codec, i.audio_codec, i.width, i.height, i.duration_sec,
				i.size_bytes, i.bitrate_kbps, i.thumb_path, i.has_thumb, i.mod_time,
				i.added_at,
				CASE
					WHEN INSTR(i.rel_path, '/') > 0
					THEN SUBSTR(i.rel_path, 1, INSTR(i.rel_path, '/') - 1)
					ELSE i.rel_path
				END AS top_folder
			FROM items i
			JOIN trickplay_folders tf
			  ON tf.library_id = i.library_id
			  AND (tf.folder = '' OR i.rel_path LIKE (tf.folder || '/%') ESCAPE '\')
			WHERE COALESCE(i.trickplay_status,'') = ''
			  AND i.duration_sec > 0
		)
		SELECT id, library_id, path, rel_path, title, container,
			COALESCE(video_codec,''), COALESCE(audio_codec,''), width, height, duration_sec,
			size_bytes, bitrate_kbps, COALESCE(thumb_path,''), has_thumb, mod_time
		FROM pending
		ORDER BY MAX(added_at) OVER (PARTITION BY library_id, top_folder) DESC,
		         library_id, top_folder,
		         added_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		var it model.Item
		var hasThumb int
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.RelPath, &it.Title, &it.Container,
			&it.VideoCodec, &it.AudioCodec, &it.Width, &it.Height, &it.DurationSec, &it.SizeBytes,
			&it.BitrateKbps, &it.ThumbPath, &hasThumb, &it.ModTime); err != nil {
			return nil, err
		}
		it.HasThumb = hasThumb == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListOrphanTrickplayItems liefert Items mit gesetztem trickplay_status, die
// aber NICHT (mehr) in einem aktivierten Ordner liegen. Werden beim Worker-Run
// aufgeräumt (Dateien + Status). Relikte aus früheren Versionen ohne Folder-Check.
func (s *Store) ListOrphanTrickplayItems() ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT i.id FROM items i
		WHERE COALESCE(i.trickplay_status,'') != ''
		  AND NOT EXISTS (
			SELECT 1 FROM trickplay_folders tf
			WHERE tf.library_id = i.library_id
			  AND (tf.folder = '' OR i.rel_path LIKE (tf.folder || '/%') ESCAPE '\')
		  )
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

// ListTrickplayFolders liefert alle aktivierten Ordner einer Bibliothek.
func (s *Store) ListTrickplayFolders(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT folder FROM trickplay_folders WHERE library_id = ? ORDER BY folder`,
		libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
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

// --- Settings ---

func (s *Store) GetSetting(key, def string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseDBTime ist defensiv: manche ffprobe-Tags kommen in Formaten, die
// modernc.org/sqlite beim Scan in time.Time nicht akzeptiert. Wir lesen als
// String ein und probieren mehrere Layouts. Leer/unparsebar → Zero-Time.
func parseDBTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// --- Metadata ---

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
	defer rows.Close()
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
	rows, err := s.db.Query(`
		SELECT DISTINCT i.library_id, SUBSTR(i.rel_path, 1, INSTR(i.rel_path, '/')-1) AS folder
		FROM items i
		JOIN libraries l ON l.id = i.library_id
		LEFT JOIN folder_metadata fm
		  ON fm.library_id = i.library_id
		  AND fm.folder = SUBSTR(i.rel_path, 1, INSTR(i.rel_path, '/')-1)
		WHERE INSTR(i.rel_path, '/') > 0
		  AND l.kind = 'tv'
		  AND fm.metadata_id IS NULL
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

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
