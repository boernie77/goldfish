package api

import (
	"net/http"
	"strconv"

	"github.com/boernie77/goldfish/internal/store"
)

// activityLogList: GET /api/admin/activity-log?category=&beforeId=&limit=
// Admin-only. Siehe internal/store/activity_log.go für die Filter-Semantik.
func (s *Server) activityLogList(w http.ResponseWriter, r *http.Request) {
	f := store.ActivityLogFilter{
		Category: r.URL.Query().Get("category"),
		Username: r.URL.Query().Get("username"),
	}
	if v := r.URL.Query().Get("beforeId"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.BeforeID = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	entries, err := s.Store.ListActivityLog(f)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"entries": entries})
}
