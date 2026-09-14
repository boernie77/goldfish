package download

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Aufraeum-Fristen fuer den Download-Cache (User-Vorgabe 2026-09-14).
//
// Anlass: `/config/cache/downloads` war auf 87 GB in 23 Dateien gewachsen und
// hatte keinerlei Aufraeumung — eine einmal erzeugte Kompatibilitaets-Kopie lag
// dort fuer immer, auch wenn sie nie jemand abgeholt hat (die groesste, 46 GB,
// wurde nachweislich nie uebertragen).
//
// Warum Fristen statt "sofort nach dem Abholen loeschen", wie urspruenglich
// angedacht: der Server kann den Moment "vollstaendig abgeholt" nicht erkennen.
// Clients holen die Datei in vielen Range-Haeppchen und setzen nach einem
// Verbindungsabbruch genau dort wieder auf — ein Loeschen beim ersten
// ausgelieferten Byte wuerde jeden laufenden Transfer zerstoeren (bei 46 GB
// laeuft der ueber Stunden). Stattdessen zaehlt die Zeit seit dem LETZTEN
// Zugriff: jeder Byte-Request setzt die Uhr zurueck, ein haengender oder
// unterbrochener Download ist damit automatisch geschuetzt.
const (
	// Nie ausgelieferte Kopie: jemand hat eine Formatanpassung ausgeloest und
	// die fertige Datei nie abgeholt.
	unfetchedMaxAge = 3 * 24 * time.Hour
	// Bereits ausgelieferte Kopie: das Endgeraet hat die Datei lokal, die
	// Server-Kopie nuetzt nur noch bei einem erneuten Download.
	servedMaxAge = 24 * time.Hour
)

// servedTouchInterval begrenzt, wie oft ein laufender Download den Sidecar
// neu schreibt. Ohne das wuerde JEDER Range-Request eine JSON-Datei
// ueberschreiben — bei einem grossen Download sind das tausende Schreibzugriffe
// ohne jeden Nutzen, denn die Fristen oben rechnen in Stunden.
const servedTouchInterval = 5 * time.Minute

// MarkServed vermerkt im Sidecar, dass diese Cache-Kopie ausgeliefert wurde.
// Aus `downloadItem` bei jedem Request auf eine compat-Kopie aufgerufen; der
// Schreibzugriff selbst passiert hoechstens alle `servedTouchInterval`.
func MarkServed(outPath string) {
	metaPath := outPath + ".json"
	m, ok := readMeta(metaPath)
	if !ok {
		// Ohne Sidecar laesst sich die Kopie ohnehin nicht validieren; sie wird
		// beim naechsten Bedarf neu erzeugt. Hier nichts erfinden.
		return
	}
	now := time.Now().Unix()
	if m.ServedAt > 0 && now-m.ServedAt < int64(servedTouchInterval.Seconds()) {
		return
	}
	m.ServedAt = now
	writeMeta(metaPath, m)
}

// CleanupCache entfernt abgelaufene Kopien aus dem Download-Cache und liefert
// zurueck, wie viele Dateien geloescht und wie viele Bytes frei wurden.
//
// Bestandsdateien ohne `servedAt` gelten als NIE abgeholt (User-Entscheidung
// 2026-09-14) — fuer sie laesst sich nicht rekonstruieren, ob sie jemals
// uebertragen wurden, und eine geloeschte Kopie kostet nur Rechenzeit, keine
// Daten: das Original liegt unangetastet unter /media.
func CleanupCache(cacheDir string) (removed int, freed int64) {
	dir := filepath.Join(cacheDir, "downloads")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".mp4") {
			continue
		}
		// `<id>.mp4.tmp.<ns>.mp4` ist eine LAUFENDE Konvertierung und endet
		// ebenfalls auf .mp4 — niemals anfassen.
		if strings.Contains(name, ".tmp.") {
			continue
		}
		outPath := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		m, hasMeta := readMeta(outPath + ".json")
		var expired bool
		switch {
		case hasMeta && m.ServedAt > 0:
			expired = now.Sub(time.Unix(m.ServedAt, 0)) > servedMaxAge
		default:
			// Nie ausgeliefert (oder Sidecar fehlt): Alter der Datei zaehlt.
			expired = now.Sub(info.ModTime()) > unfetchedMaxAge
		}
		if !expired {
			continue
		}
		size := info.Size()
		if os.Remove(outPath) != nil {
			continue
		}
		_ = os.Remove(outPath + ".json")
		removed++
		freed += size
	}
	return removed, freed
}
