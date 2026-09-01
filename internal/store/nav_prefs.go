package store

// nav_prefs.go — pro-User-Sichtbarkeit + Reihenfolge der Bibliotheks-
// Reiterleiste (Topbar). Bewusst eine EIGENE Tabelle, getrennt von
// user_home_prefs (Startseiten-Streifen) — eine Library kann aus der
// Reiterleiste ausgeblendet sein und trotzdem auf der Startseite erscheinen,
// oder umgekehrt (User-Wunsch 2026-09-02).

// UserNavPref ist der pro-User-Override für eine Library in der Reiterleiste.
type UserNavPref struct {
	OnNav bool
	Order int
}

// GetUserNavPrefs liefert die pro-User-Overrides als Map library_id → Pref.
// Nur explizit gesetzte Zeilen sind enthalten; für alle übrigen Libraries
// gelten die globalen libraries.on_home (als Sichtbarkeits-Default) /
// sort_order-Defaults — die Reiterleiste hatte nie einen eigenen globalen
// Sichtbarkeits-Schalter, sie zeigte historisch immer alle ACL-Libraries.
func (s *Store) GetUserNavPrefs(userID int64) (map[int64]UserNavPref, error) {
	rows, err := s.db.Query(`SELECT library_id, on_nav, sort_order FROM user_nav_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UserNavPref{}
	for rows.Next() {
		var libID int64
		var onNav, order int
		if err := rows.Scan(&libID, &onNav, &order); err != nil {
			return nil, err
		}
		out[libID] = UserNavPref{OnNav: onNav == 1, Order: order}
	}
	return out, rows.Err()
}

// SetUserNavPref setzt (Upsert) den pro-User-Sichtbarkeits-Override für eine
// Library in der Reiterleiste. sort_order bleibt bei einem bestehenden
// Datensatz unangetastet; bei einer neuen Zeile wird der aktuelle globale
// sort_order als Startwert übernommen.
func (s *Store) SetUserNavPref(userID, libraryID int64, onNav bool) error {
	flag := 0
	if onNav {
		flag = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_nav_prefs (user_id, library_id, on_nav, sort_order)
		VALUES (?, ?, ?, COALESCE((SELECT sort_order FROM libraries WHERE id = ?), 0))
		ON CONFLICT(user_id, library_id) DO UPDATE SET on_nav = excluded.on_nav`,
		userID, libraryID, flag, libraryID)
	return err
}

// SetUserNavOrder schreibt die pro-User-Reihenfolge der Reiterleiste.
// `orderedLibraryIDs` ist die komplette, vom User per ▲▼ sortierte Liste —
// jede enthaltene Library bekommt ihren Index als sort_order. on_nav bleibt
// beim bestehenden Wert (falls schon eine Zeile existiert) bzw. übernimmt
// sonst true (die Reiterleiste zeigte historisch immer alle Libraries).
func (s *Store) SetUserNavOrder(userID int64, orderedLibraryIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, libID := range orderedLibraryIDs {
		if _, err := tx.Exec(`
			INSERT INTO user_nav_prefs (user_id, library_id, on_nav, sort_order)
			VALUES (?, ?, 1, ?)
			ON CONFLICT(user_id, library_id) DO UPDATE SET sort_order = excluded.sort_order`,
			userID, libID, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
