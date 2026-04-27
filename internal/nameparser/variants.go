package nameparser

import (
	"regexp"
	"sort"
	"strings"
)

// reScenePrefix: kurzer lowercase Release-Group-Präfix gefolgt von "-" und dem Titel.
// Wir capturen sowohl Prefix als auch den ersten Zeichen des Rests (Buchstabe),
// weil Go/RE2 kein Lookahead kennt.
// Beispiele: "gma-wunderschoener", "rsg-bunkerhill", "empire-cars2", "pl3x-allesaufzucker".
var reScenePrefix = regexp.MustCompile(`^[a-z0-9]{2,7}-([a-zA-Z])`)

// stripScenePrefix entfernt einen typischen Scene/Release-Group-Präfix der Form
// "<2-7 kleine Buchstaben/Ziffern>-". Gibt den gestrippten Rest zurück, oder "" wenn
// das Muster nicht matcht.
func stripScenePrefix(s string) string {
	m := reScenePrefix.FindStringSubmatchIndex(s)
	if m == nil {
		return ""
	}
	// Rest beginnt am Start der Capture-Gruppe (erstes Buchstaben-Zeichen danach).
	rest := s[m[2]:]
	if len(rest) < 4 {
		return ""
	}
	return rest
}

// Deleetify ersetzt Leetspeak-Ziffern durch Buchstaben (z.B. "G3rm4n" → "German",
// "Undispu73d" → "Undisputed"). Nur auf Tokens angewendet, deren Buchstaben klar
// dominieren, damit echte Zahlen (Jahre, Episoden) nicht verfälscht werden.
func Deleetify(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		parts[i] = deleetToken(p)
	}
	return strings.Join(parts, " ")
}

func deleetToken(t string) string {
	if len(t) < 4 {
		return t
	}
	letters, digits := 0, 0
	for _, r := range t {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letters++
		case r >= '0' && r <= '9':
			digits++
		}
	}
	// Nur wenn Buchstaben überwiegen und mindestens 1 Ziffer mitten im Token
	if digits == 0 || letters < 3 || letters < digits {
		return t
	}
	return strings.Map(leetRune, t)
}

func leetRune(r rune) rune {
	switch r {
	case '0':
		return 'o'
	case '1':
		return 'i' // statt 'l' — 'i' ist im Deutschen häufiger (Undispu1ted wäre seltsam)
	case '3':
		return 'e'
	case '4':
		return 'a'
	case '5':
		return 's'
	case '7':
		return 't'
	}
	return r
}

// longestWord liefert das längste Token eines Titels (mind. 4 Zeichen, sonst "").
// Nützlich als Fallback-Query, wenn der vollständige Titel durch Obfuskation
// verfremdet wurde (z.B. "Sitrb Langsam" → "Langsam").
func longestWord(title string) string {
	tokens := strings.Fields(title)
	// Stabil sortieren: längere zuerst, bei Gleichheit Reihenfolge erhalten
	sort.SliceStable(tokens, func(i, j int) bool { return len(tokens[i]) > len(tokens[j]) })
	for _, t := range tokens {
		// Satzzeichen rechts/links trimmen
		t = strings.Trim(t, ".,!?:;\"'()[]{}")
		if len(t) >= 4 {
			return t
		}
	}
	return ""
}

// ExpandCandidates erzeugt aus einem ursprünglichen Parsed mehrere Varianten
// zur Verwendung in TMDB-Queries: Original, De-Leet-Variante, längstes Token.
// Duplikate werden entfernt.
func ExpandCandidates(p Parsed) []Parsed {
	seen := map[string]struct{}{}
	out := []Parsed{}
	push := func(title string, year int) {
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		key := strings.ToLower(title) + "@" + itoa(year)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, Parsed{Title: title, Year: year, Season: p.Season, Episode: p.Episode, EpisodeEnd: p.EpisodeEnd, IsEpisode: p.IsEpisode})
	}
	push(p.Title, p.Year)
	push(Deleetify(p.Title), p.Year)
	// Scene-Prefix-Strip: "gma-wunderschoener" → "wunderschoener"
	if stripped := stripScenePrefix(p.Title); stripped != "" {
		push(stripped, p.Year)
		push(Deleetify(stripped), p.Year)
		if lw := longestWord(stripped); lw != "" {
			push(lw, p.Year)
		}
	}
	if lw := longestWord(p.Title); lw != "" {
		push(lw, p.Year)
		push(Deleetify(lw), p.Year)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	// Minimal-int-to-string, ohne strconv-Import (interner Helper)
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
