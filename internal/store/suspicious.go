package store

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/boernie77/goldfish/internal/model"
)

// Release-Trash-Tokens, die vor dem Vergleich aus Dateinamen/Ordnern
// entfernt werden. Halte die Liste konservativ — was hier fehlt, bleibt als
// Vergleichs-Token drin (und produziert höchstens mehr Matches, keine falschen).
var suspicTrash = regexp.MustCompile(`(?i)\b(1080p|2160p|4k|720p|540p|480p|uhd|bluray|bdrip|web[- ]?dl|webrip|web|hdrip|dvdrip|x264|x265|h264|h265|hevc|aac|ac3|eac3d?|dd51|dts|atmos|german|dl|english|multi|remux|unrated|extended|directors?[ .-]?cut|imax|repack|proper|internal|microhd|sample|hdtv|doku|klassigerhd|avc|ld)\b`)

var suspicYear = regexp.MustCompile(`(?:19|20)\d{2}`)

var suspicStop = map[string]struct{}{
	"the": {}, "der": {}, "die": {}, "das": {}, "a": {}, "an": {}, "le": {},
	"la": {}, "les": {}, "il": {}, "el": {}, "und": {}, "and": {}, "of": {},
	"de": {}, "du": {}, "von": {}, "ein": {}, "eine": {}, "einer": {},
	"im": {}, "in": {}, "zu": {}, "on": {}, "at": {}, "ist": {}, "es": {}, "am": {},
}

func suspicNormalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Tokens eines Strings: klein, ohne Trash-Tags, ohne Stoppwörter,
// ohne Jahreszahlen — geeignet für Set-Vergleich.
func suspicTokens(s string) map[string]struct{} {
	if s == "" {
		return nil
	}
	// Punkte/Unterstriche/Bindestriche zu Space, CamelCase splitten
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	// CamelCase-Split: zwischen kleinem und großem Buchstaben
	var splitted strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			prev := rune(s[i-1])
			if unicode.IsLower(prev) {
				splitted.WriteRune(' ')
			}
		}
		splitted.WriteRune(r)
	}
	s = splitted.String()
	s = suspicTrash.ReplaceAllString(s, " ")
	s = suspicYear.ReplaceAllString(s, " ")
	out := map[string]struct{}{}
	for _, t := range strings.Fields(suspicNormalize(s)) {
		if len(t) < 2 {
			continue
		}
		if _, stop := suspicStop[t]; stop {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

// SuspiciousMatches liefert Items, bei denen die TMDB-Zuordnung
// wahrscheinlich falsch ist: es gibt weder Token-Überlappung zwischen
// Ordnername und Metadata-Titel noch eine Jahresübereinstimmung.
//
// Datei-Dateinamen (basename) werden NICHT als Hinweis genutzt — der User hat
// klargemacht, dass der Ordnername Priorität hat (der ist meistens der
// verlässliche Signaltyp bei Release-Gruppen-Inhalten).
//
// Beschränkbar via libraryID (0 = alle). User-Kontext für Watched/Favorite.
func (s *Store) SuspiciousMatches(libraryID, userID int64, isAdmin bool) ([]model.Item, error) {
	f := ItemFilter{UserID: userID, IsAdmin: isAdmin}
	if libraryID > 0 {
		f.LibraryID = libraryID
	}
	items, err := s.ListItems(f)
	if err != nil {
		return nil, err
	}
	// folder = relPath bis zum ersten '/'
	yearFromText := func(s string) int {
		m := suspicYear.FindString(s)
		if m == "" {
			return 0
		}
		// robuste Konvertierung
		y := 0
		for _, r := range m {
			y = y*10 + int(r-'0')
		}
		return y
	}
	// Parent-Show-Titel cache (für Episoden): parent metadata_id → Titel.
	// Spart uns N+1 Einzel-Queries pro Episode.
	parentTitleCache := map[int64]string{}
	lookupParent := func(pid int64) string {
		if pid == 0 {
			return ""
		}
		if t, ok := parentTitleCache[pid]; ok {
			return t
		}
		m, err := s.GetMetadata(pid)
		if err != nil || m == nil {
			parentTitleCache[pid] = ""
			return ""
		}
		parentTitleCache[pid] = m.Title + " " + m.OriginalTitle
		return parentTitleCache[pid]
	}

	// Vom User bestätigte Matches vorab laden → werden nie als verdächtig
	// gemeldet. Eine Query, nicht N.
	confirmed := map[int64]bool{}
	if rows, err := s.db.Query(`SELECT id FROM items WHERE COALESCE(metadata_confirmed, 0) = 1`); err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			confirmed[id] = true
		}
	}
	var out []model.Item
	for _, it := range items {
		if it.MetadataID == 0 || it.Metadata == nil {
			continue // unmatched zählt separat („Ohne TMDB-Zuordnung")
		}
		if confirmed[it.ID] {
			continue // vom User bestätigt → nicht mehr verdächtig
		}
		// Folder = erster Pfadteil, sonst item.Title als Fallback
		folder := it.Title
		if idx := strings.Index(it.RelPath, "/"); idx > 0 {
			folder = it.RelPath[:idx]
		}
		ftok := suspicTokens(folder)
		// Für Episoden vergleichen wir gegen den SHOW-Titel (Parent), nicht
		// den Episodentitel — sonst würden alle Folgen als verdächtig gelten,
		// weil „Pilot" ≠ „Scrubs".
		mdText := it.Metadata.Title + " " + it.Metadata.OriginalTitle
		if it.Metadata.TMDBType == "episode" && it.Metadata.ParentID != 0 {
			if p := lookupParent(it.Metadata.ParentID); p != "" {
				mdText = p
			}
		}
		mtok := suspicTokens(mdText)
		if len(ftok) == 0 || len(mtok) == 0 {
			continue
		}
		overlap := 0
		for t := range ftok {
			if _, ok := mtok[t]; ok {
				overlap++
			}
		}
		fy := yearFromText(folder)
		if fy == 0 {
			fy = yearFromText(it.RelPath)
		}
		my := it.Metadata.Year
		// Für Episoden macht der Jahresvergleich nicht viel Sinn — der Episode-
		// Jahr ist das Air-Date, der Folder hat meist den Serien-Start.
		// Wir verlangen bei Episoden ausschließlich Token-Overlap.
		if it.Metadata.TMDBType == "episode" {
			if overlap == 0 {
				out = append(out, it)
			}
			continue
		}
		yearOK := my > 0 && fy > 0 && abs(my-fy) <= 1
		if overlap == 0 && !yearOK {
			out = append(out, it)
		}
	}
	return out, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
