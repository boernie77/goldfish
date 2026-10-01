package enrich

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/catalog"
	"github.com/boernie77/goldfish/internal/tmdb"
)

// Zuordnung neuer Tatort-Folgen beim Scannen (seit 1.4.71, User-Frage
// 2026-10-01: „Wie ist das bei neuen Tatortfolgen?"). Tatort-Dateien heißen
// `Tatort.S<Jahr>E<fortlaufende Nr>.<Ermittler>.<Titel>.ext`; TMDB führt den
// Tatort aber mit einer Staffel pro Jahr (S1 = 1970) und Folgen ab 1. Der
// normale Weg (S2026E1341 → GetEpisode) findet deshalb nie etwas. Für Serien
// mit Katalog (internal/catalog) sucht dieser Weg die Folge über Jahr + Titel.
var yearNrFileRe = regexp.MustCompile(`(?i)S(\d{4})E0*(\d+[a-z]?)\.(.+)\.[^.]+$`)

// catalogEpisode liefert (Staffel, Folge) bei TMDB oder ok=false. Gilt nur für
// Serien, zu denen es einen Katalog gibt, und Dateien im Jahr/Nummer-Schema.
func (w *Worker) catalogEpisode(ctx context.Context, showTMDBID int64, path string) (int, int, bool) {
	cat := catalog.ForShow(showTMDBID)
	if cat == nil {
		return 0, 0, false
	}
	m := yearNrFileRe.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, 0, false
	}
	year, _ := strconv.Atoi(m[1])
	nr := strings.ToLower(m[2])
	// Titel-Kandidaten: Katalog-Titel (Wikipedia, sauber geschrieben) und alle
	// Endstücke des Dateinamens („Faber.Bönisch.Dalay.und.Kossik.Hydra" →
	// „Hydra", „Kossik Hydra", …) — der Ermittler-Teil davor ist unbekannt.
	want := map[string]bool{}
	var aired time.Time
	if e := cat.ByNr(nr); e != nil {
		want[catalog.NormTitle(e.Title)] = true
		aired, _ = time.Parse("2006-01-02", e.Date)
		if !aired.IsZero() {
			year = aired.Year()
		}
	}
	toks := strings.FieldsFunc(m[3], func(r rune) bool { return r == '.' || r == ' ' || r == '_' })
	for k := 1; k <= len(toks); k++ {
		if t := catalog.NormTitle(strings.Join(toks[len(toks)-k:], " ")); t != "" {
			want[t] = true
		}
	}
	for _, sn := range []int{year - 1969, year - 1970, year - 1968} {
		if sn < 1 {
			continue
		}
		season, err := w.client.GetSeason(ctx, showTMDBID, sn)
		if err != nil || season == nil {
			continue
		}
		for _, ep := range season.Episodes {
			if want[catalog.NormTitle(ep.Name)] {
				return ep.SeasonNumber, ep.EpisodeNumber, true
			}
		}
		// Rückfall: Titel weicht ab, aber Erstausstrahlung (Katalog) passt auf den Tag.
		if !aired.IsZero() {
			for _, ep := range season.Episodes {
				if d := tmdb.ParseDate(ep.AirDate); !d.IsZero() && d.Equal(aired) {
					return ep.SeasonNumber, ep.EpisodeNumber, true
				}
			}
		}
	}
	return 0, 0, false
}
