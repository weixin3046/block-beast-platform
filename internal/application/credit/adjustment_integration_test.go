package credit

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWalletAdjustmentsAtomicityConcurrencyAndReports(t *testing.T) {
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
	op, user, virtual := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,display_name,is_virtual) VALUES($1,'adjust admin',false),($2,'adjust player',false),($3,'adjust virtual',true)`, op, user, virtual)
	defer func() {
		pool.Exec(ctx, `DELETE FROM outbox_events WHERE payload->>'user_id'=ANY($1)`, []string{op, user, virtual})
		pool.Exec(ctx, `DELETE FROM admin_wallet_adjustments WHERE operator_id=$1`, op)
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=$1`, op)
		pool.Exec(ctx, `DELETE FROM point_withdrawals WHERE user_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN(SELECT id FROM wallets WHERE user_id=ANY($1::uuid[]))`, []string{user, virtual})
		pool.Exec(ctx, `DELETE FROM wallets WHERE user_id=ANY($1::uuid[])`, []string{user, virtual})
		pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, op)
		pool.Exec(ctx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{op, user, virtual})
	}()
	exec(`INSERT INTO roles(id,code,description) VALUES(gen_random_uuid(),'admin','admin') ON CONFLICT(code) DO NOTHING`)
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, op)
	s := NewService(pool)
	base := AdjustmentInput{UserID: user, OperatorID: op, Currency: "POINTS", Action: "credit", Amount: "10", RequestID: uuid.NewString()}
	first, err := s.AdjustWallet(ctx, base)
	if err != nil || first.BalanceAfterMinor != 10000 || first.Duplicate {
		t.Fatalf("credit %+v %v", first, err)
	}
	dup, err := s.AdjustWallet(ctx, base)
	if err != nil || !dup.Duplicate || dup.OperationID != first.OperationID || !dup.OccurredAt.Equal(first.OccurredAt) {
		t.Fatalf("duplicate %+v %v", dup, err)
	}
	old, err := s.AdminCredit(ctx, AdminCreditInput{UserID: user, OperatorID: op, Currency: "POINTS", Amount: "10.0", RequestID: base.RequestID})
	if err != nil || old.Credited {
		t.Fatalf("legacy alias %+v %v", old, err)
	}
	for _, mutate := range []func(*AdjustmentInput){func(i *AdjustmentInput) { i.Amount = "11" }, func(i *AdjustmentInput) { i.Action = "debit" }, func(i *AdjustmentInput) { i.UserID = virtual }, func(i *AdjustmentInput) { i.Currency = "USDT" }, func(i *AdjustmentInput) { i.Remark = "changed" }} {
		in := base
		mutate(&in)
		if _, err := s.AdjustWallet(ctx, in); !errors.Is(err, ErrAdjustmentConflict) {
			t.Fatalf("mismatch %v", err)
		}
	}
	// 先冻结 2 分，再让两个不同请求同时扣 6 分，只能一个成功。
	withdrawal, err := s.RequestPointWithdrawal(ctx, user, uuid.NewString(), 2000, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := base
			in.Action = "debit"
			in.Amount = "6"
			in.RequestID = uuid.NewString()
			_, err := s.AdjustWallet(ctx, in)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, insufficient := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInsufficientBalance) {
			insufficient++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || insufficient != 1 {
		t.Fatalf("success=%d insufficient=%d", success, insufficient)
	}
	b, err := s.Balance(ctx, user, "POINTS")
	if err != nil || b.AvailableMinor != 2000 || b.FrozenMinor != 2000 {
		t.Fatalf("balance %+v %v", b, err)
	}
	// 赠分与人工扣分各记独立流水，重复并发请求只赠送一次。
	reward := base
	reward.Action = "reward"
	reward.Amount = "1.5"
	reward.RequestID = uuid.NewString()
	errs = make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.AdjustWallet(ctx, reward); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	penalty := base
	penalty.Action = "penalty"
	penalty.Amount = "0.5"
	penalty.RequestID = uuid.NewString()
	result, err := s.AdjustWallet(ctx, penalty)
	if err != nil || result.BalanceBeforeMinor != 3500 || result.BalanceAfterMinor != 3000 || result.DeltaMinor != -500 || result.FrozenMinor != 2000 {
		t.Fatalf("penalty %+v %v", result, err)
	}
	for _, action := range []string{"credit", "reward", "debit", "penalty"} {
		in := base
		in.UserID = virtual
		in.Action = action
		in.RequestID = uuid.NewString()
		_, e := s.AdjustWallet(ctx, in)
		if action == "debit" || action == "penalty" {
			if !errors.Is(e, ErrVirtualAccountWithdrawal) {
				t.Fatal(e)
			}
		} else if e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		amount, currency string
		want             error
	}{{"0", "POINTS", ErrInvalidAmount}, {"-1", "POINTS", ErrInvalidAmount}, {"0.0001", "POINTS", ErrInvalidAmount}, {"1", "MISSING", ErrInvalidCurrency}} {
		in := base
		in.Amount = tc.amount
		in.Currency = tc.currency
		in.RequestID = uuid.NewString()
		if _, e := s.AdjustWallet(ctx, in); !errors.Is(e, tc.want) {
			t.Fatalf("validation %v", e)
		}
	}
	in := base
	in.OperatorID = user
	in.RequestID = uuid.NewString()
	if _, e := s.AdjustWallet(ctx, in); !errors.Is(e, ErrAdjustmentForbidden) {
		t.Fatalf("role %v", e)
	}
	// 审核成功只计最终扣款；冻结不能进入清退金额。
	if err = s.ReviewPointWithdrawal(ctx, withdrawal.ID, op, true); err != nil {
		t.Fatal(err)
	}
	reports := operations.NewService(pool)
	var publicID int64
	if err = pool.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, user).Scan(&publicID); err != nil {
		t.Fatal(err)
	}
	dash, err := reports.Dashboard(ctx, "", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range dash.Players {
		if p.UserID == publicID {
			for _, f := range p.Funds {
				if f.Currency == "POINTS" {
					found = true
					if f.CreditMinor != 10000 || f.ClearanceMinor != 8000 || f.GiftMinor != 1500 || f.PenaltyMinor != 500 {
						t.Fatalf("funds %+v", f)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("missing player funds")
	}
	// 全局与真实玩家资金完全一致（隔离测试库中其他用例不并行）。
	for _, g := range dash.Global {
		if g.Currency == "POINTS" && (g.ClearanceMinor != 8000 || g.GiftMinor != 1500 || g.PenaltyMinor != 500) {
			t.Fatalf("global includes virtual or double counted: %+v", g)
		}
	}
	clearances, err := reports.ListRefundClearances(ctx, "", "", time.Time{}, time.Time{}, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, c := range clearances {
		if c.UserID == publicID && c.RecordType == "admin_debit" {
			found = true
			if c.AmountMinor != 6000 || c.BalanceAfterMinor != 2000 {
				t.Fatalf("clearance %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("missing direct debit")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='wallet.ledger.committed' AND payload->>'user_id'=ANY($1)`, []string{user, virtual}).Scan(&count); err != nil || count != 8 {
		t.Fatalf("outbox events %d %v", count, err)
	}
	// 成功流水、幂等记录和审计数量一致，失败和重放不留下新记录。
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND action LIKE 'admin.wallet.%'`, op).Scan(&count); err != nil || count != 6 {
		t.Fatalf("audit %d %v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM admin_wallet_adjustments WHERE operator_id=$1`, op).Scan(&count); err != nil || count != 6 {
		t.Fatalf("idempotency rows %d %v", count, err)
	}
	// 通过针对本次请求的测试约束模拟写入末期失败，验证余额/流水/outbox全部回滚。
	failed := base
	failed.RequestID = uuid.NewString()
	failed.Amount = "1"
	exec(`ALTER TABLE admin_wallet_adjustments ADD CONSTRAINT test_adjustment_rollback CHECK(request_id <> '` + failed.RequestID + `') NOT VALID`)
	defer pool.Exec(ctx, `ALTER TABLE admin_wallet_adjustments DROP CONSTRAINT IF EXISTS test_adjustment_rollback`)
	if _, e := s.AdjustWallet(ctx, failed); e == nil {
		t.Fatal("expected injected persistence failure")
	}
	b, err = s.Balance(ctx, user, "POINTS")
	if err != nil || b.AvailableMinor != 3000 || b.FrozenMinor != 0 {
		t.Fatalf("rollback balance %+v %v", b, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='wallet.ledger.committed' AND payload->>'user_id'=ANY($1)`, []string{user, virtual}).Scan(&count); err != nil || count != 8 {
		t.Fatalf("rollback outbox %d %v", count, err)
	}
	// 虽然冻结资金不可扣，上分也必须预留其解冻空间，避免总余额溢出。
	exec(`UPDATE wallets SET available_minor=9223372036854775307,frozen_minor=500 WHERE user_id=$1 AND currency='POINTS'`, user)
	in = base
	in.Amount = "0.001"
	in.RequestID = uuid.NewString()
	if _, e := s.AdjustWallet(ctx, in); !errors.Is(e, ErrInvalidAmount) {
		t.Fatalf("overflow %v", e)
	}
	exec(`UPDATE wallets SET available_minor=3000,frozen_minor=0 WHERE user_id=$1 AND currency='POINTS'`, user)
	// 升级前旧上分流水也应安全重放，不重复入账。
	legacyRequest := uuid.NewString()
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	legacyBalance, e := addBalance(ctx, tx, user, "POINTS", 1000)
	if e != nil {
		t.Fatal(e)
	}
	if e = writeLedger(ctx, tx, user, "POINTS", BizAdminCredit, legacyRequest, 1000, legacyBalance, "", op); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	in = base
	in.Amount = "1"
	in.RequestID = legacyRequest
	legacy, e := s.AdjustWallet(ctx, in)
	if e != nil || !legacy.Duplicate || legacy.BalanceAfterMinor != 4000 {
		t.Fatalf("legacy replay %+v %v", legacy, e)
	}
	in.Action = "debit"
	if _, e = s.AdjustWallet(ctx, in); !errors.Is(e, ErrAdjustmentConflict) {
		t.Fatalf("legacy conflict %v", e)
	}
	// operator 与 admin 使用同一资金用例，数据库权限复核也必须允许。
	exec(`INSERT INTO roles(id,code,description) VALUES(gen_random_uuid(),'operator','operator') ON CONFLICT(code) DO NOTHING`)
	exec(`DELETE FROM user_roles WHERE user_id=$1`, op)
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='operator'`, op)
	in.RequestID = uuid.NewString()
	result, e = s.AdjustWallet(ctx, in)
	if e != nil || result.BalanceAfterMinor != 3000 {
		t.Fatalf("operator debit %+v %v", result, e)
	}
	in.Action = "credit"
	in.RequestID = uuid.NewString()
	result, e = s.AdjustWallet(ctx, in)
	if e != nil || result.BalanceAfterMinor != 4000 {
		t.Fatalf("operator credit %+v %v", result, e)
	}
}
