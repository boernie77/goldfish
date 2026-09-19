package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// items_search_test.go -- FTS5-Volltextsuche (items_fts, siehe schema.go +
// fts.go). Ergänzt search_cast_test.go (Cast-Wortanfang-Regel, unverändert)
// und unaccent_test.go (Diakritika + Serientitel-Vererbung, unverändert) um
// die neuen FTS5-spezifischen Fälle: Wort- vs. Präfix-Matching, Fuzzy-Modus,
// Trigger-Konsistenz und Injection-Robustheit.

// upsertMovie legt einen Film mit eindeutiger TMDB-ID an und liefert die
// Item-ID zurück (UpsertItem schreibt die ID nicht in den übergebenen
// Pointer zurück — per Pfad nachladen, siehe CLAUDE.md "Beim Schreiben von
// Store-Tests").
func upsertMovieForSearch(t *testing.T, s *Store, libID int64, tmdbID int64, rel, title string) int64 {
	t.Helper()
	it := &model.Item{LibraryID: libID, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: title}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	id, err := s.ItemIDByPath(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	metaID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: tmdbID, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadata(id, metaID); err != nil {
		t.Fatal(err)
	}
	return id
}

func searchIDs(t *testing.T, s *Store, libID int64, term string, fuzzy bool) map[int64]bool {
	t.Helper()
	items, err := s.ListItems(ItemFilter{LibraryID: libID, Search: term, SearchFuzzy: fuzzy})
	if err != nil {
		t.Fatalf("ListItems(search=%q, fuzzy=%v): %v", term, fuzzy, err)
	}
	got := map[int64]bool{}
	for _, it := range items {
		got[it.ID] = true
	}
	return got
}

// TestFTSExactWordMatch: Standardsuche findet ganze Wörter (Titel), nicht
// beliebige Teilstrings mitten im Wort — das ist die aus der FTS5-Migration
// resultierende Kernänderung gegenüber der alten LIKE-Suche.
func TestFTSExactWordMatch(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	starWars := upsertMovieForSearch(t, s, lib, 1001, "sw.mkv", "Star Wars")
	other := upsertMovieForSearch(t, s, lib, 1002, "other.mkv", "Ein ganz anderer Film")

	// Beide Wörter einzeln finden den Film.
	if got := searchIDs(t, s, lib, "star", false); !got[starWars] {
		t.Errorf("'star' haette 'Star Wars' finden muessen")
	}
	if got := searchIDs(t, s, lib, "wars", false); !got[starWars] {
		t.Errorf("'wars' haette 'Star Wars' finden muessen")
	}
	// Mehrere Wörter sind implizit UND-verknüpft (Reihenfolge egal).
	if got := searchIDs(t, s, lib, "wars star", false); !got[starWars] {
		t.Errorf("'wars star' (vertauschte Reihenfolge) haette 'Star Wars' finden muessen")
	}
	// Ein Teilstring MITTEN in einem Wort trifft im Standardfall NICHT mehr
	// (anders als die alte LIKE '%tar%'-Suche).
	if got := searchIDs(t, s, lib, "tar", false); got[starWars] {
		t.Errorf("'tar' (Teilstring mitten in 'Star') haette im exakten Modus NICHT treffen duerfen")
	}
	if got := searchIDs(t, s, lib, "star", false); got[other] {
		t.Errorf("'star' haette den unbeteiligten Film nicht finden duerfen")
	}
}

// TestFTSFuzzyFindsPrefixMatches: der Fuzzy-Modus (SearchFuzzy=true) findet
// zusätzlich Präfix-Treffer, die der exakte Modus nicht findet — und liefert
// dabei immer eine Obermenge (keine Duplikate, keine verlorenen Treffer).
func TestFTSFuzzyFindsPrefixMatches(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	exact := upsertMovieForSearch(t, s, lib, 2001, "a.mkv", "Star Wars")
	prefixOnly := upsertMovieForSearch(t, s, lib, 2002, "b.mkv", "Starship Troopers")
	unrelated := upsertMovieForSearch(t, s, lib, 2003, "c.mkv", "Der Pate")

	exactGot := searchIDs(t, s, lib, "star", false)
	if !exactGot[exact] {
		t.Errorf("exakter Modus haette 'Star Wars' finden muessen")
	}
	if exactGot[prefixOnly] {
		t.Errorf("exakter Modus haette 'Starship Troopers' NICHT finden duerfen (kein exaktes Wort 'star')")
	}

	fuzzyGot := searchIDs(t, s, lib, "star", true)
	if !fuzzyGot[exact] {
		t.Errorf("Fuzzy-Modus haette 'Star Wars' weiterhin finden muessen (Praefix-Match deckt exakte Treffer mit ab)")
	}
	if !fuzzyGot[prefixOnly] {
		t.Errorf("Fuzzy-Modus haette 'Starship Troopers' ueber den Praefix-Treffer finden muessen")
	}
	if fuzzyGot[unrelated] {
		t.Errorf("Fuzzy-Modus haette den unbeteiligten Film nicht finden duerfen")
	}

	// Keine Duplikate: jede exakt gefundene ID muss auch im Fuzzy-Ergebnis
	// stecken (Obermengen-Eigenschaft, auf der die fuzzyExtraCount-Berechnung
	// in der API-Schicht beruht).
	for id := range exactGot {
		if !fuzzyGot[id] {
			t.Errorf("Item %d war im exakten Ergebnis, fehlt aber im Fuzzy-Ergebnis", id)
		}
	}
}

// TestFTSSeriesTitleInheritance: die Suche muss den SERIENTITEL treffen
// (COALESCE(parent.title, m.title, i.title), siehe items.go) — dieselbe
// Vererbung wie in unaccent_test.go's TestSearchMatchesShowTitleNotEpisodeTitle,
// hier zusätzlich mit dem Fuzzy-Modus geprüft (Präfix auf den Serientitel).
func TestFTSSeriesTitleInheritance(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	showID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 10, Title: "Breaking Bad"})
	if err != nil {
		t.Fatal(err)
	}
	epID, err := s.UpsertMetadata(&model.Metadata{
		TMDBType: "episode", TMDBID: 11, ParentID: showID,
		Title: "Pilot", Season: 1, Episode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/bb-s01e01.mkv", RelPath: "Breaking Bad/bb-s01e01.mkv", Title: "bb.s01e01.mkv"}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	id, err := s.ItemIDByPath(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadata(id, epID); err != nil {
		t.Fatal(err)
	}

	if got := searchIDs(t, s, lib, "breaking bad", false); !got[id] {
		t.Errorf("Suche nach dem Serientitel haette die Episode finden muessen")
	}
	if got := searchIDs(t, s, lib, "pilot", false); got[id] {
		t.Errorf("Suche nach dem Episodentitel haette NICHTS finden duerfen (Serientitel schlaegt Episodentitel)")
	}
	// Fuzzy-Präfix auf den Serientitel funktioniert genauso.
	if got := searchIDs(t, s, lib, "break", true); !got[id] {
		t.Errorf("Fuzzy-Praefix 'break' haette ueber den Serientitel treffen muessen")
	}
}

