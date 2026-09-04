package model

import "time"

// LibraryKind bestimmt das TMDB-Matching-Verhalten.
//
//	movies  → jedes Video wird als Film gematcht
//	tv      → jeder Ordner wird als Serie gematcht, Dateien als Episoden (SxxExx)
//	private → keine TMDB-Anreicherung (Privatvideos)
//	music   → Audio-Dateien, Metadaten aus eingebetteten Tags (ID3/FLAC/Vorbis)
//	          + optionalem MusicBrainz/Cover-Art-Archive-Fallback, KEINE TMDB-
//	          Anreicherung (siehe internal/enrich/music_worker.go)
type LibraryKind string

const (
	KindMovies  LibraryKind = "movies"
	KindTV      LibraryKind = "tv"
	KindPrivate LibraryKind = "private"
	KindMusic   LibraryKind = "music"
)

// ItemStream beschreibt einen einzelnen Stream (Video/Audio/Subtitle) innerhalb eines Items.
type ItemStream struct {
	Index     int    `json:"index"`              // ffprobe-Stream-Index
	Type      string `json:"type"`               // "video" | "audio" | "subtitle"
	Codec     string `json:"codec"`              // z.B. "h264", "aac", "subrip"
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
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	// MaxAgeRating: nil = keine Beschränkung. Sonst 0/6/12/16/18 als maximal
	// erlaubte FSK. Admins ignorieren den Wert; für User-Accounts werden
	// Items mit höherer FSK ausgeblendet und Playback blockiert.
	MaxAgeRating *int `json:"maxAgeRating,omitempty"`
	// CanDownload: darf dieser User Dateien herunterladen (Detail-Dialog,
	// Bulk-Download)? Default true (bestehende Accounts bleiben unverändert
	// erlaubt). Admins ignorieren den Wert (siehe MaxAgeRating-Konvention
	// oben) — Admin darf immer downloaden.
	CanDownload bool      `json:"canDownload"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Session struct {
	Token     string
	UserID    int64
	ExpiresAt time.Time
}

// Playlist: benannte, geordnete Item-Sammlung.
type Playlist struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	ItemCount int       `json:"itemCount"`
	// Kind: "video" | "music" — getrennt seit 2026-09-04 (User-Wunsch: keine
	// gemischten Video-/Musik-Playlists mehr). Wird bei der Erstellung fix
	// gesetzt, nicht aus den enthaltenen Items abgeleitet (eine Playlist kann
	// leer sein). Bestehende Playlists sind per Migration auf "video" gesetzt.
	Kind string `json:"kind"`
	// PosterItemID: erstes Item in der Playlist mit TMDB-Poster (oder Thumbnail);
	// dient als Kachel-Vorschau.
	PosterItemID     int64 `json:"posterItemId,omitempty"`
	PosterMetadataID int64 `json:"posterMetadataId,omitempty"`
}

type Library struct {
	ID     int64       `json:"id"`
	Name   string      `json:"name"`
	Path   string      `json:"path"`
	Kind   LibraryKind `json:"kind"`
	OnHome bool        `json:"onHome"`
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
	PosterPath    string    `json:"posterPath"` // TMDB-Pfad, wir cachen ein JPG lokal
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
	ID                int64     `json:"id"`
	LibraryID         int64     `json:"libraryId"`
	Path              string    `json:"path"`
	RelPath           string    `json:"relPath"`
	Title             string    `json:"title"`
	Container         string    `json:"container"`
	VideoCodec        string    `json:"videoCodec"`
	AudioCodec        string    `json:"audioCodec"`
	Width             int       `json:"width"`
	Height            int       `json:"height"`
	DurationSec       float64   `json:"durationSec"`
	SizeBytes         int64     `json:"sizeBytes"`
	BitrateKbps       int       `json:"bitrateKbps"`
	ThumbPath         string    `json:"-"`
	HasThumb          bool      `json:"hasThumb"`
	ModTime           time.Time `json:"modTime"`
	ReleasedAt        time.Time `json:"releasedAt"` // creation_time aus ffprobe, Fallback: ModTime
	AddedAt           time.Time `json:"addedAt"`
	MetadataID        int64     `json:"metadataId,omitempty"`
	MetadataConfirmed bool      `json:"metadataConfirmed,omitempty"`
	// EpisodeEnd > 0 markiert Doppelfolgen (S07E23E24 → metadata_id = E23,
	// EpisodeEnd = 24). Staffel-Ansicht markiert beide Folgen als owned.
	EpisodeEnd  int       `json:"episodeEnd,omitempty"`
	Metadata    *Metadata `json:"metadata,omitempty"` // eingebettet wenn geladen
	Watched     bool      `json:"watched"`
	WatchedAt   time.Time `json:"watchedAt,omitempty"`
	Favorite    bool      `json:"favorite"`
	FavoritedAt time.Time `json:"favoritedAt,omitempty"`
	// Rating: persönliche Sternebewertung 0–3 (pro User, user_item_state.rating).
	// 0 = keine Wertung.
	Rating          int          `json:"rating,omitempty"`
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
	// DupeOtherPaths: nur im "Datei in anderem Ordner"-Filter gesetzt —
	// rel_path(s) gleichnamiger, ähnlich großer Dateien in einem ANDEREN
	// Ordner derselben Library (Kandidaten für "eine der beiden Kopien löschen").
	DupeOtherPaths []string `json:"dupeOtherPaths,omitempty"`
	// Musik-Felder (nur kind=music, aus eingebetteten Tags gelesen, siehe
	// scanner.probeItem). TrackNo 0 = keine Track-Nummer im Tag gefunden.
	Artist       string `json:"artist,omitempty"`
	Album        string `json:"album,omitempty"`
	TrackNo      int    `json:"trackNo,omitempty"`
	MusicAlbumID int64  `json:"musicAlbumId,omitempty"`
	// LastPlayedAt: user_item_state.last_played_at (pro User). Nur in
	// ListItems befüllt (für die "Alle Titel"-Listenansicht der Musik-
	// Bibliotheken); andere Item-Ladepfade lassen es bewusst leer.
	// WICHTIG: Pointer, nicht time.Time — encoding/json behandelt eine
	// time.Time-Nullzeit NICHT als "leer" (omitempty greift nur bei Pointern/
	// Slices/Maps/primitiven Nullwerten), ein nie abgespieltes Item hätte
	// sonst "0001-01-01T00:00:00Z" im JSON gehabt (Frontend zeigte das als
	// "01.01.1" an — User-Bericht 2026-09-04).
	LastPlayedAt *time.Time `json:"lastPlayedAt,omitempty"`
}

// MusicAlbum: eine (Artist,Album)-Gruppe innerhalb einer Musik-Bibliothek.
// Kanonische Identität kommt aus den Tag-Werten der zugehörigen Items (Fallback:
// übergeordneter Ordnername, wenn Tags fehlen — siehe scanner.GroupMusicAlbums).
type MusicAlbum struct {
	ID             int64     `json:"id"`
	LibraryID      int64     `json:"libraryId"`
	Artist         string    `json:"artist"`
	Album          string    `json:"album"`
	Year           int       `json:"year,omitempty"`
	Genre          string    `json:"genre,omitempty"`
	CoverSource    string    `json:"coverSource,omitempty"` // "" | "embedded" | "coverart_archive"
	MBReleaseID    string    `json:"-"`
	CoverFetchedAt time.Time `json:"-"`
	TrackCount     int       `json:"trackCount,omitempty"` // per COUNT(*) beim Listing gesetzt
	// Favorite: per-User (user_music_album_favorites), analog
	// user_item_state.favorite für einzelne Titel. Nur gesetzt, wenn die
	// Store-Funktion mit einer UserID aufgerufen wurde.
	Favorite bool `json:"favorite,omitempty"`
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
	LibraryID   int64     `json:"libraryId"`
	LibraryName string    `json:"libraryName"`
	Folder      string    `json:"folder,omitempty"`
	Force       bool      `json:"force"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	Total       int       `json:"total"`
	New         int       `json:"new"`
	Updated     int       `json:"updated"`
	Skipped     int       `json:"skipped"`
	Removed     int       `json:"removed"`
	Error       string    `json:"error,omitempty"`
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
