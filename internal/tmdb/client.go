// Package tmdb ist ein minimaler Client für die TMDB v3 API.
// Rate-Limit: 40 req/10s (Free Tier). Wir bleiben mit 35 unter der Grenze.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	baseURL       = "https://api.themoviedb.org/3"
	posterBase    = "https://image.tmdb.org/t/p"
	requestBudget = 35             // max Requests pro Fenster
	windowSize    = 10 * time.Second
)

type Client struct {
	apiKey   string
	language string
	hc       *http.Client

	mu       sync.Mutex
	reqTimes []time.Time

	// In-Memory-Cache mit TTL für Hot-Path-Reads (GetTV / GetTVCredits /
	// GetSeason). Reduziert Round-Trips wenn der User dieselbe Serie/Staffel
	// mehrfach öffnet, und entkoppelt User-Klicks vom Enricher-Backlog, der
	// denselben Rate-Limiter teilt.
	cacheMu sync.RWMutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	expiresAt time.Time
	value     any
}

// cacheTTL: Stammdaten ändern sich bei TMDB sehr selten, 15 min reichen.
const cacheTTL = 15 * time.Minute

func New(apiKey, language string) *Client {
	if language == "" {
		language = "de-DE"
	}
	return &Client{
		apiKey:   apiKey,
		language: language,
		hc:       &http.Client{Timeout: 20 * time.Second},
		cache:    map[string]cacheEntry{},
	}
}

// cacheGet liest einen Eintrag aus dem TTL-Cache. Abgelaufene Einträge werden
// als miss behandelt (aber nicht proaktiv gelöscht — Cleanup beim nächsten Put).
func (c *Client) cacheGet(key string) (any, bool) {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.value, true
}

// InvalidateShow löscht alle gecachten Einträge rund um eine Serie:
// Show-Details, Show-Credits und alle Staffel-Daten. Wird vom
// "TMDB neu laden"-Button in der Staffel-Ansicht aufgerufen.
func (c *Client) InvalidateShow(showID int64) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	prefixes := []string{
		fmt.Sprintf("tv:%d:", showID),
		fmt.Sprintf("tvcredits:%d:", showID),
		fmt.Sprintf("season:%d:", showID),
	}
	for k := range c.cache {
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				delete(c.cache, k)
				break
			}
		}
	}
}

func (c *Client) cachePut(key string, value any) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	// Billiger Cleanup: wenn der Cache zu groß wird, abgelaufene Einträge raus.
	if len(c.cache) > 500 {
		now := time.Now()
		for k, e := range c.cache {
			if now.After(e.expiresAt) {
				delete(c.cache, k)
			}
		}
	}
	c.cache[key] = cacheEntry{value: value, expiresAt: time.Now().Add(cacheTTL)}
}

// Enabled zeigt an, ob ein API-Key hinterlegt ist.
func (c *Client) Enabled() bool { return c.apiKey != "" }

