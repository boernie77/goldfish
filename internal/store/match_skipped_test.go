package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// Ein bewusst entferntes Film-Match darf nicht im Auto-Matcher landen
// (PendingItems), bis jemand es wieder von Hand zuordnet.
func TestMatchSkippedKeepsItemOutOfPending(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 1, Title: "Falscher Treffer"})
	if err != nil {
		t.Fatal(err)
	}
	newItem := func(rel string) int64 {
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", rel), RelPath: rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		id, err := s.ItemIDByPath(it.Path)
		if err != nil || id == 0 {
			t.Fatalf("ItemIDByPath: id=%d err=%v", id, err)
		}
		return id
	}
	pending := func() map[int64]bool {
		items, err := s.PendingItems(100)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64]bool{}
		for _, it := range items {
			m[it.ID] = true
		}
		return m
	}

	skipped := newItem("ohne-treffer.mkv")
	normal := newItem("noch-nie-versucht.mkv")

	// Zuordnung setzen, dann bewusst entfernen (so läuft der API-Handler).
	if err := s.SetItemMetadata(skipped, meta); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadata(skipped, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMatchSkipped(skipped, true); err != nil {
		t.Fatal(err)
	}
	p := pending()
	if p[skipped] {
		t.Error("bewusst unzugeordnetes Item darf nicht in PendingItems stehen")
	}
	if !p[normal] {
		t.Error("nie versuchtes Item muss weiter in PendingItems stehen")
	}
	if !s.ItemMatchSkipped(skipped) || s.ItemMatchSkipped(normal) {
		t.Error("ItemMatchSkipped liefert falsche Werte")
	}

	// Ein Rescan (Upsert auf denselben Pfad) behält die Sperre.
	if err := s.UpsertItem(&model.Item{LibraryID: libID, Path: filepath.Join("/media", "ohne-treffer.mkv"), RelPath: "ohne-treffer.mkv", Title: "ohne-treffer.mkv", SizeBytes: 5}); err != nil {
		t.Fatal(err)
	}
	if !s.ItemMatchSkipped(skipped) {
		t.Error("Rescan darf die Sperre nicht löschen")
	}

	// Manuelle Zuordnung hebt die Sperre auf.
	if err := s.ConfirmItemMatch(skipped, meta); err != nil {
		t.Fatal(err)
	}
	if s.ItemMatchSkipped(skipped) {
		t.Error("manuelle Zuordnung muss die Sperre aufheben")
	}
	if err := s.SetItemMatchSkipped(normal, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadata(normal, meta); err != nil {
		t.Fatal(err)
	}
	if s.ItemMatchSkipped(normal) {
		t.Error("SetItemMetadata mit ID muss die Sperre aufheben")
	}
}
