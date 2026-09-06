package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestListItemsUnconfirmedFilter sichert den neuen "Alle Unbestätigten"-Filter
// (User-Wunsch 2026-09-06) ab: nur Items MIT TMDB-Zuordnung, deren
// metadata_confirmed nie gesetzt wurde — weder unmatched Items noch bereits
// bestätigte dürfen erscheinen.
func TestListItemsUnconfirmedFilter(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 1, Title: "Confirmed Movie"})
	if err != nil {
		t.Fatal(err)
	}
	meta2, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 2, Title: "Unconfirmed Movie"})
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

	confirmedID := newItem("confirmed.mkv")
	if err := s.SetItemMetadata(confirmedID, meta); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadataConfirmed(confirmedID, true); err != nil {
		t.Fatal(err)
	}

	unconfirmedID := newItem("unconfirmed.mkv")
	if err := s.SetItemMetadata(unconfirmedID, meta2); err != nil {
		t.Fatal(err)
	}
	// bewusst NICHT bestätigt

	newItem("nevermatched.mkv") // kein SetItemMetadata -> metadata_id NULL

	items, err := s.ListItems(ItemFilter{LibraryID: libID, MatchState: "unconfirmed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 unconfirmed item, got %d: %+v", len(items), items)
	}
	if items[0].ID != unconfirmedID {
		t.Errorf("expected the unconfirmed item, got id=%d", items[0].ID)
	}
}
