package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/tmdb"
)

// seriesSeasons liefert alle Staffeln + Folgen einer Serie, die in einem
// TV-Ordner liegt, aus TMDB-Sicht angereichert um den Status "owned" (auf
// Disk vorhanden) pro Episode. Episoden ohne airDate oder mit airDate in der
// Zukunft werden rausgefiltert — wir wollen nichts als "Fehlt" markieren,
// das noch gar nicht ausgestrahlt ist. Owned-Items sind davon ausgenommen
// (User hat die Datei → muss zählen, auch wenn TMDB-Datum komisch ist).
//
// Query:
//   libraryId=<libID>
//   folder=<top-level-folder>
//
// Beispiel-Antwort:
// {
//   "showTmdbId": 1234,
//   "seasons": [
//     {
//       "seasonNumber": 1,
//       "name": "Staffel 1",
//       "posterPath": "/…",
//       "episodes": [
//         { "season":1, "episode":1, "title":"...", "airDate":"2020-01-01",
//           "owned":true, "itemId":123, "hidden":false },
//         { "season":1, "episode":2, ..., "owned":false },
//       ]
//     }, ...
//   ]
// }
func (s *Server) seriesSeasons(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige library id")
		return
	}
	if !s.requireLibAccess(w, r, libID) {
		return
	}
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		writeError(w, 400, "folder nötig")
		return
	}

	owned, _, err := s.Store.SeriesOwnedEpisodes(libID, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	showTMDB, err := s.Store.ShowTMDBForFolder(libID, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// ?refresh=true invalidiert den TMDB-Cache für diese Show und erzwingt
	// einen frischen Fetch. Trigger: "↻ TMDB neu laden"-Button im Show-Header.
	if r.URL.Query().Get("refresh") == "true" && showTMDB > 0 && s.Enrich != nil {
		s.Enrich.Client().InvalidateShow(showTMDB)
	}
	if showTMDB == 0 || s.Enrich == nil || !s.Enrich.Client().Enabled() {
		// Kein TMDB-Match oder kein TMDB-Key → Frontend gruppiert die owned
		// Episoden client-seitig, ohne "Fehlt"-Einträge.
		writeJSON(w, 200, map[string]any{
			"showTmdbId": showTMDB,
			"seasons":    []any{},
		})
		return
	}

	// Kappe bei zuletzt vorhandener (season, episode): nichts darüber hinaus
	// laden, damit User nicht "zukünftige" Folgen als Missing sieht.
	// Doppelfolgen (EpisodeEnd > Episode) werden pro abgedeckter Episode als
	// eigener Slot im owned-Lookup gehalten; alle Slots zeigen auf dieselbe
	// ItemID.
	type ownedSlot struct {
		ItemID     int64
		ItemIDs    []int64 // alle Items, die diese Episode mappen (Duplikate / Varianten)
		EpisodeEnd int     // >0 wenn Slot Teil einer Range ist, sonst 0
	}
	maxSeason := 0
	haveSeasons := map[int]struct{}{}
	ownedLookup := map[int]map[int]ownedSlot{}
	for _, e := range owned {
		end := e.Episode
		if e.EpisodeEnd > e.Episode {
			end = e.EpisodeEnd
		}
		if e.Season > maxSeason {
			maxSeason = e.Season
		}
		haveSeasons[e.Season] = struct{}{}
		if _, ok := ownedLookup[e.Season]; !ok {
			ownedLookup[e.Season] = map[int]ownedSlot{}
		}
		for ep := e.Episode; ep <= end; ep++ {
			if existing, exists := ownedLookup[e.Season][ep]; exists {
				// Slot schon belegt → zusätzliche Datei als Varianten-Eintrag
				// anhängen, damit das Frontend ein Varianten-Dropdown bauen kann.
				if e.ItemID != existing.ItemID {
					existing.ItemIDs = append(existing.ItemIDs, e.ItemID)
					ownedLookup[e.Season][ep] = existing
				}
				continue
			}
			slot := ownedSlot{ItemID: e.ItemID, ItemIDs: []int64{e.ItemID}}
			if e.EpisodeEnd > e.Episode {
				slot.EpisodeEnd = e.EpisodeEnd
			}
			ownedLookup[e.Season][ep] = slot
		}
	}
	if maxSeason == 0 {
		writeJSON(w, 200, map[string]any{
			"showTmdbId": showTMDB,
			"seasons":    []any{},
		})
		return
	}

	// Pro Staffel, in der der User etwas hat, die TMDB-Daten fetchen.
	// Wir hängen die fehlenden Episoden bis maxSeason/maxEpisode an.
	ctx := r.Context()
	client := s.Enrich.Client()

	// Show-Detail + Cast für den Info-Header über den Staffeln.
	type castOut struct {
		TMDBID      int64  `json:"tmdbId"`
		Name        string `json:"name"`
		Character   string `json:"character"`
		ProfilePath string `json:"profilePath,omitempty"`
	}
	type showOut struct {
		Title            string    `json:"title"`
		OriginalName     string    `json:"originalName,omitempty"`
		Overview         string    `json:"overview,omitempty"`
		FirstAirDate     string    `json:"firstAirDate,omitempty"`
		LastAirDate      string    `json:"lastAirDate,omitempty"`
		Status           string    `json:"status,omitempty"`
		Rating           float64   `json:"rating,omitempty"`
		PosterPath       string    `json:"posterPath,omitempty"`
		BackdropPath     string    `json:"backdropPath,omitempty"`
		Genres           []string  `json:"genres,omitempty"`
		NumberOfSeasons  int       `json:"numberOfSeasons,omitempty"`
		NumberOfEpisodes int       `json:"numberOfEpisodes,omitempty"`
		Cast             []castOut `json:"cast,omitempty"`
	}
	// Show + Credits parallel holen — beide hängen am selben Rate-Limiter,
	// aber wenn einer der Slots gerade frei ist, spart das eine Round-Trip-
	// Zeit gegenüber seriellem Aufruf.
	var (
		tv         *tmdb.TVShow
		tvCredits  []tmdb.CastEntry
		tvWG       sync.WaitGroup
	)
	tvWG.Add(2)
	go func() {
		defer tvWG.Done()
		tv, _ = client.GetTV(ctx, showTMDB)
	}()
	go func() {
		defer tvWG.Done()
		tvCredits, _ = client.GetTVCredits(ctx, showTMDB)
	}()
	tvWG.Wait()

	var show *showOut
	if tv != nil {
		so := &showOut{
			Title:            tv.Name,
			OriginalName:     tv.OriginalName,
			Overview:         tv.Overview,
			FirstAirDate:     tv.FirstAirDate,
			LastAirDate:      tv.LastAirDate,
			Status:           tv.Status,
			Rating:           tv.VoteAverage,
			PosterPath:       tv.PosterPath,
			BackdropPath:     tv.BackdropPath,
			NumberOfSeasons:  tv.NumberOfSeasons,
			NumberOfEpisodes: tv.NumberOfEpisodes,
		}
		for _, g := range tv.Genres {
			so.Genres = append(so.Genres, g.Name)
		}
		for i, c := range tvCredits {
			if i >= 15 {
				break
			}
			so.Cast = append(so.Cast, castOut{
				TMDBID: c.ID, Name: c.Name, Character: c.Character, ProfilePath: c.ProfilePath,
			})
		}
		show = so
	}

	type episodeOut struct {
		Season     int     `json:"season"`
		Episode    int     `json:"episode"`
		Title      string  `json:"title"`
		Overview   string  `json:"overview,omitempty"`
		AirDate    string  `json:"airDate,omitempty"`
		StillPath  string  `json:"stillPath,omitempty"`
		Owned      bool    `json:"owned"`
		ItemID     int64   `json:"itemId,omitempty"`
		ItemIDs    []int64 `json:"itemIds,omitempty"` // alle Files für diese Episode (Duplikate / Varianten)
		TMDBID     int64   `json:"tmdbId,omitempty"`
		// EpisodeEnd ist gesetzt, wenn die Episode Teil einer Doppelfolgen-
		// Datei ist (z.B. S07E23E24.mkv). Slots E23 UND E24 zeigen dann auf
		// dieselbe ItemID, und EpisodeEnd=24 auf beiden.
		EpisodeEnd int `json:"episodeEnd,omitempty"`
	}
	type seasonOut struct {
		SeasonNumber int           `json:"seasonNumber"`
		Name         string        `json:"name"`
		PosterPath   string        `json:"posterPath,omitempty"`
		AirDate      string        `json:"airDate,omitempty"`
		Episodes     []episodeOut  `json:"episodes"`
		OwnedCount   int           `json:"ownedCount"`
		Total        int           `json:"total"`
	}

	// Alle Staffeln parallel holen — bei 10 Staffeln spart das leicht 2-3s
	// Latenz gegenüber seriellem Aufruf, selbst wenn der Rate-Limiter die
	// Requests intern serialisiert.
	type seasonResult struct {
		sn   int
		data *tmdb.Season
	}
	var seasonList []int
	for sn := 1; sn <= maxSeason; sn++ {
		if _, ok := haveSeasons[sn]; ok {
			seasonList = append(seasonList, sn)
		}
	}
	results := make([]seasonResult, len(seasonList))
	var seasonWG sync.WaitGroup
	seasonWG.Add(len(seasonList))
	for i, sn := range seasonList {
		go func(idx, seasonNum int) {
			defer seasonWG.Done()
			d, _ := client.GetSeason(ctx, showTMDB, seasonNum)
			results[idx] = seasonResult{sn: seasonNum, data: d}
		}(i, sn)
	}
	seasonWG.Wait()

	// Cap nach Air-Date statt nach „letzte vom User besessene Episode" — sonst
	// zeigt eine fertige Staffel mit 14 ausgestrahlten Episoden, von denen der
	// User nur 10 hat, „10/10" statt „10/14". Episoden ohne airDate oder mit
	// airDate in der Zukunft werden rausgefiltert; das deckt sowohl
	// unveröffentlichte Folgen laufender Shows als auch TMDB-Phantom-Einträge
	// ohne Sendetermin ab.
	now := time.Now()
	var seasons []seasonOut
	for _, res := range results {
		if res.data == nil {
			continue // TMDB-Fehler pro Staffel stumm tolerieren
		}
		season := res.data
		so := seasonOut{
			SeasonNumber: season.SeasonNumber,
			Name:         season.Name,
			PosterPath:   season.PosterPath,
			AirDate:      season.AirDate,
		}
		for _, ep := range season.Episodes {
			// Future-Episode-Filter: airDate leer ODER in der Zukunft → skip.
			// Owned-Items sind davon ausgenommen (User hat die Datei, also
			// soll sie auch zählen — TMDB-Datum ist evtl. einfach falsch).
			slot := ownedLookup[ep.SeasonNumber][ep.EpisodeNumber]
			if slot.ItemID == 0 {
				if ep.AirDate == "" {
					continue
				}
				if airTime, err := time.Parse("2006-01-02", ep.AirDate); err == nil && airTime.After(now) {
					continue
				}
			}
			out := episodeOut{
				Season:     ep.SeasonNumber,
				Episode:    ep.EpisodeNumber,
				Title:      ep.Name,
				Overview:   ep.Overview,
				AirDate:    ep.AirDate,
				StillPath:  ep.StillPath,
				Owned:      slot.ItemID > 0,
				ItemID:     slot.ItemID,
				ItemIDs:    slot.ItemIDs,
				EpisodeEnd: slot.EpisodeEnd,
				TMDBID:     ep.ID,
			}
			so.Episodes = append(so.Episodes, out)
			so.Total++
			if slot.ItemID > 0 {
				so.OwnedCount++
			}
		}
		seasons = append(seasons, so)
	}
	writeJSON(w, 200, map[string]any{
		"showTmdbId": showTMDB,
		"show":       show,
		"seasons":    seasons,
	})
}
