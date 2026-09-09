package api

import (
	"encoding/json"
	"net/http"
)

// listScanExcludes liefert alle vom Auto-Scan ausgeschlossenen Ordner einer
// Bibliothek. Admin-only.
func (s *Server) listScanExcludes(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	folders, err := s.Store.ListScanExcludedFolders(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if folders == nil {
		folders = []string{}
	}
	writeJSON(w, 200, folders)
}

// setScanExclude aktiviert/deaktiviert den Auto-Scan-Ausschluss für einen
// Ordner. Wirkt NUR auf den zeitgesteuerten Auto-Scan (Scanner.Start mit
// respectScanExcludes=true) — ein manueller Scan (⟳-Button) ignoriert diese
// Liste bewusst und scannt immer alles (User-Vorgabe 2026-09-09: manuell
// ausgelöste Scans sollen unabhängig davon sein, wo/wie ein Ordner gerade
// gemountet ist). Der Ordner wird im Auto-Scan-Fall beim Walk übersprungen
// und seine bereits in der DB stehenden Items sind vor dem Orphan-Cleanup
// geschützt (siehe internal/scanner/scanner.go). folder="" schließt die
// gesamte Bibliothek aus. Admin-only.
func (s *Server) setScanExclude(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder   string `json:"folder"`
		Excluded bool   `json:"excluded"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetScanExcludedFolder(libID, body.Folder, body.Excluded); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if me := currentUser(r); me != nil {
		action := "scan_exclude_removed"
		if body.Excluded {
			action = "scan_exclude_added"
		}
		label := body.Folder
		if label == "" {
			label = "(gesamte Bibliothek)"
		}
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", action, label)
	}
	w.WriteHeader(204)
}
