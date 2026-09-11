package betting

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/application/rebate"
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
var ErrRequestConflict = errors.New("bet request ID has already been used with different parameters")

type PlaceBetRequest struct {
	RobotPlanID     string          `json:"-"`
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
	PlacementCount              int64           `json:"placement_count"`
	LastPlacedAt                time.Time       `json:"last_placed_at"`
	Decimals                    int             `json:"decimals"`
	Stake                       string          `json:"stake"`
	Payout                      string          `json:"payout"`
	BetID                       string          `json:"bet_id"`
	ClientRequestID             string          `json:"client_request_id"`
	RoundID                     string          `json:"round_id"`
	AccountID                   string          `json:"account_id"`
	GameType                    string          `json:"game_type"`
	GameName                    string          `json:"game_name"`
	RoundSequence               int64           `json:"round_sequence"`
	Currency                    string          `json:"currency"`
	GameRoomID                  string          `json:"game_room_id,omitempty"`
	GameRoomCode                string          `json:"game_room_code,omitempty"`
	GameRoomName                string          `json:"game_room_name,omitempty"`
	PlayMode                    string          `json:"play_mode,omitempty"`
	Selection                   json.RawMessage `json:"selection"`
	StakeMinor                  int64           `json:"stake_minor"`
	PayoutMultiplier            int64           `json:"payout_multiplier"`
	PayoutDivisor               int64           `json:"payout_divisor"`
	PayoutRate                  string          `json:"payout_rate"`
	Status                      string          `json:"status"`
	PayoutMinor                 int64           `json:"payout_minor"`
	BalanceAfterBetMinor        *int64          `json:"balance_after_bet_minor,omitempty"`
	BalanceAfterRefundMinor     *int64          `json:"balance_after_refund_minor,omitempty"`
	BalanceAfterSettlementMinor *int64          `json:"balance_after_settlement_minor,omitempty"`
	PlacedAt                    time.Time       `json:"placed_at"`
	SettledAt                   *time.Time      `json:"settled_at,omitempty"`
}

type PublicPlayer struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	IsVirtual   bool   `json:"is_virtual"`
}

type PublicBet struct {
	PlacementCount   int64           `json:"placement_count"`
	LastPlacedAt     time.Time       `json:"last_placed_at"`
	Decimals         int             `json:"decimals"`
	Stake            string          `json:"stake"`
	Payout           string          `json:"payout"`
	BetID            string          `json:"bet_id"`
	Player           PublicPlayer    `json:"player"`
	RoundID          string          `json:"round_id"`
	GameType         string          `json:"game_type"`
	GameName         string          `json:"game_name"`
	RoundSequence    int64           `json:"round_sequence"`
	Currency         string          `json:"currency"`
	GameRoomID       string          `json:"game_room_id,omitempty"`
	GameRoomCode     string          `json:"game_room_code,omitempty"`
	GameRoomName     string          `json:"game_room_name,omitempty"`
	PlayMode         string          `json:"play_mode,omitempty"`
	Selection        json.RawMessage `json:"selection"`
	StakeMinor       int64           `json:"stake_minor"`
	PayoutMultiplier int64           `json:"payout_multiplier"`
	PayoutDivisor    int64           `json:"payout_divisor"`
	PayoutRate       string          `json:"payout_rate"`
	Status           string          `json:"status"`
	PayoutMinor      int64           `json:"payout_minor"`
	PlacedAt         time.Time       `json:"placed_at"`
	SettledAt        *time.Time      `json:"settled_at,omitempty"`
}

