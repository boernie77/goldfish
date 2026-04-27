// Package omdb ist ein minimaler Client für die OMDb-API (omdbapi.com).
// OMDb liefert IMDb-basierte Daten und dient als Fallback, wenn TMDB nichts findet.
// API-Key ist kostenlos (1000 req/Tag).
package omdb

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

const baseURL = "https://www.omdbapi.com/"

type Client struct {
	apiKey string
	hc     *http.Client

	mu            sync.Mutex
	quotaExceeded bool // true, sobald OMDb 401 "Request limit reached" geliefert hat
}

// ErrQuotaExceeded signalisiert, dass das OMDb-Tageslimit erreicht ist.
// Aufrufer können damit eine User-freundliche Meldung erzeugen.
var ErrQuotaExceeded = errors.New("OMDb-Tageslimit erreicht — bitte morgen wieder versuchen")

func New(apiKey string) *Client {
	return &Client{apiKey: apiKey, hc: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Enabled() bool { return c.apiKey != "" }

// QuotaExceeded ist true, sobald OMDb für diesen Client einmal 401 geantwortet
// hat. Weitere Requests werden bis zum Prozess-Restart (= Container-Restart
// über Nacht) übersprungen, damit wir nicht weiter gegen eine gesperrte Quote
// ballern.
func (c *Client) QuotaExceeded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quotaExceeded
}

func (c *Client) markQuotaExceeded() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quotaExceeded = true
}

// Result fasst OMDb-Response auf ein einheitliches Format zusammen.
type Result struct {
	IMDBID    string
	Title     string
	Year      int
	Overview  string
	Rating    float64 // IMDb-Rating 0–10
	Poster    string  // volle URL (nicht Pfad)
	Runtime   int     // Minuten
	Genre     string  // "Action, Drama"
	Actors    []string // Schauspielerliste (bis zu 4 Einträge von OMDb)
	AgeRating string   // FSK (mapped aus MPAA), "" wenn unbekannt
	IsSeries  bool
}

// Raw-Response
type rawResponse struct {
	Title    string `json:"Title"`
	Year     string `json:"Year"`
	Plot     string `json:"Plot"`
	Poster   string `json:"Poster"`
	IMDBID   string `json:"imdbID"`
	Type     string `json:"Type"` // "movie" | "series" | "episode"
	Rating   string `json:"imdbRating"`
	Runtime  string `json:"Runtime"`
	Genre    string `json:"Genre"`
	Actors   string `json:"Actors"` // "Alice, Bob, Carol, Dave"
	Rated    string `json:"Rated"`  // MPAA: G/PG/PG-13/R/NC-17 oder "N/A"
	Response string `json:"Response"`
	Error    string `json:"Error"`
}

// mpaaToFSK mappt OMDbs MPAA-Rating auf eine grobe FSK-Entsprechung.
// Quelle der Konvention: gängige deutsche Synchron-Kennzeichnungen. Werte sind
// Approximation — eine TMDB-DE-Cert ist immer genauer.
func mpaaToFSK(mpaa string) string {
	switch strings.ToUpper(strings.TrimSpace(mpaa)) {
	case "G", "TV-G", "TV-Y":
		return "0"
	case "PG", "TV-Y7", "TV-PG":
		return "6"
	case "PG-13", "TV-14":
		return "12"
	case "R", "TV-MA":
		return "16"
	case "NC-17", "X":
		return "18"
	}
	return ""
}

// searchEntry — Einzeleintrag aus der OMDb-Search-Response (s=).
type searchEntry struct {
	Title  string `json:"Title"`
	Year   string `json:"Year"`
	IMDBID string `json:"imdbID"`
	Type   string `json:"Type"`
}
type searchResponse struct {
	Search   []searchEntry `json:"Search"`
	Response string        `json:"Response"`
}

func (r rawResponse) toResult() Result {
	res := Result{
		IMDBID:   r.IMDBID,
		Title:    r.Title,
		Overview: r.Plot,
		Genre:    r.Genre,
		IsSeries: r.Type == "series",
	}
	if r.Poster != "" && r.Poster != "N/A" {
		res.Poster = r.Poster
	}
	// Year kann "2024" oder "2020–2024" sein
	yearStr := strings.SplitN(r.Year, "–", 2)[0]
	if y, err := strconv.Atoi(yearStr); err == nil {
		res.Year = y
	}
	if rt, err := strconv.ParseFloat(r.Rating, 64); err == nil {
		res.Rating = rt
	}
	// Runtime: "142 min"
	rtStr := strings.TrimSuffix(strings.TrimSpace(r.Runtime), " min")
	if m, err := strconv.Atoi(rtStr); err == nil {
		res.Runtime = m
	}
	// Actors-Liste (OMDb liefert max 4, komma-separiert)
	if r.Actors != "" && r.Actors != "N/A" {
		for _, a := range strings.Split(r.Actors, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				res.Actors = append(res.Actors, a)
			}
		}
	}
	// FSK approximieren aus MPAA-„Rated"
	if r.Rated != "" && r.Rated != "N/A" {
		res.AgeRating = mpaaToFSK(r.Rated)
	}
	return res
}

