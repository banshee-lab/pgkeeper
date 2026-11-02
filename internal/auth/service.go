package auth

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

const (
	CookieName = "pgkeeper_session"
	stateTTL   = 10 * time.Minute
)

// ErrInvalidRequest marks failures caused by the client/IdP flow (bad state,
// bad code, bad token, nonce mismatch). Handlers map it to 400.
var ErrInvalidRequest = errors.New("invalid login request")

type Service struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	sessions domain.SessionRepository
	cfg      config.AuthConfig
	now      func() time.Time
}

// New performs OIDC discovery (3 attempts, linear backoff). ctx must live as
// long as the service: go-oidc reuses it to fetch the IdP signing keys.
func New(ctx context.Context, cfg config.AuthConfig, sessions domain.SessionRepository) (*Service, error) {
	var (
		provider *oidc.Provider
		err      error
	)
	for attempt := 1; ; attempt++ {
		provider, err = oidc.NewProvider(ctx, cfg.Issuer)
		if err == nil {
			break
		}
		if attempt == 3 {
			return nil, fmt.Errorf("oidc discovery for %s failed after %d attempts: %w", cfg.Issuer, attempt, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}

	scopes := slices.Clone(cfg.Scopes)
	if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append(scopes, oidc.ScopeOpenID)
	}
	cfg.PostLoginRedirect = cmp.Or(cfg.PostLoginRedirect, "/")

	return &Service{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.Secret.Reveal(),
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       scopes,
		},
		sessions: sessions,
		cfg:      cfg,
		now:      time.Now,
	}, nil
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LoginURL stores a fresh single-use auth state and returns the IdP URL.
func (s *Service) LoginURL(ctx context.Context, redirectTo string) (string, error) {
	state, err := randToken(32)
	if err != nil {
		return "", err
	}
	nonce, err := randToken(32)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	err = s.sessions.SaveAuthState(ctx, &domain.AuthState{
		State:        state,
		Nonce:        nonce,
		PKCEVerifier: verifier,
		RedirectTo:   SafeRedirect(redirectTo, s.cfg.PostLoginRedirect),
		ExpiresAt:    s.now().Add(stateTTL).UTC(),
	})
	if err != nil {
		return "", err
	}
	return s.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

type Login struct {
	Token      string // cookie value (never stored, only its hash)
	RedirectTo string
	ExpiresAt  time.Time
}

// HandleCallback validates state, exchanges the code (PKCE), verifies the ID
// token + nonce and creates a server-side session.
func (s *Service) HandleCallback(ctx context.Context, code, state string) (*Login, error) {
	if code == "" || state == "" {
		return nil, fmt.Errorf("%w: missing code or state", ErrInvalidRequest)
	}
	st, err := s.sessions.ConsumeAuthState(ctx, state, s.now())
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("%w: unknown, used or expired state", ErrInvalidRequest)
	}
	if err != nil {
		return nil, err
	}

	tok, err := s.oauth.Exchange(ctx, code, oauth2.VerifierOption(st.PKCEVerifier))
	if err != nil {
		return nil, fmt.Errorf("%w: code exchange: %v", ErrInvalidRequest, err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, fmt.Errorf("%w: no id_token in response", ErrInvalidRequest)
	}
	idTok, err := s.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("%w: id_token: %v", ErrInvalidRequest, err)
	}
	if subtle.ConstantTimeCompare([]byte(idTok.Nonce), []byte(st.Nonce)) != 1 {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrInvalidRequest)
	}
	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idTok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrInvalidRequest, err)
	}

	token, err := randToken(32)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	exp := now.Add(s.cfg.SessionTTL)
	err = s.sessions.CreateSession(ctx, &domain.Session{
		IDHash:    hashToken(token),
		Subject:   idTok.Subject,
		Email:     claims.Email,
		Name:      cmp.Or(claims.Name, claims.PreferredUsername),
		CreatedAt: now,
		ExpiresAt: exp,
	})
	if err != nil {
		return nil, err
	}
	return &Login{Token: token, RedirectTo: st.RedirectTo, ExpiresAt: exp}, nil
}

// Authenticate resolves a cookie value to a session (domain.ErrNotFound if
// missing or expired).
func (s *Service) Authenticate(ctx context.Context, token string) (*domain.Session, error) {
	return s.sessions.GetSession(ctx, hashToken(token), s.now())
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.DeleteSession(ctx, hashToken(token))
}

func (s *Service) SetCookie(w http.ResponseWriter, l *Login) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    l.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		Expires:  l.ExpiresAt,
	})
}

func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		MaxAge:   -1,
	})
}

// RunCleanup deletes expired sessions/auth states every `every` until ctx ends.
func (s *Service) RunCleanup(ctx context.Context, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.sessions.DeleteExpired(ctx, s.now()); err != nil && ctx.Err() == nil {
				log.Warn("session cleanup failed", "err", err)
			}
		}
	}
}

type ctxKey struct{}

func WithSession(ctx context.Context, s *domain.Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// SessionFrom returns nil when no session is attached (auth disabled).
func SessionFrom(ctx context.Context) *domain.Session {
	s, _ := ctx.Value(ctxKey{}).(*domain.Session)
	return s
}