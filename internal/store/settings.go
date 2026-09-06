// settings.go -- Key-Value-Settings-Tabelle, aus sqlite.go ausgelagert
// (Schritt 7 der Modularisierung, siehe CLAUDE.md "Code-Review 2026-09-06").
// Reine Funktionsverschiebung, keine Logik-/Signaturaenderung.
package store

import (
	"database/sql"
	"errors"
)

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