// TestFTSTriggersKeepIndexConsistent: Insert/Update/Delete auf items müssen
// items_fts synchron halten (die drei Sync-Trigger aus schema.go).
func TestFTSTriggersKeepIndexConsistent(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}

	// INSERT: neues Item ist sofort suchbar.
	it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/orig.mkv", RelPath: "orig.mkv", Title: "Ursprungstitel"}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	id, err := s.ItemIDByPath(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, s, lib, "ursprungstitel", false); !got[id] {
		t.Fatalf("INSERT-Trigger: frisch angelegtes Item nicht ueber items_fts auffindbar")
	}

	// UPDATE (UpsertItem mit gleichem Pfad → ON CONFLICT DO UPDATE): alter
	// Titel verschwindet, neuer taucht auf.
	it2 := &model.Item{LibraryID: lib, Path: it.Path, RelPath: "orig.mkv", Title: "Neuertitel"}
	if err := s.UpsertItem(it2); err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, s, lib, "ursprungstitel", false); got[id] {
		t.Errorf("UPDATE-Trigger: alter Titel ist nach Umbenennung weiterhin ueber items_fts auffindbar")
	}
	if got := searchIDs(t, s, lib, "neuertitel", false); !got[id] {
		t.Errorf("UPDATE-Trigger: neuer Titel ist nach Umbenennung NICHT ueber items_fts auffindbar")
	}

	// DELETE: Item verschwindet komplett aus der Suche.
	if err := s.DeleteItem(id); err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, s, lib, "neuertitel", false); got[id] {
		t.Errorf("DELETE-Trigger: gelöschtes Item ist weiterhin ueber items_fts auffindbar")
	}
	var ftsCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM items_fts WHERE item_id = ?`, id).Scan(&ftsCount); err != nil {
		t.Fatal(err)
	}
	if ftsCount != 0 {
		t.Errorf("items_fts haelt nach DELETE noch %d Zeile(n) fuer item_id=%d", ftsCount, id)
	}
}

// TestFTSMetadataTitleRenamePropagatesToEpisodes: eine Serientitel-Änderung
// (metadata.title einer Show) muss ALLE Episoden-Items dieser Show in
// items_fts nachziehen — nicht nur beim nächsten Scan.
func TestFTSMetadataTitleRenamePropagatesToEpisodes(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	showID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 20, Title: "Alter Name"})
	if err != nil {
		t.Fatal(err)
	}
	var epIDs []int64
	var epParent = showID
	for i := 1; i <= 3; i++ {
		epMetaID, err := s.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(21 + i), ParentID: epParent,
			Title: "Folge", Season: 1, Episode: i,
		})
		if err != nil {
			t.Fatal(err)
		}
		it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/ep.mkv", RelPath: "s/ep.mkv", Title: "ep.mkv"}
		if err := s.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		id, err := s.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetItemMetadata(id, epMetaID); err != nil {
			t.Fatal(err)
		}
		epIDs = append(epIDs, id)
	}

	// Vor der Umbenennung: alter Name findet alle drei Episoden.
	before := searchIDs(t, s, lib, "alter name", false)
	for _, id := range epIDs {
		if !before[id] {
			t.Fatalf("Testaufbau: Episode %d nicht ueber alten Serientitel auffindbar", id)
		}
	}

	// Serientitel umbenennen — direktes UPDATE auf metadata (wie es ein
	// künftiger "Serie neu zuordnen"-Pfad täte), feuert
	// items_fts_metadata_title_au.
	if _, err := s.db.Exec(`UPDATE metadata SET title = ? WHERE id = ?`, "Neuer Name", showID); err != nil {
		t.Fatal(err)
	}

	after := searchIDs(t, s, lib, "alter name", false)
	for _, id := range epIDs {
		if after[id] {
			t.Errorf("Episode %d ist nach Serientitel-Umbenennung weiterhin ueber den ALTEN Namen auffindbar", id)
		}
	}
	afterNew := searchIDs(t, s, lib, "neuer name", false)
	for _, id := range epIDs {
		if !afterNew[id] {
			t.Errorf("Episode %d ist nach Serientitel-Umbenennung NICHT ueber den neuen Namen auffindbar", id)
		}
	}
}

// TestFTSSearchInjectionSafety: FTS5-Sonderzeichen/-Operatoren im
// Sucheingabe-String dürfen weder die Query zum Absturz bringen noch als
// FTS5-Operator interpretiert werden (ftsQuery() quotet jedes Wort einzeln).
func TestFTSSearchInjectionSafety(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	upsertMovieForSearch(t, s, lib, 3001, "a.mkv", `Titel mit "Anführung" und OR NOT -Bindestrich`)
	upsertMovieForSearch(t, s, lib, 3002, "b.mkv", "Ein normaler Film")

	dangerous := []string{
		`"OR 1=1`,
		`foo*`,
		`-`,
		`AND OR NOT`,
		`"`,
		`""`,
		`(foo OR bar)`,
		`foo:bar`,
		`title:"foo"`,
		"   ", // nur Leerzeichen
	}
	for _, term := range dangerous {
		for _, fuzzy := range []bool{false, true} {
			if _, err := s.ListItems(ItemFilter{LibraryID: lib, Search: term, SearchFuzzy: fuzzy}); err != nil {
				t.Errorf("ListItems(search=%q, fuzzy=%v) lieferte einen Fehler statt (ggf. leerer) Ergebnisse: %v", term, fuzzy, err)
			}
		}
	}

	// Ein Suchbegriff, der absichtlich FTS5-Operator-Syntax UND ein
	// eingebettetes Anführungszeichen enthält, muss trotzdem als reiner Text
	// behandelt werden (kein Crash, keine versehentliche Operator-Wirkung).
	if _, err := s.ListItems(ItemFilter{LibraryID: lib, Search: `"Anführung" OR bar`}); err != nil {
		t.Errorf("ListItems mit eingebettetem Anführungszeichen + OR-Operator lieferte einen Fehler: %v", err)
	}
}

