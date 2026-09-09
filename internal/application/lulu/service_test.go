package lulu

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegerAmountsAndUID(t *testing.T) {
	for _, v := range []string{"", "0", "-1", "1.0", "1e3", " 1", "9223372036854775808", "9223372036854776", "NaN"} {
		if _, err := ParseAmount(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
	for _, v := range []string{"1", "001", "9223372036854775"} {
		if _, err := ParseAmount(v); err != nil {
			t.Errorf("rejected %q", v)
		}
	}
	for _, v := range []string{"", "123&x=1", "00123", "-123", "12", "123456789012345678901"} {
		if ValidUID(v) {
			t.Errorf("accepted UID %q", v)
		}
	}
}

type fakeProvider struct {
	result TransferResult
	mu     sync.Mutex
	calls  int
}

func (p *fakeProvider) Transfer(context.Context, Order) TransferResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.result
}
func (p *fakeProvider) Receipts(context.Context, time.Time, func(Receipt) error) error { return nil }

func TestOrdersAtomicityConcurrencyAndRecovery(t *testing.T) {
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
	user, op, other, virtual := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name,is_virtual) VALUES($1,'lulu test player',false),($2,'lulu test admin',false),($3,'lulu test other',false),($4,'lulu test virtual',true)`, user, op, other, virtual)
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT u,r.id FROM unnest($1::uuid[]) u CROSS JOIN roles r WHERE r.code='player'`, []string{user, other, virtual})
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, op)
	s := NewService(pool, "987654321").WithEncryptionKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	exec(`UPDATE lulu_config SET receiver_uid=$1,enabled=true WHERE singleton`, s.Receiver)
	balances := func(wantA, wantF int64) {
		t.Helper()
		var a, f int64
		err := pool.QueryRow(ctx, `SELECT available_minor,frozen_minor FROM wallets WHERE user_id=$1 AND currency='ORIGIN_STONE'`, user).Scan(&a, &f)
		if err != nil || a != wantA*1000 || f != wantF*1000 {
			t.Fatalf("balance %d/%d want %d/%d: %v", a, f, wantA, wantF, err)
		}
	}
	for _, kind := range []string{"deposit", "withdrawal"} {
		if _, err := s.Create(ctx, user, kind, Input{RequestID: uuid.NewString(), LuluUID: s.Receiver, Amount: "1"}); !errors.Is(err, ErrSameAccount) {
			t.Fatalf("same account %s: %v", kind, err)
		}
	}
	in := Input{RequestID: uuid.NewString(), LuluUID: "1234567", Amount: "100"}
	if _, err = s.Create(ctx, virtual, "deposit", in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("virtual: %v", err)
	}
	dep, err := s.Create(ctx, user, "deposit", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(ctx, dep.ID, op, "reject", "deposit cannot be reviewed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("deposit review: %v", err)
	}
	if dep.ExpiresAt.Sub(dep.CreatedAt) != 3*time.Minute {
		t.Fatalf("unexpected deadline: %v", dep.ExpiresAt.Sub(dep.CreatedAt))
	}
	if dep.Currency != "ORIGIN_STONE" || dep.Amount != "100" {
		t.Fatalf("wrong deposit units: %+v", dep)
	}
	dup, err := s.Create(ctx, user, "deposit", in)
	if err != nil || dup.ID != dep.ID {
		t.Fatalf("duplicate %+v %v", dup, err)
	}
	altered := in
	altered.Amount = "101"
	if _, err = s.Create(ctx, user, "deposit", altered); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request: %v", err)
	}
	if _, err = s.Create(ctx, other, "deposit", in); !errors.Is(err, ErrConflict) {
		t.Fatalf("sender reservation: %v", err)
	} else {
		var pending *PendingDepositError
		if !errors.As(err, &pending) || pending.LuluUID != in.LuluUID || pending.RetryAfterSeconds < 1 || pending.RetryAfterSeconds > 180 {
			t.Fatalf("pending countdown: %v", err)
		}
	}
	receipt := Receipt{ID: uuid.NewString(), ReceiverUID: s.Receiver, SenderUID: in.LuluUID, Amount: 100, OccurredAt: time.Now().UTC().Truncate(time.Microsecond)}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.RecordReceipt(ctx, receipt) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	balances(100, 0)
	badReceipt := receipt
	badReceipt.Amount = 101
	if err = s.RecordReceipt(ctx, badReceipt); !errors.Is(err, ErrConflict) {
		t.Fatalf("receipt changed: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE business_id=$1`, dep.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger %d %v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='wallet.ledger.committed' AND payload->>'business_id'=$1`, dep.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outbox %d %v", count, err)
	}
	withdraw := func(amount string) Order {
		t.Helper()
		o, e := s.Create(ctx, user, "withdrawal", Input{RequestID: uuid.NewString(), LuluUID: "7654321", Amount: amount})
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	w := withdraw("40")
	balances(60, 40)
	if _, err = s.Review(ctx, w.ID, other, "approve", "checked"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("nonadmin: %v", err)
	}
	if _, err = s.Review(ctx, w.ID, op, "approve", "recipient checked"); err != nil {
		t.Fatal(err)
	}
	balances(60, 40)
	p := &fakeProvider{result: TransferUnknown}
	if err = s.Dispatch(ctx, p); err != nil {
		t.Fatal(err)
	}
	balances(60, 40)
	if err = s.Dispatch(ctx, p); err != nil || p.calls != 1 {
		t.Fatalf("unknown resent: %d %v", p.calls, err)
	}
	if _, err = s.Review(ctx, w.ID, op, "reject", "cannot reject sent"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown refunded through reject: %v", err)
	}
	if _, err = s.Review(ctx, w.ID, op, "confirm_paid", "external history checked"); err != nil {
		t.Fatal(err)
	}
	balances(60, 0)
	if _, err = s.Review(ctx, w.ID, op, "confirm_paid", "external history checked"); err != nil {
		t.Fatal(err)
	}
	balances(60, 0)
	w = withdraw("10")
	if _, err = s.Review(ctx, w.ID, op, "reject", "wrong recipient"); err != nil {
		t.Fatal(err)
	}
	balances(60, 0)
	w = withdraw("10")
	if _, err = s.Review(ctx, w.ID, op, "approve", "checked"); err != nil {
		t.Fatal(err)
	}
	p.result = TransferConfirmed
	if err = s.Dispatch(ctx, p); err != nil {
		t.Fatal(err)
	}
	balances(50, 0)
	w = withdraw("10")
	if _, err = s.Review(ctx, w.ID, op, "approve", "checked"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE lulu_orders SET status='sending' WHERE id=$1`, w.ID)
	if err = s.RecoverSending(ctx); err != nil {
		t.Fatal(err)
	}
	previous := p.calls
	if err = s.Dispatch(ctx, p); err != nil || p.calls != previous {
		t.Fatalf("recovered order resent: %v", err)
	}
	balances(40, 10)
	if _, err = s.Review(ctx, w.ID, op, "confirm_not_paid", "external transfer confirmed absent"); err != nil {
		t.Fatal(err)
	}
	balances(50, 0)
	// Concurrent withdrawals cannot overdraw; even successful duplicate submissions only freeze once.
	duplicate := Input{RequestID: uuid.NewString(), LuluUID: "7654321", Amount: "30"}
	errs = make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Create(ctx, user, "withdrawal", duplicate); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	balances(20, 30)
	if _, err = s.Create(ctx, user, "withdrawal", Input{RequestID: uuid.NewString(), LuluUID: "7654321", Amount: "21"}); !errors.Is(err, wallet.ErrInsufficientFunds) {
		t.Fatalf("overdraw %v", err)
	}
	if list, e := s.List(ctx, other, "", "", "", 100, 0); e != nil || len(list) != 0 {
		t.Fatalf("other user's orders: %+v %v", list, e)
	}
	// Late polling after claim expiration still honors actual transfer time.
	lateIn := Input{RequestID: uuid.NewString(), LuluUID: "2345678", Amount: "7"}
	late, e := s.Create(ctx, user, "deposit", lateIn)
	if e != nil {
		t.Fatal(e)
	}
	exec(`UPDATE lulu_orders SET expires_at=now()-interval '1 second',created_at=now()-interval '2 minutes' WHERE id=$1`, late.ID)
	if e = s.ExpireDeposits(ctx); e != nil {
		t.Fatal(e)
	}
	var expiredStatus string
	if e = pool.QueryRow(ctx, `SELECT status FROM lulu_orders WHERE id=$1`, late.ID).Scan(&expiredStatus); e != nil || expiredStatus != "expired" {
		t.Fatalf("expiry: %s %v", expiredStatus, e)
	}
	exec(`UPDATE lulu_orders SET expires_at=now()+interval '1 minute' WHERE id=$1`, late.ID)
	if e = s.RecordReceipt(ctx, Receipt{ID: uuid.NewString(), ReceiverUID: s.Receiver, SenderUID: lateIn.LuluUID, Amount: 7, OccurredAt: time.Now().UTC().Truncate(time.Microsecond)}); e != nil {
		t.Fatal(e)
	}
	balances(27, 30)
	// Metadata mismatch and historical receipts remain unclaimed.
	for i, amount := range []int64{8, 9} {
		r := Receipt{ID: uuid.NewString(), ReceiverUID: s.Receiver, SenderUID: lateIn.LuluUID, Amount: amount, OccurredAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)}
		if e = s.RecordReceipt(ctx, r); e != nil {
			t.Fatal(strconv.Itoa(i), e)
		}
	}
	balances(27, 30)
	// Shared configuration is authoritative, including services constructed with stale UIDs.

	// Even an empty configuration cannot adopt an account while orders are active.
	exec(`UPDATE lulu_config SET receiver_uid='',enabled=false WHERE singleton`)
	initial, e := s.Config(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: "999888777", Enabled: true, Version: initial.Version}); !errors.Is(e, ErrConfigConflict) {
		t.Fatalf("adopted wrong pending account: %v", e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: s.Receiver, Enabled: true, Version: initial.Version, loginToken: "test-token", ProtocolKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), APIURL: func() *string { v := "https://example.invalid"; return &v }(), ScanStartAt: func() *time.Time { v := time.Now().Add(-time.Hour); return &v }()}); !errors.Is(e, ErrConfigConflict) {
		t.Fatalf("adopted pending account: %v", e)
	}
	exec(`UPDATE lulu_config SET receiver_uid=$1 WHERE singleton`, s.Receiver)
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: s.Receiver, Enabled: true, Version: initial.Version, loginToken: "test-token", ProtocolKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), APIURL: func() *string { v := "https://example.invalid"; return &v }(), ScanStartAt: func() *time.Time { v := time.Now().Add(-time.Hour); return &v }()}); e != nil {
		t.Fatal(e)
	}
	c, e := s.Config(ctx)
	if e != nil {
		t.Fatal(e)
	}
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='operator'`, other)
	if _, e = s.UpdateConfig(ctx, user, ConfigUpdate{ReceiverUID: s.Receiver, Enabled: false, Version: c.Version}); !errors.Is(e, ErrForbidden) {
		t.Fatalf("player changed config: %v", e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: "bad", Enabled: true, Version: c.Version}); !errors.Is(e, ErrConfigInvalid) {
		t.Fatalf("invalid config: %v", e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: "999888777", Enabled: true, Version: c.Version}); !errors.Is(e, ErrConfigConflict) {
		t.Fatalf("switched enabled account: %v", e)
	}
	paused, e := s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: s.Receiver, Enabled: false, Version: c.Version})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: s.Receiver, Enabled: true, Version: c.Version}); !errors.Is(e, ErrConfigConflict) {
		t.Fatalf("stale version: %v", e)
	}
	if _, e = s.Create(ctx, user, "deposit", Input{RequestID: uuid.NewString(), LuluUID: "777666555", Amount: "1"}); !errors.Is(e, ErrDisabled) {
		t.Fatalf("ignored disabled database config: %v", e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: "999888777", Enabled: false, Version: paused.Version}); !errors.Is(e, ErrConfigConflict) {
		t.Fatalf("switched with pending order: %v", e)
	}
	replay, e := s.Create(ctx, user, "withdrawal", duplicate)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Review(ctx, replay.ID, op, "reject", "config switch test"); e != nil {
		t.Fatal(e)
	}
	switched, e := s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: "999888777", Enabled: true, Version: paused.Version, loginToken: "new-test-token"})
	if e != nil {
		t.Fatal(e)
	}
	if switched.Version != paused.Version+1 {
		t.Fatal("version not incremented")
	}
	fresh, e := s.Create(ctx, user, "deposit", Input{RequestID: uuid.NewString(), LuluUID: "777666555", Amount: "1"})
	if e != nil || fresh.ReceiverUID != "999888777" {
		t.Fatalf("stale constructor selected account: %+v %v", fresh, e)
	}
	var oldReceiver string
	if e = pool.QueryRow(ctx, `SELECT receiver_uid FROM lulu_orders WHERE id=$1`, dep.ID).Scan(&oldReceiver); e != nil || oldReceiver != s.Receiver {
		t.Fatal("old snapshot was overwritten", e)
	}
	var audits int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND action='lulu.config.update'`, op).Scan(&audits); e != nil || audits != 3 {
		t.Fatalf("config audit count %d: %v", audits, e)
	}

	runtime, e := s.RuntimeConfig(ctx)
	if e != nil || runtime.Token != "new-test-token" || !runtime.ProtocolKeyConfigured {
		t.Fatalf("runtime config error %v", e)
	}
	public, e := s.Config(ctx)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(public)
	if bytes.Contains(encoded, []byte("new-test-token")) {
		t.Fatal("token returned publicly")
	}
	var cipher []byte
	if e = pool.QueryRow(ctx, `SELECT token_cipher FROM lulu_config`).Scan(&cipher); e != nil || bytes.Contains(cipher, []byte("new-test-token")) {
		t.Fatal("unencrypted token", e)
	}
	var leaked bool
	if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_logs WHERE payload::text LIKE '%new-test-token%' OR payload::text LIKE '%test-token%')`).Scan(&leaked); e != nil || leaked {
		t.Fatal("audit leaked token", e)
	}
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: public.ReceiverUID, Enabled: true, Version: public.Version}); e != nil {
		t.Fatal(e)
	}
	runtime, e = s.RuntimeConfig(ctx)
	if e != nil || runtime.Token != "new-test-token" {
		t.Fatal("blank update lost token", e)
	}
	exec(`INSERT INTO lulu_collector_state(receiver_uid,scanned_at) VALUES($1,now()) ON CONFLICT(receiver_uid) DO UPDATE SET scanned_at=now()`, public.ReceiverUID)
	later := time.Now().Add(-time.Minute)
	if _, e = s.UpdateConfig(ctx, op, ConfigUpdate{ReceiverUID: runtime.ReceiverUID, Enabled: true, Version: runtime.Version, ScanStartAt: &later}); !errors.Is(e, ErrConfigConflict) {
		t.Fatal("allowed skipping scan history", e)
	}

	login := &loginStub{uid: runtime.ReceiverUID, token: "sms-test-token"}
	s.WithLoginFactory(func(base, key string) (LoginProvider, error) { return login, nil })
	if e = s.SendLoginCode(ctx, user, "13800000000", runtime.Version); !errors.Is(e, ErrForbidden) {
		t.Fatal("nonadmin sent SMS", e)
	}
	if e = s.SendLoginCode(ctx, other, "13800000000", runtime.Version); e != nil {
		t.Fatal(e)
	}
	if e = s.SendLoginCode(ctx, op, "13800000000", runtime.Version); !errors.Is(e, ErrLoginLimited) {
		t.Fatal("missing SMS limit", e)
	}
	logged, e := s.PhoneLogin(ctx, other, "13800000000", "123456", runtime.Version)
	if e != nil || logged.ReceiverUID != runtime.ReceiverUID {
		t.Fatal("SMS login", e)
	}
	saved, e := s.RuntimeConfig(ctx)
	if e != nil || saved.Token != "sms-test-token" {
		t.Fatal("SMS token not saved", e)
	}
	if _, e = s.PhoneLogin(ctx, op, "13800000000", "123456", logged.Version); !errors.Is(e, ErrLoginLimited) {
		t.Fatal("missing login limit", e)
	}
	exec(`UPDATE lulu_login_limits SET next_allowed_at=now()-interval '1 second' WHERE action='login'`)
	login.err = errors.New("provider error containing secret")
	if _, e = s.PhoneLogin(ctx, op, "13800000000", "123456", logged.Version); !errors.Is(e, ErrLoginFailed) {
		t.Fatal("provider failure", e)
	}
	saved, e = s.RuntimeConfig(ctx)
	if e != nil || saved.Token != "sms-test-token" {
		t.Fatal("failure overwrote token", e)
	}
	if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_logs WHERE payload::text LIKE '%sms-test-token%' OR payload::text LIKE '%123456%')`).Scan(&leaked); e != nil || leaked {
		t.Fatal("SMS credential audit leak", e)
	}

}

type loginStub struct {
	uid, token string
	err        error
	calls      int
}

func (p *loginStub) SendCode(context.Context, string) error { p.calls++; return p.err }
func (p *loginStub) PhoneLogin(context.Context, string, string) (string, string, error) {
	p.calls++
	return p.uid, p.token, p.err
}

func TestListRejectsInvalidStatusBeforeQuery(t *testing.T) {
	s := NewService(nil, "")
	for _, status := range []string{"matched", "typo", "CONFIRMED"} {
		if _, err := s.List(context.Background(), "", "", "", status, 50, 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("status %q: %v", status, err)
		}
	}
}

type healthProvider struct {
	fakeProvider
	err error
}

func (p *healthProvider) Receipts(context.Context, time.Time, func(Receipt) error) error {
	return p.err
}
func TestCollectionHealthRecovery(t *testing.T) {
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
	uid := "987123456"
	if _, err = pool.Exec(ctx, `UPDATE lulu_config SET receiver_uid=$1,enabled=true WHERE singleton`, uid); err != nil {
		t.Fatal(err)
	}
	s := NewService(pool, uid)
	p := &healthProvider{err: ErrTokenInvalid}
	start := time.Now().Add(-time.Hour)
	read := func() (int64, int64, int64, string) {
		t.Helper()
		var d, r, m int64
		var c string
		if e := pool.QueryRow(ctx, `SELECT down_at,recovered_at,last_down_ms,last_cycle FROM lulu_collector_state WHERE receiver_uid=$1`, uid).Scan(&d, &r, &m, &c); e != nil {
			t.Fatal(e)
		}
		return d, r, m, c
	}
	if err = s.Collect(ctx, p, start); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal(err)
	}
	d, _, _, c := read()
	if d <= 0 || c == "" {
		t.Fatal("missing outage state")
	}
	p.err = errors.New("network failure")
	if err = s.Collect(ctx, p, start); err == nil {
		t.Fatal("expected failure")
	}
	d2, _, _, _ := read()
	if d2 != d {
		t.Fatal("outage start changed")
	}
	p.err = nil
	if err = s.Collect(ctx, p, start); err != nil {
		t.Fatal(err)
	}
	d2, r, m, c := read()
	if d2 != 0 || r < d || m < 0 || c != "噜噜登录已恢复，采集成功" {
		t.Fatalf("recovery %d %d %d %s", d2, r, m, c)
	}
}

func TestDepositThreeMinuteLookback(t *testing.T) {
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
	user := uuid.NewString()
	receiver := "987123777"
	if _, e = p.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'lookback')`, user); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='player'`, user); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE lulu_config SET receiver_uid=$1,enabled=true WHERE singleton`, receiver); e != nil {
		t.Fatal(e)
	}
	s := NewService(p, receiver)
	for _, tc := range []struct {
		uid  string
		age  time.Duration
		late bool
		want string
	}{
		{"123456111", 120 * time.Second, false, "confirmed"}, {"123456222", 181 * time.Second, false, "requested"}, {"123456333", 120 * time.Second, true, "confirmed"},
	} {
		r := Receipt{ID: uuid.NewString(), ReceiverUID: receiver, SenderUID: tc.uid, Amount: 1, OccurredAt: time.Now().UTC().Add(-tc.age).Truncate(time.Microsecond)}
		if !tc.late {
			if e = s.RecordReceipt(ctx, r); e != nil {
				t.Fatal(e)
			}
		}
		in := Input{RequestID: uuid.NewString(), LuluUID: tc.uid, Amount: "1"}
		o, e := s.Create(ctx, user, "deposit", in)
		if e != nil {
			t.Fatal(e)
		}
		if tc.late {
			// Simulate a receipt persisted under the previous matching rule.
			if _, e = p.Exec(ctx, `INSERT INTO lulu_receipts(id,receiver_uid,sender_uid,amount,occurred_at) VALUES($1,$2,$3,$4,$5)`, r.ID, r.ReceiverUID, r.SenderUID, r.Amount, r.OccurredAt); e != nil {
				t.Fatal(e)
			}
			if e = s.ReconcileStored(ctx); e != nil {
				t.Fatal(e)
			}
			if e = s.RecordReceipt(ctx, r); e != nil {
				t.Fatal(e)
			}
		}
		if e = s.RecordReceipt(ctx, r); e != nil {
			t.Fatal(e)
		}
		repeat, e := s.Create(ctx, user, "deposit", in)
		if e != nil || repeat.Status != tc.want || repeat.ID != o.ID {
			t.Fatalf("%+v %v", repeat, e)
		}
		var count int
		if e = p.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE business_id=$1`, o.ID).Scan(&count); e != nil {
			t.Fatal(e)
		}
		want := 0
		if tc.want == "confirmed" {
			want = 1
		}
		if count != want {
			t.Fatalf("ledger count %d want %d", count, want)
		}
	}
}
