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

// 🔴 Regression (gefixt 2026-09-17, User-Wunsch: "Bei Serien den Serientitel
// oder den Schauspieler. Nicht nach Folgen Titeln, oder Beschreibungen"):
// eine Titelsuche muss bei Episoden den SERIENTITEL treffen, nicht den
// Episodentitel. Live in der DB gefunden: eine Episode von "One Tree Hill"
// hieß in metadata.title "Plötzlich ist alles anders" — der Serientitel
// steckt nur im Parent-Datensatz. Vorher fand die Suche nach "One Tree Hill"
// KEINE Episode dieser Serie, und eine Suche nach dem Episodentitel traf
// (unerwünscht) sehr wohl.
func TestSearchMatchesShowTitleNotEpisodeTitle(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	showID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 1, Title: "One Tree Hill"})
	if err != nil {
		t.Fatal(err)
	}
	epID, err := s.UpsertMetadata(&model.Metadata{
		TMDBType: "episode", TMDBID: 2, ParentID: showID,
		Title: "Plötzlich ist alles anders", Season: 1, Episode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	it := &model.Item{
		LibraryID: libID, Path: "/media/oth-s01e01.mkv", RelPath: "One Tree Hill/oth-s01e01.mkv",
		Title: "tvs-one-tree-hill-104",
	}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	// UpsertItem setzt keine ID am übergebenen Pointer — frisch nachladen.
	loaded, err := s.ListItems(ItemFilter{LibraryID: libID})
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Testaufbau: Item nicht gefunden, err=%v len=%d", err, len(loaded))
	}
	if err := s.SetItemMetadata(loaded[0].ID, epID); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListItems(ItemFilter{LibraryID: libID, Search: "One Tree Hill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("Suche nach Serientitel 'One Tree Hill' haette die Episode finden muessen, %d Treffer", len(items))
	}

	items, err = s.ListItems(ItemFilter{LibraryID: libID, Search: "Plötzlich ist alles anders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("Suche nach dem Episodentitel haette NICHTS finden sollen (User-Wunsch), %d Treffer", len(items))
	}
}
