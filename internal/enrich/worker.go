// Package enrich reichert Bibliothek-Items mit TMDB-Metadaten an.
// Läuft als Hintergrund-Goroutine, rate-limited über den TMDB-Client.
package enrich

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/nameparser"
	"github.com/boernie77/goldfish/internal/omdb"
	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/tmdb"
)

type Worker struct {
	store     *store.Store
	client    *tmdb.Client
	omdb      *omdb.Client
	posterDir string

	mu      sync.Mutex
	running bool
	status  Status
	trigger chan struct{}

	refreshAllMu sync.Mutex
	refreshAll   RefreshAllStatus
}

type Status struct {
	Running       bool      `json:"running"`
	LastRun       time.Time `json:"lastRun,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
	ItemsTotal    int       `json:"itemsTotal"`
	ItemsMatched  int       `json:"itemsMatched"`
	ItemsFailed   int       `json:"itemsFailed"`
	FoldersTotal  int       `json:"foldersTotal"`
	FoldersMatched int      `json:"foldersMatched"`
}

// RefreshAllStatus beschreibt den Fortschritt eines Bulk-Refresh-Laufs
// („alle Metadaten neu laden"). Wird vom UI gepollt.
type RefreshAllStatus struct {
	Running    bool      `json:"running"`
	Total      int       `json:"total"`
	Done       int       `json:"done"`
	Updated    int       `json:"updated"`
	Failed     int       `json:"failed"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	LastError  string    `json:"lastError,omitempty"`
	Current    string    `json:"current,omitempty"`
}

// New erzeugt einen Worker. `client` kann mit leerem API-Key initialisiert werden —
// der Worker wartet dann einfach bis ein Key gesetzt ist.
func New(s *store.Store, client *tmdb.Client, posterDir string) *Worker {
	_ = os.MkdirAll(posterDir, 0o755)
	return &Worker{
		store:     s,
		client:    client,
		posterDir: posterDir,
		trigger:   make(chan struct{}, 1),
	}
}

// Client gibt den aktuellen Client zurück (darf nach Key-Update ausgetauscht werden).
func (w *Worker) Client() *tmdb.Client { return w.client }

// SetClient ersetzt den Client (z.B. nach Settings-Änderung).
func (w *Worker) SetClient(c *tmdb.Client) {
	w.mu.Lock()
	w.client = c
	w.mu.Unlock()
}

// OMDb gibt den aktuellen OMDb-Fallback-Client zurück.
func (w *Worker) OMDb() *omdb.Client { return w.omdb }

// SetOMDb ersetzt den OMDb-Client.
func (w *Worker) SetOMDb(c *omdb.Client) {
	w.mu.Lock()
	w.omdb = c
	w.mu.Unlock()
}

func (w *Worker) PosterDir() string { return w.posterDir }

// Trigger stößt einen Enrichment-Lauf an (non-blocking).
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// Run blockiert bis der Kontext abläuft. Startet Enrichment-Läufe alle 5 Minuten
// oder wenn Trigger() aufgerufen wurde.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	// Initial kurz warten, dann sofort laufen
	time.Sleep(3 * time.Second)
	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.runOnce(ctx)
		case <-w.trigger:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	if !w.client.Enabled() {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.status = Status{Running: true}
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.status.Running = false
		w.status.LastRun = time.Now()
		w.running = false
		w.mu.Unlock()
	}()

	if err := w.enrichFolders(ctx); err != nil {
		log.Printf("[enrich] folders: %v", err)
		w.mu.Lock()
		w.status.LastError = err.Error()
		w.mu.Unlock()
	}
	if err := w.enrichItems(ctx); err != nil {
		log.Printf("[enrich] items: %v", err)
		w.mu.Lock()
		w.status.LastError = err.Error()
		w.mu.Unlock()
	}
	// Cast-Backfill: holt Credits für bestehende Metadata-Einträge, bei denen
	// noch keine Schauspieler-Daten vorhanden sind (z. B. vor Cast-Feature angelegt).
	if err := w.backfillCast(ctx); err != nil {
		log.Printf("[enrich] cast-backfill: %v", err)
	}
	// Collection-Backfill: holt `belongs_to_collection` für alle bereits
	// gematchten Filme, bei denen es noch nicht geprüft wurde (vor Collections-Feature).
	if err := w.backfillCollections(ctx); err != nil {
		log.Printf("[enrich] collection-backfill: %v", err)
	}
	// Collection-Parts: alle Sammlungs-Teile bei TMDB abrufen, damit fehlende
	// Filme in der UI als Platzhalter erscheinen können.
	if err := w.backfillCollectionParts(ctx); err != nil {
		log.Printf("[enrich] collection-parts: %v", err)
	}
}

