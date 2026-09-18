package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestListItemsIncludesMetadataConfirmed ist eine Regression (User-Report
// 2026-09-18: "die Kachel in der Duplikate-Ansicht zeigt weiterhin den alten
// Titel, obwohl ich die Zuordnung längst bestätigt/umbenannt habe"). ListItems
// (die Abfrage hinter Grid/Duplikate/Suche) selektierte metadata_confirmed
// gar nicht — jedes Item kam mit MetadataConfirmed=false zurück, egal was in
// der DB stand. Nur GetItem/GetItemFor (Einzelabruf, z. B. Infoseite) hatte
// die Spalte, deshalb sah die Detailseite richtig aus, aber jede Liste nicht.
func TestListItemsIncludesMetadataConfirmed(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	metaID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 98, Title: "Gladiator"})
	if err != nil {
		t.Fatal(err)
	}
	it := &model.Item{LibraryID: libID, Path: "/media/gladiator.mkv", RelPath: "gladiator.mkv", Title: "gladiator"}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.ListItems(ItemFilter{LibraryID: libID})
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Testaufbau: Item nicht gefunden, err=%v len=%d", err, len(loaded))
	}
	if err := s.SetItemMetadata(loaded[0].ID, metaID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadataConfirmed(loaded[0].ID, true); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListItems(ItemFilter{LibraryID: libID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("erwarte 1 Item, got %d", len(items))
	}
	if !items[0].MetadataConfirmed {
		t.Fatalf("ListItems lieferte MetadataConfirmed=false, obwohl in der DB bestätigt (Regression: SELECT-Spalte fehlte)")
	}
}
