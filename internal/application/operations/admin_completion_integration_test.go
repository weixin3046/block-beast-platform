package operations

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminBetVoidAndPlayerCreation(t *testing.T) {
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

	admin, operator, ordinary := uuid.NewString(), uuid.NewString(), uuid.NewString()
	realUser, virtualUser := uuid.NewString(), uuid.NewString()
	realWallet, virtualWallet := uuid.NewString(), uuid.NewString()
	roundID, settledRoundID := uuid.NewString(), uuid.NewString()
	realBet, simulatedBet, wonBet, overflowBet := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	sequence := time.Now().UnixNano()
	loginPrefix := "admin-create-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	ids := []string{admin, operator, ordinary, realUser, virtualUser}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,login_name,display_name,is_virtual) VALUES
		($1::uuid,$1::text,'void admin',false),($2::uuid,$2::text,'void operator',false),
		($3::uuid,$3::text,'ordinary',false),($4::uuid,$4::text,'real player',false),
		($5::uuid,$5::text,'virtual player',true)`, admin, operator, ordinary, realUser, virtualUser)
	exec(`INSERT INTO roles(id,code,description) VALUES
		(gen_random_uuid(),'admin','admin'),(gen_random_uuid(),'operator','operator'),(gen_random_uuid(),'player','player')
		ON CONFLICT(code) DO NOTHING`)
	exec(`INSERT INTO user_roles(user_id,role_id)
		SELECT $1::uuid,id FROM roles WHERE code='admin'
		UNION ALL SELECT $2::uuid,id FROM roles WHERE code='operator'
		UNION ALL SELECT $3::uuid,id FROM roles WHERE code='player'`, admin, operator, ordinary)
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES
		($1,$2,'POINTS',9000),($3,$4,'POINTS',0)`, realWallet, realUser, virtualWallet, virtualUser)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at) VALUES
		($1,'09000000-0000-4000-8000-000000000001',$2,'closed',now()-interval '1 minute',now()+interval '1 minute'),
		($3,'09000000-0000-4000-8000-000000000001',$4,'settled',now()-interval '2 minutes',now()-interval '1 minute')`,
		roundID, sequence, settledRoundID, sequence+1)
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,is_simulated,settled_at) VALUES
		($1::uuid,$1::text,$2,$3,$4,'{"pick":"red"}',1000,'accepted',false,NULL),
		($5::uuid,$5::text,$2,$6,$7,'{"pick":"red"}',1000,'accepted',true,NULL),
		($8::uuid,$8::text,$9,$3,$4,'{"pick":"red"}',1000,'won',false,now())`,
		realBet, roundID, realUser, realWallet, simulatedBet, virtualUser, virtualWallet, wonBet, settledRoundID)

	defer func() {
		// Ledger/audit are immutable, so deleting their fixtures may be refused.
		// Never leave the intentional MaxInt64 overflow balance in shared reports.
		pool.Exec(ctx, `UPDATE wallets SET available_minor=0 WHERE user_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=ANY($1) OR payload->>'user_id'=ANY($2)`, []string{realBet, simulatedBet, wonBet, overflowBet}, ids)
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM admin_bet_voids WHERE operator_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN(SELECT id FROM wallets WHERE user_id=ANY($1::uuid[]))`, ids)
		pool.Exec(ctx, `DELETE FROM bets WHERE id=ANY($1::uuid[])`, []string{realBet, simulatedBet, wonBet, overflowBet})
		pool.Exec(ctx, `DELETE FROM rounds WHERE id=ANY($1::uuid[])`, []string{roundID, settledRoundID})
		pool.Exec(ctx, `DELETE FROM uploads WHERE owner_user_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM auth_identities WHERE user_id IN(SELECT id FROM users WHERE login_name LIKE $1)`, loginPrefix+"%")
		pool.Exec(ctx, `DELETE FROM wallets WHERE user_id IN(SELECT id FROM users WHERE login_name LIKE $1)`, loginPrefix+"%")
		pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id IN(SELECT id FROM users WHERE login_name LIKE $1) OR user_id=ANY($2::uuid[])`, loginPrefix+"%", ids)
		pool.Exec(ctx, `DELETE FROM users WHERE login_name LIKE $1 OR id=ANY($2::uuid[])`, loginPrefix+"%", ids)
	}()

	service := NewService(pool)
	requestID := uuid.NewString()
	first, err := service.VoidBet(ctx, BetVoidInput{OperatorID: admin, RequestID: requestID, BetID: realBet, Reason: "风控复核作废"})
	if err != nil || first.Duplicate || first.RefundMinor != 1000 || first.RoundSequence != sequence || first.Status != "voided" {
		t.Fatalf("first void: %+v %v", first, err)
	}
	duplicate, err := service.VoidBet(ctx, BetVoidInput{OperatorID: admin, RequestID: requestID, BetID: realBet, Reason: "风控复核作废"})
	if err != nil || !duplicate.Duplicate || duplicate.OperationID != first.OperationID || duplicate.RefundMinor != 1000 {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	for _, changed := range []BetVoidInput{
		{OperatorID: admin, RequestID: requestID, BetID: simulatedBet, Reason: "风控复核作废"},
		{OperatorID: admin, RequestID: requestID, BetID: realBet, Reason: "修改原因"},
	} {
		if _, err := service.VoidBet(ctx, changed); !errors.Is(err, ErrBetVoidConflict) {
			t.Fatalf("changed replay accepted: %v", err)
		}
	}
	var balance int64
	if err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, realWallet).Scan(&balance); err != nil || balance != 10000 {
		t.Fatalf("real balance=%d err=%v", balance, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id=$1 AND business_type='bet_void'`, realWallet).Scan(&count); err != nil || count != 1 {
		t.Fatalf("real ledger count=%d err=%v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='game.bet.voided'`, realBet).Scan(&count); err != nil || count != 1 {
		t.Fatalf("void outbox count=%d err=%v", count, err)
	}
	var leaked bool
	if err = pool.QueryRow(ctx, `SELECT payload ? 'reason' OR payload ? 'operator_user_id' OR payload ? 'refund_minor' FROM outbox_events WHERE aggregate_id=$1 AND event_type='game.bet.voided'`, realBet).Scan(&leaked); err != nil || leaked {
		t.Fatalf("public event leaked admin result: %v %v", leaked, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND action='admin.bet.void'`, admin).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}

	sim, err := service.VoidBet(ctx, BetVoidInput{OperatorID: operator, RequestID: uuid.NewString(), BetID: simulatedBet, Reason: "模拟单作废"})
	if err != nil || sim.RefundMinor != 0 || !sim.IsSimulated || sim.Status != "voided" {
		t.Fatalf("simulated void: %+v %v", sim, err)
	}
	if err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, virtualWallet).Scan(&balance); err != nil || balance != 0 {
		t.Fatalf("virtual balance=%d err=%v", balance, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id=$1 AND business_type='bet_void'`, virtualWallet).Scan(&count); err != nil || count != 0 {
		t.Fatalf("simulated ledger count=%d err=%v", count, err)
	}
	if _, err = service.VoidBet(ctx, BetVoidInput{OperatorID: admin, RequestID: uuid.NewString(), BetID: wonBet, Reason: "不应成功"}); !errors.Is(err, ErrBetNotVoidable) {
		t.Fatalf("settled bet changed: %v", err)
	}
	if _, err = service.VoidBet(ctx, BetVoidInput{OperatorID: ordinary, RequestID: uuid.NewString(), BetID: wonBet, Reason: "越权"}); !errors.Is(err, ErrAdminCompletionForbidden) {
		t.Fatalf("ordinary actor accepted: %v", err)
	}
	exec(`UPDATE wallets SET available_minor=$2 WHERE id=$1`, realWallet, int64(math.MaxInt64-500))
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status)
		VALUES($1::uuid,$1::text,$2,$3,$4,'{}',1000,'accepted')`, overflowBet, roundID, realUser, realWallet)
	overflowRequest := uuid.NewString()
	if _, err = service.VoidBet(ctx, BetVoidInput{OperatorID: admin, RequestID: overflowRequest, BetID: overflowBet, Reason: "溢出必须回滚"}); !errors.Is(err, ErrBetVoidBalanceOverflow) {
		t.Fatalf("overflow result: %v", err)
	}
	var overflowStatus string
	if err = pool.QueryRow(ctx, `SELECT status FROM bets WHERE id=$1`, overflowBet).Scan(&overflowStatus); err != nil || overflowStatus != "accepted" {
		t.Fatalf("overflow bet status=%s err=%v", overflowStatus, err)
	}
	if err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, realWallet).Scan(&balance); err != nil || balance != math.MaxInt64-500 {
		t.Fatalf("overflow balance=%d err=%v", balance, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM admin_bet_voids WHERE operator_id=$1 AND request_id=$2`, admin, overflowRequest).Scan(&count); err != nil || count != 0 {
		t.Fatalf("overflow idempotency count=%d err=%v", count, err)
	}
	page, err := service.ListBetVoids(ctx, admin, 1, 0)
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].OperationID != sim.OperationID {
		t.Fatalf("void page: %+v %v", page, err)
	}

	avatarKey := "uploads/" + uuid.NewString() + ".png"
	exec(`INSERT INTO uploads(id,owner_user_id,storage_key,content_type,size_bytes,status) VALUES($1,$2,$3,'image/png',8,'confirmed')`, uuid.NewString(), admin, avatarKey)
	created, err := service.CreatePlayerAccount(ctx, PlayerAccountInput{ActorUserID: admin, LoginName: loginPrefix + "-a", Password: "strong-password-123", AvatarURL: avatarKey})
	if err != nil {
		t.Fatal(err)
	}
	if created.IsVirtual || created.DisplayName != fmt.Sprintf("用户%d", created.UserID) || created.AvatarURL == "" || len(created.Roles) != 1 || created.Roles[0] != "player" {
		t.Fatalf("created player: %+v", created)
	}
	var internalID, hash string
	var virtual bool
	if err = pool.QueryRow(ctx, `SELECT u.id::text,u.is_virtual,a.password_hash FROM users u JOIN auth_identities a ON a.user_id=u.id AND a.provider='password' WHERE u.public_id=$1`, created.UserID).Scan(&internalID, &virtual, &hash); err != nil {
		t.Fatal(err)
	}
	if virtual || hash == "strong-password-123" || !identity.VerifyPassword(hash, "strong-password-123") {
		t.Fatal("player password or virtual flag is unsafe")
	}
	var walletCount, enabledWalletCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM chat_rooms r JOIN chat_room_members m ON m.room_id=r.id AND m.user_id=r.customer_user_id WHERE r.customer_user_id=$1 AND r.room_type='customer_service' AND m.member_role='owner'`, internalID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("created player customer rooms=%d err=%v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE available_minor=0 AND frozen_minor=0) FROM wallets WHERE user_id=$1`, internalID).Scan(&walletCount, &enabledWalletCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM currencies WHERE enabled AND create_on_registration`).Scan(&count); err != nil || walletCount != count || enabledWalletCount != count {
		t.Fatalf("wallets=%d zero=%d expected=%d err=%v", walletCount, enabledWalletCount, count, err)
	}
	if _, err = service.CreatePlayerAccount(ctx, PlayerAccountInput{ActorUserID: admin, LoginName: loginPrefix + "-a", Password: "strong-password-456"}); !errors.Is(err, identity.ErrLoginNameTaken) {
		t.Fatalf("duplicate login: %v", err)
	}
	if _, err = service.CreatePlayerAccount(ctx, PlayerAccountInput{ActorUserID: ordinary, LoginName: loginPrefix + "-forbidden", Password: "strong-password-456"}); !errors.Is(err, ErrAdminCompletionForbidden) {
		t.Fatalf("ordinary player created account: %v", err)
	}
	createdByOperator, err := service.CreatePlayerAccount(ctx, PlayerAccountInput{ActorUserID: operator, LoginName: loginPrefix + "-op", DisplayName: "  运营创建  ", Password: "strong-password-789"})
	if err != nil || createdByOperator.DisplayName != "运营创建" || createdByOperator.IsVirtual {
		t.Fatalf("operator creation: %+v %v", createdByOperator, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='admin.player.create' AND actor_user_id IN($1,$2)`, admin, operator).Scan(&count); err != nil || count != 2 {
		t.Fatalf("player creation audits=%d err=%v", count, err)
	}
}
