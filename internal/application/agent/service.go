package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidRelation = errors.New("invalid agent relation")
var ErrRelationExists = errors.New("agent relation already exists")
var ErrInvalidCommissionRate = errors.New("commission rate must be between 0 and 10000 basis points")
var ErrCommissionNotFound = errors.New("commission not found")
var ErrCommissionState = errors.New("commission cannot transition from its current status")
var ErrInsufficientCommissionBalance = errors.New("insufficient commission balance for reversal")
var ErrInvalidCommissionAdjustment = errors.New("invalid commission adjustment")

type Service struct{ pool *pgxpool.Pool }

type Relation struct {
	UserID       string `json:"user_id"`
	ParentUserID string `json:"parent_user_id"`
}

type Commission struct {
	ID          string `json:"id"`
	BetID       string `json:"bet_id"`
	AgentID     string `json:"agent_id"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
}

type TeamSummary struct {
	DirectPlayers int64        `json:"direct_players"`
	Metrics       []TeamMetric `json:"metrics"`
}

type TeamMetric struct {
	Currency            string `json:"currency"`
	SettledBets         int64  `json:"settled_bets"`
	ValidStakeMinor     int64  `json:"valid_stake_minor"`
	PaidCommissionMinor int64  `json:"paid_commission_minor"`
}

func (service *Service) ListCommissions(ctx context.Context, agentID string, limit int) ([]Commission, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `SELECT id::text,source_bet_id::text,beneficiary_user_id::text,currency,amount_minor,status FROM commission_entries WHERE beneficiary_user_id=$1 ORDER BY id DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Commission, 0)
	for rows.Next() {
		var item Commission
		if err := rows.Scan(&item.ID, &item.BetID, &item.AgentID, &item.Currency, &item.AmountMinor, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) ListAllCommissions(ctx context.Context, status string, limit int) ([]Commission, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT id::text,source_bet_id::text,beneficiary_user_id::text,currency,amount_minor,status FROM commission_entries`
	args := []any{limit}
	if status != "" {
		query += ` WHERE status=$2`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT $1`
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Commission, 0)
	for rows.Next() {
		var item Commission
		if err := rows.Scan(&item.ID, &item.BetID, &item.AgentID, &item.Currency, &item.AmountMinor, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) TeamSummary(ctx context.Context, agentID string) (TeamSummary, error) {
	var summary TeamSummary
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM agent_relations WHERE parent_user_id=$1`, agentID).Scan(&summary.DirectPlayers); err != nil {
		return TeamSummary{}, err
	}
	rows, err := service.pool.Query(ctx, `
		SELECT wallets.currency,
			count(*) FILTER (WHERE bets.status IN ('won','lost')),
			COALESCE(sum(bets.stake_minor) FILTER (WHERE bets.status IN ('won','lost')),0),
			COALESCE((SELECT sum(amount_minor) FROM commission_entries WHERE beneficiary_user_id=$1 AND status='paid' AND currency=wallets.currency),0)
		FROM bets
		JOIN wallets ON wallets.id=bets.wallet_id
		JOIN agent_relations ON agent_relations.user_id=bets.user_id
		WHERE agent_relations.parent_user_id=$1
		GROUP BY wallets.currency`, agentID)
	if err != nil {
		return TeamSummary{}, err
	}
	defer rows.Close()
	summary.Metrics = make([]TeamMetric, 0)
	for rows.Next() {
		var metric TeamMetric
		if err := rows.Scan(&metric.Currency, &metric.SettledBets, &metric.ValidStakeMinor, &metric.PaidCommissionMinor); err != nil {
			return TeamSummary{}, err
		}
		summary.Metrics = append(summary.Metrics, metric)
	}
	return summary, rows.Err()
}

func (service *Service) ReverseCommission(ctx context.Context, commissionID string) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var agentID, currency, status string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT beneficiary_user_id::text,currency,amount_minor,status FROM commission_entries WHERE id=$1 FOR UPDATE`, commissionID).
		Scan(&agentID, &currency, &amount, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCommissionNotFound
	}
	if err != nil {
		return err
	}
	if status != "paid" {
		return ErrCommissionState
	}
	var walletID string
	var available int64
	if err := tx.QueryRow(ctx, `SELECT id,available_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, agentID, currency).Scan(&walletID, &available); err != nil {
		return err
	}
	if available < amount {
		return ErrInsufficientCommissionBalance
	}
	available -= amount
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=now() WHERE id=$1`, walletID, available); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE commission_entries SET status='reversed' WHERE id=$1`, commissionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'commission',$3,'commission_reversal',$4,$5)`, uuid.NewString(), walletID, commissionID, -amount, available); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (service *Service) GrantCommission(ctx context.Context, requestID, agentID, currency string, amount int64, remark, operatorID string) (string, error) {
	if requestID == "" || agentID == "" || (currency != "POINTS" && currency != "USDT" && currency != "JADE" && currency != "ORIGIN_STONE") || amount <= 0 || operatorID == "" {
		return "", ErrInvalidCommissionAdjustment
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM commission_adjustments WHERE request_id=$1`, requestID).Scan(&existingID)
	if err == nil {
		return existingID, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var walletID string
	var available int64
	err = tx.QueryRow(ctx, `SELECT id,available_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, agentID, currency).Scan(&walletID, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,$3) RETURNING id,available_minor`, uuid.NewString(), agentID, currency).Scan(&walletID, &available)
	}
	if err != nil {
		return "", err
	}
	adjustmentID := uuid.NewString()
	available += amount
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=now() WHERE id=$1`, walletID, available); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO commission_adjustments(id,request_id,agent_user_id,currency,amount_minor,remark,operator_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, adjustmentID, requestID, agentID, currency, amount, remark, operatorID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'commission_adjustment',$3,'commission_manual_credit',$4,$5)`, uuid.NewString(), walletID, adjustmentID, amount, available); err != nil {
		return "", err
	}
	return adjustmentID, tx.Commit(ctx)
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Bind creates an immutable direct referral relation and a materialized ltree path.
func (service *Service) Bind(ctx context.Context, userID, parentID string) error {
	if userID == "" || parentID == "" || userID == parentID {
		return ErrInvalidRelation
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, parentID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalidRelation
	}
	var parentPath string
	err = tx.QueryRow(ctx, `SELECT COALESCE(path::text,'') FROM agent_relations WHERE user_id=$1`, parentID).Scan(&parentPath)
	if errors.Is(err, pgx.ErrNoRows) {
		parentPath = ""
	} else if err != nil {
		return err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT parent_user_id::text FROM agent_relations WHERE user_id=$1`, userID).Scan(&existing)
	if err == nil {
		return ErrRelationExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	userLabel := strings.ReplaceAll(userID, "-", "_")
	parentLabel := strings.ReplaceAll(parentID, "-", "_")
	if parentPath != "" && containsPathLabel(parentPath, userLabel) {
		return ErrInvalidRelation
	}
	path := parentLabel + "." + userLabel
	if parentPath != "" {
		path = parentPath + "." + userLabel
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,$3::ltree)`, userID, parentID, path)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func containsPathLabel(path, label string) bool {
	for _, item := range strings.Split(path, ".") {
		if item == label {
			return true
		}
	}
	return false
}

func (service *Service) GetRelation(ctx context.Context, userID string) (Relation, error) {
	var relation Relation
	err := service.pool.QueryRow(ctx, `SELECT user_id::text, COALESCE(parent_user_id::text, '') FROM agent_relations WHERE user_id=$1`, userID).Scan(&relation.UserID, &relation.ParentUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Relation{UserID: userID}, nil
	}
	return relation, err
}

func (service *Service) SetCommissionRate(ctx context.Context, agentID string, rateBasisPoints int, operatorID string) error {
	if agentID == "" || operatorID == "" || rateBasisPoints < 0 || rateBasisPoints > 10000 {
		return ErrInvalidCommissionRate
	}
	_, err := service.pool.Exec(ctx, `INSERT INTO agent_commission_rates(agent_user_id,rate_basis_points,updated_by) VALUES($1,$2,$3) ON CONFLICT(agent_user_id) DO UPDATE SET rate_basis_points=EXCLUDED.rate_basis_points,updated_by=EXCLUDED.updated_by,updated_at=now()`, agentID, rateBasisPoints, operatorID)
	return err
}
