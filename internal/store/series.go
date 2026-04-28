package store

import (
	"database/sql"
	"fmt"
)

// SeriesOwnedEpisode: einzelne Episode eines Show-Ordners, die auf Disk liegt.
type SeriesOwnedEpisode struct {
	ItemID     int64
	Season     int
	Episode    int
	EpisodeEnd int   // >0 bei Doppelfolgen (S07E23E24 → Episode=23, EpisodeEnd=24)
	MetaTMDB   int64 // tmdb_id der Episode-Metadata
	ShowTMDB   int64 // tmdb_id der Parent-Show (zum Matchen)
}

// SeriesOwnedEpisodes liefert alle Episoden-Items eines Top-Level-Ordners
// einer TV-Library, inklusive season/episode-Index und der TMDB-ID der Show.
// Episoden ohne Metadata-Match werden ausgelassen (die können wir eh nicht
// in der Staffel-Ansicht einordnen).
func (s *Store) SeriesOwnedEpisodes(libraryID int64, folder string) ([]SeriesOwnedEpisode, int64, error) {
	rows, err := s.db.Query(`
		SELECT i.id, COALESCE(m.season,0), COALESCE(m.episode,0),
		       COALESCE(i.episode_end, 0),
		       COALESCE(m.tmdb_id, 0), COALESCE(parent.tmdb_id, 0)
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		LEFT JOIN metadata parent ON parent.id = m.parent_id
		WHERE i.library_id = ?
		  AND i.rel_path LIKE ? ESCAPE '\'
		ORDER BY m.season, m.episode
	`, libraryID, escapeLike(folder)+"/%")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []SeriesOwnedEpisode
	var showID int64
	for rows.Next() {
		var e SeriesOwnedEpisode
		if err := rows.Scan(&e.ItemID, &e.Season, &e.Episode, &e.EpisodeEnd, &e.MetaTMDB, &e.ShowTMDB); err != nil {
			return nil, 0, err
		}
		if showID == 0 && e.ShowTMDB != 0 {
			showID = e.ShowTMDB
		}
		out = append(out, e)
	}
	return out, showID, rows.Err()
}

// UnmatchedEpisodeFiles liefert alle Items eines Top-Level-Ordners ohne
// TMDB-Metadata (metadata_id IS NULL) mit ihrem Pfad — der Caller parst
// daraus on-the-fly Season/Episode aus dem Dateinamen. Wird vom
// Staffel-View-Handler genutzt, damit z. B. deutsche Hallmark-Specials,
// die als S04E11/E12 nummeriert sind aber bei TMDB nur unter Season 0
// liegen, trotzdem als Owned-Slots in der Staffel erscheinen.
type UnmatchedEpisodeFile struct {
	ItemID  int64
	RelPath string
}

func (s *Store) UnmatchedEpisodeFiles(libraryID int64, folder string) ([]UnmatchedEpisodeFile, error) {
	rows, err := s.db.Query(`
		SELECT id, rel_path FROM items
		WHERE library_id = ?
		  AND metadata_id IS NULL
		  AND rel_path LIKE ? ESCAPE '\'
	`, libraryID, escapeLike(folder)+"/%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UnmatchedEpisodeFile
	for rows.Next() {
		var u UnmatchedEpisodeFile
		if err := rows.Scan(&u.ItemID, &u.RelPath); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ShowTMDBForFolder sucht die TMDB-ID der Show, die einem Top-Level-Ordner
// einer TV-Library via folder_metadata oder einer Episode via parent-Metadata
// zugeordnet ist.
func (s *Store) ShowTMDBForFolder(libraryID int64, folder string) (int64, error) {
	// Folder-Metadata hat Priorität (wird explizit per Manuell-Match gesetzt)
	var showTMDB int64
	err := s.db.QueryRow(`
		SELECT COALESCE(m.tmdb_id, 0)
		FROM folder_metadata fm
		JOIN metadata m ON m.id = fm.metadata_id
		WHERE fm.library_id = ? AND fm.folder = ? AND m.tmdb_type = 'tv'
	`, libraryID, folder).Scan(&showTMDB)
	if err == nil && showTMDB > 0 {
		return showTMDB, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	// Fallback: TMDB-ID der Parent-Show aus den Episoden-Metadata im Folder
	err = s.db.QueryRow(`
		SELECT COALESCE(parent.tmdb_id, 0)
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		JOIN metadata parent ON parent.id = m.parent_id
		WHERE i.library_id = ? AND i.rel_path LIKE ? ESCAPE '\'
		LIMIT 1
	`, libraryID, escapeLike(folder)+"/%").Scan(&showTMDB)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return showTMDB, err
}

// ItemByShowSeasonEpisode findet das Item, das zu einer konkreten Folge einer
// Show im angegebenen Folder der Library gehört — für das Markieren von Parts
// als "owned" in der Staffel-Ansicht.
func (s *Store) ItemByShowSeasonEpisode(libraryID int64, folder string, showTMDBID int64, season, episode int) (int64, error) {
	var id int64
	err := s.db.QueryRow(fmt.Sprintf(`
		SELECT i.id
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		LEFT JOIN metadata parent ON parent.id = m.parent_id
		WHERE i.library_id = ?
		  AND i.rel_path LIKE ? ESCAPE '\'
		  AND m.season = ? AND m.episode = ?
		  AND (parent.tmdb_id = ? OR ? = 0)
		LIMIT 1
	`), libraryID, escapeLike(folder)+"/%", season, episode, showTMDBID, showTMDBID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}
