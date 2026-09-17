package enrich

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// 🔴 Regressionstest für das Log-Flut-Problem (2026-09-17).
//
// `PendingItems`/`PendingFolders` liefern bei JEDEM Worker-Lauf (alle
// 5 Minuten) erneut dieselben Dateien, die dauerhaft nicht matchbar sind.
// Wurde je Datei eine Logzeile geschrieben, verdrängte das binnen Minuten
// alle `[transcode]`-Zeilen aus dem Docker-Log-Puffer — gemessen am
// 2026-09-17: 300 von 301 Zeilen waren dieselbe enrich-Warnung. Bei einem
// echten Serverproblem war die Spur dadurch längst überschrieben.
//
// Diese Tests halten fest: EINE Zusammenfassung pro Lauf, nicht N Zeilen.

// captureLog fängt die Ausgabe des Standard-Loggers ein.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	oldOut, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	}()
	fn()
	return buf.String()
}

// Viele Fehler desselben Grundes dürfen NICHT je eine Zeile erzeugen.
func TestLogMatchFailuresSummarises(t *testing.T) {
	reasons := map[string]int{
		"kein Episodenformat SxxExx im Namen": 268,
		"kein Film-Treffer":                   3,
	}
	examples := map[string]string{
		"kein Episodenformat SxxExx im Namen": "/media/Serien/Derrick/D.006.avi",
		"kein Film-Treffer":                   "/media/Filme/Unbekannt.mkv",
	}
	out := captureLog(t, func() { logMatchFailures(reasons, examples) })

	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Erwartet: 1 Kopfzeile + 1 Zeile je Grund = 3. Keinesfalls 271.
	if len(lines) != 3 {
		t.Fatalf("erwartet 3 Zeilen (Kopf + 2 Gründe), bekommen %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "271") {
		t.Errorf("Kopfzeile muss die Gesamtzahl 271 nennen: %q", lines[0])
	}
	// Häufigster Grund zuerst.
	if !strings.Contains(lines[1], "268") {
		t.Errorf("häufigster Grund muss zuerst stehen: %q", lines[1])
	}
	if !strings.Contains(lines[1], "Derrick") {
		t.Errorf("Beispielpfad fehlt: %q", lines[1])
	}
}

// Ohne Fehler darf gar nichts geloggt werden — sonst steht bei einem
// sauberen Lauf alle 5 Minuten eine nutzlose Zeile im Log.
func TestLogMatchFailuresSilentWhenEmpty(t *testing.T) {
	out := captureLog(t, func() { logMatchFailures(map[string]int{}, map[string]string{}) })
	if out != "" {
		t.Fatalf("bei 0 Fehlern darf nichts geloggt werden, bekommen: %q", out)
	}
}

// Die Zeilenzahl muss von der Zahl der GRÜNDE abhängen, nicht von der Zahl
// der betroffenen Dateien — das ist der Kern des Fixes.
func TestLogMatchFailuresScalesWithReasonsNotFiles(t *testing.T) {
	few := map[string]int{"grund A": 1}
	many := map[string]int{"grund A": 10000}
	ex := map[string]string{"grund A": "/media/x.mkv"}

	outFew := captureLog(t, func() { logMatchFailures(few, ex) })
	outMany := captureLog(t, func() { logMatchFailures(many, ex) })

	nFew := len(strings.Split(strings.TrimSpace(outFew), "\n"))
	nMany := len(strings.Split(strings.TrimSpace(outMany), "\n"))
	if nFew != nMany {
		t.Fatalf("Zeilenzahl darf nicht mit der Dateimenge wachsen: %d vs %d", nFew, nMany)
	}
}

// 🔴 Regression: Fehlermeldungen mit variablen Anteilen müssen VOR dem Zählen
// normalisiert werden.
//
// Beim Deploy von v1.4.3 fiel auf, dass der erste Fix ins Leere lief: die
// TMDB-Fehler enthalten die angefragte URL, sind also für jede einzelne
// Episode ein anderer String — und damit ein eigener „Grund". Im Log standen
// wieder so viele Zeilen wie Dateien, nur mit „1 ×" davor. Echte Zeilen aus
// dem Live-Log dienen hier als Testdaten.
func TestNormaliseReasonGroupsTMDBErrors(t *testing.T) {
	live := []string{
		`TMDB GET /tv/4454/season/9/episode/12: {"success":false,"status_code":34}`,
		`TMDB GET /tv/4454/season/9/episode/3: {"success":false,"status_code":34}`,
		`TMDB GET /tv/4583/season/2/episode/19: {"success":false,"status_code":34}`,
		`TMDB GET /tv/4583/season/2/episode/59: {"success":false,"status_code":34}`,
	}
	seen := map[string]int{}
	for _, m := range live {
		seen[normaliseReason(m)]++
	}
	if len(seen) != 1 {
		t.Fatalf("vier TMDB-Episodenfehler müssen zu EINEM Grund werden, sind: %d (%v)", len(seen), seen)
	}
	for k, n := range seen {
		if n != 4 {
			t.Fatalf("Zähler falsch: %d", n)
		}
		if strings.Contains(k, "4454") || strings.Contains(k, "episode/12") {
			t.Errorf("normalisierter Grund enthält noch variable Teile: %q", k)
		}
		if !strings.Contains(k, "TMDB GET") {
			t.Errorf("normalisierter Grund hat den Kern verloren: %q", k)
		}
	}
}

// Gründe ohne variable Anteile müssen unverändert bleiben — sonst verlieren
// die häufigsten Meldungen ihre Aussagekraft.
func TestNormaliseReasonKeepsPlainMessages(t *testing.T) {
	for _, m := range []string{
		"kein Episodenformat SxxExx im Namen",
		"kein Film-Treffer auf allen Ebenen (TMDB + OMDb)",
		"episode ohne Show-Ordner",
	} {
		if got := normaliseReason(m); got != m {
			t.Errorf("unveränderte Meldung erwartet:\n  vorher: %q\n  nachher: %q", m, got)
		}
	}
}

// Bei sehr vielen verschiedenen Gründen darf das Log trotzdem nicht fluten.
func TestLogMatchFailuresCapsLineCount(t *testing.T) {
	reasons := map[string]int{}
	examples := map[string]string{}
	for i := 0; i < 50; i++ {
		r := "grund " + strings.Repeat("x", i+1)
		reasons[r] = 1
		examples[r] = "/media/a.mkv"
	}
	out := captureLog(t, func() { logMatchFailures(reasons, examples) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Kopfzeile + max. 10 Gründe + „und N weitere" = 12.
	if len(lines) > 12 {
		t.Fatalf("zu viele Zeilen trotz Deckel: %d", len(lines))
	}
	if !strings.Contains(out, "weitere Gründe") {
		t.Error("Hinweis auf abgeschnittene Gründe fehlt")
	}
}
