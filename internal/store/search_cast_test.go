package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestSearchCastNamesOnlyAtWordStart sichert die User-Vorgabe vom 2026-09-18:
// Darsteller-Namen werden nur am WORTANFANG getroffen, nicht irgendwo im Namen.
//
// Anlass: eine Suche nach „big" lieferte an der echten Bibliothek 1518 Treffer,
// davon 506 ausschließlich über Namen wie Abigail Spencer, Mike Birbiglia,
// Michael Herbig, Jason Biggs, Mavie Hörbiger — für den Nutzer völlig
// zusammenhanglos („49 Treffer, die haben definitiv nicht big im Namen").
// Titel/Album/Künstler bleiben Teilstring-Suchen; das ist gewollt, sonst fände
// „big" nicht mehr „The Big Bang Theory".
func TestSearchCastNamesOnlyAtWordStart(t *testing.T) {
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

	// ⚠ Neutrale Titel für die Darsteller-Fälle: enthielte der Titel selbst den
	// Suchbegriff, träfe die Suche über den Titel und die Prüfung der
	// Darsteller-Regel wäre wertlos (genau das war beim zweiten Anlauf der Fall:
	// „ggs" fand den Film über „Ein Film mit Biggs" im TITEL, nicht über den
	// Darsteller).
	byWordStart := movie("a.mkv", "Film A", "Jason Biggs")
	byNameMiddle := movie("b.mkv", "Film B", "Abigail Spencer")
	byTitle := movie("c.mkv", "The Big Bang Theory Kompilation")
	byHyphenWord := movie("d.mkv", "Film D", "Jean-Claude Big-Damme")

	search := func(term string) map[int64]bool {
		t.Helper()
		items, err := s.ListItems(ItemFilter{Search: term, UserID: uid, IsAdmin: true, LibraryIDs: []int64{lib}})
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]bool{}
		for _, it := range items {
			got[it.ID] = true
		}
		return got
	}

	// 1) „big" trifft den Titel UND den Namen mit Wortanfang „Biggs",
	//    NICHT die Mittentreffer „Abigail"/„Big-Damme"(Bindestrich-Wort: doch).
	got := search("big")
	if !got[byTitle] {
		t.Errorf("Titeltreffer fehlt („The Big Bang Theory …\" muss über den Titel treffen)")
	}
	if !got[byWordStart] {
		t.Errorf("Darsteller mit Wortanfang („Jason Biggs\") wurde nicht gefunden")
	}
	if got[byNameMiddle] {
		t.Errorf("Mittentreffer im Namen („Abigail Spencer\") wurde gefunden — genau der gemeldete Fehler")
	}
	if !got[byHyphenWord] {
		t.Errorf("Wort nach Bindestrich („… Big-Damme\") wurde nicht gefunden")
	}

	// 2) Der Name selbst bleibt suchbar: „abigail" trifft ihn (Wortanfang).
	if !search("abigail")[byNameMiddle] {
		t.Errorf("„abigail\" findet den Darsteller nicht — die Darsteller-Suche darf nicht generell aus sein")
	}

	// 3) Ein Teil eines Wortes trifft NICHT: „ggs" (mitten in „Biggs").
	if search("ggs")[byWordStart] {
		t.Errorf("„ggs\" fand „Jason Biggs\" — es wird weiterhin mitten im Namen gematcht")
	}

	// 4) Unter 3 Zeichen wird gar nicht nach Darstellern gesucht (Kosten).
	//    „bi" findet dabei auch KEINEN Titeltreffer mehr — seit der FTS5-
	//    Migration (Titel/Artist/Album laufen über items_fts MATCH statt LIKE,
	//    siehe items.go) ist die Standardsuche wortbasiert: "bi" ist kein
	//    eigenständiges Wort in "The Big Bang Theory Kompilation", ein
	//    Teilstring-Treffer mitten im Wort "Big" (wie es die alte
	//    LIKE '%bi%'-Suche fand) ist damit bewusst nicht mehr Teil des
	//    Standardfalls — genau das leistet stattdessen der Fuzzy-Modus
	//    (SearchFuzzy, Präfix-Wildcard), siehe TestSearchFuzzyFindsPrefixMatches.
	short := search("bi")
	if short[byWordStart] || short[byNameMiddle] {
		t.Errorf("2-Zeichen-Suche fand einen Darsteller — Cast-Suche soll erst ab 3 Zeichen greifen")
	}
	if short[byTitle] {
		t.Errorf("2-Zeichen-Suche 'bi' fand einen Titeltreffer über einen Wort-Teilstring — FTS5 matcht seit der Migration ganze Wörter, kein Teilstring mehr")
	}
}
