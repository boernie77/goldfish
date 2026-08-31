package store

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
)

// reSimCopySuffix entfernt eine abschließende Kopie-Markierung wie " (2)".
var reSimCopySuffix = regexp.MustCompile(`\s*\(\d{1,2}\)\s*$`)

// reSimSep normalisiert Trenner (Punkt, Unterstrich, Bindestrich) zu einem Leerzeichen.
var reSimSep = regexp.MustCompile(`[._\-]+`)

// reSimExt schneidet die Dateiendung ab.
var reSimExt = regexp.MustCompile(`\.[a-z0-9]{2,4}$`)

// normalizeSimName bereitet einen Dateinamen für den Ähnlichkeitsvergleich auf:
// klein, ohne Endung, ohne " (N)"-Kopiesuffix, Trenner → Leerzeichen.
func normalizeSimName(base string) string {
	s := strings.ToLower(base)
	s = reSimExt.ReplaceAllString(s, "")
	s = reSimCopySuffix.ReplaceAllString(s, "")
	s = reSimSep.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// levenshtein — klassische Editierdistanz (Zeichen-basiert, ausreichend für
// Dateinamen). Rune-sicher.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	m, n := len(ra), len(rb)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}
	prev := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		cur := make([]int, n+1)
		cur[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[n]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// differsOnlyInDigits ist true, wenn a und b gleich lang sind und sich AN JEDER
// abweichenden Position beide nur in einer Ziffer unterscheiden. Solche Paare
// sind fast immer durchnummerierte GESCHWISTER (z. B. FTV-Shoot-IDs
// `alana-7127-07` vs `alana-7128-01`, oder Episoden `s01e03` vs `s01e04`) —
// keine Duplikate. `film` vs `film (2)` fällt hier NICHT rein (Klammer wird
// vorher gestrippt → identisch), `film.wmv` vs `film.mp4` auch nicht (Endung
// gestrippt → identisch).
func differsOnlyInDigits(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) != len(rb) {
		return false
	}
	diff := false
	for i := range ra {
		if ra[i] == rb[i] {
			continue
		}
		if !isDigitRune(ra[i]) || !isDigitRune(rb[i]) {
			return false
		}
		diff = true
	}
	return diff
}

func isDigitRune(r rune) bool { return r >= '0' && r <= '9' }

// nameSimilarity — 1.0 = identisch, 0.0 = komplett verschieden.
func nameSimilarity(a, b string) float64 {
	la, lb := len([]rune(a)), len([]rune(b))
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(maxLen)
}

// SimilarNameDupes liefert Items in `libraryID` (optional auf den Ordner
// `folder` inkl. Unterbaum eingeschränkt), deren Dateiname (nach Normalisierung,
// siehe normalizeSimName) zu mindestens `threshold` (0..1) mit einem anderen
// Item ÜBEREINSTIMMT UND das exakt gleiche Auflösung (width×height) UND fast
// gleiche Laufzeit (±1 s) hat.
//
// Zweck: Fast-Duplikate finden, die der strenge „Duplikate"-Filter (gleiche
// metadata_id) und „Datei in anderem Ordner" (exakt gleicher Name + Größe)
// nicht erwischen — z. B. `film.mp4` neben `film (2).mp4` oder `film.wmv`
// neben der `film.mp4`-Umwandlung. Pro Treffer stehen die rel_path(s) der
// Fast-Zwillinge in `item.DupeOtherPaths`.
func (s *Store) SimilarNameDupes(libraryID, userID int64, isAdmin bool, folder string, threshold float64) ([]model.Item, error) {
	if threshold <= 0 || threshold > 1 {
		threshold = 0.9
	}
	if threshold < 0.5 {
		threshold = 0.5
	}
	folder = strings.Trim(folder, "/")

	inScope := func(rel string) bool {
		if folder == "" {
			return true
		}
		return rel == folder || strings.HasPrefix(rel, folder+"/")
	}

	type ent struct {
		id      int64
		relPath string
		w, h    int
		dur     float64
		norm    string
	}
	rows, err := s.db.Query(`SELECT id, rel_path, COALESCE(width,0), COALESCE(height,0), COALESCE(duration_sec,0)
		FROM items WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	// Nach Auflösung bucketen — nur gleich große Videos können ein Paar bilden.
	byRes := map[[2]int][]ent{}
	for rows.Next() {
		var e ent
		if scanErr := rows.Scan(&e.id, &e.relPath, &e.w, &e.h, &e.dur); scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		if !inScope(e.relPath) || e.dur <= 0 || (e.w == 0 && e.h == 0) {
			continue
		}
		e.norm = normalizeSimName(path.Base(e.relPath))
		if e.norm == "" {
			continue
		}
		k := [2]int{e.w, e.h}
		byRes[k] = append(byRes[k], e)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Union-Find über Item-IDs.
	parent := map[int64]int64{}
	var find func(int64) int64
	find = func(x int64) int64 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int64) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for _, arr := range byRes {
		for _, e := range arr {
			parent[e.id] = e.id
		}
		sort.Slice(arr, func(i, j int) bool { return arr[i].dur < arr[j].dur })
		for i := 0; i < len(arr); i++ {
			for j := i + 1; j < len(arr) && arr[j].dur-arr[i].dur <= 1.0; j++ {
				if arr[i].norm != arr[j].norm && differsOnlyInDigits(arr[i].norm, arr[j].norm) {
					continue // durchnummerierte Geschwister, kein Duplikat
				}
				if nameSimilarity(arr[i].norm, arr[j].norm) >= threshold {
					union(arr[i].id, arr[j].id)
				}
			}
		}
	}

	// Gruppen mit ≥2 Mitgliedern einsammeln.
	groups := map[int64][]int64{}
	relByID := map[int64]string{}
	for _, arr := range byRes {
		for _, e := range arr {
			r := find(e.id)
			groups[r] = append(groups[r], e.id)
			relByID[e.id] = e.relPath
		}
	}
	others := map[int64][]string{}
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		for _, id := range members {
			for _, oid := range members {
				if oid != id {
					others[id] = append(others[id], relByID[oid])
				}
			}
		}
	}
	if len(others) == 0 {
		return nil, nil
	}

	f := ItemFilter{UserID: userID, IsAdmin: isAdmin, LibraryID: libraryID}
	if folder != "" {
		f.Folder = folder
	}
	items, err := s.ListItems(f)
	if err != nil {
		return nil, err
	}
	out := make([]model.Item, 0, len(others))
	for _, it := range items {
		if paths, ok := others[it.ID]; ok {
			sort.Strings(paths)
			it.DupeOtherPaths = paths
			out = append(out, it)
		}
	}
	return out, nil
}
