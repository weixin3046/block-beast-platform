package betting

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/block-beast/platform/internal/application/operations"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServicePlaceBetIsAtomicAndIdempotent(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	accountID := uuid.NewString()
	walletID := uuid.NewString()
	gameTypeID := uuid.NewString()
	roundID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, $2)`, accountID, "bet test player")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id, currency, available_minor) VALUES ($1, $2, $3, $4)`, walletID, accountID, "USDT", 10_000)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO game_types (id, code, name, rules) VALUES ($1, $2, $3, $4)`, gameTypeID, "test-"+gameTypeID, "test game", `{"outcomes":["red","blue"],"payout_multiplier":2}`)
	if err != nil {
		t.Fatalf("create game type: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO rounds (id, game_type_id, sequence, status, bet_closes_at) VALUES ($1, $2, $3, 'open', $4)`, roundID, gameTypeID, 1, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("create round: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE payload->>'user_id' = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id = $1`, gameTypeID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	})

	service := NewService(pool)
	request := PlaceBetRequest{
		ClientRequestID: "request-101",
		RoundID:         roundID,
		AccountID:       accountID,
		Currency:        "USDT",
		Selection:       json.RawMessage(`{"pick":"red"}`),
		StakeMinor:      2_500,
	}
	futureID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,$2,2,'scheduled',$3)`, futureID, gameTypeID, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, futureID) })
	for _, futureStatus := range []string{"scheduled", "open"} {
		if _, err := pool.Exec(ctx, `UPDATE rounds SET status=$2 WHERE id=$1`, futureID, futureStatus); err != nil {
			t.Fatal(err)
		}
		futureRequest := request
		futureRequest.RoundID = futureID
		futureRequest.ClientRequestID = "future-" + futureStatus
		if _, err := service.PlaceBet(ctx, futureRequest); !errors.Is(err, game.ErrBettingClosed) {
			t.Fatalf("future round %s accepted or unexpected error: %v", futureStatus, err)
		}
	}
	var unchanged int64
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, walletID).Scan(&unchanged); err != nil || unchanged != 10000 {
		t.Fatalf("rejected future bet changed balance: %d err=%v", unchanged, err)
	}
	first, err := service.PlaceBet(ctx, request)
	if err != nil {
		t.Fatalf("place bet: %v", err)
	}
	second, err := service.PlaceBet(ctx, request)
	if err != nil {
		t.Fatalf("repeat place bet: %v", err)
	}
	if first.BetID != second.BetID {
		t.Fatal("same client request ID must return the original bet")
	}
	if second.Currency != "USDT" || second.Status != "accepted" {
		t.Fatalf("repeated bet = %#v, want complete accepted bet", second)
	}
	if second.RoundSequence != 1 || second.GameType != "test-"+gameTypeID || second.GameName != "test game" || second.PayoutRate != "2" || second.BalanceAfterBetMinor == nil || *second.BalanceAfterBetMinor != 7_500 {
		t.Fatalf("repeated bet context = %#v", second)
	}
	found, err := service.Find(ctx, first.BetID)
	if err != nil {
		t.Fatalf("find placed bet: %v", err)
	}
	if found.BetID != first.BetID || found.Status != "accepted" || found.Currency != "USDT" {
		t.Fatalf("found bet = %#v", found)
	}
	if found.Decimals != 6 || found.Stake != "0.002500" || found.Payout != "0.000000" || second.Stake != found.Stake {
		t.Fatalf("bet display precision: %+v", found)
	}
	adminBets, err := operations.NewService(pool).ListAdminBets(ctx, operations.BetQuery{GameType: "test-" + gameTypeID})
	if err != nil || len(adminBets) != 1 {
		t.Fatalf("admin bets: %+v %v", adminBets, err)
	}
	if adminBets[0].Stake != found.Stake || adminBets[0].Payout != found.Payout || adminBets[0].Decimals != 6 {
		t.Fatalf("admin precision: %+v", adminBets[0])
	}
	publicBets, err := service.ListPublicBets(ctx, PublicBetQuery{GameType: "test-" + gameTypeID, Currency: "usdt", Status: "accepted", PlayerType: "real", Limit: 10})
	if err != nil {
		t.Fatalf("list public bets: %v", err)
	}
	if len(publicBets) != 1 || publicBets[0].BetID != first.BetID || publicBets[0].Player.DisplayName != "bet test player" || publicBets[0].Player.IsVirtual || publicBets[0].RoundSequence != 1 || publicBets[0].PayoutRate != "2" {
		t.Fatalf("public bets = %#v", publicBets)
	}
	virtualBets, err := service.ListPublicBets(ctx, PublicBetQuery{PlayerType: "virtual", Limit: 10})
	if publicBets[0].Stake != found.Stake || publicBets[0].Payout != found.Payout {
		t.Fatal("public precision differs")
	}
	if err != nil {
		t.Fatalf("list virtual bets: %v", err)
	}
	for _, item := range virtualBets {
		if item.BetID == first.BetID {
			t.Fatalf("real bet appeared in virtual filter: %#v", item)
		}
	}

	assertCount(t, ctx, pool, `SELECT count(*) FROM bets WHERE wallet_id = $1`, walletID, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'bet_debit'`, walletID, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = 'game.bet.placed'`, first.BetID, 1)
	var placedPayload string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM outbox_events WHERE aggregate_id=$1 AND event_type='game.bet.placed'`, first.BetID).Scan(&placedPayload); err != nil {
		t.Fatalf("placed event payload: %v", err)
	}
	if strings.Contains(placedPayload, accountID) || !strings.Contains(placedPayload, `"round_sequence": 1`) || !strings.Contains(placedPayload, `"display_name": "bet test player"`) {
		t.Fatalf("placed event payload = %s", placedPayload)
	}

	var availableMinor int64
	err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1`, walletID).Scan(&availableMinor)
	if err != nil {
		t.Fatalf("read wallet balance: %v", err)
	}
	if availableMinor != 7_500 {
		t.Fatalf("available balance = %d, want 7500", availableMinor)
	}

	_, err = service.PlaceBet(ctx, PlaceBetRequest{
		ClientRequestID: "request-102",
		RoundID:         roundID,
		AccountID:       accountID,
		Currency:        "USDT",
		Selection:       json.RawMessage(`{"pick":"blue"}`),
		StakeMinor:      7_501,
	})
	if !errors.Is(err, wallet.ErrInsufficientFunds) {
		t.Fatalf("error = %v, want insufficient funds", err)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM bets WHERE wallet_id = $1`, walletID, 1)

	cancelled, err := service.CancelBet(ctx, first.BetID, accountID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel bet = %+v, err = %v", cancelled, err)
	}
	if cancelled.BalanceAfterBetMinor == nil || *cancelled.BalanceAfterBetMinor != 7_500 || cancelled.BalanceAfterRefundMinor == nil || *cancelled.BalanceAfterRefundMinor != 10_000 || cancelled.BalanceAfterSettlementMinor == nil || *cancelled.BalanceAfterSettlementMinor != 10_000 {
		t.Fatalf("cancel balances = %+v", cancelled)
	}
	if _, err := service.CancelBet(ctx, first.BetID, accountID); err != nil {
		t.Fatalf("repeat cancel must be idempotent: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, walletID).Scan(&availableMinor); err != nil || availableMinor != 10_000 {
		t.Fatalf("balance after cancel = %d, err = %v", availableMinor, err)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id=$1 AND entry_type='bet_refund'`, walletID, 1)
}

func TestFormatPayoutRate(t *testing.T) {
	for _, testCase := range []struct {
		multiplier int64
		divisor    int64
		want       string
	}{{1940, 1000, "1.94"}, {9350, 1000, "9.35"}, {2, 1, "2"}, {0, 1000, ""}} {
		if got := formatPayoutRate(testCase.multiplier, testCase.divisor); got != testCase.want {
			t.Fatalf("formatPayoutRate(%d,%d) = %q, want %q", testCase.multiplier, testCase.divisor, got, testCase.want)
		}
	}
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, argument any, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, query, argument).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
}

func TestValidHashSelection(t *testing.T) {
	for _, test := range []struct {
		mode string
		raw  string
		want bool
	}{
		{mode: "guess", raw: `{"pick":"0"}`, want: true},
		{mode: "dodge", raw: `{"pick":"9"}`, want: true},
		{mode: "guess", raw: `{"pick":"10"}`, want: false},
		{mode: "road", raw: `{"pick":"big"}`, want: true},
		{mode: "road", raw: `{"pick":"5"}`, want: false},
		{mode: "unknown", raw: `{"pick":"5"}`, want: false},
	} {
		if got := validHashSelection(test.mode, json.RawMessage(test.raw)); got != test.want {
			t.Fatalf("validHashSelection(%q,%s) = %v, want %v", test.mode, test.raw, got, test.want)
		}
	}
}
