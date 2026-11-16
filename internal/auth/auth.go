// Package auth implements gomon's web authentication: a built-in admin
// account (created on first run), OpenID Connect single sign-on, server-side
// sessions and API keys for automation.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"gomon/internal/config"
	"gomon/internal/store"
)

const (
	sessionCookie = "gomon_session"
	oidcCookie    = "gomon_oidc"
	apiKeyHeader  = "X-API-Key"
)

type User struct {
	Username string `json:"username"`
	Method   string `json:"method"` // password, oidc, apikey
}

type ctxKey struct{}

func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

type Auth struct {
	store  *store.Store
	cfg    config.OIDCConfig
	log    *slog.Logger
	secure bool
	ttl    time.Duration

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

func New(st *store.Store, cfg *config.Config, log *slog.Logger) *Auth {
	ttl := time.Duration(cfg.HTTP.SessionHours) * time.Hour
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Auth{store: st, cfg: cfg.OIDC, log: log, ttl: ttl,
		secure: cfg.HTTP.SecureCookies || strings.HasPrefix(cfg.HTTP.BaseURL, "https://")}
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func HashPassword(pw string) (string, error) {
	if len(pw) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// Middleware authenticates API requests by session cookie or API key.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if k := r.Header.Get(apiKeyHeader); k != "" {
			name, err := a.store.CheckAPIKey(r.Context(), hashToken(k))
			if err == nil {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &User{Username: "apikey:" + name, Method: "apikey"})))
				return
			}
			writeErr(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeErr(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		user, method, err := a.store.GetSession(r.Context(), hashToken(c.Value))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session expired")
			return
		}
		// CSRF: state-changing requests must be JSON (forces a CORS preflight cross-site).
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodDelete {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &User{Username: user, Method: method})))
	})
}

func (a *Auth) startSession(w http.ResponseWriter, r *http.Request, user, method string) error {
	tok := randToken(32)
	exp := time.Now().Add(a.ttl)
	if err := a.store.CreateSession(r.Context(), hashToken(tok), user, method, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
		Secure: a.secure || r.TLS != nil, SameSite: http.SameSiteLaxMode})
	return nil
}

// Info reports what the login page should offer.
func (a *Auth) Info(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.UserCount(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setupRequired": n == 0 && !(a.cfg.Enabled && a.cfg.DisableBuiltinLogin),
		"oidcEnabled":   a.cfg.Enabled,
		"oidcLabel":     a.cfg.ButtonLabel,
		"builtinLogin":  !(a.cfg.Enabled && a.cfg.DisableBuiltinLogin),
	})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Setup creates the first admin account.
func (a *Auth) Setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || strings.TrimSpace(c.Username) == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
	n, err := a.store.UserCount(r.Context())
	if err != nil || n > 0 {
		writeErr(w, http.StatusForbidden, "setup already completed")
		return
	}
	hash, err := HashPassword(c.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.CreateUser(r.Context(), strings.TrimSpace(c.Username), hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.startSession(w, r, c.Username, "password"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, User{Username: c.Username, Method: "password"})
}

func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Enabled && a.cfg.DisableBuiltinLogin {
		writeErr(w, http.StatusForbidden, "password login is disabled")
		return
	}
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	hash, err := a.store.PasswordHash(r.Context(), c.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		time.Sleep(500 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := a.startSession(w, r, c.Username, "password"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, User{Username: c.Username, Method: "password"})
}

func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *Auth) Me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, UserFrom(r.Context()))
}

func (a *Auth) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	if u == nil || u.Method != "password" {
		writeErr(w, http.StatusForbidden, "only password accounts can change their password")
		return
	}
	var body struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	hash, err := a.store.PasswordHash(r.Context(), u.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Current)) != nil {
		writeErr(w, http.StatusUnauthorized, "current password is wrong")
		return
	}
	nh, err := HashPassword(body.New)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.SetPassword(r.Context(), u.Username, nh); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// NewAPIKey creates a key and returns its plaintext once.
func (a *Auth) NewAPIKey(ctx context.Context, name string) (int64, string, error) {
	key := "gmn_" + randToken(24)
	id, err := a.store.CreateAPIKey(ctx, name, hashToken(key))
	return id, key, err
}

