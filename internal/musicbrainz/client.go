// Package musicbrainz ist ein minimaler Client für die MusicBrainz-API +
// das begleitende Cover Art Archive. Beide sind kostenlos und offen (keine
// Nutzungsbeschränkung, kein API-Key), MusicBrainz verlangt aber einen
// aussagekräftigen User-Agent-Header und max. 1 Request/Sekunde ohne
// Authentifizierung — eigener Rate-Limiter, NICHT den TMDB-Client-Limiter
// mitbenutzen (der ist auf TMDBs 35 req/10s abgestimmt).
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	mbBaseURL  = "https://musicbrainz.org/ws/2"
	caaBaseURL = "https://coverartarchive.org"
	minGap     = 1100 * time.Millisecond // etwas Puffer über der 1 req/s-Grenze
)

type Client struct {
	userAgent string
	hc        *http.Client

	mu      sync.Mutex
	lastReq time.Time
}

// New erstellt einen Client. userAgent MUSS App-Name+Version+Kontakt enthalten
// (MusicBrainz-Policy, z.B. "Goldfish/1.0 (https://github.com/boernie77/goldfish)") —
// ohne aussagekräftigen User-Agent kann MusicBrainz Requests ablehnen.
func New(userAgent string) *Client {
	if userAgent == "" {
		userAgent = "Goldfish-MediaServer/1.0"
	}
	return &Client{userAgent: userAgent, hc: &http.Client{Timeout: 15 * time.Second}}
}

// waitForSlot blockiert bis mindestens minGap seit dem letzten Request
// vergangen ist (einfacher serieller Rate-Limiter, ausreichend für den
// periodischen Music-Enrichment-Worker, der ohnehin sequenziell arbeitet).
func (c *Client) waitForSlot(ctx context.Context) error {
	c.mu.Lock()
	wait := minGap - time.Since(c.lastReq)
	c.mu.Unlock()
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	c.lastReq = time.Now()
	c.mu.Unlock()
	return nil
}

type releaseSearchResponse struct {
	Releases []struct {
		ID    string `json:"id"`
		Score int    `json:"score"`
		Title string `json:"title"`
		Date  string `json:"date"`
	} `json:"releases"`
}

// ReleaseMatch ist das Ergebnis einer erfolgreichen Release-Suche.
type ReleaseMatch struct {
	MBID string
	Year int
}

// SearchRelease sucht ein Release per Artist+Album und liefert das beste
// Ergebnis (höchster Score). ("", 0) wenn nichts gefunden wurde — kein Fehler,
// das ist der Normalfall bei vielen kleinen/obskuren Alben.
func (c *Client) SearchRelease(ctx context.Context, artist, album string) (*ReleaseMatch, error) {
	if err := c.waitForSlot(ctx); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`artist:"%s" AND release:"%s"`, escapeQuery(artist), escapeQuery(album))
	params := url.Values{}
	params.Set("query", q)
	params.Set("fmt", "json")
	params.Set("limit", "5")
	req, err := http.NewRequestWithContext(ctx, "GET", mbBaseURL+"/release/?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("musicbrainz: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var r releaseSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if len(r.Releases) == 0 {
		return nil, nil
	}
	best := r.Releases[0]
	for _, rel := range r.Releases[1:] {
		if rel.Score > best.Score {
			best = rel
		}
	}
	year := 0
	if len(best.Date) >= 4 {
		_, _ = fmt.Sscanf(best.Date[:4], "%d", &year)
	}
	return &ReleaseMatch{MBID: best.ID, Year: year}, nil
}

// DownloadCoverFront lädt das Front-Cover eines Releases aus dem Cover Art
// Archive. (nil, nil) wenn kein Cover vorhanden ist (Normalfall bei vielen
// Releases, kein Fehler) — Aufrufer unterscheidet über nil-Check.
func (c *Client) DownloadCoverFront(ctx context.Context, mbid string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", caaBaseURL+"/release/"+mbid+"/front", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("coverartarchive: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024)) // 20 MB Cap
}

// escapeQuery escaped Lucene-Sonderzeichen für die MusicBrainz-Suchsyntax
// (die Suche selbst nutzt Lucene-Query-Syntax, doppelte Anführungszeichen im
// Suchtext würden die Query sonst zerbrechen).
func escapeQuery(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
