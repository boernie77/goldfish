package store

import (
	"testing"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

func TestImagesFoldersAndList(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("A", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	add := func(rel string) {
		t.Helper()
		if err := s.UpsertImage(&model.Image{LibraryID: lib, Path: "/x/" + rel, RelPath: rel, Title: rel, ModTime: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	add("Bilder/img2.jpg")
	add("Bilder/img10.jpg")
	add("Bilder/Urlaub/a.png")
	add("root.jpg")

	root, _ := s.ListImagesInFolder(lib, "")
	if len(root) != 1 || root[0].RelPath != "root.jpg" {
		t.Fatalf("root: %+v", root)
	}
	in, _ := s.ListImagesInFolder(lib, "Bilder")
	if len(in) != 2 || in[0].RelPath != "Bilder/img2.jpg" {
		t.Fatalf("Bilder (natürlich sortiert, Unterordner ausgeschlossen): %+v", in)
	}
	top, _ := s.ImageSubfolders(lib, "")
	if len(top) != 1 || top[0].Name != "Bilder" || top[0].ImageCount != 3 {
		t.Fatalf("top: %+v", top)
	}
	sub, _ := s.ImageSubfolders(lib, "Bilder")
	if len(sub) != 1 || sub[0].Name != "Bilder/Urlaub" || sub[0].ImageCount != 1 {
		t.Fatalf("sub: %+v", sub)
	}

	// Orphan-Cleanup: nur Bilder, die nicht im keep-Set stehen; Ausschlüsse bleiben.
	keep := map[string]struct{}{"/x/Bilder/img2.jpg": {}}
	n, err := s.DeleteImagesNotInSet(lib, "", keep, []string{"Bilder/Urlaub"})
	if err != nil || n != 2 { // img10 + root.jpg; Urlaub/a.png ist ausgeschlossen
		t.Fatalf("delete n=%d err=%v", n, err)
	}
	if c, _ := s.CountImages(lib); c != 2 {
		t.Fatalf("count=%d", c)
	}

	// Schalter + Parität über ListLibraries
	if err := s.SetLibraryImagesEnabled(lib, true); err != nil {
		t.Fatal(err)
	}
	l, _ := s.GetLibrary(lib)
	if l == nil || !l.ImagesEnabled {
		t.Fatal("ImagesEnabled nicht gesetzt")
	}
	// Bibliothek löschen räumt Bilder per CASCADE ab.
	if err := s.DeleteLibrary(lib); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.CountImages(lib); c != 0 {
		t.Fatalf("nach Löschen noch %d Bilder", c)
	}
}
