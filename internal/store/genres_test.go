package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestListGenresForLibrary sichert den Genre-Picker-Datenlieferanten ab
// (User-Wunsch 2026-09-06): movies/tv lesen aus metadata.genres (JSON-Array),
// music aus items.genre, private liefert immer eine leere Liste.
func TestListGenresForLibrary(t *testing.T) {
	s := newTestStore(t)

	movieLib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	meta1, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 1, Title: "A", Genres: `["Drama","Krimi"]`})
	if err != nil {
		t.Fatal(err)
	}
	meta2, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 2, Title: "B", Genres: `["Komödie"]`})
	if err != nil {
		t.Fatal(err)
	}
	it1 := &model.Item{LibraryID: movieLib, Path: "/media/a.mkv", RelPath: "a.mkv", Title: "a"}
	if err := s.UpsertItem(it1); err != nil {
		t.Fatal(err)
	}
	id1, _ := s.ItemIDByPath(it1.Path)
	if err := s.SetItemMetadata(id1, meta1); err != nil {
		t.Fatal(err)
	}
	it2 := &model.Item{LibraryID: movieLib, Path: "/media/b.mkv", RelPath: "b.mkv", Title: "b"}
	if err := s.UpsertItem(it2); err != nil {
		t.Fatal(err)
	}
	id2, _ := s.ItemIDByPath(it2.Path)
	if err := s.SetItemMetadata(id2, meta2); err != nil {
		t.Fatal(err)
	}

	genres, err := s.ListGenresForLibrary(movieLib)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Drama", "Komödie", "Krimi"}
	if len(genres) != len(want) {
		t.Fatalf("expected %v, got %v", want, genres)
	}
	for i, g := range want {
		if genres[i] != g {
			t.Errorf("index %d: expected %q, got %q", i, g, genres[i])
		}
	}

	musicLib, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	track := &model.Item{LibraryID: musicLib, Path: "/media/t.mp3", RelPath: "t.mp3", Title: "t", Genre: "Rock"}
	if err := s.UpsertItem(track); err != nil {
		t.Fatal(err)
	}
	musicGenres, err := s.ListGenresForLibrary(musicLib)
	if err != nil {
		t.Fatal(err)
	}
	if len(musicGenres) != 1 || musicGenres[0] != "Rock" {
		t.Fatalf("expected [Rock], got %v", musicGenres)
	}

	privateLib, err := s.CreateLibrary("Privat", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateGenres, err := s.ListGenresForLibrary(privateLib)
	if err != nil {
		t.Fatal(err)
	}
	if len(privateGenres) != 0 {
		t.Fatalf("expected empty, got %v", privateGenres)
	}
}

// TestListItemsGenreFilter sichert den generischen Genre-Filter in ListItems
// ab (matcht metadata.genres ODER items.genre, OR-verknüpft über mehrere
// gewählte Genres).
func TestListItemsGenreFilter(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	dramaMeta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 1, Title: "Drama Film", Genres: `["Drama"]`})
	if err != nil {
		t.Fatal(err)
	}
	comedyMeta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 2, Title: "Comedy Film", Genres: `["Komödie"]`})
	if err != nil {
		t.Fatal(err)
	}

	newItem := func(rel string, meta int64) {
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", rel), RelPath: rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		id, _ := s.ItemIDByPath(it.Path)
		if err := s.SetItemMetadata(id, meta); err != nil {
			t.Fatal(err)
		}
	}
	newItem("drama.mkv", dramaMeta)
	newItem("comedy.mkv", comedyMeta)

	items, err := s.ListItems(ItemFilter{LibraryID: libID, Genres: []string{"Drama"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "drama.mkv" {
		t.Fatalf("expected only drama.mkv, got %+v", items)
	}

	items, err = s.ListItems(ItemFilter{LibraryID: libID, Genres: []string{"Drama", "Komödie"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items with OR filter, got %d", len(items))
	}
}
