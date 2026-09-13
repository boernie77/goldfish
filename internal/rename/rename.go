// Package rename implements das Umbenennen von Film-Dateien anhand der
// bestaetigten TMDB-Metadaten zum Schema „<Title> (<Year>).<ext>".
//
// Wer das aufruft:
//   - api/items.go: confirmItemMetadata-Handler (wenn Setting an + tmdb_type=movie)
//   - api/admin_rename.go: manueller Einzel-Rename + Bulk-Rename + Undo
//
// Sicherheitsdesign:
//   - Sanitize: Zeichen, die auf gaengigen Filesystems unsichtbar/illegal
//     sind (`<>:"/\|?*` + Steuerzeichen) werden entfernt; trailing dots+spaces
//     getrimmt (Windows-Inkompatibilitaet).
//   - Konflikt: existiert die Zieldatei schon, wird ` (2)`, ` (3)` etc.
//     angehaengt bis 99 Versuche; danach Fehler.
//   - Side-Effect-Reihenfolge: zuerst os.Rename, dann DB-Update. Schlaegt
//     der DB-Update fehl, ist die Datei umbenannt und der DB-Pfad veraltet
//     — aber Foerderlich, weil der Rename-Helper das History-Insert UND
//     den items.path-Update in einer DB-Transaktion macht, sodass beim
//     Crash zwischen den beiden ein Rollback der DB-Aenderung folgt.
//     Auf der Disk bleibt dann der neue Name; beim naechsten Scan wird
//     der DB-Eintrag aktualisiert.
package rename

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// ErrCrossDevice signalisiert, dass Quelle und Ziel auf unterschiedlichen
// Datentraegern liegen (os.Rename kann das nicht atomar, nur echtes
// Kopieren+Loeschen) — UND der Aufrufer das (noch) nicht per
// allowCrossDevice=true bestaetigt hat. api/admin_rename.go faengt das ab
// und liefert dem Client ein eigenes Signal, statt den Vorgang mit einer
// generischen Fehlermeldung scheitern zu lassen (User-Wunsch 2026-09-13:
// Zwischenfenster VOR jedem geraeteuebergreifenden Verschieben).
var ErrCrossDevice = errors.New("Quelle und Ziel liegen auf unterschiedlichen Datentraegern")

// unsafeChars matcht Zeichen, die auf NTFS/exFAT/HFS+/APFS bzw. von
// Backup-Tools nicht erlaubt sind oder zu Verwirrung fuehren. Steuerzeichen
// 0x00-0x1f auch raus (manche Tools brechen darauf ab).
var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// SanitizeFilename macht aus einem Titel einen filesystem-sicheren Datei-Stamm
// (ohne Extension). Entfernt unsafe-Zeichen, trimmt Whitespace + trailing
// Punkte/Leerzeichen (Windows-FS reserviert `foo.` und `foo `).
func SanitizeFilename(s string) string {
	s = unsafeChars.ReplaceAllString(s, "")
	// Mehrfache Leerzeichen zu einem zusammenfassen, damit sanitisierte
	// Stellen keine doppelten Spaces hinterlassen.
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ". ")
	return s
}

// TargetFilename baut den Ziel-Dateinamen „<Title> (<Year>).<ext>".
// Ist year=0, wird ohne Klammer-Suffix gebaut. Liefert leeren String,
// wenn Title nach Sanitize leer ist (Caller sollte dann skippen).
// `ext` muss inkl. fuehrendem Punkt sein (z.B. ".mkv").
func TargetFilename(title string, year int, ext string) string {
	clean := SanitizeFilename(title)
	if clean == "" {
		return ""
	}
	if year > 0 {
		return fmt.Sprintf("%s (%d)%s", clean, year, ext)
	}
	return clean + ext
}

// ResolveConflict liefert einen freien absoluten Pfad fuer `base` im
// Verzeichnis `dir`. Wenn `dir/base` bereits existiert, werden Suffixe
// ` (2)`, ` (3)`, ... bis 99 probiert. Liefert leeren String wenn alle
// Kandidaten belegt sind (extrem unwahrscheinlich).
//
// Wenn `currentPath` gesetzt und identisch zum Kandidaten ist, gilt das
// NICHT als Konflikt — der Caller will moeglicherweise ein Item, das
// zufaellig schon den Wunschnamen hat, ueberspringen.
func ResolveConflict(dir, base, currentPath string) string {
	full := filepath.Join(dir, base)
	if full == currentPath {
		return full
	}
	if _, err := os.Stat(full); os.IsNotExist(err) {
		return full
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		full = filepath.Join(dir, candidate)
		if full == currentPath {
			return full
		}
		if _, err := os.Stat(full); os.IsNotExist(err) {
			return full
		}
	}
	return ""
}

// PreviewTarget berechnet den Ziel-Pfad fuer ein Rename, OHNE etwas zu tun.
// Gibt (newAbsPath, isAlreadyTarget, error) zurueck.
//   - isAlreadyTarget=true: die Datei traegt bereits exakt den Wunschnamen,
//     ein Rename ist nicht noetig.
//   - error nicht nil: kein gueltiger Zielname (z.B. Title leer, kein Konflikt
//     loesbar).
func PreviewTarget(currentAbsPath, title string, year int) (string, bool, error) {
	dir := filepath.Dir(currentAbsPath)
	ext := filepath.Ext(currentAbsPath)
	base := TargetFilename(title, year, ext)
	if base == "" {
		return "", false, fmt.Errorf("Titel ist nach Bereinigung leer")
	}
	candidate := ResolveConflict(dir, base, currentAbsPath)
	if candidate == "" {
		return "", false, fmt.Errorf("Konflikt: 99 Suffix-Varianten alle belegt")
	}
	return candidate, candidate == currentAbsPath, nil
}

