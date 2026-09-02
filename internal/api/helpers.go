package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/boernie77/goldfish/internal/store"
	"github.com/go-chi/chi/v5"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func pathInt(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, key), 10, 64)
}

// verifyPasswordHash ist ein dünner Wrapper um bcrypt-Vergleich (über store).
func verifyPasswordHash(hash, password string) bool {
	return store.VerifyPassword(hash, password)
}

// errString liefert err.Error() oder "(unbekannt)" bei nil.
func errString(err error) string {
	if err == nil {
		return "(unbekannt)"
	}
	return err.Error()
}

// requireLibAccess liefert true, wenn der eingeloggte User Zugriff auf die Lib hat.
// Bei fehlendem Zugriff schreibt es 403 und liefert false.
func (s *Server) requireLibAccess(w http.ResponseWriter, r *http.Request, libID int64) bool {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return false
	}
	ok, err := s.Store.UserHasLibraryAccess(me.ID, libID, me.IsAdmin)
	if err != nil {
		writeError(w, 500, err.Error())
		return false
	}
	if !ok {
		writeError(w, 403, "kein Zugriff auf diese Bibliothek")
		return false
	}
	return true
}

// requireAgeAllowed prüft, ob der User das Item basierend auf seiner FSK-
// Beschränkung sehen/abspielen darf. Admins sind immer berechtigt.
// Ein Item mit höherer Altersfreigabe als das User-Max liefert 403.
// Items ohne gesetzte age_rating bleiben erlaubt (unbekannt ≠ hoch).
func (s *Server) requireAgeAllowed(w http.ResponseWriter, r *http.Request, itemMetadataID int64) bool {
	me := currentUser(r)
	if me == nil {
		return true
	}
	if !s.isAgeAllowedForUser(me.IsAdmin, me.MaxAgeRating, itemMetadataID) {
		writeError(w, 403, "Altersfreigabe überschreitet dein Limit")
		return false
	}
	return true
}

// requireDownloadAllowed: 403 wenn der User Downloads nicht erlaubt hat.
// Admins ignorieren den Wert (siehe model.User.CanDownload-Kommentar).
func (s *Server) requireDownloadAllowed(w http.ResponseWriter, r *http.Request) bool {
	me := currentUser(r)
	if me == nil {
		return true
	}
	if me.IsAdmin || me.CanDownload {
		return true
	}
	writeError(w, 403, "Downloads sind für dieses Konto nicht erlaubt")
	return false
}

// isAgeAllowedForUser ist der reine (writer-lose) Kern von requireAgeAllowed —
// wiederverwendet für die Gesehen-Sync-Propagation (internal/api/watch_links.go),
// wo kein http.ResponseWriter zur Hand ist, sondern nur "darf der Partner
// dieses Item überhaupt sehen" geprüft werden muss.
func (s *Server) isAgeAllowedForUser(isAdmin bool, maxAgeRating *int, itemMetadataID int64) bool {
	if isAdmin || maxAgeRating == nil {
		return true
	}
	if itemMetadataID == 0 {
		return true
	}
	md, _ := s.Store.GetMetadata(itemMetadataID)
	if md == nil {
		return true
	}
	ageStr := md.AgeRating
	// Episoden: Fallback auf Parent-Show-FSK, wenn die Episode selbst keine hat.
	if ageStr == "" && md.ParentID > 0 {
		if p, _ := s.Store.GetMetadata(md.ParentID); p != nil {
			ageStr = p.AgeRating
		}
	}
	if ageStr == "" {
		return true
	}
	var itemAge int
	_, _ = fmt.Sscanf(ageStr, "%d", &itemAge)
	return itemAge <= *maxAgeRating
}
