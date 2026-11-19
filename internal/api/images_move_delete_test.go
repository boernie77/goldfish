package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// Bild-Ordner ohne Videos: erscheint im Verschieben-Baum, Bilder werden samt
// DB-Zeile verschoben (Löschen ist nicht abgedeckt).
func TestImageFolderMove(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	lib, _ := st.CreateLibrary("A", dir, model.KindPrivate)
	mk := func(rel string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
		_ = st.UpsertImage(&model.Image{LibraryID: lib, Path: p, RelPath: rel, Title: filepath.Base(rel), ModTime: time.Now()})
	}
	mk("Bilder/Urlaub/a.jpg")
	mk("Bilder/b.jpg")
	_ = os.MkdirAll(filepath.Join(dir, "Ziel"), 0o755)

	all, _ := st.AllFolderPaths(lib)
	if len(all) != 2 || all[0] != "Bilder" {
		t.Fatalf("Bildordner fehlen im Baum: %v", all)
	}

	srv := &Server{Store: st, ConfigDir: t.TempDir()}
	plan := srv.planImageMoves(lib, []string{"Bilder"}, "Ziel")
	if len(plan) != 2 {
		t.Fatalf("plan=%d", len(plan))
	}
	for _, ip := range plan {
		if msg, code := srv.executeImageMove(ip.Image, 0, ip.Target, "", false); code != 0 {
			t.Fatalf("move: %s", msg)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Ziel", "Bilder", "Urlaub", "a.jpg")); err != nil {
		t.Fatalf("Datei nicht am Ziel: %v", err)
	}
	if ims, _ := st.ImagesUnderFolder(lib, "Ziel/Bilder"); len(ims) != 2 {
		t.Fatalf("DB nicht umgeschrieben: %d", len(ims))
	}
	if ims, _ := st.ImagesUnderFolder(lib, "Bilder"); len(ims) != 0 {
		t.Fatalf("alte Zeilen übrig")
	}
}
