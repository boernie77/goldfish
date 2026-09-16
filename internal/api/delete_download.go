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
	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/playback"
	"github.com/boernie77/goldfish/internal/store"
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

	if err := s.deleteItemFilesAndRow(it); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	_ = s.Store.LogActivity(me.ID, me.Username, "admin", "item_delete", fmt.Sprintf("%q (%s)", it.Title, it.RelPath), deviceLabel(r))
	w.WriteHeader(204)
}

// deleteItemFilesAndRow entfernt Video-Datei, Thumbnail, Trickplay-Daten,
// Subtitle-Cache und den DB-Eintrag eines Items. Loggt selbst NICHTS ins
// Aktivitäts-Protokoll — das entscheiden die Aufrufer (Einzel-Löschung vs.
// Sammel-Aktion mit einem Eintrag pro Lauf, siehe „Aktivitäts-Protokoll" in
// CLAUDE.md).
func (s *Server) deleteItemFilesAndRow(it *model.Item) error {
	// 1. Video-Datei vom Filesystem entfernen.
	if err := os.Remove(it.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Datei konnte nicht gelöscht werden: %w"+
			" (ist /media read-only gemountet? Im Compose-File ':ro' entfernen.)", err)
	}
	// 2. Thumbnail
	if it.ThumbPath != "" {
		_ = os.Remove(it.ThumbPath)
	}
	// 3. Trickplay-Verzeichnis
	if s.Trickplay != nil {
		s.Trickplay.Delete(it.ID)
	}
	// 4. Subtitle-Cache
	if s.SubsDir != "" {
		_ = os.RemoveAll(filepath.Join(s.SubsDir, strconv.FormatInt(it.ID, 10)))
	}
	// 5. DB-Eintrag — CASCADE räumt verknüpfte Tabellen auf
	return s.Store.DeleteItem(it.ID)
}