// waitForSlot blockiert bis Platz im Rate-Limit-Fenster frei ist.
func (c *Client) waitForSlot(ctx context.Context) error {
	for {
		c.mu.Lock()
		now := time.Now()
		cut := now.Add(-windowSize)
		fresh := c.reqTimes[:0]
		for _, t := range c.reqTimes {
			if t.After(cut) {
				fresh = append(fresh, t)
			}
		}
		c.reqTimes = fresh
		if len(c.reqTimes) < requestBudget {
			c.reqTimes = append(c.reqTimes, now)
			c.mu.Unlock()
			return nil
		}
		wait := windowSize - now.Sub(c.reqTimes[0])
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait + 100*time.Millisecond):
		}
	}
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	if !c.Enabled() {
		return errors.New("TMDB: kein API-Key konfiguriert")
	}
	if err := c.waitForSlot(ctx); err != nil {
		return err
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)
	// Caller-gesetzte language NICHT ueberschreiben — sonst kann ein
	// English-Fallback-Call nicht den de-Default umgehen.
	if params.Get("language") == "" {
		params.Set("language", c.language)
	}
	u := baseURL + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 429 {
		// Sollte unter 40 req/10s nicht passieren; wenn doch, kurz warten und 1x retry
		time.Sleep(5 * time.Second)
		return c.get(ctx, path, params, out)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("TMDB %s %s: %s", req.Method, path, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// --- Modelle ---

type Movie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	ReleaseDate   string  `json:"release_date"` // "2025-01-23"
	Overview      string  `json:"overview"`
	VoteAverage   float64 `json:"vote_average"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Runtime       int     `json:"runtime"`
	Genres        []Genre `json:"genres"`
	IMDBID        string  `json:"imdb_id"`
	BelongsToCollection *Collection `json:"belongs_to_collection,omitempty"`
}

// Collection beschreibt eine TMDB-Sammlung (z. B. James Bond, Star Wars),
// zu der ein Film gehört.
type Collection struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

// CollectionPart ist ein einzelner Eintrag in einer TMDB-Sammlung
// (Film mit Titel/Poster/Release-Datum).
type CollectionPart struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Overview     string `json:"overview"`
	ReleaseDate  string `json:"release_date"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

// CollectionDetail ist die Antwort von /collection/{id} inklusive aller Parts.
type CollectionDetail struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Overview     string           `json:"overview"`
	PosterPath   string           `json:"poster_path"`
	BackdropPath string           `json:"backdrop_path"`
	Parts        []CollectionPart `json:"parts"`
}

// GetCollection lädt die komplette Sammlungs-Info inklusive aller Parts.
func (c *Client) GetCollection(ctx context.Context, id int64) (*CollectionDetail, error) {
	var cd CollectionDetail
	if err := c.get(ctx, fmt.Sprintf("/collection/%d", id), nil, &cd); err != nil {
		return nil, err
	}
	return &cd, nil
}

type TVShow struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	OriginalName     string  `json:"original_name"`
	FirstAirDate     string  `json:"first_air_date"`
	LastAirDate      string  `json:"last_air_date"`
	Overview         string  `json:"overview"`
	VoteAverage      float64 `json:"vote_average"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	EpisodeRuntime   []int   `json:"episode_run_time"`
	Genres           []Genre `json:"genres"`
	Status           string  `json:"status"`
	NumberOfSeasons  int     `json:"number_of_seasons"`
	NumberOfEpisodes int     `json:"number_of_episodes"`
	Seasons          []TVShowSeason `json:"seasons"`
}

// TVShowSeason ist die kompakte Staffel-Info wie sie in /tv/{id}.seasons[]
// kommt — ohne die gesamten Episoden-Listen (nur SeasonNumber + EpisodeCount).
type TVShowSeason struct {
	SeasonNumber int    `json:"season_number"`
	EpisodeCount int    `json:"episode_count"`
	Name         string `json:"name"`
	AirDate      string `json:"air_date"`
}

type Episode struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	VoteAverage   float64 `json:"vote_average"`
	StillPath     string  `json:"still_path"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	Runtime       int     `json:"runtime"`
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type searchMovieResponse struct {
	Results []struct {
		ID            int64   `json:"id"`
		Title         string  `json:"title"`
		OriginalTitle string  `json:"original_title"`
		ReleaseDate   string  `json:"release_date"`
		Overview      string  `json:"overview"`
		VoteAverage   float64 `json:"vote_average"`
		PosterPath    string  `json:"poster_path"`
		BackdropPath  string  `json:"backdrop_path"`
		Popularity    float64 `json:"popularity"`
	} `json:"results"`
}

type searchTVResponse struct {
	Results []struct {
		ID           int64   `json:"id"`
		Name         string  `json:"name"`
		OriginalName string  `json:"original_name"`
		FirstAirDate string  `json:"first_air_date"`
		Overview     string  `json:"overview"`
		VoteAverage  float64 `json:"vote_average"`
		PosterPath   string  `json:"poster_path"`
		BackdropPath string  `json:"backdrop_path"`
		Popularity   float64 `json:"popularity"`
	} `json:"results"`
}

// SearchResult ist ein vereinheitlichtes Suchergebnis.
type SearchResult struct {
	TMDBType     string  `json:"tmdbType"` // "movie" | "tv"
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Year         int     `json:"year"`
	Overview     string  `json:"overview"`
	Rating       float64 `json:"rating"`
	PosterPath   string  `json:"posterPath"`
	BackdropPath string  `json:"backdropPath"`
}

// SearchMovie führt eine Filmsuche aus. year ist optional (0 = egal).
func (c *Client) SearchMovie(ctx context.Context, title string, year int) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("query", title)
	params.Set("include_adult", "false")
	if year > 0 {
		params.Set("primary_release_year", strconv.Itoa(year))
	}
	var r searchMovieResponse
	if err := c.get(ctx, "/search/movie", params, &r); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(r.Results))
	for _, m := range r.Results {
		out = append(out, SearchResult{
			TMDBType: "movie", ID: m.ID, Title: m.Title, OriginalTitle: m.OriginalTitle,
			Year: yearFromDate(m.ReleaseDate), Overview: m.Overview, Rating: m.VoteAverage,
			PosterPath: m.PosterPath, BackdropPath: m.BackdropPath,
		})
	}
	return out, nil
}

