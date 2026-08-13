package model

import "time"

// LibraryKind bestimmt das TMDB-Matching-Verhalten.
//   movies  → jedes Video wird als Film gematcht
//   tv      → jeder Ordner wird als Serie gematcht, Dateien als Episoden (SxxExx)
//   private → keine TMDB-Anreicherung (Privatvideos)
type LibraryKind string

const (
	KindMovies  LibraryKind = "movies"
	KindTV      LibraryKind = "tv"
	KindPrivate LibraryKind = "private"
)

// ItemStream beschreibt einen einzelnen Stream (Video/Audio/Subtitle) innerhalb eines Items.
type ItemStream struct {
	Index     int    `json:"index"`           // ffprobe-Stream-Index
	Type      string `json:"type"`            // "video" | "audio" | "subtitle"
	Codec     string `json:"codec"`           // z.B. "h264", "aac", "subrip"
	Language  string `json:"language,omitempty"` // ISO 639-2 (z.B. "eng", "ger")
	Title     string `json:"title,omitempty"`    // frei gewählter Name durch den Autor
	Channels  int    `json:"channels,omitempty"` // nur Audio
	IsDefault bool   `json:"isDefault"`
	IsForced  bool   `json:"isForced,omitempty"` // Subs: forced-track
	// FieldOrder: ffprobe-Wert für Video-Streams. "progressive" oder "" =
	// keine Halbbilder, alles fein. "tt"/"bb"/"tb"/"bt" = interlaced — UI
	// blendet einen 🪤-Hinweis ein und Transcode kann deinterlacen.
	FieldOrder string `json:"fieldOrder,omitempty"`
}

// User und Session.
type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"isAdmin"`
	// MaxAgeRating: nil = keine Beschränkung. Sonst 0/6/12/16/18 als maximal
	// erlaubte FSK. Admins ignorieren den Wert; für User-Accounts werden
	// Items mit höherer FSK ausgeblendet und Playback blockiert.
	MaxAgeRating *int      `json:"maxAgeRating,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Session struct {
	Token     string
	UserID    int64
	ExpiresAt time.Time
}

