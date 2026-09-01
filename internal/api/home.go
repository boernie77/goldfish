package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/boernie77/goldfish/internal/model"
)

// home liefert die Daten der Startseite, gruppiert nach Bibliothek:
// für jede Library mit on_home=true werden Fortsetzen / Als nächstes /
// Zuletzt hinzugefügt befüllt. Sektionen ohne Einträge bleiben leer, damit
// das Frontend sie ausblenden kann.
// Non-Admins sehen nur Libraries, für die sie ACL-Zugriff haben.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	libs, err := s.Store.ListLibraries()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Pro-User-Overrides der Startseiten-Sichtbarkeit (leer = überall Default).
	homePrefs, err := s.Store.GetUserHomePrefs(me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type section struct {
		Library  model.Library `json:"library"`
		Continue []model.Item  `json:"continue"`
		NextUp   []model.Item  `json:"nextUp"`
		Recent   []model.Item  `json:"recent"`
	}
	out := make([]section, 0, len(libs))
	for _, lib := range libs {
		onHome := lib.OnHome
		if v, ok := homePrefs[lib.ID]; ok {
			onHome = v.OnHome
		}
		if !onHome {
			continue
		}
		// ACL-Check: Non-Admin sieht nur Libraries mit explizitem Zugriff.
		allowed, err := s.Store.UserHasLibraryAccess(me.ID, lib.ID, me.IsAdmin)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if !allowed {
			continue
		}
		cont, err := s.Store.HomeContinueForLibrary(me.ID, lib.ID, 12)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		var next []model.Item
		if lib.Kind == model.KindTV {
			next, err = s.Store.HomeNextUpForLibrary(me.ID, lib.ID, 12)
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
		recent, err := s.Store.HomeRecentForLibrary(me.ID, lib.ID, 20)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if cont == nil {
			cont = []model.Item{}
		}
		if next == nil {
			next = []model.Item{}
		}
		if recent == nil {
			recent = []model.Item{}
		}
		// Library ohne irgendwelche Einträge überspringen
		if len(cont) == 0 && len(next) == 0 && len(recent) == 0 {
			continue
		}
		out = append(out, section{Library: lib, Continue: cont, NextUp: next, Recent: recent})
	}
	// Pro-User-Reihenfolge: homePrefs[id].Order falls gesetzt, sonst der
	// globale libraries.sort_order-Default (stabiler Sort, Name als Tie-Break).
	effOrder := func(libID int64, fallback int) int {
		if v, ok := homePrefs[libID]; ok {
			return v.Order
		}
		return fallback
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := effOrder(out[i].Library.ID, out[i].Library.SortOrder), effOrder(out[j].Library.ID, out[j].Library.SortOrder)
		if oi != oj {
			return oi < oj
		}
		return out[i].Library.Name < out[j].Library.Name
	})
	writeJSON(w, 200, map[string]any{"sections": out})
}

// setLibraryOrder schreibt die User-definierte Library-Reihenfolge. Admin-only.
// Body: {"ids": [3, 1, 2, ...]}
func (s *Server) setLibraryOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, 400, "ids leer")
		return
	}
	if err := s.Store.SetLibraryOrder(body.IDs); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// setLibraryHomeVisibility togglet `libraries.on_home`. Admin-only.
// Body: {"onHome": bool}
func (s *Server) setLibraryHomeVisibility(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		OnHome bool `json:"onHome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetLibraryOnHome(id, body.OnHome); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// myHomePreferences liefert für den angemeldeten User alle Libraries, auf die
// er ACL-Zugriff hat, samt effektiver Startseiten-Sichtbarkeit (pro-User-
// Override, sonst globaler Default). Für jeden User verfügbar, nicht admin-only.
func (s *Server) myHomePreferences(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	libs, err := s.Store.ListLibraries()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	prefs, err := s.Store.GetUserHomePrefs(me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type row struct {
		LibraryID int64  `json:"libraryId"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		OnHome    bool   `json:"onHome"`
		Order     int    `json:"order"`
	}
	out := make([]row, 0, len(libs))
	for _, lib := range libs {
		allowed, err := s.Store.UserHasLibraryAccess(me.ID, lib.ID, me.IsAdmin)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if !allowed {
			continue
		}
		onHome, order := lib.OnHome, lib.SortOrder
		if v, ok := prefs[lib.ID]; ok {
			onHome, order = v.OnHome, v.Order
		}
		out = append(out, row{LibraryID: lib.ID, Name: lib.Name, Kind: string(lib.Kind), OnHome: onHome, Order: order})
	}
	// Bereits in effektiver Reihenfolge zurückgeben — der Dialog zeigt/sortiert
	// die Checkboxen direkt so, wie sie auf der Startseite erscheinen würden.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, 200, out)
}

// setMyHomeOrder schreibt die pro-User-Reihenfolge der Startseiten-Streifen.
// Body: {"ids": [3, 1, 2, …]} — komplette Liste in der gewünschten Reihenfolge.
// Für jeden User verfügbar; IDs ohne ACL-Zugriff des Users werden ignoriert
// (kein Fehler, damit ein knapp veralteter Client-Snapshot nicht die ganze
// Aktion blockiert).
func (s *Server) setMyHomeOrder(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	filtered := make([]int64, 0, len(body.IDs))
	for _, id := range body.IDs {
		allowed, err := s.Store.UserHasLibraryAccess(me.ID, id, me.IsAdmin)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if allowed {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		writeError(w, 400, "keine gültigen ids")
		return
	}
	if err := s.Store.SetUserHomeOrder(me.ID, filtered); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// setMyHomePreference setzt den pro-User-Startseiten-Override für eine Library.
// Body: {"onHome": bool}. Für jeden User verfügbar; die Library muss im
// ACL-Zugriff des Users liegen.
func (s *Server) setMyHomePreference(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	allowed, err := s.Store.UserHasLibraryAccess(me.ID, id, me.IsAdmin)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !allowed {
		writeError(w, 403, "kein Zugriff auf diese Bibliothek")
		return
	}
	var body struct {
		OnHome bool `json:"onHome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetUserHomePref(me.ID, id, body.OnHome); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// setLibraryChannelLabelOnTop togglet das Private-Card-Layout pro Library
// (siehe model.Library.ChannelLabelOnTop). Admin-only.
// Body: {"channelLabelOnTop": bool}
func (s *Server) setLibraryChannelLabelOnTop(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		ChannelLabelOnTop bool `json:"channelLabelOnTop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetLibraryChannelLabelOnTop(id, body.ChannelLabelOnTop); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}