// SearchTV sucht nach Serien.
func (c *Client) SearchTV(ctx context.Context, title string) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("query", title)
	params.Set("include_adult", "false")
	var r searchTVResponse
	if err := c.get(ctx, "/search/tv", params, &r); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(r.Results))
	for _, t := range r.Results {
		out = append(out, SearchResult{
			TMDBType: "tv", ID: t.ID, Title: t.Name, OriginalTitle: t.OriginalName,
			Year: yearFromDate(t.FirstAirDate), Overview: t.Overview, Rating: t.VoteAverage,
			PosterPath: t.PosterPath, BackdropPath: t.BackdropPath,
		})
	}
	return out, nil
}

// GetMovie holt Detaildaten inkl. Laufzeit/IMDb-ID.
func (c *Client) GetMovie(ctx context.Context, id int64) (*Movie, error) {
	var m Movie
	if err := c.get(ctx, "/movie/"+strconv.FormatInt(id, 10), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// PosterImage: ein einzelnes Poster aus TMDB-Images.
type PosterImage struct {
	FilePath    string  `json:"filePath"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Language    string  `json:"language,omitempty"` // ISO 639-1 ("de", "en", "")
	VoteAverage float64 `json:"voteAverage,omitempty"`
}

// MoviePosters liefert alle Poster-Varianten eines TMDB-Films, sortiert
// (DE > EN > sprachneutral, dann nach Rating).
func (c *Client) MoviePosters(ctx context.Context, id int64) ([]PosterImage, error) {
	return c.fetchPosters(ctx, fmt.Sprintf("/movie/%d/images", id))
}

// TVPosters liefert alle Poster-Varianten einer Serie.
func (c *Client) TVPosters(ctx context.Context, id int64) ([]PosterImage, error) {
	return c.fetchPosters(ctx, fmt.Sprintf("/tv/%d/images", id))
}

func (c *Client) fetchPosters(ctx context.Context, path string) ([]PosterImage, error) {
	var resp struct {
		Posters []struct {
			FilePath    string  `json:"file_path"`
			Width       int     `json:"width"`
			Height      int     `json:"height"`
			Language    string  `json:"iso_639_1"`
			VoteAverage float64 `json:"vote_average"`
		} `json:"posters"`
	}
	// Wichtig: include_image_language=de,en,null UND language="" — sonst
	// filtert TMDB nach c.language (z.B. "de-DE") und liefert nur DE-Poster.
	params := url.Values{}
	params.Set("include_image_language", "de,en,null")
	params.Set("language", "") // overrides default
	if err := c.get(ctx, path, params, &resp); err != nil {
		return nil, err
	}
	out := make([]PosterImage, 0, len(resp.Posters))
	for _, p := range resp.Posters {
		out = append(out, PosterImage{
			FilePath: p.FilePath, Width: p.Width, Height: p.Height,
			Language: p.Language, VoteAverage: p.VoteAverage,
		})
	}
	// Sortierung: DE zuerst, dann EN, dann sprachneutral; innerhalb gleicher
	// Sprache nach Vote-Average absteigend.
	rank := func(lang string) int {
		switch lang {
		case "de":
			return 0
		case "en":
			return 1
		case "":
			return 2
		}
		return 3
	}
	sortPosters(out, func(a, b PosterImage) bool {
		ra, rb := rank(a.Language), rank(b.Language)
		if ra != rb {
			return ra < rb
		}
		return a.VoteAverage > b.VoteAverage
	})
	return out, nil
}

// sortPosters: kleiner stable-sort-Helper, damit wir nicht "sort" extra importieren.
func sortPosters(s []PosterImage, less func(a, b PosterImage) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// MovieAgeRatingDE holt aus TMDBs `/movie/{id}/release_dates` die deutsche
// Altersfreigabe. Liefert "" wenn nicht gesetzt.
// TMDB-Werte sind typischerweise "0" / "6" / "12" / "16" / "18".
func (c *Client) MovieAgeRatingDE(ctx context.Context, id int64) (string, error) {
	var resp struct {
		Results []struct {
			ISO31661     string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	}
	if err := c.get(ctx, fmt.Sprintf("/movie/%d/release_dates", id), nil, &resp); err != nil {
		return "", err
	}
	for _, r := range resp.Results {
		if r.ISO31661 != "DE" {
			continue
		}
		for _, rd := range r.ReleaseDates {
			cert := strings.TrimSpace(rd.Certification)
			if cert != "" {
				return normalizeFSK(cert), nil
			}
		}
	}
	return "", nil
}

// TVAgeRatingDE holt aus TMDBs `/tv/{id}/content_ratings` die deutsche FSK.
func (c *Client) TVAgeRatingDE(ctx context.Context, id int64) (string, error) {
	var resp struct {
		Results []struct {
			ISO31661 string `json:"iso_3166_1"`
			Rating   string `json:"rating"`
		} `json:"results"`
	}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/content_ratings", id), nil, &resp); err != nil {
		return "", err
	}
	for _, r := range resp.Results {
		if r.ISO31661 == "DE" {
			return normalizeFSK(strings.TrimSpace(r.Rating)), nil
		}
	}
	return "", nil
}

// normalizeFSK bringt TMDB-Cert-Strings auf unsere Standard-Werte (0/6/12/16/18).
// TMDB liefert für DE meistens schon das nackte Numerische, manchmal aber auch
// "FSK 0" o. ä.
func normalizeFSK(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// "FSK 12" → "12"
	s = strings.TrimPrefix(strings.ToUpper(s), "FSK")
	s = strings.TrimSpace(s)
	switch s {
	case "0", "6", "12", "16", "18":
		return s
	}
	return ""
}

// GetTV holt Detaildaten einer Serie.
func (c *Client) GetTV(ctx context.Context, id int64) (*TVShow, error) {
	key := fmt.Sprintf("tv:%d:%s", id, c.language)
	if v, ok := c.cacheGet(key); ok {
		return v.(*TVShow), nil
	}
	var t TVShow
	if err := c.get(ctx, "/tv/"+strconv.FormatInt(id, 10), nil, &t); err != nil {
		return nil, err
	}
	c.cachePut(key, &t)
	return &t, nil
}

// GetSeason holt eine komplette Staffel einer Serie (inkl. aller Episoden).
type Season struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	SeasonNumber int       `json:"season_number"`
	AirDate      string    `json:"air_date"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []Episode `json:"episodes"`
}

func (c *Client) GetSeason(ctx context.Context, showID int64, season int) (*Season, error) {
	key := fmt.Sprintf("season:%d:%d:%s", showID, season, c.language)
	if v, ok := c.cacheGet(key); ok {
		return v.(*Season), nil
	}
	var s Season
	path := fmt.Sprintf("/tv/%d/season/%d", showID, season)
	if err := c.get(ctx, path, nil, &s); err != nil {
		return nil, err
	}
	// English-Fallback fuer generische/leere Episodentitel (z.B.
	// „Folge 1, Folge 2 ..." wenn TMDB keine deutschen Uebersetzungen
	// hat). Nur ausloesen wenn der Default-Lang nicht eh schon Englisch
	// ist und tatsaechlich generische Eintraege vorkommen.
	needsFallback := false
	if !strings.HasPrefix(c.language, "en") {
		for _, ep := range s.Episodes {
			if isGenericEpisodeName(ep.Name, ep.EpisodeNumber) || ep.Overview == "" {
				needsFallback = true
				break
			}
		}
	}
	if needsFallback {
		var en Season
		params := url.Values{}
		params.Set("language", "en-US")
		if err := c.get(ctx, path, params, &en); err == nil {
			enByEp := make(map[int]Episode, len(en.Episodes))
			for _, e := range en.Episodes {
				enByEp[e.EpisodeNumber] = e
			}
			for i, ep := range s.Episodes {
				if eng, ok := enByEp[ep.EpisodeNumber]; ok {
					if isGenericEpisodeName(ep.Name, ep.EpisodeNumber) &&
						eng.Name != "" &&
						!isGenericEpisodeName(eng.Name, eng.EpisodeNumber) {
						s.Episodes[i].Name = eng.Name
					}
					if ep.Overview == "" && eng.Overview != "" {
						s.Episodes[i].Overview = eng.Overview
					}
				}
			}
		}
	}
	c.cachePut(key, &s)
	return &s, nil
}

// isGenericEpisodeName erkennt TMDB-generische Episoden-Titel die kein
// echter Folgentitel sind, sondern nur das Fallback-Pattern „Folge N",
// „Episode N", „Episodio N" o. ae.
func isGenericEpisodeName(name string, epNum int) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return true
	}
	// Spracheabhaengige Defaults von TMDB
	for _, prefix := range []string{"Folge ", "Episode ", "Episodio ", "Épisode "} {
		if n == fmt.Sprintf("%s%d", prefix, epNum) {
			return true
		}
	}
	return false
}

