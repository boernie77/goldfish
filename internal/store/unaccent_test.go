package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestSearchIsAccentInsensitive sichert den User-Wunsch ab: eine Suche nach
// "senorita" (ohne Tilde) soll auch "Señorita" finden, gilt für Titel,
// Künstler und Album gleichermaßen.
func TestSearchIsAccentInsensitive(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}

	it := &model.Item{LibraryID: libID, Path: "/media/senorita.mp3", RelPath: "senorita.mp3", Title: "Señorita", Artist: "Cámila"}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListItems(ItemFilter{LibraryID: libID, Search: "senorita"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 hit searching 'senorita', got %d", len(items))
	}

	items, err = s.ListItems(ItemFilter{LibraryID: libID, Search: "camila"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 hit searching 'camila' (artist), got %d", len(items))
	}

	// Umgekehrt muss die Suche mit Akzent weiterhin funktionieren.
	items, err = s.ListItems(ItemFilter{LibraryID: libID, Search: "Señorita"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 hit searching 'Señorita', got %d", len(items))
	}
}
