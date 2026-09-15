// Sidecar-Untertitel: Untertitel-DATEIEN, die neben der Videodatei im
// Medienordner liegen (`Film.de.vtt`, `Film.en.srt`, …) — im Unterschied zu
// eingebetteten Spuren im Container, die ffprobe beim Scan erfasst und die in
// `item_streams` landen.
//
// Typischer Fall: yt-dlp lädt Untertitel ohne `--embed-subs` herunter und legt
// sie als `<Videoname>.<sprache>.vtt` daneben. Der Scanner indexiert nur
// Video-/Audio-Endungen (`supportedExt`), diese Dateien waren daher für
// Goldfish komplett unsichtbar.
//
// Bewusst zur ABFRAGEZEIT erkannt (wie die erzeugten KI-/OCR-Untertitel),
// nicht beim Scan: eine nachträglich hinzugefügte .vtt wirkt dadurch sofort,
// ohne Rescan. Kostenpunkt ist EIN os.ReadDir des Videoordners pro
// `playbackInfo`-Aufruf.
//
// ⚠ Die synthetischen Stream-Indizes (sidecarIndexBase + n) müssen zwischen
// dem `playbackInfo`-Aufruf und der späteren `/api/subtitle/{id}/{idx}.vtt`-
// Anfrage stabil bleiben — deshalb ist die Fundliste nach Dateipfad sortiert.
// Genau dadurch braucht KEIN Client eine Änderung: alle fallen für unbekannte
// Codecs ohnehin auf `/api/subtitle/{id}/{index}.vtt` zurück.
package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// sidecarIndexBase: Start der synthetischen Indizes für Sidecar-Untertitel.
// Muss oberhalb der bereits belegten Bereiche liegen (Whisper 2000+, OCR
// 2100+) und weit oberhalb echter ffprobe-Stream-Indizes.
const sidecarIndexBase = 2200

// sidecarSubExt: Endungen, die als Untertitel-Datei gelten. Alle vier wandelt
// ffmpeg verlässlich nach WebVTT (.vtt wird direkt durchgereicht).
// `.sub` (MicroDVD) fehlt absichtlich — das Format ist bildbasiert (mit .idx)
// oder frame-basiert und bräuchte die Bildrate, beides nichts für einen
// blinden Durchlauf.
var sidecarSubExt = map[string]bool{
	".vtt": true,
	".srt": true,
	".ass": true,
	".ssa": true,
}

// sidecarLangAliases bildet die in Dateinamen übliche Sprachschreibweise auf
// den 3-Buchstaben-Code ab, den ffprobe auch für eingebettete Spuren liefert.
// Damit greifen die Sprachnamen-Tabellen aller Clients unverändert.
var sidecarLangAliases = map[string]string{
	"de": "deu", "deu": "deu", "ger": "deu", "german": "deu", "deutsch": "deu",
	"en": "eng", "eng": "eng", "english": "eng", "englisch": "eng",
	"fr": "fra", "fra": "fra", "fre": "fra", "french": "fra",
	"es": "spa", "spa": "spa", "spanish": "spa",
	"it": "ita", "ita": "ita", "italian": "ita",
	"nl": "nld", "nld": "nld", "dut": "nld", "dutch": "nld",
	"pt": "por", "por": "por", "portuguese": "por",
	"pl": "pol", "pol": "pol", "polish": "pol",
	"ru": "rus", "rus": "rus", "russian": "rus",
	"ja": "jpn", "jpn": "jpn", "japanese": "jpn",
	"ko": "kor", "kor": "kor", "korean": "kor",
	"zh": "zho", "zho": "zho", "chi": "zho", "chinese": "zho",
	"tr": "tur", "tur": "tur", "turkish": "tur",
	"cs": "ces", "ces": "ces", "cze": "ces", "czech": "ces",
	"hu": "hun", "hun": "hun", "hungarian": "hun",
	"sv": "swe", "swe": "swe", "swedish": "swe",
	"da": "dan", "dan": "dan", "danish": "dan",
	"fi": "fin", "fin": "fin", "finnish": "fin",
	"no": "nor", "nor": "nor", "norwegian": "nor",
	"el": "ell", "ell": "ell", "gre": "ell", "greek": "ell",
	"he": "heb", "heb": "heb", "hebrew": "heb",
	"hi": "hin", "hin": "hin", "hindi": "hin",
	"uk": "ukr", "ukr": "ukr", "ukrainian": "ukr",
	"ro": "ron", "ron": "ron", "rum": "ron", "romanian": "ron",
	"ar": "ara", "ara": "ara", "arabic": "ara",
	"th": "tha", "tha": "tha", "thai": "tha",
	"vi": "vie", "vie": "vie", "vietnamese": "vie",
	"id": "ind", "ind": "ind", "indonesian": "ind",
}

