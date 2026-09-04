package api

import (
	"net/http"
	"os"
	"strconv"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/go-chi/chi/v5"
)

// getMetadataCast liefert die Cast-Liste einer Metadata. Für Episoden wird
// zusätzlich die Haupt-Cast-Liste des Parent-Shows vorangestellt.
func (s *Server) getMetadataCast(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	meta, err := s.Store.GetMetadata(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if meta == nil {
		writeError(w, 404, "Metadata nicht gefunden")
		return
	}

	out := []model.CastMember{}
	// Bei Episoden: Haupt-Cast aus Parent-Show holen.
	if meta.TMDBType == "episode" && meta.ParentID > 0 {
		parent, err := s.Store.GetMetadataCast(meta.ParentID)
		if err == nil {
			out = append(out, parent...)
		}
	}
	own, err := s.Store.GetMetadataCast(meta.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Für Episoden Haupt-Rolle aus Show + Gäste aus Episode; sonst nur own.
	if meta.TMDBType == "episode" {
		for _, c := range own {
			if c.Role == "guest" {
				out = append(out, c)
			}
		}
	} else {
		out = own
	}

	// Duplikate vermeiden (selbe Person einmal als main, einmal als guest)
	seen := map[int64]bool{}
	uniq := make([]model.CastMember, 0, len(out))
	for _, c := range out {
		if seen[c.PersonID] {
			continue
		}
		seen[c.PersonID] = true
		uniq = append(uniq, c)
	}
	writeJSON(w, 200, uniq)
}

// getMetadataTrailer liefert den bevorzugten öffentlichen YouTube-Trailer eines
// Films (Jellyfin-artige Trailer-Funktion, User-Anfrage 2026-09-04). Nur für
// echte Filme (tmdb_type=movie) — Serien/Episoden/Privat-Videos haben keinen.
func (s *Server) getMetadataTrailer(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	meta, err := s.Store.GetMetadata(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if meta == nil || meta.TMDBType != "movie" || meta.TMDBID <= 0 {
		writeError(w, 404, "Kein Trailer verfügbar")
		return
	}
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 404, "TMDB nicht konfiguriert")
		return
	}
	trailer, err := s.Enrich.Client().GetMovieTrailer(r.Context(), meta.TMDBID)
	if err != nil || trailer == nil {
		writeError(w, 404, "Kein Trailer gefunden")
		return
	}
	writeJSON(w, 200, trailer)
}

// getMetadataTrailerStream extrahiert eine direkt abspielbare Stream-URL für
// den Trailer (via yt-dlp, internal/ytdlp) — für die nativen Apple-Apps
// (Mac/iOS/tvOS), die den Trailer per AVPlayer statt per WebView/iframe
// abspielen (tvOS hat gar kein WebKit, siehe CLAUDE.md "Trailer"). Der
// Browser braucht diesen Endpoint NICHT (der nutzt weiterhin das iframe-Embed
// direkt im Client).
func (s *Server) getMetadataTrailerStream(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	meta, err := s.Store.GetMetadata(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if meta == nil || meta.TMDBType != "movie" || meta.TMDBID <= 0 {
		writeError(w, 404, "Kein Trailer verfügbar")
		return
	}
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 404, "TMDB nicht konfiguriert")
		return
	}
	trailer, err := s.Enrich.Client().GetMovieTrailer(r.Context(), meta.TMDBID)
	if err != nil || trailer == nil {
		writeError(w, 404, "Kein Trailer gefunden")
		return
	}
	if s.YTDLP == nil {
		writeError(w, 503, "Trailer-Stream-Extraktion nicht verfügbar")
		return
	}
	url, err := s.YTDLP.StreamURL(r.Context(), trailer.Key)
	if err != nil {
		writeError(w, 502, "Trailer konnte nicht extrahiert werden: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"url": url})
}

// getPerson liefert Bio-Daten + volle Filmografie einer Person. Bio + Credits
// kommen live von TMDB (gecacht); der lokale `people`-Eintrag dient als
// Name/Foto-Fallback, falls TMDB deaktiviert ist oder scheitert.
func (s *Server) getPerson(w http.ResponseWriter, r *http.Request) {
	tmdbID, err := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	if err != nil {
		writeError(w, 400, "ungültige tmdbId")
		return
	}
	local, _ := s.Store.GetPersonByTMDB(tmdbID)

	if s.Enrich != nil && s.Enrich.Client().Enabled() {
		if pd, err := s.Enrich.Client().GetPersonDetails(r.Context(), tmdbID); err == nil && pd != nil {
			if pd.Name == "" && local != nil {
				pd.Name = local.Name
			}
			if pd.ProfilePath == "" && local != nil {
				pd.ProfilePath = local.ProfilePath
			}
			writeJSON(w, 200, pd)
			return
		}
	}

	if local == nil {
		writeError(w, 404, "Person nicht bekannt")
		return
	}
	// TMDB nicht verfügbar → nur die lokalen Grunddaten, leere Filmografie.
	writeJSON(w, 200, map[string]any{
		"tmdbId":      local.TMDBID,
		"name":        local.Name,
		"profilePath": local.ProfilePath,
		"filmography": []any{},
	})
}

// getPersonProfile liefert das gecachte Profilbild (oder Placeholder).
func (s *Server) getPersonProfile(w http.ResponseWriter, r *http.Request) {
	tmdbID, err := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	p, err := s.Store.GetPersonByTMDB(tmdbID)
	if err != nil || p == nil || p.ProfilePath == "" {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	path := s.Enrich.EnsureProfileCached(r.Context(), tmdbID, p.ProfilePath)
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
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, path, info.ModTime(), f)
}