// TestFTSCastSearchUnaffectedByFuzzy: die Cast-Namen-Suche (Wortanfang-only)
// nimmt NICHT an der FTS5-Migration teil und muss mit SearchFuzzy=true
// identisch zu SearchFuzzy=false funktionieren.
func TestFTSCastSearchUnaffectedByFuzzy(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/a.mkv", RelPath: "a.mkv", Title: "Neutraler Titel"}
	if err := s.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	id, err := s.ItemIDByPath(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	metaID, err := s.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: 4001, Title: "Neutraler Titel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemMetadata(id, metaID); err != nil {
		t.Fatal(err)
	}
	personID, err := s.UpsertPerson(9999, "Jason Biggs", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceMetadataCast(metaID, "cast", []model.CastMember{{PersonID: personID, Name: "Jason Biggs"}}); err != nil {
		t.Fatal(err)
	}

	for _, fuzzy := range []bool{false, true} {
		gotWordStart := searchIDs(t, s, lib, "biggs", fuzzy)
		if !gotWordStart[id] {
			t.Errorf("fuzzy=%v: Wortanfang-Treffer 'biggs' haette den Film ueber den Cast finden muessen", fuzzy)
		}
		gotMidWord := searchIDs(t, s, lib, "iggs", fuzzy)
		if gotMidWord[id] {
			t.Errorf("fuzzy=%v: Mittentreffer 'iggs' haette NICHT ueber den Cast treffen duerfen (Wortanfang-Regel)", fuzzy)
		}
	}
}

// TestFTSSearchRespectsLibraryACL: Regressionstest für die am 2026-08-22
// gefundene Sicherheitslücke (siehe Kommentar bei "Sicherheitslücke
// gefunden 2026-08-22" in ListItems/items.go) — eine bibliotheksübergreifende
// Suche (kein LibraryID-Scope, wie z.B. die Home-View-Suche) darf für einen
// Non-Admin NIE Treffer aus einer Bibliothek liefern, auf die er keinen
// user_library_access hat. Deckt jetzt zusätzlich beide FTS5-Suchmodi ab
// (exakt + fuzzy), da die Migration die komplette Titel-Suchlogik ausgetauscht
// hat und ein künftiger Umbau der FTS5-Query denselben ACL-Sperrpunkt
// (itemsFromWhere) versehentlich umgehen könnte, ohne dass es auffällt.
func TestFTSSearchRespectsLibraryACL(t *testing.T) {
	s := newTestStore(t)

	libAllowed, err := s.CreateLibrary("Erlaubt", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	libForbidden, err := s.CreateLibrary("Verboten", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}

	// Beide Filme teilen denselben Suchbegriff als Wortanfang, damit sowohl
	// der exakte als auch der Fuzzy-Modus theoretisch BEIDE träfen, wenn die
	// ACL-Klausel nicht griffe.
	allowedID := upsertMovieForSearch(t, s, libAllowed, 9101, "a.mkv", "Zauberwald Eins")
	forbiddenID := upsertMovieForSearch(t, s, libForbidden, 9102, "b.mkv", "Zauberwald Zwei")

	userID, err := s.CreateUser("nonadmin", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserLibraryAccess(userID, []int64{libAllowed}); err != nil {
		t.Fatal(err)
	}

	for _, fuzzy := range []bool{false, true} {
		// Bewusst OHNE LibraryID/LibraryIDs (globale Suche) — genau der
		// Fall, der am 2026-08-22 ungefiltert über alle Bibliotheken lief.
		items, err := s.ListItems(ItemFilter{
			Search:      "zauberwald",
			SearchFuzzy: fuzzy,
			UserID:      userID,
			IsAdmin:     false,
		})
		if err != nil {
			t.Fatalf("fuzzy=%v: ListItems: %v", fuzzy, err)
		}
		got := map[int64]bool{}
		for _, it := range items {
			got[it.ID] = true
		}
		if !got[allowedID] {
			t.Errorf("fuzzy=%v: Treffer aus der erlaubten Bibliothek fehlt", fuzzy)
		}
		if got[forbiddenID] {
			t.Errorf("fuzzy=%v: Treffer aus einer NICHT freigegebenen Bibliothek wurde geliefert — ACL-Leck", fuzzy)
		}
		if len(items) != 1 {
			t.Errorf("fuzzy=%v: erwartet genau 1 Treffer, bekam %d (items=%v)", fuzzy, len(items), items)
		}

		// Gegenprobe fürs Zähl-Pendant CountItemsFiltered (nutzt dieselbe
		// itemsFromWhere-WHERE-Klausel, siehe Fix 3) — muss dieselbe
		// ACL-Sperre respektieren, sonst würde der Fuzzy-"N weitere
		// Treffer"-Button einem Non-Admin verraten, dass es in einer für
		// ihn unsichtbaren Bibliothek weitere Treffer gibt.
		count, err := s.CountItemsFiltered(ItemFilter{
			Search:      "zauberwald",
			SearchFuzzy: fuzzy,
			UserID:      userID,
			IsAdmin:     false,
		})
		if err != nil {
			t.Fatalf("fuzzy=%v: CountItemsFiltered: %v", fuzzy, err)
		}
		if count != 1 {
			t.Errorf("fuzzy=%v: CountItemsFiltered = %d, want 1 (ACL-Leck?)", fuzzy, count)
		}
	}

	// Admin sieht beide (Kontrastprobe — Admin-Bypass ist hier bewusstes
	// Design, siehe TestLibraryACL in acl_test.go).
	adminItems, err := s.ListItems(ItemFilter{Search: "zauberwald", UserID: userID, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(adminItems) != 2 {
		t.Errorf("Admin: erwartet 2 Treffer (beide Bibliotheken), bekam %d", len(adminItems))
	}
}
