package store

import (
	"database/sql"
	"errors"
	"time"
)

// --- Gesehen-Sync zwischen zwei Usern (user_watch_links) ---
//
// Eine Zeile pro Paar (user_a_id < user_b_id, per CHECK-Constraint erzwungen).
// requester_id merkt sich, wer die Anfrage gestellt hat (für die UI-Anzeige
// "wartet auf Bestätigung von …"). status durchläuft pending → accepted;
// Ablehnen/Trennen löscht die Zeile einfach wieder (keine History nötig).

func normalizeWatchLinkPair(userID, partnerID int64) (a, b int64) {
	if userID < partnerID {
		return userID, partnerID
	}
	return partnerID, userID
}

// RequestWatchLink legt eine neue Anfrage an, oder bestätigt eine bereits
// bestehende Gegenanfrage automatisch (A fragt B an, B hatte bereits A
// angefragt → sofort "accepted" statt zweimal pending).
func (s *Store) RequestWatchLink(userID, partnerID int64) error {
	if userID == partnerID {
		return errors.New("kann nicht mit sich selbst verknüpft werden")
	}
	a, b := normalizeWatchLinkPair(userID, partnerID)
	var existingStatus string
	var existingRequester int64
	err := s.db.QueryRow(
		`SELECT status, requester_id FROM user_watch_links WHERE user_a_id = ? AND user_b_id = ?`,
		a, b,
	).Scan(&existingStatus, &existingRequester)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if existingStatus == "accepted" {
			return nil
		}
		if existingRequester != userID {
			// Gegenseitige Anfrage → direkt bestätigen.
			_, err := s.db.Exec(
				`UPDATE user_watch_links SET status='accepted', confirmed_at=? WHERE user_a_id=? AND user_b_id=?`,
				time.Now(), a, b)
			return err
		}
		// Eigene Anfrage steht schon (pending) — nichts zu tun.
		return nil
	}
	_, err = s.db.Exec(
		`INSERT INTO user_watch_links(user_a_id, user_b_id, requester_id, status) VALUES(?, ?, ?, 'pending')`,
		a, b, userID)
	return err
}

// ConfirmWatchLink bestätigt eine eingehende Anfrage. Fehlschlägt, wenn keine
// pending-Anfrage an userID vorliegt oder userID selbst der Requester war.
func (s *Store) ConfirmWatchLink(userID, partnerID int64) error {
	a, b := normalizeWatchLinkPair(userID, partnerID)
	res, err := s.db.Exec(
		`UPDATE user_watch_links SET status='accepted', confirmed_at=?
		 WHERE user_a_id=? AND user_b_id=? AND status='pending' AND requester_id != ?`,
		time.Now(), a, b, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("keine offene Anfrage von diesem Benutzer")
	}
	return nil
}

// UnlinkWatchLink trennt eine bestehende Verknüpfung oder lehnt eine
// eingehende Anfrage ab — in beiden Fällen wird die Zeile einfach gelöscht.
func (s *Store) UnlinkWatchLink(userID, partnerID int64) error {
	a, b := normalizeWatchLinkPair(userID, partnerID)
	_, err := s.db.Exec(`DELETE FROM user_watch_links WHERE user_a_id=? AND user_b_id=?`, a, b)
	return err
}

// WatchLinkInfo beschreibt den Verknüpfungsstatus aus Sicht von userID.
type WatchLinkInfo struct {
	PartnerID   int64  `json:"partnerId"`
	PartnerName string `json:"partnerName"`
	// Status: "accepted" | "pending_outgoing" (ich habe angefragt, warte auf
	// Bestätigung) | "pending_incoming" (der andere hat angefragt, ich muss
	// bestätigen).
	Status string `json:"status"`
}

// GetWatchLinks liefert alle Verknüpfungen (aktiv + offen, beide Richtungen)
// von userID — i. d. R. maximal eine, aber der Picker soll auch mehrere
// gleichzeitig offene eingehende Anfragen anzeigen können.
func (s *Store) GetWatchLinks(userID int64) ([]WatchLinkInfo, error) {
	rows, err := s.db.Query(`
		SELECT
			CASE WHEN wl.user_a_id = ? THEN wl.user_b_id ELSE wl.user_a_id END AS partner_id,
			u.username,
			wl.status,
			wl.requester_id
		FROM user_watch_links wl
		JOIN users u ON u.id = CASE WHEN wl.user_a_id = ? THEN wl.user_b_id ELSE wl.user_a_id END
		WHERE wl.user_a_id = ? OR wl.user_b_id = ?
	`, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []WatchLinkInfo{}
	for rows.Next() {
		var info WatchLinkInfo
		var status string
		var requesterID int64
		if err := rows.Scan(&info.PartnerID, &info.PartnerName, &status, &requesterID); err != nil {
			return nil, err
		}
		if status == "accepted" {
			info.Status = "accepted"
		} else if requesterID == userID {
			info.Status = "pending_outgoing"
		} else {
			info.Status = "pending_incoming"
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// ActiveWatchPartnerIDs liefert die User-IDs aller Partner mit status=accepted
// — das sind die Ziele der Gesehen-Propagation beim SetWatchedFor.
func (s *Store) ActiveWatchPartnerIDs(userID int64) ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT CASE WHEN user_a_id = ? THEN user_b_id ELSE user_a_id END
		FROM user_watch_links
		WHERE (user_a_id = ? OR user_b_id = ?) AND status = 'accepted'
	`, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
