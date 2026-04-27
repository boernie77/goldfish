package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// UpsertPerson legt eine Person an oder aktualisiert Namen/Profilbild.
// Liefert die interne Person-ID.
func (s *Store) UpsertPerson(tmdbID int64, name, profilePath string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO people(tmdb_id, name, profile_path, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(tmdb_id) DO UPDATE SET
			name = excluded.name,
			profile_path = excluded.profile_path,
			updated_at = excluded.updated_at
		RETURNING id
	`, tmdbID, name, profilePath, time.Now()).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetPersonByTMDB lädt eine Person anhand der TMDB-ID.
func (s *Store) GetPersonByTMDB(tmdbID int64) (*model.Person, error) {
	var p model.Person
	var pp sql.NullString
	err := s.db.QueryRow(
		`SELECT id, tmdb_id, name, COALESCE(profile_path,'') FROM people WHERE tmdb_id = ?`, tmdbID,
	).Scan(&p.ID, &p.TMDBID, &p.Name, &pp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pp.Valid {
		p.ProfilePath = pp.String
	}
	return &p, nil
}

// ReplaceMetadataCast ersetzt die Cast-Einträge einer Metadata mit der angegebenen
// role-Kategorie ("main" oder "guest").
func (s *Store) ReplaceMetadataCast(metadataID int64, role string, entries []model.CastMember) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		`DELETE FROM metadata_cast WHERE metadata_id = ? AND role = ?`, metadataID, role,
	); err != nil {
		return err
	}
	for _, c := range entries {
		if c.PersonID == 0 {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO metadata_cast(metadata_id, person_id, character, role, ord)
			VALUES(?,?,?,?,?)
			ON CONFLICT(metadata_id, person_id, role) DO UPDATE SET
				character = excluded.character,
				ord = excluded.ord
		`, metadataID, c.PersonID, c.Character, role, c.Order); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetMetadataCast liefert die Cast-Liste einer Metadata, sortiert nach role (main zuerst)
// und ord. Bei Episoden zieht der Aufrufer zusätzlich die main-Credits des Parent-Shows.
func (s *Store) GetMetadataCast(metadataID int64) ([]model.CastMember, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.tmdb_id, p.name, COALESCE(p.profile_path,''),
		       COALESCE(mc.character,''), mc.role, mc.ord
		FROM metadata_cast mc
		JOIN people p ON p.id = mc.person_id
		WHERE mc.metadata_id = ?
		ORDER BY CASE mc.role WHEN 'main' THEN 0 ELSE 1 END, mc.ord
	`, metadataID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.CastMember
	for rows.Next() {
		var c model.CastMember
		if err := rows.Scan(&c.PersonID, &c.TMDBID, &c.Name, &c.ProfilePath,
			&c.Character, &c.Role, &c.Order); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MetadataHasCast prüft, ob bereits Cast-Einträge für diese Metadata existieren.
// Nützlich, um Backfill-Läufe übersichtlich zu halten.
func (s *Store) MetadataHasCast(metadataID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM metadata_cast WHERE metadata_id = ?`, metadataID,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkMetadataCastFetched setzt cast_fetched_at = now für diese Metadata, damit
// wir den TMDB-Call nicht endlos wiederholen, auch wenn keine Cast-Einträge kamen.
func (s *Store) MarkMetadataCastFetched(metadataID int64) error {
	_, err := s.db.Exec(
		`UPDATE metadata SET cast_fetched_at = ? WHERE id = ?`,
		time.Now(), metadataID,
	)
	return err
}

// MetadataIDsMissingCast liefert bis zu `limit` Metadata-IDs ohne Cast-Einträge.
// Filtert auf Typen, die Cast haben (movie/tv/episode, nicht omdb_*).
// Überspringt Einträge, bei denen `cast_fetched_at` bereits gesetzt ist — auch
// wenn dort (noch) keine metadata_cast-Zeilen existieren (TMDB ohne Treffer).
func (s *Store) MetadataIDsMissingCast(limit int) ([]model.Metadata, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.tmdb_type, m.tmdb_id, COALESCE(m.parent_id, 0),
		       COALESCE(m.season, 0), COALESCE(m.episode, 0)
		FROM metadata m
		WHERE m.tmdb_type IN ('movie','tv','episode')
		  AND m.cast_fetched_at IS NULL
		ORDER BY m.id
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Metadata
	for rows.Next() {
		var m model.Metadata
		if err := rows.Scan(&m.ID, &m.TMDBType, &m.TMDBID, &m.ParentID, &m.Season, &m.Episode); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
