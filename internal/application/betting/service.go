package betting

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRoundNotFound = errors.New("round not found")
var ErrInvalidSelection = errors.New("selection must be valid JSON")

type PlaceBetRequest struct {
	ClientRequestID string
	RoundID         string
	AccountID       string
	Currency        string
	Selection       json.RawMessage
	StakeMinor      int64
}

type PlacedBet struct {
	BetID           string
	ClientRequestID string
	RoundID         string
	AccountID       string
	Currency        string
	Selection       json.RawMessage
	StakeMinor      int64
	PlacedAt        time.Time
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) PlaceBet(ctx context.Context, request PlaceBetRequest) (PlacedBet, error) {
	if request.StakeMinor <= 0 {
		return PlacedBet{}, game.ErrInvalidStake
	}
	if !json.Valid(request.Selection) {
		return PlacedBet{}, ErrInvalidSelection
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return PlacedBet{}, err
	}
	defer tx.Rollback(ctx)

	existing, err := findBet(ctx, tx, request.AccountID, request.ClientRequestID)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, err
	}

	var status game.RoundStatus
	var betClosesAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT status, bet_closes_at
		FROM rounds
		WHERE id = $1
		FOR UPDATE`, request.RoundID).Scan(&status, &betClosesAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, ErrRoundNotFound
	}
	if err != nil {
		return PlacedBet{}, err
	}
	if status != game.RoundOpen || !time.Now().UTC().Before(betClosesAt) {
		return PlacedBet{}, game.ErrBettingClosed
	}

	existing, err = findBet(ctx, tx, request.AccountID, request.ClientRequestID)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, err
	}

	var walletID string
	var availableMinor int64
	err = tx.QueryRow(ctx, `
		SELECT id, available_minor
		FROM wallets
		WHERE user_id = $1 AND currency = $2
		FOR UPDATE`, request.AccountID, request.Currency).Scan(&walletID, &availableMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, wallet.ErrWalletNotFound
	}
	if err != nil {
		return PlacedBet{}, err
	}
	if availableMinor < request.StakeMinor {
		return PlacedBet{}, wallet.ErrInsufficientFunds
	}

	bet := PlacedBet{
		BetID:           uuid.NewString(),
		ClientRequestID: request.ClientRequestID,
		RoundID:         request.RoundID,
		AccountID:       request.AccountID,
		Currency:        request.Currency,
		Selection:       append(json.RawMessage(nil), request.Selection...),
		StakeMinor:      request.StakeMinor,
	}
	availableMinor -= request.StakeMinor
	_, err = tx.Exec(ctx, `
		UPDATE wallets
		SET available_minor = $2, version = version + 1, updated_at = now()
		WHERE id = $1`, walletID, availableMinor)
	if err != nil {
		return PlacedBet{}, err
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO bets (id, client_request_id, round_id, user_id, wallet_id, selection, stake_minor, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'accepted')
		RETURNING created_at`, bet.BetID, bet.ClientRequestID, bet.RoundID, bet.AccountID, walletID, bet.Selection, bet.StakeMinor).
		Scan(&bet.PlacedAt)
	if err != nil {
		return PlacedBet{}, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor
		) VALUES ($1, $2, 'bet', $3, 'bet_debit', $4, $5)`, uuid.NewString(), walletID, bet.BetID, -bet.StakeMinor, availableMinor)
	if err != nil {
		return PlacedBet{}, err
	}

	payload, err := json.Marshal(struct {
		BetID   string `json:"bet_id"`
		RoundID string `json:"round_id"`
		UserID  string `json:"user_id"`
	}{BetID: bet.BetID, RoundID: bet.RoundID, UserID: bet.AccountID})
	if err != nil {
		return PlacedBet{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'bet', $2, $3, $4)`, uuid.NewString(), bet.BetID, events.BetPlaced, payload)
	if err != nil {
		return PlacedBet{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func findBet(ctx context.Context, tx pgx.Tx, accountID string, clientRequestID string) (PlacedBet, error) {
	var bet PlacedBet
	err := tx.QueryRow(ctx, `
		SELECT id, client_request_id, round_id, user_id, selection, stake_minor, created_at
		FROM bets
		WHERE user_id = $1 AND client_request_id = $2`, accountID, clientRequestID).
		Scan(&bet.BetID, &bet.ClientRequestID, &bet.RoundID, &bet.AccountID, &bet.Selection, &bet.StakeMinor, &bet.PlacedAt)
	return bet, err
}
