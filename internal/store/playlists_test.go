package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestPlaylistUserIsolation sichert ab, dass Playlists STRIKT pro Besitzer
// getrennt sind — auch für Admins. Regression-Test für einen echten
// Cross-User-Datenleck (User-Report 2026-09-02): Admin "Börnie" sah alle
// Playlists von User "Christian", weil ListPlaylistsForUser für Admins
// ungefiltert über ALLE User lief und requirePlaylistAccess Admins pauschal
// Zugriff auf jede fremde Playlist gab. Playlists sind private Kuratierung,
// kein Bibliotheks-Zugriffsrecht — hier gilt KEINE Admin-Ausnahme (anders
// als bei Library-ACL, siehe TestLibraryACL).
func TestPlaylistUserIsolation(t *testing.T) {
	s := newTestStore(t)

	christianID, _ := s.CreateUser("Christian", "pw123456", false)
	boernieID, _ := s.CreateUser("Boernie", "pw123456", true) // Admin!

	christianPl, err := s.CreatePlaylist(christianID, "Christians Playlist", "video")
	if err != nil {
		t.Fatal(err)
	}
	boerniePl, err := s.CreatePlaylist(boernieID, "Boernies Playlist", "video")
	if err != nil {
		t.Fatal(err)
	}

	// 1) Listing: jeder sieht NUR seine eigene(n) Playlist(s), Admin
	// eingeschlossen — trotz isAdmin=true darf Boernie Christians Playlist
	// nicht in seiner Liste sehen.
	christianList, err := s.ListPlaylistsForUser(christianID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(christianList) != 1 || christianList[0].ID != christianPl {
		t.Fatalf("Christian sollte nur seine eigene Playlist sehen, bekam: %+v", christianList)
	}

	boernieList, err := s.ListPlaylistsForUser(boernieID, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(boernieList) != 1 || boernieList[0].ID != boerniePl {
		t.Fatalf("Admin Boernie sollte NUR seine eigene Playlist sehen, nicht die von Christian: %+v", boernieList)
	}

	// 2) PlaylistOwner + der API-seitige Access-Check (requirePlaylistAccess)
	// bauen direkt auf PlaylistOwner auf — hier prüfen wir die Store-Quelle
	// der Wahrheit: der Owner von Christians Playlist ist Christian, nicht
	// Boernie, und keine Admin-Sonderrolle ändert das.
	owner, err := s.PlaylistOwner(christianPl)
	if err != nil {
		t.Fatal(err)
	}
	if owner != christianID {
		t.Fatalf("PlaylistOwner(christianPl) = %d, want %d (owner darf sich nicht durch Admin-Status ändern)", owner, christianID)
	}

	// 3) PlaylistsForItem ist user-gescoped: ein Item, das in Christians
	// Playlist liegt, darf für Boernies Abfrage NICHT auftauchen.
	lib, err := s.CreateLibrary("Lib", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO items (library_id, path, rel_path, title) VALUES (?, ?, ?, ?)`,
		lib, "/tmp/x/movie.mp4", "movie.mp4", "movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	itemID, _ := res.LastInsertId()
	if _, err := s.AddToPlaylist(christianPl, itemID); err != nil {
		t.Fatal(err)
	}

	boerniesHits, err := s.PlaylistsForItem(itemID, boernieID)
	if err != nil {
		t.Fatal(err)
	}
	if len(boerniesHits) != 0 {
		t.Fatalf("PlaylistsForItem darf für Boernie Christians Playlist nicht zeigen, bekam: %v", boerniesHits)
	}
	christiansHits, err := s.PlaylistsForItem(itemID, christianID)
	if err != nil {
		t.Fatal(err)
	}
	if len(christiansHits) != 1 || christiansHits[0] != christianPl {
		t.Fatalf("PlaylistsForItem(christianID) sollte christianPl zeigen, bekam: %v", christiansHits)
	}
}
