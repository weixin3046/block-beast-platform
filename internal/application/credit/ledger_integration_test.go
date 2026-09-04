package credit

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
)

func TestUnifiedLedgerBalancesIdempotencyAndCursor(t *testing.T) {
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
	user := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'ledger test')`, user); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, `DELETE FROM admin_wallet_adjustments WHERE operator_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM outbox_events WHERE payload->>'user_id'=$1`, user)
		pool.Exec(ctx, `DELETE FROM point_withdrawals WHERE user_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN(SELECT id FROM wallets WHERE user_id=$1)`, user)
		pool.Exec(ctx, `DELETE FROM wallets WHERE user_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
	}()
	s := NewService(pool)
	if _, err = pool.Exec(ctx, `INSERT INTO roles(id,code,description) VALUES(gen_random_uuid(),'admin','admin') ON CONFLICT(code) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, user); err != nil {
		t.Fatal(err)
	}
	input := AdminCreditInput{UserID: user, Currency: "POINTS", Amount: "100", RequestID: uuid.NewString(), OperatorID: user}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.AdminCredit(ctx, input); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	balance, err := s.Balance(ctx, user, "POINTS")
	if err != nil || balance.AvailableMinor != 100000 || balance.Available != "100.000" {
		t.Fatalf("balance %+v %v", balance, err)
	}
	if _, err = s.AdminCredit(ctx, AdminCreditInput{UserID: user, Currency: "POINTS", Amount: "1.0001", RequestID: uuid.NewString(), OperatorID: user}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("precision %v", err)
	}
	withdrawal, err := s.RequestPointWithdrawal(ctx, user, uuid.NewString(), 1500, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewPointWithdrawal(ctx, withdrawal.ID, user, false); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListUnifiedLedger(ctx, user, "POINTS", "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page %+v %v", page, err)
	}
	unfreeze, freeze := page.Items[0], page.Items[1]
	if unfreeze.AvailableDeltaMinor != 1500 || unfreeze.FrozenDeltaMinor != -1500 || unfreeze.FrozenAfterMinor == nil || *unfreeze.FrozenAfterMinor != 0 {
		t.Fatalf("unfreeze %+v", unfreeze)
	}
	if freeze.AvailableDeltaMinor != -1500 || freeze.FrozenDeltaMinor != 1500 || freeze.AvailableAfter != "98.500" {
		t.Fatalf("freeze %+v", freeze)
	}
	tail, err := s.ListUnifiedLedger(ctx, user, "POINTS", page.NextCursor, 2)
	if err != nil || len(tail.Items) != 1 || tail.NextCursor != "" {
		t.Fatalf("tail %+v %v", tail, err)
	}
	if _, err = s.ListUnifiedLedger(ctx, user, "POINTS", "bad", 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor %v", err)
	}
	other, err := s.ListUnifiedLedger(ctx, uuid.NewString(), "", "", 10)
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("isolation %+v %v", other, err)
	}
	var events int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='wallet.ledger.committed' AND payload->>'user_id'=$1`, user).Scan(&events); err != nil || events != 3 {
		t.Fatalf("events %d %v", events, err)
	}
	approved, err := s.RequestPointWithdrawal(ctx, user, uuid.NewString(), 1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewPointWithdrawal(ctx, approved.ID, user, true); err != nil {
		t.Fatal(err)
	}
	paid, err := s.ListUnifiedLedger(ctx, user, "POINTS", "", 1)
	if err != nil || len(paid.Items) != 1 {
		t.Fatalf("paid %+v %v", paid, err)
	}
	v := paid.Items[0]
	if v.AvailableDeltaMinor != 0 || v.FrozenDeltaMinor != -1000 || v.AvailableAfterMinor != 99000 || v.FrozenAfterMinor == nil || *v.FrozenAfterMinor != 0 {
		t.Fatalf("confirmed withdrawal %+v", v)
	}
}
