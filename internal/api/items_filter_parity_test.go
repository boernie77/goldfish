package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 🔴 `listItems` und `randomItem` bauen ihren store.ItemFilter GETRENNT auf.
// Genau daran ist v1.4.6 beim ersten Anlauf gescheitert: `playlistId` wurde
// nur in `randomItem` geparst, `listItems` ignorierte den Parameter
// stillschweigend — statt der ~4.500 Titel aus den Playlists lieferte die
// Filter-Ansicht die kompletten ~95.000 Items der Bibliothek. Aufgefallen
// ist das erst bei der Prüfung gegen den Live-Server, weil ein Store-Test
// den Handler gar nicht durchläuft.
//
// Dieser Test hält fest, dass beide Handler dieselben Query-Parameter
// auswerten. Er liest den Quelltext, weil es für die Handler (noch) kein
// Testgerüst mit Datenbank gibt — grob, aber es fängt genau den Fehler ab,
// der real passiert ist.
func TestListAndRandomShareFilterParams(t *testing.T) {
	src, err := os.ReadFile("items.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)

	listBody := funcBody(t, code, "func (s *Server) listItems(")
	randomBody := funcBody(t, code, "func (s *Server) randomItem(")

	// Parameter, die den Ergebnis-UMFANG bestimmen. Fehlt einer davon in
	// einem der beiden Handler, liefert er mehr Daten als gewollt — im
	// schlimmsten Fall Inhalte, die der Nutzer nicht sehen darf.
	scopeParams := []string{
		"playlistId", // Playlist-Filter (v1.4.6)
		"personId",   // Schauspieler-Filter
		"search",
		"watched",
		"favorite",
	}
	// `albumId` steht bewusst NICHT in der Liste: `randomItem` wertet ihn aus
	// (Zufall innerhalb eines geoeffneten Albums), `listItems` nicht — und das
	// ist in Ordnung, weil die Musik-UI Album-Titel ueber den eigenen
	// Endpoint `/api/albums/{id}` holt, nie ueber `/api/items?albumId=`.
	// Sollte das jemals umgestellt werden, muss `listItems` den Parameter
	// nachziehen, sonst liefert er die ganze Bibliothek statt des Albums.
	for _, p := range scopeParams {
		inList := strings.Contains(listBody, `"`+p+`"`)
		inRandom := strings.Contains(randomBody, `"`+p+`"`)
		if inList != inRandom {
			t.Errorf("Parameter %q wird nur in EINEM der beiden Handler ausgewertet "+
				"(listItems=%v, randomItem=%v) — der andere ignoriert ihn still und "+
				"liefert dadurch zu viele Items", p, inList, inRandom)
		}
	}

	// Beide Handler MÜSSEN UserID und IsAdmin aus der Session setzen — darauf
	// bauen sämtliche Sichtbarkeitsregeln in Store.ListItems auf (Library-ACL,
	// Playlist-Besitz, FSK).
	for name, body := range map[string]string{"listItems": listBody, "randomItem": randomBody} {
		if !strings.Contains(body, "me.ID") {
			t.Errorf("%s setzt UserID nicht aus der Session — ACL-Filter greifen dann nicht", name)
		}
		if !strings.Contains(body, "me.IsAdmin") {
			t.Errorf("%s setzt IsAdmin nicht aus der Session", name)
		}
	}
}

// funcBody schneidet den Rumpf einer Go-Funktion aus dem Quelltext heraus —
// von der Signatur bis zur schließenden Klammer auf Spaltenposition 0.
func funcBody(t *testing.T, code, signature string) string {
	t.Helper()
	start := strings.Index(code, signature)
	if start < 0 {
		t.Fatalf("Funktion nicht gefunden: %s", signature)
	}
	rest := code[start:]
	if end := regexp.MustCompile(`(?m)^\}`).FindStringIndex(rest); end != nil {
		return rest[:end[1]]
	}
	return rest
}
