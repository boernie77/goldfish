package catalog

import (
	"testing"
	"time"
)

// TestTatortCatalog: eingebetteter Katalog lädt, Titel-/Datumsabgleich trifft
// auch bei einem Tag Abweichung (Wikipedia 25.06. vs. TMDB 26.06.1972) und bei
// „Teil 1" ↔ „(1)".
func TestTatortCatalog(t *testing.T) {
	c := ForShow(3034)
	if c == nil || len(c.Episodes) < 1300 {
		t.Fatalf("Katalog fehlt oder zu klein: %v", c != nil)
	}
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	for _, tc := range []struct {
		title, aired, nr string
	}{
		{"Strandgut", "1972-06-26", "19"},
		{"Im Herzen Eiszeit", "1995-04-02", "307"},
		{"Unvergänglich (1)", "2026-04-05", "1333"},
		{"Taxi nach Leipzig", "2016-11-13", "1000"},
		{"Taxi nach Leipzig", "1970-11-29", "1"},
	} {
		i := c.Match(tc.title, day(tc.aired))
		if i < 0 || c.Episodes[i].Nr != tc.nr {
			got := "–"
			if i >= 0 {
				got = c.Episodes[i].Nr
			}
			t.Errorf("%s %s: erwartet Nr. %s, bekam %s", tc.title, tc.aired, tc.nr, got)
		}
	}
	if c.Match("Gibt es nicht", day("1999-01-01")) != -1 {
		t.Error("Phantasie-Titel darf nicht treffen")
	}
}
