package store

import (
	"strings"
	"unicode"
)

// naturalCompare vergleicht zwei Strings „natural sort"-mässig: Zahlen werden
// als ganze Zahlen verglichen, nicht zeichenweise. „Folge 2" kommt vor
// „Folge 10", „Episode 9" vor „Episode 11". Fuer nicht-numerische Bereiche
// wird case-insensitive auf Lowercase verglichen. Wird per
// sqlite.MustRegisterCollationUtf8("NATSORT", naturalCompare) registriert
// und in ORDER-BY-Clauses als COLLATE NATSORT referenziert.
func naturalCompare(a, b string) int {
	ar := []rune(strings.ToLower(a))
	br := []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		ac, bc := ar[i], br[j]
		if unicode.IsDigit(ac) && unicode.IsDigit(bc) {
			iEnd := i
			for iEnd < len(ar) && unicode.IsDigit(ar[iEnd]) {
				iEnd++
			}
			jEnd := j
			for jEnd < len(br) && unicode.IsDigit(br[jEnd]) {
				jEnd++
			}
			// Fuehrende Nullen wegtrimmen → "007" vs "7" gleich; Reine
			// String-Compare nach Trim ist korrekt, weil bei gleicher
			// Laenge lexikografisch = numerisch ist.
			anum := strings.TrimLeft(string(ar[i:iEnd]), "0")
			bnum := strings.TrimLeft(string(br[j:jEnd]), "0")
			if anum == "" {
				anum = "0"
			}
			if bnum == "" {
				bnum = "0"
			}
			if len(anum) != len(bnum) {
				if len(anum) < len(bnum) {
					return -1
				}
				return 1
			}
			if anum < bnum {
				return -1
			}
			if anum > bnum {
				return 1
			}
			i, j = iEnd, jEnd
			continue
		}
		if ac < bc {
			return -1
		}
		if ac > bc {
			return 1
		}
		i++
		j++
	}
	switch {
	case i < len(ar):
		return 1
	case j < len(br):
		return -1
	}
	return 0
}
