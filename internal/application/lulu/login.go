package lulu

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var ErrLoginFailed = errors.New("Lulu SMS login failed")
var ErrLoginLimited = errors.New("Lulu SMS login rate limited")
var phonePattern = regexp.MustCompile(`^1[0-9]{10}$`)
var smsPattern = regexp.MustCompile(`^[0-9]{4,8}$`)

type LoginProvider interface {
	SendCode(context.Context, string) error
	PhoneLogin(context.Context, string, string) (string, string, error)
}
type LoginFactory func(string, string) (LoginProvider, error)

func (s *Service) WithLoginFactory(f LoginFactory) *Service { s.loginFactory = f; return s }

// Login preparation checks admin rights and reserves a cross-instance cooldown
// before any external request. The network call happens outside the transaction.
func (s *Service) loginPrepare(ctx context.Context, actor, phone, action string, version int64) (Config, LoginProvider, error) {
	var cfg Config
	if !phonePattern.MatchString(phone) || version < 1 {
		return cfg, nil, ErrConfigInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return cfg, nil, e
	}
	defer tx.Rollback(ctx)
	if e = admin(ctx, tx, actor); e != nil {
		return cfg, nil, e
	}
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1 AND r.code='admin')`, actor).Scan(&allowed); e != nil {
		return cfg, nil, e
	}
	if !allowed {
		return cfg, nil, ErrForbidden
	}
	cfg, e = readConfig(ctx, tx, true)
	if e != nil {
		return cfg, nil, e
	}
	if cfg.Version != version {
		return cfg, nil, ErrConfigConflict
	}
	if !validAPIURL(cfg.APIURL) || !cfg.ProtocolKeyConfigured || s.loginFactory == nil {
		return cfg, nil, ErrConfigInvalid
	}
	var cipher []byte
	if e = tx.QueryRow(ctx, `SELECT protocol_cipher FROM lulu_config WHERE singleton`).Scan(&cipher); e != nil {
		return cfg, nil, e
	}
	key, e := s.unseal(cipher, "protocol")
	if e != nil {
		return cfg, nil, e
	}
	// Verify the key used for storing the resulting token before sending an SMS.
	if _, e = s.aead(); e != nil {
		return cfg, nil, e
	}
	provider, e := s.loginFactory(cfg.APIURL, key)
	if e != nil {
		return cfg, nil, ErrLoginFailed
	}
	delay := 60 * time.Second
	if action == "login" {
		delay = 5 * time.Second
	}
	tag, e := tx.Exec(ctx, `INSERT INTO lulu_login_limits(action,next_allowed_at) VALUES($1,now()+$2::interval) ON CONFLICT(action) DO UPDATE SET next_allowed_at=EXCLUDED.next_allowed_at WHERE lulu_login_limits.next_allowed_at<=now()`, action, delay.String())
	if e != nil {
		return cfg, nil, e
	}
	if tag.RowsAffected() != 1 {
		return cfg, nil, ErrLoginLimited
	}
	if e = tx.Commit(ctx); e != nil {
		return cfg, nil, e
	}
	return cfg, provider, nil
}
func (s *Service) SendLoginCode(ctx context.Context, actor, phone string, version int64) error {
	_, p, e := s.loginPrepare(ctx, actor, phone, "send", version)
	if e != nil {
		return e
	}
	if e = p.SendCode(ctx, phone); e != nil {
		return ErrLoginFailed
	}
	return nil
}
func (s *Service) PhoneLogin(ctx context.Context, actor, phone, code string, version int64) (Config, error) {
	if !smsPattern.MatchString(code) {
		return Config{}, ErrConfigInvalid
	}
	cfg, p, e := s.loginPrepare(ctx, actor, phone, "login", version)
	if e != nil {
		return Config{}, e
	}
	uid, token, e := p.PhoneLogin(ctx, phone, code)
	if e != nil || !ValidUID(uid) || token == "" || !validToken(token) {
		return Config{}, ErrLoginFailed
	}
	// Compare-and-swap prevents an in-flight login from overwriting newer config.
	// UpdateConfig also enforces pause/pending-order checks when UID changes.
	return s.UpdateConfig(ctx, actor, ConfigUpdate{ReceiverUID: uid, Enabled: cfg.Enabled, Version: cfg.Version, loginToken: token})
}
