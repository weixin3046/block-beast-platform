package externaldraw

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestManualResultTransaction(t *testing.T) {
	dsn := os.Getenv("LULU_MANUAL_TEST_DSN")
	if dsn == "" {
		t.Skip("LULU_MANUAL_TEST_DSN not set")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "manual_" + uuid.New().String()[:8]
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(ctx, `CREATE TABLE game_types(id uuid primary key,rules jsonb);
 CREATE TABLE rounds(id uuid primary key,game_type_id uuid,sequence bigint,status text,result_at timestamptz);
 CREATE TABLE external_draw_rounds(source text,game text,external_round bigint,status text,outcome jsonb,conflict_outcome jsonb,result_received_at timestamptz,updated_at timestamptz,primary key(source,game,external_round));
 CREATE TABLE audit_logs(id uuid primary key,actor_user_id uuid,action text,target_type text,target_id text,payload jsonb);`)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(p, 3)
	actor := uuid.NewString()
	for _, tc := range []struct {
		public, game string
		result       []int
	}{{GameGreenSprint, "race", []int{5}}, {GameAngryFeather, "lh", []int{2}}, {GameStarSea, "xdy", []int{2, 5, 7}}} {
		id := uuid.NewString()
		if _, err = p.Exec(ctx, `INSERT INTO game_types VALUES($1,jsonb_build_object('source','lulu_ws','extras',jsonb_build_object('external_game',$2::text)))`, id, tc.game); err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(ctx, `INSERT INTO rounds VALUES($1,$2,15693,'closed',now()-interval '1 minute')`, uuid.NewString(), id); err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(ctx, `INSERT INTO external_draw_rounds(source,game,external_round,status) VALUES('lulu_ws',$1,15693,'pending')`, tc.game); err != nil {
			t.Fatal(err)
		}
		in := ManualInput{Game: tc.public, Issue: "15693", Result: tc.result, Reason: "test"}
		out, err := service.ConfirmManual(ctx, actor, in)
		if err != nil || out.AlreadyConfirmed {
			t.Fatalf("first %+v %v", out, err)
		}
		out, err = service.ConfirmManual(ctx, actor, in)
		if err != nil || !out.AlreadyConfirmed {
			t.Fatalf("repeat %+v %v", out, err)
		}
		in.Result = []int{1}
		if _, err = service.ConfirmManual(ctx, actor, in); !errors.Is(err, ErrManualConflict) {
			t.Fatalf("conflict: %v", err)
		}
	}
	var count int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1`, actor).Scan(&count); err != nil || count != 3 {
		t.Fatalf("audit=%d %v", count, err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM external_draw_rounds WHERE status='confirmed' AND outcome IS NOT NULL`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("confirmed=%d %v", count, err)
	}
	// 拒绝取消和未来期；失败不留下确认结果或审计。
	for _, state := range []string{"cancelled", "future"} {
		_, err = p.Exec(ctx, `UPDATE external_draw_rounds SET status='pending',outcome=NULL WHERE game='race'`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Exec(ctx, `UPDATE rounds SET status=$1,result_at=CASE WHEN $1='open' THEN now()+interval '1 hour' ELSE now()-interval '1 minute' END`, map[string]string{"cancelled": "cancelled", "future": "open"}[state])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.ConfirmManual(ctx, actor, ManualInput{Game: GameGreenSprint, Issue: "15693", Result: []int{5}, Reason: "test"}); !errors.Is(err, ErrManualConflict) {
			t.Fatalf("%s: %v", state, err)
		}
	}
	// 审计失败必须回滚确认，避免无审计的资金结果写入。
	if _, err = p.Exec(ctx, `UPDATE rounds SET status='closed',result_at=now()-interval '1 minute'; ALTER TABLE audit_logs ADD CONSTRAINT reject_test CHECK (action <> 'lulu_draw.result_manual_confirmed') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfirmManual(ctx, actor, ManualInput{Game: GameGreenSprint, Issue: "15693", Result: []int{5}, Reason: "rollback test"}); err == nil {
		t.Fatal("expected audit failure")
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM external_draw_rounds WHERE game='race' AND status='pending' AND outcome IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("transaction did not roll back: %d %v", count, err)
	}
}
