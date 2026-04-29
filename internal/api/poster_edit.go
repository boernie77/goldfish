package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// listMetadataPosters gibt eine Liste verfügbarer Poster aus TMDB zurück
// (Movie- bzw. TV-Type abhängig). Wird vom Poster-Picker-Dialog im Frontend
// genutzt — dem User wird ein Grid an Varianten angezeigt, eine zum Auswählen.
func (s *Server) listMetadataPosters(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if s.Enrich == nil || !s.Enrich.Client().Enabled() {
		writeError(w, 400, "TMDB nicht konfiguriert")
		return
	}
	md, err := s.Store.GetMetadata(id)
	if err != nil || md == nil {
		writeError(w, 404, "Metadata nicht gefunden")
		return
	}
	if md.TMDBID == 0 {
		writeJSON(w, 200, []any{})
		return
	}
	client := s.Enrich.Client()
	ctx := r.Context()
	var posters any
	switch md.TMDBType {
	case "movie", "omdb_movie":
		posters, err = client.MoviePosters(ctx, md.TMDBID)
	case "tv", "omdb_tv":
		posters, err = client.TVPosters(ctx, md.TMDBID)
	case "episode":
		// Für Episoden TMDB-Show-Poster nutzen — bei Single-Episode-Files
		// hilfreich, aber selten genutzt.
		if md.ParentID > 0 {
			if parent, _ := s.Store.GetMetadata(md.ParentID); parent != nil && parent.TMDBID > 0 {
				posters, err = client.TVPosters(ctx, parent.TMDBID)
			}
		}
	default:
		posters = []any{}
	}
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	if posters == nil {
		posters = []any{}
	}
	writeJSON(w, 200, posters)
}

// setMetadataPoster akzeptiert entweder JSON `{tmdbPath: "/abc.jpg"}` zum
// Auswählen aus den TMDB-Bildern oder eine multipart/form-data-Anfrage mit
// einem `file`-Feld für ein eigenes Poster (max 8 MiB, image/*).
//
// Beide Varianten:
//   1. Datei-Bytes besorgen
//   2. Unter `posterFilename(metaID, syntheticPath)` in /config/posters/ speichern
//   3. metadata.poster_path auf den syntheticPath setzen
func (s *Server) setMetadataPoster(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	md, err := s.Store.GetMetadata(id)
	if err != nil || md == nil {
		writeError(w, 404, "Metadata nicht gefunden")
		return
	}

	posterDir := s.PosterDir
	if posterDir == "" {
		writeError(w, 500, "PosterDir nicht konfiguriert")
		return
	}

	contentType := r.Header.Get("Content-Type")
	var data []byte
	var ext string
	var newPath string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Custom-Upload
		if err := r.ParseMultipartForm(8 * 1024 * 1024); err != nil {
			writeError(w, 400, "Upload zu groß oder fehlerhaft: "+err.Error())
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeError(w, 400, "kein file-Feld im Upload")
			return
		}
		defer func() { _ = f.Close() }()
		// Mime-Check über Header (vom Browser gesetzt)
		ct := hdr.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "image/") {
			writeError(w, 400, "nur Bilddateien erlaubt (image/*)")
			return
		}
		ext = filepath.Ext(hdr.Filename)
		if ext == "" {
			ext = ".jpg"
		}
		buf, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if len(buf) > 8*1024*1024 {
			writeError(w, 400, "Datei größer als 8 MiB")
			return
		}
		data = buf
		// Synthetisch eindeutiger Pfad (so dass posterFilename() einen
		// stabilen Hash baut). Timestamp hängt dran damit der Pfad sich nach
		// einem Re-Upload ändert → Browser holt das neue Bild.
		newPath = fmt.Sprintf("custom:%d:%d%s", id, time.Now().UnixNano(), ext)
	} else {
		// JSON-Variante: TMDB-Pick
		var body struct {
			TMDBPath string `json:"tmdbPath"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "ungültiges JSON")
			return
		}
		if body.TMDBPath == "" {
			writeError(w, 400, "tmdbPath nötig")
			return
		}
		if s.Enrich == nil || !s.Enrich.Client().Enabled() {
			writeError(w, 400, "TMDB nicht konfiguriert")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		buf, _, err := s.Enrich.Client().DownloadPoster(ctx, body.TMDBPath, "w500")
		if err != nil {
			writeError(w, 502, "TMDB-Download: "+err.Error())
			return
		}
		data = buf
		newPath = body.TMDBPath
	}

	// Datei unter dem Hash-Filename ablegen.
	filename := posterFilenameForEdit(id, newPath)
	out := filepath.Join(posterDir, filename)
	if err := os.WriteFile(out, data, 0o644); err != nil {
		writeError(w, 500, "Datei schreiben: "+err.Error())
		return
	}
	if err := s.Store.SetMetadataPosterPath(id, newPath); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	log.Printf("[poster-edit] metadata=%d new_path=%q file=%s", id, newPath, filename)
	writeJSON(w, 200, map[string]any{"posterPath": newPath})
}

// posterFilenameForEdit muss MIT dem Schema in enrich.posterFilename
// übereinstimmen — sonst findet die Serve-Logik die Datei nicht. Wir
// duplizieren die Mini-Funktion hier statt sie zu exportieren, weil das
// Schema sehr stabil ist (sha1 8-Byte-Prefix + Extension).
func posterFilenameForEdit(metaID int64, path string) string {
	sum := sha1.Sum([]byte(path))
	return "poster_" + hex.EncodeToString(sum[:8]) + filepath.Ext(path)
}
