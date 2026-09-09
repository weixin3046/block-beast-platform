package lulu

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestSentTransferAssociation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		item       int64
		amount     string
		candidates []withdrawalLink
		linked     bool
		reason     string
	}{
		{"unique completed withdrawal", 102201, "-1", []withdrawalLink{{"order-1", "confirmed"}}, true, ""},
		{"no withdrawal", 102201, "-1", nil, false, "no_matching_withdrawal"},
		{"ambiguous withdrawals", 102201, "-1", []withdrawalLink{{"order-1", "confirmed"}, {"order-2", "confirmed"}}, false, "ambiguous_withdrawal"},
		{"other item", 102202, "-1", nil, false, "unsupported_item"},
		{"wrong sign", 102201, "1", nil, false, "invalid_transfer_amount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := TransferRecord{ItemID: tc.item, Amount: tc.amount}
			applyWithdrawalLink(&r, tc.candidates)
			if r.Linked != tc.linked || r.LinkReason != tc.reason {
				t.Fatalf("unexpected result: %+v", r)
			}
			if tc.linked && (r.OrderID != "order-1" || r.LinkMethod != "account_uid_amount_completion_window") {
				t.Fatalf("missing association basis: %+v", r)
			}
		})
	}
}

func TestWithdrawalLinkDatabase(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	user, walletID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name) VALUES($1,'transfer association test')`, user)
	exec(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'ORIGIN_STONE') ON CONFLICT(user_id,currency) DO NOTHING`, walletID, user)
	if err := pool.QueryRow(ctx, `SELECT id FROM wallets WHERE user_id=$1 AND currency='ORIGIN_STONE'`, user).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 9, 10, 0, 43, 0, time.UTC)
	add := func(receiver, uid, status string, amount int64, when time.Time, manual bool) string {
		t.Helper()
		id := uuid.NewString()
		exec(`INSERT INTO lulu_orders(id,user_id,kind,request_id,lulu_uid,receiver_uid,amount,status) VALUES($1,$2,'withdrawal',$7,$3,$4,$5,$6)`, id, user, uid, receiver, amount, status, id)
		var operator any
		if manual {
			operator = user
		}
		exec(`INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,available_delta_minor,frozen_delta_minor,operator_id,occurred_at) VALUES(gen_random_uuid(),$1,'lulu_withdrawal_debit',$2,'lulu_withdrawal_debit',-1000,0,0,-1000,$3,$4)`, walletID, id, operator, when)
		return id
	}
	service := NewService(pool, "34445963")
	check := func(wantID, reason string) {
		t.Helper()
		r := TransferRecord{ItemID: 102201, Amount: "-1", CounterpartyUID: "57870217", OccurredAt: at}
		if err := service.linkWithdrawal(ctx, "34445963", &r); err != nil {
			t.Fatal(err)
		}
		if r.OrderID != wantID || r.LinkReason != reason {
			t.Fatalf("unexpected association: %+v", r)
		}
	}
	add("11111111", "57870217", "confirmed", 1, at, false)
	add("34445963", "22222222", "confirmed", 1, at, false)
	add("34445963", "57870217", "confirmed", 2, at, false)
	add("34445963", "57870217", "failed", 1, at, false)
	add("34445963", "57870217", "confirmed", 1, at.Add(30*time.Second+time.Microsecond), false)
	add("34445963", "57870217", "confirmed", 1, at.Add(-30*time.Second-time.Microsecond), false)
	add("34445963", "57870217", "confirmed", 1, at, true)
	check("", "no_matching_withdrawal")
	id := add("34445963", "57870217", "confirmed", 1, at.Add(-447*time.Millisecond), false)
	check(id, "")
	// Both inclusive boundaries still uniquely match the same order.
	exec(`UPDATE ledger_entries SET occurred_at=$2 WHERE business_id=$1`, id, at.Add(-30*time.Second))
	check(id, "")
	exec(`UPDATE ledger_entries SET occurred_at=$2 WHERE business_id=$1`, id, at.Add(30*time.Second))
	check(id, "")
	add("34445963", "57870217", "confirmed", 1, at.Add(30*time.Second), false)
	check("", "ambiguous_withdrawal")
}
