package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/boernie77/goldfish/internal/omdb"
	"github.com/boernie77/goldfish/internal/tmdb"
)

// getAutoScan liefert die aktuellen Auto-Scan-Einstellungen.
func (s *Server) getAutoScan(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, autoScanSettingsFromStore(s.Store))
}

// putAutoScan speichert die Auto-Scan-Einstellungen.
func (s *Server) putAutoScan(w http.ResponseWriter, r *http.Request) {
	var body autoScanDTO
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	// Schedule validieren
	if body.Enabled && body.Schedule != "" {
		parts := strings.Split(body.Schedule, ":")
		valid := false
		switch {
		case len(parts) == 3 && parts[0] == "daily":
			_, e1 := strconv.Atoi(parts[1])
			_, e2 := strconv.Atoi(parts[2])
			valid = e1 == nil && e2 == nil
		case len(parts) == 2 && parts[0] == "every":
			raw := strings.TrimSuffix(parts[1], "h")
			n, e := strconv.Atoi(raw)
			valid = e == nil && n >= 1 && n <= 23
		case len(parts) == 4 && parts[0] == "weekly":
			_, e1 := strconv.Atoi(parts[2])
			_, e2 := strconv.Atoi(parts[3])
			valid = e1 == nil && e2 == nil
		}
		if !valid {
			writeError(w, 400, "ungültiges Schedule-Format (daily:HH:MM | every:Nh | weekly:DOW:HH:MM)")
			return
		}
	}
	enabledStr := "false"
	if body.Enabled {
		enabledStr = "true"
	}
	_ = s.Store.SetSetting("auto_scan_enabled", enabledStr)
	if body.Schedule != "" {
		_ = s.Store.SetSetting("auto_scan_schedule", body.Schedule)
	}
	_ = s.Store.SetSetting("auto_scan_library_id", strconv.Itoa(body.LibraryID))
	writeJSON(w, 200, autoScanSettingsFromStore(s.Store))
}