// deleteWatchedExceptLast löscht alle für den aktuellen User gesehenen Videos
// einer PRIVATEN Bibliothek (optional auf einen Ordner beschränkt, rekursiv),
// behält aber pro physischem Ordner IMMER das chronologisch letzte Video —
// egal ob gesehen oder nicht. Gedacht für YouTube-Kanal-Ordner mit vielen
// bereits angesehenen Folgen, damit dort nie alle Videos verschwinden.
// Bewusst NUR für kind=private — Serien/Filme dürfen dieser Aktion nie
// zugänglich sein (User-Vorgabe).
func (s *Server) deleteWatchedExceptLast(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil || !me.IsAdmin {
		writeError(w, 403, "Administrator erforderlich")
		return
	}
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lib, err := s.Store.GetLibrary(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if lib == nil {
		writeError(w, 404, "Bibliothek nicht gefunden")
		return
	}
	if lib.Kind != model.KindPrivate {
		writeError(w, 400, "Nur für private Bibliotheken verfügbar")
		return
	}
	// Server-seitige Durchsetzung, nicht nur UI-Ausblenden: eine Bibliothek,
	// für die der Admin den Button nicht freigeschaltet hat, darf diese
	// Aktion auch per direktem API-Aufruf nicht ausführen können.
	if !lib.DeleteWatchedButtonEnabled {
		writeError(w, 403, "Für diese Bibliothek nicht freigeschaltet (siehe 🔤 Anzeige)")
		return
	}
	folder := r.URL.Query().Get("folder")

	// Aufsteigend nach "released" — der letzte Treffer je Ordner ist damit
	// automatisch das jüngste Video dieses Ordners.
	items, err := s.Store.ListItems(store.ItemFilter{
		LibraryID: libID,
		Folder:    folder,
		Sort:      "released",
		SortDir:   "asc",
		UserID:    me.ID,
		IsAdmin:   true,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	folderOf := func(relPath string) string {
		if idx := strings.LastIndex(relPath, "/"); idx >= 0 {
			return relPath[:idx]
		}
		return ""
	}

	keepID := map[string]int64{}
	for _, it := range items {
		keepID[folderOf(it.RelPath)] = it.ID
	}

	deleted, failed := 0, 0
	for _, it := range items {
		if !it.Watched || it.ID == keepID[folderOf(it.RelPath)] {
			continue
		}
		itCopy := it
		if err := s.deleteItemFilesAndRow(&itCopy); err != nil {
			failed++
			continue
		}
		deleted++
	}

	detail := fmt.Sprintf("%d gelöscht", deleted)
	if folder != "" {
		detail = fmt.Sprintf("%q: %s", folder, detail)
	}
	if failed > 0 {
		detail += fmt.Sprintf(", %d fehlgeschlagen", failed)
	}
	_ = s.Store.LogActivity(me.ID, me.Username, "admin", "delete_watched_except_last", detail, deviceLabel(r))

	writeJSON(w, 200, map[string]any{"deleted": deleted, "failed": failed})
}

// downloadItem serviert eine Videodatei als Download (Content-Disposition: attachment).
// Authentifizierte User mit Library-ACL + Alterserlaubnis dürfen herunterladen —
// zusätzlich seit 2026-09-02 nur, wenn der Account das per Benutzerverwaltung
// zugestandene "Downloads erlauben" nicht deaktiviert hat (Admins ausgenommen).
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
	if !s.requireDownloadAllowed(w, r) {
		return
	}

	// Protokollieren, WER was herunterlädt (User-Wunsch 2026-09-14). Anlass:
	// eine stundenlang laufende Formatanpassung eines 4K-Remux war im
	// Aktivitäts-Protokoll nirgends sichtbar — Downloads waren die einzige
	// teure Nutzeraktion ohne jede Spur, das Gerät liess sich hinterher nicht
	// mehr zuordnen. Bewusst NUR beim ERSTEN Request der Übertragung: ein
	// Resume schickt `Range: bytes=<offset>-` und würde sonst pro Fortsetzung
	// eine weitere Zeile erzeugen (gleiche Konvention wie "ein Eintrag pro
	// Lauf, nicht pro Datei" bei Scan/OCR).
	if rng := r.Header.Get("Range"); rng == "" || strings.HasPrefix(rng, "bytes=0-") {
		s.logDownload(r, it, r.URL.Query().Get("profile"), "download_start",
			r.URL.Query().Get("compat") == "1")
	}

	playPath := it.Path
	// "Optimierte Downloads" (User-Wunsch 2026-09-11, Plex-Vorbild
	// "Optimierte Versionen"): optionaler `&profile=`-Parameter, gleicher
	// Katalog wie beim Streaming (`playback.Profiles`). Wirkt NUR als echter
	// Auflösungs-/Bitrate-Cap, wenn das Item ihn tatsächlich überschreitet —
	// "Automatisch" (kein Parameter, oder "orig") lädt unverändert das
	// Original/die reine Codec-Fix-Kopie wie bisher. `ProfileByID("")`
	// liefert bereits `Profiles[0]` ("orig"), keine explizite Prüfung nötig.
	profile := playback.ProfileByID(r.URL.Query().Get("profile"))
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
		p, err := download.EnsureCompatible(r.Context(), s.HW, cacheDir, it.ID, it.Path, it.Container, it.VideoCodec, it.AudioCodec, profile, it.Height, it.BitrateKbps)
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
		// Zugriffs-Uhr der Cache-Kopie zuruecksetzen, BEVOR ServeContent den
		// Handler fuer die Dauer der Uebertragung belegt. Schuetzt laufende und
		// unterbrochene Downloads vor dem Aufraeumer (internal/download/cleanup.go).
		download.MarkServed(playPath)
		// Formatangepasste Kopie ist immer .mp4, unabhängig vom Original-Container.
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		// Nur wenn WIRKLICH runtergerechnet wurde (Cache-Pfad trägt den
		// Profil-Suffix, siehe `plan()`) den Dateinamen entsprechend
		// kennzeichnen — bei "Automatisch"/Profil-unter-Cap ist es exakt
		// dieselbe Datei wie beim reinen Codec-Fix, kein irreführendes Tag.
		if strings.HasSuffix(filepath.Base(playPath), "-"+profile.ID+".mp4") {
			base += " (" + profile.ID + ")"
		}
		filename = base + ".mp4"
		// Cache-Validator (ETag + Last-Modified) primär an die QUELLDATEI koppeln
		// (mtime+size): die bleibt über Resume-Versuche stabil, anders als die
		// ModTime der Cache-Kopie — das war die Ursache für „Download klebt bei
		// 99 %" (If-Range matchte nach einer Neuerzeugung nicht mehr → 200 statt
		// 206). ZUSÄTZLICH die GRÖSSE der ausgelieferten Cache-Datei mit
		// aufnehmen: ändert sich die Konvertierungs-Logik (convVersion, z. B.
		// 10-Bit-h264 wird jetzt re-encodet statt kopiert), hat die neue Kopie
		// eine andere Größe → ETag ändert sich → ein alter, teilweise
		// heruntergeladener Stand wird sauber komplett neu geladen statt
		// korrupt zusammengestückelt (Quelle unverändert, Inhalt aber komplett
		// anders).
		compatSize := int64(0)
		if info != nil {
			compatSize = info.Size()
		}
		if si, serr := os.Stat(it.Path); serr == nil {
			modTime = si.ModTime()
			w.Header().Set("ETag", fmt.Sprintf(`"compat-%d-%d-%d-%d"`,
				it.ID, si.ModTime().UnixNano(), si.Size(), compatSize))
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

// logDownload schreibt einen Aktivitäts-Protokoll-Eintrag für einen Download
// bzw. dessen (teure) server-seitige Formatanpassung. Gemeinsam genutzt von
// `downloadItem` und `downloadCompatStatus`, damit beide Auslöser denselben
// Detail-Text erzeugen — die Vorbereitung läuft detached weiter, auch wenn der
// Client danach nie die fertige Datei abholt, und ist deshalb eigenständig
// protokollwürdig.
func (s *Server) logDownload(r *http.Request, it *model.Item, profileID, action string, compat bool) {
	me := currentUser(r)
	var uid int64
	name := ""
	if me != nil {
		uid, name = me.ID, me.Username
	}
	how := "Original"
	if compat {
		how = "angepasst"
		if p := playback.ProfileByID(profileID); p.ID != "" && p.ID != "orig" {
			how = "angepasst, " + p.ID
		}
	}
	detail := fmt.Sprintf("%q (%s)", it.Title, how)
	_ = s.Store.LogActivity(uid, name, "download", action, detail, deviceLabel(r))
}
