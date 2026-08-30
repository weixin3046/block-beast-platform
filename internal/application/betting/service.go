package betting

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRoundNotFound = errors.New("round not found")
var ErrBetNotFound = errors.New("bet not found")
var ErrInvalidSelection = errors.New("selection must be valid JSON")
var ErrAccountDisabled = errors.New("account is disabled")
var ErrBettingBanned = errors.New("account is banned from betting")
var ErrStakeOutsideLimits = errors.New("stake is outside the configured play limits")
var ErrSelectionOutsidePlay = errors.New("selection is not available for this play")
var ErrHashRoomRequired = errors.New("hash room and play mode are required")
var ErrHashRoomConflict = errors.New("only one hash rate room may be used in the same round")
var ErrBetCancellationClosed = errors.New("bet can only be cancelled before betting closes")

type PlaceBetRequest struct {
	ClientRequestID string          `json:"client_request_id"`
	RoundID         string          `json:"round_id"`
	AccountID       string          `json:"account_id"`
	Currency        string          `json:"currency"`
	GameRoomID      string          `json:"game_room_id,omitempty"`
	PlayMode        string          `json:"play_mode,omitempty"`
	Selection       json.RawMessage `json:"selection"`
	StakeMinor      int64           `json:"stake_minor"`
}

type PlacedBet struct {
	BetID           string          `json:"bet_id"`
	ClientRequestID string          `json:"client_request_id"`
	RoundID         string          `json:"round_id"`
	AccountID       string          `json:"account_id"`
	Currency        string          `json:"currency"`
	GameRoomID      string          `json:"game_room_id,omitempty"`
	PlayMode        string          `json:"play_mode,omitempty"`
	Selection       json.RawMessage `json:"selection"`
	StakeMinor      int64           `json:"stake_minor"`
	Status          string          `json:"status"`
	PayoutMinor     int64           `json:"payout_minor"`
	PlacedAt        time.Time       `json:"placed_at"`
	SettledAt       *time.Time      `json:"settled_at,omitempty"`
}

// BetTaskHook 在积分投注成交后累计任务进度（如投注达标送体力），可为 nil。
// 在投注事务内调用，返回错误则整笔投注回滚。
type BetTaskHook interface {
	OnBetPlaced(ctx context.Context, tx pgx.Tx, userID, currency string, stakeMinor int64) error
}

