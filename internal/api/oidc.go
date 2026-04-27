package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig liest aus Env-Variablen. Leer = SSO deaktiviert.
type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (c OIDCConfig) Enabled() bool {
	return c.IssuerURL != "" && c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

// OIDCRuntime kapselt lazy-init des OIDC-Providers + In-Memory-State-Store.
type OIDCRuntime struct {
	cfg OIDCConfig

	once     sync.Once
	initErr  error
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   *oauth2.Config

	statesMu sync.Mutex
	states   map[string]oidcState
}

type oidcState struct {
	nonce        string
	codeVerifier string
	created      time.Time
}

const oidcStateTTL = 5 * time.Minute

// NewOIDCRuntime baut die Runtime, ohne den Discovery-Call zu machen — der
// passiert lazy beim ersten /login. So crashed der Server nicht beim Start,
// wenn Authentik noch nicht erreichbar ist.
func NewOIDCRuntime(cfg OIDCConfig) *OIDCRuntime {
	return &OIDCRuntime{
		cfg:    cfg,
		states: make(map[string]oidcState),
	}
}

func (r *OIDCRuntime) lazyInit(ctx context.Context) error {
	r.once.Do(func() {
		if !r.cfg.Enabled() {
			r.initErr = errors.New("OIDC nicht konfiguriert (OIDC_ISSUER_URL/CLIENT_ID/CLIENT_SECRET/REDIRECT_URL fehlt)")
			return
		}
		issuer := strings.TrimRight(r.cfg.IssuerURL, "/")
		provider, err := oidc.NewProvider(ctx, issuer)
		if err != nil {
			r.initErr = fmt.Errorf("oidc discover: %w", err)
			return
		}
		r.provider = provider
		r.verifier = provider.Verifier(&oidc.Config{ClientID: r.cfg.ClientID})
		r.oauth2 = &oauth2.Config{
			ClientID:     r.cfg.ClientID,
			ClientSecret: r.cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  r.cfg.RedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
	})
	if r.initErr != nil {
		// Reset Once damit der nächste Versuch erneut probieren kann
		r.once = sync.Once{}
		return r.initErr
	}
	return nil
}

func (r *OIDCRuntime) gcStates() {
	r.statesMu.Lock()
	defer r.statesMu.Unlock()
	now := time.Now()
	for k, v := range r.states {
		if now.Sub(v.created) > oidcStateTTL {
			delete(r.states, k)
		}
	}
}

func randURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- Handlers ---

// oidcLogin: GET /api/auth/oidc/login → 302 zu Authentik
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	rt := s.OIDC
	if rt == nil || !rt.cfg.Enabled() {
		writeError(w, 503, "SSO nicht konfiguriert")
		return
	}
	if err := rt.lazyInit(r.Context()); err != nil {
		writeError(w, 502, err.Error())
		return
	}
	state, err := randURLSafe(32)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	nonce, err := randURLSafe(32)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	codeVerifier, err := randURLSafe(64)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	rt.statesMu.Lock()
	rt.states[state] = oidcState{nonce: nonce, codeVerifier: codeVerifier, created: time.Now()}
	rt.statesMu.Unlock()
	rt.gcStates()

	authURL := rt.oauth2.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(codeVerifier),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// oidcCallback: GET /api/auth/oidc/callback?code=...&state=...
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	rt := s.OIDC
	redirectFail := func(msg string) {
		http.Redirect(w, r, "/login.html?sso_error="+url.QueryEscape(msg), http.StatusFound)
	}

	if rt == nil || !rt.cfg.Enabled() {
		redirectFail("SSO nicht konfiguriert")
		return
	}
	if err := rt.lazyInit(r.Context()); err != nil {
		redirectFail(err.Error())
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		redirectFail("Ungültiger Callback (state/code fehlt)")
		return
	}

	rt.statesMu.Lock()
	si, ok := rt.states[state]
	delete(rt.states, state)
	rt.statesMu.Unlock()
	if !ok {
		redirectFail("State abgelaufen oder unbekannt")
		return
	}

	token, err := rt.oauth2.Exchange(r.Context(), code, oauth2.VerifierOption(si.codeVerifier))
	if err != nil {
		redirectFail("Token-Tausch fehlgeschlagen: " + err.Error())
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		redirectFail("Antwort enthält kein id_token")
		return
	}
	idToken, err := rt.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		redirectFail("ID-Token ungültig: " + err.Error())
		return
	}
	if idToken.Nonce != si.nonce {
		redirectFail("Nonce-Mismatch")
		return
	}

	var claims struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		redirectFail("Claims unparsbar: " + err.Error())
		return
	}
	if claims.Sub == "" {
		redirectFail("Subject-Claim leer")
		return
	}

	user, err := s.Store.GetUserByOIDCSubject(claims.Sub)
	if err != nil {
		redirectFail("DB-Fehler: " + err.Error())
		return
	}
	if user == nil {
		// Fallback: match per Username (case-insensitive)
		candidate := claims.PreferredUsername
		if candidate == "" {
			candidate = claims.Email
		}
		if candidate != "" {
			user, _ = s.Store.GetUserByNameCI(candidate)
		}
		if user == nil {
			redirectFail("Kein Goldfish-Konto für " + claims.Email + ". Admin muss verknüpfen.")
			return
		}
		if err := s.Store.SetUserOIDCSubject(user.ID, claims.Sub); err != nil {
			redirectFail("Verknüpfung fehlgeschlagen: " + err.Error())
			return
		}
	}

	sess, err := s.Store.CreateSession(user.ID, sessionTTL)
	if err != nil {
		redirectFail("Session-Erstellung fehlgeschlagen")
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
	http.Redirect(w, r, "/", http.StatusFound)
}
