package api

import (
	"encoding/json"
	"net/http"
	"sort"
)

// nav.go — pro-User-Sichtbarkeit + Reihenfolge der Bibliotheks-Reiterleiste
// (Topbar). Bewusst getrennt von home.go (Startseiten-Streifen) — siehe
// internal/store/nav_prefs.go für die Begründung.

// myNavPreferences liefert für den angemeldeten User alle Libraries, auf die
// er ACL-Zugriff hat, samt effektiver Reiterleisten-Sichtbarkeit (pro-User-
// Override, sonst Default = sichtbar — die Reiterleiste zeigte historisch
// immer alle ACL-Libraries). Für jeden User verfügbar, nicht admin-only.
func (s *Server) myNavPreferences(w http.ResponseWriter, r *http.Request) {
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
	prefs, err := s.Store.GetUserNavPrefs(me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type row struct {
		LibraryID int64  `json:"libraryId"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		OnNav     bool   `json:"onNav"`
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
		onNav, order := true, lib.SortOrder
		if v, ok := prefs[lib.ID]; ok {
			onNav, order = v.OnNav, v.Order
		}
		out = append(out, row{LibraryID: lib.ID, Name: lib.Name, Kind: string(lib.Kind), OnNav: onNav, Order: order})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, 200, map[string]any{"libraries": out})
}

// setMyNavPreference setzt den pro-User-Reiterleisten-Override für eine
// Library. Body: {"onNav": bool}. Für jeden User verfügbar; die Library muss
// im ACL-Zugriff des Users liegen.
func (s *Server) setMyNavPreference(w http.ResponseWriter, r *http.Request) {
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
		OnNav bool `json:"onNav"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetUserNavPref(me.ID, id, body.OnNav); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// setMyNavOrder schreibt die pro-User-Reihenfolge der Reiterleiste.
// Body: {"ids": [3, 1, 2, …]} — komplette Liste in der gewünschten Reihenfolge.
func (s *Server) setMyNavOrder(w http.ResponseWriter, r *http.Request) {
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
	if err := s.Store.SetUserNavOrder(me.ID, filtered); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}
