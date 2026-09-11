package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListDirectPlayersReturnsMembersAndPeriodIncome(t *testing.T) {
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
	agentID, realID, virtualID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	walletID, gameID, roundID, realBetID, virtualBetID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	realCommissionID, virtualCommissionID := uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,login_name,display_name) VALUES($1,'agent-direct','上级'),($2,'real-direct','真实下级'),($3,'virtual-direct','虚拟下级')`, agentID, realID, virtualID)
	exec(`UPDATE users SET is_virtual=true WHERE id=$1`, virtualID)
	exec(`INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,'real_direct'::ltree),($3,$2,'virtual_direct'::ltree)`, realID, agentID, virtualID)
	exec(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS')`, walletID, realID)
	exec(`INSERT INTO game_types(id,code,name,rules) VALUES($1,$2,'direct-test','{}')`, gameID, gameID)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,$2,1,'settled',now())`, roundID, gameID)
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status) VALUES($1,$2,$3,$4,$5,'{}',1,'lost'),($6,$7,$3,$8,$5,'{}',1,'lost')`, realBetID, realBetID, roundID, realID, walletID, virtualBetID, virtualBetID, virtualID)
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	exec(`INSERT INTO commission_entries(id,source_bet_id,beneficiary_user_id,currency,amount_minor,status,created_at) VALUES($1,$2,$3,'POINTS',7,'paid',$4),($5,$6,$3,'USDT',9,'paid',$7)`, realCommissionID, realBetID, agentID, start.Add(time.Hour), virtualCommissionID, virtualBetID, start.Add(48*time.Hour))
	t.Run("income summary scoped and paid", func(t *testing.T) {
		exec(`UPDATE commission_entries SET created_at=now() WHERE id=ANY($1::uuid[])`, []string{realCommissionID, virtualCommissionID})
		summary, e := NewService(p).IncomeSummary(ctx, agentID)
		if e != nil {
			t.Fatal(e)
		}
		if len(summary.Items) != 4 || len(summary.Items[0].Income) != 2 || summary.Items[0].Income[0].AmountMinor != 7 || summary.Items[0].Income[1].AmountMinor != 9 {
			t.Fatalf("bad summary %+v", summary)
		}
		other, e := NewService(p).IncomeSummary(ctx, realID)
		if e != nil {
			t.Fatal(e)
		}
		if len(other.Items[0].Income) != 0 {
			t.Fatal("income leaked")
		}
		exec(`UPDATE commission_entries SET created_at=$2 WHERE id=$1`, realCommissionID, start.Add(time.Hour))
		exec(`UPDATE commission_entries SET created_at=$2 WHERE id=$1`, virtualCommissionID, start.Add(48*time.Hour))
	})
	defer func() {
		p.Exec(ctx, `DELETE FROM commission_entries WHERE id=ANY($1::uuid[])`, []string{realCommissionID, virtualCommissionID})
		p.Exec(ctx, `DELETE FROM bets WHERE id=ANY($1::uuid[])`, []string{realBetID, virtualBetID})
		p.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, roundID)
		p.Exec(ctx, `DELETE FROM game_types WHERE id=$1`, gameID)
		p.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, walletID)
		p.Exec(ctx, `DELETE FROM agent_relations WHERE user_id=ANY($1::uuid[])`, []string{realID, virtualID})
		p.Exec(ctx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{agentID, realID, virtualID})
	}()

	result, err := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{PlayerType: "real", From: start, To: start.Add(24 * time.Hour), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 {
		t.Fatalf("wrong real page: %+v", result)
	}
	item := result.Items[0]
	if item.AgentLevel != 0 {
		t.Fatalf("ordinary player level=%d", item.AgentLevel)
	}
	exec(`UPDATE users SET agent_level=2 WHERE id=$1`, realID)
	updated, e := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{PlayerType: "real"})
	if e != nil || len(updated.Items) != 1 || updated.Items[0].AgentLevel != 2 {
		t.Fatalf("updated level not returned: %+v %v", updated, e)
	}
	if item.LoginName != "real-direct" || item.IsVirtual || len(item.Income) != 1 || item.Income[0].Currency != "POINTS" || item.Income[0].AmountMinor != 7 {
		t.Fatalf("wrong real member income: %+v", item)
	}

	all, err := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{PlayerType: "all", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 2 || len(all.Items) != 1 || all.Items[0].LoginName != "virtual-direct" || !all.Items[0].IsVirtual || len(all.Items[0].Income) != 1 || all.Items[0].Income[0].Currency != "USDT" || all.Items[0].Income[0].AmountMinor != 9 {
		t.Fatalf("wrong virtual page: %+v", all)
	}
	emptyIncome, err := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{PlayerType: "real", To: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyIncome.Items) != 1 || len(emptyIncome.Items[0].Income) != 0 {
		t.Fatalf("end boundary or member visibility: %+v", emptyIncome)
	}
	exec(`UPDATE commission_entries SET status='reversed' WHERE id=$1`, realCommissionID)
	reversed, err := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{PlayerType: "real"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reversed.Items) != 1 || len(reversed.Items[0].Income) != 0 {
		t.Fatalf("reversed income counted: %+v", reversed)
	}
	other, err := NewService(p).ListDirectPlayers(ctx, realID, DirectPlayerQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Total != 0 || len(other.Items) != 0 {
		t.Fatalf("another parent's members leaked: %+v", other)
	}
	t.Run("descendants and own source income", func(t *testing.T) {
		leafID := uuid.NewString()
		exec(`INSERT INTO users(id,display_name) VALUES($1,'三级下级')`, leafID)
		defer p.Exec(ctx, `DELETE FROM users WHERE id=$1`, leafID)
		exec(`INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,'leaf_direct'::ltree)`, leafID, virtualID)
		defer p.Exec(ctx, `DELETE FROM agent_relations WHERE user_id=$1`, leafID)
		exec(`UPDATE agent_relations SET parent_user_id=$2 WHERE user_id=$1`, virtualID, realID)
		today := incomePeriods(time.Now())[0]
		exec(`UPDATE commission_entries SET status='paid',created_at=$2 WHERE id=$1`, realCommissionID, today.From.Add(-time.Hour))
		exec(`UPDATE commission_entries SET created_at=$2 WHERE id=$1`, virtualCommissionID, today.From)
		result, err := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{From: today.From, To: today.To})
		if err != nil || result.Total != 3 || len(result.Items) != 3 {
			t.Fatalf("descendants: %+v %v", result, err)
		}
		first, second, third := result.Items[0], result.Items[1], result.Items[2]
		if first.Depth != 1 || second.Depth != 2 || third.Depth != 3 || second.ParentUserID != first.UserID || third.ParentUserID != second.UserID {
			t.Fatalf("hierarchy: %+v", result)
		}
		if len(first.Income) != 0 || len(first.TodayIncome) != 0 || len(first.HistoryIncome) != 1 || first.HistoryIncome[0].AmountMinor != 7 {
			t.Fatalf("parent must not include descendant income: %+v", first)
		}
		if len(second.TodayIncome) != 1 || second.TodayIncome[0].AmountMinor != 9 || len(second.HistoryIncome) != 1 || second.HistoryIncome[0].AmountMinor != 9 || len(second.Income) != 1 {
			t.Fatalf("own income: %+v", second)
		}
		if len(third.TodayIncome) != 0 || len(third.HistoryIncome) != 0 {
			t.Fatalf("no income member: %+v", third)
		}
		real, err := NewService(p).ListDirectPlayers(ctx, agentID, DirectPlayerQuery{PlayerType: "real"})
		if err != nil || real.Total != 2 || real.Items[1].Depth != 3 {
			t.Fatalf("filter must traverse virtual intermediary: %+v %v", real, err)
		}
		other, err := NewService(p).ListDirectPlayers(ctx, realID, DirectPlayerQuery{})
		if err != nil || other.Total != 2 || len(other.Items[0].HistoryIncome) != 0 {
			t.Fatalf("other beneficiary income leaked: %+v %v", other, err)
		}
	})
}
