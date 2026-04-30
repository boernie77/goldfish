package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/rename"
	"github.com/boernie77/goldfish/internal/store"
)

// renameSettingKey: das Setting, das den Auto-Rename-Hook im
// confirmItemMetadata-Handler aktiviert.
const renameSettingKey = "auto_rename_confirmed_movies"

// renamePreview liefert den Ziel-Dateinamen ohne Side-Effect.
// Frontend nutzt das fuer die Vorschau-Zeile am „Umbenennen"-Button.
// Auch erreichbar fuer Non-Admins, weil rein lesend.
//
// Response:
//   {target: "/abs/path/to/Inception (2010).mkv",
//    targetBase: "Inception (2010).mkv",
//    alreadyOK: false,    // true = Datei traegt schon den Wunschnamen
//    canRename: true,     // false = wuerde fehlschlagen (Title leer etc.)
//    reason: "..."        // bei canRename=false: Begruendung
//   }
func (s *Server) renamePreview(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	target, alreadyOK, reason := computeRenameTarget(it)
	resp := map[string]any{
		"canRename": reason == "",
		"alreadyOK": alreadyOK,
		"reason":    reason,
	}
	if target != "" {
		resp["target"] = target
		resp["targetBase"] = filepath.Base(target)
	}
	writeJSON(w, 200, resp)
}

// computeRenameTarget zentralisiert die Logik „kann das Item umbenannt werden,
// und wenn ja wohin". Liefert (target, alreadyOK, reasonIfNotPossible).
func computeRenameTarget(it *model.Item) (string, bool, string) {
	if it.Metadata == nil || it.Metadata.TMDBType != "movie" {
		return "", false, "Nur Filme werden umbenannt (kein Movie-Metadata)."
	}
	if !it.MetadataConfirmed {
		return "", false, "Zuordnung nicht bestaetigt."
	}
	title := it.Metadata.Title
	year := it.Metadata.Year
	target, alreadyOK, err := rename.PreviewTarget(it.Path, title, year)
	if err != nil {
		return "", false, err.Error()
	}
	return target, alreadyOK, ""
}

// renameItemNow fuehrt das Rename eines einzelnen Items aus. Admin-only.
// Funktioniert auch wenn das Setting auto_rename_confirmed_movies AUS ist —
// das ist gewollt, damit der User VOR Aktivierung des Settings einzelne
// Files testweise umbenennen kann.
func (s *Server) renameItemNow(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	histID, target, msg, code := s.executeRename(it, "manual")
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	writeJSON(w, 200, map[string]any{
		"renameHistoryId": histID,
		"newPath":         target,
		"newBase":         filepath.Base(target),
	})
}

// executeRename: gemeinsamer Pfad fuer manual + auto + bulk. Liefert
// (historyID, newPath, errMsg, httpStatusCode). status=0 bedeutet OK.
// Bei alreadyOK liefert es (0, currentPath, "", 0).
func (s *Server) executeRename(it *model.Item, triggeredBy string) (int64, string, string, int) {
	target, alreadyOK, reason := computeRenameTarget(it)
	if reason != "" {
		return 0, "", reason, 400
	}
	if alreadyOK {
		// Datei traegt bereits den Wunschnamen → no-op, kein History-Eintrag.
		return 0, target, "", 0
	}
	if err := rename.RenameOnDisk(it.Path, target); err != nil {
		return 0, "", "Rename auf Disk fehlgeschlagen: " + err.Error(), 500
	}
	// rel_path: Library-relativer Teil. Wir bauen ihn neu, indem wir den
	// alten rel_path-Verzeichnisanteil mit dem neuen Basename kombinieren.
	newRelPath := filepath.Join(filepath.Dir(it.RelPath), filepath.Base(target))
	histID, err := s.Store.RecordRename(it.ID, it.Path, target, it.RelPath, newRelPath, triggeredBy)
	if err != nil {
		// DB-Update fehlgeschlagen → Datei ist umbenannt, DB veraltet.
		// Best-effort Rollback der Disk-Operation, damit das Item weiter
		// abspielbar bleibt.
		_ = rename.RenameOnDisk(target, it.Path)
		return 0, "", "DB-Update fehlgeschlagen: " + err.Error(), 500
	}
	return histID, target, "", 0
}

