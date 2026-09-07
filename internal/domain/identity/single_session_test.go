package identity

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSingleSessionReplacementAndRotation(t *testing.T) {
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
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'session test')`, id); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	r := NewPostgresRepository(pool)
	expiry := time.Now().Add(time.Hour)
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := r.CreateSession(ctx, id, a, AudiencePlayer, expiry); err != nil {
		t.Fatal(err)
	}
	sid, err := r.SessionID(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	claims := AccessTokenClaims{Subject: id, SessionID: sid}
	if err := r.ValidateSession(ctx, claims); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RotateSession(ctx, a, b, AudiencePlayer, expiry); err != nil {
		t.Fatal(err)
	}
	if next, err := r.SessionID(ctx, b); err != nil || next != sid {
		t.Fatal("rotation changed session", err)
	}
	if err := r.ValidateSession(ctx, claims); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateSession(ctx, id, c, AudiencePlayer, expiry); err != nil {
		t.Fatal(err)
	}
	if r.ValidateSession(ctx, claims) == nil {
		t.Fatal("old access session accepted")
	}
	if _, err := r.RotateSession(ctx, b, uuid.NewString(), AudiencePlayer, expiry); err == nil {
		t.Fatal("old refresh accepted")
	}
	if r.ValidateSession(ctx, AccessTokenClaims{Subject: id}) == nil {
		t.Fatal("legacy token accepted")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := r.CreateSession(ctx, id, uuid.NewString(), AudiencePlayer, expiry); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active sessions=%d err=%v", count, err)
	}
}
