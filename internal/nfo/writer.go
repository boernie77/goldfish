// Package nfo schreibt Kodi-kompatible .nfo-Sidecar-Dateien für Filme und
// Episoden. Plex und Jellyfin akzeptieren dieses Format als Metadaten-
// Override, sodass ein umbenanntes Video nicht notwendig ist.
//
// Ablage-Konventionen:
//   Movie:   <Dateiname>.nfo  neben der Videodatei
//   Episode: <Dateiname>.nfo  neben der Videodatei
//   Show:    tvshow.nfo       im Top-Level-Folder der Serie
package nfo

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

// fmtDate formatiert ReleaseDate als YYYY-MM-DD; leerer String wenn Zeit-Null.
func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// parseGenres bricht das im Store gespeicherte Genre-Feld auf: meist JSON-Array
// (`["Action","Drama"]`), seltener ein Komma-separierter Fallback. Leer → nil.
func parseGenres(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr
		}
	}
	var out []string
	for _, g := range strings.Split(s, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// uniqueID entspricht dem <uniqueid>-Tag in Kodi-NFO.
type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type movieNFO struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Outline       string     `xml:"outline,omitempty"`
	Tagline       string     `xml:"tagline,omitempty"`
	Runtime       int        `xml:"runtime,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"` // YYYY-MM-DD
	Rating        []rating   `xml:"ratings>rating,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
	ID            string     `xml:"id,omitempty"` // IMDb fallback
	TMDBID        int64      `xml:"tmdbid,omitempty"`
	Genres        []string   `xml:"genre,omitempty"`
}

type episodeNFO struct {
	XMLName       xml.Name   `xml:"episodedetails"`
	Title         string     `xml:"title"`
	ShowTitle     string     `xml:"showtitle,omitempty"`
	Season        int        `xml:"season"`
	Episode       int        `xml:"episode"`
	// EpisodeEnd wird bei Doppelfolgen (z. B. S07E23E24.mkv) geschrieben.
	// Kodi/Jellyfin/Plex interpretieren den Tag als Range-Ende.
	EpisodeEnd    int        `xml:"episodenumberend,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Runtime       int        `xml:"runtime,omitempty"`
	Aired         string     `xml:"aired,omitempty"` // YYYY-MM-DD
	Rating        []rating   `xml:"ratings>rating,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
	ID            string     `xml:"id,omitempty"`
	TMDBID        int64      `xml:"tmdbid,omitempty"`
}

type tvshowNFO struct {
	XMLName       xml.Name   `xml:"tvshow"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	Rating        []rating   `xml:"ratings>rating,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
	ID            string     `xml:"id,omitempty"`
	TMDBID        int64      `xml:"tmdbid,omitempty"`
	Genres        []string   `xml:"genre,omitempty"`
}

type rating struct {
	Name    string  `xml:"name,attr"`
	Default bool    `xml:"default,attr,omitempty"`
	Max     int     `xml:"max,attr,omitempty"`
	Value   float64 `xml:"value"`
	Votes   int     `xml:"votes,omitempty"`
}

// nfoPath liefert den Pfad für die Sidecar-NFO: basierend auf der Videodatei,
// z. B. /media/Filme/Black Hawk Down.mkv → /media/Filme/Black Hawk Down.nfo
func nfoPath(videoPath string) string {
	ext := filepath.Ext(videoPath)
	return strings.TrimSuffix(videoPath, ext) + ".nfo"
}

// WriteMovie schreibt eine Movie-NFO neben die Videodatei.
func WriteMovie(videoPath string, it *model.Item, md *model.Metadata) (string, error) {
	n := movieNFO{
		Title:         md.Title,
		OriginalTitle: md.OriginalTitle,
		Year:          md.Year,
		Plot:          md.Overview,
		TMDBID:        md.TMDBID,
	}
	if d := fmtDate(md.ReleaseDate); d != "" {
		n.Premiered = d
	}
	if md.RuntimeMin > 0 {
		n.Runtime = md.RuntimeMin
	}
	if md.Rating > 0 {
		n.Rating = []rating{{Name: "tmdb", Default: true, Max: 10, Value: md.Rating}}
	}
	if md.TMDBID > 0 {
		n.UniqueIDs = append(n.UniqueIDs, uniqueID{Type: "tmdb", Default: true, Value: fmt.Sprintf("%d", md.TMDBID)})
	}
	if md.IMDBID != "" {
		n.UniqueIDs = append(n.UniqueIDs, uniqueID{Type: "imdb", Value: md.IMDBID})
		n.ID = md.IMDBID // Plex-Legacy-Field
	}
	n.Genres = parseGenres(md.Genres)
	path := nfoPath(videoPath)
	if err := writeXML(path, n); err != nil {
		return "", err
	}
	return path, nil
}

// WriteEpisode schreibt eine Episode-NFO neben die Videodatei.
// `show` ist die Parent-Show-Metadata (optional; wird für <showtitle> genutzt).
func WriteEpisode(videoPath string, it *model.Item, md *model.Metadata, show *model.Metadata) (string, error) {
	n := episodeNFO{
		Title:   md.Title,
		Season:  md.Season,
		Episode: md.Episode,
		Plot:    md.Overview,
		TMDBID:  md.TMDBID,
	}
	if it != nil && it.EpisodeEnd > md.Episode {
		n.EpisodeEnd = it.EpisodeEnd
	}
	if show != nil {
		n.ShowTitle = show.Title
	}
	if d := fmtDate(md.ReleaseDate); d != "" {
		n.Aired = d
	}
	if md.RuntimeMin > 0 {
		n.Runtime = md.RuntimeMin
	}
	if md.Rating > 0 {
		n.Rating = []rating{{Name: "tmdb", Default: true, Max: 10, Value: md.Rating}}
	}
	if md.TMDBID > 0 {
		n.UniqueIDs = append(n.UniqueIDs, uniqueID{Type: "tmdb", Default: true, Value: fmt.Sprintf("%d", md.TMDBID)})
	}
	if md.IMDBID != "" {
		n.UniqueIDs = append(n.UniqueIDs, uniqueID{Type: "imdb", Value: md.IMDBID})
		n.ID = md.IMDBID
	}
	path := nfoPath(videoPath)
	if err := writeXML(path, n); err != nil {
		return "", err
	}
	return path, nil
}

// WriteTVShow schreibt tvshow.nfo in den Top-Level-Ordner einer Serie.
// `folderAbsolute` ist der absolute Pfad des Ordners (z. B. /media/Serien/Scrubs).
func WriteTVShow(folderAbsolute string, show *model.Metadata) (string, error) {
	n := tvshowNFO{
		Title:         show.Title,
		OriginalTitle: show.OriginalTitle,
		Year:          show.Year,
		Plot:          show.Overview,
		TMDBID:        show.TMDBID,
	}
	if d := fmtDate(show.ReleaseDate); d != "" {
		n.Premiered = d
	}
	if show.Rating > 0 {
		n.Rating = []rating{{Name: "tmdb", Default: true, Max: 10, Value: show.Rating}}
	}
	if show.TMDBID > 0 {
		n.UniqueIDs = append(n.UniqueIDs, uniqueID{Type: "tmdb", Default: true, Value: fmt.Sprintf("%d", show.TMDBID)})
	}
	if show.IMDBID != "" {
		n.UniqueIDs = append(n.UniqueIDs, uniqueID{Type: "imdb", Value: show.IMDBID})
		n.ID = show.IMDBID
	}
	n.Genres = parseGenres(show.Genres)
	path := filepath.Join(folderAbsolute, "tvshow.nfo")
	if err := writeXML(path, n); err != nil {
		return "", err
	}
	return path, nil
}

func writeXML(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}
