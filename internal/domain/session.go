package domain

import "time"

type Session struct {
	IDHash, Subject, Email, Name string
	CreatedAt, ExpiresAt         time.Time
}

type AuthState struct {
	State, Nonce, PKCEVerifier, RedirectTo string
	ExpiresAt                              time.Time
}