// ---- OIDC ----

func (a *Auth) oidcSetup(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return nil
	}
	if !a.cfg.Enabled || a.cfg.Issuer == "" || a.cfg.ClientID == "" {
		return errors.New("OIDC is not configured")
	}
	p, err := oidc.NewProvider(ctx, a.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("OIDC discovery: %w", err)
	}
	scopes := a.cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	a.provider = p
	a.verifier = p.Verifier(&oidc.Config{ClientID: a.cfg.ClientID})
	a.oauth = &oauth2.Config{ClientID: a.cfg.ClientID, ClientSecret: a.cfg.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: a.cfg.RedirectURL, Scopes: scopes}
	return nil
}

func (a *Auth) redirectURL(r *http.Request) string {
	if a.cfg.RedirectURL != "" {
		return a.cfg.RedirectURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/auth/oidc/callback"
}

func (a *Auth) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	if err := a.oidcSetup(r.Context()); err != nil {
		a.log.Error("oidc", "err", err)
		http.Redirect(w, r, "/login?error="+urlEscape(err.Error()), http.StatusFound)
		return
	}
	state, nonce, verifier := randToken(24), randToken(24), oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: state + "." + nonce + "." + verifier, Path: "/api/auth/oidc",
		MaxAge: 600, HttpOnly: true, Secure: a.secure || r.TLS != nil, SameSite: http.SameSiteLaxMode})
	cfg := *a.oauth
	cfg.RedirectURL = a.redirectURL(r)
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func urlEscape(s string) string {
	return strings.NewReplacer(" ", "+", "&", "%26", "#", "%23", "?", "%3F").Replace(s)
}

func (a *Auth) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string, err error) {
		a.log.Warn("oidc login failed", "reason", msg, "err", err)
		http.Redirect(w, r, "/login?error="+urlEscape(msg), http.StatusFound)
	}
	if err := a.oidcSetup(r.Context()); err != nil {
		fail("OIDC is not available", err)
		return
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		fail("login session expired, try again", err)
		return
	}
	parts := strings.SplitN(c.Value, ".", 3)
	if len(parts) != 3 || r.URL.Query().Get("state") != parts[0] {
		fail("invalid login state", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/api/auth/oidc", MaxAge: -1})
	if e := r.URL.Query().Get("error"); e != "" {
		fail("identity provider error: "+e, nil)
		return
	}
	cfg := *a.oauth
	cfg.RedirectURL = a.redirectURL(r)
	tok, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(parts[2]))
	if err != nil {
		fail("token exchange failed", err)
		return
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		fail("no id_token returned", nil)
		return
	}
	idt, err := a.verifier.Verify(r.Context(), raw)
	if err != nil {
		fail("invalid id_token", err)
		return
	}
	if idt.Nonce != parts[1] {
		fail("invalid nonce", nil)
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		fail("invalid claims", err)
		return
	}
	// Merge userinfo claims (groups are often only there).
	if ui, err := a.provider.UserInfo(r.Context(), oauth2.StaticTokenSource(tok)); err == nil {
		var extra map[string]any
		if ui.Claims(&extra) == nil {
			for k, v := range extra {
				if _, ok := claims[k]; !ok {
					claims[k] = v
				}
			}
		}
	}
	if len(a.cfg.AllowedGroups) > 0 && !inGroups(claims[a.cfg.GroupsClaim], a.cfg.AllowedGroups) {
		fail("you are not in an allowed group", nil)
		return
	}
	user := ""
	for _, k := range []string{"preferred_username", "email", "name", "sub"} {
		if v, ok := claims[k].(string); ok && v != "" {
			user = v
			break
		}
	}
	if err := a.startSession(w, r, user, "oidc"); err != nil {
		fail("cannot create session", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func inGroups(claim any, allowed []string) bool {
	var groups []string
	switch v := claim.(type) {
	case []any:
		for _, g := range v {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
	case string:
		groups = strings.Split(v, ",")
	}
	for _, g := range groups {
		for _, a := range allowed {
			if strings.EqualFold(strings.TrimSpace(g), a) {
				return true
			}
		}
	}
	return false
}
