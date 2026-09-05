package api

// Automatisches Backup: EINE zeitgesteuerte Aufgabe (kein Array wie bei
// Auto-Scan — für ein "die ganze DB sichern" gibt es keinen sinnvollen
// Grund für mehrere parallele Zeitpläne). Wiederverwendet das Zeitplan-
// Format und matchSchedule() aus autoscan.go 1:1.
//
// Gespeichert in settings.auto_backup_enabled / auto_backup_schedule /
// auto_backup_retention.
//
// Erzeugte Dateien landen unter <ConfigDir>/backups/auto-<Zeitstempel>.db —
// derselbe Ordner, den RestoreFromFile für die pre-restore-Sicherheitskopie
// nutzt. Nach jedem Lauf werden überzählige (älter als die letzten N)
// automatisch gelöscht (Retention).

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/store"
)

const (
	autoBackupEnabledKey   = "auto_backup_enabled"
	autoBackupScheduleKey  = "auto_backup_schedule"
	autoBackupRetentionKey = "auto_backup_retention"
	autoBackupDefaultSched = "daily:4:30"
	autoBackupDefaultKeep  = 7
)

// AutoBackupSettings ist die API-/Persistenz-Form der Automatik-Einstellung.
type AutoBackupSettings struct {
	Enabled   bool   `json:"enabled"`
	Schedule  string `json:"schedule"`
	Retention int    `json:"retention"`
}

func loadAutoBackupSettings(st *store.Store) AutoBackupSettings {
	enabled, _ := st.GetSetting(autoBackupEnabledKey, "false")
	schedule, _ := st.GetSetting(autoBackupScheduleKey, autoBackupDefaultSched)
	retStr, _ := st.GetSetting(autoBackupRetentionKey, strconv.Itoa(autoBackupDefaultKeep))
	retention, err := strconv.Atoi(retStr)
	if err != nil || retention < 1 {
		retention = autoBackupDefaultKeep
	}
	return AutoBackupSettings{Enabled: enabled == "true", Schedule: schedule, Retention: retention}
}

func saveAutoBackupSettings(st *store.Store, cfg AutoBackupSettings) error {
	if cfg.Retention < 1 {
		cfg.Retention = 1
	}
	if cfg.Retention > 60 {
		cfg.Retention = 60
	}
	if err := st.SetSetting(autoBackupEnabledKey, fmt.Sprintf("%v", cfg.Enabled)); err != nil {
		return err
	}
	if err := st.SetSetting(autoBackupScheduleKey, cfg.Schedule); err != nil {
		return err
	}
	return st.SetSetting(autoBackupRetentionKey, strconv.Itoa(cfg.Retention))
}

// autoBackupDir gibt den Zielordner zurück — derselbe wie für pre-restore-
// Sicherheitskopien (siehe Store.RestoreFromFile).
func (s *Server) autoBackupDir() string {
	return filepath.Join(s.ConfigDir, "backups")
}

// autoBackupFilePrefix/-suffix — auch für die Validierung in den
// Download-/Delete-Handlern genutzt (kein Directory-Traversal über
// beliebige Dateinamen).
const (
	autoBackupFilePrefix = "auto-"
	autoBackupFileSuffix = ".db"
)

// RunAutoBackup startet die Hintergrundschleife für automatische Backups.
// Prüft jede Minute, ob der konfigurierte Zeitplan fällig ist — analog
// RunAutoScan, aber nur eine einzelne Aufgabe statt eines Arrays.
func (s *Server) RunAutoBackup(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	var lastFired time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cfg := loadAutoBackupSettings(s.Store)
			if !cfg.Enabled || cfg.Schedule == "" {
				continue
			}
			thisMinute := now.Truncate(time.Minute)
			if thisMinute.Equal(lastFired) {
				continue
			}
			if !matchSchedule(cfg.Schedule, now) {
				continue
			}
			lastFired = thisMinute
			log.Printf("[autobackup] fällig um %s (retention=%d)", now.Format("15:04"), cfg.Retention)
			s.performAutoBackup(cfg.Retention)
		}
	}
}

// performAutoBackup erzeugt eine frische Backup-Datei und räumt danach
// überzählige alte auto-*.db-Dateien weg (Retention). Fehler werden nur
// geloggt — ein fehlgeschlagenes automatisches Backup soll den Server nicht
// beeinträchtigen, der User sieht es im "💾 Backup"-Dialog anhand der Liste.
func (s *Server) performAutoBackup(retention int) {
	dir := s.autoBackupDir()
	dest := filepath.Join(dir, fmt.Sprintf("%s%s%s", autoBackupFilePrefix, time.Now().Format("2006-01-02_150405"), autoBackupFileSuffix))
	if err := s.Store.BackupToFile(dest); err != nil {
		log.Printf("[autobackup] Backup fehlgeschlagen: %v", err)
		_ = s.Store.LogActivity(0, "", "job", "backup_auto", "fehlgeschlagen: "+err.Error())
		return
	}
	removed := pruneAutoBackups(dir, retention)
	info, _ := os.Stat(dest)
	var size int64
	if info != nil {
		size = info.Size()
	}
	log.Printf("[autobackup] Backup erstellt: %s (%d Bytes), %d alte entfernt", filepath.Base(dest), size, removed)
	_ = s.Store.LogActivity(0, "", "job", "backup_auto",
		fmt.Sprintf("%s erstellt (%s)%s", filepath.Base(dest), fmtBytes(size), removedSuffix(removed)))
}

func removedSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(", %d alte gelöscht (Retention)", n)
}

func fmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// pruneAutoBackups löscht die ältesten auto-*.db-Dateien, bis höchstens
// `keep` übrig bleiben. Der Zeitstempel im Dateinamen (YYYY-MM-DD_HHMMSS)
// sortiert lexikografisch korrekt chronologisch — kein separates Stat nötig.
func pruneAutoBackups(dir string, keep int) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, autoBackupFilePrefix) && strings.HasSuffix(n, autoBackupFileSuffix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	removed := 0
	for len(names) > keep {
		victim := names[0]
		names = names[1:]
		if err := os.Remove(filepath.Join(dir, victim)); err == nil {
			removed++
		}
	}
	return removed
}

// isValidAutoBackupFilename schützt Download/Delete gegen Directory-
// Traversal — nur exakt das erwartete Namensmuster wird akzeptiert.
func isValidAutoBackupFilename(name string) bool {
	if name == "" || name != filepath.Base(name) {
		return false
	}
	return strings.HasPrefix(name, autoBackupFilePrefix) && strings.HasSuffix(name, autoBackupFileSuffix)
}
