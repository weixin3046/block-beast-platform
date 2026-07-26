package identity

type SessionAudience string

const (
	AudiencePlayer SessionAudience = "player"
	AudienceAdmin  SessionAudience = "admin"
)
