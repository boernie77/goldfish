package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

func (s *Server) enrichStatus(w http.ResponseWriter, _ *http.Request) {
	if s.Enrich == nil {
		writeJSON(w, 200, map[string]any{"running": false})
		return
	}
	writeJSON(w, 200, s.Enrich.Status())
}

func (s *Server) runEnrich(w http.ResponseWriter, _ *http.Request) {
	if s.Enrich == nil {
		writeError(w, 503, "Enrichment nicht initialisiert")
		return
	}
	if !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	s.Enrich.Trigger()
	s.Enrich.EnrichAllFoldersNow()
	writeJSON(w, 202, map[string]string{"status": "triggered"})
}

// refreshCollectionParts setzt parts_fetched_at=NULL bei allen Collections,
// sodass der Enricher sie erneut abruft. Admin-Endpoint.
func (s *Server) refreshCollectionParts(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.ResetAllCollectionParts(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if s.Enrich != nil {
		s.Enrich.Trigger()
	}
	w.WriteHeader(204)
}

// unmatchDuplicates setzt metadata_id=NULL für TV-Items, bei denen zu viele
// Dateien auf dieselbe Episode-Metadata zeigen (typisch: Parser-Fehl-Matches).
// Startet anschließend Re-Enrichment.
func (s *Server) unmatchDuplicates(w http.ResponseWriter, r *http.Request) {
	threshold := 3
	if v := r.URL.Query().Get("threshold"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			threshold = n
		}
	}
	n, err := s.Store.UnmatchTVDuplicateEpisodes(threshold)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if s.Enrich != nil {
		s.Enrich.Trigger()
		s.Enrich.EnrichAllFoldersNow()
	}
	writeJSON(w, 200, map[string]any{"unmatched": n, "threshold": threshold})
}

// searchMetadata erlaubt dem Frontend eine TMDB-Suche für manuelles Matching.
// Query: ?type=movie|tv&q=<title>[&year=YYYY]
func (s *Server) searchMetadata(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	q := r.URL.Query()
	title := q.Get("q")
	if title == "" {
		writeError(w, 400, "q (Titel) fehlt")
		return
	}
	year := 0
	if v := q.Get("year"); v != "" {
		year, _ = strconv.Atoi(v)
	}
	client := s.Enrich.Client()
	ctx := r.Context()
	switch q.Get("type") {
	case "tv":
		res, err := client.SearchTV(ctx, title)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, res)
	default:
		res, err := client.SearchMovie(ctx, title, year)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, res)
	}
}

