package scanner

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// streamsFromProbe baut die strukturierte Stream-Liste für die DB aus ffprobe.
func streamsFromProbe(p probeResult) []model.ItemStream {
	out := make([]model.ItemStream, 0, len(p.Streams))
	for _, s := range p.Streams {
		if s.CodecType == "" || s.CodecType == "data" || s.CodecType == "attachment" {
			continue
		}
		st := model.ItemStream{
			Index:      s.Index,
			Type:       s.CodecType,
			Codec:      s.CodecName,
			Channels:   s.Channels,
			FieldOrder: s.FieldOrder,
		}
		if s.Tags != nil {
			if v, ok := s.Tags["language"]; ok {
				st.Language = v
			}
			if v, ok := s.Tags["title"]; ok {
				st.Title = v
			}
		}
		if s.Disposition != nil {
			if s.Disposition["default"] == 1 {
				st.IsDefault = true
			}
			if s.Disposition["forced"] == 1 {
				st.IsForced = true
			}
		}
		out = append(out, st)
	}
	return out
}

var supportedExt = map[string]bool{
	".mp4": true,
	".mkv": true,
	".mov": true,
	".avi": true,
	".wmv": true,
	// Musik-Bibliotheken (kind=music, seit 2026-09-04)
	".mp3":  true,
	".flac": true,
	".m4a":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
}

// musicExt: Teilmenge von supportedExt, die als Audio-Bibliothek behandelt
// wird (Tag-Extraktion statt Video-Thumbnail, siehe probeItem/makeThumbnail-
// Aufrufer in run()).
var musicExt = map[string]bool{
	".mp3":  true,
	".flac": true,
	".m4a":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
}

type Scanner struct {
	store    *store.Store
	thumbDir string
	// albumArtDir: Cache-Verzeichnis für Musik-Cover, gleiche Konvention wie
	// der Poster-Cache (siehe SetAlbumArtDir). Leer = Cover-Extraktion no-opt.
	albumArtDir string

	mu          sync.Mutex
	status      model.ScanStatus
	cancel      context.CancelFunc
	onComplete  func() // optionaler Callback nach Scan-Ende
	folderStats map[string]model.ScanFolderStats
	startedAt   time.Time
	// Detail-Listen für den Scan-Abschluss-Dialog: Vollständige rel_paths
	// pro Kategorie. Werden in das ScanSummary serialisiert, damit das UI
	// bei Klick auf eine Statistik die einzelnen Dateien anzeigen kann.
	newPaths     []string
	updatedPaths []string
	removedPaths []string
}

func New(s *store.Store, thumbDir string) *Scanner {
	_ = os.MkdirAll(thumbDir, 0o755)
	return &Scanner{store: s, thumbDir: thumbDir}
}

// SetAlbumArtDir setzt das Cache-Verzeichnis für aus Musikdateien extrahierte
// Cover (embedded APIC/PICTURE-Block). Wird in main.go auf denselben Ordner
// wie der Poster-Cache gesetzt (gleiche Flat-Dir-Konvention, eigener
// Dateiname-Präfix "album_").
func (sc *Scanner) SetAlbumArtDir(dir string) {
	sc.mu.Lock()
	sc.albumArtDir = dir
	sc.mu.Unlock()
}

// OnComplete registriert einen Callback, der nach jedem Scan-Ende aufgerufen wird.
func (sc *Scanner) OnComplete(fn func()) {
	sc.mu.Lock()
	sc.onComplete = fn
	sc.mu.Unlock()
}

func (sc *Scanner) Status() model.ScanStatus {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.status
}

func (sc *Scanner) Cancel() {
	sc.mu.Lock()
	if sc.cancel != nil {
		sc.cancel()
	}
	sc.mu.Unlock()
}

