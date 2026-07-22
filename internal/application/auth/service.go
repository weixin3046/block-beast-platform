package auth

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
)

var ErrInvalidCredentials = errors.New("invalid login name or password")
var ErrAccountDisabled = errors.New("account is not active")
var ErrAuthNotConfigured = errors.New("authentication is not configured")
var ErrInvalidLoginName = errors.New("login name must be 3-32 chars of letters, digits, '-' or '_'")
var ErrInvalidPassword = errors.New("password must contain at least 12 characters")

// DefaultWalletCurrencies 是注册时创建的零余额钱包货币列表。
var DefaultWalletCurrencies = []string{"USDT", "POINTS", "STAMINA"}

// loginNamePattern 约束登录名便于在 URL、日志与聊天中安全使用。
var loginNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type CredentialsReader interface {
	FindPasswordCredentials(ctx context.Context, loginName string) (identity.PasswordCredentials, error)
	UserRoles(ctx context.Context, userID string) ([]string, error)
}

type UserRegistrar interface {
	RegisterPasswordUser(ctx context.Context, loginName string, displayName string, passwordHash string, roleCode string, currencies []string) (string, error)
}

type Service struct {
	credentials CredentialsReader
	registrar   UserRegistrar
	secret      []byte
	ttl         time.Duration
	now         func() time.Time
}

func NewService(credentials CredentialsReader, secret string, ttl time.Duration) *Service {
	return &Service{credentials: credentials, secret: []byte(secret), ttl: ttl, now: time.Now}
}

// WithRegistrar 装配注册能力；identity.PostgresRepository 同时满足两个接口。
func (service *Service) WithRegistrar(registrar UserRegistrar) *Service {
	service.registrar = registrar
	return service
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

// Register 创建新玩家账号（用户、密码凭证、player 角色、默认货币零余额钱包）
// 并直接签发访问令牌，注册完成即可调用业务接口。
func (service *Service) Register(ctx context.Context, loginName string, displayName string, password string) (LoginResult, error) {
	if service.registrar == nil || len(service.secret) < 32 || service.ttl <= 0 {
		return LoginResult{}, ErrAuthNotConfigured
	}
	if !loginNamePattern.MatchString(loginName) {
		return LoginResult{}, ErrInvalidLoginName
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return LoginResult{}, ErrInvalidPassword
	}
	if displayName == "" {
		displayName = loginName
	}
	userID, err := service.registrar.RegisterPasswordUser(ctx, loginName, displayName, hash, identity.RolePlayer, DefaultWalletCurrencies)
	if err != nil {
		return LoginResult{}, err
	}
	roles := []string{identity.RolePlayer}
	issuedAt := service.now().UTC()
	token, err := identity.IssueAccessToken(service.secret, userID, roles, issuedAt, service.ttl)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(service.ttl / time.Second),
		UserID:      userID,
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
