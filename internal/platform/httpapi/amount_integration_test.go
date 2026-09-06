package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/application/currency"
	"github.com/block-beast/platform/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type moneyUserResolver struct {
	id     string
	public int64
}

func (m moneyUserResolver) InternalUserIDByPublicID(context.Context, int64) (string, error) {
	return m.id, nil
}
func (m moneyUserResolver) PublicUserID(context.Context, string) (int64, error) { return m.public, nil }

func TestDisplayBetHTTPStoresExactMinorOnce(t *testing.T) {
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
	user, walletID, gameID, round := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,display_name) VALUES($1,'money test')`, user)
	defer func() {
		p.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id IN (SELECT id::text FROM bets WHERE wallet_id=$1)`, walletID)
		p.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id=$1`, walletID)
		p.Exec(ctx, `DELETE FROM bets WHERE wallet_id=$1`, walletID)
		p.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, walletID)
		p.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, round)
		p.Exec(ctx, `DELETE FROM game_types WHERE id=$1`, gameID)
		p.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
	}()
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',200000)`, walletID, user)
	exec(`INSERT INTO game_types(id,code,name,rules) VALUES($1,$2,'money test','{"outcomes":["red","blue"],"payout_multiplier":2}')`, gameID, "money-"+gameID)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,$2,1,'open',$3)`, round, gameID, time.Now().Add(time.Hour))
	var public int64
	if err = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, user).Scan(&public); err != nil {
		t.Fatal(err)
	}
	svc := betting.NewService(p)
	s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), svc, readinessChecker{}, nil, nil, svc, nil, WithCurrencies(currency.NewService(p)), WithPublicUserResolver(moneyUserResolver{user, public}))
	for _, test := range []struct{ id, amount, stake string }{{"hundred", "100", "100"}, {"fraction", "1.5", "1.5"}, {"fraction", "\"1.5\"", "1.5"}} {
		body := fmt.Sprintf(`{"client_request_id":%q,"round_id":%q,"account_id":%d,"currency":"POINTS","stake":%s,"selection":{"pick":"red"}}`, test.id, round, public, test.amount)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/bets", strings.NewReader(body)))
		if w.Code != 201 || strings.Contains(w.Body.String(), "_minor") || !strings.Contains(w.Body.String(), `"stake":"`+test.stake+`"`) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	var balance, total int64
	var count int
	if err = p.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, walletID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*),sum(stake_minor) FROM bets WHERE wallet_id=$1`, walletID).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if balance != 98500 || count != 2 || total != 101500 {
		t.Fatalf("balance=%d count=%d total=%d", balance, count, total)
	}
}
