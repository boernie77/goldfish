// Package ocrsub erzeugt aus Bild-Untertitel-Streams (PGS/VOBSUB) per
// Tesseract-OCR verwertbare WebVTT-Textuntertitel. Läuft als Hintergrund-
// Worker, Opt-in pro Bibliothek/Ordner (ocr_sub_folders) — analog zur
// Intro-Erkennung (internal/introskip).
package ocrsub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/store"
)

// ErrNoPGS: das Item hat gar keinen PGS-Untertitel (nur VOBSUB/DVB o.ä.) —
// dann wird der Job gelöscht statt als „Fehler" zu bleiben.
var ErrNoPGS = errors.New("kein PGS-Untertitel")

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
//
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
	hasPGS := false
	var codecs []string
	for _, st := range streams {
		langSet[tesseractToIETF(iso639ToTesseract(st.Language))] = true
		codecs = append(codecs, st.Codec)
		switch strings.ToLower(st.Codec) {
		case "hdmv_pgs_subtitle", "pgssub", "pgs":
			hasPGS = true
		}
	}
	if !hasPGS {
		// pgsrip kann NUR PGS. VOBSUB (dvd_subtitle) / DVB bräuchte ein anderes
		// Werkzeug (vobsub2srt o.ä.) — noch nicht gebaut. Job wird gelöscht.
		return nil, fmt.Errorf("%w (Streams: %s)", ErrNoPGS, strings.Join(codecs, ", "))
	}

	ext := strings.ToLower(filepath.Ext(it.Path))
	if ext == ".mkv" || ext == ".mks" {
		return w.pgsripContainer(ctx, it.Path, ext, langSet, itemID)
	}
	return nil, fmt.Errorf("Bild-Untertitel-OCR aktuell nur für .mkv/.mks (Quelle: %s)", ext)
}


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

	// pgsrip filtert PGS-Spuren nach ihrem Sprach-TAG gegen die `--language`-
	// Liste. Bei „German.DL"-Rips ist die PGS-Spur oft als `eng` getaggt, nicht
	// `ger` → mit nur `-l de` kam „0 PGS subtitle collected" (2026-08-31).
	// `--all-languages` gibt es in dieser pgsrip-Version NICHT. Also: für JEDE
	// Sprache, für die wir ein Tesseract-Paket haben (de/en/it) + die aus den
	// Stream-Tags ein `-l` mitgeben — eine davon matcht die Spur.
	langs := map[string]bool{"de": true, "en": true, "it": true}
	for l := range langSet {
		langs[l] = true
	}
	args := []string{"--force"}
	for l := range langs {
		args = append(args, "--language", l)
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

// reCueLine: eine WebVTT/SRT-Timing-Zeile, tolerant (1–2-stellige Stunden,
// `,` oder `.` als Millisekunden-Trenner, beliebiger Text hinter dem Endstempel).
var reCueLine = regexp.MustCompile(`^(\d{1,2}:\d{2}:\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}:\d{2}:\d{2})[,.](\d{1,3})`)

// srtFileToVTT parst eine (pgsrip/Tesseract-)SRT robust in blockweise Cues und
// schreibt sauberes WebVTT. Verworfen werden: BOM, reine Index-Zeilen,
// `{\an8}`-/`<font>`-Steuertags, Blöcke ohne gültigen Zeitstempel. Fehlt jede
// Cue → Fehler (Job gilt dann als „failed", nicht „done").
func srtFileToVTT(srtPath, outVTT string) error {
	raw, err := os.ReadFile(srtPath)
	if err != nil {
		return err
	}
	text := strings.TrimPrefix(string(raw), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	cues := 0

	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		var timing string
		var payload []string
		for _, ln := range lines {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			if timing == "" {
				if m := reCueLine.FindStringSubmatch(ln); m != nil {
					timing = fmt.Sprintf("%s.%s --> %s.%s",
						m[1], pad3(m[2]), m[3], pad3(m[4]))
					continue
				}
				// reine Index-Zeile (nur Ziffern) vor dem Timing → überspringen
				if isDigits(ln) {
					continue
				}
				// alles andere vor dem Timing ignorieren
				continue
			}
			payload = append(payload, cleanCueText(ln))
		}
		if timing == "" || len(payload) == 0 {
			continue
		}
		b.WriteString(timing)
		b.WriteByte('\n')
		b.WriteString(strings.Join(payload, "\n"))
		b.WriteString("\n\n")
		cues++
	}
	if cues == 0 {
		return fmt.Errorf("keine verwertbaren Untertitel-Zeilen in der OCR-Ausgabe")
	}
	return os.WriteFile(outVTT, []byte(b.String()), 0o644)
}

func pad3(s string) string {
	for len(s) < 3 {
		s += "0"
	}
	return s[:3]
}
func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

var reFontTag = regexp.MustCompile(`</?font[^>]*>`)
var reAssTag = regexp.MustCompile(`\{\\[^}]*\}`)

func cleanCueText(s string) string {
	s = reFontTag.ReplaceAllString(s, "")
	s = reAssTag.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
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
