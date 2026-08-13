package introskip

import "testing"

// Diese Tests decken die frühere filterOutliers-Logik NICHT mehr direkt ab
// — die Ausreißer-Filterung passiert seit der Umstellung auf echtes
// Paarweise-Vergleichen (2026-08-13, siehe worker.go processShow) implizit
// über zwei unabhängige Mechanismen: (1) aggregateObservations verlangt
// mehrere übereinstimmende Referenz-Episoden (siehe TestAggregateObservations
// in correlate_test.go), (2) verifyVideoMatch verlangt zusätzlich visuelle
// Bestätigung pro einzelner Paarung (siehe TestVerifyVideoMatch). Die
// folgenden Tests sind Regressionen für dieselben real beobachteten Muster,
// jetzt gegen aggregateObservations statt der entfernten filterOutliers.

// TestAggregateObservations_RealAbbottElementaryPattern ist eine Regression
// gegen ein real in Produktion beobachtetes Muster (2026-08-12): bei
// "Abbott Elementary" landete die paarweise Korrelation bei einem Teil der
// Referenz-Episoden korrekt nahe am Anfang, beim anderen Teil fälschlich
// bei ~67% der Laufzeit (vermutlich Abspann-Musik statt Vorspann). Wenn
// die korrekten Beobachtungen in der Mehrheit sind, muss der Konsens sie
// finden und die Ausreißer verwerfen.
func TestAggregateObservations_RealAbbottElementaryPattern(t *testing.T) {
	obs := []observation{
		{startSec: 64.7, endSec: 95.7},
		{startSec: 46.7, endSec: 93.0},
		{startSec: 25.3, endSec: 81.6},
		{startSec: 29.3, endSec: 100.3},
		{startSec: 68.2, endSec: 91.6},
		{startSec: 67.3, endSec: 111.9},
		// Ausreißer — real beobachtete Fehltreffer bei ~67% der Laufzeit:
		{startSec: 879.0, endSec: 900.0},
		{startSec: 880.5, endSec: 900.0},
	}
	agg, ok := aggregateObservations(obs, 2)
	if !ok {
		t.Fatal("want ok=true")
	}
	if agg.startSec > 200 {
		t.Errorf("startSec = %v, want den frühen Cluster (~25-68s), nicht die späten Ausreißer", agg.startSec)
	}
}

// TestAggregateObservations_VariableColdOpenIsolatesEachEpisode ist die
// Regression für den Bug vom 2026-08-12: ein einzelner globaler
// Cluster-Konsens über ALLE Episoden EINER Show bestrafte Serien mit
// legitim schwankender Cold-Open-Länge. Seit dem Paarweise-Umbau wird
// aggregateObservations pro KANDIDATEN-Episode separat aufgerufen (siehe
// worker.go processShow) — jede Episode hat ihre eigenen Beobachtungen aus
// den Vergleichen mit den anderen Episoden, unabhängig davon, wie weit die
// erkannte Position dieser einen Episode von anderen Episoden abweicht.
// Dieser Test prüft nur, dass zwei nah beieinanderliegende Beobachtungen
// (von zwei verschiedenen Referenzen für DIESELBE Kandidaten-Episode)
// weiterhin akzeptiert werden, auch wenn die absolute Position ungewöhnlich
// ist (hier: später Cold Open bei 700s).
func TestAggregateObservations_VariableColdOpenIsolatesEachEpisode(t *testing.T) {
	obs := []observation{
		{startSec: 700, endSec: 740},
		{startSec: 702, endSec: 742},
	}
	agg, ok := aggregateObservations(obs, 2)
	if !ok {
		t.Fatal("want ok=true — zwei nah beieinanderliegende Beobachtungen für dieselbe Episode")
	}
	if agg.startSec < 690 || agg.startSec > 710 {
		t.Errorf("startSec = %v, want ~700 (unabhängig davon, wie weit andere Episoden abweichen)", agg.startSec)
	}
}
