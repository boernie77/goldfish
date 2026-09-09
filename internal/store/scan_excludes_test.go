package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

func TestSetScanExcludedFolderToggle(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}

	list, err := s.ListScanExcludedFolders(libID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("erwartete leere Liste, bekam %v", list)
	}

	if err := s.SetScanExcludedFolder(libID, "USB-Platte", true); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListScanExcludedFolders(libID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != "USB-Platte" {
		t.Fatalf("erwartete [USB-Platte], bekam %v", list)
	}

	// Erneutes Setzen (INSERT OR IGNORE) darf keinen Duplikat-Fehler werfen.
	if err := s.SetScanExcludedFolder(libID, "USB-Platte", true); err != nil {
		t.Fatalf("erneutes Setzen sollte idempotent sein: %v", err)
	}

	if err := s.SetScanExcludedFolder(libID, "USB-Platte", false); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListScanExcludedFolders(libID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("erwartete leere Liste nach Entfernen, bekam %v", list)
	}
}

func TestIsRelPathExcluded(t *testing.T) {
	cases := []struct {
		name     string
		relPath  string
		excluded []string
		want     bool
	}{
		{"exakter Treffer", "USB-Platte", []string{"USB-Platte"}, true},
		{"echtes Unterverzeichnis", "USB-Platte/Filme/x.mkv", []string{"USB-Platte"}, true},
		{"Namens-Präfix-Kollision KEIN Treffer", "USB-Platte2/x.mkv", []string{"USB-Platte"}, false},
		{"nicht betroffen", "Andere Lib/x.mkv", []string{"USB-Platte"}, false},
		{"leerer Ausschluss = ganze Bibliothek", "irgendwas/x.mkv", []string{""}, true},
		{"kein Ausschluss konfiguriert", "USB-Platte/x.mkv", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRelPathExcluded(c.relPath, c.excluded); got != c.want {
				t.Errorf("IsRelPathExcluded(%q, %v) = %v, want %v", c.relPath, c.excluded, got, c.want)
			}
		})
	}
}

func TestItemPathsUnderFolders(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	addItem := func(rel string) {
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", rel), RelPath: rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	addItem("USB-Platte/Film A.mkv")
	addItem("USB-Platte/Unterordner/Film B.mkv")
	addItem("Andere/Film C.mkv")

	paths, err := s.ItemPathsUnderFolders(libID, []string{"USB-Platte"})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("erwartete 2 geschützte Pfade, bekam %d: %v", len(paths), paths)
	}

	// leere Ordnerliste → nichts geschützt (kein Ausschluss konfiguriert)
	none, err := s.ItemPathsUnderFolders(libID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("erwartete keine geschützten Pfade ohne Ausschlüsse, bekam %v", none)
	}
}
