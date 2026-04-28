package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/nfo"
	"github.com/boernie77/goldfish/internal/store"
)

// suspiciousMatches: Items, deren TMDB-Zuordnung wahrscheinlich nicht zum
// Ordnernamen passt (der User nannte das „geratene" Zuordnungen). Heuristik:
// kein Token-Overlap zwischen Top-Level-Folder und Metadata-Titel UND keine
// Jahresübereinstimmung.
// Query: ?libraryId=<optional>
func (s *Server) suspiciousMatches(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	var libID int64
	if v := r.URL.Query().Get("libraryId"); v != "" {
		libID, _ = strconv.ParseInt(v, 10, 64)
		if libID > 0 && !s.requireLibAccess(w, r, libID) {
			return
		}
	}
	items, err := s.Store.SuspiciousMatches(libID, me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []model.Item{}
	}
	writeJSON(w, 200, items)
}

// writeNFOForItem ist der gemeinsame Helper, der für ein Item die passende
// Kodi-kompatible .nfo schreibt (movie oder episode) und bei Episoden zusätzlich
// die tvshow.nfo im Top-Level-Serienordner anlegt. Kein Pre-Check auf
// metadata_confirmed — der Aufrufer entscheidet ob das NFO geschrieben werden
// soll (automatisch beim Bestätigen, manuell via Button, bulk-retroaktiv).
func (s *Server) writeNFOForItem(it *model.Item) ([]string, error) {
	var written []string
	if it.Metadata == nil {
		return written, nil
	}
	switch it.Metadata.TMDBType {
	case "movie":
		p, err := nfo.WriteMovie(it.Path, it, it.Metadata)
		if err != nil {
			return written, err
		}
		written = append(written, p)
	case "episode":
		var show *model.Metadata
		if it.Metadata.ParentID != 0 {
			show, _ = s.Store.GetMetadata(it.Metadata.ParentID)
		}
		p, err := nfo.WriteEpisode(it.Path, it, it.Metadata, show)
		if err != nil {
			return written, err
		}
		written = append(written, p)
		// tvshow.nfo im Top-Level-Ordner (einmalig pro Serie).
		if show != nil && it.RelPath != "" {
			idx := strings.Index(it.RelPath, "/")
			if idx > 0 {
				libBase := strings.TrimSuffix(it.Path, "/"+it.RelPath)
				showFolder := filepath.Join(libBase, it.RelPath[:idx])
				if p2, err := nfo.WriteTVShow(showFolder, show); err == nil {
					written = append(written, p2)
				}
			}
		}
	}
	return written, nil
}

// writeItemNFO — manueller Button-Endpoint. Erwartet bestätigtes Item.
// Admin-only, pro Item.
func (s *Server) writeItemNFO(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !it.MetadataConfirmed {
		writeError(w, 400, "Zuordnung muss erst als bestätigt markiert werden (✅ im Detail-Dialog)")
		return
	}
	if it.Metadata == nil {
		writeError(w, 400, "keine TMDB-Zuordnung vorhanden")
		return
	}
	written, err := s.writeNFOForItem(it)
	if err != nil {
		writeError(w, 500, "NFO-Schreibfehler: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"written": written})
}

// writeAllConfirmedNFOs iteriert alle bestätigten Items und schreibt für jedes
// eine NFO. Admin-only. Nützlich für bestehenden Datenbestand, wo der
// Bestätigungs-Status schon gesetzt ist, aber noch keine NFO-Dateien
// existieren.
func (s *Server) writeAllConfirmedNFOs(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListConfirmedItems()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	var ok, fail int
	var firstErr string
	for _, it := range items {
		if _, err := s.writeNFOForItem(&it); err != nil {
			fail++
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		ok++
	}
	writeJSON(w, 200, map[string]any{
		"total":    len(items),
		"ok":       ok,
		"failed":   fail,
		"firstErr": firstErr,
	})
}

// refreshItemMetadata zieht TMDB-Details für die bestehende TMDB-Zuordnung
// eines Items frisch (mit OMDb-Fallback für leere Felder). Die Zuordnung
// (metadata_id, metadata_confirmed) bleibt unverändert. POST /api/items/{id}/refresh-metadata
func (s *Server) refreshItemMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
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
	if s.Enrich == nil {
		writeError(w, 503, "Enricher nicht initialisiert")
		return
	}
	meta, err := s.Enrich.RefreshItemMetadata(r.Context(), it)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	// Wenn das Item bestätigt ist, NFO mit den frischen Daten neu schreiben.
	if it.MetadataConfirmed {
		if it2, _ := s.Store.GetItem(id); it2 != nil {
			_, _ = s.writeNFOForItem(it2)
		}
	}
	writeJSON(w, 200, meta)
}

// startRefreshAllMetadata stößt einen Bulk-Refresh aller Metadaten an.
// Admin-only. Läuft im Hintergrund, Status via /api/enrich/refresh-all-status.
func (s *Server) startRefreshAllMetadata(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil {
		writeError(w, 503, "Enricher nicht initialisiert")
		return
	}
	if !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB-Key nicht konfiguriert")
		return
	}
	if !s.Enrich.StartRefreshAllMetadata() {
		writeError(w, 409, "Es läuft bereits ein Bulk-Refresh")
		return
	}
	writeJSON(w, 202, map[string]any{"status": "started"})
}

// refreshAllMetadataStatus liefert den Fortschritt des Bulk-Refresh.
func (s *Server) refreshAllMetadataStatus(w http.ResponseWriter, r *http.Request) {
	if s.Enrich == nil {
		writeError(w, 503, "Enricher nicht initialisiert")
		return
	}
	writeJSON(w, 200, s.Enrich.RefreshAllStatus())
}

// confirmItemMetadata togglet `items.metadata_confirmed` für ein Item.
// Body: {"confirmed": bool}. Bestätigte Items tauchen nicht mehr in der
// „⚠ Verdächtige Zuordnungen"-Liste auf und werden vom Enricher/Unmatch-
// Mechanismus nicht überschrieben.
func (s *Server) confirmItemMetadata(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	var body struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetItemMetadataConfirmed(id, body.Confirmed); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Beim Bestätigen automatisch NFO schreiben — das hatte der User
	// explizit gewünscht: ✅ impliziert Plex/Jellyfin-kompatibles Sidecar.
	if body.Confirmed {
		it2, _ := s.Store.GetItem(id)
		if it2 != nil {
			_, _ = s.writeNFOForItem(it2)
		}
	}
	w.WriteHeader(204)
}

// searchItemsByPath: Admin-Diagnose-Endpunkt. Sucht in rel_path + path
// (Dateiname/Ordnerpfad), um falsch zugeordnete Items zu finden, die im
// normalen Titel-Search nicht auftauchen (z.B. weil TMDB sie einem völlig
// anderen Film zugeordnet hat).
func (s *Server) searchItemsByPath(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, 200, []any{})
		return
	}
	items, err := s.Store.SearchItemsByPath(q, 200)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []model.Item{}
	}
	writeJSON(w, 200, items)
}

