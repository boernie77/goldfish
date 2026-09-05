package store

import "testing"

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