// setItemMetadata setzt manuell die TMDB-Zuordnung eines Items.
// Body: {"tmdbType":"movie|tv|episode","tmdbId":123,"season":1,"episode":2}
// Bei "movie" wird Film-Metadata erstellt/aktualisiert.
// Bei "tv" wird eine Show-Metadata angelegt (sinnvoll für Ordner-Match über Item-Endpoint ist selten – daher simpel gehalten).
// Bei "episode" wird Show-ID und Season/Episode über TMDB aufgelöst.
func (s *Server) setItemMetadata(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		TMDBType string `json:"tmdbType"`
		TMDBID   int64  `json:"tmdbId"`
		IMDBID   string `json:"imdbId"` // optionaler alternativer Eingang: "tt1234567"
		Season   int    `json:"season"`
		Episode  int    `json:"episode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	// IMDb-ID → TMDB oder OMDb-Fallback
	if body.TMDBID == 0 && body.IMDBID != "" {
		meta, err := s.Enrich.EnrichByIMDbID(r.Context(), body.IMDBID)
		if err != nil {
			writeError(w, 502, "IMDb-Lookup: "+err.Error())
			return
		}
		if meta == nil {
			writeError(w, 404, "IMDb-ID bei TMDB nicht gefunden und kein OMDb-Key / OMDb-Treffer")
			return
		}
		// Bereits als Metadata gespeichert → direkt an Item binden und als
		// bestätigt markieren (User hat bewusst zugeordnet).
		if err := s.Store.ConfirmItemMatch(id, meta.ID); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		// NFO gleich mitschreiben
		if it2, _ := s.Store.GetItem(id); it2 != nil {
			_, _ = s.writeNFOForItem(it2)
		}
		w.WriteHeader(204)
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	ctx := r.Context()
	var metaID int64
	// Manuelle Zuordnung läuft über die exportierten Enricher-Funktionen,
	// damit Cast, Genres, Collection-Verknüpfung, Poster und FSK genauso
	// geladen werden wie beim Auto-Match. Frühere Versionen haben hier
	// inline minimal-Metadata erstellt → keine Schauspieler, kein Genres,
	// kein FSK, was bei manuell gematchten Filmen sehr auffiel.
	switch body.TMDBType {
	case "movie":
		meta, err := s.Enrich.FetchMovieMetadata(ctx, body.TMDBID)
		if err != nil || meta == nil {
			writeError(w, 502, "TMDB-Fetch: "+errString(err))
			return
		}
		metaID = meta.ID
	case "episode":
		meta, err := s.Enrich.FetchEpisodeMetadata(ctx, body.TMDBID, body.Season, body.Episode)
		if err != nil || meta == nil {
			writeError(w, 502, "TMDB-Fetch: "+errString(err))
			return
		}
		metaID = meta.ID
	default:
		writeError(w, 400, "tmdbType muss 'movie' oder 'episode' sein")
		return
	}
	// Manuelle Zuordnung → als bestätigt markieren, damit Scan/Enricher
	// sie zukünftig nicht überschreibt und sie nicht als verdächtig gilt.
	if err := s.Store.ConfirmItemMatch(id, metaID); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it2, _ := s.Store.GetItem(id); it2 != nil {
		_, _ = s.writeNFOForItem(it2)
	}
	w.WriteHeader(204)
}

// autoMergeDuplicates durchsucht alle Ordner der Library und führt Items mit
// gleichem Parent-Folder zusammen, sobald im Ordner *genau eine* TMDB-Zuordnung
// vorhandensein ist. Items ohne Zuordnung oder mit der gleichen Zuordnung werden
// auf diese canonical metadata_id gesetzt. Ordner mit mehreren konkurrierenden
// Zuordnungen (echte Sammlungen oder Trilogien) bleiben unberührt — der User
// muss dort manuell entscheiden.
// Nur Bibliotheken vom Typ "movies" werden verarbeitet (TV-Ordner enthalten
// legitim unterschiedliche Episoden-Metadata).
// POST /api/libraries/{id}/auto-merge-duplicates
func (s *Server) autoMergeDuplicates(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lib, err := s.Store.GetLibrary(libID)
	if err != nil || lib == nil {
		writeError(w, 404, "Bibliothek nicht gefunden")
		return
	}
	if lib.Kind != "movies" {
		writeError(w, 400, "Auto-Merge ist nur für Movies-Bibliotheken sinnvoll")
		return
	}
	items, err := s.Store.ListItems(store.ItemFilter{LibraryID: libID})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Gruppierung nach Parent-Folder aus rel_path
	byFolder := map[string][]model.Item{}
	for _, it := range items {
		p := it.RelPath
		idx := strings.LastIndex(p, "/")
		parent := ""
		if idx >= 0 {
			parent = p[:idx]
		}
		byFolder[parent] = append(byFolder[parent], it)
	}
	foldersTouched := 0
	itemsUpdated := 0
	skippedConflicts := 0
	for _, group := range byFolder {
		if len(group) < 2 {
			continue
		}
		// Unique non-zero metadata_ids
		ids := map[int64]struct{}{}
		for _, it := range group {
			if it.MetadataID != 0 {
				ids[it.MetadataID] = struct{}{}
			}
		}
		if len(ids) == 0 {
			continue
		}
		if len(ids) > 1 {
			skippedConflicts++
			continue
		}
		var canonical int64
		for id := range ids {
			canonical = id
		}
		touched := 0
		for _, it := range group {
			if it.MetadataID == canonical {
				continue
			}
			if err := s.Store.SetItemMetadata(it.ID, canonical); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			touched++
		}
		if touched > 0 {
			foldersTouched++
			itemsUpdated += touched
		}
	}
	writeJSON(w, 200, map[string]int{
		"foldersTouched":   foldersTouched,
		"itemsUpdated":     itemsUpdated,
		"skippedConflicts": skippedConflicts,
	})
}

// mergeItems vereint mehrere Items unter derselben Metadata — sinnvoll für
// manuelle Duplikat-Zusammenführung. Erstes Item mit metadata_id gewinnt;
// alle anderen übernehmen diese metadata_id. Items ohne metadata_id bleiben
// unangetastet, wenn keines der übergebenen Items eine hat.
// Body: {"ids":[id1,id2,…]}
func (s *Server) mergeItems(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if len(body.IDs) < 2 {
		writeError(w, 400, "mindestens 2 Items nötig")
		return
	}
	var canonical int64
	var libID int64
	for _, id := range body.IDs {
		it, err := s.Store.GetItem(id)
		if err != nil || it == nil {
			continue
		}
		if libID == 0 {
			libID = it.LibraryID
		} else if libID != it.LibraryID {
			writeError(w, 400, "Items müssen aus derselben Bibliothek stammen")
			return
		}
		if canonical == 0 && it.MetadataID != 0 {
			canonical = it.MetadataID
		}
	}
	if canonical == 0 {
		writeError(w, 400, "Keines der Items hat eine TMDB-Zuordnung — bitte zuerst mindestens eins manuell zuordnen")
		return
	}
	n := 0
	for _, id := range body.IDs {
		if err := s.Store.SetItemMetadata(id, canonical); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		n++
	}
	writeJSON(w, 200, map[string]any{"merged": n, "metadataId": canonical})
}

// setFolderMetadata setzt manuell die Show-Zuordnung für einen Ordner.
// Body: {"folder":"Arrow","tmdbId":1412}
func (s *Server) setFolderMetadata(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder string `json:"folder"`
		TMDBID int64  `json:"tmdbId"`
		IMDBID string `json:"imdbId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.Folder == "" {
		writeError(w, 400, "folder nötig")
		return
	}
	// IMDb-ID zu TMDB-TV auflösen
	if body.TMDBID == 0 && body.IMDBID != "" {
		res, err := s.Enrich.Client().FindByIMDb(r.Context(), body.IMDBID)
		if err != nil {
			writeError(w, 502, "IMDb-Lookup: "+err.Error())
			return
		}
		if res == nil {
			writeError(w, 404, "TMDB kennt diese IMDb-ID nicht (weder Serie/Episode noch Film). Prüfe auf themoviedb.org, ob die ID dort als External-ID hinterlegt ist.")
			return
		}
		if res.TMDBType != "tv" {
			writeError(w, 404, "IMDb-ID liefert "+res.TMDBType+", keine Serie. Falls die Reihe aus einzelnen TV-Filmen besteht, leg die Library als „Filme" an und matche die Dateien einzeln.")
			return
		}
		body.TMDBID = res.ID
	}
	if body.TMDBID == 0 {
		writeError(w, 400, "tmdbId oder imdbId nötig")
		return
	}
	ctx := r.Context()
	show, err := s.Enrich.Client().GetTV(ctx, body.TMDBID)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	meta := &model.Metadata{
		TMDBType: "tv", TMDBID: show.ID, Title: show.Name, OriginalTitle: show.OriginalName,
		Year: yearFromStr(show.FirstAirDate), Overview: show.Overview,
		Rating: show.VoteAverage, PosterPath: show.PosterPath, BackdropPath: show.BackdropPath,
	}
	id, err := s.Store.UpsertMetadata(meta)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if err := s.Store.SetFolderMetadata(libID, body.Folder, id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.Enrich.EnsurePosterCached(ctx, meta)
	// Wichtig: alte Episoden-Zuordnungen räumen, die noch auf eine andere Show
	// zeigen — sonst überspringt der Enricher sie (skip-wenn-metadata_id>0).
	// Nur Episoden, deren Parent-Show eine andere TMDB-ID hat als die neu
	// zugeordnete, werden unmatched.
	if n, err := s.Store.UnmatchEpisodesInFolder(libID, body.Folder, body.TMDBID); err == nil && n > 0 {
		log.Printf("[setFolderMetadata] %d Episoden in %q unmatched (alte Show-Zuordnung)", n, body.Folder)
	}
	// Episoden dieses Ordners sofort matchen (statt auf den 5-Min-Ticker zu warten)
	s.Enrich.EnrichFolderNow(libID, body.Folder)
	w.WriteHeader(204)
}

// validAgeRating enthält die erlaubten FSK-Werte. Leerer String = nicht gesetzt.
var validAgeRating = map[string]bool{"": true, "0": true, "6": true, "12": true, "16": true, "18": true}

// updateMetadata nimmt manuelle Edits des Admins entgegen und aktualisiert
// ausschließlich die User-editierbaren Felder. TMDB-ID/Type/Parent bleiben
// unberührt, weil die die Identität der Zuordnung definieren.
func (s *Server) updateMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Title         string  `json:"title"`
		OriginalTitle string  `json:"originalTitle"`
		Year          int     `json:"year"`
		ReleaseDate   string  `json:"releaseDate"` // YYYY-MM-DD, leer = unverändert
		Overview      string  `json:"overview"`
		Rating        float64 `json:"rating"`
		RuntimeMin    int     `json:"runtimeMin"`
		Genres        string  `json:"genres"` // JSON-Array-String, wie im Schema
		AgeRating     string  `json:"ageRating"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if !validAgeRating[body.AgeRating] {
		writeError(w, 400, "ageRating muss leer oder 0/6/12/16/18 sein")
		return
	}
	if body.Rating < 0 || body.Rating > 10 {
		writeError(w, 400, "rating muss zwischen 0 und 10 liegen")
		return
	}
	existing, err := s.Store.GetMetadata(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if existing == nil {
		writeError(w, 404, "Metadata nicht gefunden")
		return
	}
	rd := existing.ReleaseDate
	if body.ReleaseDate != "" {
		if t, err := time.Parse("2006-01-02", body.ReleaseDate); err == nil {
			rd = t
		}
	}
	if err := s.Store.UpdateMetadataManual(id, body.Title, body.OriginalTitle, body.Year,
		rd, body.Overview, body.Rating, body.RuntimeMin, body.Genres, body.AgeRating); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// NFO für alle Items, die auf diese Metadata zeigen, neu schreiben (damit
	// Plex/Jellyfin die neuen Werte auch sehen).
	if items, _ := s.Store.ListConfirmedItems(); items != nil {
		for i := range items {
			if items[i].MetadataID == id {
				_, _ = s.writeNFOForItem(&items[i])
			}
		}
	}
	w.WriteHeader(204)
}

// backfillAgeRatings durchsucht alle Movie-/TV-Metadaten ohne age_rating und
// holt sie aus TMDB nach (für TV: content_ratings, für Movies: release_dates).
// Läuft als Hintergrund-Goroutine — der Endpoint kehrt sofort zurück.
func (s *Server) backfillAgeRatings(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB nicht konfiguriert")
		return
	}
	go func() {
		// WICHTIG: context.Background() statt r.Context() — die Request-Context
		// wird sofort gecancelled wenn der Handler `202 Accepted` zurückgibt
		// und die Goroutine läuft mit gecancelltem Context weiter, was beim
		// ersten TMDB-Call sofort zum Abbruch führt. Hier brauchen wir einen
		// Long-Running-Context, der unabhängig vom HTTP-Lifecycle ist.
		ctx := context.Background()
		client := s.Enrich.Client()
		// Alle Metadata mit tmdb_type 'movie' oder 'tv' ohne age_rating holen.
		ids, types, tmdbIDs, err := s.Store.MetadataMissingAgeRating()
		if err != nil {
			log.Printf("[fsk-backfill] list: %v", err)
			return
		}
		log.Printf("[fsk-backfill] starte: %d Metadata-Einträge ohne FSK", len(ids))
		var ok, miss, fail int
		for i, id := range ids {
			if ctx.Err() != nil {
				log.Printf("[fsk-backfill] abgebrochen: %v", ctx.Err())
				return
			}
			var fsk string
			if types[i] == "movie" {
				fsk, err = client.MovieAgeRatingDE(ctx, tmdbIDs[i])
			} else if types[i] == "tv" {
				fsk, err = client.TVAgeRatingDE(ctx, tmdbIDs[i])
			} else {
				continue
			}
			if err != nil {
				fail++
				continue
			}
			if fsk == "" {
				miss++
				continue
			}
			if err := s.Store.SetMetadataAgeRatingIfEmpty(id, fsk); err != nil {
				fail++
				continue
			}
			ok++
		}
		log.Printf("[fsk-backfill] fertig: %d gesetzt, %d ohne DE-Cert bei TMDB, %d Fehler", ok, miss, fail)
	}()
	writeJSON(w, 202, map[string]any{"status": "started"})
}

// reEnrichFolderEpisodes: setzt alle Episoden-Zuordnungen eines TV-Ordners
// zurück (inkl. confirmed-Flags) und stößt den Enricher sofort an. Wird vom
// „⚠ Episoden neu zuordnen"-Button in der Staffel-Ansicht aufgerufen, wenn
// der Enricher systematisch falsch gemappt hat (z.B. Off-by-One bei Billions).
// Die folder_metadata-Zuordnung (Show → TMDB) bleibt unangetastet.
func (s *Server) reEnrichFolderEpisodes(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige library id")
		return
	}
	if !s.requireLibAccess(w, r, libID) {
		return
	}
	var body struct {
		Folder string `json:"folder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Folder == "" {
		writeError(w, 400, "folder nötig")
		return
	}
	n, err := s.Store.UnmatchAllEpisodesInFolder(libID, body.Folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	log.Printf("[re-enrich] %d Episoden-Items in %q unmatched → erneuter Match läuft", n, body.Folder)
	if s.Enrich != nil {
		s.Enrich.EnrichFolderNow(libID, body.Folder)
	}
	writeJSON(w, 200, map[string]any{"unmatched": n})
}

// getPoster liefert ein gecachtes TMDB-Poster (oder Placeholder).
func (s *Server) getPoster(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	meta, err := s.Store.GetMetadata(id)
	if err != nil || meta == nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	path := s.Enrich.EnsurePosterCached(r.Context(), meta)
	if path == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	// `no-cache` bedeutet: Browser MUSS bei jedem Request revalidieren.
	// Server antwortet via ETag mit 304 (sehr billig, leerer Body) wenn
	// die Datei unverändert ist. Bei Poster-Wechsel sieht der User sofort
	// das neue Bild — `max-age` würde sonst minutenlang die alte Version
	// aus dem Browser-Cache servieren.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, path, info.ModTime(), f)
}

func yearFromStr(s string) int {
	if len(s) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(s[:4])
	return y
}
