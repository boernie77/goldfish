// folders.go -- Ordner-Navigation (TV-Top-Level-Folder, Drilldown,
// Auto-Merge gleicher Show), aus sqlite.go ausgelagert (Schritt 7 der
// Modularisierung, siehe CLAUDE.md "Code-Review 2026-09-06"). Reine
// Funktionsverschiebung, keine Logik-/Signaturaenderung.
package store

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
)

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

// AllFolderPaths liefert ALLE Ordnerpfade einer Bibliothek (jede Ebene, nicht
// nur Top-Level) als sortierte, eindeutige Liste — für die Autocomplete-Liste
// im "Verschieben"-Dialog. Leitet die Ordner aus den vorhandenen rel_path-
// Verzeichnisanteilen ab (inkl. aller Zwischenebenen), nicht aus einer
// separaten Ordner-Tabelle — Goldfish legt Ordner nicht explizit an.
func (s *Store) AllFolderPaths(libraryID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT rel_path FROM items WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			return nil, err
		}
		segs := strings.Split(rel, "/")
		for i := 1; i < len(segs); i++ {
			seen[strings.Join(segs[:i], "/")] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// TopLevelFolders liefert alle direkten Unterordner einer Bibliothek mit Item-Zählung.
// Bei TV-Bibliotheken wird zusätzlich die Show-Metadata aus folder_metadata angehängt.
func (s *Store) TopLevelFolders(libraryID int64) ([]Folder, error) {
	return s.topLevelFolders(libraryID, false)
}

// topLevelFolders mit optionalem „nur unmatched"-Filter: liefert Folder, in
// denen mindestens ein Item metadata_id IS NULL hat. Wird vom Filter „Ohne
// TMDB-Zuordnung" genutzt. Folder ohne folder_metadata-Eintrag, deren
// Episoden alle via Auto-Match gemappt wurden (z. B. Columbo, Downton Abbey),
// sind aus User-Sicht „fertig" und tauchen hier nicht auf.
//
// Folder.ItemCount zählt seit 2026-09-05 EINDEUTIGE Episoden/Titel
// (COUNT DISTINCT über metadata_id, unmatched Items sowie per variant_split
// bewusst entkoppelte Items einzeln über ihre id — gleiche Konvention wie
// attachVariantCounts), nicht mehr rohe Dateizeilen — User-Anfrage: „die
// Kachel soll auch nur, so wie die Staffeln, die Anzahl unterschiedlicher
// Folgen zeigen". Vorher
// zählte ein simples COUNT(*) jede Datei einzeln, wodurch z. B. eine Folge,
// die als zwei Qualitäts-Varianten vorlag, die Kachel um 1 zu hoch zeigte
// (User-Report: "70 Folgen" auf der Kachel, aber nur 68 unterschiedliche
// Episoden). Deckt NICHT den Fall ab, dass dieselbe metadata_id in ZWEI
// gemergten Ordnern auftaucht (siehe mergeFoldersBySameShow) — dort wird
// weiterhin einfach summiert, ein in beiden Ordnern vorhandenes Duplikat
// würde dort doppelt gezählt. Für den ursprünglichen Bosch-Report (ein
// vermutlich unmatched Streufile im verwaisten Ordner) ist das irrelevant.
func (s *Store) topLevelFolders(libraryID int64, onlyUnmatched bool) ([]Folder, error) {
	postFilter := ""
	if onlyUnmatched {
		postFilter = " WHERE f.unmatched_cnt > 0"
	}
	// Wichtig: erst aggregieren, dann mit folder_metadata joinen — sonst führt der LEFT JOIN
	// vor dem GROUP BY zu Ambiguität beim "folder"-Alias in SQLite und alle Items landen
	// im selben Bucket.
	rows, err := s.db.Query(`
		SELECT f.folder, f.cnt, f.thumb_id, fm.metadata_id, f.added_at
		FROM (
			SELECT
				SUBSTR(rel_path, 1, INSTR(rel_path, '/')-1) AS folder,
				library_id,
				COUNT(DISTINCT CASE WHEN metadata_id IS NOT NULL AND COALESCE(variant_split,0)=0 THEN 'm'||metadata_id ELSE 'i'||id END) AS cnt,
				SUM(CASE WHEN metadata_id IS NULL THEN 1 ELSE 0 END) AS unmatched_cnt,
				MIN(CASE WHEN has_thumb=1 THEN id ELSE NULL END) AS thumb_id,
				MAX(added_at) AS added_at
			FROM items
			WHERE library_id = ? AND INSTR(rel_path, '/') > 0
			GROUP BY folder
		) f
		LEFT JOIN folder_metadata fm
		  ON fm.library_id = f.library_id AND fm.folder = f.folder`+postFilter+`
		ORDER BY f.folder COLLATE NATSORT
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
		var addedAt sql.NullString
		if err := rows.Scan(&f.Name, &f.ItemCount, &thumb, &meta, &addedAt); err != nil {
			return nil, err
		}
		if addedAt.Valid {
			f.AddedAt = addedAt.String
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
	return mergeFoldersBySameShow(out), nil
}

// mergeFoldersBySameShow fasst mehrere physische Ordner, die auf dieselbe TMDB-Show
// gematcht sind (z. B. ein verwaister "Bosch"-Ordner nur mit NFO/Poster neben dem
// echten "Bosch (2014)"-Ordner), zu EINER Kachel zusammen — User-Anfrage 2026-09-05:
// "warum ist dann in der Serienübersicht 2x die Serie Bosch drinnen". Rein
// anzeigeseitig (Kachel-Ebene), rührt keine Dateien/DB-Zeilen an. Repräsentant ist
// der Ordner mit den meisten Items (bei Gleichstand der zuerst gefundene, `out` ist
// bereits NATSORT-sortiert) — Klick auf die Kachel navigiert weiterhin nur zu diesem
// EINEN Ordner. Löst NICHT den allgemeineren Fall zweier Ordner mit jeweils echtem,
// unterschiedlichem Episoden-Inhalt (dafür bräuchte es echtes Multi-Folder-Browsing
// in SeriesOwnedEpisodes/ListItems — bewusst nicht Teil dieser Änderung).
func mergeFoldersBySameShow(folders []Folder) []Folder {
	byMeta := map[int64][]int{}
	for i, f := range folders {
		if f.MetadataID > 0 {
			byMeta[f.MetadataID] = append(byMeta[f.MetadataID], i)
		}
	}
	winner := map[int64]int{}
	skip := map[int]bool{}
	for metaID, idxs := range byMeta {
		if len(idxs) < 2 {
			continue
		}
		best := idxs[0]
		for _, i := range idxs[1:] {
			if folders[i].ItemCount > folders[best].ItemCount {
				best = i
			}
		}
		winner[metaID] = best
		for _, i := range idxs {
			if i != best {
				skip[i] = true
			}
		}
	}
	if len(skip) == 0 {
		return folders
	}
	out := make([]Folder, 0, len(folders))
	for i, f := range folders {
		if skip[i] {
			continue
		}
		if idxs := byMeta[f.MetadataID]; len(idxs) > 1 && winner[f.MetadataID] == i {
			total := 0
			for _, j := range idxs {
				total += folders[j].ItemCount
				if j != i {
					f.MergedFolders = append(f.MergedFolders, folders[j].Name)
				}
				if folders[j].AddedAt > f.AddedAt {
					f.AddedAt = folders[j].AddedAt
				}
			}
			f.ItemCount = total
		}
		out = append(out, f)
	}
	return out
}
