package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestSearchPeoplePrefix sichert die User-Vorgabe vom 2026-09-18 (Wortanfang-
// only) UND die User-Entscheidung vom 2026-09-20 (Schauspieler-Treffer sind
// eine eigene Sektion, siehe Store.SearchPeoplePrefix, NICHT mehr Teil der
// Item-Suche selbst — siehe TestFTSCastSearchRemovedFromItemSearch).
//
// Anlass der Wortanfang-Regel: eine Suche nach „big" lieferte an der echten
// Bibliothek 1518 Treffer, davon 506 ausschließlich über Namen wie Abigail
// Spencer, Mike Birbiglia, Michael Herbig, Jason Biggs, Mavie Hörbiger — für
// den Nutzer völlig zusammenhanglos („49 Treffer, die haben definitiv nicht
// big im Namen").
func TestSearchPeoplePrefix(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := s.CreateUser("christian", "pw123456", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserLibraryAccess(uid, []int64{lib}); err != nil {
		t.Fatal(err)
	}

	// movie legt einen Film samt Darstellerliste an.
	//
	// ⚠ Jeder Film braucht eine EIGENE TMDB-ID: `metadata` hat
	// UNIQUE(tmdb_type, tmdb_id, season, episode), gleiche IDs hätten also
	// denselben Metadaten-Datensatz geteilt (Titel überschrieben, Cast-Listen
	// gegenseitig ersetzt) — beim ersten Anlauf dieses Tests war GENAU das der
	// Grund für ein scheinbar falsches Suchergebnis.
	tmdbSeq := int64(0)
	movie := func(rel, title string, cast ...string) int64 {
		t.Helper()
		tmdbSeq++
		it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: title}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		metaID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 500 + tmdbSeq, Title: title})
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
		var entries []model.CastMember
		for _, name := range cast {
			// Eindeutige TMDB-ID je Person: mit einer festen ID je Film würde
			// derselbe Person-Datensatz wiederverwendet und der Test prüfte
			// dann versehentlich einen fremden Namen (beim ersten Anlauf genau
			// so passiert — „abigail" fand nichts, „big" dafür den falschen).
			personID, err := s.UpsertPerson(int64(9000+len(name)*7+len(rel)+int(tmdbSeq)), name, "")
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, model.CastMember{PersonID: personID, Name: name})
		}
		if len(entries) > 0 {
			if err := s.ReplaceMetadataCast(metaID, "cast", entries); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}

	movie("a.mkv", "Film A", "Jason Biggs")
	movie("b.mkv", "Film B", "Abigail Spencer")
	movie("c.mkv", "The Big Bang Theory Kompilation") // kein Cast — nur Titel-Treffer, für Personen-Suche irrelevant
	movie("d.mkv", "Film D", "Jean-Claude Big-Damme")

	search := func(term string) map[string]bool {
		t.Helper()
		people, err := s.SearchPeoplePrefix(term, ItemFilter{UserID: uid, IsAdmin: true, LibraryIDs: []int64{lib}})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, p := range people {
			got[p.Name] = true
		}
		return got
	}

	// 1) „big" trifft den Namen mit Wortanfang „Biggs" und das Wort nach dem
	//    Bindestrich in „Big-Damme", NICHT den Mittentreffer „Abigail".
	got := search("big")
	if !got["Jason Biggs"] {
		t.Errorf("Darsteller mit Wortanfang („Jason Biggs\") wurde nicht gefunden")
	}
	if got["Abigail Spencer"] {
		t.Errorf("Mittentreffer im Namen („Abigail Spencer\") wurde gefunden — genau der gemeldete Fehler")
	}
	if !got["Jean-Claude Big-Damme"] {
		t.Errorf("Wort nach Bindestrich („… Big-Damme\") wurde nicht gefunden")
	}

	// 2) Der Name selbst bleibt suchbar: „abigail" trifft ihn (Wortanfang).
	if !search("abigail")["Abigail Spencer"] {
		t.Errorf("„abigail\" findet den Darsteller nicht — die Darsteller-Suche darf nicht generell aus sein")
	}

	// 3) Ein Teil eines Wortes trifft NICHT: „ggs" (mitten in „Biggs").
	if search("ggs")["Jason Biggs"] {
		t.Errorf("„ggs\" fand „Jason Biggs\" — es wird weiterhin mitten im Namen gematcht")
	}

	// 4) Unter 3 Zeichen wird gar nicht gesucht (Kosten).
	if len(search("bi")) != 0 {
		t.Errorf("2-Zeichen-Suche fand einen Darsteller — Personen-Suche soll erst ab 3 Zeichen greifen")
	}
}