type Service struct {
	pool     *pgxpool.Pool
	taskHook BetTaskHook
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// WithTaskHook 装配投注任务钩子；未装配时跳过任务进度累计。
func (service *Service) WithTaskHook(hook BetTaskHook) *Service {
	service.taskHook = hook
	return service
}

func (service *Service) Find(ctx context.Context, betID string) (PlacedBet, error) {
	var bet PlacedBet
	err := service.pool.QueryRow(ctx, `
		SELECT bets.id, bets.client_request_id, bets.round_id, bets.user_id, wallets.currency,
			COALESCE(bets.game_room_id::text,''),COALESCE(bets.play_mode,''),
			bets.selection, bets.stake_minor, bets.status, bets.payout_minor, bets.created_at, bets.settled_at
		FROM bets
		JOIN wallets ON wallets.id = bets.wallet_id
		WHERE bets.id = $1`, betID).
		Scan(&bet.BetID, &bet.ClientRequestID, &bet.RoundID, &bet.AccountID, &bet.Currency, &bet.GameRoomID, &bet.PlayMode, &bet.Selection, &bet.StakeMinor, &bet.Status, &bet.PayoutMinor, &bet.PlacedAt, &bet.SettledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, ErrBetNotFound
	}
	if err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func (service *Service) ListUserBets(ctx context.Context, userID, status string, limit int) ([]PlacedBet, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT bets.id,bets.client_request_id,bets.round_id,bets.user_id,wallets.currency,COALESCE(bets.game_room_id::text,''),COALESCE(bets.play_mode,''),bets.selection,bets.stake_minor,bets.status,bets.payout_minor,bets.created_at,bets.settled_at FROM bets JOIN wallets ON wallets.id=bets.wallet_id WHERE bets.user_id=$1`
	args := []any{userID, limit}
	if status != "" {
		query += ` AND bets.status=$3`
		args = append(args, status)
	}
	query += ` ORDER BY bets.created_at DESC LIMIT $2`
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PlacedBet, 0)
	for rows.Next() {
		var bet PlacedBet
		if err := rows.Scan(&bet.BetID, &bet.ClientRequestID, &bet.RoundID, &bet.AccountID, &bet.Currency, &bet.GameRoomID, &bet.PlayMode, &bet.Selection, &bet.StakeMinor, &bet.Status, &bet.PayoutMinor, &bet.PlacedAt, &bet.SettledAt); err != nil {
			return nil, err
		}
		items = append(items, bet)
	}
	return items, rows.Err()
}

// CancelBet 在封盘前原子取消玩家自己的投注并把本金退回原钱包。
func (service *Service) CancelBet(ctx context.Context, betID, userID string) (PlacedBet, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return PlacedBet{}, err
	}
	defer tx.Rollback(ctx)
	var bet PlacedBet
	var walletID string
	var betClosesAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT b.id,b.client_request_id,b.round_id,b.user_id,w.currency,
			COALESCE(b.game_room_id::text,''),COALESCE(b.play_mode,''),b.selection,b.stake_minor,
			b.status,b.payout_minor,b.created_at,b.settled_at,b.wallet_id,r.bet_closes_at
		FROM bets b JOIN wallets w ON w.id=b.wallet_id JOIN rounds r ON r.id=b.round_id
		WHERE b.id=$1 AND b.user_id=$2 FOR UPDATE OF b,w,r`, betID, userID).
		Scan(&bet.BetID, &bet.ClientRequestID, &bet.RoundID, &bet.AccountID, &bet.Currency,
			&bet.GameRoomID, &bet.PlayMode, &bet.Selection, &bet.StakeMinor, &bet.Status,
			&bet.PayoutMinor, &bet.PlacedAt, &bet.SettledAt, &walletID, &betClosesAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, ErrBetNotFound
	}
	if err != nil {
		return PlacedBet{}, err
	}
	if bet.Status == "cancelled" {
		return bet, tx.Commit(ctx)
	}
	if bet.Status != "accepted" || !time.Now().UTC().Before(betClosesAt) {
		return PlacedBet{}, ErrBetCancellationClosed
	}
	var availableMinor int64
	if err := tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1 FOR UPDATE`, walletID).Scan(&availableMinor); err != nil {
		return PlacedBet{}, err
	}
	if availableMinor > math.MaxInt64-bet.StakeMinor {
		return PlacedBet{}, errors.New("cancel refund would overflow wallet balance")
	}
	availableMinor += bet.StakeMinor
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=$3 WHERE id=$1`, walletID, availableMinor, now); err != nil {
		return PlacedBet{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bets SET status='cancelled',settled_at=$2 WHERE id=$1`, betID, now); err != nil {
		return PlacedBet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'bet_cancel',$3,'bet_refund',$4,$5)`, uuid.NewString(), walletID, betID, bet.StakeMinor, availableMinor); err != nil {
		return PlacedBet{}, err
	}
	payload, err := json.Marshal(map[string]string{"bet_id": betID, "round_id": bet.RoundID, "user_id": userID})
	if err != nil {
		return PlacedBet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,occurred_at) VALUES($1,'bet',$2,$3,$4,$5)`, uuid.NewString(), betID, events.BetCancelled, payload, now); err != nil {
		return PlacedBet{}, err
	}
	bet.Status, bet.SettledAt = "cancelled", &now
	if err := tx.Commit(ctx); err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func (service *Service) PlaceBet(ctx context.Context, request PlaceBetRequest) (PlacedBet, error) {
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.PlayMode = strings.ToLower(strings.TrimSpace(request.PlayMode))
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

	var userStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, request.AccountID).Scan(&userStatus); err != nil {
		return PlacedBet{}, err
	}
	if userStatus == "disabled" {
		return PlacedBet{}, ErrAccountDisabled
	}
	if userStatus == "bet_banned" {
		return PlacedBet{}, ErrBettingBanned
	}

	var status game.RoundStatus
	var betClosesAt time.Time
	var rawRules json.RawMessage
	err = tx.QueryRow(ctx, `
		SELECT rounds.status, rounds.bet_closes_at, game_types.rules
		FROM rounds
		JOIN game_types ON game_types.id=rounds.game_type_id
		WHERE rounds.id = $1
		FOR UPDATE OF rounds`, request.RoundID).Scan(&status, &betClosesAt, &rawRules)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, ErrRoundNotFound
	}
	if err != nil {
		return PlacedBet{}, err
	}
	if status != game.RoundOpen || !time.Now().UTC().Before(betClosesAt) {
		return PlacedBet{}, game.ErrBettingClosed
	}
	rules, err := game.ParseRules(rawRules)
	if err != nil {
		return PlacedBet{}, err
	}
	payoutMultiplier, payoutDivisor := rules.PayoutMultiplier, rules.PayoutScale()
	if sharedHashRules(rules) {
		if request.GameRoomID == "" || !validHashSelection(request.PlayMode, request.Selection) {
			return PlacedBet{}, ErrHashRoomRequired
		}
		var minStake, maxStake int64
		err := tx.QueryRow(ctx, `
			SELECT c.min_stake_minor,
				CASE $4 WHEN 'guess' THEN c.guess_max_stake_minor WHEN 'dodge' THEN c.dodge_max_stake_minor ELSE c.road_max_stake_minor END,
				CASE $4 WHEN 'guess' THEN c.guess_multiplier WHEN 'dodge' THEN c.dodge_multiplier ELSE c.road_multiplier END,
				CASE $4 WHEN 'guess' THEN c.guess_divisor WHEN 'dodge' THEN c.dodge_divisor ELSE c.road_divisor END
			FROM rounds r
			JOIN game_room_types grt ON grt.game_type_id=r.game_type_id
			JOIN game_rooms gr ON gr.id=grt.room_id AND gr.enabled=true
			JOIN hash_room_currency_configs c ON c.room_id=gr.id AND c.currency=$3
			WHERE r.id=$1 AND gr.id=$2`, request.RoundID, request.GameRoomID, request.Currency, request.PlayMode).
			Scan(&minStake, &maxStake, &payoutMultiplier, &payoutDivisor)
		if errors.Is(err, pgx.ErrNoRows) {
			return PlacedBet{}, ErrHashRoomRequired
		}
		if err != nil {
			return PlacedBet{}, err
		}
		if request.StakeMinor < minStake {
			return PlacedBet{}, ErrStakeOutsideLimits
		}
		var differentRoom bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bets WHERE round_id=$1 AND user_id=$2 AND status='accepted' AND game_room_id IS DISTINCT FROM $3::uuid)`, request.RoundID, request.AccountID, request.GameRoomID).Scan(&differentRoom); err != nil {
			return PlacedBet{}, err
		}
		if differentRoom {
			return PlacedBet{}, ErrHashRoomConflict
		}
		var existingStake int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(stake_minor),0) FROM bets WHERE round_id=$1 AND user_id=$2 AND game_room_id=$3 AND play_mode=$4 AND selection=$5 AND status='accepted'`, request.RoundID, request.AccountID, request.GameRoomID, request.PlayMode, request.Selection).Scan(&existingStake); err != nil {
			return PlacedBet{}, err
		}
		if existingStake > math.MaxInt64-request.StakeMinor || existingStake+request.StakeMinor > maxStake {
			return PlacedBet{}, ErrStakeOutsideLimits
		}
	} else {
		if !rules.SelectionAllowed(request.Selection) {
			return PlacedBet{}, ErrSelectionOutsidePlay
		}
		if limit, ok := rules.BetLimits[request.Currency]; ok &&
			(request.StakeMinor < limit.MinStakeMinor || request.StakeMinor > limit.MaxStakeMinor) {
			return PlacedBet{}, ErrStakeOutsideLimits
		}
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
		GameRoomID:      request.GameRoomID,
		PlayMode:        request.PlayMode,
		Selection:       append(json.RawMessage(nil), request.Selection...),
		StakeMinor:      request.StakeMinor,
		Status:          "accepted",
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
		INSERT INTO bets (id, client_request_id, round_id, user_id, wallet_id, game_room_id, play_mode,
			selection, stake_minor, status, payout_multiplier_snapshot, payout_divisor_snapshot)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,'')::uuid, NULLIF($7,''), $8, $9, 'accepted', $10, $11)
		RETURNING created_at`, bet.BetID, bet.ClientRequestID, bet.RoundID, bet.AccountID, walletID,
		bet.GameRoomID, bet.PlayMode, bet.Selection, bet.StakeMinor, payoutMultiplier, payoutDivisor).
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

	// 投注按任务配置的累计币种触发进度；重复请求已在上方返回，不会走到这里。
	if service.taskHook != nil {
		if err := service.taskHook.OnBetPlaced(ctx, tx, request.AccountID, request.Currency, request.StakeMinor); err != nil {
			return PlacedBet{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func findBet(ctx context.Context, tx pgx.Tx, accountID string, clientRequestID string) (PlacedBet, error) {
	var bet PlacedBet
	err := tx.QueryRow(ctx, `
		SELECT bets.id, bets.client_request_id, bets.round_id, bets.user_id, wallets.currency,
			COALESCE(bets.game_room_id::text,''),COALESCE(bets.play_mode,''),
			bets.selection, bets.stake_minor, bets.status, bets.created_at
		FROM bets
		JOIN wallets ON wallets.id = bets.wallet_id
		WHERE bets.user_id = $1 AND bets.client_request_id = $2`, accountID, clientRequestID).
		Scan(&bet.BetID, &bet.ClientRequestID, &bet.RoundID, &bet.AccountID, &bet.Currency, &bet.GameRoomID, &bet.PlayMode, &bet.Selection, &bet.StakeMinor, &bet.Status, &bet.PlacedAt)
	return bet, err
}

func sharedHashRules(rules game.Rules) bool {
	var extras struct {
		HashShared bool `json:"hash_shared"`
	}
	return json.Unmarshal(rules.Extras, &extras) == nil && extras.HashShared
}

func validHashSelection(mode string, raw json.RawMessage) bool {
	var selection struct {
		Pick string `json:"pick"`
	}
	if json.Unmarshal(raw, &selection) != nil {
		return false
	}
	switch mode {
	case "guess", "dodge":
		return len(selection.Pick) == 1 && selection.Pick[0] >= '0' && selection.Pick[0] <= '9'
	case "road":
		return selection.Pick == "big" || selection.Pick == "small" || selection.Pick == "odd" || selection.Pick == "even"
	default:
		return false
	}
}