// sidecarFlagTokens: Zusätze im Dateinamen, die KEINE Sprache benennen.
// `forced` wird als Flag ausgewertet, der Rest landet nur im Label.
var sidecarFlagTokens = map[string]bool{
	"forced": true, "sdh": true, "cc": true, "hi": true,
	"default": true, "full": true, "orig": true, "auto": true,
	"und": true, // ISO-Code fuer "unbestimmt" — gueltig, aber keine Sprache
}

// sidecarSub beschreibt eine gefundene Untertitel-Datei.
type sidecarSub struct {
	Path     string   // absoluter Pfad der Untertitel-Datei
	Ext      string   // ".vtt" / ".srt" / ".ass" / ".ssa", kleingeschrieben
	Language string   // 3-Buchstaben-Code, "" wenn im Namen keiner steht
	Forced   bool     // Dateiname enthält "forced"
	Notes    []string // weitere Zusätze für das Label, z. B. "sdh"
}

// normalizeSidecarLang liefert den 3-Buchstaben-Code zu einem Namens-Token,
// oder "" wenn das Token keine erkennbare Sprache ist. `en-orig`/`de-DE`
// (yt-dlp) werden am Bindestrich auf den Sprachteil reduziert.
func normalizeSidecarLang(token string) string {
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "" {
		return ""
	}
	if code, ok := sidecarLangAliases[t]; ok {
		return code
	}
	// `de-DE`, `en-US`, `en-orig`, `pt_BR` → vorderer Teil entscheidet.
	if i := strings.IndexAny(t, "-_"); i > 0 {
		if code, ok := sidecarLangAliases[t[:i]]; ok {
			return code
		}
	}
	return ""
}

