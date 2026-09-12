package operations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/application/settlement"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConvertUserToVirtual(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	admin, op, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ids := []string{admin, op, user}
	roundID, betID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name) VALUES($1,'admin'),($2,'operator'),($3,'player')`, admin, op, user)
	defer func() {
		p.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=$1::text OR aggregate_id IN (SELECT id::text FROM bets WHERE round_id=$1::uuid) OR payload->>'user_id'=$2`, roundID, user)
		p.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN (SELECT id FROM wallets WHERE user_id=$1)`, user)
		p.Exec(ctx, `DELETE FROM bets WHERE round_id=$1`, roundID)
		p.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, roundID)
		p.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM wallets WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM user_roles WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, ids)
	}()
	for role, id := range map[string]string{"admin": admin, "operator": op, "player": user} {
		exec(`INSERT INTO user_roles SELECT $1,id FROM roles WHERE code=$2`, id, role)
	}
	var uid, aid int64
	if err = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, user).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, admin).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor,frozen_minor) VALUES($1,$2,'USDT',123,45)`, uuid.NewString(), user)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,'09000000-0000-4000-8000-000000000001',$2,'open',$3)`, roundID, time.Now().UnixNano(), time.Now().Add(time.Hour))
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,is_simulated,game_room_id,play_mode,payout_multiplier_snapshot,payout_divisor_snapshot) SELECT $1::uuid,$1::text,$2::uuid,$3::uuid,id,'{"pick":"odd"}',100,'accepted',false,'94000000-0000-4000-8000-000000000001','road',194,100 FROM wallets WHERE user_id=$3`, betID, roundID, user)
	s := NewService(p)
	for _, tc := range []struct {
		actor string
		id    int64
		want  error
	}{{user, uid, ErrUserControlForbidden}, {op, aid, ErrUserControlForbidden}, {admin, aid, ErrUserControlForbidden}, {admin, 99999, ErrUserNotFound}} {
		if err = s.ConvertUserToVirtual(ctx, tc.actor, tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("got %v want %v", err, tc.want)
		}
	}
	// A role-management transaction must finish before eligibility is checked.
	roleTx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer roleTx.Rollback(ctx)
	if _, err = roleTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-role-management', 0))`); err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	err = s.ConvertUserToVirtual(blockedCtx, admin, uid)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("conversion did not wait for role management: %v", err)
	}
	if err = roleTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := s.Dashboard(ctx, fmtID(uid), time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil || len(before.Players) != 1 {
		t.Fatalf("before: %+v %v", before, err)
	}
	for _, actor := range []string{op, admin} {
		if err = s.ConvertUserToVirtual(ctx, actor, uid); err != nil {
			t.Fatal(err)
		}
	}
	var virtual bool
	var available, frozen int64
	if err = p.QueryRow(ctx, `SELECT u.is_virtual,w.available_minor,w.frozen_minor FROM users u JOIN wallets w ON w.user_id=u.id WHERE u.id=$1`, user).Scan(&virtual, &available, &frozen); err != nil || !virtual || available != 123 || frozen != 45 {
		t.Fatalf("funds changed: %v %d %d %v", virtual, available, frozen, err)
	}
	after, err := s.Dashboard(ctx, fmtID(uid), time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil || len(after.Players) != 0 {
		t.Fatalf("after: %+v %v", after, err)
	}
	var count int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE target_id=$1 AND action='user.virtual.convert'`, user).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count=%d %v", count, err)
	}
	var simulated bool
	if err = p.QueryRow(ctx, `SELECT is_simulated FROM bets WHERE id=$1`, betID).Scan(&simulated); err != nil || simulated {
		t.Fatalf("historical funding mode changed: %v %v", simulated, err)
	}
	newBet, err := betting.NewService(p).PlaceBet(ctx, betting.PlaceBetRequest{ClientRequestID: uuid.NewString(), RoundID: roundID, AccountID: user, Currency: "USDT", GameRoomID: "94000000-0000-4000-8000-000000000001", PlayMode: "road", Selection: json.RawMessage(`{"pick":"odd"}`), StakeMinor: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `SELECT is_simulated FROM bets WHERE id=$1`, newBet.BetID).Scan(&simulated); err != nil || !simulated {
		t.Fatalf("new bet must be simulated: %v %v", simulated, err)
	}
	exec(`UPDATE rounds SET status='closed' WHERE id=$1`, roundID)
	var raw []byte
	if err = p.QueryRow(ctx, `SELECT rules FROM game_types WHERE id='09000000-0000-4000-8000-000000000001'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	rules, err := game.ParseRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = settlement.NewService(p).SettleRound(ctx, roundID, []string{"1", "small", "odd"}, rules); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE user_id=$1 AND currency='USDT'`, user).Scan(&available); err != nil || available != 317 {
		t.Fatalf("only real bet pays wallet: got %d want 317, %v", available, err)
	}

}
