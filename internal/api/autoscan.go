package api

// AutoScanConfig beschreibt, wann und welche Libraries automatisch gescannt werden.
//
// Format des schedule-Strings (in settings.auto_scan_schedule):
//
//	"daily:HH:MM"           — täglich um HH:MM Uhr (Serverzeit)
//	"every:Nh"              — alle N Stunden (N ∈ 1..23)
//	"weekly:DOW:HH:MM"      — wöchentlich; DOW = mon|tue|wed|thu|fri|sat|sun
//
// LibraryID = 0 bedeutet „alle Libraries".
// Enabled = false → Goroutine schläft, führt keinen Scan aus.
//
// Die Goroutine läuft dauerhaft im Hintergrund und überprüft jede Minute,
// ob der nächste Scan fällig ist. Das ist robuster als ein time.Timer, weil
// Sommer-/Winterzeit und Suspend/Resume damit transparent behandelt werden.

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// RunAutoScan startet die Auto-Scan-Hintergrundschleife. Blockiert bis ctx done ist.
func (s *Server) RunAutoScan(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	var lastFired time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if !s.autoScanDue(now, lastFired) {
				continue
			}
			lastFired = now.Truncate(time.Minute)
			log.Printf("[autoscan] Scan fällig um %s", now.Format("2006-01-02 15:04"))
			go s.runAutoScanNow()
		}
	}
}

// autoScanDue prüft ob jetzt (now) ein Auto-Scan fällig ist.
func (s *Server) autoScanDue(now, lastFired time.Time) bool {
	enabled, _ := s.Store.GetSetting("auto_scan_enabled", "false")
	if enabled != "true" {
		return false
	}
	schedule, _ := s.Store.GetSetting("auto_scan_schedule", "")
	if schedule == "" {
		return false
	}
	// Nur einmal pro Minute feuern, nicht mehrfach innerhalb derselben Minute.
	thisMinute := now.Truncate(time.Minute)
	if thisMinute.Equal(lastFired) {
		return false
	}
	return matchSchedule(schedule, now)
}

// matchSchedule gibt true zurück, wenn `now` mit dem Zeitplan übereinstimmt.
func matchSchedule(schedule string, now time.Time) bool {
	parts := strings.Split(schedule, ":")
	if len(parts) < 2 {
		return false
	}
	switch parts[0] {
	case "daily":
		// "daily:HH:MM"
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
		// "every:Nh" — alle N Stunden. Feuert wenn Minute=0 und Stunde % N == 0.
		if len(parts) < 2 {
			return false
		}
		raw := strings.TrimSuffix(parts[1], "h")
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 23 {
			return false
		}
		return now.Minute() == 0 && now.Hour()%n == 0

	case "weekly":
		// "weekly:DOW:HH:MM"
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

// runAutoScanNow führt den konfigurierten Auto-Scan aus.
func (s *Server) runAutoScanNow() {
	libIDStr, _ := s.Store.GetSetting("auto_scan_library_id", "0")
	libID, _ := strconv.ParseInt(libIDStr, 10, 64)

	if libID == 0 {
		// Alle Libraries
		libs, err := s.Store.ListLibraries()
		if err != nil {
			log.Printf("[autoscan] ListLibraries: %v", err)
			return
		}
		for _, lib := range libs {
			runLibScan(s, lib)
		}
	} else {
		lib, err := s.Store.GetLibrary(libID)
		if err != nil || lib == nil {
			log.Printf("[autoscan] Library %d nicht gefunden: %v", libID, err)
			return
		}
		runLibScan(s, *lib)
	}
}

func runLibScan(s *Server, lib model.Library) {
	log.Printf("[autoscan] Starte Scan für Library %d (%s)", lib.ID, lib.Name)
	if err := s.Scanner.Start(lib, false, ""); err != nil {
		log.Printf("[autoscan] Scan für Library %d: %v", lib.ID, err)
		return
	}
	// Warten bis fertig (maximal 6 Stunden)
	deadline := time.Now().Add(6 * time.Hour)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		if !s.Scanner.Status().Running {
			break
		}
	}
}

// autoScanSettingsFromStore liest die aktuellen Auto-Scan-Settings.
func autoScanSettingsFromStore(st *store.Store) autoScanDTO {
	enabled, _ := st.GetSetting("auto_scan_enabled", "false")
	schedule, _ := st.GetSetting("auto_scan_schedule", "daily:03:00")
	libIDStr, _ := st.GetSetting("auto_scan_library_id", "0")
	libID, _ := strconv.Atoi(libIDStr)
	return autoScanDTO{
		Enabled:   enabled == "true",
		Schedule:  schedule,
		LibraryID: libID,
	}
}

type autoScanDTO struct {
	Enabled   bool   `json:"enabled"`
	Schedule  string `json:"schedule"`
	LibraryID int    `json:"libraryId"` // 0 = alle
}
