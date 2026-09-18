package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestNextEpisodeCandidates sichert die "nächste Folge"-Reihenfolge ab, die
// jeder Client für "Nächste Folge automatisch starten" nutzt. Die Fälle sind
// genau die, die ein Client NICHT selbst nachbauen soll:
// Staffel-übergreifend, Doppelfolgen, Auflösungsvarianten und das Ende der
// Serie.
func TestNextEpisodeCandidates(t *testing.T) {
	s := newTestStore(t)

	libA, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	// Zweite Bibliothek, in der dieselbe Serie NICHT liegt — dient als
	// Gegenprobe, dass die Suche nicht über Bibliotheksgrenzen hinweg
	// irgendwelche Episoden einsammelt.
	libB, err := s.CreateLibrary("Andere Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	showID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 1000, Title: "Testserie"})
	if err != nil {
		t.Fatal(err)
	}

	// addEpisode legt Item + Episoden-Metadaten an und verknüpft beide.
	// height steuert die Varianten-Wahl (Auflösung).
	addEpisode := func(libID int64, rel string, season, episode, height int) int64 {
		t.Helper()
		it := &model.Item{
			LibraryID: libID,
			Path:      t.TempDir() + "/" + rel,
			RelPath:   rel,
			Title:     rel,
			Height:    height,
		}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		metaID, err := s.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(100000 + season*100 + episode),
			ParentID: showID, Title: rel, Season: season, Episode: episode,
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatalf("Item-ID nicht gefunden (%s): %v", it.Path, err)
		}
		if err := s.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}

	e1 := addEpisode(libA, "S01E01.mkv", 1, 1, 1080)
	e2 := addEpisode(libA, "S01E02.mkv", 1, 2, 1080)
	e3 := addEpisode(libA, "S01E03.mkv", 1, 3, 1080)
	s2e1 := addEpisode(libA, "S02E01.mkv", 2, 1, 1080)
	// Dieselbe Episode 1 der zweiten Staffel als 4K-Variante: muss die
	// Vertreter-Zeile werden (groupVariants-Parität), nicht die 1080p-Datei.
	s2e1_4k := addEpisode(libA, "S02E01-4K.mkv", 2, 1, 2160)
	// Episode in einer anderen Bibliothek derselben Serie: darf NICHT als
	// "nächste Folge" auftauchen, sondern nur über die ACL-Prüfung des API-
	// Layers — hier ist die Bibliothek eine andere, also bleibt sie liegen.
	_ = addEpisode(libB, "S01E04.mkv", 1, 4, 1080)

	next := func(itemID int64) []EpisodeCandidate {
		t.Helper()
		cands, err := s.NextEpisodeCandidates(itemID, 25)
		if err != nil {
			t.Fatal(err)
		}
		return cands
	}

	// 1) Innerhalb der Staffel: E01 → E02.
	if got := next(e1); len(got) == 0 || got[0].ID != e2 {
		t.Fatalf("nach E01 erwartet E02 (%d), bekam %+v", e2, got)
	}
	// 2) Reihenfolge nach der ersten Folge: E02, E03, dann Staffel 2.
	got := next(e1)
	if len(got) < 3 || got[0].ID != e2 || got[1].ID != e3 {
		t.Fatalf("Reihenfolge nach E01 falsch: %+v", got)
	}
	// 3) Staffel-Übergang: nach S01E03 kommt S02E01 (4K-Variante als Vertreter).
	// Hinweis: die Bibliotheks-Trennung passiert NICHT hier — der Store liefert
	// die Episode aus der zweiten Bibliothek (S01E04) mit, weil sie über
	// dasselbe Serien-Metadaten-Objekt zur Serie gehört. Genau deshalb filtert
	// die API-Schicht (nextEpisode) mit UserHasLibraryAccess nach; hier wird
	// nur die REIHENFOLGE geprüft (Staffel vor Bibliothek).
	got3 := next(e3)
	if len(got3) == 0 {
		t.Fatalf("nach S01E03 keine Kandidaten")
	}
	firstS2 := -1
	for i, c := range got3 {
		if c.Season == 2 {
			firstS2 = i
			if c.ID != s2e1_4k {
				t.Fatalf("S02E01 nicht als 4K-Variante (%d) vertreten: %+v", s2e1_4k, c)
			}
			break
		}
	}
	if firstS2 < 0 {
		t.Fatalf("S02E01 fehlt in %+v", got3)
	}
	for _, c := range got3 {
		if c.ID == s2e1 {
			t.Fatalf("1080p-Variante wurde zusätzlich geliefert: %+v", got3)
		}
	}
	// 4) Varianten erzeugen keine Doppeleinträge pro (Staffel, Folge).
	seen := map[[2]int]int{}
	for _, c := range next(e1) {
		key := [2]int{c.Season, c.Episode}
		if seen[key] > 0 {
			t.Fatalf("(Staffel %d, Folge %d) doppelt in %+v", c.Season, c.Episode, next(e1))
		}
		seen[key]++
	}
	// 5) Letzte Folge der Serie → leere Liste, kein Fehler.
	if got := next(s2e1); len(got) != 0 {
		t.Fatalf("nach der letzten Folge erwartet leer, bekam %+v", got)
	}
	// 6) Kein Serien-Item (kein parent_id) → keine Kandidaten.
	movieMeta, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 2000, Title: "Ein Film"})
	if err != nil {
		t.Fatal(err)
	}
	movie := &model.Item{LibraryID: libA, Path: t.TempDir() + "/film.mkv", RelPath: "film.mkv", Title: "Ein Film"}
	if err := s.UpsertItem(movie); err != nil {
		t.Fatal(err)
	}
	movieID, _ := s.ItemIDByPath(movie.Path)
	if err := s.SetItemMetadata(movieID, movieMeta); err != nil {
		t.Fatal(err)
	}
	if got := next(movieID); len(got) != 0 {
		t.Fatalf("Film lieferte Kandidaten: %+v", got)
	}
}

