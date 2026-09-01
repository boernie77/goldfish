package store

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