func (c *Client) get(ctx context.Context, params url.Values) (*Result, error) {
	if !c.Enabled() {
		return nil, errors.New("OMDb: kein API-Key konfiguriert")
	}
	if c.QuotaExceeded() {
		return nil, ErrQuotaExceeded
	}
	params.Set("apikey", c.apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyStr := strings.TrimSpace(string(body))
		// OMDb liefert 401 + "Request limit reached!" wenn das Tages-
		// Kontingent (Free-Tier: 1000/Tag) verbraucht ist. Merker setzen,
		// damit weitere Calls in dieser Session gespart werden.
		if resp.StatusCode == 401 && strings.Contains(bodyStr, "limit reached") {
			c.markQuotaExceeded()
			return nil, ErrQuotaExceeded
		}
		return nil, fmt.Errorf("OMDb HTTP %d: %s", resp.StatusCode, bodyStr)
	}
	var raw rawResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Response == "False" {
		return nil, nil // kein Treffer – kein Fehler
	}
	r := raw.toResult()
	return &r, nil
}

// SearchByTitle sucht nach Titel (und optional Jahr) via strikter `t=`-Query.
// OMDbs `t=` erwartet exakten Titel-Match. Wenn das fehlschlägt, kaskadiert
// der Enricher auf SearchTitleLoose (`s=`-Query) zurück.
func (c *Client) SearchByTitle(ctx context.Context, title string, year int) (*Result, error) {
	p := url.Values{}
	p.Set("t", title)
	if year > 0 {
		p.Set("y", strconv.Itoa(year))
	}
	return c.get(ctx, p)
}

// SearchTitleLoose nutzt OMDbs Search-API (`s=`), die wie eine Volltextsuche
// arbeitet — kleine Titelabweichungen (z. B. „und" vs „&") werden toleriert.
// Wir nehmen den ersten Treffer mit passendem Jahr (falls gesetzt), sonst
// den ersten insgesamt, und laden dessen vollständige Details via IMDb-ID
// nach (Plot/Rating/Actors kommen in der Such-Response nicht mit).
func (c *Client) SearchTitleLoose(ctx context.Context, title string, year int) (*Result, error) {
	if !c.Enabled() {
		return nil, errors.New("OMDb: kein API-Key konfiguriert")
	}
	if c.QuotaExceeded() {
		return nil, ErrQuotaExceeded
	}
	p := url.Values{}
	p.Set("s", title)
	p.Set("type", "movie")
	if year > 0 {
		p.Set("y", strconv.Itoa(year))
	}
	p.Set("apikey", c.apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"?"+p.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyStr := strings.TrimSpace(string(body))
		if resp.StatusCode == 401 && strings.Contains(bodyStr, "limit reached") {
			c.markQuotaExceeded()
			return nil, ErrQuotaExceeded
		}
		return nil, fmt.Errorf("OMDb search HTTP %d: %s", resp.StatusCode, bodyStr)
	}
	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	if sr.Response == "False" || len(sr.Search) == 0 {
		return nil, nil
	}
	// Wenn Year gesetzt ist, bevorzugen wir exakte Jahres-Übereinstimmung —
	// sonst erster Treffer.
	pick := sr.Search[0]
	if year > 0 {
		for _, e := range sr.Search {
			ys := strings.SplitN(e.Year, "–", 2)[0]
			if y, err := strconv.Atoi(ys); err == nil && y == year {
				pick = e
				break
			}
		}
	}
	if pick.IMDBID == "" {
		return nil, nil
	}
	return c.ByIMDb(ctx, pick.IMDBID)
}

// ByIMDb holt Details zu einer IMDb-ID.
func (c *Client) ByIMDb(ctx context.Context, imdbID string) (*Result, error) {
	p := url.Values{}
	p.Set("i", imdbID)
	return c.get(ctx, p)
}
