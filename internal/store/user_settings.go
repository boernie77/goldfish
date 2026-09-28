package store

import "strconv"

// user_settings.go — generische Pro-User-Key-Value-Einstellungen, analog zur
// globalen settings-Tabelle. Erster Einsatzzweck: Sichtbarkeit der globalen
// Startseiten-Streifen "▶ Fortsetzen"/"📺 Als nächstes" (2026-09-02).

// GetUserSettingBool liest einen Pro-User-Boolean-Wert ("0"/"1"). Fehlt die
// Zeile, gilt `def`.
func (s *Store) GetUserSettingBool(userID int64, key string, def bool) (bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE user_id = ? AND key = ?`, userID, key).Scan(&v)
	if err != nil {
		return def, nil // ErrNoRows und alles andere → Default (Komfort-Feature, kein Blocker)
	}
	return v == "1", nil
}

// SetUserSettingBool setzt (Upsert) einen Pro-User-Boolean-Wert.
func (s *Store) SetUserSettingBool(userID int64, key string, value bool) error {
	v := "0"
	if value {
		v = "1"
	}
	_, err := s.db.Exec(`
		INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`,
		userID, key, v)
	return err
}

// GetUserSettingInt liest einen Pro-User-Ganzzahlwert. Fehlt die Zeile oder
// ist der Wert kaputt, gilt `def`.
func (s *Store) GetUserSettingInt(userID int64, key string, def int) (int, error) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM user_settings WHERE user_id = ? AND key = ?`, userID, key).Scan(&v); err != nil {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, nil
	}
	return n, nil
}

// SetUserSettingInt setzt (Upsert) einen Pro-User-Ganzzahlwert.
func (s *Store) SetUserSettingInt(userID int64, key string, value int) error {
	_, err := s.db.Exec(`
		INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`,
		userID, key, strconv.Itoa(value))
	return err
}
