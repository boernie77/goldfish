package introskip

import "testing"

// realChuckAnalyzedSec: fpcalc wurde mit "-length 900" gegen die echten
// Dateien aufgerufen (siehe realdata_fixture.go-Kommentar) — dieselbe
// Umrechnung wie in fingerprint.go (analyzedSec / len(frames)).
const realChuckAnalyzedSec = 900.0

// TestDetectIntro_RealChuckPilotExcerpt ist eine Regression gegen echte
// Chromaprint-Fingerprints (nicht synthetisch) — siehe realdata_fixture.go.
// Die Fixture ist S01E02 (Referenz) gegen S01E01 "Pilot" (Kandidat).
//
// Unter dem früheren, laxeren Algorithmus (maxHammingPerFrame=15, kein
// Inverted-Index) fand dieses Paar noch einen (fälschlichen) Treffer. Unter
// dem jetzigen, an Jellyfins Intro-Skipper-Plugin angelehnten Algorithmus
// (maxHammingPerFrame=6, Inverted-Index-Shift-Search) findet dieses Paar
// KEINEN Treffer mehr — und das ist korrekt: der Pilot einer Serie hat
// oft eine abweichende oder fehlende Vorspann-Sequenz (Live-Beobachtung
// 2026-08-12/13 gegen die echte Bibliothek: alle anderen Chuck-Episoden-
// paare fanden konsistente Treffer, nur Paare MIT E01 fanden keinen). Diese
// Fixture dokumentiert das jetzt bewusst als erwartetes Verhalten, statt
// fälschlich einen Treffer zu erzwingen.
func TestDetectIntro_RealChuckPilotExcerpt(t *testing.T) {
	frameDuration := realChuckAnalyzedSec / float64(len(realChuckCand))
	start, end, _, ok := detectIntro(realChuckRef, realChuckCand, frameDuration)
	if ok {
		t.Errorf("erwartet: kein Treffer (Pilot hat kein zuverlässig wiedererkennbares Intro), got start=%.1f end=%.1f", start, end)
	}
}
