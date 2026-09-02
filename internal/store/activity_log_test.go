package store

import "testing"

func TestActivityLog(t *testing.T) {
	s := newTestStore(t)

	if err := s.LogActivity(1, "admin", "auth", "login", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.LogActivity(0, "unknown", "auth", "login_failed", "Anmeldung fehlgeschlagen"); err != nil {
		t.Fatal(err)
	}
	if err := s.LogActivity(1, "admin", "job", "scan_run", `"Filme", gesamte Bibliothek`); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListActivityLog(ActivityLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(all))
	}
	// Neueste zuerst.
	if all[0].Action != "scan_run" {
		t.Errorf("expected newest first (scan_run), got %s", all[0].Action)
	}

	authOnly, err := s.ListActivityLog(ActivityLogFilter{Category: "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if len(authOnly) != 2 {
		t.Fatalf("expected 2 auth entries, got %d", len(authOnly))
	}

	adminOnly, err := s.ListActivityLog(ActivityLogFilter{Username: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(adminOnly) != 2 {
		t.Fatalf("expected 2 entries for username=admin, got %d", len(adminOnly))
	}

	// Pagination: mit beforeId auf die älteste Zeile zeigen sollte nur ältere liefern.
	oldest := all[len(all)-1]
	page, err := s.ListActivityLog(ActivityLogFilter{BeforeID: oldest.ID + 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != oldest.ID {
		t.Fatalf("expected pagination to return oldest entry, got %+v", page)
	}
}
