package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
//
//	{target: "/abs/path/to/Inception (2010).mkv",
//	 targetBase: "Inception (2010).mkv",
//	 alreadyOK: false,    // true = Datei traegt schon den Wunschnamen
//	 canRename: true,     // false = wuerde fehlschlagen (Title leer etc.)
//	 reason: "..."        // bei canRename=false: Begruendung
//	}
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
	target, alreadyOK, reason := s.computeRenameTargetForItem(it)
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
//
// Bedingungen:
//   - Library muss kind=movies sein (Bluray-Libs etc. haben auch kind=movies,
//     auch wenn sie anders heissen). TV/Private bleiben unberuehrt.
//   - Item-Metadata muss tmdb_type=movie sein.
//   - metadata_confirmed muss gesetzt sein.
func (s *Server) computeRenameTargetForItem(it *model.Item) (string, bool, string) {
	lib, err := s.Store.GetLibrary(it.LibraryID)
	if err != nil || lib == nil {
		return "", false, "Library nicht gefunden."
	}
	if lib.Kind != "movies" {
		return "", false, "Library ist nicht vom Typ Filme (kind=" + string(lib.Kind) + ")."
	}
	if it.Metadata == nil || it.Metadata.TMDBType != "movie" {
		return "", false, "Item-Metadata ist kein Movie."
	}
	if !it.MetadataConfirmed {
		return "", false, "Zuordnung nicht bestaetigt."
	}
	title := it.Metadata.Title
	year := it.Metadata.Year
	target, alreadyOK, perr := rename.PreviewTarget(it.Path, title, year)
	if perr != nil {
		return "", false, perr.Error()
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
	if me := currentUser(r); me != nil && histID != 0 {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "item_rename", fmt.Sprintf("%q → %q", filepath.Base(it.Path), filepath.Base(target)))
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
	target, alreadyOK, reason := s.computeRenameTargetForItem(it)
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
		Skipped  int      `json:"skipped"` // bereits im Wunschnamen
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
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "item_rename_bulk",
			fmt.Sprintf("%d umbenannt, %d übersprungen, %d fehlgeschlagen (von %d)", stats.Renamed, stats.Skipped, stats.Failed, stats.Total))
	}
	writeJSON(w, 200, stats)
}

