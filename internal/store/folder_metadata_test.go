package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestPendingFoldersIgnoresExplicitlyUnmatchedFolder sichert die
// 2026-09-06-Regression ab: ein Ordner, dessen Zuordnung bewusst entfernt
// wurde (folder_metadata-Zeile mit metadata_id=NULL existiert), darf NICHT
// erneut als "pending" auftauchen — sonst matcht der periodische Enrichment-
// Worker ihn binnen Minuten automatisch wieder (User-Report: "Terra X" war
// nach dem manuellen Entfernen der Zuordnung kurz danach schon wieder,
// diesmal auf eine andere falsche Show, gematcht).
func TestPendingFoldersIgnoresExplicitlyUnmatchedFolder(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	// Ordner A: noch nie versucht (keine folder_metadata-Zeile) -> MUSS pending sein.
	mustUpsertMusicItem(t, s, libID, "Show A/S01E01.mkv", "", "", "")
	// Ordner B: bewusst unmatched (Zeile MIT metadata_id=NULL) -> darf NICHT
	// mehr als pending auftauchen.
	mustUpsertMusicItem(t, s, libID, "Show B/S01E01.mkv", "", "", "")
	if err := s.SetFolderMetadata(libID, "Show B", 0); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingFolders(200)
	if err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, p := range pending {
		if p.LibraryID == libID {
			folders = append(folders, p.Folder)
		}
	}
	if len(folders) != 1 || folders[0] != "Show A" {
		t.Fatalf("expected only 'Show A' pending, got %v", folders)
	}
}

// TestFolderMetadataRowExists prüft die Unterscheidung "keine Zeile" vs.
// "Zeile mit NULL" direkt.
func TestFolderMetadataRowExists(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	exists, err := s.FolderMetadataRowExists(libID, "Nie Angefasst")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected no row for a folder that was never touched")
	}

	if err := s.SetFolderMetadata(libID, "Bewusst Entfernt", 0); err != nil {
		t.Fatal(err)
	}
	exists, err = s.FolderMetadataRowExists(libID, "Bewusst Entfernt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected a row to exist even though metadata_id is NULL")
	}
}
