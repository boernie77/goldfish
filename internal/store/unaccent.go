package store

import (
	"database/sql/driver"
	"sync"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	sqlite "modernc.org/sqlite"
)

// unaccentTransform zerlegt zuerst in Basiszeichen + Kombinationszeichen
// (NFD, z.B. "é" -> "e" + U+0301 COMBINING ACUTE ACCENT), entfernt dann alle
// Zeichen der Unicode-Kategorie Mn ("nonspacing mark" — die Kombinations-
// zeichen selbst) und fügt den Rest wieder zusammen (NFC). "é"/"ñ"/"ö"/…
// werden so auf "e"/"n"/"o" abgebildet.
var unaccentTransform = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// unaccent macht die Suche tolerant gegenüber diakritischen Zeichen
// (User-Wunsch: "Señorita" soll auch mit "senorita" gefunden werden, gilt
// für Titel/Künstler/Album/Cast-Namen). Scheitert die Transformation
// (ungültiges UTF-8), wird der Originalstring unverändert zurückgegeben —
// dann greift wenigstens noch die normale LIKE-Suche.
func unaccent(s string) string {
	result, _, err := transform.String(unaccentTransform, s)
	if err != nil {
		return s
	}
	return result
}

var registerUnaccentOnce sync.Once

// registerUnaccentFunction registriert UNACCENT(x) als SQLite-Skalarfunktion,
// analog zu registerNaturalCollation() für COLLATE NATSORT — einmal pro
// Prozess, danach in jeder Query nutzbar. Nur auf TEXT-Spalten sinnvoll;
// NULL/keine Strings kommen unverändert durch.
func registerUnaccentFunction() {
	registerUnaccentOnce.Do(func() {
		sqlite.MustRegisterDeterministicScalarFunction(
			"UNACCENT",
			1,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				s, ok := args[0].(string)
				if !ok {
					return args[0], nil
				}
				return unaccent(s), nil
			},
		)
	})
}
