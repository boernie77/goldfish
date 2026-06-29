package api

// Auto-Scan: mehrere unabhängige Scan-Aufgaben, jede mit eigenem Zeitplan,
// eigener Library und Wahl zwischen inkrementellem und vollständigem Scan.
//
// Gespeichert als JSON-Array in settings.auto_scan_tasks.
//
// Zeitplan-Format (schedule-String):
//
//	"daily:HH:MM"           — täglich um HH:MM Uhr (Serverzeit)
//	"every:Nh"              — alle N Stunden (N ∈ 1..23), startet zu vollen Stunden
//	"weekly:DOW:HH:MM"      — wöchentlich; DOW = mon|tue|wed|thu|fri|sat|sun
//
// LibraryID = 0 → alle Libraries. Force = true → vollständiger Scan (kein mtime-Vergleich).

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

const autoScanTasksKey = "auto_scan_tasks"

// AutoScanTask beschreibt eine einzelne Auto-Scan-Aufgabe.
type AutoScanTask struct {
	ID        int    `json:"id"`
	Enabled   bool   `json:"enabled"`
	Schedule  string `json:"schedule"`
	LibraryID int    `json:"libraryId"` // 0 = alle
	Force     bool   `json:"force"`      // true = vollständig, false = inkrementell
}

// loadAutoScanTasks liest alle Aufgaben aus der DB (leeres Slice wenn noch keine).
func loadAutoScanTasks(st *store.Store) []AutoScanTask {
	raw, _ := st.GetSetting(autoScanTasksKey, "")
	if raw == "" {
		// Legacy-Migration: alte Einzel-Einstellungen übernehmen wenn vorhanden.
		enabled, _ := st.GetSetting("auto_scan_enabled", "")
		schedule, _ := st.GetSetting("auto_scan_schedule", "")
		libIDStr, _ := st.GetSetting("auto_scan_library_id", "0")
		libID, _ := strconv.Atoi(libIDStr)
		if enabled == "true" && schedule != "" {
			return []AutoScanTask{{ID: 1, Enabled: true, Schedule: schedule, LibraryID: libID, Force: false}}
		}
		return nil
	}
	var tasks []AutoScanTask
	_ = json.Unmarshal([]byte(raw), &tasks)
	return tasks
}

// saveAutoScanTasks speichert die Aufgabenliste als JSON in der DB.
func saveAutoScanTasks(st *store.Store, tasks []AutoScanTask) error {
	b, err := json.Marshal(tasks)
	if err != nil {
		return err
	}
	return st.SetSetting(autoScanTasksKey, string(b))
}

// RunAutoScan startet die Auto-Scan-Hintergrundschleife. Prüft jede Minute
// alle aktiven Aufgaben und führt fällige Scans aus. Blockiert bis ctx done.
func (s *Server) RunAutoScan(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Pro Aufgaben-ID merken, wann sie zuletzt ausgelöst wurde.
	lastFired := make(map[int]time.Time)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			tasks := loadAutoScanTasks(s.Store)
			thisMinute := now.Truncate(time.Minute)
			for _, task := range tasks {
				if !task.Enabled || task.Schedule == "" {
					continue
				}
				if thisMinute.Equal(lastFired[task.ID]) {
					continue // in dieser Minute bereits ausgelöst
				}
				if !matchSchedule(task.Schedule, now) {
					continue
				}
				lastFired[task.ID] = thisMinute
				t := task // Kopie für Goroutine
				log.Printf("[autoscan] Aufgabe %d fällig um %s (force=%v, libId=%d)",
					t.ID, now.Format("15:04"), t.Force, t.LibraryID)
				go s.runAutoScanTask(t)
			}
		}
	}
}

// matchSchedule gibt true zurück, wenn `now` mit dem Zeitplan übereinstimmt.
func matchSchedule(schedule string, now time.Time) bool {
	parts := strings.Split(schedule, ":")
	if len(parts) < 2 {
		return false
	}
	switch parts[0] {
	case "daily":
		if len(parts) < 3 {
			return false
		}
		h, errH := strconv.Atoi(parts[1])
		m, errM := strconv.Atoi(parts[2])
		if errH != nil || errM != nil {
			return false
		}
		return now.Hour() == h && now.Minute() == m

	case "every":
		raw := strings.TrimSuffix(parts[1], "h")
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 23 {
			return false
		}
		return now.Minute() == 0 && now.Hour()%n == 0

	case "weekly":
		if len(parts) < 4 {
			return false
		}
		dow := map[string]time.Weekday{
			"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
			"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
			"sun": time.Sunday,
		}
		want, ok := dow[strings.ToLower(parts[1])]
		if !ok {
			return false
		}
		h, errH := strconv.Atoi(parts[2])
		m, errM := strconv.Atoi(parts[3])
		if errH != nil || errM != nil {
			return false
		}
		return now.Weekday() == want && now.Hour() == h && now.Minute() == m
	}
	return false
}

// runAutoScanTask führt eine einzelne Aufgabe aus.
func (s *Server) runAutoScanTask(task AutoScanTask) {
	if task.LibraryID == 0 {
		libs, err := s.Store.ListLibraries()
		if err != nil {
			log.Printf("[autoscan] Aufgabe %d: ListLibraries: %v", task.ID, err)
			return
		}
		for _, lib := range libs {
			runLibScan(s, lib, task.Force)
		}
	} else {
		lib, err := s.Store.GetLibrary(int64(task.LibraryID))
		if err != nil || lib == nil {
			log.Printf("[autoscan] Aufgabe %d: Library %d nicht gefunden: %v", task.ID, task.LibraryID, err)
			return
		}
		runLibScan(s, *lib, task.Force)
	}
}

func runLibScan(s *Server, lib model.Library, force bool) {
	log.Printf("[autoscan] Starte Scan Library %d (%s) force=%v", lib.ID, lib.Name, force)
	if err := s.Scanner.Start(lib, force, ""); err != nil {
		log.Printf("[autoscan] Scan Library %d: %v", lib.ID, err)
		return
	}
	deadline := time.Now().Add(6 * time.Hour)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		if !s.Scanner.Status().Running {
			break
		}
	}
}
