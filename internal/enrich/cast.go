package enrich

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/tmdb"
)

const (
	mainCastLimit = 15 // maximale Haupt-Cast-Einträge pro Film/Serie
)

// fetchMovieCast lädt die Top-N Cast-Einträge eines Films und speichert sie als
// "main"-Rolle. Markiert die Metadata als "cast fetched" damit der Backfill-Loop
// Einträge ohne Treffer nicht wiederholt.
func (w *Worker) fetchMovieCast(ctx context.Context, metaID, tmdbMovieID int64) {
	if !w.client.Enabled() {
		return
	}
	entries, err := w.client.GetMovieCredits(ctx, tmdbMovieID)
	if err != nil {
		log.Printf("[enrich] movie-cast %d: %v", tmdbMovieID, err)
		return
	}
	w.storeCast(ctx, metaID, "main", entries, mainCastLimit)
	_ = w.store.MarkMetadataCastFetched(metaID)
}

// fetchShowCast lädt die Haupt-Cast-Liste einer Serie und speichert sie als "main".
func (w *Worker) fetchShowCast(ctx context.Context, metaID, tmdbShowID int64) {
	if !w.client.Enabled() {
		return
	}
	entries, err := w.client.GetTVCredits(ctx, tmdbShowID)
	if err != nil {
		log.Printf("[enrich] tv-cast %d: %v", tmdbShowID, err)
		return
	}
	w.storeCast(ctx, metaID, "main", entries, mainCastLimit)
	_ = w.store.MarkMetadataCastFetched(metaID)
}

// fetchEpisodeGuests lädt die Gast-Stars einer Episode und speichert sie als
// "guest". Der Haupt-Cast kommt aus der Parent-Show und wird in der UI kombiniert.
func (w *Worker) fetchEpisodeGuests(ctx context.Context, metaID, tmdbShowID int64, season, episode int) {
	if !w.client.Enabled() {
		return
	}
	_, guests, err := w.client.GetEpisodeCredits(ctx, tmdbShowID, season, episode)
	if err != nil {
		log.Printf("[enrich] episode-guests %d s%de%d: %v", tmdbShowID, season, episode, err)
		return
	}
	// Gäste haben kein hartes Limit — meist wenige, aber manchmal viele bei großen Episoden
	w.storeCast(ctx, metaID, "guest", guests, 30)
	_ = w.store.MarkMetadataCastFetched(metaID)
}

// storeCast: Personen upsert, dann Cast-Relation ersetzen. Lädt parallel die Profilbilder.
func (w *Worker) storeCast(ctx context.Context, metaID int64, role string, entries []tmdb.CastEntry, limit int) {
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	members := make([]model.CastMember, 0, len(entries))
	for _, e := range entries {
		if e.ID == 0 || e.Name == "" {
			continue
		}
		pid, err := w.store.UpsertPerson(e.ID, e.Name, e.ProfilePath)
		if err != nil {
			log.Printf("[enrich] upsert person %d: %v", e.ID, err)
			continue
		}
		members = append(members, model.CastMember{
			PersonID:    pid,
			TMDBID:      e.ID,
			Name:        e.Name,
			ProfilePath: e.ProfilePath,
			Character:   e.Character,
			Order:       e.Order,
		})
		w.cacheProfile(ctx, e.ID, e.ProfilePath)
	}
	if err := w.store.ReplaceMetadataCast(metaID, role, members); err != nil {
		log.Printf("[enrich] replace cast meta=%d role=%s: %v", metaID, role, err)
	}
}

// cacheProfile lädt ein Personen-Profilbild einmalig lokal (w185).
func (w *Worker) cacheProfile(ctx context.Context, personTMDBID int64, tmdbPath string) {
	if tmdbPath == "" || personTMDBID == 0 {
		return
	}
	dir := filepath.Join(filepath.Dir(w.posterDir), "people")
	_ = os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, profileFilename(personTMDBID, tmdbPath))
	if _, err := os.Stat(out); err == nil {
		return
	}
	data, _, err := w.client.DownloadPoster(ctx, tmdbPath, "w185")
	if err != nil {
		log.Printf("[enrich] profile %d: %v", personTMDBID, err)
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		log.Printf("[enrich] save profile %d: %v", personTMDBID, err)
	}
}

// ProfileFile liefert den Dateipfad zu einem gecachten Profilbild, falls vorhanden.
// Wenn kein Cache-Hit existiert, wird ein leerer String zurückgegeben — Aufrufer
// entscheidet dann über Placeholder.
func (w *Worker) ProfileFile(personTMDBID int64, tmdbPath string) string {
	if tmdbPath == "" || personTMDBID == 0 {
		return ""
	}
	dir := filepath.Join(filepath.Dir(w.posterDir), "people")
	p := filepath.Join(dir, profileFilename(personTMDBID, tmdbPath))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// EnsureProfileCached lädt ein Profilbild synchron, falls noch nicht vorhanden.
func (w *Worker) EnsureProfileCached(ctx context.Context, personTMDBID int64, tmdbPath string) string {
	if p := w.ProfileFile(personTMDBID, tmdbPath); p != "" {
		return p
	}
	w.cacheProfile(ctx, personTMDBID, tmdbPath)
	return w.ProfileFile(personTMDBID, tmdbPath)
}

func profileFilename(personTMDBID int64, tmdbPath string) string {
	sum := sha1.Sum([]byte(tmdbPath))
	return "person_" + hex.EncodeToString(sum[:8]) + filepath.Ext(tmdbPath)
}

// backfillCast iteriert Metadata-Einträge ohne Cast und holt sie nach. Wird im
// runOnce-Ablauf nach dem normalen Enrichment aufgerufen.
func (w *Worker) backfillCast(ctx context.Context) error {
	const batch = 100
	metas, err := w.store.MetadataIDsMissingCast(batch)
	if err != nil {
		return err
	}
	for _, m := range metas {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch m.TMDBType {
		case "movie":
			w.fetchMovieCast(ctx, m.ID, m.TMDBID)
		case "tv":
			w.fetchShowCast(ctx, m.ID, m.TMDBID)
		case "episode":
			if m.ParentID == 0 {
				continue
			}
			parent, err := w.store.GetMetadata(m.ParentID)
			if err != nil || parent == nil {
				continue
			}
			w.fetchEpisodeGuests(ctx, m.ID, parent.TMDBID, m.Season, m.Episode)
		}
	}
	return nil
}
