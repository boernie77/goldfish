package scanner

import (
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// Durchlauf über mehrere Bibliotheken (2026-09-27): während er läuft, nur
// Fortschritt; danach die Berichte ALLER Bibliotheken — vorher überschrieb
// jeder Einzelscan den Bericht des vorigen.
func TestBatchSummariesOnlyAfterEnd(t *testing.T) {
	sc := &Scanner{}
	sc.BeginBatch(3)
	sc.batch = append(sc.batch, model.ScanSummary{LibraryName: "Filme"}, model.ScanSummary{LibraryName: "Serien"})

	st := sc.Status()
	if !st.BatchActive || st.BatchTotal != 3 || st.BatchDone != 2 {
		t.Fatalf("Fortschritt falsch: active=%v total=%d done=%d", st.BatchActive, st.BatchTotal, st.BatchDone)
	}
	if len(st.BatchSummaries) != 0 {
		t.Fatal("während des Durchlaufs keine Berichte ausliefern (Poll jede Sekunde)")
	}

	sc.batch = append(sc.batch, model.ScanSummary{LibraryName: "Musik"})
	sc.EndBatch()
	st = sc.Status()
	if st.BatchActive {
		t.Fatal("nach EndBatch darf der Durchlauf nicht mehr aktiv sein")
	}
	if len(st.BatchSummaries) != 3 || st.BatchSummaries[0].LibraryName != "Filme" || st.BatchSummaries[2].LibraryName != "Musik" {
		t.Fatalf("alle drei Berichte in Reihenfolge erwartet, bekommen %+v", st.BatchSummaries)
	}
	// Die ausgelieferte Liste ist eine Kopie — ein späterer Scan darf sie
	// beim Aufrufer nicht verändern.
	st.BatchSummaries[0].LibraryName = "geändert"
	if sc.batch[0].LibraryName != "Filme" {
		t.Fatal("Status() muss eine Kopie liefern")
	}
}

// Ein einzelner Scan ohne Durchlauf liefert keinen Sammelbericht.
func TestSingleScanHasNoBatch(t *testing.T) {
	sc := &Scanner{}
	sc.batch = []model.ScanSummary{{LibraryName: "Filme"}}
	st := sc.Status()
	if st.BatchActive || st.BatchTotal != 0 || len(st.BatchSummaries) != 0 {
		t.Fatalf("Einzelscan darf keine Durchlauf-Felder setzen: %+v", st)
	}
}

// Der letzte Bericht muss einen Neustart überstehen (2026-09-27): ein Deploy
// direkt nach einem langen Scan über alle Bibliotheken warf ihn vorher weg.
func TestLastReportSurvivesRestart(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "report.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sc := New(st, t.TempDir())
	sc.BeginBatch(2)
	sc.batch = append(sc.batch, model.ScanSummary{LibraryName: "Filme", New: 1}, model.ScanSummary{LibraryName: "Serien", New: 4})
	sc.status.LastSummary = &sc.batch[1]
	sc.EndBatch()

	restarted := New(st, t.TempDir())
	got := restarted.Status()
	if len(got.BatchSummaries) != 2 || got.BatchSummaries[1].LibraryName != "Serien" || got.BatchSummaries[1].New != 4 {
		t.Fatalf("Durchlauf nach Neustart verloren: %+v", got.BatchSummaries)
	}
	if got.LastSummary == nil || got.LastSummary.LibraryName != "Serien" {
		t.Fatalf("Einzelbericht nach Neustart verloren: %+v", got.LastSummary)
	}
	if got.Running || got.BatchActive {
		t.Fatal("nach Neustart darf kein Scan als laufend gelten")
	}
}