// Start kicks off a scan in the background. Returns error if one is already running.
// force=true ignoriert die mtime-Optimierung und probt jede Datei neu.
// Start startet einen Scan. folder="" bedeutet komplette Library; sonst wird nur der
// angegebene rel_path-Unterbaum gescannt und Orphan-Delete bleibt auf diesen Ordner
// beschränkt.
func (sc *Scanner) Start(lib model.Library, force bool, folder string) error {
	sc.mu.Lock()
	if sc.status.Running {
		sc.mu.Unlock()
		return fmt.Errorf("scan läuft bereits")
	}
	folder = strings.TrimSuffix(strings.TrimPrefix(folder, "/"), "/")
	ctx, cancel := context.WithCancel(context.Background())
	sc.cancel = cancel
	sc.status = model.ScanStatus{Running: true, LibraryID: lib.ID, Force: force, Folder: folder}
	sc.folderStats = map[string]model.ScanFolderStats{}
	sc.newPaths = nil
	sc.updatedPaths = nil
	sc.removedPaths = nil
	sc.startedAt = time.Now()
	sc.mu.Unlock()

	go func() {
		defer func() {
			sc.mu.Lock()
			sc.status.Running = false
			sc.cancel = nil
			// Summary-Snapshot für die UI bauen.
			summary := &model.ScanSummary{
				LibraryID:    lib.ID,
				LibraryName:  lib.Name,
				Folder:       folder,
				Force:        force,
				StartedAt:    sc.startedAt,
				FinishedAt:   time.Now(),
				Total:        sc.status.Total,
				New:          sc.status.New,
				Updated:      sc.status.Updated,
				Skipped:      sc.status.Skipped,
				Removed:      sc.status.Removed,
				Error:        sc.status.LastError,
				PerFolder:    sc.folderStats,
				NewPaths:     sc.newPaths,
				UpdatedPaths: sc.updatedPaths,
				RemovedPaths: sc.removedPaths,
			}
			sc.status.LastSummary = summary
			cb := sc.onComplete
			sc.mu.Unlock()
			if cb != nil {
				cb()
			}
		}()
		if err := sc.run(ctx, lib, force, folder); err != nil {
			log.Printf("[scan] lib=%d folder=%q error: %v", lib.ID, folder, err)
			sc.mu.Lock()
			sc.status.LastError = err.Error()
			sc.mu.Unlock()
		}
	}()
	return nil
}

