package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestUpdateMusicItemMetadata sichert die manuelle Tag-Korrektur ab
// (User-Wunsch 2026-09-06: "Bei Musik fehlt grundsätzlich noch, die
// Metadaten zu bearbeiten") — inkl. dass ein geänderter Artist/Album-Wert
// die Album-Gruppierung tatsächlich neu zieht (GroupMusicAlbums-Re-Trigger).
func TestUpdateMusicItemMetadata(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	id := mustUpsertMusicItem(t, s, libID, "Alte Band/01 Song.mp3", "Alte Band", "Altes Album", "Rock")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateMusicItemMetadata(id, "Neuer Titel", "Neue Band", "Altes Album", 3, "Pop"); err != nil {
		t.Fatal(err)
	}

	it, err := s.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Neuer Titel" || it.Artist != "Neue Band" || it.TrackNo != 3 || it.Genre != "Pop" {
		t.Fatalf("unexpected item after update: %+v", it)
	}

	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].Artist != "Neue Band" || albums[0].Genre != "Pop" {
		t.Fatalf("expected album re-grouped with updated artist/genre, got %+v", albums)
	}
}
