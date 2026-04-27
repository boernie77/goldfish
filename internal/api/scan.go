package api

import (
	"net/http"
	"time"
)

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lib, err := s.Store.GetLibrary(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if lib == nil {
		writeError(w, 404, "Bibliothek nicht gefunden")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	folder := r.URL.Query().Get("folder")
	if err := s.Scanner.Start(*lib, force, folder); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"status": "started", "force": force, "folder": folder})
}

// startScanAll scannt alle Bibliotheken nacheinander. Setzt Force wenn ?force=true.
func (s *Server) startScanAll(w http.ResponseWriter, r *http.Request) {
	libs, err := s.Store.ListLibraries()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	force := r.URL.Query().Get("force") == "true"
	go func() {
		for _, l := range libs {
			if err := s.Scanner.Start(l, force, ""); err != nil {
				// Scanner gibt Fehler zurück wenn bereits läuft — kurz warten
				continue
			}
			// Bis dieser Scan fertig ist warten, dann nächsten starten
			for {
				st := s.Scanner.Status()
				if !st.Running {
					break
				}
				time.Sleep(2 * time.Second)
			}
		}
	}()
	writeJSON(w, 202, map[string]any{"status": "started", "libraries": len(libs), "force": force})
}

func (s *Server) scanStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.Scanner.Status())
}

func (s *Server) cancelScan(w http.ResponseWriter, _ *http.Request) {
	s.Scanner.Cancel()
	w.WriteHeader(204)
}
