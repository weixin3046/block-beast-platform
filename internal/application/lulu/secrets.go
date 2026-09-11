package lulu

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"
)

var ErrEncryption = errors.New("Lulu credential encryption unavailable")

func (s *Service) WithEncryptionKey(key string) *Service { s.encryptionKey = key; return s }
func (s *Service) aead() (cipher.AEAD, error) {
	key, e := base64.StdEncoding.DecodeString(s.encryptionKey)
	if e != nil || len(key) != 32 {
		return nil, ErrEncryption
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, ErrEncryption
	}
	return cipher.NewGCM(block)
}
func (s *Service) seal(value, purpose string) ([]byte, error) {
	a, e := s.aead()
	if e != nil {
		return nil, e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, ErrEncryption
	}
	return a.Seal(n, n, []byte(value), []byte("lulu:"+purpose)), nil
}
func (s *Service) unseal(value []byte, purpose string) (string, error) {
	a, e := s.aead()
	if e != nil || len(value) < a.NonceSize() {
		return "", ErrEncryption
	}
	p, e := a.Open(nil, value[:a.NonceSize()], value[a.NonceSize():], []byte("lulu:"+purpose))
	if e != nil {
		return "", ErrEncryption
	}
	return string(p), nil
}
func validAPIURL(value string) bool {
	u, e := url.Parse(value)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && len(value) <= 2048
}

// RuntimeConfig never crosses an HTTP or audit boundary.
type RuntimeConfig struct {
	Config
	Token       string `json:"-"`
	ProtocolKey string `json:"-"`
}

func (s *Service) RuntimeConfig(ctx context.Context) (RuntimeConfig, error) {
	return s.runtimeConfig(ctx, false)
}
func (s *Service) runtimeConfig(ctx context.Context, includeDisabled bool) (RuntimeConfig, error) {
	var r RuntimeConfig
	var token, key []byte
	e := s.pool.QueryRow(ctx, `SELECT `+configColumns+`,token_cipher,protocol_cipher FROM lulu_config WHERE singleton`).Scan(&r.ReceiverUID, &r.Enabled, &r.Version, &r.UpdatedAt, &r.APIURL, &r.ScanStartAt, &r.TokenConfigured, &r.ProtocolKeyConfigured, &token, &key)
	if e != nil {
		return r, e
	}
	if !r.Enabled && !includeDisabled {
		return r, nil
	}
	r.Token, e = s.unseal(token, "token")
	if e != nil {
		return RuntimeConfig{}, e
	}
	r.ProtocolKey, e = s.unseal(key, "protocol")
	return r, e
}
func validToken(t string) bool {
	return len(t) <= 16384 && strings.TrimSpace(t) == t && !strings.ContainsAny(t, "\r\n")
}
func validateStart(t *time.Time) bool { return t == nil || (!t.IsZero() && !t.After(time.Now())) }

// ValidProtocolKey validates the upstream shared secret, not the storage key.
func ValidProtocolKey(value string) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	return err == nil && (len(raw) == 24 || len(raw) == 32)
}
