package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestForceAdminOnlyLibraries sichert den generischen Hardening-Mechanismus
// ab (SetForceAdminOnlyLibraries) — unabhängig von jeder konkreten
// Deployment-Konfiguration. Eine als "immer admin-only" konfigurierte
// Library darf für einen Non-Admin NIRGENDS auftauchen, auch nicht mit einer
// expliziten ACL-Zeile dafür.
func TestForceAdminOnlyLibraries(t *testing.T) {
	s := newTestStore(t)

	locked, err := s.CreateLibrary("Locked", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	open, err := s.CreateLibrary("Open", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}

	nonAdminID, _ := s.CreateUser("member", "pw123456", false)
	adminID, _ := s.CreateUser("boss", "pw123456", true)

	// Non-Admin bekommt explizit Zugriff auf BEIDE Libraries — der Hardening-
	// Mechanismus muss trotzdem "Locked" ausblenden.
	if err := s.SetUserLibraryAccess(nonAdminID, []int64{locked, open}); err != nil {
		t.Fatal(err)
	}

	s.SetForceAdminOnlyLibraries([]string{"Locked"})

	if ok, _ := s.UserHasLibraryAccess(nonAdminID, locked, false); ok {
		t.Error("Non-Admin sollte trotz ACL-Zeile KEINEN Zugriff auf die gesperrte Library haben")
	}
	if ok, _ := s.UserHasLibraryAccess(nonAdminID, open, false); !ok {
		t.Error("Non-Admin sollte weiterhin Zugriff auf die offene Library haben")
	}
	if ok, _ := s.UserHasLibraryAccess(adminID, locked, true); !ok {
		t.Error("Admin sollte IMMER Zugriff haben, auch auf die gesperrte Library")
	}

	libs, err := s.ListLibrariesForUser(nonAdminID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range libs {
		if l.ID == locked {
			t.Error("ListLibrariesForUser sollte die gesperrte Library für den Non-Admin nicht zurückgeben")
		}
	}

	items, err := s.ListItems(ItemFilter{UserID: nonAdminID, IsAdmin: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.LibraryID == locked {
			t.Error("ListItems sollte keine Items aus der gesperrten Library für den Non-Admin liefern")
		}
	}

	// Ohne Konfiguration (leer) ist der Mechanismus ein No-op.
	s.SetForceAdminOnlyLibraries(nil)
	if ok, _ := s.UserHasLibraryAccess(nonAdminID, locked, false); !ok {
		t.Error("nach dem Zurücksetzen sollte die ACL-Zeile wieder normal greifen")
	}
}