// backfillCollectionParts holt pro Sammlung via /collection/{id} die Teile-Liste
// (auch Filme die der User nicht hat) und speichert sie zur UI-Anzeige.
func (w *Worker) backfillCollectionParts(ctx context.Context) error {
	if !w.client.Enabled() {
		return nil
	}
	const batch = 50
	cols, err := w.store.CollectionsNeedingPartsFetch(batch)
	if err != nil {
		return err
	}
	for _, c := range cols {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		det, err := w.client.GetCollection(ctx, c.TMDBID)
		if err != nil {
			log.Printf("[enrich] collection %d parts: %v", c.TMDBID, err)
			continue
		}
		parts := make([]store.CollectionPartRow, 0, len(det.Parts))
		for i, p := range det.Parts {
			// TMDB-Platzhalter überspringen: angekündigte/unfertige Filme ohne
			// Release-Datum UND ohne Poster sind nicht abrufbar und sollten nicht
			// als "Fehlt" in der Sammlung auftauchen.
			if p.ReleaseDate == "" && p.PosterPath == "" {
				continue
			}
			parts = append(parts, store.CollectionPartRow{
				TMDBMovieID: p.ID,
				Title:       p.Title,
				ReleaseDate: p.ReleaseDate,
				PosterPath:  p.PosterPath,
				Ord:         i,
			})
		}
		if err := w.store.ReplaceCollectionParts(c.ID, parts); err != nil {
			log.Printf("[enrich] store parts %d: %v", c.TMDBID, err)
			continue
		}
	}
	return nil
}

// backfillCollections iteriert Movie-Metadata ohne `collection_checked_at`,
// holt die Movie-Details bei TMDB und verknüpft Collection (falls vorhanden).
func (w *Worker) backfillCollections(ctx context.Context) error {
	if !w.client.Enabled() {
		return nil
	}
	const batch = 100
	metas, err := w.store.MoviesNeedingCollectionCheck(batch)
	if err != nil {
		return err
	}
	for _, mm := range metas {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m, err := w.client.GetMovie(ctx, mm.TMDBID)
		if err != nil {
			log.Printf("[enrich] collection-backfill %d: %v", mm.TMDBID, err)
			continue
		}
		if m.BelongsToCollection != nil && m.BelongsToCollection.ID > 0 {
			c := m.BelongsToCollection
			if cid, err := w.store.UpsertCollection(c.ID, c.Name, c.PosterPath, c.BackdropPath); err == nil {
				_ = w.store.SetMetadataCollection(mm.ID, cid)
				if c.PosterPath != "" {
					w.cachePoster(ctx, -cid, c.PosterPath, "w342")
				}
			}
		}
		_ = w.store.MarkCollectionChecked(mm.ID)
	}
	return nil
}

// enrichFolders matcht Top-Level-Ordner in TV-Bibliotheken als Shows.
func (w *Worker) enrichFolders(ctx context.Context) error {
	folders, err := w.store.PendingFolders(200)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.status.FoldersTotal = len(folders)
	w.status.FoldersMatched = 0
	w.mu.Unlock()

	for _, f := range folders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.matchShow(ctx, f.LibraryID, f.Folder); err != nil {
			log.Printf("[enrich] show %q: %v", f.Folder, err)
			continue
		}
		w.mu.Lock()
		w.status.FoldersMatched++
		w.mu.Unlock()
	}
	return nil
}

// enrichItems matcht einzelne Items als Film oder Episode.
func (w *Worker) enrichItems(ctx context.Context) error {
	items, err := w.store.PendingItems(500)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.status.ItemsTotal = len(items)
	w.status.ItemsMatched = 0
	w.status.ItemsFailed = 0
	w.mu.Unlock()

	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lib, err := w.store.GetLibrary(it.LibraryID)
		if err != nil || lib == nil {
			continue
		}
		if lib.Kind == model.KindPrivate {
			continue
		}
		if err := w.matchItem(ctx, lib, it); err != nil {
			log.Printf("[enrich] %s: %v", it.Path, err)
			w.mu.Lock()
			w.status.ItemsFailed++
			w.mu.Unlock()
			continue
		}
		w.mu.Lock()
		w.status.ItemsMatched++
		w.mu.Unlock()
	}
	return nil
}

func (w *Worker) matchShow(ctx context.Context, libraryID int64, folder string) error {
	parsed := nameparser.ParseFolder(folder)
	if parsed.Title == "" {
		return errors.New("leerer Titel")
	}
	results, err := w.client.SearchTV(ctx, parsed.Title)
	if err != nil {
		return err
	}
	best := pickBest(results, parsed.Year)
	if best == nil {
		// Kein Treffer – setze NULL damit wir nicht endlos retry'en
		_ = w.store.SetFolderMetadata(libraryID, folder, 0)
		return errors.New("keine TMDB-TV-Treffer")
	}
	meta, err := w.fetchShowMetadata(ctx, best.ID)
	if err != nil {
		return err
	}
	if err := w.store.SetFolderMetadata(libraryID, folder, meta.ID); err != nil {
		return err
	}
	log.Printf("[enrich] show %q → %s (%d)", folder, meta.Title, meta.Year)
	return nil
}

