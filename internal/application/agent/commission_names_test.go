package agent

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
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
	// Exercise the upgrade against legacy rows, rolling back schema and ledger
	// fixtures so this test does not alter other integration tests' database.
	t.Run("historical migration", func(t *testing.T) {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		run := func(q string, args ...any) {
			t.Helper()
			if _, err := tx.Exec(ctx, q, args...); err != nil {
				t.Fatal(err)
			}
		}
		run(`ALTER TABLE commission_entries DROP COLUMN created_at`)
		unknown, beneficiaryWallet := uuid.NewString(), uuid.NewString()
		run(`INSERT INTO commission_entries(id,source_bet_id,beneficiary_user_id,currency,amount_minor,status) VALUES($1,$2,$3,'POINTS',1,'paid')`, unknown, bet, player)
		run(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS')`, beneficiaryWallet, user)
		oldTime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		run(`INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,occurred_at) VALUES($1,$2,'commission',$3,'commission_credit',1,1,$4)`, uuid.NewString(), beneficiaryWallet, commission, oldTime)
		migration, err := os.ReadFile("../../../migrations/0082_commission_created_at.sql")
		if err != nil {
			t.Fatal(err)
		}
		run(string(migration))
		var got *time.Time
		if err := tx.QueryRow(ctx, `SELECT created_at FROM commission_entries WHERE id=$1`, commission).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == nil || !got.Equal(oldTime) {
			t.Fatalf("historical credit time=%v", got)
		}
		if err := tx.QueryRow(ctx, `SELECT created_at FROM commission_entries WHERE id=$1`, unknown).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("fabricated historical time=%v", got)
		}
	})
	for _, status := range []string{"", "paid"} {
		items, e := NewService(p).ListAllCommissions(ctx, status, "", 100, time.Time{}, time.Time{})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, item := range items {
			if item.ID == commission {
				found = true
				var publicID int64
				if err := p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, player).Scan(&publicID); err != nil {
					t.Fatal(err)
				}
				if item.SourceUserID != publicID || item.SourceLoginName != "player-"+player || item.SourceDisplayName != "投注昵称" || item.CreatedAt == nil {
					t.Fatalf("wrong source or missing time: %+v", item)
				}
				if item.AgentID != user || item.LoginName != "agent-"+user || item.DisplayName != "收款昵称" {
					t.Fatalf("wrong beneficiary: %+v", item)
				}
			}
		}
		if !found {
			t.Fatal("missing commission")
		}
	}
	at := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	exec(`UPDATE commission_entries SET created_at=$2 WHERE id=$1`, commission, at)
	details, e := NewService(p).ListCommissionDetails(ctx, user, "POINTS", at, at.Add(time.Hour), 100)
	if e != nil || len(details) != 1 {
		t.Fatalf("details=%+v err=%v", details, e)
	}
	var sourceID int64
	if err := p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, player).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if details[0].SourceUserID != sourceID || details[0].SourceLoginName != "player-"+player || details[0].SourceDisplayName != "投注昵称" || details[0].AgentID != user {
		t.Fatalf("wrong source player: %+v", details[0])
	}
	if details[0].GameName != "test" || details[0].GameType != gt || details[0].Sequence != 1 || details[0].CreatedAt == nil || !details[0].CreatedAt.Equal(at) || string(details[0].Selection) != "{}" {
		t.Fatalf("wrong game metadata %+v", details[0])
	}
	if details[0].StakeMinor != 1 || details[0].RebateBaseMinor != 1 || details[0].RebateRateBasisPoints != 10000 {
		t.Fatalf("wrong stake/base/rate: %+v", details[0])
	}
	for _, tc := range []struct {
		owner, currency string
		from, to        time.Time
	}{{user, "USDT", at, at.Add(time.Hour)}, {player, "POINTS", at, at.Add(time.Hour)}, {user, "POINTS", at.Add(-time.Hour), at}} {
		got, e := NewService(p).ListCommissionDetails(ctx, tc.owner, tc.currency, tc.from, tc.to, 100)
		if e != nil || len(got) != 0 {
			t.Fatalf("filter failed %+v %v", got, e)
		}
	}
	for _, tc := range []struct {
		name     string
		from, to time.Time
		want     bool
	}{
		{"start inclusive", at, at.Add(time.Hour), true},
		{"end exclusive", at.Add(-time.Hour), at, false},
		{"from only", at.Add(time.Second), time.Time{}, false},
		{"to only", time.Time{}, at.Add(time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := NewService(p).ListAllCommissions(ctx, "paid", "", 100, tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range items {
				if item.ID == commission {
					found = true
					if item.CreatedAt == nil || !item.CreatedAt.Equal(at) {
						t.Fatalf("wrong time: %+v", item)
					}
				}
			}
			if found != tc.want {
				t.Fatalf("found=%v want=%v", found, tc.want)
			}
		})
	}
	exec(`UPDATE commission_entries SET created_at=NULL WHERE id=$1`, commission)
	items, err := NewService(p).ListAllCommissions(ctx, "paid", "", 100, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ID == commission {
			found = true
			if item.CreatedAt != nil {
				t.Fatal("unknown historical time must be null")
			}
		}
	}
	if !found {
		t.Fatal("unknown historical time hidden without filter")
	}
	items, err = NewService(p).ListAllCommissions(ctx, "", "", 100, at.Add(-time.Hour), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == commission {
			t.Fatal("unknown historical time included in filtered results")
		}
	}
	items, err = NewService(p).ListAllCommissions(ctx, "paid", "JADE", 100, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == commission {
			t.Fatal("currency filter returned a different currency")
		}
	}
	items, err = NewService(p).ListAllCommissions(ctx, "paid", "POINTS", 100, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, item := range items {
		if item.ID == commission {
			found = true
		}
	}
	if !found {
		t.Fatal("currency filter omitted matching currency")
	}
}
