package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type SessionStore struct{ db *sql.DB }

func NewSessionStore(db *sql.DB) *SessionStore { return &SessionStore{db: db} }

var _ domain.SessionRepository = (*SessionStore)(nil)

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func (s *SessionStore) CreateSession(ctx context.Context, x *domain.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, subject, email, name, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		x.IDHash, x.Subject, nullStr(x.Email), nullStr(x.Name), fmtTime(x.CreatedAt), fmtTime(x.ExpiresAt))
	return err
}

func (s *SessionStore) GetSession(ctx context.Context, idHash string, now time.Time) (*domain.Session, error) {
	var (
		x             domain.Session
		email, name   sql.NullString
		created, expi string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id_hash, subject, email, name, created_at, expires_at FROM sessions WHERE id_hash=? AND expires_at>?`,
		idHash, fmtTime(now)).Scan(&x.IDHash, &x.Subject, &email, &name, &created, &expi)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	x.Email, x.Name = email.String, name.String
	if x.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if x.ExpiresAt, err = parseTime(expi); err != nil {
		return nil, err
	}
	return &x, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
	return err
}

func (s *SessionStore) DeleteExpired(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, fmtTime(now)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_states WHERE expires_at<=?`, fmtTime(now))
	return err
}

func (s *SessionStore) SaveAuthState(ctx context.Context, a *domain.AuthState) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_states (state, nonce, pkce_verifier, redirect_to, expires_at) VALUES (?,?,?,?,?)`,
		a.State, a.Nonce, a.PKCEVerifier, a.RedirectTo, fmtTime(a.ExpiresAt))
	return err
}

// ConsumeAuthState deletes and returns the state atomically (single use).
func (s *SessionStore) ConsumeAuthState(ctx context.Context, state string, now time.Time) (*domain.AuthState, error) {
	a := domain.AuthState{State: state}
	var exp string
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM auth_states WHERE state=? AND expires_at>? RETURNING nonce, pkce_verifier, redirect_to, expires_at`,
		state, fmtTime(now)).Scan(&a.Nonce, &a.PKCEVerifier, &a.RedirectTo, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if a.ExpiresAt, err = parseTime(exp); err != nil {
		return nil, err
	}
	return &a, nil
}