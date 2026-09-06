package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestPendingMusicMetadataAlbumsScope sichert die Abgrenzung ab: Alben mit
// bereits vollständigen Tags (Genre UND Jahr vorhanden) tauchen nie auf,
// Alben mit fehlendem Genre ODER Jahr schon — unabhängig vom Cover-Status
// (PendingMusicMetadataAlbums ist ein eigenständiges Gate von
// PendingMusicAlbums, siehe Kommentar in music.go).
func TestPendingMusicMetadataAlbumsScope(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "Vollständig/01 Song.mp3", "Band A", "Album A", "Rock")
	mustUpsertMusicItem(t, s, libID, "OhneGenre/01 Song.mp3", "Band B", "Album B", "")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 2 {
		t.Fatalf("setup: albums=%v err=%v", albums, err)
	}
	var complete, incomplete model.MusicAlbum
	for _, a := range albums {
		if a.Artist == "Band A" {
			complete = a
		} else {
			incomplete = a
		}
	}
	// "Vollständig" bekommt Jahr manuell gesetzt, damit nur noch "OhneGenre" fehlt.
	if err := s.ApplyMusicBrainzMetadata(complete.ID, "mbid-a", 2020, ""); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingMusicMetadataAlbums(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != incomplete.ID {
		t.Fatalf("expected only the incomplete album pending, got %+v", pending)
	}
}

// TestApplyMusicBrainzMetadataDoesNotOverwriteExisting sichert die
// Fallback-Priorität: ein bereits vorhandener Wert (aus Tags) wird NIE von
// MusicBrainz überschrieben — weder auf music_albums noch auf den einzelnen
// Tracks.
func TestApplyMusicBrainzMetadataDoesNotOverwriteExisting(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	itemID := mustUpsertMusicItem(t, s, libID, "Band/Album/01 Song.mp3", "Band", "Album", "Jazz")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 1 {
		t.Fatalf("setup: albums=%v err=%v", albums, err)
	}
	albumID := albums[0].ID

	if err := s.ApplyMusicBrainzMetadata(albumID, "mbid-x", 1999, "Klassik"); err != nil {
		t.Fatal(err)
	}

	album, err := s.GetMusicAlbum(albumID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if album.Genre != "Jazz" {
		t.Fatalf("expected tag genre 'Jazz' to survive, got %q", album.Genre)
	}
	if album.Year != 1999 {
		t.Fatalf("expected MB year to fill the empty year, got %d", album.Year)
	}

	it, err := s.GetItem(itemID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Genre != "Jazz" {
		t.Fatalf("expected track genre to stay 'Jazz' (tag wins), got %q", it.Genre)
	}
}

// TestApplyMusicBrainzMetadataFillsMissingGenreOnTrack sichert die
// Propagation auf Tracks OHNE eigenes Genre-Tag ab.
func TestApplyMusicBrainzMetadataFillsMissingGenreOnTrack(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	itemID := mustUpsertMusicItem(t, s, libID, "Band/Album/01 Song.mp3", "Band", "Album", "")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 1 {
		t.Fatalf("setup: albums=%v err=%v", albums, err)
	}

	if err := s.ApplyMusicBrainzMetadata(albums[0].ID, "mbid-y", 2005, "Hard Rock"); err != nil {
		t.Fatal(err)
	}

	it, err := s.GetItem(itemID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Genre != "Hard Rock" {
		t.Fatalf("expected MB genre to fill the empty track genre, got %q", it.Genre)
	}
}

// TestGetMusicMetadataStat sichert die Vollständigkeits-Aggregation für die
// Statistik-Ansicht (User-Wunsch 2026-09-06: sehen, "wieviel der Titel
// komplett mit Metadaten versehen sind").
func TestGetMusicMetadataStat(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "Vollständig/01 Song.mp3", "Band A", "Album A", "Rock")
	mustUpsertMusicItem(t, s, libID, "OhneGenre/01 Song.mp3", "Band B", "Album B", "")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}

	stat, err := s.GetMusicMetadataStat(libID, "")
	if err != nil {
		t.Fatal(err)
	}
	if stat.TotalTracks != 2 || stat.TracksWithArtist != 2 || stat.TracksWithGenre != 1 {
		t.Fatalf("unexpected track stats: %+v", stat)
	}
	if stat.TotalAlbums != 2 || stat.AlbumsWithGenre != 1 || stat.AlbumsWithYear != 0 {
		t.Fatalf("unexpected album stats: %+v", stat)
	}
}