// GetEpisode holt eine einzelne Episode (TV-Show-ID + Season + Episode).
// Macht einen English-Fallback-Call wenn der Default-Lang nicht Englisch ist
// UND TMDB einen generischen Namen („Folge N") oder leeres Overview liefert.
func (c *Client) GetEpisode(ctx context.Context, showID int64, season, episode int) (*Episode, error) {
	var e Episode
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", showID, season, episode)
	if err := c.get(ctx, path, nil, &e); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(c.language, "en") &&
		(isGenericEpisodeName(e.Name, e.EpisodeNumber) || e.Overview == "") {
		var en Episode
		params := url.Values{}
		params.Set("language", "en-US")
		if err := c.get(ctx, path, params, &en); err == nil {
			if isGenericEpisodeName(e.Name, e.EpisodeNumber) &&
				en.Name != "" &&
				!isGenericEpisodeName(en.Name, en.EpisodeNumber) {
				e.Name = en.Name
			}
			if e.Overview == "" && en.Overview != "" {
				e.Overview = en.Overview
			}
		}
	}
	return &e, nil
}

// CastEntry beschreibt einen einzelnen Schauspieler-Eintrag aus TMDB-Credits.
type CastEntry struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	Order       int    `json:"order"`
	ProfilePath string `json:"profile_path"`
}