// RenameOnDisk fuehrt das eigentliche os.Rename aus. Caller ist fuer
// DB-Update und History-Insert zustaendig. Liefert keinen Fehler, wenn
// Quelle == Ziel (dann no-op).
//
// allowCrossDevice steuert das Verhalten, wenn Quelle und Ziel auf
// unterschiedlichen Datentraegern liegen (os.Rename liefert dann EXDEV,
// z.B. Unraid-Array ↔ externe Unassigned-Devices-Platte, siehe
// project_todo/CLAUDE.md "Verschieben" — echte Beispiel-Anfrage 2026-09-13,
// bei der 126 Dateien wegen exakt dieser Grenze nicht wie erwartet vom
// Array auf eine externe Platte gewandert sind, sondern innerhalb der
// selben physischen Quelle nur umbenannt wurden):
//   - false (Default-Erwartung des Aufrufers ohne explizite Bestaetigung):
//     liefert ErrCrossDevice, OHNE irgendetwas anzufassen — der Aufrufer
//     zeigt dem User ein Zwischenfenster, bevor tatsaechlich kopiert wird.
//   - true (User hat im Zwischenfenster "Ja" gewaehlt): echtes
//     Kopieren+Loeschen als Fallback fuer os.Rename.
func RenameOnDisk(oldAbsPath, newAbsPath string, allowCrossDevice bool) error {
	if oldAbsPath == newAbsPath {
		return nil
	}
	// Sicherheitscheck: Quelle existiert, Ziel existiert NICHT.
	if _, err := os.Stat(oldAbsPath); err != nil {
		return fmt.Errorf("Quelldatei nicht lesbar: %w", err)
	}
	if _, err := os.Stat(newAbsPath); err == nil {
		return fmt.Errorf("Zieldatei existiert bereits: %s", newAbsPath)
	}
	err := os.Rename(oldAbsPath, newAbsPath)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("os.Rename fehlgeschlagen: %w", err)
	}
	if !allowCrossDevice {
		return ErrCrossDevice
	}
	return copyAndRemove(oldAbsPath, newAbsPath)
}

// IsCrossDevice sagt vorab (ohne etwas zu veraendern), ob ein Rename von
// `oldAbsPath` nach einer Datei im Verzeichnis `newDir` auf einen anderen
// Datentraeger fallen wuerde — fuer den Preflight-Check
// (POST /api/items/move-preview), damit der Client das Zwischenfenster
// zeigen kann, BEVOR ueberhaupt versucht wird zu verschieben.
// `newDir` muss nicht existieren (wird von executeMove erst per
// os.MkdirAll angelegt) — verglichen wird gegen den naechsten
// existierenden Vorfahren.
func IsCrossDevice(oldAbsPath, newDir string) (bool, error) {
	oldDev, err := deviceOf(filepath.Dir(oldAbsPath))
	if err != nil {
		return false, err
	}
	dir := newDir
	for {
		if dev, err := deviceOf(dir); err == nil {
			return dev != oldDev, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, fmt.Errorf("kein existierendes Vorfahren-Verzeichnis fuer %q gefunden", newDir)
		}
		dir = parent
	}
}

func deviceOf(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("kein syscall.Stat_t fuer %q verfuegbar", path)
	}
	return uint64(stat.Dev), nil
}

// copyAndRemove ist der Fallback fuer os.Rename ueber Datentraeger-Grenzen
// hinweg (EXDEV) — echtes Kopieren, danach die Quelle loeschen. Eine
// unvollstaendige Zieldatei wird bei einem Fehler aufgeraeumt, damit kein
// halb geschriebener Rest liegen bleibt.
func copyAndRemove(oldAbsPath, newAbsPath string) (err error) {
	src, err := os.Open(oldAbsPath)
	if err != nil {
		return fmt.Errorf("Quelldatei konnte nicht geoeffnet werden: %w", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("Quelldatei-Info nicht lesbar: %w", err)
	}

	dst, err := os.OpenFile(newAbsPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("Zieldatei konnte nicht angelegt werden: %w", err)
	}
	// Aufraeumen bei jedem Fehlerpfad ab hier: `dst` schliessen (Close auf
	// einem bereits geschlossenen *os.File ist harmlos, liefert nur einen
	// ignorierten Fehler) und die unvollstaendige Zieldatei entfernen.
	defer func() {
		if err != nil {
			dst.Close()
			_ = os.Remove(newAbsPath)
		}
	}()

	if _, err = io.Copy(dst, src); err != nil {
		return fmt.Errorf("Kopieren fehlgeschlagen: %w", err)
	}
	if err = dst.Sync(); err != nil {
		return fmt.Errorf("Kopieren (Sync) fehlgeschlagen: %w", err)
	}
	if err = dst.Close(); err != nil {
		return fmt.Errorf("Zieldatei konnte nicht geschlossen werden: %w", err)
	}
	if err = os.Remove(oldAbsPath); err != nil {
		return fmt.Errorf("Quelldatei konnte nach dem Kopieren nicht geloescht werden: %w", err)
	}
	return nil
}
