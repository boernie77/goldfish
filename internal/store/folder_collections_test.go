package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestFolderCollections: Ordner-Sammlung (Tatort nach Kommissar, 2026-09-30)
// erscheint mit negativer ID, zählt Dateien rekursiv, aber nicht die eines
// Nachbarordners mit gleichem Präfix, und respektiert die Bibliotheks-ACL.
func TestFolderCollections(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Tatort/Finke/a.mp4", "Tatort/Thiel/b.mp4", "Tatort/c.mp4", "Tatort Spezial/d.mp4"} {
		if err := s.UpsertItem(&model.Item{LibraryID: libID, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: rel}); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.SetFolderCollection(libID, "Tatort", "Tatort")
	if err != nil || id <= 0 {
		t.Fatalf("Set: id=%d err=%v", id, err)
	}
	// Upsert: gleicher Ordner → gleiche ID, neuer Name.
	if id2, _ := s.SetFolderCollection(libID, "Tatort", "Tatort (Kommissare)"); id2 != id {
		t.Errorf("Upsert lieferte neue ID %d statt %d", id2, id)
	}

	admin, _ := s.CreateUser("admin", "pw", true)
	cs, err := s.ListFolderCollections(admin, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].ID != -id || cs[0].Kind != "folder" || cs[0].Folder != "Tatort" ||
		cs[0].LibraryID != libID || cs[0].MovieCount != 3 || cs[0].Name != "Tatort (Kommissare)" {
		t.Fatalf("Listing: %+v", cs)
	}

	// Nutzer ohne Zugriff auf die Bibliothek sieht die Sammlung nicht.
	kid, _ := s.CreateUser("kind", "pw", false)
	if err := s.SetUserLibraryAccess(kid, nil); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.ListFolderCollections(kid, false, 0); len(cs) != 0 {
		t.Errorf("ohne ACL sichtbar: %+v", cs)
	}

	if fc, _ := s.FindFolderCollection(libID, "Tatort"); fc == nil || fc.ID != id {
		t.Errorf("Find: %+v", fc)
	}
	if err := s.DeleteFolderCollection(libID, "Tatort"); err != nil {
		t.Fatal(err)
	}
	if fc, _ := s.GetFolderCollection(id); fc != nil {
		t.Errorf("nach Delete noch vorhanden: %+v", fc)
	}
}
