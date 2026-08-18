package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/boernie77/goldfish/internal/model"
)

// --- Gesehen-Sync zwischen zwei Usern (User-Anfrage 2026-08-19) ---
//
// Server-seitige Write-Time-Propagation statt Client-seitiger Lösung, damit
// der Sync für ALLE Clients (Browser, Android, Mac) automatisch funktioniert,
// nicht nur für die Mac-App. Ein User schlägt einen Partner vor, der Partner
// muss bestätigen (mutual opt-in), danach spiegelt jeder Gesehen-Toggle
// automatisch zum Partner — aber NUR für Items, die der Partner laut seiner
// eigenen Library-ACL + FSK-Beschränkung auch selbst sehen dürfte.

// listOtherUsernames liefert alle Usernamen außer dem eigenen — für den
// Partner-Picker in den Einstellungen. Bewusst NICHT admin-only (jeder
// eingeloggte User soll einen Partner auswählen können), aber liefert nur
// id+username, keine sensiblen Felder.
func (s *Server) listOtherUsernames(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	users, err := s.Store.ListUsers()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out := []map[string]any{}
	for _, u := range users {
		if u.ID == me.ID {
			continue
		}
		out = append(out, map[string]any{"id": u.ID, "username": u.Username})
	}
	writeJSON(w, 200, out)
}

func (s *Server) listWatchLinks(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	links, err := s.Store.GetWatchLinks(me.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, links)
}

func (s *Server) requestWatchLink(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	partner, _, err := s.Store.GetUserByName(body.Username)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if partner == nil {
		writeError(w, 404, "Benutzer nicht gefunden")
		return
	}
	if err := s.Store.RequestWatchLink(me.ID, partner.ID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) confirmWatchLink(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	partnerID, err := pathInt(r, "partnerId")
	if err != nil {
		writeError(w, 400, "ungültige partnerId")
		return
	}
	if err := s.Store.ConfirmWatchLink(me.ID, partnerID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	w.WriteHeader(204)
}

// unlinkWatchLink trennt eine bestehende Verknüpfung ODER lehnt eine
// eingehende Anfrage ab — beides ist einfach "Zeile löschen".
func (s *Server) unlinkWatchLink(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	partnerID, err := pathInt(r, "partnerId")
	if err != nil {
		writeError(w, 400, "ungültige partnerId")
		return
	}
	if err := s.Store.UnlinkWatchLink(me.ID, partnerID); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// propagateWatchedToLinkedPartners spiegelt einen Gesehen-Toggle zu allen
// aktiv verknüpften Partnern — aber nur wenn der Partner selbst Zugriff auf
// die Library hat UND die FSK-Beschränkung des Partners das Item erlaubt.
// Fehler werden nur geloggt, der ursprüngliche setWatched-Call bleibt
// erfolgreich (Sync ist Komfort-Feature, kein Blocker — analog NFO-Write).
func (s *Server) propagateWatchedToLinkedPartners(userID int64, it *model.Item, watched bool) {
	partnerIDs, err := s.Store.ActiveWatchPartnerIDs(userID)
	if err != nil || len(partnerIDs) == 0 {
		return
	}
	for _, partnerID := range partnerIDs {
		partner, err := s.Store.GetUser(partnerID)
		if err != nil || partner == nil {
			continue
		}
		ok, err := s.Store.UserHasLibraryAccess(partner.ID, it.LibraryID, partner.IsAdmin)
		if err != nil || !ok {
			continue
		}
		if !s.isAgeAllowedForUser(partner.IsAdmin, partner.MaxAgeRating, it.MetadataID) {
			continue
		}
		if err := s.Store.SetWatchedFor(partner.ID, it.ID, watched); err != nil {
			log.Printf("[watch-link] propagate an user %d fehlgeschlagen: %v", partner.ID, err)
		}
	}
}
