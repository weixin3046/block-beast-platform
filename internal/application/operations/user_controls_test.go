package operations

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUserControlsAndMultiCurrencyDashboard(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	admin, op, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ids := []string{admin, op, user}
	exec(`INSERT INTO users(id,login_name,display_name) VALUES($1::uuid,$1::text,'admin'),($2::uuid,$2::text,'operator'),($3::uuid,$3::text,'player')`, admin, op, user)
	defer func() {
		p.Exec(ctx, `DELETE FROM agent_relations WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN (SELECT id FROM wallets WHERE user_id=ANY($1::uuid[]))`, ids)
		p.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM sessions WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM auth_identities WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM wallets WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM agent_commission_rates WHERE agent_user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM user_roles WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(ctx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, ids)
	}()
	exec(`INSERT INTO user_roles SELECT $1,id FROM roles WHERE code='admin'`, admin)
	exec(`INSERT INTO user_roles SELECT $1,id FROM roles WHERE code='operator'`, op)
	var uid int64
	if err = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, user).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO auth_identities(id,user_id,provider,subject,password_hash) VALUES($1,$2::uuid,'password',$2::text,'old')`, uuid.NewString(), user)
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'USDT',1500000),($3,$2,'JADE',1500)`, uuid.NewString(), user, uuid.NewString())
	s := NewService(p)
	exec(`INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,'test')`, user, admin)
	var parentID int64
	if err = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, admin).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) SELECT $1,id,'test',$2,'test',-198500,1500000 FROM wallets WHERE user_id=$3 AND currency='USDT'`, uuid.NewString(), uuid.NewString(), user)
	ledger, err := s.ListAdminLedger(ctx, LedgerQuery{User: fmtID(uid)})
	if err != nil || len(ledger) != 1 {
		t.Fatalf("ledger: %+v %v", ledger, err)
	}
	if ledger[0].Amount != "-0.198500" || ledger[0].BalanceAfter != "1.500000" || ledger[0].DisplayName != "player" {
		t.Fatalf("ledger precision: %+v", ledger[0])
	}
	if err = s.ResetUserPassword(ctx, op, uid, "login", "new-password-123"); !errors.Is(err, ErrUserControlForbidden) {
		t.Fatal(err)
	}
	exec(`INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, uuid.NewString(), user, uuid.NewString())
	if err = s.ResetUserPassword(ctx, admin, uid, "login", "new-password-123"); err != nil {
		t.Fatal(err)
	}
	var sessionCount int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, user).Scan(&sessionCount); err != nil || sessionCount != 0 {
		t.Fatal("sessions not revoked", err)
	}
	var hash string
	if err = p.QueryRow(ctx, `SELECT password_hash FROM auth_identities WHERE user_id=$1`, user).Scan(&hash); err != nil || !identity.VerifyPassword(hash, "new-password-123") {
		t.Fatal("login reset", err)
	}
	if err = s.ResetUserPassword(ctx, admin, uid, "secondary", "trade-secret"); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `SELECT secondary_password_hash FROM users WHERE id=$1`, user).Scan(&hash); err != nil || !identity.VerifyPassword(hash, "trade-secret") {
		t.Fatal("trade reset", err)
	}
	for _, kind := range []string{"login", "secondary"} {
		for _, password := range []string{"1", strings.Repeat("密", 100)} {
			if err = s.ResetUserPassword(ctx, admin, uid, kind, password); err != nil {
				t.Fatalf("reset %s: %v", kind, err)
			}
			query := `SELECT password_hash FROM auth_identities WHERE user_id=$1`
			if kind == "secondary" {
				query = `SELECT secondary_password_hash FROM users WHERE id=$1`
			}
			if err = p.QueryRow(ctx, query, user).Scan(&hash); err != nil || !identity.VerifyPassword(hash, password) {
				t.Fatalf("verify %s: %v", kind, err)
			}
		}
		if err = s.ResetUserPassword(ctx, admin, uid, kind, " \t"); !errors.Is(err, ErrResetPasswordEmpty) {
			t.Fatalf("empty %s: %v", kind, err)
		}
	}
	if err = s.SetUserMuted(ctx, op, uid, true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetAgentLevel(ctx, fmtID(uid), 3); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE agent_commission_rates SET rate_basis_points=100 WHERE agent_user_id=$1`, user)
	if err = s.SetAgentLevel(ctx, fmtID(uid), 4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.SetAgentLevel(ctx, fmtID(uid), 0); err != nil {
			t.Fatal("restore ordinary user", err)
		}
		var ordinary bool
		if err = p.QueryRow(ctx, `SELECT agent_level IS NULL FROM users WHERE id=$1`, user).Scan(&ordinary); err != nil || !ordinary {
			t.Fatal("ordinary user must be stored as NULL", err)
		}
		ordinaryUsers, e := s.SearchUsers(ctx, UserSearch{Query: fmtID(uid)})
		if e != nil || len(ordinaryUsers) != 1 || ordinaryUsers[0].AgentLevel != 0 || ordinaryUsers[0].ParentUserID == nil || *ordinaryUsers[0].ParentUserID != parentID {
			t.Fatalf("ordinary user and parent relation: %+v %v", ordinaryUsers, e)
		}
	}
	if err = s.SetAgentLevel(ctx, fmtID(uid), 4); err != nil {
		t.Fatal("restore agent", err)
	}
	var rate int
	if err = p.QueryRow(ctx, `SELECT rate_basis_points FROM agent_commission_rates WHERE agent_user_id=$1`, user).Scan(&rate); err != nil || rate != 100 {
		t.Fatal(rate, err)
	}
	list, err := s.SearchUsers(ctx, UserSearch{Query: fmtID(uid), Currencies: []string{"USDT", "JADE"}, Minimum: "1.5", UserType: "real"})
	if err != nil || len(list) != 1 {
		t.Fatalf("users: %+v %v", list, err)
	}
	if list[0].ParentUserID == nil || *list[0].ParentUserID != parentID || list[0].ParentDisplayName == nil || *list[0].ParentDisplayName != "admin" {
		t.Fatalf("parent: %+v", list[0])
	}
	if err != nil || len(list) != 1 || list[0].AgentLevel != 4 || !list[0].ChatMuted || len(list[0].Balances) != 2 {
		t.Fatal(list, err)
	}
	for _, b := range list[0].Balances {
		if b.Available != "1.500000" && b.Available != "1.500" {
			t.Fatal(b)
		}
	}
	list, err = s.SearchUsers(ctx, UserSearch{Query: fmtID(uid), Minimum: "2"})
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	board, err := s.Dashboard(ctx, fmtID(uid), time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil || len(board.Players) != 1 || len(board.Players[0].Funds) != 2 {
		t.Fatal(board, err)
	}
	raw, _ := json.Marshal(board.Players[0])
	var obj map[string]any
	json.Unmarshal(raw, &obj)
	if _, ok := obj["balance_minor"]; ok {
		t.Fatal("mixed currency aggregate leaked")
	}
	for _, f := range board.Players[0].Funds {
		if f.Balance != "1.500000" && f.Balance != "1.500" {
			t.Fatal(f)
		}
	}
	var audit string
	if err = p.QueryRow(ctx, `SELECT jsonb_agg(payload)::text FROM audit_logs WHERE actor_user_id=$1`, admin).Scan(&audit); err != nil || strings.Contains(audit, "secret") || strings.Contains(audit, "new-password") {
		t.Fatal(audit, err)
	}
	exec(`DELETE FROM user_roles WHERE user_id=$1`, op)
	if err = s.SetUserMuted(ctx, op, uid, false); !errors.Is(err, ErrUserControlForbidden) {
		t.Fatal(err)
	}
}
func fmtID(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestSetAgentLevelValidation(t *testing.T) {
	s := NewService(nil)
	for _, level := range []int{-1, 7} {
		if err := s.SetAgentLevel(context.Background(), "invalid", level); !errors.Is(err, ErrInvalidAgentLevel) {
			t.Fatalf("level %d: %v", level, err)
		}
	}
	if err := s.SetAgentLevel(context.Background(), "invalid", 0); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ordinary user level should pass validation: %v", err)
	}
}
