package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) listUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.Store.ListUsers()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, users)
}

func (s *Server) createUserAdmin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || len(body.Password) < 6 {
		writeError(w, 400, "username + password (>=6 Zeichen) nötig")
		return
	}
	id, err := s.Store.CreateUser(body.Username, body.Password, body.IsAdmin)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, 409, "Username schon vergeben")
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	u, _ := s.Store.GetUser(id)
	writeJSON(w, 201, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	me := currentUser(r)
	if me != nil && me.ID == id {
		writeError(w, 400, "Kann eigenen Account nicht löschen")
		return
	}
	if err := s.Store.DeleteUser(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if len(body.Password) < 6 {
		writeError(w, 400, "password muss >=6 Zeichen sein")
		return
	}
	if err := s.Store.SetUserPassword(id, body.Password); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) setUserAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		IsAdmin bool `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	me := currentUser(r)
	if me != nil && me.ID == id && !body.IsAdmin {
		writeError(w, 400, "Kann eigene Admin-Rechte nicht entziehen")
		return
	}
	if err := s.Store.SetUserAdmin(id, body.IsAdmin); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// setUserAgeRating setzt die maximale Altersfreigabe für einen User.
// Body: `{"maxAgeRating": 12}` oder `{"maxAgeRating": null}` für unbeschränkt.
func (s *Server) setUserAgeRating(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		MaxAgeRating *int `json:"maxAgeRating"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.MaxAgeRating != nil {
		v := *body.MaxAgeRating
		if v != 0 && v != 6 && v != 12 && v != 16 && v != 18 {
			writeError(w, 400, "maxAgeRating muss 0/6/12/16/18 oder null sein")
			return
		}
	}
	if err := s.Store.SetUserMaxAgeRating(id, body.MaxAgeRating); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// getUserLibraries gibt die ACL-Liste zurück.
func (s *Server) getUserLibraries(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	ids, err := s.Store.UserLibraryAccess(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if ids == nil {
		ids = []int64{}
	}
	writeJSON(w, 200, ids)
}

// setUserLibraries ersetzt die ACL-Liste.
func (s *Server) setUserLibraries(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		LibraryIDs []int64 `json:"libraryIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetUserLibraryAccess(id, body.LibraryIDs); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// changeMyPassword: User ändert sein eigenes Passwort.
func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	_, hash, err := s.Store.GetUserByName(me.Username)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !verifyPasswordHash(hash, body.OldPassword) {
		writeError(w, 403, "altes Passwort falsch")
		return
	}
	if len(body.NewPassword) < 6 {
		writeError(w, 400, "neues Passwort muss >=6 Zeichen sein")
		return
	}
	if err := s.Store.SetUserPassword(me.ID, body.NewPassword); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}
