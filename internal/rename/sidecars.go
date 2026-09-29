package rename

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Begleitdateien beim Umbenennen/Verschieben mitnehmen (seit 2026-09-29).
//
// Anlass: Beim Umbenennen der Kommissar-Dupin-Filme blieben die von Goldfish
// selbst geschriebenen NFOs unter dem alten Namen liegen — verwaist, weil
// Kodi/Jellyfin/Plex eine NFO nur über den identischen Dateistamm der
// Videodatei finden. Dasselbe gilt für Untertitel (Goldfish liest Sidecar-
// Untertitel ebenfalls über den Stamm, siehe api/subtitles_sidecar.go) und
// Kodi-Bilder wie `<stamm>-poster.jpg`.

// sidecarExts: Dateiendungen, die als Begleitdatei gelten, wenn der Name mit
// `<stamm>.` beginnt (NFO, Untertitel inkl. VobSub-Paar .sub/.idx).
var sidecarExts = map[string]bool{
	".nfo": true,
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true,
	".sub": true, ".idx": true, ".sup": true,
}

// sidecarImageSuffixes: Kodi-Bildnamen `<stamm><suffix>`.
var sidecarImageSuffixes = []string{
	"-poster.jpg", "-poster.png", "-fanart.jpg", "-fanart.png",
	"-thumb.jpg", "-thumb.png", "-landscape.jpg", "-clearlogo.png",
}

// videoExts: Endungen, deren Dateien selbst Videos sind — ihr Stamm konkurriert
// um Begleitdateien (siehe ownerStem).
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true, ".wmv": true,
	".m4v": true, ".ts": true, ".m2ts": true, ".webm": true,
}

// sidecarRest liefert den Teil des Dateinamens hinter dem Stamm, wenn `name`
// eine Begleitdatei zu `stem` ist (z. B. ".de.srt", ".nfo", "-poster.jpg"),
// sonst "". Vergleich case-insensitiv, der Rest behält seine Schreibweise.
func sidecarRest(name, stem string) string {
	if len(name) <= len(stem) || !strings.EqualFold(name[:len(stem)], stem) {
		return ""
	}
	rest := name[len(stem):]
	lower := strings.ToLower(rest)
	for _, suf := range sidecarImageSuffixes {
		if lower == suf {
			return rest
		}
	}
	if !strings.HasPrefix(rest, ".") || !sidecarExts[strings.ToLower(filepath.Ext(rest))] {
		return ""
	}
	return rest
}

// SidecarMove ist eine geplante Umbenennung einer Begleitdatei.
type SidecarMove struct {
	From, To string
}

// PlanSidecars sucht die Begleitdateien des Videos `videoPath` und berechnet
// ihre neuen Pfade für das Ziel `newVideoPath`. Liegen mehrere Videos im
// Ordner, gehört eine Datei dem Video mit dem LÄNGSTEN passenden Stamm:
// `Film.2.de.srt` bleibt bei `Film.2.mkv`, auch wenn `Film.mkv` umbenannt wird.
func PlanSidecars(videoPath, newVideoPath string) []SidecarMove {
	dir := filepath.Dir(videoPath)
	name := filepath.Base(videoPath)
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	newStem := strings.TrimSuffix(filepath.Base(newVideoPath), filepath.Ext(newVideoPath))
	if stem == "" || newStem == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var videoStems []string
	for _, e := range entries {
		if !e.IsDir() && videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			videoStems = append(videoStems, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	var out []SidecarMove
	for _, e := range entries {
		if e.IsDir() || e.Name() == name {
			continue
		}
		rest := sidecarRest(e.Name(), stem)
		if rest == "" {
			continue
		}
		owner := stem
		for _, vs := range videoStems {
			if len(vs) > len(owner) && sidecarRest(e.Name(), vs) != "" {
				owner = vs
			}
		}
		if owner != stem {
			continue
		}
		out = append(out, SidecarMove{
			From: filepath.Join(dir, e.Name()),
			To:   filepath.Join(filepath.Dir(newVideoPath), newStem+rest),
		})
	}
	return out
}

// MoveSidecars zieht die Begleitdateien eines bereits umbenannten/verschobenen
// Videos nach. Aufruf NACH dem erfolgreichen RenameOnDisk der Videodatei, mit
// dem Plan, der VORHER per PlanSidecars erstellt wurde (danach ist der alte
// Name nicht mehr im Ordner, der längste-Stamm-Vergleich wäre verfälscht).
// Best-effort: eine fehlgeschlagene Begleitdatei bricht nichts ab, das Video
// ist bereits am Ziel. Existiert das Ziel schon, bleibt die Datei liegen.
func MoveSidecars(plan []SidecarMove, allowCrossDevice bool) []error {
	var errs []error
	for _, m := range plan {
		if err := RenameOnDisk(m.From, m.To, allowCrossDevice); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(m.From), err))
		}
	}
	return errs
}
