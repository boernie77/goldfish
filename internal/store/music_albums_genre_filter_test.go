package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestListMusicAlbumsFilteredByGenre sichert den Genre-Filter auf Album-
// Ebene ab (User-Wunsch 2026-09-06: der Genre-Filter muss auch in der
// Musik-Album-Übersicht wirken, nicht nur in der flachen Track-Liste).
func TestListMusicAlbumsFilteredByGenre(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "Rock Album/01 Song.mp3", "Band A", "Rock Album", "Rock")
	mustUpsertMusicItem(t, s, libID, "Pop Album/01 Song.mp3", "Band B", "Pop Album", "Pop")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListMusicAlbumsFiltered(libID, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 albums without filter, got %d", len(all))
	}

	rockOnly, err := s.ListMusicAlbumsFiltered(libID, 0, []string{"Rock"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rockOnly) != 1 || rockOnly[0].Album != "Rock Album" {
		t.Fatalf("expected only Rock Album, got %+v", rockOnly)
	}

	both, err := s.ListMusicAlbumsFiltered(libID, 0, []string{"Rock", "Pop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 {
		t.Fatalf("expected 2 albums with OR filter, got %d", len(both))
	}
}