func parseDate(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	for _, l := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(l, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	q := r.URL.Query()
	f := store.ItemFilter{
		Search:     q.Get("search"),
		Sort:       q.Get("sort"),
		SortDir:    q.Get("dir"),
		DateFrom:   parseDate(q.Get("dateFrom")),
		DateTo:     parseDate(q.Get("dateTo")),
		Folder:     q.Get("folder"),
		Watched:    q.Get("watched"),
		Favorite:   q.Get("favorite"),
		MatchState: q.Get("match"),
		DupesOnly:  q.Get("duplicates") == "yes",
		Interlaced: q.Get("interlaced") == "yes",
		TrickplayStatus: q.Get("trickplay"),
		UserID:     me.ID,
	}
	if v := q.Get("libraryId"); v != "" {
		f.LibraryID, _ = strconv.ParseInt(v, 10, 64)
		if f.LibraryID > 0 && !s.requireLibAccess(w, r, f.LibraryID) {
			return
		}
	}
	if v := q.Get("personId"); v != "" {
		f.PersonTMDB, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("minHeight"); v != "" {
		n, _ := strconv.Atoi(v)
		f.MinHeight = n
	}
	if v := q.Get("maxHeight"); v != "" {
		n, _ := strconv.Atoi(v)
		f.MaxHeight = n
	}
	if buckets, ok := q["bucket"]; ok {
		f.ResBuckets = buckets
	}
	items, err := s.Store.ListItems(f)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []model.Item{}
	}
	writeJSON(w, 200, items)
}

// randomItem liefert ein einzelnes zufälliges Item, auf dem die üblichen Filter greifen.
func (s *Server) randomItem(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	q := r.URL.Query()
	f := store.ItemFilter{
		Search:     q.Get("search"),
		Sort:       "random",
		Folder:     q.Get("folder"),
		Watched:    q.Get("watched"),
		Favorite:   q.Get("favorite"),
		MatchState: q.Get("match"),
	}
	if me != nil {
		f.UserID = me.ID
		// FSK-Beschränkung nur für Non-Admins: Admins sehen alles.
		if !me.IsAdmin && me.MaxAgeRating != nil {
			f.MaxAgeRating = *me.MaxAgeRating
		}
	}
	if v := q.Get("libraryId"); v != "" {
		f.LibraryID, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("minHeight"); v != "" {
		n, _ := strconv.Atoi(v)
		f.MinHeight = n
	}
	if v := q.Get("maxHeight"); v != "" {
		n, _ := strconv.Atoi(v)
		f.MaxHeight = n
	}
	if buckets, ok := q["bucket"]; ok {
		f.ResBuckets = buckets
	}
	items, err := s.Store.ListItems(f)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if len(items) == 0 {
		writeError(w, 404, "keine Items in diesem Kontext")
		return
	}
	writeJSON(w, 200, items[0])
}

func (s *Server) listYears(w http.ResponseWriter, r *http.Request) {
	var libID int64
	if v := r.URL.Query().Get("libraryId"); v != "" {
		libID, _ = strconv.ParseInt(v, 10, 64)
	}
	years, err := s.Store.DistinctYears(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if years == nil {
		years = []int{}
	}
	writeJSON(w, 200, years)
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItemFor(me.ID, id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}
	writeJSON(w, 200, it)
}

// getItemVariants liefert alle Items mit derselben metadata_id wie das übergebene
// Item — inklusive des Items selbst. Wird vom Frontend aufgerufen, wenn der
// Detail-Dialog aus einem Kontext geöffnet wird, in dem das Frontend-Klebefeld
// `_variants` nicht gesetzt war (z. B. Path-Search-Dialog, Person-Filter,
// Direktlink). Geschwister in Libraries ohne ACL-Zugriff werden ausgefiltert,
// damit der Endpoint die Existenz fremder Files nicht verrät.
func (s *Server) getItemVariants(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	src, err := s.Store.GetItemFor(me.ID, id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if src == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, src.LibraryID) {
		return
	}
	if src.MetadataID == 0 {
		writeJSON(w, 200, []model.Item{*src})
		return
	}
	all, err := s.Store.ListItems(store.ItemFilter{
		MetadataID: src.MetadataID,
		UserID:     me.ID,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out := make([]model.Item, 0, len(all))
	for _, v := range all {
		ok, _ := s.Store.UserHasLibraryAccess(me.ID, v.LibraryID, me.IsAdmin)
		if !ok {
			continue
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}
