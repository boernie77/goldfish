package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

func TestScanImages(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg fehlt")
	}
	dir := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		// 64x48 PNG per ffmpeg erzeugen
		if err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", "color=c=red:s=64x48", "-frames:v", "1", p).Run(); err != nil {
			t.Fatal(err)
		}
	}
	mk("Bilder/a.png")
	mk("Bilder/Urlaub/b.png")
	mk("Skip/c.png")
	// Begleitbilder von Videos dürfen NICHT als Bilder erscheinen.
	mk("Filme/film.mp4.jpg")
	mk("Filme/film2.png")
	_ = os.WriteFile(filepath.Join(dir, "Filme", "film2.mkv"), []byte("v"), 0o644)
	mk("Filme/poster.jpg")
	mk("Filme/season01-poster.jpg")
	_ = os.WriteFile(filepath.Join(dir, "Bilder", "notes.txt"), []byte("x"), 0o644)

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	libID, err := db.CreateLibrary("A", dir, model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetLibraryImagesEnabled(libID, true); err != nil {
		t.Fatal(err)
	}
	lib, _ := db.GetLibrary(libID)
	thumbs := t.TempDir()
	sc := New(db, thumbs)
	if err := sc.scanImages(context.Background(), *lib, []string{dir}, false, "", nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountImages(libID); n != 3 {
		t.Fatalf("erwartet 3 Bilder, got %d", n)
	}
	in, _ := db.ListImagesInFolder(libID, "Bilder")
	if len(in) != 1 {
		t.Fatalf("Ordner: %+v", in)
	}
	// Vorschau entsteht erst auf Anfrage, nicht im Scan.
	thumb := ImageThumbPath(thumbs, filepath.Join(dir, "Bilder", "a.png"))
	if _, err := os.Stat(thumb); err == nil {
		t.Fatal("Scan darf keine Vorschauen erzeugen")
	}
	if err := MakeImageThumb(context.Background(), filepath.Join(dir, "Bilder", "a.png"), thumb); err != nil {
		t.Fatalf("Vorschau auf Anfrage: %v", err)
	}
	// Ausschluss: Skip wird nicht gewalkt, bestehende Zeilen bleiben erhalten.
	if err := sc.scanImages(context.Background(), *lib, []string{dir}, false, "", []string{"Skip"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountImages(libID); n != 3 {
		t.Fatalf("Ausschluss darf nichts löschen, got %d", n)
	}
	// Datei verschwindet -> beim nächsten Voll-Scan entfernt.
	_ = os.Remove(filepath.Join(dir, "Bilder", "Urlaub", "b.png"))
	if err := sc.scanImages(context.Background(), *lib, []string{dir}, false, "", nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountImages(libID); n != 2 {
		t.Fatalf("nach Löschen erwartet 2, got %d", n)
	}
}
