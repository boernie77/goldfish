package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

func TestMergeFoldersBySameShow(t *testing.T) {
	folders := []Folder{
		{Name: "Bosch", ItemCount: 2, MetadataID: 42},         // verwaister Ordner, nur NFO/Poster
		{Name: "Bosch (2014)", ItemCount: 70, MetadataID: 42}, // echter Ordner mit allen Folgen
		{Name: "Columbo", ItemCount: 45, MetadataID: 7},       // unbetroffene Serie
		{Name: "Ohne Zuordnung", ItemCount: 3, MetadataID: 0}, // kein Match -> nie gemergt
	}

	out := mergeFoldersBySameShow(folders)

	if len(out) != 3 {
		t.Fatalf("expected 3 folders after merge, got %d: %+v", len(out), out)
	}

	var bosch *Folder
	for i := range out {
		if out[i].MetadataID == 42 {
			bosch = &out[i]
		}
	}
	if bosch == nil {
		t.Fatal("merged Bosch folder not found")
	}
	if bosch.Name != "Bosch (2014)" {
		t.Errorf("expected representative folder with most items (Bosch (2014)), got %q", bosch.Name)
	}
	if bosch.ItemCount != 72 {
		t.Errorf("expected summed item count 72, got %d", bosch.ItemCount)
	}
	if len(bosch.MergedFolders) != 1 || bosch.MergedFolders[0] != "Bosch" {
		t.Errorf("expected MergedFolders=[Bosch], got %v", bosch.MergedFolders)
	}
}

func TestMergeFoldersBySameShowNoOp(t *testing.T) {
	folders := []Folder{
		{Name: "Columbo", ItemCount: 45, MetadataID: 7},
		{Name: "Ohne Zuordnung", ItemCount: 3, MetadataID: 0},
	}
	out := mergeFoldersBySameShow(folders)
	if len(out) != 2 {
		t.Fatalf("expected no-op when nothing to merge, got %+v", out)
	}
}

// TestTopLevelFoldersItemCountDedupesVariants sichert das eigentliche
// User-Anliegen ab: "Bosch (2014)" mit einer Folge, die zweimal (zwei
// Qualitäten, gleiche metadata_id) vorliegt, soll auf der Kachel als EINE
// Episode zählen, nicht als zwei Dateien — analog dazu, wie die Staffel-
// Ansicht Varianten zu einem Owned-Slot zusammenfasst.
func TestTopLevelFoldersItemCountDedupesVariants(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("TV", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	metaE1, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 1, Season: 1, Episode: 1, Title: "E01"})
	if err != nil {
		t.Fatal(err)
	}
	metaE2, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 1, Season: 1, Episode: 2, Title: "E02"})
	if err != nil {
		t.Fatal(err)
	}

	mustUpsertAndMatch := func(relPath string, metaID int64) {
		t.Helper()
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", relPath), RelPath: relPath}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		id, err := s.ItemIDByPath(it.Path)
		if err != nil || id == 0 {
			t.Fatalf("ItemIDByPath(%q): id=%d err=%v", it.Path, id, err)
		}
		if metaID > 0 {
			if err := s.SetItemMetadata(id, metaID); err != nil {
				t.Fatal(err)
			}
		}
	}

	// "Bosch (2014)": E01 liegt zweimal (zwei Qualitäten, gleiche metadata_id) -> 1 Episode.
	mustUpsertAndMatch("Bosch (2014)/S01/E01.mkv", metaE1)
	mustUpsertAndMatch("Bosch (2014)/S01/E01 (1080p).mkv", metaE1)
	mustUpsertAndMatch("Bosch (2014)/S01/E02.mkv", metaE2)
	// "Bosch": ein unmatched Streufile -> zählt einzeln (kein metadata_id zum Dedupen da).
	mustUpsertAndMatch("Bosch/stray.mkv", 0)

	folders, err := s.TopLevelFolders(libID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Folder{}
	for _, f := range folders {
		byName[f.Name] = f
	}

	if got := byName["Bosch (2014)"].ItemCount; got != 2 {
		t.Errorf("Bosch (2014): expected 2 eindeutige Episoden (nicht 3 Dateien), got %d", got)
	}
	if got := byName["Bosch"].ItemCount; got != 1 {
		t.Errorf("Bosch: expected 1 item, got %d", got)
	}
}
