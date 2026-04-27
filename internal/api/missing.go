package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/tmdb"
)

// missingMovies liefert alle Collection-Parts, die der User nicht besitzt und
// nicht ausgeblendet hat. Format ist abhängig vom Accept-Header bzw. dem
// `format`-Query-Param: "csv" → CSV-Download, sonst JSON.
//
// Der CSV-Export ist Radarr-kompatibel: erste Spalte = TMDB-ID, dann Titel,
// Jahr und Sammlung. Radarr kann TMDB-IDs direkt importieren.
func (s *Server) missingMovies(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	rows, err := s.Store.ListMissingMovies(me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		writeMissingCSV(w, "fehlende-filme.csv",
			[]string{"tmdb_id", "title", "release_date", "collection"},
			func() [][]string {
				out := make([][]string, 0, len(rows))
				for _, m := range rows {
					out = append(out, []string{
						strconv.FormatInt(m.TMDBMovieID, 10),
						m.Title, m.ReleaseDate, m.CollectionName,
					})
				}
				return out
			}())
		return
	}
	if rows == nil {
		rows = []store.MissingMovieRow{}
	}
	writeJSON(w, 200, rows)
}

// missingEpisodesEntry: ein einzelner fehlender Folgen-Eintrag.
type missingEpisodesEntry struct {
	ShowTMDBID int64  `json:"showTmdbId"`
	ShowTitle  string `json:"showTitle"`
	Folder     string `json:"folder"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	Title      string `json:"title"`
	AirDate    string `json:"airDate,omitempty"`
}

// missingEpisodes durchsucht eine TV-Library nach Show-Ordnern, ermittelt pro
// Show die fehlenden Folgen via TMDB-Season-Daten und gibt das Ergebnis als
// JSON oder CSV zurück. Das macht TMDB-Calls — pro Show + Season einer.
//
// Query: libraryId=N (TV-Library-ID, Pflicht) [&format=csv]
func (s *Server) missingEpisodes(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	libID, err := strconv.ParseInt(r.URL.Query().Get("libraryId"), 10, 64)
	if err != nil || libID == 0 {
		writeError(w, 400, "libraryId nötig")
		return
	}
	if !s.requireLibAccess(w, r, libID) {
		return
	}
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB nicht konfiguriert")
		return
	}

	folders, err := s.Store.ListTVFoldersForLibrary(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	client := s.Enrich.Client()

	// Pro Show parallel arbeiten, aber moderat (max 4 gleichzeitig), damit
	// wir TMDBs Rate-Limiter (35 req/10s) nicht überfahren.
	type result struct {
		entries []missingEpisodesEntry
	}
	results := make([]result, len(folders))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	for i, folder := range folders {
		i, folder := i, folder
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			entries := collectMissingEpisodesForFolder(ctx, s.Store, client, libID, folder)
			results[i] = result{entries: entries}
		}()
	}
	wg.Wait()

	all := make([]missingEpisodesEntry, 0, 64)
	for _, r := range results {
		all = append(all, r.entries...)
	}

	if r.URL.Query().Get("format") == "csv" {
		writeMissingCSV(w, "fehlende-folgen.csv",
			[]string{"show_tmdb_id", "show_title", "season", "episode", "title", "air_date", "folder"},
			func() [][]string {
				out := make([][]string, 0, len(all))
				for _, e := range all {
					out = append(out, []string{
						strconv.FormatInt(e.ShowTMDBID, 10),
						e.ShowTitle,
						strconv.Itoa(e.Season),
						strconv.Itoa(e.Episode),
						e.Title, e.AirDate, e.Folder,
					})
				}
				return out
			}())
		return
	}
	writeJSON(w, 200, all)
}

// collectMissingEpisodesForFolder kapselt die Logik aus seriesSeasons:
// owned-Episoden lesen, max(season,episode) bestimmen, TMDB-Season-Daten
// laden und alle nicht-owned Folgen bis zum Cap als „fehlt" zurückgeben.
func collectMissingEpisodesForFolder(ctx context.Context, st *store.Store, client *tmdb.Client,
	libID int64, folder string) []missingEpisodesEntry {
	owned, _, err := st.SeriesOwnedEpisodes(libID, folder)
	if err != nil || len(owned) == 0 {
		return nil
	}
	showTMDB, err := st.ShowTMDBForFolder(libID, folder)
	if err != nil || showTMDB == 0 {
		return nil
	}
	maxSeason, maxEpisode := 0, 0
	owns := map[int]map[int]bool{}
	for _, e := range owned {
		end := e.Episode
		if e.EpisodeEnd > e.Episode {
			end = e.EpisodeEnd
		}
		if e.Season > maxSeason || (e.Season == maxSeason && end > maxEpisode) {
			maxSeason, maxEpisode = e.Season, end
		}
		if owns[e.Season] == nil {
			owns[e.Season] = map[int]bool{}
		}
		for ep := e.Episode; ep <= end; ep++ {
			owns[e.Season][ep] = true
		}
	}
	if maxSeason == 0 {
		return nil
	}
	tv, err := client.GetTV(ctx, showTMDB)
	showTitle := folder
	if err == nil && tv != nil && tv.Name != "" {
		showTitle = tv.Name
	}

	out := []missingEpisodesEntry{}
	for sn := 1; sn <= maxSeason; sn++ {
		if owns[sn] == nil {
			continue // Staffel komplett unbekannt → User scheint sie nicht zu wollen
		}
		season, err := client.GetSeason(ctx, showTMDB, sn)
		if err != nil || season == nil {
			continue
		}
		for _, ep := range season.Episodes {
			if sn == maxSeason && ep.EpisodeNumber > maxEpisode {
				continue
			}
			if owns[sn][ep.EpisodeNumber] {
				continue
			}
			out = append(out, missingEpisodesEntry{
				ShowTMDBID: showTMDB,
				ShowTitle:  showTitle,
				Folder:     folder,
				Season:     ep.SeasonNumber,
				Episode:    ep.EpisodeNumber,
				Title:      ep.Name,
				AirDate:    ep.AirDate,
			})
		}
	}
	return out
}

// writeMissingCSV schreibt die Tabelle als CSV-Download.
func writeMissingCSV(w http.ResponseWriter, filename string, header []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	cw := csv.NewWriter(w)
	cw.Comma = ';' // Excel-DE freundlich
	_ = cw.Write(header)
	for _, row := range rows {
		_ = cw.Write(row)
	}
	cw.Flush()
}
