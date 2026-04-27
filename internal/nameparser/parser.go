// Package nameparser extrahiert Titel, Jahr und Staffel/Episode aus Dateinamen.
package nameparser

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Parsed struct {
	Title      string
	Year       int
	Season     int
	Episode    int
	EpisodeEnd int // >0 wenn Doppelfolge (S07E23E24 → Episode=23, EpisodeEnd=24)
	IsEpisode  bool
}

var (
	// S01E01, s1e1, S.01.E.01 (Punkte werden weiter unten durch Leerzeichen ersetzt).
	// Optionaler zweiter E-Block für Doppel-Episoden wie S05E16E17 — wird
	// separat als EpisodeEnd gecaptured, Episode bleibt die ERSTE Nummer.
	// Kein `\b` am Anfang: erlaubt "The MiddleS1E01" (ohne Trenner) zu matchen.
	// `\d{1,2}` + `E\d{1,3}` reicht als Anker, damit wir nicht in Wörtern false-positiv matchen.
	reSxxExx = regexp.MustCompile(`(?i)S(\d{1,2})\s*E(\d{1,3})(?:\s*-?\s*E(\d{1,3}))?(?:\s*-?\s*E\d{1,3})*\b`)
	// 1x01
	reNxN = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	// Episode-only (E01, E42) ohne Season — Fallback für flach strukturierte Shows
	// wie "Der Bulle von Tölz/E01 - Titel.avi". Wird als Season 1 interpretiert.
	reEonly = regexp.MustCompile(`(?i)\bE(\d{1,3})\b`)
	// Season-Range in Release-Package-Folder-Namen: "S01-S06", "S01.-.S06", "S01 - S06"
	reSeasonRange = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*[-–]\s*S(\d{1,2})\b`)
	// Einzelne Season-Marker in Folder-Namen: "S01.Complete.German..." → Cut bei S01.
	reSeasonOnly = regexp.MustCompile(`(?i)\bS(\d{1,2})\b`)
	// 4-Ziffern-Jahr (1900–2099)
	reYear = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	// Numerische Episode-Codes (3 oder 4 Ziffern): 104 → S1E04, 1004 → S10E04.
	// Nur aktiv bei ParseEpisodeFile (TV-Kontext), nicht bei ParseFile (Movies).
	reNumericEp = regexp.MustCompile(`\b(\d{3,4})\b`)
	// typische Release-Tags, die vom Titel abgeschnitten werden
	reTrash = regexp.MustCompile(`(?i)\b(480p|576p|720p|1080p|2160p|4k|uhd|hdr|hdr10|dv|hevc|x264|x265|h264|h265|blu[- ]?ray|bd|bdrip|brrip|web[- ]?dl|web|webrip|hdtv|dvdrip|dvd|tvrip|xvid|divx|aac|ac3|dd5?1?|dd\+?|ddp|dts|dts[- ]?hd|atmos|truehd|multi|multisub|dual|dubbed|german|deutsch|english|eng|ger|dl|fs|ws|ld|ml|proper|repack|remux|extended|uncut|directors?[- ]?cut|imax|unrated|custom|internal|retail|limited|complete|video|amazonhd|amzn|nf|hulu|dsnp|max|hmax|scene|t\d{2,}|cd\d+|disc\d+|part\d+|v\d{1,2})\b`)
	// release-group suffix wie "-GROUP"
	reGroupSuffix = regexp.MustCompile(`(?i)-[A-Z0-9]+$`)
)

// stripHashPrefix entfernt Hash-ID-artige Präfixe vor einem `-` oder `_`
// (z.B. "abc123def-MATRIX..." → "MATRIX..."). Vorsichtig: nur wenn das Token
// sehr lang ist UND mehrere Ziffern enthält — um legitimate Titel wie
// "SPIDER-MAN" nicht zu köpfen.
func stripHashPrefix(s string) string {
	idx := strings.IndexAny(s, "-_")
	if idx < 8 {
		return s
	}
	prefix := s[:idx]
	if strings.ContainsRune(prefix, ' ') {
		return s
	}
	digits := 0
	for i := 0; i < len(prefix); i++ {
		if prefix[i] >= '0' && prefix[i] <= '9' {
			digits++
		}
	}
	if digits < 3 {
		return s
	}
	return s[idx+1:]
}

// ParseFile extrahiert Metadaten aus einem Dateinamen (Film-Kontext).
// Erkennt SxxExx und NxN, ABER keine reinen Zahlen (Movies wie "Film 2024" sollen
// nicht als S20E24 missinterpretiert werden).
func ParseFile(path string) Parsed {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return parseString(name, false)
}

// ParseFileStrict wie ParseFile, aber ohne numerische-Episode-Codes (explizit: nur
// SxxExx / NxN). Wird verwendet, wenn wir im TV-Kontext die Raterei über bloße
// Zahlen vermeiden wollen — z. B. bei Release-Dateinamen mit vielen 3-stelligen
// Zahlen wie `tvs-911-dd51-dl-x264-108.mkv`, wo "911" fälschlich als S9E11 gälte.
func ParseFileStrict(path string) Parsed {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return parseString(name, false)
}

// ParseSegmentStrict parst einen reinen Ordner-Namen (ohne Path-Extension-Stripping)
// mit striktem SxxExx/NxN-Matching. Für rel_path-Segmente.
func ParseSegmentStrict(segment string) Parsed {
	return parseString(segment, false)
}

// ParseEpisodeFile extrahiert Metadaten im TV-Kontext.
// Zusätzlich zu SxxExx/NxN werden 3-4-stellige Zahlen als Episode-Codes erkannt:
//   "Derrick 104.avi"           → S1E04
//   "The Wire 304 Dead Soldiers" → S3E04
//   "Show 1004.mkv"             → S10E04
// Jahre (1900–2099) werden ausgeschlossen.
func ParseEpisodeFile(path string) Parsed {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return parseString(name, true)
}

// ParseFolder extrahiert Titel + Jahr aus einem Ordnernamen.
// Liefert IsEpisode=false, da Ordner die Show-Ebene repräsentieren.
// Zusätzliche Folder-spezifische Vorverarbeitung: Release-Package-Notationen wie
// "House.of.Cards.S01.-.S06.Complete..." werden vor dem ersten Season-Marker
// abgeschnitten, damit der Show-Name sauber bei TMDB gefunden wird.
func ParseFolder(name string) Parsed {
	// Erst dot/underscore-Trennung anwenden, um S01.-.S06 als "S01 - S06" zu sehen.
	work := strings.ReplaceAll(name, ".", " ")
	work = strings.ReplaceAll(work, "_", " ")
	// Season-Range zuerst prüfen (spezifischer als SxxExx bei Release-Paketen).
	if m := reSeasonRange.FindStringIndex(work); m != nil {
		work = work[:m[0]]
	} else if m := reSxxExx.FindStringIndex(work); m != nil {
		work = work[:m[0]]
	} else if m := reSeasonOnly.FindStringIndex(work); m != nil {
		// Einzelner S\d Marker ohne E → evtl. "Staffel 1"-Package. Vorsichtig:
		// nur abschneiden wenn danach ein typisches Release-Wort folgt, damit
		// nicht Serien wie "Sisi" fälschlich getrimmt werden.
		tail := work[m[1]:]
		if reTrash.MatchString(tail) {
			work = work[:m[0]]
		}
	}
	p := parseString(work, false)
	p.IsEpisode = false
	p.Season = 0
	p.Episode = 0
	return p
}

func parseString(raw string, allowNumericEpisode bool) Parsed {
	// Ersetze Trennzeichen durch Leerzeichen. Punkte und Underscores sind
	// die häufigsten Release-Trenner.
	work := strings.ReplaceAll(raw, ".", " ")
	work = strings.ReplaceAll(work, "_", " ")

	var p Parsed

	// 1. Episode-Muster
	if m := reSxxExx.FindStringSubmatchIndex(work); m != nil {
		s, _ := strconv.Atoi(work[m[2]:m[3]])
		e, _ := strconv.Atoi(work[m[4]:m[5]])
		p.Season, p.Episode, p.IsEpisode = s, e, true
		// Doppelfolge: S07E23E24 → EpisodeEnd=24. Gruppe 3 ist optional.
		if m[6] >= 0 && m[7] > m[6] {
			if e2, err := strconv.Atoi(work[m[6]:m[7]]); err == nil && e2 > e && e2-e <= 10 {
				p.EpisodeEnd = e2
			}
		}
		work = work[:m[0]]
	} else if m := reNxN.FindStringSubmatchIndex(work); m != nil {
		s, _ := strconv.Atoi(work[m[2]:m[3]])
		e, _ := strconv.Atoi(work[m[4]:m[5]])
		// 1x01 kommt in vielen nicht-Episode-Kontexten vor; wir sind streng:
		// akzeptieren nur wenn Season <= 50 und kein Jahr-Match (z.B. 2024)
		if s <= 50 && e <= 999 {
			p.Season, p.Episode, p.IsEpisode = s, e, true
			work = work[:m[0]]
		}
	}

	// 1b. E-only Format (nur TV-Kontext): "E01 - Titel.avi" → S1E01.
	// Wird vor den numerischen Codes geprüft, weil "E01" eindeutiger ist.
	if !p.IsEpisode && allowNumericEpisode {
		if m := reEonly.FindStringSubmatchIndex(work); m != nil {
			e, _ := strconv.Atoi(work[m[2]:m[3]])
			if e >= 1 && e <= 999 {
				p.Season, p.Episode, p.IsEpisode = 1, e, true
				work = work[:m[0]] + " " + work[m[1]:]
			}
		}
	}

	// 1c. Numerische Episode-Codes (nur im TV-Kontext, nur wenn kein SxxExx-Treffer)
	if !p.IsEpisode && allowNumericEpisode {
		for _, m := range reNumericEp.FindAllStringSubmatchIndex(work, -1) {
			num, _ := strconv.Atoi(work[m[2]:m[3]])
			// Jahre ausschließen
			if num >= 1900 && num <= 2099 {
				continue
			}
			season := num / 100
			episode := num % 100
			if season >= 1 && season <= 29 && episode >= 1 && episode <= 99 {
				p.Season, p.Episode, p.IsEpisode = season, episode, true
				// Zahl durch Leerzeichen ersetzen — Rest-Titel (z.B. Episodentitel dahinter) bleibt
				work = work[:m[0]] + " " + work[m[1]:]
				break
			}
		}
	}

	// 2. Jahr extrahieren (nimmt das erste Jahr im verbleibenden Teil).
	// Edge-Case: Titel IST eine Jahreszahl, z. B. "1917.2019" oder "1992 2022".
	// Steht das erste Jahr ganz am Anfang und existiert ein zweites, nehmen wir
	// das zweite als Release-Jahr und behalten das erste als Titel-Teil.
	if ms := reYear.FindAllStringIndex(work, -1); len(ms) > 0 {
		idx := 0
		trimmed := strings.TrimLeft(work, " \t")
		leadOffset := len(work) - len(trimmed)
		if len(ms) > 1 && ms[0][0] == leadOffset {
			idx = 1
		}
		p.Year, _ = strconv.Atoi(work[ms[idx][0]:ms[idx][1]])
		work = work[:ms[idx][0]]
	}

	// 3. Release-Tags raus
	work = reTrash.ReplaceAllString(work, " ")
	// 4. Klammern entfernen (nach Year-Schnitt sollten nur noch leere übrig sein)
	work = strings.NewReplacer("(", " ", ")", " ", "[", " ", "]", " ").Replace(work)
	// 5. Release-Group-Suffix "-GROUP" am Ende raus (nur falls wir sonst nichts hätten)
	work = reGroupSuffix.ReplaceAllString(work, "")
	// 6. Hash-ID-Präfix "abc123def-…" entfernen, falls lang+mit Ziffern
	work = stripHashPrefix(strings.TrimSpace(work))
	// 7. Tokenweise trimmen: entfernt lose Zeichen wie "-", "_", "." die zwischen
	//    den Wörtern übriggeblieben sind (z.B. "GUARDIANS … VOL 2 -" → "GUARDIANS … VOL 2")
	toks := strings.Fields(work)
	clean := make([]string, 0, len(toks))
	for _, t := range toks {
		t = strings.Trim(t, " -_.,")
		if t != "" {
			clean = append(clean, t)
		}
	}
	p.Title = strings.Join(clean, " ")
	return p
}