// findSidecarSubs sucht Untertitel-Dateien, die zur Videodatei gehören: gleicher
// Ordner, gleicher Dateiname-Stamm, danach optional Sprach-/Zusatz-Tokens.
// Die Rückgabe ist nach Pfad sortiert, damit die abgeleiteten Stream-Indizes
// über mehrere Requests stabil bleiben.
func findSidecarSubs(videoPath string) []sidecarSub {
	dir := filepath.Dir(videoPath)
	name := filepath.Base(videoPath)
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// Vergleich case-insensitiv: bei Groß-/Kleinschreibungs-Abweichungen im
	// Sprachkürzel (`Film.DE.srt`) soll die Datei trotzdem gefunden werden.
	lowerStem := strings.ToLower(stem)
	out := make([]sidecarSub, 0, 4)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fn := e.Name()
		ext := strings.ToLower(filepath.Ext(fn))
		if !sidecarSubExt[ext] {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(fn, filepath.Ext(fn)))
		if base != lowerStem && !strings.HasPrefix(base, lowerStem+".") {
			continue
		}
		sc := sidecarSub{Path: filepath.Join(dir, fn), Ext: ext}
		// Nur der Teil NACH dem Videonamen trägt Sprache/Zusätze. Der Stamm
		// selbst enthält bei Release-Namen reichlich Punkte, die hier nichts
		// zu suchen haben.
		//
		// ⚠ JEDES Token dahinter muss erkannt sein (Sprache oder bekannter
		// Zusatz), sonst wird die Datei verworfen. Ohne diese Strenge greift ein
		// reiner Präfix-Vergleich in flachen Ordnern zu weit: `Film.2.de.vtt`
		// gehört zu `Film.2.mkv`, beginnt aber ebenfalls mit `Film.` und würde
		// sonst zusätzlich bei `Film.mkv` auftauchen.
		unknownToken := false
		rest := strings.TrimPrefix(base, lowerStem)
		for _, tok := range strings.Split(strings.Trim(rest, "."), ".") {
			if tok == "" {
				continue
			}
			if code := normalizeSidecarLang(tok); code != "" && sc.Language == "" {
				sc.Language = code
				continue
			}
			if tok == "forced" {
				sc.Forced = true
				continue
			}
			if sidecarFlagTokens[tok] {
				sc.Notes = append(sc.Notes, tok)
				continue
			}
			unknownToken = true
			break
		}
		if unknownToken {
			continue
		}
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// sidecarSubLabel baut den im Dropdown gezeigten Namen. Das 📄 grenzt die Datei
// sichtbar von eingebetteten Spuren ab, analog 🎤 (KI) und 📝 (OCR).
func sidecarSubLabel(sc sidecarSub) string {
	lang := "Untertitel"
	if sc.Language != "" {
		lang = sidecarLangLabel(sc.Language)
	}
	parts := []string{}
	if sc.Forced {
		parts = append(parts, "Forced")
	}
	parts = append(parts, sc.Notes...)
	suffix := "Datei"
	if len(parts) > 0 {
		suffix = "Datei · " + strings.Join(parts, " · ")
	}
	return fmt.Sprintf("📄 %s (%s)", lang, suffix)
}

// sidecarLangLabel: deutscher Sprachname zu einem 3-Buchstaben-Code. Bewusst
// eine eigene, kleine Tabelle statt whisperLangLabel (das kennt nur de/en/it
// und arbeitet auf 2-Buchstaben-Codes).
var sidecarLangNames = map[string]string{
	"deu": "Deutsch", "eng": "Englisch", "fra": "Französisch", "spa": "Spanisch",
	"ita": "Italienisch", "nld": "Niederländisch", "por": "Portugiesisch",
	"pol": "Polnisch", "rus": "Russisch", "jpn": "Japanisch", "kor": "Koreanisch",
	"zho": "Chinesisch", "tur": "Türkisch", "ces": "Tschechisch", "hun": "Ungarisch",
	"swe": "Schwedisch", "dan": "Dänisch", "fin": "Finnisch", "nor": "Norwegisch",
	"ell": "Griechisch", "heb": "Hebräisch", "hin": "Hindi", "ukr": "Ukrainisch",
	"ron": "Rumänisch", "ara": "Arabisch", "tha": "Thai", "vie": "Vietnamesisch",
	"ind": "Indonesisch",
}

func sidecarLangLabel(code string) string {
	if n, ok := sidecarLangNames[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// serveSidecarSubtitle liefert die Sidecar-Datei als WebVTT aus.
// `.vtt` geht unverändert raus, alles andere wandelt ffmpeg einmalig und legt
// das Ergebnis im selben Cache ab wie extrahierte eingebettete Spuren.
func (s *Server) serveSidecarSubtitle(w http.ResponseWriter, r *http.Request, itemID int64, sc sidecarSub, idxStr string) {
	out := sc.Path
	if sc.Ext != ".vtt" {
		cacheDir := filepath.Join(s.SubsDir, fmt.Sprintf("%d", itemID))
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		out = filepath.Join(cacheDir, "sidecar-"+idxStr+".vtt")
		// Neu wandeln, wenn der Cache fehlt ODER die Quelldatei neuer ist
		// (eine nachgelieferte, korrigierte .srt soll nicht ewig aus dem
		// Cache überdeckt bleiben).
		stale := true
		if ci, err := os.Stat(out); err == nil {
			if si, serr := os.Stat(sc.Path); serr == nil && !si.ModTime().After(ci.ModTime()) {
				stale = false
			}
		}
		if stale {
			cmd := exec.CommandContext(r.Context(), "ffmpeg",
				"-hide_banner", "-loglevel", "error", "-y",
				"-i", sc.Path,
				"-c:s", "webvtt",
				out,
			)
			if err := cmd.Run(); err != nil {
				_ = os.Remove(out)
				log.Printf("[subtitle] item %d Sidecar %s: WebVTT-Wandlung fehlgeschlagen: %v", itemID, filepath.Base(sc.Path), err)
				writeError(w, 500, "Untertitel-Wandlung fehlgeschlagen: "+err.Error())
				return
			}
		}
	}
	f, err := os.Open(out)
	if err != nil {
		writeError(w, 404, "Untertitel-Datei nicht lesbar")
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	// Kürzer als die 24 h der extrahierten Spuren: eine Datei im Medienordner
	// kann jederzeit ersetzt werden, ohne dass sich die Item-ID ändert.
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, out, info.ModTime(), f)
}
