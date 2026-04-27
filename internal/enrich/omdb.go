package enrich

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/omdb"
	"github.com/boernie77/goldfish/internal/tmdb"
)

// attachOMDbCast legt die Cast-Einträge aus einer OMDb-Antwort an. OMDb
// liefert keine TMDB-Person-IDs — wir nutzen einen negativen synthetischen
// Schlüssel (Namens-Hash), damit die Einträge stabil upsertable sind, aber
// nicht mit echten TMDB-People kollidieren.
func (w *Worker) attachOMDbCast(metadataID int64, actors []string) {
	entries := make([]model.CastMember, 0, len(actors))
	for i, name := range actors {
		if i >= 10 {
			break
		}
		tmdbKey := omdbPersonKey(name)
		pid, err := w.store.UpsertPerson(tmdbKey, name, "")
		if err != nil {
			log.Printf("[enrich] omdb cast upsert %q: %v", name, err)
			continue
		}
		entries = append(entries, model.CastMember{
			PersonID: pid,
			TMDBID:   tmdbKey,
			Name:     name,
			Role:     "main",
			Order:    i,
		})
	}
	if len(entries) > 0 {
		if err := w.store.ReplaceMetadataCast(metadataID, "main", entries); err != nil {
			log.Printf("[enrich] omdb cast replace: %v", err)
		}
	}
}

// omdbPersonKey erzeugt einen stabilen negativen Integer-Key aus dem Namen.
// Vermeidet Kollisionen mit positiven TMDB-Person-IDs.
func omdbPersonKey(name string) int64 {
	h := sha1.Sum([]byte("omdb:" + strings.ToLower(strings.TrimSpace(name))))
	// 8 Bytes in int64, dann negativ machen
	v := int64(binary.BigEndian.Uint64(h[:8]) & 0x7fffffffffffffff)
	if v == 0 {
		v = 1
	}
	return -v
}

