package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// TestUpdateMusicAlbumMetadataPerformance ist kein echter Benchmark (kein
// -bench-Ziel), sondern ein manueller Zeitmess-Test für den 2026-09-12
// Performance-Fix (User-Report: "dauert weit über eine Minute") — mit einer
// zur echten Bibliothek des Users vergleichbaren Größenordnung (~2700 Alben)
// soll ein einzelnes Metadaten-Edit deutlich unter einer Sekunde bleiben,
// nicht wie zuvor mehrere zehn Sekunden bis Minuten (tausende einzeln
// committete Statements ohne Transaktion). t.Skip im normal `go test`-Lauf
// vermeiden wir bewusst NICHT — die paar hundert ms Laufzeit sind für die
// CI/lokale Suite unproblematisch und die Absicherung ist es wert.
func TestUpdateMusicAlbumMetadataPerformance(t *testing.T) {
	s := newTestStore(t)
	libID, err := s.CreateLibrary("Musik", t.TempDir(), model.KindMusic)
	if err != nil {
		t.Fatal(err)
	}
	const numAlbums = 2700
	for i := 0; i < numAlbums; i++ {
		mustUpsertMusicItem(t, s, libID,
			fmt.Sprintf("Album%d/01 Song.mp3", i),
			fmt.Sprintf("Artist %d", i),
			fmt.Sprintf("Album %d", i),
			"Rock",
		)
	}
	if err := s.GroupMusicAlbums(libID); err != nil {
		t.Fatal(err)
	}
	albums, err := s.ListMusicAlbums(libID, 0)
	if err != nil || len(albums) != numAlbums {
		t.Fatalf("expected %d albums, got %d err=%v", numAlbums, len(albums), err)
	}

	start := time.Now()
	if err := s.UpdateMusicAlbumMetadata(albums[0].ID, albums[0].Artist, albums[0].Album, "NewGenre", 2020); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("UpdateMusicAlbumMetadata mit %d Alben in der Library: %s", numAlbums, elapsed)
	if elapsed > 3*time.Second {
		t.Fatalf("UpdateMusicAlbumMetadata zu langsam: %s (erwartet: deutlich unter 3s)", elapsed)
	}
}