func (sc *Scanner) run(ctx context.Context, lib model.Library, force bool, folder string) error {
	// Alle Quellordner der Bibliothek abfragen. Jede Datei trägt rel_path relativ
	// zum jeweiligen Quellpfad — dadurch tauchen gleichnamige Unterordner aus
	// verschiedenen Quellen als EIN "Ordner" in der UI auf (Multi-Path-Aggregation).
	paths, err := sc.store.LibraryPaths(lib.ID)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		paths = []string{lib.Path}
	}

	// Phase 1: Dateien sammeln (mit Info, aus welchem Root sie kommen)
	type fileRef struct {
		path string
		root string
	}
	var files []fileRef
	for _, root := range paths {
		startDir := root
		if folder != "" {
			// Scope: nur diesen Unterordner scannen. Wenn er im aktuellen Root nicht
			// existiert, überspringen — der Ordner kann aus einer anderen Multi-Path-
			// Quelle stammen.
			scoped := filepath.Join(root, folder)
			if st, err := os.Stat(scoped); err != nil || !st.IsDir() {
				continue
			}
			startDir = scoped
		}
		err := filepath.WalkDir(startDir, func(path string, d fs.DirEntry, err error) error {
			// Intentional: bei einzelnen Datei-Fehlern (Permission denied, mitten im
			// Scan verschwundene Datei) den Walk fortsetzen statt abzubrechen. nil
			// statt err returnen ist hier Programm — `filepath.SkipDir` waere zu
			// aggressiv (uebergeht den ganzen Ordner), und das Bubblen des Errors
			// wuerde den Library-Scan komplett scheitern lassen.
			if err != nil {
				return nil //nolint:nilerr
			}
			if d.IsDir() {
				// Sample-Ordner komplett überspringen — die Teaser-Files sind redundant
				// zum Hauptfilm, gehören thematisch dazu und machen nur Rauschen in der
				// Library-Liste.
				if strings.EqualFold(d.Name(), "Sample") || strings.EqualFold(d.Name(), "Samples") {
					return filepath.SkipDir
				}
				return nil
			}
			if supportedExt[strings.ToLower(filepath.Ext(path))] {
				files = append(files, fileRef{path: path, root: root})
			}
			return nil
		})
		if err != nil {
			log.Printf("[scan] walk %s: %v", root, err)
		}
	}

	sc.mu.Lock()
	sc.status.Total = len(files)
	sc.status.Done = 0
	sc.mu.Unlock()

	keep := map[string]struct{}{}

	for _, ref := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sc.mu.Lock()
		sc.status.Current = ref.path
		sc.mu.Unlock()

		info, err := os.Stat(ref.path)
		if err != nil {
			log.Printf("[scan] stat %s: %v", ref.path, err)
			continue
		}

		existingMT, exists, _ := sc.store.GetItemModTime(ref.path)
		if !force && exists && existingMT.Equal(info.ModTime()) {
			keep[ref.path] = struct{}{}
			sc.mu.Lock()
			sc.status.Done++
			sc.status.Skipped++
			sc.mu.Unlock()
			continue
		}

		item, err := sc.probeItem(ctx, lib, ref.root, ref.path, info)
		if err != nil {
			log.Printf("[scan] probe %s: %v", ref.path, err)
			sc.mu.Lock()
			sc.status.Done++
			sc.mu.Unlock()
			continue
		}

		// Musik-Dateien haben kein Video-Frame zum Thumbnail-Ziehen — Cover
		// kommt stattdessen einmalig pro Album nach der Album-Gruppierung
		// unten (extractAlbumCovers), nicht pro Track.
		if lib.Kind != model.KindMusic {
			sc.makeThumbnail(ctx, item)
		}
		if err := sc.store.UpsertItem(item); err != nil {
			log.Printf("[scan] upsert %s: %v", ref.path, err)
		}
		// Stream-Liste persistent machen (nach UpsertItem, damit item.ID stabil ist)
		if len(item.Streams) > 0 {
			if itID, _ := sc.store.ItemIDByPath(ref.path); itID > 0 {
				_ = sc.store.ReplaceItemStreams(itID, item.Streams)
			}
		}
		keep[ref.path] = struct{}{}

		// Top-Level-Ordner ableiten (rel_path[0]) für Per-Folder-Statistik.
		topFolder := topLevelFolder(item.RelPath)

		sc.mu.Lock()
		sc.status.Done++
		if exists {
			sc.status.Updated++
			sc.bumpFolderCounter(topFolder, false)
			sc.updatedPaths = append(sc.updatedPaths, item.RelPath)
		} else {
			sc.status.New++
			sc.bumpFolderCounter(topFolder, true)
			sc.newPaths = append(sc.newPaths, item.RelPath)
		}
		sc.mu.Unlock()
	}

	// Orphan-Cleanup. Per-Folder-Removed + Detail-Liste brauchen wir VOR
	// dem SQL-DELETE — die Items sind danach weg.
	removedPerFolder := map[string]int{}
	var orphans []string
	if folder == "" {
		orphans, _ = sc.store.ListItemPathsNotInSet(lib.ID, keep)
	} else {
		orphans, _ = sc.store.ListItemPathsInFolderNotInSet(lib.ID, folder, keep)
	}
	sc.mu.Lock()
	for _, p := range orphans {
		rel := relPathFor(paths, p)
		tf := topLevelFolder(rel)
		removedPerFolder[tf]++
		sc.removedPaths = append(sc.removedPaths, rel)
	}
	sc.mu.Unlock()
	var removed int
	if folder == "" {
		removed, err = sc.store.DeleteItemsNotInSet(lib.ID, keep)
	} else {
		removed, err = sc.store.DeleteItemsInFolderNotInSet(lib.ID, folder, keep)
	}
	if err != nil {
		return err
	}
	sc.mu.Lock()
	sc.status.Removed = removed
	for f, n := range removedPerFolder {
		stats := sc.folderStats[f]
		stats.Removed = n
		sc.folderStats[f] = stats
	}
	sc.mu.Unlock()
	log.Printf("[scan] lib=%d folder=%q fertig: %d Dateien aus %d Quellen, %d entfernt",
		lib.ID, folder, len(files), len(paths), removed)

	// Musik: Artist/Album-Gruppierung + Cover-Extraktion aus der Audiodatei
	// selbst (MusicBrainz/Cover-Art-Archive-Fallback läuft separat im
	// MusicWorker, getriggert über sc.OnComplete in main.go).
	if lib.Kind == model.KindMusic {
		if err := sc.store.GroupMusicAlbums(lib.ID); err != nil {
			log.Printf("[scan] music album grouping lib=%d: %v", lib.ID, err)
		} else {
			sc.extractAlbumCovers(ctx, lib.ID)
		}
	}
	return nil
}