// imdbNum extrahiert die Zahl aus "tt1234567" → 1234567. Bei Fehler → 0.
func imdbNum(imdbID string) int64 {
	s := strings.TrimPrefix(imdbID, "tt")
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// omdbToMetadata konvertiert ein OMDb-Result zu unserem Metadata-Eintrag.
// Nutzt tmdb_type = "omdb_movie" | "omdb_tv" und tmdb_id = numerische IMDb-ID.
func omdbToMetadata(r *omdb.Result) *model.Metadata {
	t := "omdb_movie"
	if r.IsSeries {
		t = "omdb_tv"
	}
	m := &model.Metadata{
		TMDBType:      t,
		TMDBID:        imdbNum(r.IMDBID),
		Title:         r.Title,
		OriginalTitle: r.Title,
		Year:          r.Year,
		Overview:      r.Overview,
		Rating:        r.Rating,
		RuntimeMin:    r.Runtime,
		PosterPath:    r.Poster, // volle URL — PosterURL-Helper reicht durch
		IMDBID:        r.IMDBID,
		AgeRating:     r.AgeRating, // approximiert aus MPAA → FSK
	}
	if r.Genre != "" {
		parts := strings.Split(r.Genre, ", ")
		b, _ := json.Marshal(parts)
		m.Genres = string(b)
	}
	return m
}

// enrichItemViaOMDb versucht, ein Item via OMDb anzureichern (Fallback, wenn TMDB nichts findet).
// Wird nur bei KindMovies aufgerufen (OMDb-Serien-Episoden unterstützen wir nicht).
//
// Kaskade: zuerst strikter Title-Match (`t=`) mit + ohne Jahr, dann OMDbs
// Volltextsuche (`s=`). Das toleriert kleine Abweichungen ("und" vs "&",
// Zusatz-Wörter im Dateinamen usw.), die bei `t=` fehlschlagen.
func (w *Worker) enrichItemViaOMDb(ctx context.Context, title string, year int) (*model.Metadata, error) {
	if w.omdb == nil || !w.omdb.Enabled() || w.omdb.QuotaExceeded() {
		return nil, nil
	}
	var r *omdb.Result
	var err error
	// 1) exakter Titel + Jahr (wenn vorhanden)
	if r, err = w.omdb.SearchByTitle(ctx, title, year); err != nil {
		if errors.Is(err, omdb.ErrQuotaExceeded) {
			return nil, nil
		}
		return nil, err
	}
	// 2) exakter Titel ohne Jahr (nur wenn Quota noch OK).
	if r == nil && year > 0 && !w.omdb.QuotaExceeded() {
		if r, err = w.omdb.SearchByTitle(ctx, title, 0); err != nil {
			if errors.Is(err, omdb.ErrQuotaExceeded) {
				return nil, nil
			}
			return nil, err
		}
	}
	// 3) Volltextsuche (nur wenn Quota noch OK). Loose-Search braucht einen
	//    zweiten API-Call (s= → i=), darum gezielt sparen.
	if r == nil && !w.omdb.QuotaExceeded() {
		if r, err = w.omdb.SearchTitleLoose(ctx, title, year); err != nil {
			if errors.Is(err, omdb.ErrQuotaExceeded) {
				return nil, nil
			}
			return nil, err
		}
	}
	if r == nil {
		return nil, nil
	}
	meta := omdbToMetadata(r)
	id, err := w.store.UpsertMetadata(meta)
	if err != nil {
		return nil, err
	}
	meta.ID = id
	w.cachePoster(ctx, id, meta.PosterPath, "")
	// OMDb-Cast (bis zu 4 Schauspieler) in metadata_cast eintragen, damit
	// Detail-Dialog sie anzeigt. Person-IDs sind synthetisch (wir nutzen
	// negative IDs basierend auf Namens-Hash, da OMDb keine TMDB-Person-IDs
	// liefert) — Cast-Filter via personId wirkt für diese Einträge nicht.
	if len(r.Actors) > 0 {
		w.attachOMDbCast(id, r.Actors)
	}
	return meta, nil
}

// EnrichByIMDbID: manueller Pfad — IMDb-ID → erst TMDB.FindByIMDb, dann OMDb-Fallback.
// Liefert das gespeicherte Metadata-Objekt zurück.
func (w *Worker) EnrichByIMDbID(ctx context.Context, imdbID string) (*model.Metadata, error) {
	// 1. TMDB Versuch
	if w.client.Enabled() {
		if res, err := w.client.FindByIMDb(ctx, imdbID); err == nil && res != nil {
			switch res.TMDBType {
			case "movie":
				return w.fetchMovieMetadata(ctx, res.ID)
			case "tv":
				return w.fetchShowMetadata(ctx, res.ID)
			}
		}
	}
	// 2. OMDb-Fallback
	if w.omdb == nil || !w.omdb.Enabled() {
		return nil, nil
	}
	r, err := w.omdb.ByIMDb(ctx, imdbID)
	if err != nil || r == nil {
		return nil, err
	}
	meta := omdbToMetadata(r)
	id, err := w.store.UpsertMetadata(meta)
	if err != nil {
		return nil, err
	}
	meta.ID = id
	w.cachePoster(ctx, id, meta.PosterPath, "")
	// Cast aus OMDb (max 4 Schauspieler) ebenfalls anhängen — analog zum
	// auto-Fallback in enrichItemViaOMDb. Vorher fehlte das hier, dadurch
	// hatten manuell per IMDb-ID gematchte Filme nie Schauspieler.
	if len(r.Actors) > 0 {
		w.attachOMDbCast(id, r.Actors)
	}
	return meta, nil
}

// UnusedTmdbToShutUpCompiler hält den tmdb-Import lebendig, falls dieser File als erstes
// kompiliert wird (defensive). Entfällt automatisch, wenn Funktionen sie nutzen.
var _ = tmdb.PosterURL