type settingsDTO struct {
	BufferSeconds        int    `json:"bufferSeconds"`
	// Minimaler Vorpuffer (Sekunden), den der Client aufbauen muss, bevor die
	// Wiedergabe automatisch startet. 0 = sofort starten (Video.js-Default).
	StartBufferSeconds   int    `json:"startBufferSeconds"`
	TrickplayIntervalSec int    `json:"trickplayIntervalSec"`
	TMDBKey              string `json:"tmdbKey"`
	TMDBConfigured       bool   `json:"tmdbConfigured"`
	OMDbKey              string `json:"omdbKey"`
	OMDbConfigured       bool   `json:"omdbConfigured"`
	HwaccelMode          string `json:"hwaccelMode"` // auto | vaapi | nvenc | software
	// Auto-Rename-Hook im confirmItemMetadata-Handler. Wenn true wird beim
	// Bestaetigen eines Films (tmdb_type=movie) die Datei zu „Title (Year).ext"
	// umbenannt + in rename_history protokolliert. Default: false.
	AutoRenameConfirmedMovies bool `json:"autoRenameConfirmedMovies"`
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	bs, _ := s.Store.GetSetting("buffer_seconds", "30")
	n, _ := strconv.Atoi(bs)
	if n <= 0 {
		n = 30
	}
	sbs, _ := s.Store.GetSetting("start_buffer_seconds", "0")
	sbn, _ := strconv.Atoi(sbs)
	if sbn < 0 || sbn > 120 {
		sbn = 0
	}
	ti, _ := s.Store.GetSetting("trickplay_interval_sec", "10")
	tn, _ := strconv.Atoi(ti)
	if tn < 2 || tn > 60 {
		tn = 10
	}
	key, _ := s.Store.GetSetting("tmdb_api_key", "")
	okey, _ := s.Store.GetSetting("omdb_api_key", "")
	hw, _ := s.Store.GetSetting("hwaccel_mode", "auto")
	arn, _ := s.Store.GetSetting(renameSettingKey, "")
	writeJSON(w, 200, settingsDTO{
		BufferSeconds:             n,
		StartBufferSeconds:        sbn,
		TrickplayIntervalSec:      tn,
		TMDBKey:                   "",
		TMDBConfigured:            key != "",
		OMDbKey:                   "",
		OMDbConfigured:            okey != "",
		HwaccelMode:               hw,
		AutoRenameConfirmedMovies: arn == "true" || arn == "1",
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsDTO
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	if body.BufferSeconds < 5 || body.BufferSeconds > 600 {
		writeError(w, 400, "bufferSeconds muss zwischen 5 und 600 liegen")
		return
	}
	if err := s.Store.SetSetting("buffer_seconds", strconv.Itoa(body.BufferSeconds)); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// StartBufferSeconds: 0-120 s. 0 = deaktiviert (sofort starten).
	if body.StartBufferSeconds < 0 || body.StartBufferSeconds > 120 {
		writeError(w, 400, "startBufferSeconds muss zwischen 0 und 120 liegen")
		return
	}
	if err := s.Store.SetSetting("start_buffer_seconds", strconv.Itoa(body.StartBufferSeconds)); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if body.TrickplayIntervalSec > 0 {
		if body.TrickplayIntervalSec < 2 || body.TrickplayIntervalSec > 60 {
			writeError(w, 400, "trickplayIntervalSec muss zwischen 2 und 60 liegen")
			return
		}
		if err := s.Store.SetSetting("trickplay_interval_sec", strconv.Itoa(body.TrickplayIntervalSec)); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	// TMDB-Key nur überschreiben, wenn im Body ein nicht-leerer Wert kommt.
	if body.TMDBKey == "__clear__" {
		_ = s.Store.SetSetting("tmdb_api_key", "")
		if s.Enrich != nil {
			s.Enrich.SetClient(tmdb.New("", ""))
		}
	} else if body.TMDBKey != "" {
		if err := s.Store.SetSetting("tmdb_api_key", body.TMDBKey); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if s.Enrich != nil {
			s.Enrich.SetClient(tmdb.New(body.TMDBKey, "de-DE"))
			s.Enrich.Trigger()
		}
	}
	// HWAccel-Modus setzen + live auf den Manager + Trickplay-Worker anwenden.
	if body.HwaccelMode != "" {
		switch body.HwaccelMode {
		case "auto", "vaapi", "nvenc", "software":
			if err := s.Store.SetSetting("hwaccel_mode", body.HwaccelMode); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			s.HW.ApplySelection(body.HwaccelMode)
			if s.Playback != nil {
				s.Playback.SetHWAccel(s.HW)
			}
			if s.Trickplay != nil {
				s.Trickplay.SetBackend(string(s.HW.Selected))
			}
		default:
			writeError(w, 400, "hwaccelMode muss auto|vaapi|nvenc|software sein")
			return
		}
	}
	// OMDb-Key analog
	if body.OMDbKey == "__clear__" {
		_ = s.Store.SetSetting("omdb_api_key", "")
		if s.Enrich != nil {
			s.Enrich.SetOMDb(omdb.New(""))
		}
	} else if body.OMDbKey != "" {
		if err := s.Store.SetSetting("omdb_api_key", body.OMDbKey); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if s.Enrich != nil {
			s.Enrich.SetOMDb(omdb.New(body.OMDbKey))
		}
	}
	key, _ := s.Store.GetSetting("tmdb_api_key", "")
	okey, _ := s.Store.GetSetting("omdb_api_key", "")
	// aktuellen (gerade gespeicherten) Trickplay-Wert aus DB zurückgeben
	tiStr, _ := s.Store.GetSetting("trickplay_interval_sec", "10")
	ti, _ := strconv.Atoi(tiStr)
	if ti < 2 || ti > 60 {
		ti = 10
	}
	// Auto-Rename-Setting persistieren ("true" / "false" als String).
	arnVal := "false"
	if body.AutoRenameConfirmedMovies {
		arnVal = "true"
	}
	if err := s.Store.SetSetting(renameSettingKey, arnVal); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	hw, _ := s.Store.GetSetting("hwaccel_mode", "auto")
	writeJSON(w, 200, settingsDTO{
		BufferSeconds:             body.BufferSeconds,
		StartBufferSeconds:        body.StartBufferSeconds,
		TrickplayIntervalSec:      ti,
		TMDBConfigured:            key != "",
		OMDbConfigured:            okey != "",
		HwaccelMode:               hw,
		AutoRenameConfirmedMovies: body.AutoRenameConfirmedMovies,
	})
}