func (w *Worker) matchItem(ctx context.Context, lib *model.Library, it model.Item) error {
	// TV-Kontext: Priorität liegt auf explizitem SxxExx in den Ordner-Segmenten —
	// Release-Dateinamen wie "tvs-911-dd51-dl-x264-108.mkv" würden sonst eine
	// zufällige 3-stellige Zahl (911) als S9E11 fehlinterpretieren und ALLE
	// Episoden der Serie auf dieselbe Metadata ziehen.
	var parsed nameparser.Parsed
	if lib.Kind == model.KindTV {
		// 1) Erst im Dateinamen nach SxxExx/NxN suchen (explizite Muster, keine numerischen Raten).
		fileParsed := nameparser.ParseFileStrict(it.Path)
		if fileParsed.IsEpisode {
			parsed = fileParsed
		} else if it.RelPath != "" {
			// 2) Ordner-Segmente rückwärts mit demselben strikten Parser prüfen
			segs := strings.Split(it.RelPath, "/")
			for i := len(segs) - 2; i >= 0; i-- {
				folderParsed := nameparser.ParseSegmentStrict(segs[i])
				if folderParsed.IsEpisode {
					parsed.Season = folderParsed.Season
					parsed.Episode = folderParsed.Episode
					parsed.EpisodeEnd = folderParsed.EpisodeEnd
					parsed.IsEpisode = true
					break
				}
			}
		}
		// 3) Als letztes greift der aggressive Parser auf die Datei (inkl. numerischer
		//    3-4-stelliger Episode-Codes wie "104" → S1E04 für "Derrick 104.avi").
		if !parsed.IsEpisode {
			parsed = nameparser.ParseEpisodeFile(it.Path)
		}
	} else {
		parsed = nameparser.ParseFile(it.Path)
	}

	switch lib.Kind {
	case model.KindMovies:
		// Grundkandidaten: File-Name + alle Ordner-Segmente rückwärts.
		base := []nameparser.Parsed{}
		if parsed.Title != "" {
			base = append(base, parsed)
		}
		if it.RelPath != "" {
			segs := strings.Split(it.RelPath, "/")
			for i := len(segs) - 2; i >= 0; i-- {
				c := nameparser.ParseFile(segs[i])
				if c.Title != "" {
					base = append(base, c)
				}
			}
		}
		// Erweiterte Kandidaten: pro Basis noch De-Leet- und Längstes-Token-Varianten.
		// So werden obfuskierte Releases wie "Undispu73d" → "Undisputed" und
		// vertauschte Titel wie "Sitrb Langsam" → "Langsam" gefunden.
		candidates := []nameparser.Parsed{}
		seen := map[string]struct{}{}
		for _, b := range base {
			for _, v := range nameparser.ExpandCandidates(b) {
				key := strings.ToLower(v.Title) + "|" + itoaLocal(v.Year)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, v)
			}
		}
		if len(candidates) == 0 {
			return errors.New("leerer Titel auf allen Ebenen")
		}
		// Jeden Kandidaten nacheinander bei TMDB und notfalls OMDb probieren.
		for _, c := range candidates {
			results, err := w.client.SearchMovie(ctx, c.Title, c.Year)
			if err != nil {
				return err
			}
			if best := pickBest(results, c.Year); best != nil {
				meta, err := w.fetchMovieMetadata(ctx, best.ID)
				if err != nil {
					return err
				}
				return w.store.SetItemMetadata(it.ID, meta.ID)
			}
			// OMDb-Fallback — mit Year wenn vorhanden, sonst ohne. Die interne
			// Kaskade in enrichItemViaOMDb probiert zuerst strikten Title-Match,
			// dann Loose-Search, sodass kleine Titel-Abweichungen (z.B.
			// „und" vs „&") toleriert werden. Zu strenge Vorfilter hier würden
			// dem Fallback wieder die Luft nehmen.
			if meta, err := w.enrichItemViaOMDb(ctx, c.Title, c.Year); err == nil && meta != nil {
				log.Printf("[enrich] OMDb-Fallback (%q %d) → %s", c.Title, c.Year, meta.Title)
				return w.store.SetItemMetadata(it.ID, meta.ID)
			}
		}
		return errors.New("kein Film-Treffer auf allen Ebenen (TMDB + OMDb)")

	case model.KindTV:
		if !parsed.IsEpisode {
			return errors.New("kein Episodenformat SxxExx im Namen")
		}
		// Show-ID über den Top-Level-Ordner
		folder := topFolder(it.RelPath)
		if folder == "" {
			return errors.New("episode ohne Show-Ordner")
		}
		showMetaID, err := w.store.GetFolderMetadataID(lib.ID, folder)
		if err != nil {
			return err
		}
		if showMetaID == 0 {
			// Show-Match ist noch nicht gelaufen oder gescheitert – trigger jetzt
			if err := w.matchShow(ctx, lib.ID, folder); err != nil {
				return err
			}
			showMetaID, err = w.store.GetFolderMetadataID(lib.ID, folder)
			if err != nil || showMetaID == 0 {
				return errors.New("show konnte nicht gematcht werden")
			}
		}
		showMeta, err := w.store.GetMetadata(showMetaID)
		if err != nil || showMeta == nil {
			return errors.New("Show-Metadata nicht auffindbar")
		}
		ep, err := w.client.GetEpisode(ctx, showMeta.TMDBID, parsed.Season, parsed.Episode)
		if err != nil {
			return err
		}
		epMeta := &model.Metadata{
			TMDBType:    "episode",
			TMDBID:      ep.ID,
			ParentID:    showMeta.ID,
			Title:       ep.Name,
			Year:        showMeta.Year,
			ReleaseDate: tmdb.ParseDate(ep.AirDate),
			Overview:    ep.Overview,
			Rating:      ep.VoteAverage,
			RuntimeMin:  ep.Runtime,
			PosterPath:  ep.StillPath,
			Season:      ep.SeasonNumber,
			Episode:     ep.EpisodeNumber,
		}
		id, err := w.store.UpsertMetadata(epMeta)
		if err != nil {
			return err
		}
		// Episoden-Still als Poster cachen (fällt zurück auf Show-Poster falls leer)
		posterPath := ep.StillPath
		if posterPath == "" {
			posterPath = showMeta.PosterPath
		}
		w.cachePoster(ctx, id, posterPath, "w342")
		w.fetchEpisodeGuests(ctx, id, showMeta.TMDBID, ep.SeasonNumber, ep.EpisodeNumber)
		if err := w.store.SetItemMetadata(it.ID, id); err != nil {
			return err
		}
		// Doppelfolge (S07E23E24) → episode_end auf dem Item setzen. Die
		// zusätzlich mitgerissenen Episoden werden in der Staffel-Ansicht als
		// owned angezeigt, alle zeigen auf dasselbe Item.
		if parsed.EpisodeEnd > parsed.Episode {
			_ = w.store.SetItemEpisodeEnd(it.ID, parsed.EpisodeEnd)
		} else {
			_ = w.store.SetItemEpisodeEnd(it.ID, 0)
		}
		return nil
	}
	return nil
}

