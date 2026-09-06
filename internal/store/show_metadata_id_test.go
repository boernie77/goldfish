package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestShowMetadataIDForFolder sichert ab, dass ShowMetadataIDForFolder die
// lokale metadata.id der Show liefert (nicht die TMDB-ID) — Voraussetzung
// für die Poster-Bearbeitung im Staffel-Header (User-Wunsch 2026-09-06:
// "auch bei Serien das Poster ändern"), die auf metadata.id statt tmdb_id
// arbeitet. Beide Fallback-Stufen werden geprüft: explizite
// folder_metadata-Zuordnung und der Episoden-Parent-Fallback.
func TestShowMetadataIDForFolder(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("via folder_metadata", func(t *testing.T) {
		showMeta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 100, Title: "Show A"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetFolderMetadata(libID, "Show A", showMeta); err != nil {
			t.Fatal(err)
		}
		got, err := s.ShowMetadataIDForFolder(libID, "Show A")
		if err != nil {
			t.Fatal(err)
		}
		if got != showMeta {
			t.Errorf("expected %d, got %d", showMeta, got)
		}
	})

	t.Run("via episode parent fallback", func(t *testing.T) {
		showMeta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 200, Title: "Show B"})
		if err != nil {
			t.Fatal(err)
		}
		epMeta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 201, ParentID: showMeta, Title: "Episode 1"})
		if err != nil {
			t.Fatal(err)
		}
		rel := filepath.Join("Show B", "S01E01.mkv")
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", rel), RelPath: rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		itemID, err := s.ItemIDByPath(it.Path)
		if err != nil || itemID == 0 {
			t.Fatalf("ItemIDByPath: id=%d err=%v", itemID, err)
		}
		if err := s.SetItemMetadata(itemID, epMeta); err != nil {
			t.Fatal(err)
		}
		got, err := s.ShowMetadataIDForFolder(libID, "Show B")
		if err != nil {
			t.Fatal(err)
		}
		if got != showMeta {
			t.Errorf("expected %d, got %d", showMeta, got)
		}
	})

	t.Run("no match returns 0", func(t *testing.T) {
		got, err := s.ShowMetadataIDForFolder(libID, "Nonexistent Show")
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Errorf("expected 0, got %d", got)
		}
	})
}
