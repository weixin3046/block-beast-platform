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