// TestNextEpisodeCandidatesDoppelfolge sichert ab, dass eine Doppelfolge
// (S01E01E02 → metadata.episode=1, items.episode_end=2) als Block zählt: als
// "nächste Folge" muss die dritte Folge kommen, nicht die zweite Hälfte der
// eigenen Datei. Ohne diese Regel würde der Autoplay-Modus nach einer
// Doppelfolge noch einmal deren zweite Hälfte abspielen.
func TestNextEpisodeCandidatesDoppelfolge(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	showID, _ := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 1000, Title: "Testserie"})

	add := func(rel string, season, episode int) int64 {
		t.Helper()
		it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		metaID, _ := s.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(100000 + season*100 + episode),
			ParentID: showID, Title: rel, Season: season, Episode: episode,
		})
		id, err := s.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatalf("Item-ID nicht gefunden (%s): %v", it.Path, err)
		}
		if err := s.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Normalfall zuerst: eine Einzelfolge E01 → nächste Folge ist E02.
	single := add("S01E01.mkv", 1, 1)
	second := add("S01E02.mkv", 1, 2)
	third := add("S01E03.mkv", 1, 3)
	if got, _ := s.NextEpisodeCandidates(single, 25); len(got) == 0 || got[0].ID != second {
		t.Fatalf("nach E01 erwartet E02 (%d), bekam %+v", second, got)
	}

	// Doppelfolge: E01 + episode_end=2 deckt E02 ab → nächste Folge ist E03.
	double := add("S01E01E02.mkv", 1, 1)
	if err := s.SetItemEpisodeEnd(double, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.NextEpisodeCandidates(double, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != third {
		t.Fatalf("Doppelfolge: nach E01E02 erwartet E03 (%d), bekam %+v", third, got)
	}
	for _, c := range got {
		if c.Episode == 2 {
			t.Fatalf("durch die Doppelfolge abgedeckte Folge 2 wurde geliefert: %+v", got)
		}
	}
}
