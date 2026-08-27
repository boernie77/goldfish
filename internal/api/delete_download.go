package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/boernie77/goldfish/internal/download"
)

// deleteItem löscht ein Item vom Filesystem UND aus der Datenbank. Admin-only.
// Zusätzlich werden Thumbnail, Trickplay-Daten und Subtitle-Cache entfernt.
func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil || !me.IsAdmin {
		writeError(w, 403, "Administrator erforderlich")
		return
	}
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

	// 1. Video-Datei vom Filesystem entfernen.
	if err := os.Remove(it.Path); err != nil && !os.IsNotExist(err) {
		writeError(w, 500, "Datei konnte nicht gelöscht werden: "+err.Error()+
			" (ist /media read-only gemountet? Im Compose-File ':ro' entfernen.)")
		return
	}

	// 2. Thumbnail
	if it.ThumbPath != "" {
		_ = os.Remove(it.ThumbPath)
	}
	// 3. Trickplay-Verzeichnis
	if s.Trickplay != nil {
		s.Trickplay.Delete(id)
	}
	// 4. Subtitle-Cache
	if s.SubsDir != "" {
		_ = os.RemoveAll(filepath.Join(s.SubsDir, strconv.FormatInt(id, 10)))
	}
	// 5. DB-Eintrag — CASCADE räumt verknüpfte Tabellen auf
	if err := s.Store.DeleteItem(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// downloadItem serviert eine Videodatei als Download (Content-Disposition: attachment).
// Alle authentifizierten User mit Library-ACL dürfen herunterladen.
func (s *Server) downloadItem(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if !s.requireAgeAllowed(w, r, it.MetadataID) {
		return
	}

	playPath := it.Path
	// User-Anfrage 2026-08-27: "wie löst Jellyfin das eigentlich" — statt die
	// Original-Datei blind rauszugeben und den Client (Mac/iOS-App) sie danach
	// selbst per lokalem ffmpeg reparieren zu lassen (mit dem realen Bug, dass
	// dabei eine zweite Tonspur verloren ging, siehe `internal/download`s
	// Doku-Kommentar), entscheidet jetzt der SERVER — analog zu Jellyfins
	// Geräteprofil-Direct-Play-Logik — VOR dem Ausliefern, ob die Datei
	// überhaupt angefasst werden muss, und liefert sonst eine einmalig
	// erzeugte, dauerhaft gecachte kompatible Kopie. Opt-in über `?compat=1`,
	// damit Browser/Android (die die Original-Datei wie bisher wollen bzw.
	// selbst breiter dekodieren können) unverändert bleiben.
	if r.URL.Query().Get("compat") == "1" {
		cacheDir := filepath.Join(s.ConfigDir, "cache", "downloads")
		p, err := download.EnsureCompatible(r.Context(), s.HW, cacheDir, it.ID, it.Path, it.Container, it.VideoCodec, it.AudioCodec)
		if err != nil {
			writeError(w, 500, "Formatanpassung fehlgeschlagen: "+err.Error())
			return
		}
		playPath = p
	}

	f, err := os.Open(playPath)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()

	filename := filepath.Base(it.Path)
	modTime := info.ModTime()
	if playPath != it.Path {
		// Formatangepasste Kopie ist immer .mp4, unabhängig vom Original-Container.
		filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ".mp4"
		// Cache-Validator (ETag + Last-Modified) an die QUELLDATEI koppeln,
		// nicht an die ModTime der Cache-Kopie: die wechselt bei jeder
		// Neuerzeugung, wodurch der `If-Range`-Header eines Resume-Versuchs
		// (Apple-App nach einem Read-Timeout) nicht mehr matcht →
		// `http.ServeContent` liefert 200 statt 206 → die App verrechnet sich
		// und der Download bleibt bei 99 % hängen. Solange die Quelle
		// unverändert ist, sind ETag + Last-Modified jetzt über alle Versuche
		// hinweg stabil; ändert sich die Quelle, erzwingt der neue ETag
		// korrekt einen sauberen Voll-Download.
		if si, serr := os.Stat(it.Path); serr == nil {
			modTime = si.ModTime()
			w.Header().Set("ETag", fmt.Sprintf(`"compat-%d-%d-%d"`,
				it.ID, si.ModTime().UnixNano(), si.Size()))
		}
	}
	// RFC 5987 für UTF-8-Dateinamen (inkl. Umlaute etc.)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
			sanitizeASCII(filename), url.PathEscape(filename)))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, filename, modTime, f)
}

// sanitizeASCII ersetzt Nicht-ASCII-Zeichen durch '_', um einen sicheren
// fallback-Dateinamen für alte Browser zu geben.
func sanitizeASCII(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '"' || c >= 0x80 {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
