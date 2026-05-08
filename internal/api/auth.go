package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/omdb"
	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/tmdb"
)

const sessionCookieName = "goldfish_session"
const sessionTTL = 30 * 24 * time.Hour

type ctxKey int

const userCtxKey ctxKey = 1

// currentUser liefert den eingeloggten User aus dem Context, oder nil.
func currentUser(r *http.Request) *model.User {
	if u, ok := r.Context().Value(userCtxKey).(*model.User); ok {
		return u
	}
	return nil
}

// withUser baut den Context mit User auf.
func withUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// isPublicPath: Routen, die ohne Login erreichbar sein müssen.
func isPublicPath(path string) bool {
	if path == "/api/auth/login" || path == "/api/auth/logout" ||
		path == "/api/auth/status" || path == "/api/auth/setup" ||
		path == "/api/auth/oidc/login" || path == "/api/auth/oidc/callback" ||
		path == "/api/health" {
		return true
	}
	// Alles außer /api/* → statische Assets, /login etc. kommen vom Server durch
	return !strings.HasPrefix(path, "/api/")
}

// resolveSessionToken: Cookie zuerst, dann Query-Param `?session=…` als
// Fallback. Letzteres ist nötig, damit Cast-Receiver-Geräte (Chromecast,
// FireTV, AirPlay-Targets) die Stream-URL ohne Browser-Cookie laden können.
// Der Token ist derselbe wie im Cookie — wird beim Cast-Klick clientseitig
// aus dem Cookie ausgelesen und an die URL gehängt.
func resolveSessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return r.URL.Query().Get("session")
}

// authMiddleware prüft Cookie/Query-Token, attacht User zum Context.
// Bei Fehler 401 für /api/*.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			// Trotzdem versuchen, User in den Context zu packen (z.B. für /api/auth/status)
			if tok := resolveSessionToken(r); tok != "" {
				if u, _ := s.Store.GetSession(tok); u != nil {
					r = r.WithContext(withUser(r.Context(), u))
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		tok := resolveSessionToken(r)
		if tok == "" {
			writeError(w, 401, "nicht angemeldet")
			return
		}
		u, err := s.Store.GetSession(tok)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if u == nil {
			writeError(w, 401, "Session abgelaufen")
			return
		}
		r = r.WithContext(withUser(r.Context(), u))
		next.ServeHTTP(w, r)
	})
}

// castToken erzeugt einen kurzlebigen Session-Token für Cast-Receiver-Geräte
// (Chromecast, FireTV, …), die über `?session=<token>`-Query-Param
// authentifizieren — sie haben keinen Cookie-Speicher und der reguläre
// Session-Cookie ist HttpOnly (für JS unzugänglich, gut so). 4 h TTL reicht
// für eine Cast-Sitzung; der Token läuft danach automatisch ab.
func (s *Server) castToken(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeError(w, 401, "nicht angemeldet")
		return
	}
	sess, err := s.Store.CreateSession(u.ID, 4*time.Hour)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"token":     sess.Token,
		"expiresAt": sess.ExpiresAt,
	})
}

// requireAdmin: Handler erfordert Admin-Privileg.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if u == nil || !u.IsAdmin {
			writeError(w, 403, "Administrator erforderlich")
			return
		}
		next(w, r)
	}
}

// --- Handler ---

type authStatusResponse struct {
	Setup         bool   `json:"setup"` // wenn true → noch kein User vorhanden
	LoggedIn      bool   `json:"loggedIn"`
	Username      string `json:"username,omitempty"`
	IsAdmin       bool   `json:"isAdmin"`
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	cnt, err := s.Store.CountUsers()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp := authStatusResponse{Setup: cnt == 0}
	if u := currentUser(r); u != nil {
		resp.LoggedIn = true
		resp.Username = u.Username
		resp.IsAdmin = u.IsAdmin
	}
	writeJSON(w, 200, resp)
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiges JSON")
		return
	}
	u, hash, err := s.Store.GetUserByName(body.Username)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if u == nil || !store.VerifyPassword(hash, body.Password) {
		// Konstant 401 um User-Enumeration zu verhindern
		writeError(w, 401, "ungültige Anmeldedaten")
		return
	}
	sess, err := s.Store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.Token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, 200, map[string]any{
		"username": u.Username,
		"isAdmin":  u.IsAdmin,
	})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.Store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
	})
	w.WriteHeader(204)
}

// authSetup erstellt den ersten Admin-User und migriert existierenden Watched/Favorite-State.
// Nur zulässig, solange keine User existieren.
func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	cnt, err := s.Store.CountUsers()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cnt > 0 {
		writeError(w, 409, "Setup bereits abgeschlossen")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// Optionale Konfiguration die das Setup-Fenster sammelt — leere Felder
		// bleiben einfach ungesetzt (User kann später in Settings nachtragen).
		TMDBKey string `json:"tmdbKey"`
		OMDbKey string `json:"omdbKey"`
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
	id, err := s.Store.CreateUser(body.Username, body.Password, true)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Optionale Initial-Settings persistieren — nur wenn nicht-leer.
	if k := strings.TrimSpace(body.TMDBKey); k != "" {
		_ = s.Store.SetSetting("tmdb_api_key", k)
		// TMDB-Client für Enrichment frisch verdrahten (gleiche Logik wie
		// im /api/settings-Endpoint), damit der Worker direkt loslegen kann.
		if s.Enrich != nil {
			s.Enrich.SetClient(tmdb.New(k, "de-DE"))
		}
	}
	if k := strings.TrimSpace(body.OMDbKey); k != "" {
		_ = s.Store.SetSetting("omdb_api_key", k)
		if s.Enrich != nil {
			s.Enrich.SetOMDb(omdb.New(k))
		}
	}
	// Alle existing Libraries für den Admin freigeben (sonst sieht er nichts)
	libs, _ := s.Store.ListLibraries()
	libIDs := make([]int64, 0, len(libs))
	for _, l := range libs {
		libIDs = append(libIDs, l.ID)
	}
	_ = s.Store.SetUserLibraryAccess(id, libIDs)
	// Existierenden Watched/Favorite-State auf den Admin übernehmen
	_ = s.Store.MigrateLegacyItemStateToUser(id)
	// Existierende Playlists dem Admin zuordnen (falls vorhanden, sonst NULL)
	_ = s.Store.MigrateLegacyPlaylistOwners(id)

	// Direkt einloggen
	sess, err := s.Store.CreateSession(id, sessionTTL)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.Token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, 201, map[string]any{
		"username": body.Username,
		"isAdmin":  true,
	})
}
