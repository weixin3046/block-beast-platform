package operations

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidBetVoid = errors.New("作废请求参数无效")
var ErrBetVoidNotFound = errors.New("投注不存在")
var ErrBetNotVoidable = errors.New("只有未最终结算的已接受投注可以作废")
var ErrBetVoidConflict = errors.New("请求编号已用于其他作废操作，请勿修改参数后重用")
var ErrBetVoidBalanceOverflow = errors.New("退款后余额超出允许范围")

type BetVoidInput struct {
	OperatorID string `json:"-"`
	RequestID  string `json:"request_id"`
	BetID      string `json:"-"`
	Reason     string `json:"reason"`
}

type BetVoidResult struct {
	OperationID    string    `json:"operation_id"`
	BetID          string    `json:"bet_id"`
	UserID         int64     `json:"user_id"`
	OperatorUserID int64     `json:"operator_user_id"`
	GameType       string    `json:"game_type"`
	RoundSequence  int64     `json:"round_sequence"`
	Currency       string    `json:"currency"`
	StakeMinor     int64     `json:"stake_minor"`
	RefundMinor    int64     `json:"refund_minor"`
	IsSimulated    bool      `json:"is_simulated"`
	Reason         string    `json:"reason"`
	Status         string    `json:"status"`
	Duplicate      bool      `json:"duplicate"`
	CreatedAt      time.Time `json:"created_at"`
}

type BetVoidPage struct {
	Items []BetVoidResult `json:"items"`
	Total int64           `json:"total"`
}

