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

// processItem OCR-t alle Bild-Untertitel eines Items.
//
// Vorgehen (robust, nach den ersten Fehlversuchen 2026-08-31):
//   - `ffmpeg -c:s copy … .sup` scheiterte reihenweise mit „[sup] Not enough
//     data … Invalid data" — ffmpegs SUP-Muxer verträgt die PGS-Display-Sets
//     an Stream-Grenzen nicht.
//   - `pgsrip` bekommt daher die MKV DIREKT (über einen Symlink in /tmp, damit
//     die erzeugte .srt nicht in /media landet). pgsrip nutzt intern
//     `mkvextract` (sauber) und OCR-t alle passenden PGS-Spuren in einem Lauf.
//   - Der Ausgabe-Dateiname von pgsrip ist versionsabhängig → wir GLOBBEN
//     `<link-basename>.*.srt` statt einen Namen zu raten und normalisieren den
//     Sprachcode aus dem Dateinamen auf unser IETF-Set.
// Nicht-MKV-Quellen (m2ts/ts/mp4): Fallback über ffmpeg-`-f sup`-Extraktion.
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
	if err := os.MkdirAll(filepath.Join(w.configDir, "generated-subs", fmt.Sprintf("%d", itemID)), 0o755); err != nil {
		return nil, err
	}

	// Zielsprachen (IETF) aus den Stream-Tags. Ohne Tag → "en".
	langSet := map[string]bool{}
	for _, st := range streams {
		langSet[tesseractToIETF(iso639ToTesseract(st.Language))] = true
	}

	ext := strings.ToLower(filepath.Ext(it.Path))
	if ext == ".mkv" || ext == ".mks" {
		return w.pgsripContainer(ctx, it.Path, ext, langSet, itemID)
	}
	return nil, fmt.Errorf("Bild-Untertitel-OCR aktuell nur für .mkv/.mks (Quelle: %s)", ext)
}

var reSrtTime = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// normalizeLang: pgsrips Ausgabe-Sprachcode (de/deu/ger/…) → unser IETF-Set.
func normalizeLang(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) == 2 {
		return raw
	}
	return tesseractToIETF(iso639ToTesseract(raw))
}

// pgsripContainer: MKV/MKS direkt an pgsrip (via /tmp-Symlink), dann alle
// erzeugten .srt einsammeln.
func (w *Worker) pgsripContainer(ctx context.Context, sourcePath, ext string, langSet map[string]bool, itemID int64) ([]string, error) {
	base := filepath.Join(os.TempDir(), fmt.Sprintf("goldfish-ocr-%d", time.Now().UnixNano()))
	link := base + ext
	if err := os.Symlink(sourcePath, link); err != nil {
		return nil, fmt.Errorf("symlink: %w", err)
	}
	defer func() {
		_ = os.Remove(link)
		if m, _ := filepath.Glob(base + ".*.srt"); m != nil {
			for _, f := range m {
				_ = os.Remove(f)
			}
		}
		_ = os.Remove(base + ".srt")
	}()

	args := []string{"--force"}
	if len(langSet) == 0 {
		args = append(args, "--all-languages")
	} else {
		for l := range langSet {
			args = append(args, "--language", l)
		}
	}
	args = append(args, link)
	pg := exec.CommandContext(ctx, w.pgsrip, args...)
	out, runErr := pg.CombinedOutput()

	// pgsrip meldet exit!=0 auch, wenn nur EINE Sprache scheitert — daher erst
	// die erzeugten .srt suchen, dann urteilen. pgsrip schreibt normalerweise
	// neben den (Symlink-)Pfad; manche Versionen lösen den realpath auf und
	// legen sie neben die Quelldatei in /media — beides abklappern + aufräumen.
	srcBase := strings.TrimSuffix(sourcePath, ext)
	collect := func() []string {
		var s []string
		for _, pat := range []string{base + ".*.srt", base + ".srt", srcBase + ".*.srt", srcBase + ".srt"} {
			m, _ := filepath.Glob(pat)
			s = append(s, m...)
		}
		return s
	}
	srts := collect()
	// Alle gefundenen .srt am Ende wegräumen (auch die evtl. in /media).
	defer func() {
		for _, f := range collect() {
			_ = os.Remove(f)
		}
	}()
	if len(srts) == 0 {
		return nil, fmt.Errorf("pgsrip erzeugte keine .srt (%v) — %s", runErr, tail(string(out), 900))
	}

	// Fallback-Sprache, falls pgsrip die .srt ohne Sprach-Infix schreibt.
	fallbackLang := "en"
	for l := range langSet {
		fallbackLang = l
		break
	}

	var produced []string
	seen := map[string]bool{}
	for _, srt := range srts {
		stem := strings.TrimSuffix(filepath.Base(srt), ".srt")
		mid := ""
		if i := strings.LastIndex(stem, "."); i >= 0 {
			mid = stem[i+1:]
		}
		lang := ""
		if l := len(mid); (l == 2 || l == 3) && isAlpha(mid) {
			lang = normalizeLang(mid)
		}
		if lang == "" {
			lang = fallbackLang
		}
		if seen[lang] {
			continue
		}
		if err := srtFileToVTT(srt, VTTPath(w.configDir, itemID, lang)); err != nil {
			continue
		}
		seen[lang] = true
		produced = append(produced, lang)
	}
	if len(produced) == 0 {
		return nil, fmt.Errorf("keine verwertbare .srt (%v)", runErr)
	}
	return produced, nil
}

// srtFileToVTT liest eine SRT und schreibt WebVTT (Header + Komma→Punkt).
func srtFileToVTT(srtPath, outVTT string) error {
	srt, err := os.ReadFile(srtPath)
	if err != nil {
		return err
	}
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
		return fmt.Errorf("leer")
	}
	return os.WriteFile(outVTT, []byte(b.String()), 0o644)
}

func isAlpha(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
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

