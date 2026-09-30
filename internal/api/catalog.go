package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/boernie77/goldfish/internal/catalog"
	"github.com/boernie77/goldfish/internal/store"
)

// fileNrRe: Tatort-Dateinamen tragen die fortlaufende Nummer
// (`Tatort.S1972E0019.Finke.Strandgut.mp4`, ORF-Folgen `E186a`). Rückfall für
// Dateien ohne TMDB-Zuordnung, die trotzdem im Katalog stehen (186a).
var fileNrRe = regexp.MustCompile(`(?i)\.S\d{4}E0*(\d+[a-z]?)\.`)

type catalogEntryOut struct {
	Nr        string   `json:"nr"`
	Title     string   `json:"title"`
	Date      string   `json:"date"`
	Sender    string   `json:"sender"`
	Ermittler []string `json:"ermittler"`
	Owned     bool     `json:"owned"`
}

type catalogGroupOut struct {
	Team   string `json:"team"`
	Folder string `json:"folder"` // Unterordner (leer = kein eigener Ordner)
	Total  int    `json:"total"`
	Owned  int    `json:"owned"`
}

// libraryCatalog (seit 1.4.65): Ermittler-Katalog zu einem Serien-Ordner.
//   - ?folder=Tatort                → {groups:[…]} alle Teams mit Bestand/Gesamt
//     und dem Unterordner, dem Goldfish das Team zuordnet (leer = keiner)
//   - ?folder=Tatort/Batic und Leitmayr → Folgen der Teams dieses Ordners
//   - ?folder=Tatort&team=Brinkmann  → Folgen genau dieses Teams
//
// Die Zuordnung Ordner ↔ Team lernt Goldfish aus den vorhandenen Dateien: jede
// Datei wird über TMDB-Titel/-Erstausstrahlung (Rückfall: Nummer im Dateinamen)
// einer Katalog-Zeile zugeordnet; ein Team gehört zu dem Unterordner, in dem die
// MEISTEN seiner Folgen liegen (Crossover-Folgen verschieben so kein Team).
func (s *Server) libraryCatalog(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	if !s.requireLibAccess(w, r, libID) {
		return
	}
	folder := strings.Trim(r.URL.Query().Get("folder"), "/")
	top, sub, _ := strings.Cut(folder, "/")
	if i := strings.Index(sub, "/"); i >= 0 {
		sub = sub[:i]
	}
	team := r.URL.Query().Get("team")

	metaID, _ := s.Store.GetFolderMetadataID(libID, top)
	meta, _ := s.Store.GetMetadata(metaID)
	if meta == nil || meta.TMDBType != "tv" {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	cat := catalog.ForShow(meta.TMDBID)
	if cat == nil {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	var maxAge int
	if !me.IsAdmin && me.MaxAgeRating != nil {
		maxAge = *me.MaxAgeRating
	}
	items, err := s.Store.ListItems(store.ItemFilter{LibraryID: libID, Folder: top, UserID: me.ID, IsAdmin: me.IsAdmin, MaxAgeRating: maxAge})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	nrIdx := map[string]int{}
	for i, e := range cat.Episodes {
		nrIdx[e.Nr] = i
	}
	owned := map[int]bool{}
	teamFolderCount := map[string]map[string]int{} // Team → Unterordner → Anzahl
	for _, it := range items {
		idx := -1
		if it.Metadata != nil && it.Metadata.TMDBType == "episode" {
			idx = cat.Match(it.Metadata.Title, it.Metadata.ReleaseDate)
		}
		if idx < 0 {
			if m := fileNrRe.FindStringSubmatch(it.RelPath); m != nil {
				if i, ok := nrIdx[strings.ToLower(m[1])]; ok {
					idx = i
				}
			}
		}
		if idx < 0 {
			continue
		}
		owned[idx] = true
		parts := strings.Split(it.RelPath, "/")
		if len(parts) < 3 {
			continue
		}
		for _, t := range cat.Episodes[idx].Ermittler {
			if teamFolderCount[t] == nil {
				teamFolderCount[t] = map[string]int{}
			}
			teamFolderCount[t][parts[1]]++
		}
	}
	teamFolder := map[string]string{}
	for t, m := range teamFolderCount {
		best, bestN := "", 0
		for f, n := range m {
			if n > bestN || (n == bestN && f < best) {
				best, bestN = f, n
			}
		}
		teamFolder[t] = best
	}

	// Übersicht aller Teams (Tatort-Sammlung, „Ermittler ohne eigenen Ordner").
	if sub == "" && team == "" {
		groups := map[string]*catalogGroupOut{}
		for i, e := range cat.Episodes {
			for _, t := range e.Ermittler {
				g := groups[t]
				if g == nil {
					g = &catalogGroupOut{Team: t, Folder: teamFolder[t]}
					groups[t] = g
				}
				g.Total++
				if owned[i] {
					g.Owned++
				}
			}
		}
		out := make([]catalogGroupOut, 0, len(groups))
		for _, g := range groups {
			out = append(out, *g)
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Team) < strings.ToLower(out[j].Team) })
		writeJSON(w, 200, map[string]any{"available": true, "source": cat.Source, "groups": out})
		return
	}

	teams := map[string]bool{}
	if team != "" {
		teams[team] = true
	} else {
		for t, f := range teamFolder {
			if f == sub {
				teams[t] = true
			}
		}
	}
	var list []catalogEntryOut
	nOwned := 0
	for i, e := range cat.Episodes {
		hit := false
		for _, t := range e.Ermittler {
			if teams[t] {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		list = append(list, catalogEntryOut{Nr: e.Nr, Title: e.Title, Date: e.Date, Sender: e.Sender, Ermittler: e.Ermittler, Owned: owned[i]})
		if owned[i] {
			nOwned++
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Date < list[j].Date })
	teamNames := make([]string, 0, len(teams))
	for t := range teams {
		teamNames = append(teamNames, t)
	}
	sort.Strings(teamNames)
	missing := make([]catalogEntryOut, 0)
	for _, e := range list {
		if !e.Owned {
			missing = append(missing, e)
		}
	}
	writeJSON(w, 200, map[string]any{
		"available": true, "source": cat.Source, "teams": teamNames,
		"total": len(list), "owned": nOwned, "missing": missing,
	})
}
