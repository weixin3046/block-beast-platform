package operations

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

// Temporary tables on a single connection keep this test independent of
// existing application data and exercise the actual production SQL.
func TestMonitorRoundsLatestLuluAndWaitingGames(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(ctx, `CREATE TEMP TABLE game_types(id int,code text,name text,enabled bool,rules jsonb);
	CREATE TEMP TABLE rounds(game_type_id int,sequence bigint,status text,bet_closes_at timestamptz,result_at timestamptz);
	INSERT INTO game_types VALUES (1,'lulu-xdy','xdy',true,'{"source":"lulu_ws"}'),(2,'lulu-lh','lh',true,'{"source":"lulu_ws"}'),(3,'hash_9','hash',true,'{"source":"tron_hash"}'),(4,'off','off',false,'{}'),(5,'done','done',true,'{}');
	INSERT INTO rounds VALUES (1,7323,'closed',now(),now()),(1,7493,'open',now(),now()),(3,90,'closed',now(),now()),(3,99,'scheduled',now(),now()),(5,10,'settled',now(),now());`)
	if err != nil {
		t.Fatal(err)
	}
	items, err := NewService(p).RoundCountdowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items=%+v", items)
	}
	byCode := map[string]MonitorRound{}
	for _, item := range items {
		byCode[item.GameType] = item
	}
	if byCode["lulu-xdy"].Sequence != 7493 || byCode["hash_9"].Sequence != 90 || byCode["done"].Status != "settled" {
		t.Fatalf("items=%+v", items)
	}
	waiting := byCode["lulu-lh"]
	if waiting.Sequence != 0 || waiting.Status != "waiting" || waiting.BetClosesAt != nil || waiting.ResultAt != nil {
		t.Fatalf("waiting=%+v", waiting)
	}
}
