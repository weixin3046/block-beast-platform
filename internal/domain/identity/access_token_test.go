package identity

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var accessTokenTestSecret = []byte("development-secret-must-have-32-bytes")

func TestPermanentAccessTokenRequiresSessionAndSurvivesTime(t *testing.T) {
	now := time.Now().UTC()
	token, err := IssueAccessToken(accessTokenTestSecret, "player-1", []string{"player"}, now, 0, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := VerifyAccessToken(accessTokenTestSecret, token, now.AddDate(100, 0, 0))
	if err != nil || claims.ExpiresAt != 0 || claims.SessionID != "session-1" {
		t.Fatalf("permanent claims: %+v, %v", claims, err)
	}
	var payload map[string]any
	if err := decodeTokenPart(strings.Split(token, ".")[1], &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["exp"]; exists {
		t.Fatal("permanent JWT must omit exp")
	}
	if _, err := IssueAccessToken(accessTokenTestSecret, "player-1", nil, now, 0); err == nil {
		t.Fatal("unrevocable permanent token accepted")
	}
	if _, err := VerifyAccessToken(accessTokenTestSecret, token, now.Add(-time.Minute)); err == nil {
		t.Fatal("future issuance accepted")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	token, err := IssueAccessToken(accessTokenTestSecret, "player-1", []string{"player"}, now, 15*time.Minute)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	claims, err := VerifyAccessToken(accessTokenTestSecret, token, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	if claims.Subject != "player-1" || len(claims.Roles) != 1 || claims.Roles[0] != "player" {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestAccessTokenRejectsTamperingAndExpiration(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	token, err := IssueAccessToken(accessTokenTestSecret, "player-1", nil, now, time.Minute)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	tampered := token[:len(token)-1] + "x"
	if _, err := VerifyAccessToken(accessTokenTestSecret, tampered, now); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("tampered token error = %v", err)
	}
	if _, err := VerifyAccessToken(accessTokenTestSecret, token, now.Add(time.Minute)); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("expired token error = %v", err)
	}
	if _, err := IssueAccessToken([]byte("too-short"), "player-1", nil, now, time.Minute); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("short secret error = %v", err)
	}
}

func TestAccessTokenRejectsAlgorithmConfusion(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	token, err := IssueAccessToken(accessTokenTestSecret, "player-1", nil, now, time.Minute)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	parts := strings.Split(token, ".")
	parts[0] = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0"
	if _, err := VerifyAccessToken(accessTokenTestSecret, strings.Join(parts, "."), now); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("algorithm confusion error = %v", err)
	}
}