// renameUndo macht eine Umbenennung rueckgaengig: schreibt die Datei
// zurueck zum alten Namen und markiert den History-Eintrag als undone.
func (s *Server) renameUndo(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	entry, err := s.Store.GetRenameHistory(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if entry == nil {
		writeError(w, 404, "Rename-Eintrag nicht gefunden")
		return
	}
	if !entry.UndoneAt.IsZero() {
		writeError(w, 409, "bereits rückgängig gemacht")
		return
	}
	// Reverse-Rename auf Disk
	if err := rename.RenameOnDisk(entry.NewPath, entry.OldPath); err != nil {
		writeError(w, 500, "Reverse-Rename fehlgeschlagen: "+err.Error())
		return
	}
	if err := s.Store.MarkRenameUndone(id); err != nil {
		// Disk wurde zurueckgerollt, DB-Update fehlgeschlagen — neuer Versuch
		// das Disk-Rename wieder vorwaerts zu fahren, damit DB+Disk konsistent.
		_ = rename.RenameOnDisk(entry.OldPath, entry.NewPath)
		writeError(w, 500, "DB-Update fehlgeschlagen: "+err.Error())
		return
	}
	w.WriteHeader(204)
}

// renameList: Liste aller History-Eintraege als JSON.
func (s *Server) renameList(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	list, err := s.Store.ListRenameHistory(limit)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []store.RenameHistoryEntry{}
	}
	writeJSON(w, 200, list)
}

// renameCSV: alle History-Eintraege als CSV-Download.
func (s *Server) renameCSV(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListRenameHistory(0)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="goldfish-renames.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "item_id", "renamed_at", "old_path", "new_path", "triggered_by", "undone_at"})
	for _, e := range list {
		undone := ""
		if !e.UndoneAt.IsZero() {
			undone = e.UndoneAt.Format("2006-01-02T15:04:05Z")
		}
		_ = cw.Write([]string{
			strconv.FormatInt(e.ID, 10),
			strconv.FormatInt(e.ItemID, 10),
			e.RenamedAt.Format("2006-01-02T15:04:05Z"),
			e.OldPath,
			e.NewPath,
			e.TriggeredBy,
			undone,
		})
	}
	cw.Flush()
}

// renameAllConfirmed: Bulk-Rename fuer alle bestaetigten Filme, die noch
// nicht im Wunsch-Schema liegen. Liefert eine Summary mit Zaehlern.
// Skipt Items mit Fehler stillschweigend (Fehler kommen ins Log).
func (s *Server) renameAllConfirmed(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListConfirmedMovies()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	stats := struct {
		Total    int      `json:"total"`
		Renamed  int      `json:"renamed"`
		Skipped  int      `json:"skipped"`  // bereits im Wunschnamen
		Failed   int      `json:"failed"`
		Failures []string `json:"failures,omitempty"`
	}{Total: len(items)}
	for i := range items {
		it := &items[i]
		_, target, msg, code := s.executeRename(it, "bulk")
		if code != 0 {
			stats.Failed++
			if len(stats.Failures) < 20 {
				stats.Failures = append(stats.Failures,
					fmt.Sprintf("[%d] %s: %s", it.ID, filepath.Base(it.Path), msg))
			}
			continue
		}
		if target == it.Path {
			stats.Skipped++
		} else {
			stats.Renamed++
		}
	}
	writeJSON(w, 200, stats)
}

// settingAutoRenameOn liefert true, wenn der Auto-Rename-Hook aktiv ist.
func (s *Server) settingAutoRenameOn() bool {
	v, _ := s.Store.GetSetting(renameSettingKey, "")
	return strings.EqualFold(v, "true") || v == "1"
}
