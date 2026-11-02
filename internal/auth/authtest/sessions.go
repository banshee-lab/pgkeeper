// Package authtest provides test doubles for the auth flow: an in-memory
// SessionRepository and a minimal OIDC provider.
package authtest

import (
	"context"
	"sync"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type MemSessions struct {
	mu       sync.Mutex
	sessions map[string]domain.Session
	states   map[string]domain.AuthState
}

var _ domain.SessionRepository = (*MemSessions)(nil)

func NewMemSessions() *MemSessions {
	return &MemSessions{sessions: map[string]domain.Session{}, states: map[string]domain.AuthState{}}
}

func (m *MemSessions) CreateSession(_ context.Context, s *domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.IDHash] = *s
	return nil
}

func (m *MemSessions) GetSession(_ context.Context, idHash string, now time.Time) (*domain.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[idHash]
	if !ok || !s.ExpiresAt.After(now) {
		return nil, domain.ErrNotFound
	}
	return &s, nil
}

func (m *MemSessions) DeleteSession(_ context.Context, idHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, idHash)
	return nil
}

func (m *MemSessions) DeleteExpired(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if !s.ExpiresAt.After(now) {
			delete(m.sessions, k)
		}
	}
	for k, s := range m.states {
		if !s.ExpiresAt.After(now) {
			delete(m.states, k)
		}
	}
	return nil
}

func (m *MemSessions) SaveAuthState(_ context.Context, a *domain.AuthState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[a.State] = *a
	return nil
}

func (m *MemSessions) ConsumeAuthState(_ context.Context, state string, now time.Time) (*domain.AuthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.states[state]
	if !ok {
		return nil, domain.ErrNotFound
	}
	delete(m.states, state)
	if !a.ExpiresAt.After(now) {
		return nil, domain.ErrNotFound
	}
	return &a, nil
}