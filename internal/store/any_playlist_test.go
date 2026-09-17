package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// 🔴 AnyPlaylist (`playlistId=any`) ist der Filter fuer die
// Playlist-UEBERSICHT: Zufallswiedergabe und Filter sollen dort ueber ALLE
// Playlists des Nutzers wirken. Genau dabei ist ein Cross-User-Leak leicht
// gebaut — deshalb dieselbe Sichtbarkeitsregel wie ListPlaylistsForUser:
// eigene Playlists plus besitzerlose Alt-Playlists (letztere nur fuer
// Admins). **Playlists sind private Kuratierung, es gibt KEINE
// Admin-Ausnahme auf fremde Playlists** (siehe TestPlaylistUserIsolation).
func TestAnyPlaylistFilterRespectsOwnership(t *testing.T) {
	s := newTestStore(t)

	christianID, _ := s.CreateUser("Christian", "pw123456", false)
	boernieID, _ := s.CreateUser("Boernie", "pw123456", true) // Admin!

	lib, err := s.CreateLibrary("Lib", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ ListItems blendet fuer NICHT-Admins alle Bibliotheken ohne
	// user_library_access-Zeile aus — ohne diese Freigabe kaeme jede Abfrage
	// leer zurueck und der Test wuerde aus dem falschen Grund gruen/rot.
	for _, uid := range []int64{christianID, boernieID} {
		if err := s.SetUserLibraryAccess(uid, []int64{lib}); err != nil {
			t.Fatal(err)
		}
	}
	mkItem := func(name string) int64 {
		it := &model.Item{
			LibraryID: lib, Path: "/tmp/x/" + name, RelPath: name, Title: name,
			Container: "mkv", VideoCodec: "h264", AudioCodec: "aac",
			Width: 1920, Height: 1080, DurationSec: 100, SizeBytes: 1000, BitrateKbps: 1000,
		}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		// ⚠ UpsertItem schreibt die vergebene ID NICHT in das übergebene
		// Item zurück — über den (eindeutigen) Pfad nachschlagen.
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM items WHERE path = ?`, it.Path).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	christiansItem := mkItem("christian.mp4")
	boerniesItem := mkItem("boernie.mp4")
	freierItem := mkItem("frei.mp4") // in keiner Playlist

	christianPl, err := s.CreatePlaylist(christianID, "Christians Playlist", "video")
	if err != nil {
		t.Fatal(err)
	}
	boerniePl, err := s.CreatePlaylist(boernieID, "Boernies Playlist", "video")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddToPlaylist(christianPl, christiansItem); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddToPlaylist(boerniePl, boerniesItem); err != nil {
		t.Fatal(err)
	}

	idsOf := func(userID int64, isAdmin bool) map[int64]bool {
		t.Helper()
		items, err := s.ListItems(ItemFilter{UserID: userID, IsAdmin: isAdmin, AnyPlaylist: true})
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]bool{}
		for _, it := range items {
			got[it.ID] = true
		}
		return got
	}

	// Christian sieht nur sein eigenes Playlist-Item.
	c := idsOf(christianID, false)
	if !c[christiansItem] {
		t.Error("Christian muss sein eigenes Playlist-Item sehen")
	}
	if c[boerniesItem] {
		t.Error("🔴 LECK: Christian sieht ein Item aus Boernies Playlist")
	}
	if c[freierItem] {
		t.Error("Item in keiner Playlist darf bei AnyPlaylist nicht auftauchen")
	}

	// Der ADMIN darf Christians Playlist ebenfalls nicht sehen — Playlists
	// sind privat, hier gilt keine Admin-Ausnahme.
	b := idsOf(boernieID, true)
	if !b[boerniesItem] {
		t.Error("Boernie muss sein eigenes Playlist-Item sehen")
	}
	if b[christiansItem] {
		t.Error("🔴 LECK: Admin sieht ein Item aus Christians privater Playlist")
	}
}

// Eine konkrete PlaylistID hat Vorrang vor AnyPlaylist — sonst wuerde eine
// geoeffnete Playlist plötzlich Titel aus allen anderen mitziehen.
func TestExplicitPlaylistIDWinsOverAnyPlaylist(t *testing.T) {
	s := newTestStore(t)
	userID, _ := s.CreateUser("Christian", "pw123456", false)
	lib, err := s.CreateLibrary("Lib", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	// Siehe Kommentar im ersten Test: ohne Library-Freigabe liefert
	// ListItems fuer Nicht-Admins grundsaetzlich nichts.
	if err := s.SetUserLibraryAccess(userID, []int64{lib}); err != nil {
		t.Fatal(err)
	}
	mkItem := func(name string) int64 {
		it := &model.Item{
			LibraryID: lib, Path: "/tmp/x/" + name, RelPath: name, Title: name,
			Container: "mkv", VideoCodec: "h264", AudioCodec: "aac",
			Width: 1920, Height: 1080, DurationSec: 100, SizeBytes: 1000, BitrateKbps: 1000,
		}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		// ⚠ UpsertItem schreibt die vergebene ID NICHT in das übergebene
		// Item zurück — über den (eindeutigen) Pfad nachschlagen.
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM items WHERE path = ?`, it.Path).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	inA, inB := mkItem("a.mp4"), mkItem("b.mp4")

	plA, err := s.CreatePlaylist(userID, "A", "video")
	if err != nil {
		t.Fatal(err)
	}
	plB, err := s.CreatePlaylist(userID, "B", "video")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddToPlaylist(plA, inA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddToPlaylist(plB, inB); err != nil {
		t.Fatal(err)
	}

	// Beides gesetzt: die konkrete Playlist gewinnt.
	items, err := s.ListItems(ItemFilter{UserID: userID, PlaylistID: plA, AnyPlaylist: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != inA {
		t.Fatalf("PlaylistID muss AnyPlaylist ueberstimmen, bekam: %+v", items)
	}

	// Nur AnyPlaylist: beide Titel.
	all, err := s.ListItems(ItemFilter{UserID: userID, AnyPlaylist: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("AnyPlaylist sollte beide Playlist-Titel liefern, bekam %d", len(all))
	}
}

// Ein Item in MEHREREN Playlists darf nur EINMAL zurueckkommen (EXISTS statt
// JOIN) — sonst erschiene es im Raster doppelt und die Zufallswiedergabe
// zoege es ueberproportional oft.
func TestAnyPlaylistNoDuplicatesAcrossPlaylists(t *testing.T) {
	s := newTestStore(t)
	userID, _ := s.CreateUser("Christian", "pw123456", false)
	lib, err := s.CreateLibrary("Lib", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	// Siehe Kommentar im ersten Test: ohne Library-Freigabe liefert
	// ListItems fuer Nicht-Admins grundsaetzlich nichts.
	if err := s.SetUserLibraryAccess(userID, []int64{lib}); err != nil {
		t.Fatal(err)
	}
	it := &model.Item{
		LibraryID: lib, Path: "/tmp/x/dup.mp4", RelPath: "dup.mp4", Title: "dup.mp4",
		Container: "mkv", VideoCodec: "h264", AudioCodec: "aac",
		Width: 1920, Height: 1080, DurationSec: 100, SizeBytes: 1000, BitrateKbps: 1000,
	}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	var itemID int64
	if err := s.db.QueryRow(`SELECT id FROM items WHERE path = ?`, it.Path).Scan(&itemID); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"P1", "P2", "P3"} {
		pl, err := s.CreatePlaylist(userID, name, "video")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddToPlaylist(pl, itemID); err != nil {
			t.Fatal(err)
		}
	}

	items, err := s.ListItems(ItemFilter{UserID: userID, AnyPlaylist: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("Item in 3 Playlists muss genau einmal erscheinen, bekam %d", len(items))
	}
}