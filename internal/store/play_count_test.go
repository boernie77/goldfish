package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestTouchLastPlayedIncrementsPlayCount sichert den 2026-09-14-Zusatz ab
// (User-Wunsch: "wie oft abgespielt" als Musik-Listenspalte) — TouchLastPlayed
// zählt play_count bei jedem Aufruf um 1 hoch, unabhängig von last_played_at.
func TestTouchLastPlayedIncrementsPlayCount(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	itemID := mustUpsertMusicItem(t, s, libID, "Band/01 Song.mp3", "Band", "Album", "Rock")
	userID, err := s.CreateUser("tester", "pw", false)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := s.TouchLastPlayed(userID, itemID); err != nil {
			t.Fatal(err)
		}
	}

	items, err := s.ListItems(ItemFilter{LibraryID: libID, UserID: userID, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].PlayCount != 3 {
		t.Errorf("expected playCount=3, got %d", items[0].PlayCount)
	}
	if items[0].LastPlayedAt == nil {
		t.Errorf("expected LastPlayedAt to be set")
	}
}

// TestListMusicAlbumsAggregatesPlayCountAndLastPlayed sichert die
// Album-weite Aggregation ab (SUM(play_count) / MAX(last_played_at) über
// alle Tracks eines Albums) — User-Wunsch: dieselben Spalten auch in der
// Album-Übersicht.
func TestListMusicAlbumsAggregatesPlayCountAndLastPlayed(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	track1 := mustUpsertMusicItem(t, s, libID, "Band/01 Song.mp3", "Band", "Album", "Rock")
	track2 := mustUpsertMusicItem(t, s, libID, "Band/02 Song.mp3", "Band", "Album", "Rock")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.CreateUser("tester2", "pw", false)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.TouchLastPlayed(userID, track1); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchLastPlayed(userID, track1); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchLastPlayed(userID, track2); err != nil {
		t.Fatal(err)
	}

	albums, err := s.ListMusicAlbums(libID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected 1 album, got %d", len(albums))
	}
	if albums[0].PlayCount != 3 {
		t.Errorf("expected album playCount=3 (2+1), got %d", albums[0].PlayCount)
	}
	if albums[0].LastPlayedAt == nil {
		t.Errorf("expected album LastPlayedAt to be set")
	}
	if albums[0].AddedAt.IsZero() {
		t.Errorf("expected album AddedAt to be set")
	}
}
