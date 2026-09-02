package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestCollectionsACL sichert den Fix vom 2026-09-02 ab (User-Report: Benutzer
// "reviewer" sah Sammlungen aus Bibliotheken, auf die er keinen ACL-Zugriff
// hatte). Zwei Libraries, zwei Filme derselben TMDB-Collection — ein Non-Admin
// mit ACL nur auf Library A darf den Film aus Library B nirgends sehen
// (weder als eigene Sammlung, noch als "owned" Part, noch in der
// Item-Fallback-Liste); ein Admin sieht immer beide.
func TestCollectionsACL(t *testing.T) {
	s := newTestStore(t)

	libA, err := s.CreateLibrary("A", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	libB, err := s.CreateLibrary("B", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}

	// Sammlung + zwei TMDB-Movie-Metadata-Einträge, je einer pro Library.
	var collectionID int64
	if err := s.db.QueryRow(
		`INSERT INTO collections(tmdb_id, name) VALUES(999, 'Test-Reihe') RETURNING id`,
	).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}

	insertMeta := func(tmdbID int64, title string) int64 {
		t.Helper()
		var metaID int64
		if err := s.db.QueryRow(
			`INSERT INTO metadata(tmdb_type, tmdb_id, title, collection_id) VALUES('movie', ?, ?, ?) RETURNING id`,
			tmdbID, title, collectionID,
		).Scan(&metaID); err != nil {
			t.Fatal(err)
		}
		return metaID
	}
	metaA := insertMeta(101, "Film A")
	metaB := insertMeta(102, "Film B")

	insertItem := func(libID, metaID int64, path string) int64 {
		t.Helper()
		res, err := s.db.Exec(
			`INSERT INTO items(library_id, path, rel_path, title, container, width, height, duration_sec, size_bytes, bitrate_kbps, mod_time, added_at, metadata_id)
			 VALUES(?, ?, ?, ?, 'mkv', 1920, 1080, 100, 1000, 1000, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)`,
			libID, path, path, path, metaID,
		)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	insertItem(libA, metaA, "/A/film-a.mkv")
	insertItem(libB, metaB, "/B/film-b.mkv")

	// collection_parts für GetCollectionParts (sonst greift nur der
	// ListItemsInCollection-Fallback).
	if _, err := s.db.Exec(
		`INSERT INTO collection_parts(collection_id, tmdb_movie_id, title, release_date, poster_path, ord) VALUES (?,101,'Film A','','',1), (?,102,'Film B','','',2)`,
		collectionID, collectionID,
	); err != nil {
		t.Fatal(err)
	}

	restrictedID, _ := s.CreateUser("reviewer", "pw123456", false)
	if err := s.SetUserLibraryAccess(restrictedID, []int64{libA}); err != nil {
		t.Fatal(err)
	}
	adminID, _ := s.CreateUser("admin", "pw123456", true)

	// ListCollections: Non-Admin mit nur 1 zugänglichem Film in der Sammlung
	// darf die Sammlung NICHT sehen (Mindestens-2-eigene-Filme-Regel greift
	// nur auf ACL-sichtbare Filme) — das ist zugleich der schärfste ACL-Test:
	// ohne Fix hätte er beide Filme gezählt und die Sammlung gesehen.
	restrictedCols, err := s.ListCollections(restrictedID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(restrictedCols) != 0 {
		t.Errorf("reviewer sollte die Sammlung NICHT sehen (nur 1 zugänglicher Film), bekam %d Sammlungen", len(restrictedCols))
	}
	adminCols, err := s.ListCollections(adminID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminCols) != 1 {
		t.Errorf("admin sollte die Sammlung sehen (2 Filme total), bekam %d", len(adminCols))
	}

	// GetCollectionParts: reviewer darf Film B nicht als "owned" sehen.
	restrictedParts, err := s.GetCollectionParts(collectionID, restrictedID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range restrictedParts {
		if p["tmdbMovieId"] == int64(102) && p["owned"] == true {
			t.Errorf("reviewer sieht Film B (Library B) fälschlich als owned=true")
		}
	}
	adminParts, err := s.GetCollectionParts(collectionID, adminID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	ownedForAdmin := 0
	for _, p := range adminParts {
		if p["owned"] == true {
			ownedForAdmin++
		}
	}
	if ownedForAdmin != 2 {
		t.Errorf("admin sollte beide Filme als owned sehen, bekam %d", ownedForAdmin)
	}

	// ListItemsInCollection (Fallback-Pfad): reviewer bekommt nur Film A.
	restrictedItems, err := s.ListItemsInCollection(collectionID, restrictedID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(restrictedItems) != 1 {
		t.Errorf("reviewer sollte genau 1 Item sehen (Film A), bekam %d", len(restrictedItems))
	}
	adminItems, err := s.ListItemsInCollection(collectionID, adminID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminItems) != 2 {
		t.Errorf("admin sollte beide Items sehen, bekam %d", len(adminItems))
	}
}

// TestCollectionsAgeRating sichert den zweiten, unabhängigen Fund vom selben
// Tag ab: die FSK-Altersgrenze (ItemFilter.MaxAgeRating im normalen Grid)
// griff bei Sammlungen ursprünglich gar nicht — ein Kind-/eingeschränkter
// Account hätte FSK-18-Filme über den Sammlungs-Umweg trotzdem gesehen.
// Beide Filme liegen in DERSELBEN (zugänglichen) Library, unterscheiden sich
// nur in der Altersfreigabe.
func TestCollectionsAgeRating(t *testing.T) {
	s := newTestStore(t)

	lib, err := s.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}

	var collectionID int64
	if err := s.db.QueryRow(
		`INSERT INTO collections(tmdb_id, name) VALUES(999, 'Test-Reihe') RETURNING id`,
	).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}

	insertMeta := func(tmdbID int64, title, ageRating string) int64 {
		t.Helper()
		var metaID int64
		if err := s.db.QueryRow(
			`INSERT INTO metadata(tmdb_type, tmdb_id, title, collection_id, age_rating) VALUES('movie', ?, ?, ?, ?) RETURNING id`,
			tmdbID, title, collectionID, ageRating,
		).Scan(&metaID); err != nil {
			t.Fatal(err)
		}
		return metaID
	}
	metaKids := insertMeta(201, "Kinderfilm", "6")
	metaAdult := insertMeta(202, "FSK18-Film", "18")

	insertItem := func(metaID int64, path string) {
		t.Helper()
		if _, err := s.db.Exec(
			`INSERT INTO items(library_id, path, rel_path, title, container, width, height, duration_sec, size_bytes, bitrate_kbps, mod_time, added_at, metadata_id)
			 VALUES(?, ?, ?, ?, 'mkv', 1920, 1080, 100, 1000, 1000, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)`,
			lib, path, path, path, metaID,
		); err != nil {
			t.Fatal(err)
		}
	}
	insertItem(metaKids, "/Filme/kinderfilm.mkv")
	insertItem(metaAdult, "/Filme/fsk18.mkv")

	if _, err := s.db.Exec(
		`INSERT INTO collection_parts(collection_id, tmdb_movie_id, title, release_date, poster_path, ord) VALUES (?,201,'Kinderfilm','','',1), (?,202,'FSK18-Film','','',2)`,
		collectionID, collectionID,
	); err != nil {
		t.Fatal(err)
	}

	childID, _ := s.CreateUser("kind", "pw123456", false)
	if err := s.SetUserLibraryAccess(childID, []int64{lib}); err != nil {
		t.Fatal(err)
	}

	// FSK-Limit 12: der FSK-18-Film darf nirgends auftauchen, obwohl die
	// Library selbst voll zugänglich ist.
	parts, err := s.GetCollectionParts(collectionID, childID, false, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if p["tmdbMovieId"] == int64(202) && p["owned"] == true {
			t.Errorf("FSK18-Film sollte für einen 12er-Account NICHT als owned erscheinen")
		}
	}

	items, err := s.ListItemsInCollection(collectionID, childID, false, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("ListItemsInCollection sollte nur den Kinderfilm zeigen (FSK-Limit 12), bekam %d Items", len(items))
	}

	// Ohne Limit (0) sieht derselbe Account wieder beide.
	itemsUnrestricted, err := s.ListItemsInCollection(collectionID, childID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsUnrestricted) != 2 {
		t.Errorf("ohne FSK-Limit sollten beide Filme sichtbar sein, bekam %d", len(itemsUnrestricted))
	}
}