// Playlist: benannte, geordnete Item-Sammlung.
type Playlist struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"createdAt"`
	ItemCount      int       `json:"itemCount"`
	// PosterItemID: erstes Item in der Playlist mit TMDB-Poster (oder Thumbnail);
	// dient als Kachel-Vorschau.
	PosterItemID     int64 `json:"posterItemId,omitempty"`
	PosterMetadataID int64 `json:"posterMetadataId,omitempty"`
}

type Library struct {
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	Path      string      `json:"path"`
	Kind      LibraryKind `json:"kind"`
	OnHome    bool        `json:"onHome"`
	// SortOrder steuert die Reihenfolge in Topbar-Dropdown + Home-Recent-
	// Sektionen. Default 0 — bei Gleichstand fallen wir alphabetisch zurueck.
	SortOrder int `json:"sortOrder"`
	// ChannelLabelOnTop steuert das Card-Layout fuer private-Libs: true (Default)
	// = Top-Folder (z.B. YouTube-Kanal) als Top-Zeile, Dateiname unten dicker.
	// false = standard layout (Titel oben, Folder im Pfad). Wirkt nur fuer
	// Libraries mit kind=private; bei movies/tv ist das Feld irrelevant.
	ChannelLabelOnTop bool      `json:"channelLabelOnTop"`
	CreatedAt         time.Time `json:"createdAt"`
}

// Metadata hält angereicherte TMDB-Daten zu einem Film, einer Serie oder einer Episode.
type Metadata struct {
	ID            int64     `json:"id"`
	TMDBType      string    `json:"tmdbType"` // "movie" | "tv" | "episode"
	TMDBID        int64     `json:"tmdbId"`
	ParentID      int64     `json:"parentId,omitempty"` // Episode → Show
	Title         string    `json:"title"`
	OriginalTitle string    `json:"originalTitle"`
	Year          int       `json:"year,omitempty"`
	ReleaseDate   time.Time `json:"releaseDate,omitempty"`
	Overview      string    `json:"overview"`
	Rating        float64   `json:"rating"`
	Genres        string    `json:"genres"` // JSON array
	RuntimeMin    int       `json:"runtimeMin"`
	PosterPath    string    `json:"posterPath"`   // TMDB-Pfad, wir cachen ein JPG lokal
	BackdropPath  string    `json:"backdropPath"`
	Season        int       `json:"season,omitempty"`  // nur Episode
	Episode       int       `json:"episode,omitempty"` // nur Episode
	IMDBID        string    `json:"imdbId"`
	// AgeRating: Altersfreigabe (FSK), Werte "", "0", "6", "12", "16", "18".
	// Manuell setzbar im Metadata-Edit-Dialog.
	AgeRating string    `json:"ageRating,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Item struct {
	ID           int64     `json:"id"`
	LibraryID    int64     `json:"libraryId"`
	Path         string    `json:"path"`
	RelPath      string    `json:"relPath"`
	Title        string    `json:"title"`
	Container    string    `json:"container"`
	VideoCodec   string    `json:"videoCodec"`
	AudioCodec   string    `json:"audioCodec"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	DurationSec  float64   `json:"durationSec"`
	SizeBytes    int64     `json:"sizeBytes"`
	BitrateKbps  int       `json:"bitrateKbps"`
	ThumbPath    string    `json:"-"`
	HasThumb     bool      `json:"hasThumb"`
	ModTime      time.Time `json:"modTime"`
	ReleasedAt   time.Time `json:"releasedAt"` // creation_time aus ffprobe, Fallback: ModTime
	AddedAt      time.Time `json:"addedAt"`
	MetadataID        int64     `json:"metadataId,omitempty"`
	MetadataConfirmed bool      `json:"metadataConfirmed,omitempty"`
	// EpisodeEnd > 0 markiert Doppelfolgen (S07E23E24 → metadata_id = E23,
	// EpisodeEnd = 24). Staffel-Ansicht markiert beide Folgen als owned.
	EpisodeEnd int       `json:"episodeEnd,omitempty"`
	Metadata   *Metadata `json:"metadata,omitempty"` // eingebettet wenn geladen
	Watched      bool      `json:"watched"`
	WatchedAt    time.Time `json:"watchedAt,omitempty"`
	Favorite     bool      `json:"favorite"`
	FavoritedAt  time.Time `json:"favoritedAt,omitempty"`
	TrickplayStatus string       `json:"trickplayStatus,omitempty"` // "" | "pending" | "done" | "failed"
	Streams         []ItemStream `json:"streams,omitempty"`
	// VariantCount: wieviele Items insgesamt dieselbe metadata_id haben (= dieses
	// Item + Geschwister). 0/1 bedeutet „keine Geschwister". Wird im Server
	// per attachVariantCounts gesetzt — die Kachel kann so den ×N-Badge auch
	// dann anzeigen, wenn das Geschwister in einer anderen Library liegt und
	// im aktuellen Grid-Render nicht enthalten ist.
	VariantCount int `json:"variantCount,omitempty"`
	// VariantSplit: true = dieses Item ist bewusst aus der automatischen
	// ×N-Varianten-Gruppierung herausgenommen (groupVariants in app.js
	// überspringt es) und erscheint immer als eigene Kachel, obwohl es
	// dieselbe metadata_id wie Geschwister-Items hat.
	VariantSplit bool `json:"variantSplit,omitempty"`
	// IntroStartSec/IntroEndSec: erkannte Vorspann-/Opening-Zeitspanne
	// (internal/introskip). nil = nicht analysiert oder kein Intro erkannt.
	IntroStartSec *float64 `json:"introStartSec,omitempty"`
	IntroEndSec   *float64 `json:"introEndSec,omitempty"`
}


// Person: TMDB-Schauspieler (dedupliziert über tmdb_id).
type Person struct {
	ID          int64  `json:"id"`
	TMDBID      int64  `json:"tmdbId"`
	Name        string `json:"name"`
	ProfilePath string `json:"profilePath,omitempty"` // TMDB-Pfad; gecacht unter /config/people/
}

// CastMember: eine Rolle, mit eingebetteter Person für die UI.
type CastMember struct {
	PersonID    int64  `json:"personId"`
	TMDBID      int64  `json:"tmdbId"`
	Name        string `json:"name"`
	ProfilePath string `json:"profilePath,omitempty"`
	Character   string `json:"character"`
	Role        string `json:"role"` // "main" | "guest"
	Order       int    `json:"order"`
}

type ScanStatus struct {
	Running   bool   `json:"running"`
	LibraryID int64  `json:"libraryId"`
	Folder    string `json:"folder,omitempty"` // leer = gesamte Library, sonst Scope-Ordner
	Current   string `json:"current"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Skipped   int    `json:"skipped"` // inkrementell übersprungene Dateien
	New       int    `json:"new"`     // neu hinzugefügt
	Updated   int    `json:"updated"` // bestehende Items mit geändertem mtime neu probt
	Removed   int    `json:"removed"` // Orphan-Bereinigung am Scan-Ende
	Force     bool   `json:"force"`
	LastError string `json:"lastError"`
	// LastSummary: nach Ende eines Scans wird hier der vollständige Bericht
	// abgelegt — Frontend zeigt ihn als Abschluss-Dialog. Wird mit jedem neuen
	// Scan-Start auf nil zurückgesetzt.
	LastSummary *ScanSummary `json:"lastSummary,omitempty"`
}

// ScanSummary fasst das Ergebnis eines abgeschlossenen Scans zusammen.
// Wird vom Frontend gepollt und einmalig als Abschluss-Dialog angezeigt.
type ScanSummary struct {
	LibraryID    int64                       `json:"libraryId"`
	LibraryName  string                      `json:"libraryName"`
	Folder       string                      `json:"folder,omitempty"`
	Force        bool                        `json:"force"`
	StartedAt    time.Time                   `json:"startedAt"`
	FinishedAt   time.Time                   `json:"finishedAt"`
	Total        int                         `json:"total"`
	New          int                         `json:"new"`
	Updated      int                         `json:"updated"`
	Skipped      int                         `json:"skipped"`
	Removed      int                         `json:"removed"`
	Error        string                      `json:"error,omitempty"`
	// PerFolder: Top-Level-Folder → Counts. Leer-String-Key bedeutet
	// Library-Root (Dateien direkt in der Lib ohne Unterordner).
	PerFolder map[string]ScanFolderStats `json:"perFolder,omitempty"`
	// Detail-Listen: vollständige rel_paths der betroffenen Items, damit das
	// UI bei Klick auf eine Statistik die einzelnen Dateien zeigen kann.
	// Pfade sind relativ zur Library-Root (z.B. "Subordner/Film.mkv").
	NewPaths     []string `json:"newPaths,omitempty"`
	UpdatedPaths []string `json:"updatedPaths,omitempty"`
	RemovedPaths []string `json:"removedPaths,omitempty"`
}

// ScanFolderStats: Counts pro Top-Level-Ordner.
type ScanFolderStats struct {
	New     int `json:"new"`
	Updated int `json:"updated"`
	Removed int `json:"removed,omitempty"`
}
