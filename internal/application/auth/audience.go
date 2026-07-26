package auth

import "github.com/block-beast/platform/internal/domain/identity"

type SessionAudience = identity.SessionAudience

const (
	AudiencePlayer = identity.AudiencePlayer
	AudienceAdmin  = identity.AudienceAdmin
)

func audienceAllowsRoles(audience SessionAudience, roles []string) bool {
	if audience == AudienceAdmin {
		return adminLoginAllowed(roles)
	}
	return playerLoginAllowed(roles)
}
