package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "acl_test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestLibraryACL sichert die Bibliotheks-Trennung ab — inklusive der Regel
// (2026-08-31): eine explizite ACL greift AUCH für einen Admin.
func TestLibraryACL(t *testing.T) {
	s := newTestStore(t)

	libA, err := s.CreateLibrary("A", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	libB, err := s.CreateLibrary("B", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}

	normalID, _ := s.CreateUser("normal", "pw123456", false)
	adminID, _ := s.CreateUser("admin", "pw123456", true)
	restrictedAdminID, _ := s.CreateUser("radmin", "pw123456", true)

	// normal: nur A
	if err := s.SetUserLibraryAccess(normalID, []int64{libA}); err != nil {
		t.Fatal(err)
	}
	// restrictedAdmin: nur B (obwohl Admin!)
	if err := s.SetUserLibraryAccess(restrictedAdminID, []int64{libB}); err != nil {
		t.Fatal(err)
	}
	// admin: keine ACL-Zeile → sieht alles

	type tc struct {
		name    string
		user    int64
		isAdmin bool
		lib     int64
		want    bool
	}
	for _, c := range []tc{
		{"normal sieht A", normalID, false, libA, true},
		{"normal sieht B NICHT", normalID, false, libB, false},
		{"admin ohne ACL sieht A", adminID, true, libA, true},
		{"admin ohne ACL sieht B", adminID, true, libB, true},
		{"restricted-admin sieht B", restrictedAdminID, true, libB, true},
		{"restricted-admin sieht A NICHT", restrictedAdminID, true, libA, false},
	} {
		got, err := s.UserHasLibraryAccess(c.user, c.lib, c.isAdmin)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: UserHasLibraryAccess = %v, want %v", c.name, got, c.want)
		}
	}

	// ListLibrariesForUser
	check := func(name string, uid int64, admin bool, wantN int) {
		libs, err := s.ListLibrariesForUser(uid, admin)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(libs) != wantN {
			t.Errorf("%s: ListLibrariesForUser -> %d libs, want %d", name, len(libs), wantN)
		}
	}
	check("normal", normalID, false, 1)
	check("admin ohne ACL", adminID, true, 2)
	check("restricted-admin", restrictedAdminID, true, 1)

	// ListItems: die zentrale Sperre greift auch, wenn kein libraryId-Filter
	// gesetzt ist (library-übergreifend).
	countItems := func(name string, uid int64, admin bool) int {
		items, err := s.ListItems(ItemFilter{UserID: uid, IsAdmin: admin})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return len(items)
	}
	// (keine Items in der DB → 0, aber der Query darf nicht failen und die
	// ACL-Klausel muss syntaktisch valide sein für alle drei Fälle)
	_ = countItems("normal", normalID, false)
	_ = countItems("admin", adminID, true)
	_ = countItems("restricted-admin", restrictedAdminID, true)

	// Explizite ACL wieder leeren → Admin sieht wieder alles.
	if err := s.SetUserLibraryAccess(restrictedAdminID, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.UserHasLibraryAccess(restrictedAdminID, libA, true); !ok {
		t.Errorf("nach ACL-Leeren sollte der Admin wieder Zugriff auf A haben")
	}
}
