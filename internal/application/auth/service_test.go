package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestLoginRejectsUnconfiguredService(t *testing.T) {
	service := NewService(nil, "short", time.Minute)
	if _, err := service.Login(context.Background(), "user", "password"); !errors.Is(err, ErrAuthNotConfigured) {
		t.Fatalf("error = %v, want ErrAuthNotConfigured", err)
	}
}

type stubRegistrar struct{}

func (stubRegistrar) RegisterPasswordUser(_ context.Context, _ string, _ string, _ string, _ string, _ string) (string, error) {
	return "", nil
}

func TestRegisterValidatesInput(t *testing.T) {
	newService := func() *Service {
		return NewService(nil, testSecret, time.Minute).WithRegistrar(stubRegistrar{})
	}
	if _, err := newService().Register(context.Background(), "ab", "", "valid-password-12"); !errors.Is(err, ErrInvalidLoginName) {
		t.Fatalf("short login name error = %v, want ErrInvalidLoginName", err)
	}
	if _, err := newService().Register(context.Background(), "bad name!", "", "valid-password-12"); !errors.Is(err, ErrInvalidLoginName) {
		t.Fatalf("invalid chars error = %v, want ErrInvalidLoginName", err)
	}
	if _, err := newService().Register(context.Background(), "valid-name", "", "short"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password error = %v, want ErrInvalidPassword", err)
	}
	service := NewService(nil, testSecret, time.Minute)
	if _, err := service.Register(context.Background(), "valid-name", "", "valid-password-12"); !errors.Is(err, ErrAuthNotConfigured) {
		t.Fatalf("missing registrar error = %v, want ErrAuthNotConfigured", err)
	}
}

func TestRegisterCreatesPlayableAccount(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	loginName := "reg-" + uuid.NewString()[:8]
	password := "register-test-password"
	repository := identity.NewPostgresRepository(pool)
	service := NewService(repository, testSecret, 15*time.Minute).WithRegistrar(repository)

	result, err := service.Register(ctx, loginName, "", password)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	userID := result.UserID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM auth_identities WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE code = 'player' AND id NOT IN (SELECT role_id FROM user_roles)`)
	})
	if result.AccessToken == "" || result.UserID == "" || len(result.Roles) != 1 || result.Roles[0] != "player" {
		t.Fatalf("register result = %+v", result)
	}
	claims, err := identity.VerifyAccessToken([]byte(testSecret), result.AccessToken, time.Now().UTC())
	if err != nil || claims.Subject != userID {
		t.Fatalf("registered token claims = %+v, err = %v", claims, err)
	}

	// 注册后立即可登录。
	login, err := service.Login(ctx, loginName, password)
	if err != nil {
		t.Fatalf("login after register: %v", err)
	}
	if login.UserID != userID {
		t.Fatalf("login user = %s, want %s", login.UserID, userID)
	}

	// 默认 USDT 零余额钱包已创建。
	var currency string
	var availableMinor int64
	err = pool.QueryRow(ctx, `SELECT currency, available_minor FROM wallets WHERE user_id = $1`, userID).Scan(&currency, &availableMinor)
	if err != nil {
		t.Fatalf("read wallet: %v", err)
	}
	if currency != DefaultWalletCurrency || availableMinor != 0 {
		t.Fatalf("wallet = %s/%d, want %s/0", currency, availableMinor, DefaultWalletCurrency)
	}

	// display_name 缺省回退为登录名。
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName); err != nil {
		t.Fatalf("read display name: %v", err)
	}
	if displayName != loginName {
		t.Fatalf("display name = %q, want %q", displayName, loginName)
	}

	// 重复注册同一登录名必须冲突。
	if _, err := service.Register(ctx, loginName, "", password); !errors.Is(err, identity.ErrLoginNameTaken) {
		t.Fatalf("duplicate register error = %v, want ErrLoginNameTaken", err)
	}
}

func TestLoginIssuesTokenWithRoles(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	userID := uuid.NewString()
	roleID := uuid.NewString()
	loginName := "player-" + userID
	password := "correct-horse-battery"
	hash, err := identity.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name, login_name) VALUES ($1, 'login test player', $2)`, userID, loginName)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO auth_identities (id, user_id, provider, subject, password_hash) VALUES ($1, $2, 'password', $3, $4)`, uuid.NewString(), userID, loginName, hash)
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO roles (id, code, description) VALUES ($1, 'player', 'player role')`, roleID)
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID)
	if err != nil {
		t.Fatalf("assign role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE id = $1`, roleID)
		_, _ = pool.Exec(ctx, `DELETE FROM auth_identities WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	service := NewService(identity.NewPostgresRepository(pool), testSecret, 15*time.Minute)
	result, err := service.Login(ctx, loginName, password)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.TokenType != "Bearer" || result.ExpiresIn != 900 || result.UserID != userID || len(result.Roles) != 1 || result.Roles[0] != "player" {
		t.Fatalf("login result = %+v", result)
	}
	claims, err := identity.VerifyAccessToken([]byte(testSecret), result.AccessToken, time.Now().UTC())
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Subject != userID || !claims.HasRole("player") {
		t.Fatalf("claims = %+v", claims)
	}

	if _, err := service.Login(ctx, loginName, "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, "nobody-"+userID, password); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user error = %v, want ErrInvalidCredentials", err)
	}

	_, err = pool.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, userID)
	if err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := service.Login(ctx, loginName, password); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled user error = %v, want ErrAccountDisabled", err)
	}
}
