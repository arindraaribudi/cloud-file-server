package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/example/cos-ftp-server/internal/audit"
)

// OIDCConfig configures SSO login. Role is derived from the ID token's
// "groups" claim: membership in AdminGroup grants the "admin" role,
// ReadonlyGroup grants "readonly". OIDC identities are not persisted to
// admin_users — the session role comes straight from the token on every login.
type OIDCConfig struct {
	IssuerURL        string
	ClientID         string
	ClientSecret     string
	PublicURL        string // this service's public origin, e.g. https://host — the callback path is fixed (oidcCallbackPath)
	AdminGroup       string
	ReadonlyGroup    string
	FrontendRedirect string // browser destination after login, e.g. /auth/callback
}

type oidcAuth struct {
	cfg      OIDCConfig
	oauth2   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// oidcCallbackPath is where the IdP redirects back to after login. Register
// PublicURL + oidcCallbackPath as the redirect URI with the IdP.
const oidcCallbackPath = "/api/v1/auth/oidc/callback"

// ConfigureOIDC enables SSO login by discovering the provider's endpoints.
// Safe to skip: without it, /api/v1/auth/oidc/* return 501.
func (a *API) ConfigureOIDC(ctx context.Context, cfg OIDCConfig) error {
	if cfg.PublicURL == "" {
		return errors.New("admin: OIDCConfig.PublicURL is required")
	}
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return err
	}
	a.oidc = &oidcAuth{
		cfg: cfg,
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  strings.TrimRight(cfg.PublicURL, "/") + oidcCallbackPath,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}
	return nil
}

const oidcStateCookie = "oidc_state"

func (a *API) oidcAuthorize(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.Error(w, "sso is not configured", http.StatusNotImplemented)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: state, Path: "/", HttpOnly: true,
		Secure: a.Sessions.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 300,
	})
	http.Redirect(w, r, a.oidc.oauth2.AuthCodeURL(state), http.StatusFound)
}

func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.Error(w, "sso is not configured", http.StatusNotImplemented)
		return
	}
	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/", MaxAge: -1})

	token, err := a.oidc.oauth2.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "sso exchange failed", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "sso response missing id_token", http.StatusUnauthorized)
		return
	}
	idToken, err := a.oidc.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "sso token invalid", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Username   string   `json:"preferred_username"`
		Email      string   `json:"email"`
		GivenName  string   `json:"given_name"`
		FamilyName string   `json:"family_name"`
		Groups     []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "sso claims invalid", http.StatusUnauthorized)
		return
	}
	username := claims.Username
	if username == "" {
		username = claims.Email
	}
	role := ""
	for _, g := range claims.Groups {
		switch g {
		case a.oidc.cfg.AdminGroup:
			role = "admin"
		case a.oidc.cfg.ReadonlyGroup:
			if role == "" {
				role = "readonly"
			}
		}
	}
	if username == "" || role == "" {
		http.Error(w, "your account is not a member of an authorized group", http.StatusForbidden)
		return
	}
	cookie, err := a.Sessions.Issue(username, role, "oidc", claims.Email, claims.GivenName, claims.FamilyName)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: username, Action: "ADMIN_LOGIN", Success: true, Detail: map[string]any{"role": role, "method": "oidc"}})
	http.SetCookie(w, cookie)
	http.Redirect(w, r, a.oidc.cfg.FrontendRedirect, http.StatusFound)
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
