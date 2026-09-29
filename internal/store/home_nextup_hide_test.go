package store

import (
	"testing"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// TestHomeNextUpHideAndMaxAge sichert die beiden Startseiten-Wünsche vom
// 2026-09-28 ab: Serie per ✕ aus "Als nächstes" entfernen (nur für diesen
// User, kommt beim Weiterschauen zurück) und die Verweildauer (since) für
// "Als nächstes" und "Fortsetzen". Läuft bewusst gegen echtes SQLite, weil
// die Zeitvergleiche (hidden_at vs. MAX(watched_at), since-Parameter) nur
// dort zeigen, ob die gespeicherten Zeitformate zueinander passen.
func TestHomeNextUpHideAndMaxAge(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	showID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 2000, Title: "Testserie", PosterPath: "/show.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	addEpisode := func(rel string, episode int) int64 {
		t.Helper()
		it := &model.Item{LibraryID: libID, Path: t.TempDir() + "/" + rel, RelPath: "Testserie/" + rel, Title: rel}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		metaID, err := s.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(200000 + episode),
			ParentID: showID, Title: rel, Season: 1, Episode: episode,
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e1 := addEpisode("S01E01.mkv", 1)
	e2 := addEpisode("S01E02.mkv", 2)
	e3 := addEpisode("S01E03.mkv", 3)

	alice, err := s.CreateUser("alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.CreateUser("bob", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{alice, bob} {
		if err := s.SetWatchedFor(u, e1, true); err != nil {
			t.Fatal(err)
		}
	}

	nextUp := func(user int64, since time.Time) []model.Item {
		t.Helper()
		items, err := s.HomeNextUpForLibrary(user, libID, 12, since)
		if err != nil {
			t.Fatal(err)
		}
		return items
	}

	// Ausgangslage: E02 steht in "Als nächstes", inkl. Serienposter.
	got := nextUp(alice, time.Time{})
	if len(got) != 1 || got[0].ID != e2 {
		t.Fatalf("erwartet E02 (%d), bekam %+v", e2, got)
	}
	if got[0].Metadata == nil || got[0].Metadata.ShowPosterPath != "/show.jpg" {
		t.Errorf("ShowPosterPath fehlt: %+v", got[0].Metadata)
	}

	// Verweildauer: E01 vor 40 Tagen gesehen → bei 30 Tagen weg, bei 60 da.
	old := time.Now().AddDate(0, 0, -40)
	if _, err := s.db.Exec(`UPDATE user_item_state SET watched_at = ? WHERE user_id = ? AND item_id = ?`, old, alice, e1); err != nil {
		t.Fatal(err)
	}
	if got := nextUp(alice, time.Now().AddDate(0, 0, -30)); len(got) != 0 {
		t.Errorf("30 Tage: erwartet leer, bekam %d", len(got))
	}
	if got := nextUp(alice, time.Now().AddDate(0, 0, -60)); len(got) != 1 {
		t.Errorf("60 Tage: erwartet 1, bekam %d", len(got))
	}

	// Ausblenden: nur für Alice, Bob sieht die Serie weiter.
	if ok, err := s.HideNextUpShowForItem(alice, e2); err != nil || !ok {
		t.Fatalf("Hide: ok=%v err=%v", ok, err)
	}
	if got := nextUp(alice, time.Time{}); len(got) != 0 {
		t.Errorf("nach Hide: erwartet leer, bekam %d", len(got))
	}
	if got := nextUp(bob, time.Time{}); len(got) != 1 {
		t.Errorf("Bob darf nicht betroffen sein, bekam %d", len(got))
	}

	// Weiterschauen holt die Serie zurück (nächste Folge ist dann E03).
	if _, err := s.db.Exec(`UPDATE user_nextup_hidden SET hidden_at = ? WHERE user_id = ?`, time.Now().Add(-time.Minute), alice); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWatchedFor(alice, e2, true); err != nil {
		t.Fatal(err)
	}
	if got := nextUp(alice, time.Time{}); len(got) != 1 || got[0].ID != e3 {
		t.Errorf("nach Weiterschauen erwartet E03 (%d), bekam %+v", e3, got)
	}

	// Kein Episoden-Item → false, kein Fehler.
	movieItem := &model.Item{LibraryID: libID, Path: t.TempDir() + "/film.mkv", RelPath: "film.mkv", Title: "film"}
	if err := s.UpsertItem(movieItem); err != nil {
		t.Fatal(err)
	}
	movieID, _ := s.ItemIDByPath(movieItem.Path)
	if ok, err := s.HideNextUpShowForItem(alice, movieID); err != nil || ok {
		t.Errorf("Nicht-Folge: ok=%v err=%v", ok, err)
	}

	// Fortsetzen + Verweildauer.
	if err := s.SetResumePosition(bob, e3, 120); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchLastPlayed(bob, e3); err != nil {
		t.Fatal(err)
	}
	cont := func(since time.Time) int {
		t.Helper()
		items, err := s.HomeContinueForLibrary(bob, libID, 12, since)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	if n := cont(time.Time{}); n != 1 {
		t.Errorf("Fortsetzen unbegrenzt: erwartet 1, bekam %d", n)
	}
	if n := cont(time.Now().AddDate(0, 0, -7)); n != 1 {
		t.Errorf("Fortsetzen 7 Tage: erwartet 1, bekam %d", n)
	}
	if _, err := s.db.Exec(`UPDATE user_item_state SET last_played_at = ? WHERE user_id = ? AND item_id = ?`, time.Now().AddDate(0, 0, -10), bob, e3); err != nil {
		t.Fatal(err)
	}
	if n := cont(time.Now().AddDate(0, 0, -7)); n != 0 {
		t.Errorf("Fortsetzen 7 Tage, vor 10 Tagen gespielt: erwartet 0, bekam %d", n)
	}

	// Linux-Fall (2026-09-29): Client speichert nur die Position, ruft aber
	// nie /played auf — muss trotzdem frisch in "Fortsetzen" stehen.
	if err := s.SetResumePosition(bob, e3, 300); err != nil {
		t.Fatal(err)
	}
	if n := cont(time.Now().AddDate(0, 0, -7)); n != 1 {
		t.Errorf("Fortsetzen nach reinem SetResumePosition: erwartet 1, bekam %d", n)
	}
}
