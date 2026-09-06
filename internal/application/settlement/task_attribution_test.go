package settlement

import (
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

type dateTaskHook struct {
	calls int
	at    time.Time
}

func (h *dateTaskHook) OnBetSettled(_ context.Context, _ pgx.Tx, _, _ string, _ int64, at time.Time) error {
	h.calls++
	h.at = at
	return nil
}
func TestTasksExcludeDodgeAndUsePlacedAt(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	for _, mode := range []string{"road", "dodge"} {
		t.Run(mode, func(t *testing.T) {
			exec := func(q string, a ...any) {
				t.Helper()
				if _, e = p.Exec(ctx, q, a...); e != nil {
					t.Fatal(e)
				}
			}
			user, round := uuid.NewString(), uuid.NewString()
			exec("INSERT INTO users(id,display_name) VALUES($1,'task attribution')", user)
			exec("INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',10000)", uuid.NewString(), user)
			exec("INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,'09000000-0000-4000-8000-000000000001',$2,'open',now()+interval '1 hour')", round, time.Now().UnixNano())
			pick := "odd"
			if mode == "dodge" {
				pick = "2"
			}
			selection, _ := json.Marshal(map[string]string{"pick": pick})
			bet, e := betting.NewService(p).PlaceBet(ctx, betting.PlaceBetRequest{AccountID: user, RoundID: round, ClientRequestID: uuid.NewString(), Currency: "POINTS", StakeMinor: 1000, GameRoomID: "94000000-0000-4000-8000-000000000001", PlayMode: mode, Selection: selection})
			if e != nil {
				t.Fatal(e)
			}
			placed := time.Date(2026, 9, 1, 15, 59, 0, 0, time.UTC)
			exec("UPDATE bets SET created_at=$2 WHERE id=$1", bet.BetID, placed)
			exec("UPDATE rounds SET status='closed' WHERE id=$1", round)
			var raw []byte
			if e = p.QueryRow(ctx, "SELECT rules FROM game_types WHERE id='09000000-0000-4000-8000-000000000001'").Scan(&raw); e != nil {
				t.Fatal(e)
			}
			rules, e := game.ParseRules(raw)
			if e != nil {
				t.Fatal(e)
			}
			hook := &dateTaskHook{}
			if _, e = NewService(p).WithTaskHook(hook).SettleRound(ctx, round, []string{"1", "odd", "small"}, rules); e != nil {
				t.Fatal(e)
			}
			if mode == "road" && (hook.calls != 1 || !hook.at.Equal(placed)) {
				t.Fatalf("wrong date: %+v", hook)
			}
			if mode == "dodge" && hook.calls != 0 {
				t.Fatalf("dodge accumulated: %+v", hook)
			}
		})
	}
}