// resolveCumulativeEpisode versucht, eine durch den User/Encoder kumulativ
// nummerierte Episode (z. B. „S01E10" obwohl Staffel 1 nur 9 Episoden hat) auf
// die korrekte TMDB-(Staffel, Episode)-Kombination abzubilden. Liefert
// `ok=false`, wenn keine plausible Übersetzung gefunden wurde.
func (w *Worker) resolveCumulativeEpisode(ctx context.Context, showTMDBID int64, requestedSeason, requestedEpisode int) (*tmdb.Episode, int, int, bool) {
	show, err := w.client.GetTV(ctx, showTMDBID)
	if err != nil || show == nil {
		return nil, 0, 0, false
	}
	// Globale Episode-Nummer: Summe aller Episoden in Staffeln < requestedSeason,
	// + die vom User angefragte Episode. Specials (Staffel 0) werden ignoriert.
	targetGlobal := requestedEpisode
	for _, s := range show.Seasons {
		if s.SeasonNumber == 0 {
			continue
		}
		if s.SeasonNumber < requestedSeason {
			targetGlobal += s.EpisodeCount
		}
	}
	// Iteriere durch die echten Staffeln und finde die, in die `targetGlobal` fällt.
	cumulative := 0
	for _, s := range show.Seasons {
		if s.SeasonNumber == 0 {
			continue
		}
		if cumulative+s.EpisodeCount >= targetGlobal {
			actualSeason := s.SeasonNumber
			actualEpisode := targetGlobal - cumulative
			if actualSeason == requestedSeason && actualEpisode == requestedEpisode {
				return nil, 0, 0, false // gleiche Kombi → keine Verbesserung
			}
			ep, err := w.client.GetEpisode(ctx, showTMDBID, actualSeason, actualEpisode)
			if err != nil || ep == nil {
				return nil, 0, 0, false
			}
			return ep, actualSeason, actualEpisode, true
		}
		cumulative += s.EpisodeCount
	}
	return nil, 0, 0, false
}

