// libraries.go -- Bibliotheks-CRUD (Anlegen/Loeschen/Umbenennen, Multi-Path,
// Sortierung), aus sqlite.go ausgelagert (Schritt 7 der Modularisierung,
// siehe CLAUDE.md "Code-Review 2026-09-06"). Reine Funktionsverschiebung,
// keine Logik-/Signaturaenderung.
package store

import (
	"database/sql"
	"errors"

	"github.com/boernie77/goldfish/internal/model"
)

func (s *Store) ListLibraries() ([]model.Library, error) {
	rows, err := s.db.Query(`SELECT id, name, path, kind, COALESCE(on_home, 1), COALESCE(sort_order, 0), COALESCE(channel_label_on_top, 1), created_at FROM libraries ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Library
	for rows.Next() {
		var l model.Library
		var kind string
		var onHome, channelTop int
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &kind, &onHome, &l.SortOrder, &channelTop, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Kind = model.LibraryKind(kind)
		l.OnHome = onHome == 1
		l.ChannelLabelOnTop = channelTop == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetLibrary(id int64) (*model.Library, error) {
	var l model.Library
	var kind string
	var onHome, channelTop int
	err := s.db.QueryRow(`SELECT id, name, path, kind, COALESCE(on_home, 1), COALESCE(sort_order, 0), COALESCE(channel_label_on_top, 1), created_at FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Path, &kind, &onHome, &l.SortOrder, &channelTop, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	l.Kind = model.LibraryKind(kind)
	l.OnHome = onHome == 1
	l.ChannelLabelOnTop = channelTop == 1
	return &l, err
}

// SetLibraryChannelLabelOnTop togglet das Card-Layout fuer eine Library
// (siehe model.Library.ChannelLabelOnTop). Wirkt erst nach Neu-Laden der
// Items-Liste im Client.
func (s *Store) SetLibraryChannelLabelOnTop(libraryID int64, v bool) error {
	flag := 0
	if v {
		flag = 1
	}
	_, err := s.db.Exec(`UPDATE libraries SET channel_label_on_top = ? WHERE id = ?`, flag, libraryID)
	return err
}

// SetLibraryOrder schreibt die User-definierte Reihenfolge atomar in einer TX.
// IDs in der uebergebenen Reihenfolge bekommen sort_order 1..N. IDs die nicht
// uebergeben werden bleiben unveraendert (kein DELETE/Reset, falls fremde
// Libraries dazukommen sollten).
func (s *Store) SetLibraryOrder(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE libraries SET sort_order = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
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
//
// Realer Bug (2026-09-02, User-Report): `libraries.path` (die alte
// Single-Path-Spalte von vor dem Multi-Path-Feature, UNIQUE constraint,
// dient heute nur noch als Fallback für `scanner.go`/`admin_rename.go`,
// falls `library_paths` mal leer sein sollte) wurde bei Entfernen NIE
// nachgezogen. Entfernte man den ursprünglichen Erstellungs-Pfad einer
// Library wieder, blieb er trotzdem in `libraries.path` UNIQUE-reserviert —
// unsichtbar in der Ordner-Liste, aber ein Anlegen einer NEUEN Library mit
// genau diesem Pfad schlug mit "UNIQUE constraint failed: libraries.path"
// fehl. Zusätzlich gab es keine Sperre gegen das Entfernen des LETZTEN
// verbleibenden Pfads — leer gewordene `library_paths` hätte den Scanner
// (Zeile `if len(paths) == 0 { paths = []string{lib.Path} }`) STILLSCHWEIGEND
// auf den (eigentlich entfernten) alten Pfad zurückfallen lassen. Fix: (1)
// Entfernen des letzten Pfads wird abgelehnt, (2) `libraries.path` wird beim
// Entfernen des aktuell dort hinterlegten Pfads auf einen verbleibenden
// echten Pfad nachgezogen, damit die Spalte nie einen stillen Karteileichen-
// Wert behält.
func (s *Store) DeleteLibraryPath(libraryID int64, path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM library_paths WHERE library_id = ?`, libraryID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastLibraryPath
	}

	if _, err := tx.Exec(`DELETE FROM library_paths WHERE library_id = ? AND path = ?`, libraryID, path); err != nil {
		return err
	}

	var currentPrimary string
	if err := tx.QueryRow(`SELECT path FROM libraries WHERE id = ?`, libraryID).Scan(&currentPrimary); err != nil {
		return err
	}
	if currentPrimary == path {
		var replacement string
		if err := tx.QueryRow(`SELECT path FROM library_paths WHERE library_id = ? ORDER BY path LIMIT 1`, libraryID).Scan(&replacement); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE libraries SET path = ? WHERE id = ?`, replacement, libraryID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RepairOrphanedLibraryPaths behebt bereits VOR diesem Fix entstandene
// Karteileichen in `libraries.path` (siehe Kommentar bei `DeleteLibraryPath`)
// — für jede Library, deren `libraries.path` nicht mehr in `library_paths`
// auftaucht, wird die Spalte auf einen tatsächlich noch existierenden Pfad
// der Library gesetzt. Einmaliger Backfill, aufgerufen aus `main.go`.
func (s *Store) RepairOrphanedLibraryPaths() (int, error) {
	rows, err := s.db.Query(`
		SELECT l.id, l.path
		FROM libraries l
		WHERE NOT EXISTS (
			SELECT 1 FROM library_paths lp WHERE lp.library_id = l.id AND lp.path = l.path
		)
	`)
	if err != nil {
		return 0, err
	}
	type orphan struct {
		id   int64
		path string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.path); err != nil {
			_ = rows.Close()
			return 0, err
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	_ = rows.Close()

	fixed := 0
	for _, o := range orphans {
		var replacement string
		err := s.db.QueryRow(`SELECT path FROM library_paths WHERE library_id = ? ORDER BY path LIMIT 1`, o.id).Scan(&replacement)
		if err != nil {
			// Kein einziger Pfad mehr vorhanden (sollte durch die neue Sperre in
			// DeleteLibraryPath nicht mehr vorkommen, aber vor diesem Fix denkbar) —
			// nichts zu reparieren, Library bleibt wie sie ist statt sie kaputt zu machen.
			continue
		}
		if _, err := s.db.Exec(`UPDATE libraries SET path = ? WHERE id = ?`, replacement, o.id); err != nil {
			return fixed, err
		}
		fixed++
	}
	return fixed, nil
}

func (s *Store) UpdateLibraryKind(id int64, kind model.LibraryKind) error {
	_, err := s.db.Exec(`UPDATE libraries SET kind = ? WHERE id = ?`, string(kind), id)
	return err
}

func (s *Store) DeleteLibrary(id int64) error {
	_, err := s.db.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	return err
}
