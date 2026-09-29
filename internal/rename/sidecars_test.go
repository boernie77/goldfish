package rename

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestPlanAndMoveSidecars sichert den Kommissar-Dupin-Fall (2026-09-29) ab:
// Beim Umbenennen muss die NFO mit, ebenso Untertitel und Kodi-Bilder — aber
// nichts, was zu einem ANDEREN Video im selben Ordner gehört.
func TestPlanAndMoveSidecars(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	touch := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{
		"Film.mkv", "Film.nfo", "Film.de.srt", "Film.forced.vtt", "Film-poster.jpg",
		"Film.sub", "Film.idx",
		// Gehören zu Film.2.mkv, nicht zu Film.mkv:
		"Film.2.mkv", "Film.2.nfo", "Film.2.de.srt",
		// Kein Sidecar (unbekannte Endung / anderer Stamm):
		"Film.txt", "Filmmusik.nfo", "desktop.ini",
	} {
		touch(n)
	}

	oldVideo := filepath.Join(src, "Film.mkv")
	newVideo := filepath.Join(dst, "Neuer Titel (2020).mkv")
	plan := PlanSidecars(oldVideo, newVideo)

	var got []string
	for _, m := range plan {
		got = append(got, filepath.Base(m.From)+" -> "+filepath.Base(m.To))
	}
	sort.Strings(got)
	want := []string{
		"Film-poster.jpg -> Neuer Titel (2020)-poster.jpg",
		"Film.de.srt -> Neuer Titel (2020).de.srt",
		"Film.forced.vtt -> Neuer Titel (2020).forced.vtt",
		"Film.idx -> Neuer Titel (2020).idx",
		"Film.nfo -> Neuer Titel (2020).nfo",
		"Film.sub -> Neuer Titel (2020).sub",
	}
	if len(got) != len(want) {
		t.Fatalf("Plan:\n got  %v\n want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Plan[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	if err := RenameOnDisk(oldVideo, newVideo, false); err != nil {
		t.Fatal(err)
	}
	if errs := MoveSidecars(plan, false); len(errs) != 0 {
		t.Fatalf("MoveSidecars: %v", errs)
	}
	for _, n := range []string{"Neuer Titel (2020).nfo", "Neuer Titel (2020).de.srt", "Neuer Titel (2020)-poster.jpg"} {
		if _, err := os.Stat(filepath.Join(dst, n)); err != nil {
			t.Errorf("%s fehlt am Ziel: %v", n, err)
		}
	}
	for _, n := range []string{"Film.2.nfo", "Film.2.de.srt", "Film.txt", "Filmmusik.nfo", "desktop.ini"} {
		if _, err := os.Stat(filepath.Join(src, n)); err != nil {
			t.Errorf("%s hätte liegen bleiben müssen: %v", n, err)
		}
	}
}
