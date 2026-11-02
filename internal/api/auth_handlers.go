package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/banshee-lab/pgkeeper/internal/auth"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

var oauthErrRe = regexp.MustCompile(`^[a-z_]{1,64}$`)

func (h *Handler) AuthLogin(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "authentication is not enabled")
		return
	}
	u, err := h.Auth.LoginURL(r.Context(), r.URL.Query().Get("redirect_to"))
	if err != nil {
		h.internalError(w, r, "login url", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u, http.StatusFound)
}

func (h *Handler) AuthCallback(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "authentication is not enabled")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		if !oauthErrRe.MatchString(e) { // only the code, sanitised; never error_description
			e = "unknown"
		}
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "identity provider returned error: "+e)
		return
	}
	login, err := h.Auth.HandleCallback(r.Context(), q.Get("code"), q.Get("state"))
	if errors.Is(err, auth.ErrInvalidRequest) {
		h.Logger.Warn("login rejected", "err", err)
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid or expired login attempt")
		return
	}
	if err != nil {
		h.internalError(w, r, "auth callback", err)
		return
	}
	h.Auth.SetCookie(w, login)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, login.RedirectTo, http.StatusFound)
}

func (h *Handler) AuthLogout(w http.ResponseWriter, r *http.Request) {
	if h.Auth != nil {
		if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
			if err := h.Auth.Logout(r.Context(), c.Value); err != nil {
				h.Logger.Warn("logout: delete session", "err", err)
			}
		}
		h.Auth.ClearCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireAuth guards the /api/v1 group. API calls never redirect: the
// frontend navigates to /auth/login on 401.
func (h *Handler) requireAuth(next http.Handler) http.Handler {
	if h.Auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "authentication required")
			return
		}
		s, err := h.Auth.Authenticate(r.Context(), c.Value)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "session missing or expired")
			return
		}
		if err != nil {
			h.internalError(w, r, "authenticate", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), s)))
	})
}