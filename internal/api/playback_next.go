package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
)

// playback_next.go — Server-Seite für "Nächste Folge automatisch starten".
//
// Zwei Dinge, die ALLE Clients gemeinsam nutzen sollen (statt sieben Mal
// dieselbe Logik zu pflegen):
//
//  1. GET /api/items/{id}/next-episode → welche Folge kommt nach dieser?
//     Die Reihenfolge-/Doppelfolgen-/Varianten-Logik steht in
//     store.NextEpisodeCandidates, die Rechtprüfung hier.
//  2. GET/PUT /api/playback/preferences → der Pro-Konto-Schalter
//     "autoplayNext" (Default AUS, User-Vorgabe 2026-09-18: jede Nutzerin und
//     jeder Nutzer stellt das selbst ein, gespeichert pro Konto — deshalb
//     serverseitig in user_settings und nicht lokal je Gerät; nur so gilt die
//     Einstellung auch auf dem nächsten Gerät).
//
// Die Auflösung/Qualität (Profil) ist bewusst NICHT Teil der Server-
// Präferenzen: sie wird clientseitig gemerkt (letzte Auswahl) und beim
// Folgenstart mitgeschickt, siehe `?profile=` in /api/playback/{id}.

// userSettingAutoplayNext — Pro-User-Schalter "nächste Folge automatisch
// starten". Default false: ohne aktives Zuschalten ändert sich das bisherige
// Verhalten (Folge endet → Wiedergabe endet) nicht.
const userSettingAutoplayNext = "playback_autoplay_next"

// nextEpisode liefert die auf {id} folgende Episode derselben Serie.
//
// Antwort: {"next": <Item>, "nextTitle": "<Anzeigename>"} oder
// {"next": null, "nextTitle": ""}. Bewusst kein 404, wenn es keine nächste
// Folge gibt — "letzte Folge der Serie" ist ein Normalfall, kein Fehler, und
// jeder Client soll ihn ohne Fehlerbehandlung darstellen können.
//
// `nextTitle` ist der ANZEIGENAME der Folge (TMDB-Folgentitel) und bewusst ein
// eigenes Feld: `Item.title` ist der Dateiname bzw. der daraus geparste Name,
// der TMDB-Titel steht im verknüpften Metadata-Objekt (`metadata.title`).
// Ohne dieses Feld zeigten alle Clients im Autoplay-Hinweis den Dateinamen
// (User-Report 2026-09-18: "Die nächste Folge soll der tmDB Name genannt
// werden, und nicht der der Datei"). Der Endpoint löst das EINMAL zentral auf,
// statt fünf Clients die Metadata-Struktur nachbauen zu lassen.
// `next.metadata.title` bleibt zusätzlich verfügbar (GetItemFor hängt die
// Metadaten an) — Clients mit eigener Fallback-Kette können es nutzen.
//
// Rechtprüfung: Zugriff auf das laufende Item wird geprüft, und der erste
// Kandidat, den der Nutzer sehen darf, wird zurückgegeben. Kandidaten aus
// fremden Bibliotheken (nicht freigegeben) oder oberhalb seiner Altersfreigabe
// werden übersprungen. Damit kann dieser Endpoint per Konstruktion kein
// fremdes Item verraten: die Antwort ist auf das beschränkt, was
// /api/items/{id} für denselben Nutzer ohnehin liefern würde.
func (s *Server) nextEpisode(w http.ResponseWriter, r *http.Request) {
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
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil {
		writeError(w, 404, "nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}

	cands, err := s.Store.NextEpisodeCandidates(it.ID, 25)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for _, c := range cands {
		// Bibliotheks-ACL: kein Zugriff → überspringen, nicht abbrechen.
		ok, err := s.Store.UserHasLibraryAccess(me.ID, c.LibraryID, me.IsAdmin)
		if err != nil || !ok {
			continue
		}
		// Altersfreigabe (0/nil = keine Beschränkung; Admins unbegrenzt).
		if !s.isAgeAllowedForUser(me.IsAdmin, me.MaxAgeRating, c.MetadataID) {
			continue
		}
		next, err := s.Store.GetItemFor(me.ID, c.ID)
		if err != nil || next == nil {
			continue
		}
		writeJSON(w, 200, map[string]any{
			"next":      next,
			"nextTitle": episodeDisplayTitle(next),
		})
		return
	}
	writeJSON(w, 200, map[string]any{"next": nil, "nextTitle": ""})
}

// episodeDisplayTitle — Anzeigename einer Folge für den Autoplay-Hinweis.
//
// `Item.title` ist der Dateiname (bzw. der daraus geparste Name), der echte
// Folgentitel kommt von TMDB und liegt im verknüpften Metadata-Objekt. Der
// Hinweis "Nächste Folge …" muss den TMDB-Titel zeigen, nicht die Datei
// (User-Wunsch 2026-09-18).
//
// Fallback-Kette: metadata.title → item.title. Ein Item ohne Anreicherung
// (TMDB-Match fehlt, Enrichment ausstehend) fällt damit auf den Dateinamen
// zurück statt einen leeren Hinweis zu erzeugen.
func episodeDisplayTitle(it *model.Item) string {
	if it == nil {
		return ""
	}
	if it.Metadata != nil {
		if t := strings.TrimSpace(it.Metadata.Title); t != "" {
			return t
		}
	}
	return it.Title
}

// getPlaybackPreferences liefert die Wiedergabe-Einstellungen des angemeldeten
// Users. Aktuell nur "autoplayNext"; das Objekt ist bewusst erweiterbar
// angelegt, damit weitere Wiedergabe-Schalter denselben Endpoint nutzen.
func (s *Server) getPlaybackPreferences(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	autoplay, err := s.Store.GetUserSettingBool(me.ID, userSettingAutoplayNext, false)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"autoplayNext": autoplay})
}

// setPlaybackPreferences ändert die Wiedergabe-Einstellungen des angemeldeten
// Users. Body: {"autoplayNext": bool} — nur mitgeschickte Felder werden
// geändert (gleiches Muster wie /api/home/strips).
func (s *Server) setPlaybackPreferences(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	if me == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	var body struct {
		AutoplayNext *bool `json:"autoplayNext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.AutoplayNext != nil {
		if err := s.Store.SetUserSettingBool(me.ID, userSettingAutoplayNext, *body.AutoplayNext); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	w.WriteHeader(204)
}
