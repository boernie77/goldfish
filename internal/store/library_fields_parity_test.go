package store

import (
	"reflect"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestListLibrariesForUserFieldParity fängt genau die Bug-Klasse ab, die am
// 2026-09-18 zum Datenleck-Verdacht führte: ListLibrariesForUser (Nicht-
// Admin-ACL-Pfad) hatte eine EIGENE, separate SQL-Query, die neue
// Library-Felder (channelLabelOnTop, deleteWatchedButtonEnabled,
// showReleaseDate) nicht selektierte — nur der Admin-Pfad (ListLibraries)
// tat das. Für einen Nicht-Admin kamen die Felder dadurch immer mit ihrem
// Zero-Value zurück, unabhängig vom echten DB-Wert (z. B. "showReleaseDate":
// false statt true).
//
// Dieser Test vergleicht PER REFLECTION jedes Feld von model.Library
// zwischen beiden Code-Pfaden für denselben Datensatz. Anders als eine
// Handliste einzelner Feldnamen fängt das auch jedes künftig neu
// hinzugefügte Library-Feld automatisch ab, falls jemand es nur in einer
// der beiden Queries ergänzt.
//
// WICHTIG: Das ist ein reiner Feld-Paritäts-Test, KEIN ACL-Sichtbarkeits-
// test (den gibt es separat in acl_test.go) — er sagt nichts darüber aus,
// OB ein User eine Library sehen darf, nur dass die zurückgelieferten
// Feldwerte für sichtbare Libraries identisch sind.
func TestListLibrariesForUserFieldParity(t *testing.T) {
	s := newTestStore(t)

	libA, err := s.CreateLibrary("A", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	libB, err := s.CreateLibrary("B", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	// Alle bekannten Toggle-Felder von ihrem Zero-Value wegdrehen, damit ein
	// vergessenes SELECT in ListLibrariesForUser nicht zufällig durch den
	// Default (meist 0/false) verdeckt wird.
	if err := s.SetLibraryChannelLabelOnTop(libA, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLibraryDeleteWatchedButtonEnabled(libA, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLibraryShowReleaseDate(libA, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLibraryOnHome(libA, true); err != nil {
		t.Fatal(err)
	}

	normalID, err := s.CreateUser("normal-parity", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	// ACL-Zugriff auf BEIDE Libraries, damit der Nicht-Admin-Pfad beide
	// zurückliefert und wir sie 1:1 mit dem Admin-Pfad vergleichen können.
	if err := s.SetUserLibraryAccess(normalID, []int64{libA, libB}); err != nil {
		t.Fatal(err)
	}

	adminView, err := s.ListLibraries()
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	userView, err := s.ListLibrariesForUser(normalID, false)
	if err != nil {
		t.Fatalf("ListLibrariesForUser: %v", err)
	}

	adminByID := map[int64]model.Library{}
	for _, l := range adminView {
		adminByID[l.ID] = l
	}
	if len(userView) != len(adminByID) {
		t.Fatalf("ListLibrariesForUser lieferte %d Libraries, ListLibraries (Admin-Referenz) %d — Testaufbau prüfen",
			len(userView), len(adminByID))
	}

	for _, got := range userView {
		want, ok := adminByID[got.ID]
		if !ok {
			t.Errorf("Library %d von ListLibrariesForUser, aber nicht in ListLibraries enthalten", got.ID)
			continue
		}
		compareLibraryFields(t, want, got)
	}
}

// compareLibraryFields vergleicht jedes exportierte Feld zweier
// model.Library-Werte per Reflection und meldet den Feldnamen mit, statt nur
// "structs differ" zu sagen — sonst muss man beim Fehlschlag erst debuggen,
// welches Feld überhaupt abweicht.
func compareLibraryFields(t *testing.T, want, got model.Library) {
	t.Helper()
	wv := reflect.ValueOf(want)
	gv := reflect.ValueOf(got)
	wt := wv.Type()
	for i := 0; i < wt.NumField(); i++ {
		field := wt.Field(i)
		wf := wv.Field(i).Interface()
		gf := gv.Field(i).Interface()
		if !reflect.DeepEqual(wf, gf) {
			t.Errorf("Library %d, Feld %q: ListLibrariesForUser=%v, ListLibraries=%v — "+
				"vermutlich fehlt das Feld in der SQL-Query von ListLibrariesForUser (users.go)",
				want.ID, field.Name, gf, wf)
		}
	}
}
