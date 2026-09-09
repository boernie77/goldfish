package store

// migrate() enthält alle Schema-Definitionen (CREATE TABLE) und additiven
// Migrationen (addCol) — ausgelagert aus sqlite.go (Code-Review 2026-09-06,
// User-Auftrag "Codebasis intern umstrukturieren"), reine Datei-Verschiebung
// ohne Logik-/Signaturänderung. Migrationen sind additiv und idempotent, siehe
// CLAUDE.md "Entwicklungsworkflow".
import (
	"fmt"
	"strings"
)

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
		// intro_skip_folders: Opt-in-Set für die Intro-Erkennung, strikt pro
		// Serien-Ordner (kein "ganze Bibliothek"-Fall — folder=="" wird von
		// Store+API zurückgewiesen). Zeilen-Existenz = aktiviert, wie
		// trickplay_folders.
		`CREATE TABLE IF NOT EXISTS intro_skip_folders (
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			folder TEXT NOT NULL,
			PRIMARY KEY (library_id, folder)
		)`,
		// intro_skip_jobs: ein Job pro Serien-Ordner (nicht pro Episode) — die
		// Erkennung vergleicht immer alle Episoden einer Show gemeinsam.
		`CREATE TABLE IF NOT EXISTS intro_skip_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			folder TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			episodes_total INTEGER NOT NULL DEFAULT 0,
			episodes_matched INTEGER NOT NULL DEFAULT 0,
			error TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			UNIQUE(library_id, folder)
		)`,
		`CREATE INDEX IF NOT EXISTS intro_skip_jobs_status_idx ON intro_skip_jobs(status)`,
		// ocr_sub_folders: Opt-in-Set für die OCR-Untertitel-Erzeugung. folder=""
		// = ganze Bibliothek (z.B. "Filme"), sonst ein Top-Level-Ordner. Zeilen-
		// Existenz = aktiviert.
		`CREATE TABLE IF NOT EXISTS ocr_sub_folders (
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			folder TEXT NOT NULL,
			PRIMARY KEY (library_id, folder)
		)`,
		// ocr_sub_jobs: ein Job pro Item. Der Worker sucht die Bild-Untertitel-
		// Streams (PGS/VOBSUB) und OCR-t jeden in eine <lang>-ocr.vtt.
		`CREATE TABLE IF NOT EXISTS ocr_sub_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			status TEXT NOT NULL DEFAULT 'pending',
			langs TEXT NOT NULL DEFAULT '',
			error TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			UNIQUE(item_id)
		)`,
		`CREATE INDEX IF NOT EXISTS ocr_sub_jobs_status_idx ON ocr_sub_jobs(status)`,
		// scan_excluded_folders: Opt-out-Set — Zeilen-Existenz = ausgeschlossen.
		// Gilt NUR für den zeitgesteuerten Auto-Scan (Scanner.Start mit
		// respectScanExcludes=true), NICHT für einen manuellen ⟳-Scan (User-
		// Vorgabe 2026-09-09 — ein manuell ausgelöster Scan soll bewusst
		// immer alles scannen, unabhängig davon, wo/wie ein Ordner gerade
		// gemountet ist; nur der unbeaufsichtigte Auto-Scan braucht den
		// Schutz vor fälschlichem Orphan-Löschen bei z.B. nicht angeschlossener
		// externer Platte). folder ist rekursiv: schließt den Ordner UND alle
		// Unterordner vom Walk UND vom Orphan-Cleanup aus (siehe scanner.go
		// IsRelPathExcluded + Store.ItemPathsUnderFolders).
		`CREATE TABLE IF NOT EXISTS scan_excluded_folders (
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
		// Pro-User-Sichtbarkeit einer Library auf der Startseite. Eine Zeile
		// überschreibt den globalen libraries.on_home-Default NUR für diesen
		// User. Fehlt die Zeile, gilt weiterhin der globale Default.
		`CREATE TABLE IF NOT EXISTS user_home_prefs (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			on_home INTEGER NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, library_id)
		)`,
		// Pro-User-Sichtbarkeit + Reihenfolge der Bibliotheks-REITERLEISTE
		// (oben in der Topbar) — bewusst GETRENNT von user_home_prefs
		// (Startseiten-Streifen): eine Library kann z.B. aus der Reiterleiste
		// ausgeblendet sein, aber trotzdem auf der Startseite erscheinen,
		// oder umgekehrt (User-Wunsch 2026-09-02, nach anfänglich
		// vereinheitlichtem Versuch explizit getrennt gefordert).
		`CREATE TABLE IF NOT EXISTS user_nav_prefs (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			on_nav INTEGER NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, library_id)
		)`,
		// Generische Pro-User-Einstellungen (Key-Value), analog zur globalen
		// settings-Tabelle. Erster Einsatzzweck: Sichtbarkeit der beiden
		// globalen Startseiten-Streifen "Fortsetzen"/"Als nächstes"
		// (home_show_continue / home_show_nextup, Werte "0"/"1").
		`CREATE TABLE IF NOT EXISTS user_settings (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			PRIMARY KEY (user_id, key)
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
		// Auto-Rename-History: jede Datei-Umbenennung (manuell oder automatisch
		// beim Confirm) wird hier protokolliert. undone_at bleibt NULL solange
		// die Umbenennung aktiv ist; beim Undo wird der Eintrag nicht geloescht
		// sondern der Timestamp gesetzt — so bleibt die Historie nachvollziehbar.
		`CREATE TABLE IF NOT EXISTS rename_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			old_path TEXT NOT NULL,
			new_path TEXT NOT NULL,
			old_rel_path TEXT NOT NULL,
			new_rel_path TEXT NOT NULL,
			renamed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			undone_at DATETIME,
			triggered_by TEXT NOT NULL DEFAULT 'auto'
		)`,
		`CREATE INDEX IF NOT EXISTS rename_history_item_idx ON rename_history(item_id)`,
		`CREATE INDEX IF NOT EXISTS rename_history_renamed_idx ON rename_history(renamed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS generated_subtitles (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			language    TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'pending',
			error       TEXT,
			generated_at DATETIME,
			UNIQUE(item_id, language)
		)`,
		`CREATE INDEX IF NOT EXISTS gen_subs_item_idx ON generated_subtitles(item_id)`,
		`CREATE INDEX IF NOT EXISTS gen_subs_status_idx ON generated_subtitles(status)`,
		// Gesehen-Sync zwischen zwei Usern (User-Anfrage 2026-08-19): eine Zeile
		// pro Richtung (a→b), Status durchläuft pending → accepted, oder wird
		// gelöscht bei Ablehnen/Trennen. Zwei User können sich so gegenseitig
		// verlinken; requester_id ist die Person, die den Link angestoßen hat
		// (für die UI "wartet auf Bestätigung von …"), die Sync-Propagation
		// selbst ist danach aber symmetrisch (beide Richtungen spiegeln).
		`CREATE TABLE IF NOT EXISTS user_watch_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_a_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			user_b_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			requester_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			confirmed_at DATETIME,
			CHECK (user_a_id < user_b_id),
			UNIQUE(user_a_id, user_b_id)
		)`,
		`CREATE TABLE IF NOT EXISTS activity_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			user_id INTEGER,
			username TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL,
			action TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS activity_log_at_idx ON activity_log(at DESC)`,
		// Musik-Bibliotheken (kind=music): eine Zeile pro (Artist,Album)-Gruppe.
		// Kanonische Identität kommt aus Tag-Werten der zugehörigen Items, NICHT
		// aus der metadata-Tabelle — die ist auf TMDB-int-IDs zugeschnitten
		// (UNIQUE(tmdb_type,tmdb_id,...)), MusicBrainz-IDs sind UUIDs und passen
		// da nicht sauber rein. Cover-Datei selbst liegt wie bei Postern im
		// Flat-Cache-Verzeichnis (posterFilename-Konvention, eigener Präfix).
		`CREATE TABLE IF NOT EXISTS music_albums (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			artist TEXT NOT NULL,
			album TEXT NOT NULL,
			year INTEGER NOT NULL DEFAULT 0,
			genre TEXT NOT NULL DEFAULT '',
			cover_source TEXT NOT NULL DEFAULT '',
			mb_release_id TEXT NOT NULL DEFAULT '',
			cover_fetched_at DATETIME,
			metadata_fetched_at DATETIME,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(library_id, artist, album)
		)`,
		`CREATE INDEX IF NOT EXISTS music_albums_lib_idx ON music_albums(library_id)`,
		// Album-Favoriten sind per-User (wie Track-Favoriten in user_item_state)
		// — Alben sind aber keine items-Zeile, sondern eine eigene virtuelle
		// Gruppierung (siehe music_albums-Kommentar oben), daher eine eigene
		// Junction-Tabelle statt user_item_state mitzunutzen. User-Anfrage
		// 2026-09-04: "Favoriten will ich für Alben als auch für einzelne Songs
		// erstellen können" — Songs nutzen bereits die normale
		// item-favorite-Funktion (user_item_state.favorite), das hier ist NUR
		// die Album-Ebene.
		`CREATE TABLE IF NOT EXISTS user_music_album_favorites (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			album_id INTEGER NOT NULL REFERENCES music_albums(id) ON DELETE CASCADE,
			favorited_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, album_id)
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
	// User-konfigurierbare Reihenfolge fuer Topbar-Dropdown + Home-Sektionen.
	// Default 0 — bei Gleichstand sortieren wir alphabetisch (Bestands-DBs).
	if err := addCol("libraries", "sort_order", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// Card-Layout-Toggle fuer Private-Libs: 1 = Top-Folder als Top-Zeile (Default,
	// YouTube-Style), 0 = klassisch Titel oben. Bestands-Private-Libs behalten
	// damit das aktuelle Verhalten; User kann pro Lib im Library-Manager opten.
	if err := addCol("libraries", "channel_label_on_top", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	// Pro-User-Reihenfolge der Startseiten-Streifen (zusätzlich zum
	// pro-User on_home-Override in derselben Tabelle). addCol nötig, weil
	// user_home_prefs bereits vor dieser Spalte live war (CREATE TABLE IF
	// NOT EXISTS legt sie auf Bestands-DBs nicht nachträglich an).
	if err := addCol("user_home_prefs", "sort_order", "INTEGER NOT NULL DEFAULT 0"); err != nil {
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
	// Pro-User Download-Erlaubnis. Default 1 (erlaubt) — bestehende Accounts
	// bleiben unverändert nutzbar, Admin kann pro User gezielt einschränken.
	// Admins ignorieren den Wert (siehe requireDownloadAllowed in internal/api).
	if err := addCol("users", "can_download", "INTEGER NOT NULL DEFAULT 1"); err != nil {
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
	// Bibliotheks-übergreifendes Verschieben (seit 2026-07-12): rename_history
	// protokolliert jetzt auch einen library_id-Wechsel, damit Undo ihn
	// zurücksetzen kann. 0 = kein Wechsel (deckt auch alle historischen
	// Einträge ab, die vor dieser Migration entstanden sind).
	if err := addCol("rename_history", "old_library_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addCol("rename_history", "new_library_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// Varianten-Trennung (seit 2026-07-31): Items mit gleicher metadata_id
	// werden im Grid client-seitig automatisch zu einer ×N-Kachel gruppiert
	// (groupVariants in app.js). variant_split=1 nimmt ein einzelnes Item
	// bewusst aus dieser automatischen Gruppierung heraus, ohne die
	// metadata_id (und damit TMDB-Zuordnung/Poster) zu verändern — es bleibt
	// derselbe Film, erscheint aber als eigene Kachel statt im Varianten-
	// Dropdown zu verschwinden.
	if err := addCol("items", "variant_split", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// intro_start_sec/intro_end_sec: NULL = nicht analysiert bzw. kein Intro
	// erkannt (kein Fehlerzustand). Werden ausschließlich von der
	// Intro-Erkennung (internal/introskip) geschrieben.
	if err := addCol("items", "intro_start_sec", "REAL"); err != nil {
		return err
	}
	if err := addCol("items", "intro_end_sec", "REAL"); err != nil {
		return err
	}
	// intro_checked_at: wird bei JEDEM Analyse-Versuch gesetzt, auch ohne
	// Treffer — unterscheidet "nie analysiert" (NULL) von "analysiert, aber
	// kein Intro gefunden" (gesetzt, intro_start/end bleiben NULL). Analog
	// zu metadata.cast_fetched_at, verhindert Endlosschleifen im
	// EnqueueStaleFolders-Rescan.
	if err := addCol("items", "intro_checked_at", "DATETIME"); err != nil {
		return err
	}
	// intro_skip_folders.season: 0 = ganze Serie (Default, alle Staffeln),
	// >0 = nur diese Staffel wird analysiert/eingereiht. Für kontrolliertes
	// Testen an einer einzelnen Staffel, ohne gleich die ganze Show
	// laufen zu lassen (siehe CLAUDE.md "Intro-Erkennung").
	if err := addCol("intro_skip_folders", "season", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// Persönliche Sternebewertung (0–3) pro User+Item — analog zur Mac/iOS-App
	// (`LocalItem.rating`), aber server-seitig, damit sie im Browser + allen
	// Clients gilt. 0 = keine Wertung.
	if err := addCol("user_item_state", "rating", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// Musik-Felder (nur kind=music, aus eingebetteten Tags via scanner.probeItem
	// gelesen). track_no 0 = keine Track-Nummer im Tag gefunden.
	if err := addCol("items", "artist", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addCol("items", "album", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addCol("items", "track_no", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addCol("items", "music_album_id", "INTEGER REFERENCES music_albums(id) ON DELETE SET NULL"); err != nil {
		return err
	}
	// Genre-Tag pro Track — Zwischenlager für GroupMusicAlbums (aggregiert
	// nach music_albums.genre), war in der ursprünglichen Musik-Feature-Runde
	// geplant, aber nie verdrahtet (Scanner las nur artist/album/track/title,
	// music_albums.genre blieb dadurch für ALLE Alben leer). User-Anfrage
	// 2026-09-04: "Kann man dazu in den Infos noch das Genre hinzufügen?".
	if err := addCol("items", "genre", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Jahr-Tag pro Track (User-Wunsch 2026-09-06: "Jahr als Spalte/Feld
	// ergänzen") — bewusst NICHT `items.released_at` wiederverwendet: das
	// füllt der Scanner IMMER mit mindestens der Datei-mtime
	// (`extractReleaseTime`-Fallback), zeigte beim ersten Anlauf dieses
	// Features dadurch reihenweise das aktuelle Kopierdatum statt des
	// echten Erscheinungsjahrs (User-Report mit Screenshot: "An Innocent
	// Man" von Billy Joel [1983] zeigte "2026"). Eigene Spalte, exakt wie
	// `genre` aus den Tags gelesen (0 = kein Jahr-Tag gefunden).
	if err := addCol("items", "year", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// MusicBrainz-Metadaten-Backfill (Genre + Jahr, User-Wunsch 2026-09-06:
	// "Alle sollen Titel, Künstler, Genre, Dauer und Jahr enthalten") —
	// analog cover_fetched_at, verhindert Endlos-Retry bei Alben ohne
	// MusicBrainz-Treffer. NULL = noch nie versucht.
	if err := addCol("music_albums", "metadata_fetched_at", "DATETIME"); err != nil {
		return err
	}
	// Playlists strikt nach Video/Musik getrennt (User-Wunsch 2026-09-04:
	// "gemeinsame Playlists gefällt mir eigentlich nicht"). DEFAULT 'video'
	// gilt auch rückwirkend für ALLE bestehenden Zeilen (SQLite wendet den
	// ALTER-TABLE-Default sofort auf existierende Rows an) — zutreffende
	// Annahme, da die Musik-Bibliothek erst seit wenigen Tagen existiert und
	// alle bisherigen Playlists Videos enthalten.
	if err := addCol("playlists", "kind", "TEXT NOT NULL DEFAULT 'video'"); err != nil {
		return err
	}
	// Intro-Erkennung: "Neue Serien automatisch aktivieren" pro Bibliothek
	// (User-Wunsch 2026-09-06: hatte über "Alle auswählen" im Dialog ALLE
	// vorhandenen Serien einmalig aktiviert und erwartete danach, dass neu
	// hinzukommende Serien automatisch mitlaufen — bewusste Erweiterung des
	// bisher strikten Pro-Ordner-Opt-in, siehe intro_skip_folders unten).
	if err := addCol("libraries", "intro_skip_auto_new", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS intro_skip_seen_folders (
			library_id INTEGER NOT NULL,
			folder     TEXT NOT NULL,
			PRIMARY KEY(library_id, folder)
		)
	`); err != nil {
		return fmt.Errorf("migrate intro_skip_seen_folders: %w", err)
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
		`CREATE INDEX IF NOT EXISTS user_watch_links_a_idx ON user_watch_links(user_a_id, status)`,
		`CREATE INDEX IF NOT EXISTS user_watch_links_b_idx ON user_watch_links(user_b_id, status)`,
		`CREATE INDEX IF NOT EXISTS items_music_album_idx ON items(music_album_id)`,
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
