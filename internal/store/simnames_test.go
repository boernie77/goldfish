package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

func TestNormalizeSimName(t *testing.T) {
	cases := map[string]string{
		"1-massage_aj_mg18.mp4":     "1 massage aj mg18",
		"1-massage_aj_mg18 (2).mp4": "1 massage aj mg18",
		"1-massage_alettamgnew.wmv": "1 massage alettamgnew",
		"Film.Title.2020.1080p.mkv": "film title 2020 1080p",
		"already normal":            "already normal",
	}
	for in, want := range cases {
		if got := normalizeSimName(in); got != want {
			t.Errorf("normalizeSimName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameSimilarity(t *testing.T) {
	if s := nameSimilarity("abc", "abc"); s != 1 {
		t.Errorf("identical => %v, want 1", s)
	}
	if s := nameSimilarity("massage aj mg18", "massage aj mg18"); s < 0.999 {
		t.Errorf("identical normalized => %v", s)
	}
	// ein Zeichen anders bei ~15 Länge -> ~0.93, über 0.9
	if s := nameSimilarity("massage jynx mg18", "massage jinx mg18"); s < 0.9 {
		t.Errorf("one-char diff => %v, want >= 0.9", s)
	}
	// klar verschieden
	if s := nameSimilarity("massage aj mg18", "massage victoria rae mg18"); s >= 0.9 {
		t.Errorf("different names => %v, want < 0.9", s)
	}
}

func TestSimilarNameDupes(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("a", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	adminID, _ := s.CreateUser("admin", "pw123456", true)

	add := func(rel string, w, h int, dur float64) {
		t.Helper()
		it := &model.Item{
			LibraryID:   lib,
			Path:        "/media/a/" + rel,
			RelPath:     rel,
			Title:       rel,
			Container:   "mp4",
			Width:       w,
			Height:      h,
			DurationSec: dur,
			SizeBytes:   1000,
		}
		if err := s.UpsertItem(it); err != nil {
			t.Fatalf("UpsertItem(%s): %v", rel, err)
		}
	}

	// Paar 1: name + "(2)", gleiche Auflösung, Länge ±1s
	add("Siterips/Massage/1-massage_aj_mg18.mp4", 1280, 720, 1000)
	add("Siterips/Massage/1-massage_aj_mg18 (2).mp4", 1280, 720, 1000.5)
	// Paar 2: .mp4 vs .wmv, identisch
	add("Siterips/Massage/1-massage_alettamgnew.mp4", 1000, 564, 1283)
	add("Siterips/Massage/1-massage_alettamgnew.wmv", 1000, 564, 1283)
	// Einzelgänger
	add("Siterips/Massage/1-massage_solo.mp4", 1280, 720, 800)
	// gleicher Name wie Paar-1 aber ANDERE Auflösung -> kein Treffer
	add("Siterips/Massage/1-massage_aj_mg18_lowres.mp4", 640, 360, 1000)
	// gleicher Name wie Paar-1 aber Länge weit weg -> kein Treffer
	add("Siterips/Massage/1-massage_aj_mg18_long.mp4", 1280, 720, 3000)
	// Paar in einem ANDEREN Ordner (Scoping-Test)
	add("Other/clip.mp4", 1920, 1080, 500)
	add("Other/clip.wmv", 1920, 1080, 500)

	// ganze Library
	all, err := s.SimilarNameDupes(lib, adminID, true, "", 0.9)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, it := range all {
		got[it.RelPath] = it.DupeOtherPaths
	}
	// erwartet: die 4 Paar-Dateien + die 2 Other-Dateien
	wantPresent := []string{
		"Siterips/Massage/1-massage_aj_mg18.mp4",
		"Siterips/Massage/1-massage_aj_mg18 (2).mp4",
		"Siterips/Massage/1-massage_alettamgnew.mp4",
		"Siterips/Massage/1-massage_alettamgnew.wmv",
		"Other/clip.mp4",
		"Other/clip.wmv",
	}
	for _, w := range wantPresent {
		if _, ok := got[w]; !ok {
			t.Errorf("erwartet Treffer fehlt: %s", w)
		}
	}
	wantAbsent := []string{
		"Siterips/Massage/1-massage_solo.mp4",
		"Siterips/Massage/1-massage_aj_mg18_lowres.mp4",
		"Siterips/Massage/1-massage_aj_mg18_long.mp4",
	}
	for _, w := range wantAbsent {
		if _, ok := got[w]; ok {
			t.Errorf("unerwarteter Treffer: %s", w)
		}
	}
	if len(all) != 6 {
		t.Errorf("Library-weit: %d Treffer, want 6", len(all))
	}

	// auf Massage-Ordner beschränkt -> Other/* fällt weg
	scoped, err := s.SimilarNameDupes(lib, adminID, true, "Siterips/Massage", 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 4 {
		t.Errorf("Ordner-Scope: %d Treffer, want 4", len(scoped))
	}
	for _, it := range scoped {
		if it.RelPath == "Other/clip.mp4" || it.RelPath == "Other/clip.wmv" {
			t.Errorf("Ordner-Scope hat Other/-Datei durchgelassen: %s", it.RelPath)
		}
	}
}
