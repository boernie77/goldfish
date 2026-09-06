package store

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	sqlite "modernc.org/sqlite"
)

// NATSORT-Collation einmalig registrieren — danach ist `COLLATE NATSORT` in
// jedem ORDER BY nutzbar (Zahlen werden als ganze Zahlen verglichen,
// case-insensitive). Wird nur einmal pro Prozess registriert; spaetere
// Aufrufe von Open koennen kein Re-Register triggern.
// ErrLastLibraryPath: eine Bibliothek muss immer mindestens einen Quellordner
// behalten (siehe `DeleteLibraryPath`-Kommentar für den Bug, den diese Sperre
// verhindert).
var ErrLastLibraryPath = errors.New("letzter Pfad einer Bibliothek kann nicht entfernt werden")

var registerNaturalOnce sync.Once

func registerNaturalCollation() {
	registerNaturalOnce.Do(func() {
		sqlite.MustRegisterCollationUtf8("NATSORT", naturalCompare)
	})
}

type Store struct {
	db *sql.DB
	// path: Dateipfad der SQLite-DB, wie an Open() übergeben — gebraucht für
	// Backup (VACUUM INTO) und Restore (Dateitausch), siehe backup.go.
	path string
	// forceAdminOnlyLibraries: siehe hardening.go / SetForceAdminOnlyLibraries.
	forceAdminOnlyLibraries map[string]bool
}

