package store

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestSeriesOwnedEpisodesAcrossMergedFolders sichert das "Two and a Half
// Men"-Szenario ab (User-Report 2026-09-10): dieselbe Show liegt in zwei
// GETRENNTEN physischen Top-Level-Ordnern (z.B. "Show S01" + "Show S02"),
// beide per folder_metadata auf dieselbe Show gematcht. MergedFolderNames
// muss den jeweils anderen Ordner als Geschwister liefern, und
// SeriesOwnedEpisodes muss — mit beiden Ordnern übergeben — die Episoden
// AUS BEIDEN Ordnern zusammen liefern, ohne dass eine Datei verschoben wird.
func TestSeriesOwnedEpisodesAcrossMergedFolders(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("TV", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	show, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 999, Title: "Show"})
	if err != nil {
		t.Fatal(err)
	}
	e1, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 1, ParentID: show, Season: 1, Episode: 1, Title: "S01E01"})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 2, ParentID: show, Season: 2, Episode: 1, Title: "S02E01"})
	if err != nil {
		t.Fatal(err)
	}

	folderS01 := "Show.S01.WEB"
	folderS02 := "Show.S02.WEB"
	if err := s.SetFolderMetadata(libID, folderS01, show); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFolderMetadata(libID, folderS02, show); err != nil {
		t.Fatal(err)
	}

	mustUpsertAndMatch := func(relPath string, metaID int64) {
		t.Helper()
		it := &model.Item{LibraryID: libID, Path: filepath.Join("/media", relPath), RelPath: relPath}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		id, err := s.ItemIDByPath(it.Path)
		if err != nil || id == 0 {
			t.Fatalf("ItemIDByPath(%q): id=%d err=%v", it.Path, id, err)
		}
		if err := s.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
	}
	mustUpsertAndMatch(folderS01+"/s01e01.mkv", e1)
	mustUpsertAndMatch(folderS02+"/s02e01.mkv", e2)

	// MergedFolderNames muss symmetrisch den jeweils ANDEREN Ordner liefern,
	// egal von welcher Seite man startet.
	sibsFromS01, err := s.MergedFolderNames(libID, folderS01)
	if err != nil {
		t.Fatal(err)
	}
	if len(sibsFromS01) != 1 || sibsFromS01[0] != folderS02 {
		t.Errorf("MergedFolderNames(%q) = %v, expected [%q]", folderS01, sibsFromS01, folderS02)
	}
	sibsFromS02, err := s.MergedFolderNames(libID, folderS02)
	if err != nil {
		t.Fatal(err)
	}
	if len(sibsFromS02) != 1 || sibsFromS02[0] != folderS01 {
		t.Errorf("MergedFolderNames(%q) = %v, expected [%q]", folderS02, sibsFromS02, folderS01)
	}

	// Single-Folder-Aufruf bleibt unverändert auf genau diesen einen Ordner
	// beschränkt (Rückwärtskompatibilität für den Normalfall ohne Merge).
	single, _, err := s.SeriesOwnedEpisodes(libID, []string{folderS01})
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 || single[0].Season != 1 {
		t.Errorf("single-folder SeriesOwnedEpisodes(%q) = %+v, expected genau S01E01", folderS01, single)
	}

	// Multi-Folder-Aufruf (folder + Geschwister) liefert Episoden aus BEIDEN
	// physischen Ordnern zusammen.
	combined, showTMDB, err := s.SeriesOwnedEpisodes(libID, append([]string{folderS01}, sibsFromS01...))
	if err != nil {
		t.Fatal(err)
	}
	if len(combined) != 2 {
		t.Fatalf("combined SeriesOwnedEpisodes = %+v, expected 2 Episoden aus beiden Ordnern", combined)
	}
	var seasons []int
	for _, e := range combined {
		seasons = append(seasons, e.Season)
	}
	sort.Ints(seasons)
	if seasons[0] != 1 || seasons[1] != 2 {
		t.Errorf("expected seasons [1 2], got %v", seasons)
	}
	if showTMDB != 999 {
		t.Errorf("expected showTMDB=999, got %d", showTMDB)
	}
}

func TestMergedFolderNamesEmptyWhenNoSibling(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("TV", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	show, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 1, Title: "Solo Show"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetFolderMetadata(libID, "Solo Show", show); err != nil {
		t.Fatal(err)
	}
	sibs, err := s.MergedFolderNames(libID, "Solo Show")
	if err != nil {
		t.Fatal(err)
	}
	if len(sibs) != 0 {
		t.Errorf("expected no siblings for a show in a single folder, got %v", sibs)
	}
}
