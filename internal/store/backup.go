package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Path gibt den Dateipfad der aktiven SQLite-DB zurück (siehe Open()).
// Gebraucht von den Backup/Restore-Handlern in internal/api/backup.go.
func (s *Store) Path() string { return s.path }

// BackupToFile schreibt eine vollständige, konsistente Kopie der aktuellen
// Datenbank nach destPath — inklusive ALLER Inhalte (Items, TMDB-Zuordnungen,
// OCR-/Whisper-Jobs, Benutzer, Einstellungen, Aktivitäts-Protokoll, …).
// Nutzt SQLites eingebautes "VACUUM INTO": checkpointed den WAL-Log
// automatisch und erzeugt eine einzelne, in sich konsistente Datei — ein
// simples Kopieren von .db/.db-wal/.db-shm im laufenden Betrieb wäre
// riskant, weil .db-wal noch nicht übernommene Änderungen enthalten kann.
func (s *Store) BackupToFile(destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(destPath) // VACUUM INTO schlägt fehl, wenn die Zieldatei schon existiert
	_, err := s.db.Exec(`VACUUM INTO ?`, destPath)
	return err
}

// ValidateBackupFile prüft, ob path eine plausible Goldfish-Datenbank ist —
// öffnet sie readonly in einer eigenen, kurzlebigen Verbindung (rührt NICHT
// an der aktiven DB) und prüft Integrität + das Vorhandensein der Kern-Tabellen.
func ValidateBackupFile(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("kann Datei nicht als SQLite-Datenbank öffnen: %w", err)
	}
	defer func() { _ = db.Close() }()

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("integrity_check fehlgeschlagen: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("Datenbank ist beschädigt: %s", integrity)
	}
	for _, table := range []string{"users", "items", "settings", "libraries"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n == 0 {
			return fmt.Errorf("keine gültige Goldfish-Datenbank (Tabelle %q fehlt)", table)
		}
	}
	return nil
}

// RestoreFromFile ersetzt die aktive Datenbank durch incomingPath (muss auf
// demselben Volume liegen wie die aktive DB — os.Rename kann sonst nicht
// filesystemübergreifend verschieben). Ablauf bewusst restart-basiert statt
// Live-Hot-Swap: mehrere Hintergrund-Worker (Enrich, Trickplay, Introskip,
// OCR, Whisper, Auto-Scan) halten langlebige Referenzen auf den *Store — ein
// sauberer Prozess-Neustart (Docker "restart: unless-stopped" fängt das auf)
// ist deutlich robuster, als den *sql.DB unter allen laufenden Zugriffen live
// auszutauschen.
//
//  1. Sicherheitskopie der AKTUELLEN Datenbank (falls sich incomingPath als
//     Fehlgriff herausstellt) nach <config>/backups/pre-restore-<Zeitstempel>.db
//  2. eigene DB-Verbindung schließen
//  3. .db-wal/.db-shm der aktiven DB entfernen (gehören zur alten Datei)
//  4. incomingPath an die Stelle der aktiven DB verschieben
//
// Der Aufrufer (API-Handler) MUSS nach erfolgreicher Rückgabe den Prozess
// beenden (os.Exit) — der Restore wird erst nach dem Neustart wirksam, weil
// diese Store-Instanz danach keine offene DB-Verbindung mehr hat.
func (s *Store) RestoreFromFile(incomingPath string) (safetyBackupPath string, err error) {
	dir := filepath.Dir(s.path)
	safetyBackupPath = filepath.Join(dir, "backups", fmt.Sprintf("pre-restore-%s.db", time.Now().Format("2006-01-02_150405")))
	if err := s.BackupToFile(safetyBackupPath); err != nil {
		return "", fmt.Errorf("Sicherheitskopie vor dem Restore fehlgeschlagen, Restore abgebrochen: %w", err)
	}
	if err := s.db.Close(); err != nil {
		return safetyBackupPath, fmt.Errorf("konnte aktive Datenbank nicht schließen: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(s.path + suffix)
	}
	if err := os.Rename(incomingPath, s.path); err != nil {
		return safetyBackupPath, fmt.Errorf("konnte hochgeladene Datenbank nicht einsetzen: %w", err)
	}
	return safetyBackupPath, nil
}
