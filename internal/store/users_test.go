package store

import "testing"

// TestGetSessionCarriesMaxAgeRatingAndCanDownload sichert einen echten Bug ab
// (gefunden 2026-09-02): GetSession lud max_age_rating gar nicht mit — jeder
// per currentUser(r) geladene User hatte dadurch IMMER MaxAgeRating==nil,
// unabhängig vom tatsächlichen DB-Wert. Alle FSK-Checks im API-Layer hängen
// an genau diesem Feld (requireAgeAllowed, ListItems-Filter, Collections) —
// die Altersfreigabe griff dadurch bei keiner eingeloggten Session.
func TestGetSessionCarriesMaxAgeRatingAndCanDownload(t *testing.T) {
	s := newTestStore(t)

	uid, err := s.CreateUser("kid", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	age := 12
	if err := s.SetUserMaxAgeRating(uid, &age); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserCanDownload(uid, false); err != nil {
		t.Fatal(err)
	}

	sess, err := s.CreateSession(uid, 3600*1e9)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.GetSession(sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil {
		t.Fatal("expected user from session")
	}
	if u.MaxAgeRating == nil || *u.MaxAgeRating != 12 {
		t.Errorf("expected MaxAgeRating=12 from GetSession, got %v", u.MaxAgeRating)
	}
	if u.CanDownload {
		t.Error("expected CanDownload=false from GetSession")
	}

	// Default für neue User ohne expliziten Toggle: Downloads erlaubt.
	uid2, err := s.CreateUser("default-user", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := s.GetUser(uid2)
	if err != nil {
		t.Fatal(err)
	}
	if !u2.CanDownload {
		t.Error("expected CanDownload=true by default for a new user")
	}
}
