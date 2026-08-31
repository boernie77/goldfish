// Package ocrsub erzeugt aus Bild-Untertitel-Streams (PGS/VOBSUB) per
// Tesseract-OCR verwertbare WebVTT-Textuntertitel. Läuft als Hintergrund-
// Worker, Opt-in pro Bibliothek/Ordner (ocr_sub_folders) — analog zur
// Intro-Erkennung (internal/introskip).
package ocrsub

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/store"
)

// iso639ToTesseract mappt gängige ffprobe-Sprachtags (ISO 639-2/B) auf
// Tesseract-Sprachpakete. Unbekannt → "eng".
func iso639ToTesseract(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "ger", "deu", "de":
		return "deu"
	case "eng", "en":
		return "eng"
	case "ita", "it":
		return "ita"
	case "fra", "fre", "fr":
		return "fra"
	case "spa", "es":
		return "spa"
	case "nld", "dut", "nl":
		return "nld"
	case "por", "pt":
		return "por"
	default:
		return "eng"
	}
}

// tesseractToIETF: für den Dateinamen der VTT + das Player-Dropdown.
func tesseractToIETF(t string) string {
	switch t {
	case "deu":
		return "de"
	case "eng":
		return "en"
	case "ita":
		return "it"
	case "fra":
		return "fr"
	case "spa":
		return "es"
	case "nld":
		return "nl"
	case "por":
		return "pt"
	default:
		return "en"
	}
}

// VTTPath: /config/generated-subs/{itemID}/{ietf}-ocr.vtt — bewusst getrennt
// von whisper.VTTPath ({ietf}.vtt), damit OCR- und KI-Untertitel koexistieren.
func VTTPath(configDir string, itemID int64, ietf string) string {
	return filepath.Join(configDir, "generated-subs", fmt.Sprintf("%d", itemID), ietf+"-ocr.vtt")
}

// processItem OCR-t alle Bild-Untertitel-Streams eines Items. Rückgabe: die
// Liste der erzeugten IETF-Sprachcodes.
func (w *Worker) processItem(ctx context.Context, itemID int64) ([]string, error) {
	it, err := w.store.GetItem(itemID)
	if err != nil {
		return nil, err
	}
	if it == nil {
		return nil, fmt.Errorf("item %d nicht gefunden", itemID)
	}
	streams, err := w.store.ItemBitmapSubStreams(itemID)
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 {
		return nil, fmt.Errorf("keine Bild-Untertitel-Streams")
	}

	outDir := filepath.Join(w.configDir, "generated-subs", fmt.Sprintf("%d", itemID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	var produced []string
	seen := map[string]bool{}
	var lastErr error
	for _, st := range streams {
		if err := w.waitWhilePaused(ctx); err != nil {
			return produced, err
		}
		tess := iso639ToTesseract(st.Language)
		ietf := tesseractToIETF(tess)
		if seen[ietf] {
			continue // pro Sprache nur den ersten Stream
		}
		if err := w.ocrOneStream(ctx, it.Path, st.Index, tess, VTTPath(w.configDir, itemID, ietf)); err != nil {
			lastErr = fmt.Errorf("stream %d (%s): %w", st.Index, ietf, err)
			continue
		}
		seen[ietf] = true
		produced = append(produced, ietf)
	}
	if len(produced) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("kein Untertitel erzeugt")
	}
	return produced, nil
}

var reSrtTime = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// ocrOneStream: ffmpeg extrahiert den PGS-Stream als .sup, pgsrip macht daraus
// per Tesseract eine .srt, die wir nach WebVTT konvertieren.
func (w *Worker) ocrOneStream(ctx context.Context, sourcePath string, streamIndex int, tessLang, outVTT string) error {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("goldfish-ocr-%d-%d.sup", time.Now().UnixNano(), streamIndex))
	defer func() {
		_ = os.Remove(tmp)
		_ = os.Remove(strings.TrimSuffix(tmp, ".sup") + "." + tessLang + ".srt")
	}()

	// 1. Bild-Untertitel-Stream herausziehen (Stream-Copy, Sekunden).
	ex := exec.CommandContext(ctx, w.ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", sourcePath, "-map", fmt.Sprintf("0:%d", streamIndex),
		"-c:s", "copy", tmp,
	)
	if out, err := ex.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg-Extraktion: %v — %s", err, tail(string(out), 500))
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Size() == 0 {
		return fmt.Errorf("leere .sup-Datei (Stream ohne Inhalt?)")
	}

	// 2. pgsrip: PGS → SRT per Tesseract. Schreibt <tmp-ohne-ext>.<ietf>.srt.
	ietf := tesseractToIETF(tessLang)
	pg := exec.CommandContext(ctx, w.pgsrip, "--language", ietf, "--force", tmp)
	if out, err := pg.CombinedOutput(); err != nil {
		return fmt.Errorf("pgsrip/tesseract: %v — %s", err, tail(string(out), 800))
	}
	srtPath := strings.TrimSuffix(tmp, ".sup") + "." + ietf + ".srt"
	srt, err := os.ReadFile(srtPath)
	if err != nil {
		return fmt.Errorf("pgsrip lieferte keine .srt: %w", err)
	}

	// 3. SRT → WebVTT: Header + Komma→Punkt in den Zeitstempeln.
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	sc := bufio.NewScanner(strings.NewReader(string(srt)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "-->") {
			line = reSrtTime.ReplaceAllString(line, "$1.$2")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if strings.TrimSpace(b.String()) == "WEBVTT" {
		return fmt.Errorf("OCR-Ergebnis leer")
	}
	return os.WriteFile(outVTT, []byte(b.String()), 0o644)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// probeHasBitmapSubs ist ein billiger Vorabcheck (DB) ob ein Item überhaupt
// einen Bild-Untertitel hat — genutzt vom Auto-Enqueue nach Scans.
func HasBitmapSubs(s *store.Store, itemID int64) bool {
	streams, err := s.ItemBitmapSubStreams(itemID)
	return err == nil && len(streams) > 0
}

var _ = context.Background
