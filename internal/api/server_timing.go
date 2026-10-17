package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// serverTimingWriter setzt den Server-Timing-Header kurz vor dem ersten
// Schreiben, damit er die reine Handler-Zeit enthält.
type serverTimingWriter struct {
	http.ResponseWriter
	start time.Time
	sent  bool
}

func (w *serverTimingWriter) WriteHeader(code int) {
	if !w.sent {
		w.sent = true
		ms := float64(time.Since(w.start).Microseconds()) / 1000
		w.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.1f", ms))
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *serverTimingWriter) Write(b []byte) (int, error) {
	if !w.sent {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// serverTiming misst die Serverzeit der Such-Endpoints (Diagnose der
// Suchlatenz in den Apps: Server vs. Netz).
func serverTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isSearch := strings.HasPrefix(r.URL.Path, "/api/search/") ||
			(r.URL.Path == "/api/items" && r.URL.Query().Get("q") != "")
		if !isSearch {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&serverTimingWriter{ResponseWriter: w, start: time.Now()}, r)
	})
}