type movieCreditsResponse struct {
	Cast []CastEntry `json:"cast"`
}

type tvCreditsResponse struct {
	Cast []CastEntry `json:"cast"`
}

type episodeCreditsResponse struct {
	Cast       []CastEntry `json:"cast"`
	GuestStars []CastEntry `json:"guest_stars"`
}

// GetMovieCredits holt die Cast-Liste eines Films.
func (c *Client) GetMovieCredits(ctx context.Context, movieID int64) ([]CastEntry, error) {
	var r movieCreditsResponse
	if err := c.get(ctx, fmt.Sprintf("/movie/%d/credits", movieID), nil, &r); err != nil {
		return nil, err
	}
	return r.Cast, nil
}

// GetTVCredits holt die Cast-Liste (aggregierte Show-Credits) einer Serie.
func (c *Client) GetTVCredits(ctx context.Context, tvID int64) ([]CastEntry, error) {
	key := fmt.Sprintf("tvcredits:%d:%s", tvID, c.language)
	if v, ok := c.cacheGet(key); ok {
		return v.([]CastEntry), nil
	}
	var r tvCreditsResponse
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/credits", tvID), nil, &r); err != nil {
		return nil, err
	}
	c.cachePut(key, r.Cast)
	return r.Cast, nil
}

// GetEpisodeCredits holt Cast + Guest-Stars einer Episode.
func (c *Client) GetEpisodeCredits(ctx context.Context, tvID int64, season, episode int) ([]CastEntry, []CastEntry, error) {
	var r episodeCreditsResponse
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d/credits", tvID, season, episode)
	if err := c.get(ctx, path, nil, &r); err != nil {
		return nil, nil, err
	}
	return r.Cast, r.GuestStars, nil
}

// PosterURL liefert die vollständige URL zu einem TMDB-Bildpfad (posterPath / stillPath / backdropPath).
// Wenn `path` bereits eine absolute URL ist (OMDb liefert volle URLs), wird sie unverändert zurückgegeben.
func PosterURL(path, size string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if size == "" {
		size = "w342"
	}
	return posterBase + "/" + size + path
}

// DownloadPoster lädt ein TMDB-Bild herunter und gibt die Bytes zurück.
func (c *Client) DownloadPoster(ctx context.Context, path, size string) ([]byte, string, error) {
	if path == "" {
		return nil, "", errors.New("kein Pfad")
	}
	u := PosterURL(path, size)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("poster %s: HTTP %d", u, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, "", err
	}
	return b, resp.Header.Get("Content-Type"), nil
}

// --- Helpers ---

func yearFromDate(s string) int {
	if len(s) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(s[:4])
	return y
}

// ParseDate konvertiert "YYYY-MM-DD" zu time.Time.
func ParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// GenresString rendert eine Genre-Liste als JSON-Array (für DB).
func GenresString(gs []Genre) string {
	if len(gs) == 0 {
		return "[]"
	}
	names := make([]string, 0, len(gs))
	for _, g := range gs {
		names = append(names, g.Name)
	}
	b, _ := json.Marshal(names)
	return string(b)
}
