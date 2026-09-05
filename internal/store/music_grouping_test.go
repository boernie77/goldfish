package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// mustUpsertMusicItem legt ein Item mit Musik-Tags an und gibt seine id zurück.
func mustUpsertMusicItem(t *testing.T, s *Store, libID int64, relPath, artist, album, genre string) int64 {
	t.Helper()
	it := &model.Item{
		LibraryID: libID,
		Path:      filepath.Join("/media", relPath),
		RelPath:   relPath,
		Artist:    artist,
		Album:     album,
		Genre:     genre,
	}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	id, err := s.ItemIDByPath(it.Path)
	if err != nil || id == 0 {
		t.Fatalf("ItemIDByPath(%q): id=%d err=%v", it.Path, id, err)
	}
	return id
}

// TestGroupMusicAlbumsFoldersOverridesInconsistentArtist sichert den
// Phantom-der-Oper-Fall ab: Tracks im selben Ordner mit demselben Album-Tag,
// aber unterschiedlichen Artist-Tags (kein album_artist vorhanden) müssen zu
// EINEM Album zusammengefasst werden, nicht zu einem pro abweichendem Artist.
func TestGroupMusicAlbumsFoldersOverridesInconsistentArtist(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}

	const folder = "Compilations/Das Phantom Der Oper (Die Höhepunkte Der Hamburger Aufführung)"
	mustUpsertMusicItem(t, s, libID, folder+"/07 Briefe.mp3", "Thomas Schulze", "Das Phantom Der Oper", "")
	mustUpsertMusicItem(t, s, libID, folder+"/1-01 Ouvertüre.mp3", "Andrew Lloyd Webber", "Das Phantom Der Oper", "Soundtrack")
	mustUpsertMusicItem(t, s, libID, folder+"/1-05 Titelsong.mp3", "Peter Hofmann, Andrew Lloyd Webber, Anna Maria Kaufmann", "Das Phantom Der Oper", "")

	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}

	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected exactly 1 album, got %d: %+v", len(albums), albums)
	}
	a := albums[0]
	if a.Album != "Das Phantom Der Oper" {
		t.Errorf("expected album title %q, got %q", "Das Phantom Der Oper", a.Album)
	}
	if a.Artist != "Verschiedene Interpreten" {
		t.Errorf("expected artist 'Verschiedene Interpreten' for inconsistent per-track artists, got %q", a.Artist)
	}
	if a.TrackCount != 3 {
		t.Errorf("expected 3 tracks grouped into the album, got %d", a.TrackCount)
	}
	if a.Genre != "Soundtrack" {
		t.Errorf("expected genre picked up from any track in the group, got %q", a.Genre)
	}
}

// TestGroupMusicAlbumsFolderFallbackWithoutAlbumTag: kein Track im Ordner hat
// ein Album-Tag -> der Ordnername selbst wird zum Album-Titel.
func TestGroupMusicAlbumsFolderFallbackWithoutAlbumTag(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	const folder = "Bootlegs/Live In Berlin 1987"
	mustUpsertMusicItem(t, s, libID, folder+"/01.mp3", "Some Band", "", "")
	mustUpsertMusicItem(t, s, libID, folder+"/02.mp3", "Some Band", "", "")

	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected 1 album, got %d: %+v", len(albums), albums)
	}
	if albums[0].Album != "Live In Berlin 1987" {
		t.Errorf("expected folder name as album fallback, got %q", albums[0].Album)
	}
	if albums[0].Artist != "Some Band" {
		t.Errorf("expected consistent artist to survive, got %q", albums[0].Artist)
	}
}

// TestGroupMusicAlbumsRootLevelFilesUseTagPair: Dateien direkt im
// Bibliotheks-Root (kein Unterordner) haben keinen gemeinsamen Ordner —
// die behalten das alte reine (artist,album)-Verhalten (kein "Verschiedene
// Interpreten"-Zusammenwurf über völlig unabhängige lose Singles).
func TestGroupMusicAlbumsRootLevelFilesUseTagPair(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "SingleA.mp3", "Artist A", "Single A", "")
	mustUpsertMusicItem(t, s, libID, "SingleB.mp3", "Artist B", "Single B", "")

	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 2 {
		t.Fatalf("expected 2 separate root-level albums, got %d: %+v", len(albums), albums)
	}
}

// TestGroupMusicAlbumsCleansUpOrphanedAlbums: eine music_albums-Zeile, auf
// die kein Item mehr zeigt (z.B. Rückstand einer früheren Gruppierungslogik
// nach einem Algorithmus-Wechsel), darf nach einem GroupMusicAlbums-Lauf
// nicht mehr als sichtbare "0 Titel"-Karteileiche auftauchen (User-Report
// 2026-09-05 — genau das passierte beim Wechsel von Artist- auf
// Ordner-basierte Gruppierung).
func TestGroupMusicAlbumsCleansUpOrphanedAlbums(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "Folder/track.mp3", "Artist", "Album", "")

	// Simuliert eine "Karteileiche" von einem früheren Gruppierungslauf, auf
	// die kein Item (mehr) zeigt.
	if _, err := s.db.Exec(
		`INSERT INTO music_albums(library_id, artist, album, genre) VALUES(?, 'Ghost Artist', 'Ghost Album', '')`,
		libID,
	); err != nil {
		t.Fatal(err)
	}
	var orphanID int64
	if err := s.db.QueryRow(`SELECT id FROM music_albums WHERE artist = 'Ghost Artist'`).Scan(&orphanID); err != nil {
		t.Fatal(err)
	}

	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}

	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected exactly 1 visible album (orphan must not leak through), got %d: %+v", len(albums), albums)
	}
	var orphanCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM music_albums WHERE id = ?`, orphanID).Scan(&orphanCount); err != nil {
		t.Fatal(err)
	}
	if orphanCount != 0 {
		t.Errorf("expected orphaned album row %d to be deleted by GroupMusicAlbums, still exists", orphanID)
	}
}

// TestGroupMusicAlbumsConsistentArtistSurvives: normales Album, jeder Track
// trägt denselben Artist-Tag -> keine "Verschiedene Interpreten"-Regression.
func TestGroupMusicAlbumsConsistentArtistSurvives(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	const folder = "ABBA/Voulez-Vous"
	mustUpsertMusicItem(t, s, libID, folder+"/01.mp3", "ABBA", "Voulez-Vous", "Pop")
	mustUpsertMusicItem(t, s, libID, folder+"/02.mp3", "ABBA", "Voulez-Vous", "Pop")

	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].Artist != "ABBA" {
		t.Fatalf("expected single ABBA album, got %+v", albums)
	}
}
