package agent

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestAdminCommissionBeneficiaryNames(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	user, player, wallet, gt, round, bet, commission := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO users(id,login_name,display_name) VALUES($1,$2,'收款昵称'),($3,$4,'投注昵称')`, user, "agent-"+user, player, "player-"+player)
	exec(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS')`, wallet, player)
	exec(`INSERT INTO game_types(id,code,name,rules) VALUES($1,$2,'test','{}')`, gt, gt)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,$2,1,'settled',now())`, round, gt)
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status) VALUES($1,$2,$3,$4,$5,'{}',1,'lost')`, bet, bet, round, player, wallet)
	exec(`INSERT INTO commission_entries(id,source_bet_id,beneficiary_user_id,currency,amount_minor,status) VALUES($1,$2,$3,'POINTS',1,'paid')`, commission, bet, user)
	defer func() {
		p.Exec(ctx, `DELETE FROM commission_entries WHERE id=$1`, commission)
		p.Exec(ctx, `DELETE FROM bets WHERE id=$1`, bet)
		p.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, round)
		p.Exec(ctx, `DELETE FROM game_types WHERE id=$1`, gt)
		p.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, wallet)
		p.Exec(ctx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{user, player})
	}()
	for _, status := range []string{"", "paid"} {
		items, e := NewService(p).ListAllCommissions(ctx, status, 100)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, item := range items {
			if item.ID == commission {
				found = true
				if item.AgentID != user || item.LoginName != "agent-"+user || item.DisplayName != "收款昵称" {
					t.Fatalf("wrong beneficiary: %+v", item)
				}
			}
		}
		if !found {
			t.Fatal("missing commission")
		}
	}
}
