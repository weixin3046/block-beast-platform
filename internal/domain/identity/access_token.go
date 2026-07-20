package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalidAccessToken = errors.New("invalid access token")

type AccessTokenClaims struct {
	Subject   string   `json:"sub"`
	Roles     []string `json:"roles"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
}

// IssueAccessToken creates a signed, short-lived JWT for authenticated API calls.
// The signing key must contain at least 32 bytes of secret material.
func IssueAccessToken(secret []byte, subject string, roles []string, issuedAt time.Time, lifetime time.Duration) (string, error) {
	if len(secret) < 32 || subject == "" || lifetime <= 0 {
		return "", ErrInvalidAccessToken
	}
	header, err := encodeTokenPart(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := encodeTokenPart(AccessTokenClaims{
		Subject:   subject,
		Roles:     append([]string(nil), roles...),
		IssuedAt:  issuedAt.UTC().Unix(),
		ExpiresAt: issuedAt.UTC().Add(lifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + claims
	return signingInput + "." + signature(secret, signingInput), nil
}

// VerifyAccessToken validates the JWT signature and expiration before returning claims.
func VerifyAccessToken(secret []byte, token string, now time.Time) (AccessTokenClaims, error) {
	if len(secret) < 32 {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := decodeTokenPart(parts[0], &header); err != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	expectedSignature := signature(secret, parts[0]+"."+parts[1])
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expectedSignature)) != 1 {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	var claims AccessTokenClaims
	if err := decodeTokenPart(parts[1], &claims); err != nil || claims.Subject == "" || claims.ExpiresAt <= now.UTC().Unix() || claims.IssuedAt > now.UTC().Unix() {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	return claims, nil
}

func encodeTokenPart(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeTokenPart(encoded string, destination any) error {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, destination)
}

func signature(secret []byte, signingInput string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