func Open(path string) (*Store, error) {
	registerNaturalCollation()
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// --- Libraries ---

// --- Items ---

// ItemFilter fasst optionale Filter für ListItems zusammen.
type ItemFilter struct {
	LibraryID  int64   // einzelne Library (Legacy)
	LibraryIDs []int64 // mehrere Libraries (virtuelle Zusammenlegung); wird mit LibraryID ODER verknüpft
	Search     string
	Sort       string
	SortDir    string // "asc" | "desc" | "" (= Default-Richtung des Sort-Feldes)
	DateFrom   time.Time
	DateTo     time.Time
	Folder     string
	Watched    string
	Favorite   string
	// RatingFilter: "" (aus) | "unrated" (0 Sterne) | "min1" | "min2" | "exact3"
	// — persönliche Sternebewertung (user_item_state.rating), spiegelt den
	// Filter der Mac/iOS-App.
	RatingFilter string
	MatchState   string
	DupesOnly    bool // true = nur Items mit mehrfach vergebener metadata_id
	// FileDupesOnly: nur Items, deren (size_bytes, duration_sec) mit einem
	// anderen Item im selben Scope (LibraryID/LibraryIDs + Folder) übereinstimmt.
	// Ergänzung zu DupesOnly für Bibliotheken ohne TMDB-Metadata (kind=private) —
	// dort ist metadata_id meist NULL, sodass DupesOnly nie greift. Erkennt
	// z.B. versehentlich zweimal heruntergeladene identische Videodateien.
	FileDupesOnly bool
	MetadataID    int64 // 0 = aus; sonst nur Items mit exakt dieser metadata_id (Variants-Fetch)
	PersonTMDB    int64 // 0 = aus; sonst nur Items, deren Metadata (oder Parent-Show bei Episoden) diese Person listet
	PlaylistID    int64 // 0 = aus; sonst nur Items, die in dieser Playlist liegen (fuer Shuffle/Zufall in Playlist-Ansicht)
	MusicAlbumID  int64 // 0 = aus; sonst nur Tracks dieses Albums (fuer Shuffle/Zufall innerhalb eines geoeffneten Albums)
	// ExcludeAudiobooks: Hörbücher (.m4b) aus dem Zufalls-Pool ausschließen
	// (User-Wunsch 2026-09-04: "Bei Zufall Play dürfen Hörbücher nicht
	// berücksichtigt werden") — Extension ist ein zuverlässigeres Signal als
	// Genre-Tags (die bei Hörbüchern oft fehlen/uneinheitlich sind).
	ExcludeAudiobooks bool
	MinHeight         int      // 0 = aus; sonst nur Items mit height >= MinHeight
	MaxHeight         int      // 0 = aus; sonst nur Items mit height <= MaxHeight (exakter Bucket über Min+Max)
	ResBuckets        []string // Multi-Select-Auflösungs-Filter: 4k/2k/1080p/720p/576p/540p/480p/360p; mehrere → OR
	Interlaced        bool     // true = nur Items, deren Video-Stream field_order ∉ {progressive, unknown, ""}
	TrickplayStatus   string   // "" | "failed" | "pending" | "done" — Filter im Trickplay-Manager
	UserID            int64    // 0 = ungesetzt (Worker-Kontext); sonst pro-User-Zustand laden
	// IsAdmin: true = keine user_library_access-Einschränkung (Admins sehen alle Bibliotheken).
	// Nur relevant wenn UserID > 0 — vom Aufrufer aus dem eingeloggten User zu setzen.
	IsAdmin bool
	// MaxAgeRating: wenn > 0, werden Items mit metadata.age_rating > Max
	// ausgeblendet. 0 = keine Beschränkung (Admin-Default).
	MaxAgeRating int
	// Folders: Multi-Ordner-Filter (Zufallswiedergabe mit Ordner-Auswahl).
	// Nicht leer → ersetzt LibraryID/LibraryIDs/Folder komplett und filtert
	// stattdessen auf die ODER-Verknüpfung dieser Selektoren (auch über
	// mehrere Libraries hinweg möglich).
	Folders []FolderSelector
	// Genres: Multi-Select-Genre-Filter (User-Wunsch 2026-09-06), mehrere →
	// OR. Global nutzbar (Filme/Serien UND Musik) — matcht wahlweise gegen
	// metadata.genres (TMDB-JSON-Array-String, z.B. `["Drama","Krimi"]`) ODER
	// items.genre (Musik-Tag-Wert). Beide Felder werden immer gemeinsam
	// geprüft statt nach Library-Kind zu unterscheiden — ein Musik-Genre wie
	// "Rock" kommt praktisch nie in TMDB-Genres vor und umgekehrt, echte
	// Kollisionen sind kein realistisches Risiko.
	Genres []string
}

// FolderSelector wählt einen Ordner (rekursiv inkl. Unterordner) oder eine
// ganze Library für den Multi-Ordner-Filter aus. Folder="" = ganze Library.
type FolderSelector struct {
	LibraryID int64
	Folder    string
}

// Folder beschreibt einen virtuellen Unterordner innerhalb einer Bibliothek.
type Folder struct {
	Name string `json:"name"` // voller relativer Pfad (z.B. "a/Siterips")
	// ItemCount = Anzahl EINDEUTIGER Episoden/Titel (Dubletten mit gleicher
	// metadata_id, z.B. zwei Qualitäts-Varianten derselben Folge, zählen nur
	// einmal), nicht die Anzahl Dateizeilen — siehe topLevelFolders.
	ItemCount   int             `json:"itemCount"`
	ThumbItemID int64           `json:"thumbItemId"`
	MetadataID  int64           `json:"metadataId,omitempty"`
	Metadata    *model.Metadata `json:"metadata,omitempty"`
	Drilldown   bool            `json:"drilldown"` // true = Klick zeigt Subfolder statt flacher Liste
	// AddedAt = MAX(items.added_at) aller Items in diesem Ordner (rekursiv wäre teurer,
	// Top-Level-Items reichen als Näherung). Ermöglicht Clients, Show-/Ordner-Kacheln nach
	// "Hinzugefügt" zu sortieren — der Browser sortiert Kacheln bisher immer nur alphabetisch
	// (grid.js `folderCollator`), die Apple-Apps nutzen das Feld seit 2026-09-03 clientseitig
	// für den Sort-Modus `.added` (ItemGridView.swift in GoldfishApple).
	AddedAt string `json:"addedAt,omitempty"`
	// MergedFolders: weitere physische Ordner, die auf dieselbe TMDB-Show gematcht sind
	// und deshalb hier zu EINER Kachel zusammengefasst wurden (siehe mergeFoldersBySameShow).
	// Rein informativ fürs Frontend (Tooltip o.ä.) — Klick auf die Kachel navigiert weiterhin
	// nur zu `Name` (dem Ordner mit den meisten Items), die hier gelisteten Ordner bleiben
	// über die normale Ordner-Navigation trotzdem erreichbar.
	MergedFolders []string `json:"mergedFolders,omitempty"`
}

// --- Trickplay ---

// --- Settings ---

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseDBTime ist defensiv: manche ffprobe-Tags kommen in Formaten, die
// modernc.org/sqlite beim Scan in time.Time nicht akzeptiert. Wir lesen als
// String ein und probieren mehrere Layouts. Leer/unparsebar → Zero-Time.
func parseDBTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// --- Metadata ---

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