// extractAlbumCovers zieht für jedes Album ohne Cover EINMAL das eingebettete
// Bild (ID3-APIC bzw. FLAC/Vorbis METADATA_BLOCK_PICTURE) aus der ersten
// Track-Datei — nicht pro Track, das Cover gehört zum Album. Fehlschlag (kein
// eingebettetes Bild vorhanden) ist der Normalfall bei vielen Dateien und
// fällt durch zum MusicWorker-Fallback (MusicBrainz+Cover Art Archive).
func (sc *Scanner) extractAlbumCovers(ctx context.Context, libraryID int64) {
	if sc.albumArtDir == "" {
		return
	}
	albums, err := sc.store.PendingAlbumsForCoverExtraction(libraryID)
	if err != nil {
		log.Printf("[scan] pending album covers lib=%d: %v", libraryID, err)
		return
	}
	for _, album := range albums {
		path, err := sc.store.FirstTrackPathForAlbum(album.ID)
		if err != nil || path == "" {
			continue
		}
		out := filepath.Join(sc.albumArtDir, fmt.Sprintf("album_%d.jpg", album.ID))
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		cmd := exec.CommandContext(cctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-i", path, "-an", "-vcodec", "copy", out)
		runErr := cmd.Run()
		cancel()
		if runErr != nil {
			// Kein eingebettetes Cover (häufigster Fall) oder ffmpeg-Fehler —
			// fällt durch zum MusicWorker-Fallback, cover_source bleibt ''.
			_ = os.Remove(out)
			continue
		}
		info, statErr := os.Stat(out)
		if statErr != nil || info.Size() == 0 {
			_ = os.Remove(out)
			continue
		}
		if err := sc.store.SetMusicAlbumCover(album.ID, "embedded", ""); err != nil {
			log.Printf("[scan] set album cover lib=%d album=%d: %v", libraryID, album.ID, err)
		}
	}
}

// topLevelFolder extrahiert das erste rel_path-Segment. Files direkt im
// Library-Root liefern "".
func topLevelFolder(rel string) string {
	if rel == "" {
		return ""
	}
	if idx := strings.Index(rel, "/"); idx > 0 {
		return rel[:idx]
	}
	return ""
}

// relPathFor sucht den passenden Library-Pfad-Prefix und liefert den Rest
// als rel_path. Fallback: leerer String.
func relPathFor(roots []string, fullPath string) string {
	for _, root := range roots {
		if strings.HasPrefix(fullPath, root+"/") {
			return strings.TrimPrefix(fullPath, root+"/")
		}
		if strings.HasPrefix(fullPath, root) {
			return strings.TrimPrefix(fullPath, root+"/")
		}
	}
	return ""
}

// bumpFolderCounter erhöht den New- oder Updated-Counter pro Folder.
// Caller MUSS sc.mu halten.
func (sc *Scanner) bumpFolderCounter(folder string, isNew bool) {
	if sc.folderStats == nil {
		sc.folderStats = map[string]model.ScanFolderStats{}
	}
	stats := sc.folderStats[folder]
	if isNew {
		stats.New++
	} else {
		stats.Updated++
	}
	sc.folderStats[folder] = stats
}

// --- ffprobe ---

type probeFormat struct {
	Duration string            `json:"duration"`
	Size     string            `json:"size"`
	BitRate  string            `json:"bit_rate"`
	Format   string            `json:"format_name"`
	Tags     map[string]string `json:"tags"`
}

type probeStream struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	Channels    int               `json:"channels"`
	FieldOrder  string            `json:"field_order"`
	Disposition map[string]int    `json:"disposition"`
	Tags        map[string]string `json:"tags"`
}

type probeResult struct {
	Format  probeFormat   `json:"format"`
	Streams []probeStream `json:"streams"`
}

