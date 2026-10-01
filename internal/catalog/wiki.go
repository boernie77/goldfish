package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Laufende Aktualisierung des Tatort-Katalogs (seit 1.4.71): der Server holt
// die Wikipedia-Liste wöchentlich selbst (gleiche Regeln wie
// scripts/import_tatort_catalog.py) und speichert sie unter
// <config>/catalog/tatort.json. Die eingebettete Fassung bleibt Rückfall.

const tatortPage = "Liste_der_Tatort-Folgen"

var (
	reBrTail   = regexp.MustCompile(`(?s)<br\s*/?>.*`)
	reSmall    = regexp.MustCompile(`(?s)<small>.*?</small>`)
	reRef      = regexp.MustCompile(`(?s)<ref[^>]*/>|<ref[^>]*>.*?</ref>`)
	reTmpl     = regexp.MustCompile(`\{\{[^{}]*\}\}`)
	reLink     = regexp.MustCompile(`\[\[(?:[^\]|]*\|)?([^\]]*)\]\]`)
	reTag      = regexp.MustCompile(`<[^>]+>`)
	reSpace    = regexp.MustCompile(`\s+`)
	reDatum    = regexp.MustCompile(`DatumZelle\|(\d{4}-\d{2}-\d{2})`)
	reDeDate   = regexp.MustCompile(`(\d{1,2})\.\s*([A-Za-zä]{3})[a-zä]*\.?\s*(\d{4})`)
	reNr       = regexp.MustCompile(`^\d+[a-z]?$`)
	reGuest    = regexp.MustCompile(`\((?:[^()]*Gastauftritt|Gast)[^()]*\)`)
	reCrossBr  = regexp.MustCompile(`/\s*<br\s*/?>`)
	reRowSplit = regexp.MustCompile(`\n\|-[^\n]*`)
)

var deMonths = map[string]int{"jan": 1, "feb": 2, "mär": 3, "mar": 3, "apr": 4, "mai": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "okt": 10, "nov": 11, "dez": 12}

func plainCell(cell string) string {
	s := reBrTail.ReplaceAllString(cell, "")
	s = reSmall.ReplaceAllString(s, "")
	s = reRef.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "{{0}}", "")
	s = reTmpl.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1")
	s = reTag.ReplaceAllString(s, "")
	s = strings.ReplaceAll(strings.ReplaceAll(s, "&nbsp;", " "), "''", "")
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

func parseCellDate(cell string) string {
	if m := reDatum.FindStringSubmatch(cell); m != nil {
		return m[1]
	}
	if m := reDeDate.FindStringSubmatch(plainCell(cell)); m != nil {
		if mon, ok := deMonths[strings.ToLower(m[2])]; ok {
			var d, y int
			fmt.Sscanf(m[1], "%d", &d)
			fmt.Sscanf(m[3], "%d", &y)
			return time.Date(y, time.Month(mon), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		}
	}
	return ""
}

func teamsOf(cell string) []string {
	text := plainCell(reCrossBr.ReplaceAllString(cell, " / "))
	text = reGuest.ReplaceAllString(text, "")
	var out []string
	for _, t := range strings.Split(text, " / ") {
		if t = strings.Trim(t, " ,;"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseTable(text, heading string, orf bool) []Episode {
	start := strings.Index(text, heading)
	if start < 0 {
		return nil
	}
	t0 := strings.Index(text[start:], "{|")
	if t0 < 0 {
		return nil
	}
	t0 += start
	t1 := strings.Index(text[t0:], "\n|}")
	if t1 < 0 {
		return nil
	}
	var out []Episode
	for _, chunk := range reRowSplit.Split(text[t0:t0+t1], -1)[1:] {
		var cells []string
		for _, line := range strings.Split(strings.TrimSpace(chunk), "\n") {
			if strings.HasPrefix(line, "|") && !strings.HasPrefix(line, "|}") {
				cells = append(cells, strings.Split(line[1:], "||")...)
			} else if len(cells) > 0 {
				cells[len(cells)-1] += "\n" + line
			}
		}
		if len(cells) < 5 {
			continue
		}
		nr := plainCell(cells[0])
		if !reNr.MatchString(nr) {
			continue
		}
		out = append(out, Episode{
			Nr:        nr,
			Title:     plainCell(cells[1]),
			Sender:    plainCell(cells[2]),
			Date:      parseCellDate(cells[3]),
			Ermittler: teamsOf(cells[4]),
			ORF:       orf,
		})
	}
	return out
}

// ParseTatortWikitext wandelt den Wikitext der Wikipedia-Liste in einen Katalog.
func ParseTatortWikitext(text string) *Catalog {
	eps := parseTable(text, "=== Ausgestrahlte Folgen ===", false)
	eps = append(eps, parseTable(text, "== ORF-eigene Produktionen ==", true)...)
	return &Catalog{
		Source:     "https://de.wikipedia.org/wiki/Liste_der_Tatort-Folgen (CC BY-SA 4.0)",
		TMDBShowID: 3034,
		Episodes:   eps,
	}
}

// FetchTatort holt die aktuelle Wikipedia-Liste.
func FetchTatort(ctx context.Context) (*Catalog, error) {
	u := "https://de.wikipedia.org/w/api.php?" + url.Values{
		"action": {"parse"}, "page": {tatortPage}, "prop": {"wikitext"},
		"format": {"json"}, "formatversion": {"2"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoldfishTatortCatalog/1.0 (github.com/boernie77/goldfish)")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wikipedia %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var d struct {
		Parse struct {
			Wikitext string `json:"wikitext"`
		} `json:"parse"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return ParseTatortWikitext(d.Parse.Wikitext), nil
}

// UseFile lädt eine gespeicherte (neuere) Fassung aus dir/tatort.json, sofern
// sie mindestens so viele Folgen hat wie die aktive. Beim Start aufrufen.
func UseFile(dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, "tatort.json"))
	if err != nil {
		return
	}
	var c Catalog
	if json.Unmarshal(raw, &c) != nil || c.TMDBShowID == 0 {
		return
	}
	install(&c)
}

// Refresh holt die Liste neu, prüft sie auf Plausibilität, speichert sie unter
// dir/tatort.json und aktiviert sie. Liefert die Anzahl Folgen.
func Refresh(ctx context.Context, dir string) (int, error) {
	c, err := FetchTatort(ctx)
	if err != nil {
		return 0, err
	}
	if cur := ForShow(c.TMDBShowID); cur != nil && len(c.Episodes) < len(cur.Episodes)-5 {
		// Deutlich weniger Zeilen als bisher → Seite umgebaut/kaputt geparst:
		// lieber die bekannte Fassung behalten.
		return 0, fmt.Errorf("nur %d statt %d Folgen gelesen — Seite verändert?", len(c.Episodes), len(cur.Episodes))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(dir, "tatort.json"), raw, 0o644); err != nil {
		return 0, err
	}
	install(c)
	return len(c.Episodes), nil
}
