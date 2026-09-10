package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// SeriesOwnedEpisode: einzelne Episode eines Show-Ordners, die auf Disk liegt.
type SeriesOwnedEpisode struct {
	ItemID      int64
	Season      int
	Episode     int
	EpisodeEnd  int   // >0 bei Doppelfolgen (S07E23E24 → Episode=23, EpisodeEnd=24)
	MetaTMDB    int64 // tmdb_id der Episode-Metadata
	ShowTMDB    int64 // tmdb_id der Parent-Show (zum Matchen)
	Width       int
	Height      int
	DurationSec float64
}

// folderScopeClause baut eine OR-verknüpfte LIKE-Bedingung für einen oder
// mehrere Top-Level-Ordner (Multi-Folder-Browsing für virtuell zusammengeführte
// Serien-Ordner, siehe MergedFolderNames). `column` ist der volle
// SQL-Spaltenausdruck (z.B. "i.rel_path" oder "rel_path", je nach Alias der
// jeweiligen Query). Mit genau einem Ordner (der Normalfall) verhält sich das
// exakt wie die frühere Single-Folder-Variante.
func folderScopeClause(column string, folders []string) (string, []any) {
	if len(folders) == 0 {
		return "0", nil
	}
	parts := make([]string, 0, len(folders))
	args := make([]any, 0, len(folders))
	for _, f := range folders {
		parts = append(parts, column+" LIKE ? ESCAPE '\\'")
		args = append(args, escapeLike(f)+"/%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// SeriesOwnedEpisodes liefert alle Episoden-Items eines oder mehrerer
// Top-Level-Ordner einer TV-Library (mehrere Ordner = virtuell zusammengeführte
// Serie, siehe MergedFolderNames), inklusive season/episode-Index und der
// TMDB-ID der Show. Episoden ohne Metadata-Match werden ausgelassen (die
// können wir eh nicht in der Staffel-Ansicht einordnen).
func (s *Store) SeriesOwnedEpisodes(libraryID int64, folders []string) ([]SeriesOwnedEpisode, int64, error) {
	where, args := folderScopeClause("i.rel_path", folders)
	rows, err := s.db.Query(`
		SELECT i.id, COALESCE(m.season,0), COALESCE(m.episode,0),
		       COALESCE(i.episode_end, 0),
		       COALESCE(m.tmdb_id, 0), COALESCE(parent.tmdb_id, 0),
		       COALESCE(i.width, 0), COALESCE(i.height, 0), COALESCE(i.duration_sec, 0)
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		LEFT JOIN metadata parent ON parent.id = m.parent_id
		WHERE i.library_id = ?
		  AND `+where+`
		ORDER BY m.season, m.episode
	`, append([]any{libraryID}, args...)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []SeriesOwnedEpisode
	var showID int64
	for rows.Next() {
		var e SeriesOwnedEpisode
		if err := rows.Scan(&e.ItemID, &e.Season, &e.Episode, &e.EpisodeEnd, &e.MetaTMDB, &e.ShowTMDB, &e.Width, &e.Height, &e.DurationSec); err != nil {
			return nil, 0, err
		}
		if showID == 0 && e.ShowTMDB != 0 {
			showID = e.ShowTMDB
		}
		out = append(out, e)
	}
	return out, showID, rows.Err()
}

// WatchedItemIDsInFolder liefert die Menge aller item-IDs, die der gegebene
// User in einem oder mehreren Show-Folder(n) als "gesehen" markiert hat.
// Wird vom Season-Handler genutzt, damit jede Episode im Response ihren
// watched-Status mitbekommt (per-User, nicht das Legacy-`items.watched`).
func (s *Store) WatchedItemIDsInFolder(userID, libraryID int64, folders []string) (map[int64]bool, error) {
	where, args := folderScopeClause("i.rel_path", folders)
	rows, err := s.db.Query(`
		SELECT us.item_id
		FROM user_item_state us
		JOIN items i ON i.id = us.item_id
		WHERE us.user_id = ? AND us.watched = 1
		  AND i.library_id = ?
		  AND `+where+`
	`, append([]any{userID, libraryID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UnmatchedEpisodeFiles liefert alle Items eines oder mehrerer Top-Level-
// Ordner ohne TMDB-Metadata (metadata_id IS NULL) mit ihrem Pfad — der
// Caller parst daraus on-the-fly Season/Episode aus dem Dateinamen. Wird vom
// Staffel-View-Handler genutzt, damit z. B. deutsche Hallmark-Specials,
// die als S04E11/E12 nummeriert sind aber bei TMDB nur unter Season 0
// liegen, trotzdem als Owned-Slots in der Staffel erscheinen.
type UnmatchedEpisodeFile struct {
	ItemID  int64
	RelPath string
}

func (s *Store) UnmatchedEpisodeFiles(libraryID int64, folders []string) ([]UnmatchedEpisodeFile, error) {
	where, args := folderScopeClause("rel_path", folders)
	rows, err := s.db.Query(`
		SELECT id, rel_path FROM items
		WHERE library_id = ?
		  AND metadata_id IS NULL
		  AND `+where+`
	`, append([]any{libraryID}, args...)...)
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

// ShowMetadataIDForFolder sucht die lokale metadata.id der Show (nicht die
// TMDB-ID) — für Poster-Bearbeitung (setMetadataPoster/listMetadataPosters
// arbeiten auf metadata.id, nicht auf tmdb_id). Gleiche zwei Fallback-Stufen
// wie ShowTMDBForFolder: erst folder_metadata, sonst die Parent-Metadata der
// Episoden im Ordner.
func (s *Store) ShowMetadataIDForFolder(libraryID int64, folder string) (int64, error) {
	var metaID int64
	err := s.db.QueryRow(`
		SELECT m.id
		FROM folder_metadata fm
		JOIN metadata m ON m.id = fm.metadata_id
		WHERE fm.library_id = ? AND fm.folder = ? AND m.tmdb_type = 'tv'
	`, libraryID, folder).Scan(&metaID)
	if err == nil && metaID > 0 {
		return metaID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	err = s.db.QueryRow(`
		SELECT parent.id
		FROM items i
		JOIN metadata m ON m.id = i.metadata_id AND m.tmdb_type = 'episode'
		JOIN metadata parent ON parent.id = m.parent_id
		WHERE i.library_id = ? AND i.rel_path LIKE ? ESCAPE '\'
		LIMIT 1
	`, libraryID, escapeLike(folder)+"/%").Scan(&metaID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return metaID, err
}

// MergedFolderNames liefert die Namen anderer Top-Level-Ordner derselben
// Library, deren folder_metadata.metadata_id mit dem von `folder` übereinstimmt
// (also dieselbe Show, nur physisch in einem zweiten Ordner, z.B. getrennte
// "Show S01"/"Show S02"-Ordner) — für virtuelles Multi-Folder-Browsing in der
// Staffel-Ansicht, OHNE Dateien zu verschieben (siehe „Serienübersicht —
// Auto-Merge doppelter Serien-Ordner" in CLAUDE.md, dort bisher nur die
// Kachel-Ebene betroffen; dies erweitert es auf den tatsächlichen Episoden-
// Zugriff). Symmetrisch: funktioniert unabhängig davon, ob `folder` der von
// mergeFoldersBySameShow gewählte "Repräsentant" ist oder einer der anderen
// Geschwister-Ordner — beide liefern dieselbe Geschwister-Menge.
func (s *Store) MergedFolderNames(libraryID int64, folder string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT fm2.folder
		FROM folder_metadata fm1
		JOIN folder_metadata fm2
		  ON fm2.library_id = fm1.library_id
		 AND fm2.metadata_id = fm1.metadata_id
		 AND fm2.folder != fm1.folder
		WHERE fm1.library_id = ? AND fm1.folder = ? AND fm1.metadata_id IS NOT NULL
	`, libraryID, folder)
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
