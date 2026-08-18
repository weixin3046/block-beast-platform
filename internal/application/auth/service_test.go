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

func (stubRegistrar) RegisterPasswordUser(_ context.Context, _ string, _ string, _ string, _ string, _ []string, _ int64) (string, error) {
	return "user-1", nil
}

type stubCredentials struct {
	roles []string
}

func (stubCredentials) FindPasswordCredentials(context.Context, string) (identity.PasswordCredentials, error) {
	return identity.PasswordCredentials{}, identity.ErrIdentityNotFound
}

func (credentials stubCredentials) UserRoles(context.Context, string) ([]string, error) {
	return credentials.roles, nil
}

func (stubCredentials) PublicUserID(context.Context, string) (int64, error) { return 100000, nil }
func (stubCredentials) PasswordHashByUserID(context.Context, string) (string, error) {
	return "", identity.ErrIdentityNotFound
}
func (stubCredentials) UpdatePasswordHash(context.Context, string, string) error { return nil }
func (stubCredentials) SecondaryPasswordHashByUserID(context.Context, string) (string, error) {
	return "", identity.ErrIdentityNotFound
}
func (stubCredentials) UpdateSecondaryPasswordHash(context.Context, string, string) error { return nil }

type loginCredentials struct {
	passwordHash string
	roles        []string
}

func (credentials loginCredentials) FindPasswordCredentials(context.Context, string) (identity.PasswordCredentials, error) {
	return identity.PasswordCredentials{UserID: "user-1", Status: "active", PasswordHash: credentials.passwordHash}, nil
}

func (credentials loginCredentials) UserRoles(context.Context, string) ([]string, error) {
	return credentials.roles, nil
}

func (loginCredentials) PublicUserID(context.Context, string) (int64, error) { return 100000, nil }
func (loginCredentials) PasswordHashByUserID(context.Context, string) (string, error) {
	return "", identity.ErrIdentityNotFound
}
func (loginCredentials) UpdatePasswordHash(context.Context, string, string) error { return nil }
func (loginCredentials) SecondaryPasswordHashByUserID(context.Context, string) (string, error) {
	return "", identity.ErrIdentityNotFound
}
func (loginCredentials) UpdateSecondaryPasswordHash(context.Context, string, string) error {
	return nil
}

type memoryLoginAttempts struct {
	failures    int
	lockedUntil time.Time
	clearCount  int
}

func (store *memoryLoginAttempts) LoginBlocked(_ context.Context, _ string, now time.Time) (bool, error) {
	return store.lockedUntil.After(now), nil
}

func (store *memoryLoginAttempts) RecordLoginFailure(
	_ context.Context,
	_ string,
	now time.Time,
	maxFailures int,
	_ time.Duration,
	lockout time.Duration,
) (bool, error) {
	store.failures++
	if store.failures >= maxFailures {
		store.lockedUntil = now.Add(lockout)
		return true, nil
	}
	return false, nil
}

func (store *memoryLoginAttempts) ClearLoginFailures(context.Context, string) error {
	store.failures = 0
	store.lockedUntil = time.Time{}
	store.clearCount++
	return nil
}

func TestLoginProtectionLocksAndRecovers(t *testing.T) {
	hash, err := identity.HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	attempts := &memoryLoginAttempts{}
	service := NewService(loginCredentials{passwordHash: hash, roles: []string{identity.RolePlayer}}, testSecret, time.Minute).
		WithLoginProtection(attempts, LoginProtectionPolicy{MaxFailures: 3, Window: 15 * time.Minute, Lockout: 15 * time.Minute})
	service.now = func() time.Time { return now }

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := service.Login(context.Background(), "player-1", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", attempt, err)
		}
	}
	if _, err := service.Login(context.Background(), "player-1", "wrong"); !errors.Is(err, ErrTooManyLoginAttempts) {
		t.Fatalf("third attempt error = %v, want ErrTooManyLoginAttempts", err)
	}
	if _, err := service.Login(context.Background(), "player-1", "correct-horse-battery"); !errors.Is(err, ErrTooManyLoginAttempts) {
		t.Fatalf("locked correct-password error = %v, want ErrTooManyLoginAttempts", err)
	}

	now = now.Add(16 * time.Minute)
	if _, err := service.Login(context.Background(), "player-1", "correct-horse-battery"); err != nil {
		t.Fatalf("login after lockout: %v", err)
	}
	if attempts.clearCount != 1 || attempts.failures != 0 {
		t.Fatalf("clear count/failures = %d/%d", attempts.clearCount, attempts.failures)
	}
}

