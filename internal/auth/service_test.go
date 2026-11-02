package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/auth/authtest"
	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

func newSvc(t *testing.T) (*Service, *authtest.IdP) {
	t.Helper()
	idp := authtest.NewIdP(t, "pgkeeper")
	svc, err := New(context.Background(), config.AuthConfig{
		Issuer: idp.URL, ClientID: "pgkeeper", Secret: "s3cret",
		RedirectURL: "http://app.test/auth/callback",
		Scopes:      []string{"openid", "profile", "email"},
		SessionTTL:  time.Hour, PostLoginRedirect: "/home",
	}, authtest.NewMemSessions())
	if err != nil {
		t.Fatal(err)
	}
	return svc, idp
}

func login(t *testing.T, svc *Service, idp *authtest.IdP, redirectTo string) (code, state string) {
	t.Helper()
	loc, err := svc.LoginURL(context.Background(), redirectTo)
	if err != nil {
		t.Fatal(err)
	}
	return idp.Authorize(t, loc)
}

func TestFullLoginAndLogout(t *testing.T) {
	svc, idp := newSvc(t)
	ctx := context.Background()
	code, state := login(t, svc, idp, "/backups")

	l, err := svc.HandleCallback(ctx, code, state)
	if err != nil {
		t.Fatal(err)
	}
	if l.RedirectTo != "/backups" {
		t.Errorf("redirect = %q", l.RedirectTo)
	}
	s, err := svc.Authenticate(ctx, l.Token)
	if err != nil || s.Subject != "user-1" || s.Email != "u@example.com" || s.Name != "Test User" {
		t.Fatalf("session = %+v, err = %v", s, err)
	}
	if err := svc.Logout(ctx, l.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, l.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after logout err = %v", err)
	}
}

func TestWrongState(t *testing.T) {
	svc, idp := newSvc(t)
	code, _ := login(t, svc, idp, "/")
	if _, err := svc.HandleCallback(context.Background(), code, "bogus"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestReplayedState(t *testing.T) {
	svc, idp := newSvc(t)
	code, state := login(t, svc, idp, "/")
	if _, err := svc.HandleCallback(context.Background(), code, state); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HandleCallback(context.Background(), code, state); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("replay err = %v", err)
	}
}

func TestBadNonce(t *testing.T) {
	svc, idp := newSvc(t)
	idp.WrongNonce.Store(true)
	code, state := login(t, svc, idp, "/")
	if _, err := svc.HandleCallback(context.Background(), code, state); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestExpiredIDToken(t *testing.T) {
	svc, idp := newSvc(t)
	idp.Expired.Store(true)
	code, state := login(t, svc, idp, "/")
	if _, err := svc.HandleCallback(context.Background(), code, state); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	svc, idp := newSvc(t)
	code, state := login(t, svc, idp, "/")
	l, err := svc.HandleCallback(context.Background(), code, state)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := svc.Authenticate(context.Background(), l.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRedirectIsNeutralised(t *testing.T) {
	svc, idp := newSvc(t)
	for _, evil := range []string{"//evil.com", "https://evil", `/\evil`} {
		code, state := login(t, svc, idp, evil)
		l, err := svc.HandleCallback(context.Background(), code, state)
		if err != nil {
			t.Fatal(err)
		}
		if l.RedirectTo != "/home" {
			t.Errorf("%q -> %q, want /home", evil, l.RedirectTo)
		}
	}
}