// FetchMovieMetadata ist die exportierte Variante von fetchMovieMetadata.
// Wird vom manuellen Match-Handler genutzt, damit dort Cast, Poster, FSK
// und Collection-Verknüpfung ebenfalls gezogen werden.
func (w *Worker) FetchMovieMetadata(ctx context.Context, tmdbID int64) (*model.Metadata, error) {
	return w.fetchMovieMetadata(ctx, tmdbID)
}

// FetchShowMetadata: exportierte Variante.
func (w *Worker) FetchShowMetadata(ctx context.Context, tmdbID int64) (*model.Metadata, error) {
	return w.fetchShowMetadata(ctx, tmdbID)
}

// FetchEpisodeMetadata kapselt das, was matchItem für TV-Episoden macht:
// Episode aus TMDB holen, Metadata anlegen, Poster-Cache, Cast-Backfill,
// FSK ist optional (Episode-FSK ist in TMDB selten gepflegt — Parent-Show
// liefert die FSK über das Detail-Dialog-Fallback).
func (w *Worker) FetchEpisodeMetadata(ctx context.Context, showTMDBID int64, season, episode int) (*model.Metadata, error) {
	showMeta, err := w.fetchShowMetadata(ctx, showTMDBID)
	if err != nil || showMeta == nil {
		return nil, err
	}
	ep, err := w.client.GetEpisode(ctx, showTMDBID, season, episode)
	if err != nil {
		// Auto-Rollover: viele deutsche Releases zählen Episoden kumulativ
		// durch ("S01E10" für die 10. Folge insgesamt), während TMDB sie auf
		// mehrere Staffeln aufteilt. Wenn der erste GetEpisode-Call 404
		// liefert, rechnen wir die globale Episode-Nummer aus und finden
		// die richtige (S', E')-Kombination.
		if ep2, sNew, eNew, ok := w.resolveCumulativeEpisode(ctx, showTMDBID, season, episode); ok {
			log.Printf("[match] episode-rollover S%02dE%02d → S%02dE%02d für show %d (%s)",
				season, episode, sNew, eNew, showTMDBID, showMeta.Title)
			ep = ep2
			err = nil
		}
		if err != nil {
			return nil, err
		}
	}
	epMeta := &model.Metadata{
		TMDBType:    "episode",
		TMDBID:      ep.ID,
		ParentID:    showMeta.ID,
		Title:       ep.Name,
		Year:        showMeta.Year,
		ReleaseDate: tmdb.ParseDate(ep.AirDate),
		Overview:    ep.Overview,
		Rating:      ep.VoteAverage,
		RuntimeMin:  ep.Runtime,
		PosterPath:  ep.StillPath,
		Season:      ep.SeasonNumber,
		Episode:     ep.EpisodeNumber,
	}
	id, err := w.store.UpsertMetadata(epMeta)
	if err != nil {
		return nil, err
	}
	epMeta.ID = id
	posterPath := ep.StillPath
	if posterPath == "" {
		posterPath = showMeta.PosterPath
	}
	w.cachePoster(ctx, id, posterPath, "w342")
	w.fetchEpisodeGuests(ctx, id, showTMDBID, ep.SeasonNumber, ep.EpisodeNumber)
	return epMeta, nil
}

// RefreshItemMetadata zieht TMDB-Daten für die bestehende Zuordnung eines
// Items frisch (ohne re-matching), und füllt leer gebliebene Felder ggf.
// aus OMDb nach. Das Item bleibt unverändert verknüpft (metadata_id /
// metadata_confirmed nicht angefasst).
func (w *Worker) RefreshItemMetadata(ctx context.Context, item *model.Item) (*model.Metadata, error) {
	if item == nil || item.MetadataID == 0 {
		return nil, errors.New("item hat keine TMDB-Zuordnung")
	}
	return w.refreshMetadataByID(ctx, item.MetadataID)
}

