package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/boernie77/goldfish/internal/store"
)

const introSkipEnabledSettingKey = "introskip_enabled"

// introSkipGetSettings liefert den globalen An/Aus-Zustand der Intro-Erkennung.
func (s *Server) introSkipGetSettings(w http.ResponseWriter, _ *http.Request) {
	v, _ := s.Store.GetSetting(introSkipEnabledSettingKey, "false")
	writeJSON(w, 200, map[string]any{"enabled": v == "true"})
}

func (s *Server) introSkipSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	v := "false"
	if body.Enabled {
		v = "true"
	}
	if err := s.Store.SetSetting(introSkipEnabledSettingKey, v); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if body.Enabled && s.IntroSkip != nil {
		s.IntroSkip.Trigger()
	}
	w.WriteHeader(204)
}

// listIntroSkipFolders liefert pro aktiviertem Serien-Ordner den Job-Status.
func (s *Server) listIntroSkipFolders(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	folders, err := s.Store.ListIntroSkipFolders(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type entry struct {
		Folder          string `json:"folder"`
		Season          int    `json:"season,omitempty"`
		JobStatus       string `json:"jobStatus"`
		EpisodesTotal   int    `json:"episodesTotal"`
		EpisodesMatched int    `json:"episodesMatched"`
		Error           string `json:"error,omitempty"`
	}
	out := []entry{}
	for _, f := range folders {
		e := entry{Folder: f}
		if season, ok, err := s.Store.IntroSkipFolderSeason(libID, f); err == nil && ok {
			e.Season = season
		}
		if job, err := s.Store.GetIntroSkipJob(libID, f); err == nil && job != nil {
			e.JobStatus = job.Status
			e.EpisodesTotal = job.EpisodesTotal
			e.EpisodesMatched = job.EpisodesMatched
			e.Error = job.Error
		}
		out = append(out, e)
	}
	writeJSON(w, 200, out)
}

// setIntroSkipFolder aktiviert/deaktiviert die Intro-Erkennung für EINEN
// Serien-Ordner. folder=="" wird abgelehnt — bewusst kein
// "ganze Bibliothek"-Schalter, siehe CLAUDE.md.
func (s *Server) setIntroSkipFolder(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder  string `json:"folder"`
		Enabled bool   `json:"enabled"`
		// Season: Zeiger, damit "Feld fehlt" (nil, z.B. beim reinen
		// Checkbox-Toggle) von "explizit auf 0/alle Staffeln gesetzt"
		// unterscheidbar ist. Ohne diese Unterscheidung würde JEDER
		// enabled=true-Aufruf (auch ein simples An/Aus-Toggle ohne
		// Staffel-Absicht) die Staffel-Beschränkung stillschweigend auf 0
		// zurücksetzen — real passiert (2026-08-13): Chuck stand nach dem
		// Season-2-Backfill korrekt auf season=2, ein späteres reines
		// Checkbox-Toggle (Off/On beim Explorieren des Dialogs) setzte es
		// unbemerkt wieder auf 0 zurück.
		Season *int `json:"season"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.Folder == "" {
		writeError(w, 400, "folder erforderlich — Intro-Erkennung ist nur pro einzelner Serie aktivierbar")
		return
	}
	if err := s.Store.SetIntroSkipFolder(libID, body.Folder, body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if body.Enabled {
		// 🔴 Bug bis 2026-09-06: UpsertIntroSkipJob hing fälschlich zusätzlich
		// an `body.Season != nil` — ein reiner Checkbox-Toggle OHNE season-Feld
		// (genau das, was jeder einzelne Zeilen-Klick UND "☑ Alle auswählen"
		// im Dialog senden, introskip.js sendet dort nie ein season-Feld)
		// aktivierte den Ordner zwar (Zeile in intro_skip_folders existiert),
		// legte aber NIE einen intro_skip_jobs-Eintrag an — der Worker hatte
		// dadurch für diesen Ordner schlicht nichts zu tun, "startet nie".
		// Live gefunden: 6 von 218 aktivierten Serien einer Bibliothek hatten
		// gar keinen jobStatus. Season-Set bleibt bewusst an Season!=nil
		// gekoppelt (das war der korrekte Teil des ursprünglichen Fixes vom
		// 2026-08-13, verhinderte ungewolltes Zurücksetzen einer season-
		// Beschränkung) — nur der Job-Upsert selbst gehört an reines Enabled.
		if body.Season != nil {
			if err := s.Store.SetIntroSkipFolderSeason(libID, body.Folder, *body.Season); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
		if err := s.Store.UpsertIntroSkipJob(libID, body.Folder); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if s.IntroSkip != nil {
			s.IntroSkip.Trigger()
		}
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "job", "introskip_folder_toggle", fmt.Sprintf("%q → aktiv: %v", body.Folder, body.Enabled), deviceLabel(r))
	}
	w.WriteHeader(204)
}

// getIntroSkipAutoNew liefert den Zustand des "Neue Serien automatisch
// aktivieren"-Flags einer Bibliothek (User-Wunsch 2026-09-06).
func (s *Server) getIntroSkipAutoNew(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	enabled, err := s.Store.LibraryIntroSkipAutoNew(libID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled})
}

func (s *Server) setIntroSkipAutoNew(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if err := s.Store.SetLibraryIntroSkipAutoNew(libID, body.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if me := currentUser(r); me != nil {
		_ = s.Store.LogActivity(me.ID, me.Username, "admin", "introskip_auto_new", fmt.Sprintf("lib %d → %v", libID, body.Enabled), deviceLabel(r))
	}
	w.WriteHeader(204)
}

// introSkipFolderEpisodes liefert alle Episoden eines Serien-Ordners mit
// ihrem aktuellen Erkennungs-Status — für die aufklappbare Episoden-Liste
// im Admin-Dialog (pro Job-Tab: Fertig/Fehler/Ausstehend).
func (s *Server) introSkipFolderEpisodes(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		writeError(w, 400, "folder erforderlich")
		return
	}
	details, err := s.Store.IntroSkipEpisodeDetails(libID, folder)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if details == nil {
		details = []store.IntroSkipEpisodeDetail{}
	}
	writeJSON(w, 200, details)
}

// introSkipWorkerStatus liefert den globalen Live-Zustand des Workers —
// nicht admin-gated (harmlos: nur Queue/Laufend-Info, keine Verwaltung).
func (s *Server) introSkipWorkerStatus(w http.ResponseWriter, _ *http.Request) {
	if s.IntroSkip == nil {
		writeJSON(w, 200, map[string]any{"running": false})
		return
	}
	writeJSON(w, 200, s.IntroSkip.Status())
}

func (s *Server) introSkipLog(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	// "running" seit 2026-09-06 ergänzt (User-Wunsch, analog OCR-Dialog) —
	// der Status existiert im Store schon lange (MarkIntroSkipJobRunning),
	// war nur nie im Log-Endpoint abfragbar.
	allowed := map[string]bool{"done": true, "failed": true, "pending": true, "running": true}
	if !allowed[status] {
		writeError(w, 400, "status=pending|running|done|failed erforderlich")
		return
	}
	jobs, err := s.Store.ListIntroSkipJobsByStatus(status)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if jobs == nil {
		writeJSON(w, 200, []any{})
		return
	}
	writeJSON(w, 200, jobs)
}

// retryIntroSkipFolder setzt den Job eines einzelnen Ordners zurück auf
// 'pending' und triggert den Worker — für den ↻-Button im Admin-Dialog.
func (s *Server) retryIntroSkipFolder(w http.ResponseWriter, r *http.Request) {
	libID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Folder string `json:"folder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Folder == "" {
		writeError(w, 400, "folder erforderlich")
		return
	}
	if s.IntroSkip == nil {
		writeError(w, 503, "Intro-Erkennung nicht initialisiert")
		return
	}
	if err := s.IntroSkip.RetryFolder(libID, body.Folder); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// retryFailedIntroSkip setzt ALLE fehlgeschlagenen Jobs zurück auf 'pending'.
func (s *Server) retryFailedIntroSkip(w http.ResponseWriter, _ *http.Request) {
	n, err := s.Store.RetryFailedIntroSkipJobs()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if s.IntroSkip != nil {
		s.IntroSkip.Trigger()
	}
	writeJSON(w, 200, map[string]any{"reset": n})
}
