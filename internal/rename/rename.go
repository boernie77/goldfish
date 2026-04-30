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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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
func RenameOnDisk(oldAbsPath, newAbsPath string) error {
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
	if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
		return fmt.Errorf("os.Rename fehlgeschlagen: %w", err)
	}
	return nil
}