// refreshMetadataByID ist der Kern von RefreshItemMetadata + RefreshAllMetadata:
// holt TMDB-Daten für die gegebene Metadata-ID frisch und füllt leere Felder
// per OMDb nach. Liefert das aktualisierte Metadata-Objekt.
func (w *Worker) refreshMetadataByID(ctx context.Context, metaID int64) (*model.Metadata, error) {
	cur, err := w.store.GetMetadata(metaID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, errors.New("metadata nicht gefunden")
	}
	if w.client == nil || !w.client.Enabled() {
		return nil, errors.New("TMDB-Key nicht konfiguriert")
	}
	var refreshed *model.Metadata
	switch cur.TMDBType {
	case "movie":
		refreshed, err = w.fetchMovieMetadata(ctx, cur.TMDBID)
	case "tv":
		refreshed, err = w.fetchShowMetadata(ctx, cur.TMDBID)
	case "episode":
		var parent *model.Metadata
		if cur.ParentID > 0 {
			parent, _ = w.store.GetMetadata(cur.ParentID)
		}
		if parent == nil {
			return nil, errors.New("episode hat keinen Parent-Show — manuell neu zuordnen")
		}
		refreshed, err = w.FetchEpisodeMetadata(ctx, parent.TMDBID, cur.Season, cur.Episode)
	default:
		return nil, errors.New("unbekannter TMDB-Typ: " + cur.TMDBType)
	}
	if err != nil {
		return nil, err
	}
	if refreshed == nil {
		return nil, errors.New("kein Ergebnis von TMDB")
	}
	if w.omdb != nil && w.omdb.Enabled() && refreshed.IMDBID != "" {
		needsFill := refreshed.Overview == "" || refreshed.RuntimeMin == 0 ||
			refreshed.Genres == "" || refreshed.Genres == "[]"
		if needsFill {
			if r, oerr := w.omdb.ByIMDb(ctx, refreshed.IMDBID); oerr == nil && r != nil {
				changed := false
				if refreshed.Overview == "" && r.Overview != "" {
					refreshed.Overview = r.Overview
					changed = true
				}
				if refreshed.RuntimeMin == 0 && r.Runtime > 0 {
					refreshed.RuntimeMin = r.Runtime
					changed = true
				}
				if (refreshed.Genres == "" || refreshed.Genres == "[]") && r.Genre != "" {
					parts := strings.Split(r.Genre, ", ")
					b, _ := json.Marshal(parts)
					if len(b) > 0 {
						refreshed.Genres = string(b)
						changed = true
					}
				}
				if refreshed.Year == 0 && r.Year > 0 {
					refreshed.Year = r.Year
					changed = true
				}
				if changed {
					if _, uerr := w.store.UpsertMetadata(refreshed); uerr != nil {
						log.Printf("[refresh] OMDb-Fallback Upsert für meta=%d: %v", metaID, uerr)
					}
				}
			}
		}
	}
	return refreshed, nil
}

// RefreshAllStatus liefert den aktuellen Bulk-Refresh-Stand (für UI-Polling).
func (w *Worker) RefreshAllStatus() RefreshAllStatus {
	w.refreshAllMu.Lock()
	defer w.refreshAllMu.Unlock()
	return w.refreshAll
}

// StartRefreshAllMetadata stößt einen Hintergrund-Lauf an, der ALLE
// Metadata-Einträge mit gesetzter TMDB-ID frisch von TMDB (+ OMDb-Fallback)
// nachzieht. Liefert false zurück wenn schon ein Lauf aktiv ist.
func (w *Worker) StartRefreshAllMetadata() bool {
	w.refreshAllMu.Lock()
	if w.refreshAll.Running {
		w.refreshAllMu.Unlock()
		return false
	}
	w.refreshAll = RefreshAllStatus{Running: true, StartedAt: time.Now()}
	w.refreshAllMu.Unlock()

	go func() {
		ctx := context.Background()
		ids, err := w.store.ListMetadataIDsForRefresh()
		if err != nil {
			w.refreshAllMu.Lock()
			w.refreshAll.Running = false
			w.refreshAll.LastError = err.Error()
			w.refreshAll.FinishedAt = time.Now()
			w.refreshAllMu.Unlock()
			return
		}
		w.refreshAllMu.Lock()
		w.refreshAll.Total = len(ids)
		w.refreshAllMu.Unlock()
		log.Printf("[refresh-all] starte: %d Metadata-Einträge", len(ids))
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			meta, err := w.refreshMetadataByID(ctx, id)
			w.refreshAllMu.Lock()
			w.refreshAll.Done++
			if err != nil {
				w.refreshAll.Failed++
				w.refreshAll.LastError = err.Error()
			} else if meta != nil {
				w.refreshAll.Updated++
				w.refreshAll.Current = meta.Title
			}
			w.refreshAllMu.Unlock()
		}
		w.refreshAllMu.Lock()
		w.refreshAll.Running = false
		w.refreshAll.FinishedAt = time.Now()
		w.refreshAll.Current = ""
		updated, failed, total := w.refreshAll.Updated, w.refreshAll.Failed, w.refreshAll.Total
		lastErr := w.refreshAll.LastError
		w.refreshAllMu.Unlock()
		log.Printf("[refresh-all] fertig: %d/%d aktualisiert, %d Fehler%s",
			updated, total, failed,
			func() string {
				if lastErr != "" {
					return " (letzter Fehler: " + lastErr + ")"
				}
				return ""
			}())
	}()
	return true
}

