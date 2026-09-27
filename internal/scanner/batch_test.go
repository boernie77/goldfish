package scanner

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
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