func (s *Service) VoidBet(ctx context.Context, in BetVoidInput) (BetVoidResult, error) {
	in.RequestID = strings.TrimSpace(in.RequestID)
	in.Reason = strings.TrimSpace(in.Reason)
	if _, err := uuid.Parse(in.OperatorID); err != nil || len(in.RequestID) == 0 || len(in.RequestID) > 128 || len(in.Reason) == 0 || len(in.Reason) > 2000 {
		return BetVoidResult{}, ErrInvalidBetVoid
	}
	if _, err := uuid.Parse(in.BetID); err != nil {
		return BetVoidResult{}, ErrInvalidBetVoid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BetVoidResult{}, err
	}
	defer tx.Rollback(ctx)
	operatorPublicID, err := requireAdminOperator(ctx, tx, in.OperatorID)
	if err != nil {
		return BetVoidResult{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bet_void:"+in.OperatorID+":"+in.RequestID); err != nil {
		return BetVoidResult{}, err
	}
	var oldBetID, oldReason string
	var data []byte
	err = tx.QueryRow(ctx, `SELECT bet_id::text,reason,result FROM admin_bet_voids WHERE operator_id=$1 AND request_id=$2`, in.OperatorID, in.RequestID).
		Scan(&oldBetID, &oldReason, &data)
	if err == nil {
		if oldBetID != in.BetID || oldReason != in.Reason {
			return BetVoidResult{}, ErrBetVoidConflict
		}
		var out BetVoidResult
		if err = json.Unmarshal(data, &out); err != nil {
			return BetVoidResult{}, err
		}
		out.Duplicate = true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BetVoidResult{}, err
	}
	var roundID string
	err = tx.QueryRow(ctx, `SELECT round_id::text FROM bets WHERE id=$1`, in.BetID).Scan(&roundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BetVoidResult{}, ErrBetVoidNotFound
	}
	if err != nil {
		return BetVoidResult{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM rounds WHERE id=$1 FOR UPDATE`, roundID); err != nil {
		return BetVoidResult{}, err
	}
	var walletID, status string
	var balance int64
	out := BetVoidResult{OperationID: uuid.NewString(), BetID: in.BetID, OperatorUserID: operatorPublicID, Reason: in.Reason, CreatedAt: time.Now().UTC()}
	err = tx.QueryRow(ctx, `SELECT b.wallet_id::text,b.status,b.stake_minor,b.is_simulated,u.public_id,w.currency,r.sequence,gt.code
		FROM bets b JOIN users u ON u.id=b.user_id JOIN wallets w ON w.id=b.wallet_id
		JOIN rounds r ON r.id=b.round_id JOIN game_types gt ON gt.id=r.game_type_id
		WHERE b.id=$1 FOR UPDATE OF b`, in.BetID).
		Scan(&walletID, &status, &out.StakeMinor, &out.IsSimulated, &out.UserID, &out.Currency, &out.RoundSequence, &out.GameType)
	if errors.Is(err, pgx.ErrNoRows) {
		return BetVoidResult{}, ErrBetVoidNotFound
	}
	if err != nil {
		return BetVoidResult{}, err
	}
	if status != "accepted" {
		return BetVoidResult{}, ErrBetNotVoidable
	}
	if err = tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1 FOR UPDATE`, walletID).Scan(&balance); err != nil {
		return BetVoidResult{}, err
	}
	out.Status = "voided"
	if !out.IsSimulated {
		out.RefundMinor = out.StakeMinor
		if balance > math.MaxInt64-out.StakeMinor {
			return BetVoidResult{}, ErrBetVoidBalanceOverflow
		}
		balance += out.StakeMinor
		if _, err = tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=$3 WHERE id=$1`, walletID, balance, out.CreatedAt); err != nil {
			return BetVoidResult{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,remark,operator_id)
			VALUES($1,$2,'bet_void',$3,'bet_void_refund',$4,$5,$6,$7)`, uuid.NewString(), walletID, out.OperationID, out.RefundMinor, balance, in.Reason, in.OperatorID); err != nil {
			return BetVoidResult{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE bets SET status='voided',settled_at=$2,balance_after_settlement_minor=$3 WHERE id=$1`, in.BetID, out.CreatedAt, balance); err != nil {
		return BetVoidResult{}, err
	}
	data, err = json.Marshal(out)
	if err != nil {
		return BetVoidResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_bet_voids(id,operator_id,request_id,bet_id,user_id,wallet_id,round_id,round_sequence,game_type,currency,stake_minor,refund_minor,is_simulated,reason,result,created_at)
		SELECT $1,$2,$3,b.id,b.user_id,b.wallet_id,b.round_id,$4,$5,$6,$7,$8,$9,$10,$11,$12 FROM bets b WHERE b.id=$13`,
		out.OperationID, in.OperatorID, in.RequestID, out.RoundSequence, out.GameType, out.Currency, out.StakeMinor, out.RefundMinor, out.IsSimulated, in.Reason, data, out.CreatedAt, in.BetID); err != nil {
		return BetVoidResult{}, err
	}
	// Game events are public broadcasts: never broadcast operator, reason or
	// refund details from the private administrative result.
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,occurred_at)
		VALUES($1,'bet',$2,'game.bet.voided',jsonb_build_object('bet_id',$2::text,'round_id',$3::text,'status','voided'),$4)`, uuid.NewString(), in.BetID, roundID, out.CreatedAt); err != nil {
		return BetVoidResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,request_id,payload,created_at)
		VALUES($1,$2,'admin.bet.void','bet',$3,$4,$5,$6)`, uuid.NewString(), in.OperatorID, in.BetID, in.RequestID, data, out.CreatedAt); err != nil {
		return BetVoidResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BetVoidResult{}, err
	}
	return out, nil
}

func (s *Service) ListBetVoids(ctx context.Context, actorID string, limit, offset int) (BetVoidPage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return BetVoidPage{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = requireAdminOperator(ctx, tx, actorID); err != nil {
		return BetVoidPage{}, err
	}
	page := BetVoidPage{Items: []BetVoidResult{}}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_bet_voids`).Scan(&page.Total); err != nil {
		return BetVoidPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT result FROM admin_bet_voids ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return BetVoidPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var item BetVoidResult
		if err = rows.Scan(&data); err != nil {
			return BetVoidPage{}, err
		}
		if err = json.Unmarshal(data, &item); err != nil {
			return BetVoidPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return BetVoidPage{}, err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return BetVoidPage{}, err
	}
	return page, nil
}
