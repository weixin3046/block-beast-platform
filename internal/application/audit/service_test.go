package audit

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordAppendsAuditLog(t *testing.T) {
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
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, 'audit actor')`, userID)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	targetID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE target_id = $1`, targetID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	service := NewService(pool)
	err = service.Record(ctx, Entry{
		ActorUserID: userID,
		Action:      "round.cancel",
		TargetType:  "round",
		TargetID:    targetID,
		Payload:     map[string]int{"refunded_bet_count": 3},
	})
	if err != nil {
		t.Fatalf("record audit: %v", err)
	}
	// 匿名操作写入 NULL actor。
	err = service.Record(ctx, Entry{
		Action:     "auth.login",
		TargetType: "user",
		TargetID:   targetID,
		Payload:    map[string]string{"outcome": "failure"},
	})
	if err != nil {
		t.Fatalf("record anonymous audit: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE target_id = $1`, targetID).Scan(&count)
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if count != 2 {
		t.Fatalf("audit log count = %d, want 2", count)
	}
	var nullActorCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE target_id = $1 AND actor_user_id IS NULL AND action = 'auth.login'`, targetID).Scan(&nullActorCount)
	if err != nil {
		t.Fatalf("count anonymous audit logs: %v", err)
	}
	if nullActorCount != 1 {
		t.Fatalf("anonymous audit log count = %d, want 1", nullActorCount)
	}
}
