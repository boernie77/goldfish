// fts.go -- Hilfsfunktion zum Bauen sicherer FTS5-MATCH-Ausdrücke für die
// Titel-/Artist-/Album-Suche (items_fts, siehe schema.go).
package store

import "strings"

// ftsQuery baut aus einem freien Sucheingabe-String eine FTS5-MATCH-Abfrage.
// Jedes Wort wird als eigenständiger, in Anführungszeichen gesetzter Token
// behandelt — interne " werden verdoppelt (FTS5-Escape-Konvention, analog zu
// SQL-String-Literalen). Das neutralisiert JEDE FTS5-Query-Syntax im
// Nutzereingabe-String (AND/OR/NOT/*/-/:/Klammern landen als literaler Text
// innerhalb der Anführungszeichen, nie als Operator) — sicher gegen eine
// Sucheingabe wie `"OR 1=1` oder `foo*`. Mehrere Wörter sind implizit
// UND-verknüpft (FTS5-Default zwischen zwei Termen ohne Operator).
//
// prefix=true hängt an jedes Token zusätzlich ein Präfix-Wildcard an
// ("wort"*) — für den Fuzzy-Modus (ItemFilter.SearchFuzzy). Ein Präfix-Match
// auf "star" trifft auch das Wort "star" selbst, ein Fuzzy-MATCH deckt daher
// automatisch auch die exakten Treffer mit ab.
//
// Leere Eingabe (nur Leerzeichen) liefert "" — der Aufrufer muss das als
// "kein Treffer möglich" behandeln, nicht als leere/ungültige MATCH-Abfrage.
func ftsQuery(search string, prefix bool) string {
	fields := strings.Fields(search)
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, w := range fields {
		esc := strings.ReplaceAll(w, `"`, `""`)
		tok := `"` + esc + `"`
		if prefix {
			tok += "*"
		}
		parts = append(parts, tok)
	}
	return strings.Join(parts, " ")
}

// ftsTitleExpr liefert den SQL-Ausdruck für die indizierte Titelspalte:
// Serientitel schlägt Episodentitel (User-Wunsch 2026-09-17) — Ausnahme
// Tatort (User-Wunsch 2026-10-07): dort zusätzlich der Folgentitel, weil
// Tatort-Folgen unter ihrem Titel gesucht werden. Erwartet die Aliase
// parent/m (metadata) im Kontext; fallback ist der Item-Titel.
func ftsTitleExpr(itemTitle string) string {
	return "CASE WHEN parent.title = 'Tatort' AND m.title IS NOT NULL " +
		"THEN parent.title || ' ' || m.title " +
		"ELSE COALESCE(parent.title, m.title, " + itemTitle + ") END"
}
