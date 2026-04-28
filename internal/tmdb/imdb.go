package tmdb

import (
	"context"
	"log"
	"net/url"
)

// FindByIMDb löst eine IMDb-ID ("tt1234567") zu TMDB-Movie oder -TV auf.
// Nutzt /find mit external_source=imdb_id. Ein Treffer ist in exakt einer Kategorie.
func (c *Client) FindByIMDb(ctx context.Context, imdbID string) (*SearchResult, error) {
	if imdbID == "" {
		return nil, nil
	}
	type resp struct {
		MovieResults []struct {
			ID            int64   `json:"id"`
			Title         string  `json:"title"`
			OriginalTitle string  `json:"original_title"`
			ReleaseDate   string  `json:"release_date"`
			Overview      string  `json:"overview"`
			VoteAverage   float64 `json:"vote_average"`
			PosterPath    string  `json:"poster_path"`
			BackdropPath  string  `json:"backdrop_path"`
		} `json:"movie_results"`
		TVResults []struct {
			ID           int64   `json:"id"`
			Name         string  `json:"name"`
			OriginalName string  `json:"original_name"`
			FirstAirDate string  `json:"first_air_date"`
			Overview     string  `json:"overview"`
			VoteAverage  float64 `json:"vote_average"`
			PosterPath   string  `json:"poster_path"`
			BackdropPath string  `json:"backdrop_path"`
		} `json:"tv_results"`
		TVEpisodeResults []struct {
			ShowID        int64  `json:"show_id"`
			SeasonNumber  int    `json:"season_number"`
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
		} `json:"tv_episode_results"`
		TVSeasonResults []struct {
			ShowID       int64 `json:"show_id"`
			SeasonNumber int   `json:"season_number"`
		} `json:"tv_season_results"`
	}
	var r resp
	params := url.Values{}
	params.Set("external_source", "imdb_id")
	if err := c.get(ctx, "/find/"+imdbID, params, &r); err != nil {
		return nil, err
	}
	log.Printf("[tmdb.FindByIMDb] %s -> movies=%d tv=%d episodes=%d seasons=%d",
		imdbID, len(r.MovieResults), len(r.TVResults), len(r.TVEpisodeResults), len(r.TVSeasonResults))
	if len(r.MovieResults) > 0 {
		m := r.MovieResults[0]
		return &SearchResult{
			TMDBType: "movie", ID: m.ID, Title: m.Title, OriginalTitle: m.OriginalTitle,
			Year: yearFromDate(m.ReleaseDate), Overview: m.Overview, Rating: m.VoteAverage,
			PosterPath: m.PosterPath, BackdropPath: m.BackdropPath,
		}, nil
	}
	if len(r.TVResults) > 0 {
		t := r.TVResults[0]
		return &SearchResult{
			TMDBType: "tv", ID: t.ID, Title: t.Name, OriginalTitle: t.OriginalName,
			Year: yearFromDate(t.FirstAirDate), Overview: t.Overview, Rating: t.VoteAverage,
			PosterPath: t.PosterPath, BackdropPath: t.BackdropPath,
		}, nil
	}
	// Bei Episoden-IDs (z. B. TV-Reihen, deren einzelne Folgen je eine eigene
	// IMDb-tt-Nummer haben) liefert TMDB den Treffer unter tv_episode_results
	// mit show_id. Wir mappen das auf die Parent-Show — der Folder-Match-
	// Workflow lädt sich danach via GetTV(showID) den Rest, und der
	// Episoden-Enrichment-Pass matcht die einzelnen Folgen anschließend.
	if len(r.TVEpisodeResults) > 0 {
		e := r.TVEpisodeResults[0]
		return &SearchResult{TMDBType: "tv", ID: e.ShowID}, nil
	}
	if len(r.TVSeasonResults) > 0 {
		s := r.TVSeasonResults[0]
		return &SearchResult{TMDBType: "tv", ID: s.ShowID}, nil
	}
	return nil, nil
}
