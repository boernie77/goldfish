package api

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// Ganze Ordner behalten beim Verschieben Namen und Unterstruktur.
func TestPlanMovesKeepsStructure(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary("Lib", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	libID := lib
	for _, rel := range []string{"Serie/S1/a.mkv", "Serie/b.mkv", "Andere/c.mkv", "x/Serie2/S1/d.mkv"} {
		if err := st.UpsertItem(&model.Item{LibraryID: libID, Path: "/tmp/" + rel, RelPath: rel, Title: rel, Container: "mkv"}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Store: st}
	got := func(folder, target string) []string {
		plan, moves, err := s.planMoves(libID, nil, []string{folder}, target)
		if err != nil || len(moves) != 1 {
			t.Fatalf("planMoves: %v %v", err, moves)
		}
		var out []string
		for _, p := range plan {
			out = append(out, p.Target)
		}
		sort.Strings(out)
		return out
	}
	if g := got("Serie", "Archiv"); len(g) != 2 || g[0] != "Archiv/Serie" || g[1] != "Archiv/Serie/S1" {
		t.Errorf("Serie → Archiv: %v", g)
	}
	if g := got("Serie", ""); len(g) != 2 || g[0] != "Serie" || g[1] != "Serie/S1" {
		t.Errorf("Serie → Wurzel: %v", g)
	}
	if g := got("x/Serie2", "Y"); len(g) != 1 || g[0] != "Y/Serie2/S1" {
		t.Errorf("x/Serie2 → Y: %v", g)
	}
	if _, _, err := s.planMoves(libID, nil, []string{"Serie"}, "Serie/S1"); err == nil {
		t.Error("Ziel im Quellordner muss abgelehnt werden")
	}
}
