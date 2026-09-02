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

// TestLibraryACL sichert die Bibliotheks-Trennung ab. Ein Admin sieht IMMER
// alle Bibliotheken, unabhängig von etwaigen eigenen user_library_access-
// Zeilen — die 2026-08-31 eingeführte Admin-Selbsteinschränkung wurde am
// 2026-09-02 zurückgenommen (siehe Kommentar bei UserHasExplicitLibraryACL
// in users.go): eine neu angelegte Bibliothek tauchte für einen Admin mit
// eigener ACL-Liste nirgends mehr auf, auch nicht im Verwaltungs-Dialog.
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
	// restrictedAdmin: hat trotzdem eine ACL-Zeile auf nur B (z. B. weil er sie
	// früher mal für sich selbst gesetzt hatte) — muss als Admin TROTZDEM
	// beides sehen.
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
		{"admin mit ACL-Zeile sieht B trotzdem", restrictedAdminID, true, libB, true},
		{"admin mit ACL-Zeile sieht A trotzdem", restrictedAdminID, true, libA, true},
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
	check("admin mit ACL-Zeile sieht trotzdem alles", restrictedAdminID, true, 2)

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
