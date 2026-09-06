package store

import (
	"encoding/json"
	"sort"
)

// ListGenresForLibrary liefert alle verfügbaren, distinkten Genre-Werte
// einer Bibliothek — Grundlage für den Genre-Picker (User-Wunsch 2026-09-06:
// "im Ordner Musik sollen dort nur Musiktreffer stehen, bei Filmen oder
// Serien nur die dortigen möglichen Treffer"). Je nach Bibliothekstyp aus
// unterschiedlichen Quellen: movies/tv aus metadata.genres (TMDB-JSON-Array-
// String, in Go geparst statt per SQLite-JSON-Funktionen — robuster und ohne
// JSON1-Extension-Abhängigkeit), music aus items.genre (Tag-Wert pro Track).
// private hat kein Genre-Konzept, liefert immer eine leere Liste.
func (s *Store) ListGenresForLibrary(libraryID int64) ([]string, error) {
	lib, err := s.GetLibrary(libraryID)
	if err != nil || lib == nil {
		return nil, err
	}
	switch lib.Kind {
	case "movies", "tv":
		rows, err := s.db.Query(`
			SELECT DISTINCT m.genres FROM items i
			JOIN metadata m ON m.id = i.metadata_id
			WHERE i.library_id = ? AND m.genres IS NOT NULL AND m.genres != ''
		`, libraryID)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		set := map[string]struct{}{}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return nil, err
			}
			var list []string
			if err := json.Unmarshal([]byte(raw), &list); err != nil {
				continue // vereinzelte kaputte/leere Einträge überspringen, kein harter Fehler
			}
			for _, g := range list {
				if g != "" {
					set[g] = struct{}{}
				}
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return sortedKeys(set), nil
	case "music":
		rows, err := s.db.Query(`
			SELECT DISTINCT genre FROM items WHERE library_id = ? AND genre != ''
		`, libraryID)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		set := map[string]struct{}{}
		for rows.Next() {
			var g string
			if err := rows.Scan(&g); err != nil {
				return nil, err
			}
			set[g] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return sortedKeys(set), nil
	default:
		return []string{}, nil
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
