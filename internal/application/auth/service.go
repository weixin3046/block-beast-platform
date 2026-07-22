package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
)

var ErrInvalidCredentials = errors.New("invalid login name or password")
var ErrAccountDisabled = errors.New("account is not active")
var ErrAuthNotConfigured = errors.New("authentication is not configured")

type CredentialsReader interface {
	FindPasswordCredentials(ctx context.Context, loginName string) (identity.PasswordCredentials, error)
	UserRoles(ctx context.Context, userID string) ([]string, error)
}

type Service struct {
	credentials CredentialsReader
	secret      []byte
	ttl         time.Duration
	now         func() time.Time
}

func NewService(credentials CredentialsReader, secret string, ttl time.Duration) *Service {
	return &Service{credentials: credentials, secret: []byte(secret), ttl: ttl, now: time.Now}
}

type LoginResult struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int64    `json:"expires_in"`
	UserID      string   `json:"user_id"`
	Roles       []string `json:"roles"`
}

// Login 校验登录名与密码，为激活账号签发携带角色的短期访问令牌。
// 登录名不存在时同样执行一次哈希校验，避免通过响应时间探测账号是否存在。
func (service *Service) Login(ctx context.Context, loginName string, password string) (LoginResult, error) {
	if len(service.secret) < 32 || service.ttl <= 0 {
		return LoginResult{}, ErrAuthNotConfigured
	}
	credentials, err := service.credentials.FindPasswordCredentials(ctx, loginName)
	if errors.Is(err, identity.ErrIdentityNotFound) {
		identity.VerifyPassword(dummyHash(), password)
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}
	if !identity.VerifyPassword(credentials.PasswordHash, password) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if credentials.Status != "active" {
		return LoginResult{}, ErrAccountDisabled
	}
	roles, err := service.credentials.UserRoles(ctx, credentials.UserID)
	if err != nil {
		return LoginResult{}, err
	}
	issuedAt := service.now().UTC()
	token, err := identity.IssueAccessToken(service.secret, credentials.UserID, roles, issuedAt, service.ttl)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(service.ttl / time.Second),
		UserID:      credentials.UserID,
		Roles:       roles,
	}, nil
}

var dummyHash = sync.OnceValue(func() string {
	hash, err := identity.HashPassword("timing-equalization-dummy")
	if err != nil {
		return ""
	}
	return hash
})
