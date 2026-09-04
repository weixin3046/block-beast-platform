package adminsecurity

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPasswordValidation(t *testing.T) {
	for _, p := range []string{"", "  ", strings.Repeat("中", 43)} {
		if validPassword(p) {
			t.Fatalf("accepted invalid password length=%d", len(p))
		}
	}
	if !validPassword("a") || !validPassword(strings.Repeat("a", 128)) || validLevel("third") {
		t.Fatal("validation boundaries")
	}
}

func TestGlobalPasswordsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := pgx.Identifier{"security_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Isolated schema: tests must never change the target database's global passwords.
	exec(`CREATE TABLE users(id uuid PRIMARY KEY,status text DEFAULT 'active',secondary_password_hash text);
 CREATE TABLE roles(id uuid PRIMARY KEY,code text);
 CREATE TABLE user_roles(user_id uuid,role_id uuid);
 CREATE TABLE auth_identities(user_id uuid,provider text,password_hash text);
 CREATE TABLE audit_logs(id uuid,actor_user_id uuid,action text,target_type text,target_id text,payload jsonb);`)
	migration, err := os.ReadFile("../../../migrations/0050_admin_security_passwords.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	admin, op, player := uuid.NewString(), uuid.NewString(), uuid.NewString()
	loginHash, err := identity.HashPassword("login-secret")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO users(id,secondary_password_hash) VALUES($1,'personal-unchanged'),($2,''),($3,'')`, admin, op, player)
	exec(`INSERT INTO roles VALUES($1,'admin'),($2,'operator');`, admin, op)
	exec(`INSERT INTO user_roles VALUES($1,$1),($2,$2)`, admin, op)
	exec(`INSERT INTO auth_identities VALUES($1,'password',$2)`, admin, loginHash)
	s := NewService(pool)
	check := func(got, want error) {
		t.Helper()
		if !errors.Is(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	status, err := s.Status(ctx, op)
	check(err, nil)
	if status.FirstSet || status.SecondSet {
		t.Fatal("must have no defaults")
	}
	check(s.Verify(ctx, op, "first", "secret"), ErrNotSet)
	check(s.Set(ctx, op, "first", "login-secret", "secret"), ErrForbidden)
	check(s.Set(ctx, admin, "first", "wrong", "secret"), ErrLoginPassword)
	check(s.Set(ctx, admin, "first", "login-secret", "first-secret"), nil)
	check(s.Set(ctx, admin, "second", "login-secret", "second-secret"), nil)
	check(s.Verify(ctx, player, "first", "first-secret"), ErrForbidden)
	check(s.Verify(ctx, op, "first", "first-secret"), nil)
	check(s.Verify(ctx, op, "second", "first-secret"), ErrIncorrect)
	check(s.Verify(ctx, op, "second", "second-secret"), nil)
	check(s.Set(ctx, admin, "first", "login-secret", "rotated-secret"), nil)
	check(s.Verify(ctx, op, "first", "first-secret"), ErrIncorrect)
	check(s.Verify(ctx, op, "first", "rotated-secret"), nil)
	// Parallel failures serialize to five, including the request that triggers lockout.
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Verify(ctx, op, "first", "bad") }()
	}
	wg.Wait()
	close(errs)
	locked, incorrect := 0, 0
	for err := range errs {
		if errors.Is(err, ErrLocked) {
			locked++
		} else if errors.Is(err, ErrIncorrect) {
			incorrect++
		} else {
			t.Fatal(err)
		}
	}
	if locked != 1 || incorrect != 4 {
		t.Fatalf("locked=%d incorrect=%d", locked, incorrect)
	}
	check(s.Verify(ctx, op, "first", "rotated-secret"), ErrLocked)
	check(s.Verify(ctx, admin, "first", "rotated-secret"), nil)
	check(s.Verify(ctx, op, "second", "second-secret"), nil)
	exec(`UPDATE admin_security_attempts SET blocked_until=now()-interval '1 second',window_started_at=now()-interval '16 minutes' WHERE actor_id=$1 AND scope='first'`, op)
	check(s.Verify(ctx, op, "first", "rotated-secret"), nil)
	var hash, personal, payload string
	var count int
	check(pool.QueryRow(ctx, `SELECT password_hash FROM admin_security_passwords WHERE level='first'`).Scan(&hash), nil)
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatal("not Argon2id")
	}
	check(pool.QueryRow(ctx, `SELECT secondary_password_hash FROM users WHERE id=$1`, admin).Scan(&personal), nil)
	if personal != "personal-unchanged" {
		t.Fatal("personal password changed")
	}
	check(pool.QueryRow(ctx, `SELECT count(*),jsonb_agg(payload)::text FROM audit_logs`).Scan(&count, &payload), nil)
	if count != 3 || strings.Contains(payload, "secret") || strings.Contains(payload, "password_hash") {
		t.Fatalf("unsafe or missing audit: %s", payload)
	}
	exec(`UPDATE users SET status='disabled' WHERE id=$1`, op)
	check(s.Verify(ctx, op, "first", "rotated-secret"), ErrForbidden)
}