func (sc *Scanner) probeItem(ctx context.Context, lib model.Library, root, path string, info os.FileInfo) (*model.Item, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var p probeResult
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, err
	}

	it := &model.Item{
		LibraryID:  lib.ID,
		Path:       path,
		Title:      strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Container:  normalizeContainer(p.Format.Format, filepath.Ext(path)),
		SizeBytes:  info.Size(),
		ModTime:    info.ModTime(),
		ReleasedAt: extractReleaseTime(p, info.ModTime()),
	}
	rel, err := filepath.Rel(root, path)
	if err == nil {
		it.RelPath = rel
	}
	if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil {
		it.DurationSec = d
	}
	if br, err := strconv.Atoi(p.Format.BitRate); err == nil {
		it.BitrateKbps = br / 1000
	}
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if it.VideoCodec == "" {
				it.VideoCodec = s.CodecName
				it.Width = s.Width
				it.Height = s.Height
			}
		case "audio":
			if it.AudioCodec == "" {
				it.AudioCodec = s.CodecName
			}
		}
	}
	it.Streams = streamsFromProbe(p)
	// Musik-Tags (ID3/FLAC/Vorbis, bereits in p.Format.Tags vorhanden — bisher
	// nur für extractReleaseTime genutzt). Titel-Tag wird bevorzugt vor der
	// Dateiname-Ableitung oben, da Musikdateien fast immer einen sinnvollen
	// eingebetteten Titel haben.
	if lib.Kind == model.KindMusic {
		it.Artist = lookupTag(p.Format.Tags, "artist", "album_artist")
		it.Album = lookupTag(p.Format.Tags, "album")
		if track := lookupTag(p.Format.Tags, "track"); track != "" {
			if idx := strings.Index(track, "/"); idx > 0 {
				track = track[:idx]
			}
			if n, err := strconv.Atoi(strings.TrimSpace(track)); err == nil {
				it.TrackNo = n
			}
		}
		if title := lookupTag(p.Format.Tags, "title"); title != "" {
			it.Title = title
		}
	}
	return it, nil
}

// lookupTag sucht case-insensitiv nach dem ersten passenden Tag-Wert (Matroska/
// Vorbis-Tags sind oft GROSSBUCHSTABEN, MP4/ID3 meist lower_snake_case).
func lookupTag(tags map[string]string, keys ...string) string {
	for k, v := range tags {
		if v == "" {
			continue
		}
		lk := strings.ToLower(k)
		for _, want := range keys {
			if lk == want {
				return v
			}
		}
	}
	return ""
}

// extractReleaseTime sucht in ffprobe-Tags nach einem plausiblen Aufnahme-/Erstelldatum.
// Priorität: yt-dlp-Upload-Tag DATE > QuickTime-creationdate > container-creation_time > stream-creation_time.
// Matching läuft case-insensitiv, da Matroska Tags in GROSSBUCHSTABEN schreibt (DATE),
// während MP4 lower_snake_case nutzt (creation_time).
func extractReleaseTime(p probeResult, fallback time.Time) time.Time {
	lookup := func(tags map[string]string, keys ...string) []string {
		// case-insensitive Suche
		norm := map[string]string{}
		for k, v := range tags {
			norm[strings.ToLower(k)] = v
		}
		var out []string
		for _, k := range keys {
			if v, ok := norm[strings.ToLower(k)]; ok && v != "" {
				out = append(out, v)
			}
		}
		return out
	}

	// 1. yt-dlp schreibt das YouTube-Upload-Datum als "DATE" (YYYYMMDD) ins Format-Tag.
	// 2. Apple QuickTime / Sony-Cams: "com.apple.quicktime.creationdate"
	// 3. Generisch: "creation_time", "date", "release_date", "recording_time"
	candidates := lookup(p.Format.Tags,
		"DATE", "date",
		"com.apple.quicktime.creationdate",
		"creation_time",
		"release_date",
		"recording_time",
	)
	for _, s := range p.Streams {
		candidates = append(candidates, lookup(s.Tags, "creation_time", "DATE", "date")...)
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"20060102", // yt-dlp
		"2006",
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		for _, l := range layouts {
			if t, err := time.Parse(l, c); err == nil && t.Year() > 1970 {
				return t
			}
		}
	}
	return fallback
}

func normalizeContainer(formatName, ext string) string {
	e := strings.TrimPrefix(strings.ToLower(ext), ".")
	if e != "" {
		return e
	}
	return strings.Split(formatName, ",")[0]
}

// --- Thumbnail ---

func (sc *Scanner) makeThumbnail(ctx context.Context, it *model.Item) {
	if it.DurationSec <= 0 {
		return
	}
	seek := it.DurationSec * 0.1
	if seek < 2 {
		seek = 2
	}
	if seek > 600 {
		seek = 600
	}

	sum := sha1.Sum([]byte(it.Path))
	fname := hex.EncodeToString(sum[:]) + ".jpg"
	out := filepath.Join(sc.thumbDir, fname)

	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", fmt.Sprintf("%.2f", seek),
		"-i", it.Path,
		"-vframes", "1",
		"-vf", "scale=480:-2",
		"-q:v", "5",
		out,
	)
	if err := cmd.Run(); err != nil {
		log.Printf("[thumb] %s: %v", it.Path, err)
		return
	}
	it.ThumbPath = out
	it.HasThumb = true
}