func TestCorrectPasswordOnWrongAudienceDoesNotIncreaseFailures(t *testing.T) {
	hash, err := identity.HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	attempts := &memoryLoginAttempts{failures: 2}
	service := NewService(loginCredentials{passwordHash: hash, roles: []string{identity.RoleAdmin}}, testSecret, time.Minute).
		WithLoginProtection(attempts, LoginProtectionPolicy{MaxFailures: 3, Window: 15 * time.Minute, Lockout: 15 * time.Minute})

	if _, err := service.Login(context.Background(), "admin", "correct-horse-battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
	if attempts.failures != 0 || attempts.clearCount != 1 {
		t.Fatalf("wrong audience should clear valid-password failures, got %d/%d", attempts.failures, attempts.clearCount)
	}
}

func TestLoginSeparatesPlayerAndAdminAudiences(t *testing.T) {
	hash, err := identity.HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		roles      []string
		adminLogin bool
		wantOK     bool
	}{
		{name: "player on player endpoint", roles: []string{identity.RolePlayer}, wantOK: true},
		{name: "player on admin endpoint", roles: []string{identity.RolePlayer}, adminLogin: true},
		{name: "admin on admin endpoint", roles: []string{identity.RoleAdmin}, adminLogin: true, wantOK: true},
		{name: "operator on admin endpoint", roles: []string{identity.RoleOperator}, adminLogin: true, wantOK: true},
		{name: "admin on player endpoint", roles: []string{identity.RoleAdmin}},
		{name: "admin player on player endpoint", roles: []string{identity.RolePlayer, identity.RoleAdmin}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService(loginCredentials{passwordHash: hash, roles: testCase.roles}, testSecret, time.Minute)
			var err error
			if testCase.adminLogin {
				_, err = service.LoginAdmin(context.Background(), "user", "correct-horse-battery")
			} else {
				_, err = service.Login(context.Background(), "user", "correct-horse-battery")
			}
			if testCase.wantOK && err != nil {
				t.Fatalf("login error = %v", err)
			}
			if !testCase.wantOK && !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("login error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

type memorySessions struct {
	userID   string
	current  string
	revoked  bool
	audience SessionAudience
}

func (sessions *memorySessions) CreateSession(_ context.Context, userID string, tokenHash string, audience SessionAudience, _ time.Time) error {
	sessions.userID = userID
	sessions.current = tokenHash
	sessions.audience = audience
	return nil
}

func (sessions *memorySessions) RotateSession(_ context.Context, oldTokenHash string, newTokenHash string, audience SessionAudience, _ time.Time) (string, error) {
	if sessions.revoked || oldTokenHash != sessions.current || audience != sessions.audience {
		return "", identity.ErrIdentityNotFound
	}
	sessions.current = newTokenHash
	return sessions.userID, nil
}

func (sessions *memorySessions) RevokeSession(_ context.Context, tokenHash string) error {
	if sessions.revoked || tokenHash != sessions.current {
		return identity.ErrIdentityNotFound
	}
	sessions.revoked = true
	return nil
}

func TestRefreshRotatesTokenAndLogoutRevokesIt(t *testing.T) {
	sessions := &memorySessions{userID: "user-1", audience: AudiencePlayer}
	service := NewService(stubCredentials{roles: []string{"player"}}, testSecret, 15*time.Minute).
		WithSessions(sessions, 30*24*time.Hour)
	initial, err := randomRefreshToken()
	if err != nil {
		t.Fatalf("create initial token: %v", err)
	}
	sessions.current = hashRefreshToken(initial)

	result, err := service.Refresh(context.Background(), initial)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if result.RefreshToken == "" || result.RefreshToken == initial || result.AccessToken == "" || result.UserID != "user-1" {
		t.Fatalf("refresh result = %+v", result)
	}
	if _, err := service.Refresh(context.Background(), initial); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("reusing old token error = %v, want ErrInvalidRefreshToken", err)
	}
	if err := service.Logout(context.Background(), result.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := service.Refresh(context.Background(), result.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("refresh after logout error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestRefreshTokenCannotCrossAudience(t *testing.T) {
	sessions := &memorySessions{userID: "admin-1", audience: AudienceAdmin}
	service := NewService(stubCredentials{roles: []string{identity.RoleAdmin}}, testSecret, 15*time.Minute).
		WithSessions(sessions, 30*24*time.Hour)
	initial, err := randomRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	sessions.current = hashRefreshToken(initial)
	if _, err := service.Refresh(context.Background(), initial); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("admin token on player endpoint error = %v", err)
	}
	result, err := service.RefreshAdmin(context.Background(), initial)
	if err != nil || result.UserID != "admin-1" || !containsRole(result.Roles, identity.RoleAdmin) {
		t.Fatalf("admin refresh result = %+v, err=%v", result, err)
	}
}

func TestRefreshRevokesSessionAfterRoleChange(t *testing.T) {
	sessions := &memorySessions{userID: "former-admin", audience: AudienceAdmin}
	service := NewService(stubCredentials{roles: []string{identity.RolePlayer}}, testSecret, 15*time.Minute).
		WithSessions(sessions, 30*24*time.Hour)
	initial, err := randomRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	sessions.current = hashRefreshToken(initial)
	if _, err := service.RefreshAdmin(context.Background(), initial); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("role-changed refresh error = %v", err)
	}
	if !sessions.revoked {
		t.Fatal("role-changed session must be revoked")
	}
}

func containsRole(roles []string, expected string) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}

func TestRegisterValidatesInput(t *testing.T) {
	newService := func() *Service {
		return NewService(stubCredentials{}, testSecret, time.Minute).WithRegistrar(stubRegistrar{})
	}
	if _, err := newService().Register(context.Background(), "ab", "", "valid-password-12", "101"); !errors.Is(err, ErrInvalidLoginName) {
		t.Fatalf("short login name error = %v, want ErrInvalidLoginName", err)
	}
	if _, err := newService().Register(context.Background(), "bad name!", "", "valid-password-12", "101"); !errors.Is(err, ErrInvalidLoginName) {
		t.Fatalf("invalid chars error = %v, want ErrInvalidLoginName", err)
	}
	if _, err := newService().Register(context.Background(), "valid-name", "", "short", "101"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password error = %v, want ErrInvalidPassword", err)
	}
	service := NewService(nil, testSecret, time.Minute)
	if _, err := service.Register(context.Background(), "valid-name", "", "valid-password-12", "101"); !errors.Is(err, ErrAuthNotConfigured) {
		t.Fatalf("missing registrar error = %v, want ErrAuthNotConfigured", err)
	}
}

func TestDevelopmentCanDisablePasswordLengthPolicy(t *testing.T) {
	service := NewService(stubCredentials{}, testSecret, time.Minute).
		WithStrictPasswordPolicy(false).
		WithRegistrar(stubRegistrar{})
	if _, err := service.Register(context.Background(), "dev-user", "", "123", "101"); err != nil {
		t.Fatalf("development registration error = %v", err)
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

	result, err := service.Register(ctx, loginName, "", password, "101")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	userID := result.UserID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM auth_identities WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
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

	// 默认三种货币零余额钱包已创建。
	rows, err := pool.Query(ctx, `SELECT currency, available_minor FROM wallets WHERE user_id = $1 ORDER BY currency`, userID)
	if err != nil {
		t.Fatalf("read wallets: %v", err)
	}
	defer rows.Close()
	wallets := make(map[string]int64)
	for rows.Next() {
		var currency string
		var availableMinor int64
		if err := rows.Scan(&currency, &availableMinor); err != nil {
			t.Fatalf("scan wallet: %v", err)
		}
		wallets[currency] = availableMinor
	}
	for _, currency := range DefaultWalletCurrencies {
		if balance, ok := wallets[currency]; !ok || balance != 0 {
			t.Fatalf("wallet %s = %d, want 0", currency, balance)
		}
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
	if _, err := service.Register(ctx, loginName, "", password, "101"); !errors.Is(err, identity.ErrLoginNameTaken) {
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
	var roleID string
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
	if err := pool.QueryRow(ctx, `SELECT id::text FROM roles WHERE code = 'player'`).Scan(&roleID); err != nil {
		t.Fatalf("find player role: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID)
	if err != nil {
		t.Fatalf("assign role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID)
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