// moveItem verschiebt eine Datei in einen anderen Ordner — optional auch in
// eine andere Bibliothek (targetLibraryId, seit 2026-07-12). Admin-only.
//
// Body: {targetFolder: "Anime/Staffel 2", targetLibraryId?: 3} —
// targetFolder ist relativ zur (Ziel-)Library-Wurzel, "" = Wurzel.
// targetLibraryId fehlt/0 → bleibt in derselben Library. Zielordner muss
// nicht existieren, wird bei Bedarf angelegt.
func (s *Server) moveItem(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		TargetFolder    string `json:"targetFolder"`
		TargetLibraryID int64  `json:"targetLibraryId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiger Body")
		return
	}
	if body.TargetLibraryID > 0 && body.TargetLibraryID != it.LibraryID {
		if !s.requireLibAccess(w, r, body.TargetLibraryID) {
			return
		}
	}
	histID, target, msg, code := s.executeMove(it, body.TargetLibraryID, body.TargetFolder, "move")
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "item_move", fmt.Sprintf("%q → %q", it.RelPath, target))
	}
	writeJSON(w, 200, map[string]any{
		"renameHistoryId": histID,
		"newPath":         target,
	})
}

// executeMove: gemeinsamer Pfad für Einzel- und Bulk-Move. Liefert
// (historyID, newPath, errMsg, httpStatusCode) — status=0 bedeutet OK.
// targetLibraryID=0 → bleibt in it.LibraryID (Standardfall).
//
// Root-Auflösung:
//   - Gleiche Library: der "Root" eines Items (welcher der ggf. mehreren
//     library_paths-Quellordner es physisch ist) wird aus Path/RelPath selbst
//     abgeleitet (Path minus "/"+RelPath-Suffix) statt aus library_paths
//     nachgeschlagen — dadurch bleibt ein verschobenes Item im selben
//     physischen Quellordner wie zuvor, auch bei Multi-Path-Bibliotheken.
//     Verschieben ÜBER zwei verschiedene Quellordner DERSELBEN Library
//     hinweg ist bewusst nicht unterstützt (Storage-Grenzen könnte der User
//     nicht im Kopf haben — sonst landet die Datei überraschend auf einem
//     anderen Volume).
//   - Andere Ziel-Library: nutzt deren ERSTEN `library_paths`-Eintrag (bzw.
//     `libraries.path` als Fallback bei Single-Path-Libs) als Root. Bei
//     Multi-Path-Ziel-Libraries landet die Datei immer im ersten Quellordner —
//     der User kann sie danach bei Bedarf per erneutem Move innerhalb der
//     Ziel-Library weiter verschieben.
func (s *Server) executeMove(it *model.Item, targetLibraryID int64, targetFolder, triggeredBy string) (int64, string, string, int) {
	targetFolder = strings.Trim(strings.TrimSpace(targetFolder), "/")
	// Traversal-Schutz: keine ".."-Segmente, kein Backslash.
	for _, seg := range strings.Split(targetFolder, "/") {
		if seg == ".." || strings.ContainsAny(seg, "\\\x00") {
			return 0, "", "ungültiger Zielordner", 400
		}
	}
	destLibraryID := it.LibraryID
	if targetLibraryID > 0 {
		destLibraryID = targetLibraryID
	}
	var root string
	if destLibraryID == it.LibraryID {
		relSlash := filepath.ToSlash(it.RelPath)
		root = strings.TrimSuffix(filepath.ToSlash(it.Path), "/"+relSlash)
		if root == filepath.ToSlash(it.Path) {
			return 0, "", "Item-Pfad/RelPath inkonsistent — Move abgebrochen", 500
		}
	} else {
		paths, err := s.Store.LibraryPaths(destLibraryID)
		if err != nil {
			return 0, "", "Ziel-Bibliothek konnte nicht gelesen werden: " + err.Error(), 500
		}
		if len(paths) > 0 {
			root = paths[0]
		} else {
			lib, err := s.Store.GetLibrary(destLibraryID)
			if err != nil || lib == nil {
				return 0, "", "Ziel-Bibliothek nicht gefunden", 404
			}
			root = lib.Path
		}
	}
	base := filepath.Base(it.Path)
	newDir := filepath.Join(root, targetFolder)
	newPath := filepath.Join(newDir, base)
	if newPath == it.Path && destLibraryID == it.LibraryID {
		return 0, it.Path, "", 0 // schon dort — no-op
	}
	// Konflikt am Ziel auflösen (gleiches Schema wie beim Umbenennen: " (2)", " (3)", …).
	resolved := rename.ResolveConflict(newDir, base, it.Path)
	if resolved == "" {
		return 0, "", "Zielordner voll — alle Namens-Varianten belegt", 409
	}
	newPath = resolved
	newRelPath := filepath.Join(targetFolder, filepath.Base(resolved))
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return 0, "", "Zielordner konnte nicht angelegt werden: " + err.Error(), 500
	}
	if err := rename.RenameOnDisk(it.Path, newPath); err != nil {
		return 0, "", "Verschieben auf Disk fehlgeschlagen: " + err.Error(), 500
	}
	histID, err := s.Store.RecordMove(it.ID, it.Path, newPath, it.RelPath, newRelPath, it.LibraryID, destLibraryID, triggeredBy)
	if err != nil {
		_ = rename.RenameOnDisk(newPath, it.Path)
		return 0, "", "DB-Update fehlgeschlagen: " + err.Error(), 500
	}
	return histID, newPath, "", 0
}

// moveItemsBulk verschiebt mehrere Items auf einmal in denselben Zielordner.
// Admin-only. Fehler pro Item werden gesammelt, ein einzelner Fehlschlag
// bricht die restlichen Items nicht ab (analog Bulk-Delete/-Favorite im Frontend).
func (s *Server) moveItemsBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs             []int64 `json:"ids"`
		TargetFolder    string  `json:"targetFolder"`
		TargetLibraryID int64   `json:"targetLibraryId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiger Body")
		return
	}
	if body.TargetLibraryID > 0 {
		if !s.requireLibAccess(w, r, body.TargetLibraryID) {
			return
		}
	}
	stats := struct {
		Total    int      `json:"total"`
		Moved    int      `json:"moved"`
		Failed   int      `json:"failed"`
		Failures []string `json:"failures,omitempty"`
	}{Total: len(body.IDs)}
	for _, id := range body.IDs {
		it, err := s.Store.GetItem(id)
		if err != nil || it == nil {
			stats.Failed++
			continue
		}
		_, _, msg, code := s.executeMove(it, body.TargetLibraryID, body.TargetFolder, "move")
		if code != 0 {
			stats.Failed++
			if len(stats.Failures) < 20 {
				stats.Failures = append(stats.Failures, fmt.Sprintf("[%d] %s: %s", it.ID, filepath.Base(it.Path), msg))
			}
			continue
		}
		stats.Moved++
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "item_move_bulk",
			fmt.Sprintf("%d verschoben, %d fehlgeschlagen (von %d) → %q", stats.Moved, stats.Failed, stats.Total, body.TargetFolder))
	}
	writeJSON(w, 200, stats)
}

// listAllFolders: alle Ordnerpfade (jede Ebene) einer Bibliothek, für die
// Autocomplete-Liste im "Verschieben"-Dialog.
func (s *Server) listAllFolders(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, id) {
		return
	}
	folders, err := s.Store.AllFolderPaths(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if folders == nil {
		folders = []string{}
	}
	writeJSON(w, 200, folders)
}

// settingAutoRenameOn liefert true, wenn der Auto-Rename-Hook aktiv ist.
func (s *Server) settingAutoRenameOn() bool {
	v, _ := s.Store.GetSetting(renameSettingKey, "")
	return strings.EqualFold(v, "true") || v == "1"
}
