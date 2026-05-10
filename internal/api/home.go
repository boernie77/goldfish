package api

import (
	"encoding/json"
	"net/http"

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
	type section struct {
		Library  model.Library `json:"library"`
		Continue []model.Item  `json:"continue"`
		NextUp   []model.Item  `json:"nextUp"`
		Recent   []model.Item  `json:"recent"`
	}
	out := make([]section, 0, len(libs))
	for _, lib := range libs {
		if !lib.OnHome {
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
