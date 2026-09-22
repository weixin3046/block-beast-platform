package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPermanentLoginLifecycle(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, role := range []string{"player", "admin"} {
		t.Run(role, func(t *testing.T) {
			id := uuid.NewString()
			name := "permanent-" + id
			password := "permanent-test-password"
			hash, err := identity.HashPassword(password)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO users(id,login_name,display_name) VALUES($1,$2,'permanent test')`, id, name); err != nil {
				t.Fatal(err)
			}
			defer pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
			if _, err = pool.Exec(ctx, `INSERT INTO auth_identities(id,user_id,provider,subject,password_hash) VALUES($1,$2,'password',$3,$4)`, uuid.NewString(), id, name, hash); err != nil {
				t.Fatal(err)
			}
			defer pool.Exec(ctx, `DELETE FROM auth_identities WHERE user_id=$1`, id)
			if _, err = pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=$2`, id, role); err != nil {
				t.Fatal(err)
			}
			defer pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, id)
			repo := identity.NewPostgresRepository(pool)
			service := NewService(repo, testSecret, 15*time.Minute).WithSessions(repo, 24*time.Hour).WithPermanentTokens(true)
			login, refresh := service.Login, service.Refresh
			if role == "admin" {
				login, refresh = service.LoginAdmin, service.RefreshAdmin
			}
			first, err := login(ctx, name, password)
			if err != nil {
				t.Fatal(err)
			}
			check := func(result LoginResult) identity.AccessTokenClaims {
				t.Helper()
				if result.ExpiresIn != 0 {
					t.Fatal("login still expires")
				}
				claims, err := identity.VerifyAccessToken([]byte(testSecret), result.AccessToken, time.Now().AddDate(100, 0, 0))
				if err != nil {
					t.Fatal(err)
				}
				var infinite bool
				if err := pool.QueryRow(ctx, `SELECT expires_at='infinity'::timestamptz FROM sessions WHERE id=$1`, claims.SessionID).Scan(&infinite); err != nil || !infinite {
					t.Fatalf("session has deadline: %v", err)
				}
				if err := repo.ValidateSession(ctx, claims); err != nil {
					t.Fatal(err)
				}
				return claims
			}
			claims := check(first)
			if _, err := login(ctx, name, "wrong"); err == nil {
				t.Fatal("wrong password accepted")
			}
			if err := repo.ValidateSession(ctx, claims); err != nil {
				t.Fatal("failed login revoked session")
			}
			rotated, err := refresh(ctx, first.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			rotatedClaims := check(rotated)
			if rotatedClaims.SessionID != claims.SessionID {
				t.Fatal("refresh changed session")
			}
			if _, err := refresh(ctx, first.RefreshToken); err == nil {
				t.Fatal("old refresh reused")
			}
			second, err := login(ctx, name, password)
			if err != nil {
				t.Fatal(err)
			}
			secondClaims := check(second)
			if repo.ValidateSession(ctx, claims) == nil {
				t.Fatal("relogin did not revoke access")
			}
			if _, err := refresh(ctx, rotated.RefreshToken); err == nil {
				t.Fatal("relogin did not revoke refresh")
			}
			if err := service.Logout(ctx, second.RefreshToken); err != nil {
				t.Fatal(err)
			}
			if repo.ValidateSession(ctx, secondClaims) == nil {
				t.Fatal("logout did not revoke access")
			}
			third, err := login(ctx, name, password)
			if err != nil {
				t.Fatal(err)
			}
			thirdClaims := check(third)
			if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if repo.ValidateSession(ctx, thirdClaims) == nil {
				t.Fatal("disabled account accepted")
			}
			if _, err := refresh(ctx, third.RefreshToken); err == nil {
				t.Fatal("disabled account refreshed")
			}
		})
	}
}

func TestPermanentLoginRequiresSessionStore(t *testing.T) {
	service := NewService(nil, testSecret, time.Minute).WithPermanentTokens(true)
	if _, err := service.Login(context.Background(), "user", "password"); err != ErrAuthNotConfigured {
		t.Fatalf("missing session store: %v", err)
	}
}
