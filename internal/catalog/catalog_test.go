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

// TestParseTatortWikitext: der Go-Parser liefert dasselbe wie das Python-Skript
// (Crossover mit Umbruch, Gastauftritt entfernt, ORF-Nummer mit Buchstabe).
func TestParseTatortWikitext(t *testing.T) {
	text := `=== Ausgestrahlte Folgen ===
{| class="wikitable"
! Folge
|-
| 19
| [[Tatort: Strandgut|Strandgut]]
| NDR
| {{DatumZelle|1972-06-25}}
| [[Kommissar Finke|Finke]]<br />(Gastauftritt&nbsp;[[Paul Trimmel|Trimmel]])
| 4
|-
| 458
| [[Tatort: Quartett in Leipzig|Quartett in Leipzig]]
| MDR/WDR
| {{DatumZelle|2000-11-26}}
| [[Ehrlicher und Kain]] /<br />[[Ballauf und Schenk]]
| 24
|}
== ORF-eigene Produktionen ==
{| class="wikitable"
|-
|186a
|[[Tatort: Der Schnee vom vergangenen Jahr|Der Schnee vom vergangenen Jahr]]
|ORF/BR
|12. Okt. 1986
|[[Miguel Herz-Kestranek|Lutinsky]]
|1
|}`
	c := ParseTatortWikitext(text)
	if len(c.Episodes) != 3 {
		t.Fatalf("erwartet 3 Folgen, bekam %d: %+v", len(c.Episodes), c.Episodes)
	}
	e := c.Episodes
	if e[0].Nr != "19" || e[0].Title != "Strandgut" || e[0].Date != "1972-06-25" || len(e[0].Ermittler) != 1 || e[0].Ermittler[0] != "Finke" {
		t.Errorf("Zeile 19: %+v", e[0])
	}
	if len(e[1].Ermittler) != 2 || e[1].Ermittler[1] != "Ballauf und Schenk" {
		t.Errorf("Crossover: %+v", e[1])
	}
	if e[2].Nr != "186a" || e[2].Date != "1986-10-12" || !e[2].ORF || e[2].Ermittler[0] != "Lutinsky" {
		t.Errorf("ORF-Zeile: %+v", e[2])
	}
}
