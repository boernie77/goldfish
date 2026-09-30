// Package catalog enthält externe Folgenkataloge mit Angaben, die TMDB nicht
// kennt — erster (und bisher einziger) Fall: die Ermittler-Teams des Tatort
// (Wikipedia „Liste der Tatort-Folgen", CC BY-SA 4.0, eingelesen per
// scripts/import_tatort_catalog.py). Damit zeigt die Kommissar-Ansicht, welche
// Folgen eines Teams fehlen (User-Wunsch 2026-09-30).
package catalog

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

//go:embed data/tatort.json
var tatortJSON []byte

// Episode ist eine Katalog-Zeile.
type Episode struct {
	Nr        string   `json:"nr"`
	Title     string   `json:"title"`
	Sender    string   `json:"sender"`
	Date      string   `json:"date"` // Erstausstrahlung YYYY-MM-DD
	Ermittler []string `json:"ermittler"`
	ORF       bool     `json:"orf"`
}

// Catalog gehört zu genau einer TMDB-Serie.
type Catalog struct {
	Source     string    `json:"source"`
	TMDBShowID int64     `json:"tmdbShowId"`
	Episodes   []Episode `json:"episodes"`
}

var (
	once     sync.Once
	byShowID map[int64]*Catalog
)

func load() {
	byShowID = map[int64]*Catalog{}
	var c Catalog
	if err := json.Unmarshal(tatortJSON, &c); err == nil && c.TMDBShowID > 0 {
		byShowID[c.TMDBShowID] = &c
	}
}

// ForShow liefert den Katalog einer TMDB-Serie (nil = keiner vorhanden).
func ForShow(tmdbShowID int64) *Catalog {
	once.Do(load)
	return byShowID[tmdbShowID]
}

// NormTitle vereinheitlicht Titel für den Vergleich: Kleinbuchstaben,
// Umlaute ausgeschrieben, Akzente weg, „(1)"/„Teil 1" gleich, nur a–z0–9.
func NormTitle(s string) string {
	s = strings.ToLower(s)
	r := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "(1)", "teil 1", "(2)", "teil 2", "(3)", "teil 3")
	s = r.Replace(s)
	var b strings.Builder
	for _, ch := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, ch) {
			continue
		}
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// Match sucht die Katalog-Folge zu einer Datei anhand von TMDB-Titel und
// -Erstausstrahlung. Kriterium: gleicher Titel (normalisiert) und Datum höchstens
// 7 Tage auseinander (Wikipedia und TMDB weichen um einen Tag ab, Strandgut:
// 25. vs. 26.06.1972), sonst exakt gleiches Datum bei ähnlichem Titel
// (enthält/enthalten). -1 = kein Treffer.
func (c *Catalog) Match(title string, aired time.Time) int {
	nt := NormTitle(title)
	best, bestScore := -1, 0
	for i, e := range c.Episodes {
		d, err := time.Parse("2006-01-02", e.Date)
		if err != nil || aired.IsZero() {
			continue
		}
		days := int(aired.Sub(d).Hours() / 24)
		if days < 0 {
			days = -days
		}
		ne := NormTitle(e.Title)
		score := 0
		switch {
		case ne == nt && days <= 7:
			score = 3
		case days == 0 && nt != "" && ne != "" && (strings.Contains(ne, nt) || strings.Contains(nt, ne)):
			score = 2
		case ne == nt && days <= 400: // gleicher Titel, Datum stark abweichend (Wiederholung als Erstausstrahlung bei TMDB)
			score = 1
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}
