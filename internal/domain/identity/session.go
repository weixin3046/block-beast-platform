package identity

import "context"

type SessionValidator interface {
	ValidateSession(context.Context, AccessTokenClaims) error
}

type SessionAudience string

const (
	AudiencePlayer SessionAudience = "player"
	AudienceAdmin  SessionAudience = "admin"
)
