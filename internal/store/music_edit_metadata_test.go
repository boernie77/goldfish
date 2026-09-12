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
	if it.Title != "Neuer Titel" || it.Artist != "Neue Band" || it.TrackNo != 3 || it.Genre != "Pop" || it.Year != 2011 {
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

// TestUpdateMusicItemMetadataOverwritesExistingAlbumGenre sichert den
// 2026-09-12-Bugfix für den TRACK-Edit-Pfad ab (die Schwester von
// TestUpdateMusicAlbumMetadataOverwritesExistingGenre für den ALBUM-Edit-
// Pfad in einer anderen Datei): TestUpdateMusicItemMetadata oben ändert
// bewusst/versehentlich auch den Artist mit — das erzeugt über den
// (library_id,artist,album)-Konfliktschlüssel eine BRANDNEUE music_albums-
// Zeile (Insert statt Update) und umgeht den schützenden UPSERT-CASE-Guard
// dadurch zufällig. Dieser Test hält Artist/Album bewusst UNVERÄNDERT, nur
// das Genre ändert sich — genau der Fall, der beim User real fehlschlug
// (Titelübersicht zeigte das neue Genre, Albenübersicht das alte).
func TestUpdateMusicItemMetadataOverwritesExistingAlbumGenre(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	id := mustUpsertMusicItem(t, s, libID, "Hoerbuch/kapitel1.mp3", "Autor", "Mein Hoerbuch", "OldGenre")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateMusicItemMetadata(id, "Kapitel 1", "Autor", "Mein Hoerbuch", 1, "NewGenre", 0); err != nil {
		t.Fatal(err)
	}

	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 1 {
		t.Fatalf("expected 1 album, got %+v err=%v", albums, err)
	}
	if albums[0].Genre != "NewGenre" {
		t.Fatalf("expected album aggregate genre to follow the track edit, got %+v", albums[0])
	}
}

// TestUpdateMusicItemMetadataYearZeroKeepsExisting sichert ab, dass ein
// leeres Jahr-Feld (year=0) den bestehenden Wert NICHT löscht — anders als
// Genre/Artist/Album/Titel, die immer überschrieben werden, gibt es für
// Jahr keinen "leer setzen"-Weg über dieses Formular (0 ist kein gültiger
// Jahreswert, sondern bedeutet "unverändert lassen").
func TestUpdateMusicItemMetadataYearZeroKeepsExisting(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	id := mustUpsertMusicItem(t, s, libID, "Band/01 Song.mp3", "Band", "Album", "Rock")
	if err := s.UpdateMusicItemMetadata(id, "Song", "Band", "Album", 1, "Rock", 1983); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMusicItemMetadata(id, "Song", "Band", "Album", 1, "Pop", 0); err != nil {
		t.Fatal(err)
	}
	it, err := s.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Year != 1983 || it.Genre != "Pop" {
		t.Fatalf("expected year to stay 1983 (genre updated to Pop), got %+v", it)
	}
}

// TestUpdateMusicAlbumMetadataOverwritesExistingGenre sichert den
// 2026-09-12-Bugfix ab: GroupMusicAlbums' UPSERT schützt ein bereits
// gesetztes music_albums.genre/.year bewusst vor dem automatischen
// Neuberechnen bei einem Scan — das verhinderte aber auch, dass ein
// expliziter Admin-Edit über UpdateMusicAlbumMetadata je in der
// Aggregat-Zeile ankam (User-Report: Titelübersicht zeigte das neue Genre
// korrekt, die Album-Kachel/-Liste weiterhin das alte).
func TestUpdateMusicAlbumMetadataOverwritesExistingGenre(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsertMusicItem(t, s, libID, "Old Album/01 Song.mp3", "Old Artist", "Old Album", "OldGenre")
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 1 {
		t.Fatalf("expected 1 album, got %+v err=%v", albums, err)
	}
	albumID := albums[0].ID

	if err := s.UpdateMusicAlbumMetadata(albumID, "Old Artist", "Old Album", "NewGenre", 2011); err != nil {
		t.Fatal(err)
	}

	albums, err = s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != 1 {
		t.Fatalf("expected 1 album after update, got %+v err=%v", albums, err)
	}
	if albums[0].Genre != "NewGenre" || albums[0].Year != 2011 {
		t.Fatalf("expected album aggregate to reflect the edit, got %+v", albums[0])
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