// fetchMovieMetadata lädt Detail-Daten zu einem Film und legt einen Metadata-Eintrag an.
func (w *Worker) fetchMovieMetadata(ctx context.Context, tmdbID int64) (*model.Metadata, error) {
	m, err := w.client.GetMovie(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	meta := &model.Metadata{
		TMDBType:      "movie",
		TMDBID:        m.ID,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Year:          tmdbYear(m.ReleaseDate),
		ReleaseDate:   tmdb.ParseDate(m.ReleaseDate),
		Overview:      m.Overview,
		Rating:        m.VoteAverage,
		Genres:        tmdb.GenresString(m.Genres),
		RuntimeMin:    m.Runtime,
		PosterPath:    m.PosterPath,
		BackdropPath:  m.BackdropPath,
		IMDBID:        m.IMDBID,
	}
	id, err := w.store.UpsertMetadata(meta)
	if err != nil {
		return nil, err
	}
	meta.ID = id
	w.cachePoster(ctx, id, m.PosterPath, "w342")
	w.fetchMovieCast(ctx, id, m.ID)
	// Deutsche FSK über separaten Endpoint nachziehen — TMDBs Movie-Details
	// liefern KEINE Certifications. Nur setzen wenn noch leer (manuelle Edits
	// haben Vorrang).
	if fsk, err := w.client.MovieAgeRatingDE(ctx, m.ID); err == nil && fsk != "" {
		_ = w.store.SetMetadataAgeRatingIfEmpty(id, fsk)
		meta.AgeRating = fsk
	}
	// TMDB-Collection (James Bond, Star Wars, …) wenn vorhanden verknüpfen.
	if m.BelongsToCollection != nil && m.BelongsToCollection.ID > 0 {
		c := m.BelongsToCollection
		if cid, err := w.store.UpsertCollection(c.ID, c.Name, c.PosterPath, c.BackdropPath); err == nil {
			_ = w.store.SetMetadataCollection(id, cid)
			// Collection-Poster ebenfalls cachen
			if c.PosterPath != "" {
				w.cachePoster(ctx, -cid, c.PosterPath, "w342") // negative id als namespacing
			}
		}
	}
	return meta, nil
}

// fetchShowMetadata lädt Detail-Daten zu einer Serie und legt einen Metadata-Eintrag an.
func (w *Worker) fetchShowMetadata(ctx context.Context, tmdbID int64) (*model.Metadata, error) {
	t, err := w.client.GetTV(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	runtime := 0
	if len(t.EpisodeRuntime) > 0 {
		runtime = t.EpisodeRuntime[0]
	}
	meta := &model.Metadata{
		TMDBType:      "tv",
		TMDBID:        t.ID,
		Title:         t.Name,
		OriginalTitle: t.OriginalName,
		Year:          tmdbYear(t.FirstAirDate),
		ReleaseDate:   tmdb.ParseDate(t.FirstAirDate),
		Overview:      t.Overview,
		Rating:        t.VoteAverage,
		Genres:        tmdb.GenresString(t.Genres),
		RuntimeMin:    runtime,
		PosterPath:    t.PosterPath,
		BackdropPath:  t.BackdropPath,
	}
	id, err := w.store.UpsertMetadata(meta)
	if err != nil {
		return nil, err
	}
	meta.ID = id
	w.cachePoster(ctx, id, t.PosterPath, "w342")
	w.fetchShowCast(ctx, id, t.ID)
	if fsk, err := w.client.TVAgeRatingDE(ctx, t.ID); err == nil && fsk != "" {
		_ = w.store.SetMetadataAgeRatingIfEmpty(id, fsk)
		meta.AgeRating = fsk
	}
	return meta, nil
}

// cachePoster lädt das Poster einmalig lokal, Datei-Name basiert auf metadata-ID.
func (w *Worker) cachePoster(ctx context.Context, metaID int64, tmdbPath, size string) {
	if tmdbPath == "" {
		return
	}
	out := filepath.Join(w.posterDir, posterFilename(metaID, tmdbPath))
	if _, err := os.Stat(out); err == nil {
		return
	}
	data, _, err := w.client.DownloadPoster(ctx, tmdbPath, size)
	if err != nil {
		log.Printf("[enrich] poster %d: %v", metaID, err)
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		log.Printf("[enrich] save poster %d: %v", metaID, err)
	}
}

func posterFilename(metaID int64, tmdbPath string) string {
	sum := sha1.Sum([]byte(tmdbPath))
	return "poster_" + hex.EncodeToString(sum[:8]) + filepath.Ext(tmdbPath)
}

// PosterFile gibt den Dateipfad zum gecachten Poster zurück, falls vorhanden.
func (w *Worker) PosterFile(meta *model.Metadata) string {
	if meta == nil || meta.PosterPath == "" {
		return ""
	}
	p := filepath.Join(w.posterDir, posterFilename(meta.ID, meta.PosterPath))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// PosterPathFor liefert den Dateipfad eines gecachten Posters, anhand einer
// beliebigen ID (Metadata-ID oder negative Collection-ID). Gibt "" zurück
// wenn die Datei nicht existiert.
func (w *Worker) PosterPathFor(id int64, tmdbPath string) string {
	if tmdbPath == "" {
		return ""
	}
	p := filepath.Join(w.posterDir, posterFilename(id, tmdbPath))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// EnsureCollectionPosterCached lädt das Poster einer Sammlung bei Bedarf
// synchron herunter. Wir verwenden eine negative ID als Namespace im Cache-Dir,
// sodass es die Metadata-Poster nicht überschreibt.
func (w *Worker) EnsureCollectionPosterCached(ctx context.Context, collectionID int64, tmdbPath string) string {
	if tmdbPath == "" || collectionID == 0 {
		return ""
	}
	if p := w.PosterPathFor(-collectionID, tmdbPath); p != "" {
		return p
	}
	w.cachePoster(ctx, -collectionID, tmdbPath, "w342")
	return w.PosterPathFor(-collectionID, tmdbPath)
}

// EnsurePosterCached lädt das Poster bei Bedarf synchron.
func (w *Worker) EnsurePosterCached(ctx context.Context, meta *model.Metadata) string {
	if meta == nil || meta.PosterPath == "" {
		return ""
	}
	if p := w.PosterFile(meta); p != "" {
		return p
	}
	w.cachePoster(ctx, meta.ID, meta.PosterPath, "w342")
	return w.PosterFile(meta)
}

// EnrichFolderNow matcht alle Episoden im angegebenen Ordner sofort
// (wird aufgerufen nach manueller Show-Zuordnung). Nicht-blockierend per Goroutine.
func (w *Worker) EnrichFolderNow(libraryID int64, folder string) {
	go w.enrichFolderSync(libraryID, folder)
}

// EnrichAllFoldersNow durchsucht alle TV-Libraries nach Top-Level-Ordnern mit
// unmatched Items und stößt pro Ordner eine Enrichment-Session an.
// Eine Folder nach der anderen (sequentiell), um TMDB-Rate-Limit nicht zu sprengen.
// Non-blocking per Goroutine. Wird nach jedem Scan-Ende aufgerufen, damit der
// reguläre 5-Minuten-Worker nicht durch Queue-Order hunderte Items ignoriert.
func (w *Worker) EnrichAllFoldersNow() {
	go w.enrichAllFoldersSync()
}

func (w *Worker) enrichAllFoldersSync() {
	if !w.client.Enabled() {
		return
	}
	libs, err := w.store.ListLibraries()
	if err != nil {
		log.Printf("[enrich] list libs: %v", err)
		return
	}
	for _, lib := range libs {
		if lib.Kind != model.KindTV {
			continue
		}
		folders, err := w.store.ListTVFoldersWithUnmatched(lib.ID)
		if err != nil {
			log.Printf("[enrich] unmatched-folders lib=%d: %v", lib.ID, err)
			continue
		}
		for _, f := range folders {
			w.enrichFolderSync(lib.ID, f)
		}
	}
}

func (w *Worker) enrichFolderSync(libraryID int64, folder string) {
	if !w.client.Enabled() {
		return
	}
	lib, err := w.store.GetLibrary(libraryID)
	if err != nil || lib == nil || lib.Kind != model.KindTV {
		return
	}
	items, err := w.store.ListItems(store.ItemFilter{
		LibraryID: libraryID,
		Folder:    folder,
	})
	if err != nil {
		log.Printf("[enrich] folder-now %q: %v", folder, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	matched := 0
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		// Items in diesem Folder, die schon Metadata haben, überspringen
		if it.MetadataID > 0 {
			continue
		}
		if err := w.matchItem(ctx, lib, it); err != nil {
			log.Printf("[enrich] folder-now item %s: %v", it.Path, err)
			continue
		}
		matched++
	}
	log.Printf("[enrich] folder-now %q: %d Episoden gematcht", folder, matched)
}

// --- Helpers ---

// pickBest wählt das am besten passende Suchergebnis (bevorzugt Jahres-Match).
func pickBest(results []tmdb.SearchResult, year int) *tmdb.SearchResult {
	if len(results) == 0 {
		return nil
	}
	if year > 0 {
		for i := range results {
			if results[i].Year == year {
				return &results[i]
			}
		}
		// Jahresdifferenz ≤ 1 akzeptieren (Release-Year vs. Upload-Year)
		for i := range results {
			diff := results[i].Year - year
			if diff < 0 {
				diff = -diff
			}
			if diff <= 1 {
				return &results[i]
			}
		}
	}
	return &results[0]
}

func tmdbYear(dateStr string) int {
	t := tmdb.ParseDate(dateStr)
	if t.IsZero() {
		return 0
	}
	return t.Year()
}

// itoaLocal ist ein kleiner int-to-string-Helper, um strconv nicht bloß für einen
// Key-String importieren zu müssen.
func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func topFolder(relPath string) string {
	for i := 0; i < len(relPath); i++ {
		if relPath[i] == '/' {
			return relPath[:i]
		}
	}
	return ""
}
