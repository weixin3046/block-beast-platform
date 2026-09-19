package externaldraw

import (
	"context"
	"fmt"
	"github.com/block-beast/platform/internal/platform/luludraw"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

// Use a dedicated local test DB. Fixtures live in a private schema, not app tables.
func TestBackfillOnlyConfirmsPendingOverdueRounds(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("backfill_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
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
	_, err = pool.Exec(ctx, `CREATE TABLE external_draw_rounds(source text,game text,external_round bigint,status text,outcome jsonb,result_received_at timestamptz,updated_at timestamptz);
 CREATE TABLE game_types(id int,rules jsonb);
 CREATE TABLE rounds(game_type_id int,sequence bigint,status text,result_at timestamptz);
 CREATE TABLE audit_logs(id uuid,action text,target_type text,target_id text,payload jsonb);
 INSERT INTO game_types VALUES(1,'{"source":"lulu_ws","extras":{"external_game":"race"}}');`)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, 10)
	for i, tc := range []struct {
		status, roundStatus, outcome string
		future                       bool
		want                         string
	}{
		{"pending", "closed", "null", false, "confirmed"},
		{"confirmed", "closed", `["2"]`, false, "confirmed"},
		{"conflict", "closed", `["2"]`, false, "conflict"},
		{"pending", "settled", "null", false, "pending"},
		{"pending", "cancelled", "null", false, "pending"},
		{"pending", "open", "null", true, "pending"},
	} {
		issue := int64(i + 1)
		at := time.Now().Add(-time.Minute)
		if tc.future {
			at = time.Now().Add(time.Minute)
		}
		_, err = pool.Exec(ctx, `INSERT INTO external_draw_rounds VALUES('lulu_ws','race',$1,$2,$3,NULL,NULL)`, issue, tc.status, tc.outcome)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `INSERT INTO rounds VALUES(1,$1,$2,$3)`, issue, tc.roundStatus, at)
		if err != nil {
			t.Fatal(err)
		}
		event := luludraw.Event{Game: "race", Round: fmt.Sprint(issue), Result: []string{"5"}}
		for range 2 {
			if err = service.confirmBackfill(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
		var status, outcome string
		if err = pool.QueryRow(ctx, `SELECT status,outcome::text FROM external_draw_rounds WHERE external_round=$1`, issue).Scan(&status, &outcome); err != nil {
			t.Fatal(err)
		}
		if status != tc.want {
			t.Fatalf("case %d status=%s", i, status)
		}
		if tc.status == "confirmed" && outcome != `["2"]` {
			t.Fatal("confirmed result overwritten")
		}
	}
	var audits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d error=%v", audits, err)
	}
	if err = service.confirmBackfill(ctx, luludraw.Event{Game: "race", Round: "999", Result: []string{"5"}}); err != nil {
		t.Fatal(err)
	}
}