type PublicBetQuery struct {
	GameType   string
	Currency   string
	Status     string
	PlayerType string
	Limit      int
	Offset     int
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) Find(ctx context.Context, betID string) (PlacedBet, error) {
	bet, err := scanPlacedBet(service.pool.QueryRow(ctx, placedBetSelect+` WHERE b.id=$1`, betID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, ErrBetNotFound
	}
	if err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func (service *Service) ListUserBets(ctx context.Context, userID, status string, limit, offset int) ([]PlacedBet, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := placedBetSelect + ` WHERE b.user_id=$1`
	args := []any{userID, limit, offset}
	if status != "" {
		query += ` AND b.status=$4`
		args = append(args, status)
	}
	query += ` ORDER BY b.created_at DESC,b.id DESC LIMIT $2 OFFSET $3`
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PlacedBet, 0)
	for rows.Next() {
		bet, err := scanPlacedBet(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, bet)
	}
	return items, rows.Err()
}

const placedBetSelect = `
	SELECT b.id::text,b.client_request_id,b.round_id::text,b.user_id::text,
		gt.code,gt.name,r.sequence,w.currency,
		COALESCE(b.game_room_id::text,''),COALESCE(gr.code,''),COALESCE(gr.name,''),COALESCE(b.play_mode,''),
		b.selection,b.stake_minor,COALESCE(b.payout_multiplier_snapshot,0),COALESCE(b.payout_divisor_snapshot,0),
		b.status,b.payout_minor,debit.balance_after_minor,b.balance_after_settlement_minor,b.created_at,b.settled_at,c.decimals,
		b.placement_count,COALESCE(b.last_placed_at,b.created_at)
	FROM bets b
	JOIN wallets w ON w.id=b.wallet_id
	JOIN currencies c ON c.code=w.currency
	JOIN rounds r ON r.id=b.round_id
	JOIN game_types gt ON gt.id=r.game_type_id
	LEFT JOIN game_rooms gr ON gr.id=b.game_room_id
	LEFT JOIN LATERAL (SELECT le.balance_after_minor FROM ledger_entries le
		LEFT JOIN bet_placements bp ON bp.id=le.bet_placement_id
		WHERE le.wallet_id=b.wallet_id AND le.business_type='bet'
		AND le.business_id=b.id::text AND le.entry_type='bet_debit'
		ORDER BY COALESCE(bp.created_at,le.occurred_at) DESC,le.id DESC LIMIT 1) debit ON true`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlacedBet(row rowScanner) (PlacedBet, error) {
	var bet PlacedBet
	err := row.Scan(&bet.BetID, &bet.ClientRequestID, &bet.RoundID, &bet.AccountID,
		&bet.GameType, &bet.GameName, &bet.RoundSequence, &bet.Currency,
		&bet.GameRoomID, &bet.GameRoomCode, &bet.GameRoomName, &bet.PlayMode,
		&bet.Selection, &bet.StakeMinor, &bet.PayoutMultiplier, &bet.PayoutDivisor,
		&bet.Status, &bet.PayoutMinor, &bet.BalanceAfterBetMinor, &bet.BalanceAfterSettlementMinor, &bet.PlacedAt, &bet.SettledAt, &bet.Decimals,
		&bet.PlacementCount, &bet.LastPlacedAt)
	if err != nil {
		return PlacedBet{}, err
	}
	bet.PayoutRate = formatPayoutRate(bet.PayoutMultiplier, bet.PayoutDivisor)
	if bet.Stake, err = wallet.FormatDisplayAmount(bet.StakeMinor, bet.Decimals); err != nil {
		return PlacedBet{}, err
	}
	if bet.Payout, err = wallet.FormatDisplayAmount(bet.PayoutMinor, bet.Decimals); err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func formatPayoutRate(multiplier, divisor int64) string {
	if multiplier <= 0 || divisor <= 0 {
		return ""
	}
	value := new(big.Rat).SetFrac64(multiplier, divisor).FloatString(6)
	value = strings.TrimRight(value, "0")
	return strings.TrimRight(value, ".")
}

func (service *Service) ListPublicBets(ctx context.Context, query PublicBetQuery) ([]PublicBet, error) {
	if query.Limit <= 0 || query.Limit > 100 {
		query.Limit = 50
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	query.Currency = strings.ToUpper(strings.TrimSpace(query.Currency))
	query.GameType = strings.TrimSpace(query.GameType)
	query.Status = strings.TrimSpace(query.Status)
	query.PlayerType = strings.TrimSpace(query.PlayerType)
	if query.PlayerType == "" {
		query.PlayerType = "all"
	}
	rows, err := service.pool.Query(ctx, publicBetSelect+`
		WHERE ($1='' OR gt.code=$1)
			AND ($2='' OR w.currency=$2)
			AND ($3='' OR b.status=$3)
			AND ($4='all' OR ($4='real' AND u.is_virtual=false) OR ($4='virtual' AND u.is_virtual=true))
		ORDER BY b.created_at DESC,b.id DESC
		LIMIT $5 OFFSET $6`, query.GameType, query.Currency, query.Status, query.PlayerType, query.Limit, query.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PublicBet, 0)
	for rows.Next() {
		item, err := scanPublicBet(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const publicBetSelect = `
		SELECT b.id::text,u.public_id,u.display_name,
			CASE WHEN u.avatar_url LIKE 'uploads/%'
				THEN '/v1/avatars/' || u.public_id::text || '?v=' || regexp_replace(u.avatar_url, '^.*/', '')
				ELSE COALESCE(u.avatar_url,'') END,u.is_virtual,
			b.round_id::text,gt.code,gt.name,r.sequence,w.currency,
			COALESCE(b.game_room_id::text,''),COALESCE(gr.code,''),COALESCE(gr.name,''),COALESCE(b.play_mode,''),
			b.selection,b.stake_minor,COALESCE(b.payout_multiplier_snapshot,0),COALESCE(b.payout_divisor_snapshot,0),
			b.status,b.payout_minor,b.created_at,b.settled_at,c.decimals,
			b.placement_count,COALESCE(b.last_placed_at,b.created_at)
		FROM bets b
		JOIN users u ON u.id=b.user_id
		JOIN wallets w ON w.id=b.wallet_id
		JOIN currencies c ON c.code=w.currency
		JOIN rounds r ON r.id=b.round_id
		JOIN game_types gt ON gt.id=r.game_type_id
		LEFT JOIN game_rooms gr ON gr.id=b.game_room_id`

func scanPublicBet(row rowScanner) (PublicBet, error) {
	var item PublicBet
	if err := row.Scan(&item.BetID, &item.Player.UserID, &item.Player.DisplayName, &item.Player.AvatarURL, &item.Player.IsVirtual,
		&item.RoundID, &item.GameType, &item.GameName, &item.RoundSequence, &item.Currency, &item.GameRoomID,
		&item.GameRoomCode, &item.GameRoomName, &item.PlayMode, &item.Selection, &item.StakeMinor,
		&item.PayoutMultiplier, &item.PayoutDivisor, &item.Status,
		&item.PayoutMinor, &item.PlacedAt, &item.SettledAt, &item.Decimals, &item.PlacementCount, &item.LastPlacedAt); err != nil {
		return PublicBet{}, err
	}
	item.PayoutRate = formatPayoutRate(item.PayoutMultiplier, item.PayoutDivisor)
	var err error
	if item.Stake, err = wallet.FormatDisplayAmount(item.StakeMinor, item.Decimals); err != nil {
		return PublicBet{}, err
	}
	if item.Payout, err = wallet.FormatDisplayAmount(item.PayoutMinor, item.Decimals); err != nil {
		return PublicBet{}, err
	}
	return item, nil
}

// CancelBet 在封盘前原子取消玩家自己的投注并把本金退回原钱包。
func (service *Service) CancelBet(ctx context.Context, betID, userID string) (PlacedBet, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return PlacedBet{}, err
	}
	defer tx.Rollback(ctx)
	var bet PlacedBet
	// Serialize with settlement before locking this bet or its wallet.
	var cancelRoundID string
	if err = tx.QueryRow(ctx, `SELECT round_id::text FROM bets WHERE id=$1 AND user_id=$2`, betID, userID).Scan(&cancelRoundID); errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, ErrBetNotFound
	} else if err != nil {
		return PlacedBet{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM rounds WHERE id=$1 FOR UPDATE`, cancelRoundID); err != nil {
		return PlacedBet{}, err
	}
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
	var simulated bool
	if err = tx.QueryRow(ctx, `SELECT is_simulated FROM bets WHERE id=$1`, betID).Scan(&simulated); err != nil {
		return PlacedBet{}, err
	}
	if simulated {
		if bet.Status == "accepted" && time.Now().UTC().Before(betClosesAt) {
			_, err = tx.Exec(ctx, `UPDATE bets SET status='cancelled',settled_at=now(),balance_after_settlement_minor=(SELECT available_minor FROM wallets WHERE id=$2) WHERE id=$1`, betID, walletID)
			if err != nil {
				return PlacedBet{}, err
			}
			publicBet, err := scanPublicBet(tx.QueryRow(ctx, publicBetSelect+` WHERE b.id=$1`, betID))
			if err != nil {
				return PlacedBet{}, err
			}
			payload, err := json.Marshal(struct {
				Bet PublicBet `json:"bet"`
			}{Bet: publicBet})
			if err != nil {
				return PlacedBet{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'bet',$2,$3,$4)`, uuid.NewString(), betID, events.BetCancelled, payload); err != nil {
				return PlacedBet{}, err
			}
		} else if bet.Status != "cancelled" {
			return PlacedBet{}, ErrBetCancellationClosed
		}
		result, err := findBet(ctx, tx, userID, bet.ClientRequestID)
		if err != nil {
			return PlacedBet{}, err
		}
		return result, tx.Commit(ctx)
	}
	if bet.Status == "cancelled" {
		bet, err = cancelledBetResult(ctx, tx, bet.AccountID, bet.ClientRequestID, bet.BetID)
		if err != nil {
			return PlacedBet{}, err
		}
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
	if _, err := tx.Exec(ctx, `UPDATE bets SET status='cancelled',settled_at=$2,balance_after_settlement_minor=$3 WHERE id=$1`, betID, now, availableMinor); err != nil {
		return PlacedBet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'bet_cancel',$3,'bet_refund',$4,$5)`, uuid.NewString(), walletID, betID, bet.StakeMinor, availableMinor); err != nil {
		return PlacedBet{}, err
	}
	publicBet, err := scanPublicBet(tx.QueryRow(ctx, publicBetSelect+` WHERE b.id=$1`, betID))
	if err != nil {
		return PlacedBet{}, err
	}
	payload, err := json.Marshal(struct {
		Bet PublicBet `json:"bet"`
	}{Bet: publicBet})
	if err != nil {
		return PlacedBet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,occurred_at) VALUES($1,'bet',$2,$3,$4,$5)`, uuid.NewString(), betID, events.BetCancelled, payload, now); err != nil {
		return PlacedBet{}, err
	}
	bet, err = cancelledBetResult(ctx, tx, userID, bet.ClientRequestID, betID)
	if err != nil {
		return PlacedBet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlacedBet{}, err
	}
	return bet, nil
}

func cancelledBetResult(ctx context.Context, tx pgx.Tx, userID, clientRequestID, betID string) (PlacedBet, error) {
	bet, err := findBet(ctx, tx, userID, clientRequestID)
	if err != nil {
		return PlacedBet{}, err
	}
	var balance int64
	err = tx.QueryRow(ctx, `
		SELECT balance_after_minor FROM ledger_entries
		WHERE business_type='bet_cancel' AND business_id=$1 AND entry_type='bet_refund'`, betID).Scan(&balance)
	if err != nil {
		return PlacedBet{}, err
	}
	bet.BalanceAfterRefundMinor = &balance
	return bet, nil
}

func (service *Service) PlaceBet(ctx context.Context, request PlaceBetRequest) (PlacedBet, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return PlacedBet{}, err
	}
	defer tx.Rollback(ctx)
	bet, err := service.placeBetTx(ctx, tx, request, false)
	if err != nil {
		return PlacedBet{}, err
	}
	return bet, tx.Commit(ctx)
}

// PlaceRobotBetTx is internal-only and must be committed with plan progression.
func (service *Service) PlaceRobotBetTx(ctx context.Context, tx pgx.Tx, request PlaceBetRequest) (PlacedBet, error) {
	return service.placeBetTx(ctx, tx, request, true)
}

func (service *Service) placeBetTx(ctx context.Context, tx pgx.Tx, request PlaceBetRequest, requireRobot bool) (PlacedBet, error) {
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.PlayMode = strings.ToLower(strings.TrimSpace(request.PlayMode))
	if request.StakeMinor <= 0 {
		return PlacedBet{}, game.ErrInvalidStake
	}
	if !json.Valid(request.Selection) {
		return PlacedBet{}, ErrInvalidSelection
	}

	existing, err := findPlacement(ctx, tx, request)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, err
	}

	var userStatus string
	var simulated bool
	if err := tx.QueryRow(ctx, `SELECT status,is_virtual FROM users WHERE id=$1 FOR UPDATE`, request.AccountID).Scan(&userStatus, &simulated); err != nil {
		return PlacedBet{}, err
	}
	// Recheck after serializing this user's writes, before state/limit checks.
	// A concurrent retry must succeed even if the first request reached a limit.
	if existing, err = findPlacement(ctx, tx, request); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PlacedBet{}, err
	}
	if requireRobot && !simulated {
		return PlacedBet{}, ErrBettingBanned
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
	var earlierUnfinished bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rounds prior JOIN rounds target
	 ON prior.game_type_id=target.game_type_id AND prior.sequence<target.sequence
	 WHERE target.id=$1 AND prior.status IN ('scheduled','open','closed','settling'))`, request.RoundID).Scan(&earlierUnfinished); err != nil {
		return PlacedBet{}, err
	}
	if earlierUnfinished {
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
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(b.stake_minor),0) FROM bets b JOIN wallets w ON w.id=b.wallet_id WHERE b.round_id=$1 AND b.user_id=$2 AND b.game_room_id=$3 AND b.play_mode=$4 AND b.selection->>'pick'=$5::jsonb->>'pick' AND b.status='accepted' AND w.currency=$6`, request.RoundID, request.AccountID, request.GameRoomID, request.PlayMode, request.Selection, request.Currency).Scan(&existingStake); err != nil {
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
	if !simulated && availableMinor < request.StakeMinor {
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
	merging := false
	if sharedHashRules(rules) {
		// The round lock is shared with cancel/void/settlement. Never combine
		// different currencies, picks, rooms, or historical non-merge orders.
		var total int64
		err = tx.QueryRow(ctx, `SELECT id::text,stake_minor FROM bets
			WHERE user_id=$1 AND round_id=$2 AND wallet_id=$3 AND game_room_id=$4
			AND play_mode=$5 AND selection->>'pick'=$6::jsonb->>'pick'
			AND status='accepted' AND merge_enabled AND is_simulated=$7 FOR UPDATE`,
			request.AccountID, request.RoundID, walletID, request.GameRoomID, request.PlayMode, request.Selection, simulated).Scan(&bet.BetID, &total)
		if err == nil {
			if total > math.MaxInt64-request.StakeMinor {
				return PlacedBet{}, ErrStakeOutsideLimits
			}
			merging = true
			bet.StakeMinor += total
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return PlacedBet{}, err
		}
	}
	if !simulated {
		availableMinor -= request.StakeMinor
		_, err = tx.Exec(ctx, `
		UPDATE wallets
		SET available_minor = $2, version = version + 1, updated_at = now()
		WHERE id = $1`, walletID, availableMinor)
		if err != nil {
			return PlacedBet{}, err
		}
	}
	if merging {
		_, err = tx.Exec(ctx, `UPDATE bets SET stake_minor=$2,placement_count=placement_count+1,last_placed_at=clock_timestamp() WHERE id=$1`, bet.BetID, bet.StakeMinor)
	} else {
		err = tx.QueryRow(ctx, `
		INSERT INTO bets (id, client_request_id, round_id, user_id, wallet_id, game_room_id, play_mode,
			selection, stake_minor, status, payout_multiplier_snapshot, payout_divisor_snapshot,is_simulated,robot_plan_id,merge_enabled,last_placed_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,'')::uuid, NULLIF($7,''), $8, $9, 'accepted', $10, $11,$12,NULLIF($13,'')::uuid,$14,clock_timestamp())
		RETURNING created_at`, bet.BetID, bet.ClientRequestID, bet.RoundID, bet.AccountID, walletID,
			bet.GameRoomID, bet.PlayMode, bet.Selection, bet.StakeMinor, payoutMultiplier, payoutDivisor, simulated, request.RobotPlanID, sharedHashRules(rules)).
			Scan(&bet.PlacedAt)
	}
	if err != nil {
		return PlacedBet{}, err
	}
	if sharedHashRules(rules) && !merging {
		if err = rebate.SnapshotTx(ctx, tx, bet.BetID); err != nil {
			return PlacedBet{}, err
		}
	}
	placementID := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO bet_placements(id,bet_id,user_id,round_id,client_request_id,currency,game_room_id,play_mode,selection,stake_minor,robot_plan_id)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,NULLIF($8,''),$9,$10,NULLIF($11,'')::uuid)`,
		placementID, bet.BetID, request.AccountID, request.RoundID, request.ClientRequestID, request.Currency, request.GameRoomID, request.PlayMode, request.Selection, request.StakeMinor, request.RobotPlanID)
	if err != nil {
		return PlacedBet{}, err
	}

	if !simulated {
		_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor,bet_placement_id
		) VALUES ($1, $2, 'bet', $3, 'bet_debit', $4, $5,$6)`, uuid.NewString(), walletID, bet.BetID, -request.StakeMinor, availableMinor, placementID)
		if err != nil {
			return PlacedBet{}, err
		}
	}
	publicBet, err := scanPublicBet(tx.QueryRow(ctx, publicBetSelect+` WHERE b.id=$1`, bet.BetID))
	if err != nil {
		return PlacedBet{}, err
	}
	addedStake, err := wallet.FormatDisplayAmount(request.StakeMinor, publicBet.Decimals)
	if err != nil {
		return PlacedBet{}, err
	}
	payload, err := json.Marshal(struct {
		Bet         PublicBet `json:"bet"`
		PlacementID string    `json:"placement_id"`
		AddedStake  string    `json:"added_stake"`
	}{Bet: publicBet, PlacementID: placementID, AddedStake: addedStake})
	if err != nil {
		return PlacedBet{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'bet', $2, $3, $4)`, uuid.NewString(), bet.BetID, events.BetPlaced, payload)
	if err != nil {
		return PlacedBet{}, err
	}

	bet, err = findPlacement(ctx, tx, request)
	if err != nil {
		return PlacedBet{}, err
	}

	return bet, nil
}

func findBet(ctx context.Context, tx pgx.Tx, accountID string, clientRequestID string) (PlacedBet, error) {
	return scanPlacedBet(tx.QueryRow(ctx, placedBetSelect+` WHERE b.user_id=$1 AND b.client_request_id=$2`, accountID, clientRequestID))
}

// Return the current canonical order, while validating against the immutable
// incremental request (the order's stake may already contain later additions).
func findPlacement(ctx context.Context, tx pgx.Tx, request PlaceBetRequest) (PlacedBet, error) {
	var betID string
	var matches bool
	err := tx.QueryRow(ctx, `SELECT bet_id::text,
		round_id=$3::uuid AND currency=$4 AND game_room_id IS NOT DISTINCT FROM NULLIF($5,'')::uuid
		AND COALESCE(play_mode,'')=$6 AND selection=$7::jsonb AND stake_minor=$8
		AND robot_plan_id IS NOT DISTINCT FROM NULLIF($9,'')::uuid
		FROM bet_placements WHERE user_id=$1 AND client_request_id=$2`,
		request.AccountID, request.ClientRequestID, request.RoundID, request.Currency, request.GameRoomID,
		request.PlayMode, request.Selection, request.StakeMinor, request.RobotPlanID).Scan(&betID, &matches)
	if err != nil {
		return PlacedBet{}, err
	}
	if !matches {
		return PlacedBet{}, ErrRequestConflict
	}
	bet, err := scanPlacedBet(tx.QueryRow(ctx, placedBetSelect+` WHERE b.id=$1`, betID))
	bet.ClientRequestID = request.ClientRequestID
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
