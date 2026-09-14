package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/boernie77/goldfish/internal/download"
)

// downloadCleanupInterval — wie oft der Download-Cache durchgesehen wird. Die
// Fristen selbst liegen bei Stunden bzw. Tagen (internal/download/cleanup.go),
// stuendlich ist also reichlich genau und kostet praktisch nichts (ein ReadDir
// ueber ein paar Dutzend Eintraege).
const downloadCleanupInterval = time.Hour

// RunDownloadCacheCleanup raeumt abgelaufene Kompatibilitaets-Kopien weg.
// Anlass 2026-09-14: `/config/cache/downloads` hatte keinerlei Aufraeumung und
// war auf 87 GB gewachsen, darunter eine 46-GB-Kopie, die nie jemand abgeholt
// hat. Laeuft einmal beim Start (der Cache kann seit dem letzten Lauf beliebig
// alt geworden sein) und danach stuendlich.
func (s *Server) RunDownloadCacheCleanup(ctx context.Context) {
	s.runDownloadCleanupOnce()

	ticker := time.NewTicker(downloadCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDownloadCleanupOnce()
		}
	}
}

func (s *Server) runDownloadCleanupOnce() {
	removed, freed := download.CleanupCache(s.ConfigDir + "/cache")
	if removed == 0 {
		return
	}
	gb := float64(freed) / (1 << 30)
	log.Printf("[download] %d abgelaufene Cache-Kopien entfernt, %.1f GB frei", removed, gb)
	// EIN Eintrag pro Lauf, nicht pro Datei — gleiche Konvention wie Scan/OCR.
	// Leerer Benutzer = System-Event (siehe LogActivity-Konvention).
	_ = s.Store.LogActivity(0, "", "job", "download_cache_cleanup",
		fmt.Sprintf("%d Kopien entfernt, %.1f GB frei", removed, gb), "")
}
