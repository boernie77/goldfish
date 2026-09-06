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

	if err := s.UpdateMusicItemMetadata(id, "Neuer Titel", "Neue Band", "Altes Album", 3, "Pop", 2011); err != nil {
		t.Fatal(err)
	}

	it, err := s.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Neuer Titel" || it.Artist != "Neue Band" || it.TrackNo != 3 || it.Genre != "Pop" {
		t.Fatalf("unexpected item after update: %+v", it)
	}
	if it.ReleasedAt.Year() != 2011 {
		t.Fatalf("expected released_at year 2011, got %v", it.ReleasedAt)
	}

	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].Artist != "Neue Band" || albums[0].Genre != "Pop" {
		t.Fatalf("expected album re-grouped with updated artist/genre, got %+v", albums)
	}
}

// TestListMusicAlbumTracksIncludesGenre sichert ab, dass ListMusicAlbumTracks
// (Datenquelle für GET /api/albums/{id}, die tatsächliche Album-Detail-
// Trackliste im Frontend) das Genre pro Track mitliefert — Bug gefunden
// 2026-09-06: die Genre-Spalte in der Listenansicht blieb leer, weil dieser
// Query i.genre nicht SELECTed hatte (ListItems/GetItemFor waren bereits
// gefixt, dieser dritte Aufrufer wurde zunächst übersehen).
func TestListMusicAlbumTracksIncludesGenre(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "Band/Album/01 Song.mp3", "Band", "Album", "Jazz")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 1 {
		t.Fatalf("setup: albums=%v err=%v", albums, err)
	}
	tracks, err := s.ListMusicAlbumTracks(albums[0].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Genre != "Jazz" {
		t.Fatalf("expected track genre 'Jazz', got %+v", tracks)
	}
}
