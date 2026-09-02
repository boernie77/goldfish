package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/boernie77/goldfish/internal/store"
)

// downloadBackup: GET /api/admin/backup — erzeugt eine frische, konsistente
// Kopie der GESAMTEN Datenbank (Items, TMDB-Zuordnungen, OCR-/Whisper-Jobs,
// Benutzer, Einstellungen, Aktivitäts-Protokoll, …) und liefert sie zum
// Download. Enthält bewusst NICHT die Mediendateien selbst (/media) oder die
// Cache-Ordner (Poster/Thumbnails/Trickplay lassen sich jederzeit neu
// erzeugen) — nur die eigentliche Datenbank.
func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	tmpPath := filepath.Join(s.ConfigDir, "backups", fmt.Sprintf(".tmp-backup-%d.db", time.Now().UnixNano()))
	if err := s.Store.BackupToFile(tmpPath); err != nil {
		writeError(w, 500, "Backup fehlgeschlagen: "+err.Error())
		return
	}
	defer func() { _ = os.Remove(tmpPath) }()

	fname := fmt.Sprintf("goldfish-backup-%s.db", time.Now().Format("2006-01-02_1504"))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fname))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, tmpPath)

	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "backup_download", "Datenbank-Backup heruntergeladen")
	}
}

// uploadRestore: POST /api/admin/backup/restore (multipart, Feld "file").
// Prüft die hochgeladene Datei, sichert die aktuelle DB, tauscht sie aus und
// beendet danach ABSICHTLICH den Prozess (siehe Store.RestoreFromFile-
// Kommentar) — Docker "restart: unless-stopped" startet den Container
// automatisch mit der wiederhergestellten Datenbank neu.
func (s *Server) uploadRestore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(2 << 30); err != nil { // bis 2 GiB
		writeError(w, 400, "Upload zu groß oder ungültig: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "keine Datei im Feld 'file' gefunden")
		return
	}
	defer func() { _ = file.Close() }()

	// Gleiches Verzeichnis wie die aktive DB (ConfigDir) — RestoreFromFile
	// verschiebt per os.Rename, das geht nur innerhalb desselben Volumes.
	incomingPath := filepath.Join(s.ConfigDir, fmt.Sprintf(".restore-incoming-%d.db", time.Now().UnixNano()))
	out, err := os.Create(incomingPath)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		_ = out.Close()
		_ = os.Remove(incomingPath)
		writeError(w, 500, "konnte Upload nicht speichern: "+err.Error())
		return
	}
	_ = out.Close()

	if err := store.ValidateBackupFile(incomingPath); err != nil {
		_ = os.Remove(incomingPath)
		writeError(w, 400, err.Error())
		return
	}

	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "backup_restore", "Datenbank-Restore gestartet — Server startet danach neu")
	}

	safetyPath, err := s.Store.RestoreFromFile(incomingPath)
	if err != nil {
		_ = os.Remove(incomingPath)
		writeError(w, 500, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"ok":               true,
		"safetyBackupPath": safetyPath,
		"message":          "Wiederhergestellt. Der Server startet jetzt neu — bitte in ca. 15 Sekunden neu laden.",
	})

	// Bewusst NACH dem Response-Schreiben, damit der Client die
	// Erfolgsmeldung noch bekommt, bevor der Prozess (und die HTTP-
	// Verbindung) endet.
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}
