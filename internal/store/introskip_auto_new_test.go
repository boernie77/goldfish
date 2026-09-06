package store

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestNewIntroSkipCandidateFolders sichert das "Neue Serien automatisch
// aktivieren"-Feature (User-Wunsch 2026-09-06) ab: ein Top-Level-Ordner ohne
// jede Historie ist ein Kandidat; ein aktivierter, ein explizit
// deaktivierter (seen, aber nicht mehr aktiv) und der aktuell aktive Ordner
// selbst sind es nicht.
func TestNewIntroSkipCandidateFolders(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	addItem := func(rel string) {
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", rel), RelPath: rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	addItem("Show Active/S01E01.mkv")
	addItem("Show Deactivated/S01E01.mkv")
	addItem("Show Never Touched/S01E01.mkv")

	if err := s.SetIntroSkipFolder(libID, "Show Active", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIntroSkipFolder(libID, "Show Deactivated", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIntroSkipFolder(libID, "Show Deactivated", false); err != nil {
		t.Fatal(err)
	}

	candidates, err := s.NewIntroSkipCandidateFolders(libID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0] != "Show Never Touched" {
		t.Fatalf("expected exactly [Show Never Touched], got %v", candidates)
	}
}

// TestLibraryIntroSkipAutoNewToggle sichert das Persistieren des Bibliotheks-
// weiten Auto-Flags ab (Default aus, setzbar, lesbar).
func TestLibraryIntroSkipAutoNewToggle(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := s.LibraryIntroSkipAutoNew(libID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("expected default false")
	}
	if err := s.SetLibraryIntroSkipAutoNew(libID, true); err != nil {
		t.Fatal(err)
	}
	enabled, err = s.LibraryIntroSkipAutoNew(libID)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("expected true after enabling")
	}
	ids, err := s.ListLibraryIDsWithIntroSkipAutoNew()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != libID {
		t.Fatalf("expected [%d], got %v", libID, ids)
	}
}
