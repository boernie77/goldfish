package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// TestPrivacyRedirect: /datenschutz.html muss OHNE Login auf die allgemeine
// Datenschutzerklärung weiterleiten — App Store Connect und Amazon verweisen
// auf diese Adresse (404 bis 1.4.54, gefunden 2026-09-29).
func TestPrivacyRedirect(t *testing.T) {
	s := &Server{WebFS: fstest.MapFS{"index.html": {Data: []byte("x")}}}
	h := s.Router()
	for path, want := range map[string]string{
		"/datenschutz.html":      "https://boernie77.github.io/goldfish/privacy.html",
		"/account-deletion.html": "https://boernie77.github.io/goldfish/account-deletion.html",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s: Code %d, Location %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}
