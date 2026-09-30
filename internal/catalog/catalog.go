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

	// Einmal beim Laden aufgebaut (Match lief vorher für jede Datei über
	// alle Zeilen und normalisierte jeden Titel neu — 17 s für den Tatort).
	byTitle map[string][]int
	byDay   map[string][]int
	days    []time.Time
}

func (c *Catalog) index() {
	c.byTitle = map[string][]int{}
	c.byDay = map[string][]int{}
	c.days = make([]time.Time, len(c.Episodes))
	for i, e := range c.Episodes {
		c.byTitle[NormTitle(e.Title)] = append(c.byTitle[NormTitle(e.Title)], i)
		if d, err := time.Parse("2006-01-02", e.Date); err == nil {
			c.days[i] = d
			c.byDay[e.Date] = append(c.byDay[e.Date], i)
		}
	}
}

var (
	once     sync.Once
	byShowID map[int64]*Catalog
)

func load() {
	byShowID = map[int64]*Catalog{}
	var c Catalog
	if err := json.Unmarshal(tatortJSON, &c); err == nil && c.TMDBShowID > 0 {
		c.index()
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
	if aired.IsZero() {
		return -1
	}
	nt := NormTitle(title)
	absDays := func(i int) int {
		d := int(aired.Sub(c.days[i]).Hours() / 24)
		if d < 0 {
			d = -d
		}
		return d
	}
	best, bestScore := -1, 0
	// Gleicher Titel: ≤ 7 Tage → sicher, ≤ 400 Tage → schwacher Treffer.
	for _, i := range c.byTitle[nt] {
		if c.days[i].IsZero() {
			continue
		}
		score := 0
		if d := absDays(i); d <= 7 {
			score = 3
		} else if d <= 400 {
			score = 1
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	// Gleicher Tag, Titel enthält/ist enthalten.
	if bestScore < 3 && nt != "" {
		for _, i := range c.byDay[aired.Format("2006-01-02")] {
			ne := NormTitle(c.Episodes[i].Title)
			if ne != "" && (strings.Contains(ne, nt) || strings.Contains(nt, ne)) && bestScore < 2 {
				best, bestScore = i, 2
			}
		}
	}
	return best
}
