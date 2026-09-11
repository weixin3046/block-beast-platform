package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidRelation = errors.New("invalid agent relation")
var ErrRelationExists = errors.New("agent relation already exists")
var ErrAdminBindForbidden = errors.New("仅管理员或运营人员可以绑定上级")
var ErrRelationUserNotFound = errors.New("用户或上级不存在")
var ErrVirtualRelation = errors.New("虚拟用户不能建立代理关系")
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

type AdminRelation struct {
	UserID       int64 `json:"user_id"`
	ParentUserID int64 `json:"parent_user_id"`
}

type Commission struct {
	LoginName   string `json:"login_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	ID          string `json:"id"`
	BetID       string `json:"bet_id"`
	AgentID     string `json:"agent_id"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
}

// AdminCommission adds source player details without changing the personal API.
type AdminCommission struct {
	Commission
	SourceUserID      int64      `json:"source_user_id"`
	SourceLoginName   string     `json:"source_login_name"`
	SourceDisplayName string     `json:"source_display_name"`
	CreatedAt         *time.Time `json:"created_at"`
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

func (service *Service) ListAllCommissions(ctx context.Context, status, currency string, limit int, from, to time.Time) ([]AdminCommission, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT ce.id::text,ce.source_bet_id::text,ce.beneficiary_user_id::text,ce.currency,ce.amount_minor,ce.status,COALESCE(u.login_name,''),u.display_name,src.public_id,COALESCE(src.login_name,''),src.display_name,ce.created_at FROM commission_entries ce JOIN users u ON u.id=ce.beneficiary_user_id JOIN bets b ON b.id=ce.source_bet_id JOIN users src ON src.id=b.user_id WHERE TRUE`
	args := []any{limit}
	if status != "" {
		query += ` AND ce.status=$2`
		args = append(args, status)
	}
	if currency != "" {
		args = append(args, currency)
		query += fmt.Sprintf(" AND ce.currency = $%d", len(args))
	}
	if !from.IsZero() {
		args = append(args, from)
		query += fmt.Sprintf(" AND ce.created_at >= $%d", len(args))
	}
	if !to.IsZero() {
		args = append(args, to)
		query += fmt.Sprintf(" AND ce.created_at < $%d", len(args))
	}
	query += ` ORDER BY ce.created_at DESC NULLS LAST, ce.id DESC LIMIT $1`
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AdminCommission, 0)
	for rows.Next() {
		var item AdminCommission
		if err := rows.Scan(&item.ID, &item.BetID, &item.AgentID, &item.Currency, &item.AmountMinor, &item.Status, &item.LoginName, &item.DisplayName, &item.SourceUserID, &item.SourceLoginName, &item.SourceDisplayName, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) TeamSummary(ctx context.Context, agentID string) (TeamSummary, error) {
	var summary TeamSummary
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM agent_relations ar JOIN users u ON u.id=ar.user_id WHERE ar.parent_user_id=$1 AND NOT u.is_virtual`, agentID).Scan(&summary.DirectPlayers); err != nil {
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
		JOIN users ON users.id=bets.user_id
		WHERE agent_relations.parent_user_id=$1 AND NOT users.is_virtual AND NOT bets.is_simulated
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('agent-relation-graph',0))`); err != nil {
		return err
	}
	if err = ensureRealRelationUsers(ctx, tx, userID, parentID); err != nil {
		if errors.Is(err, ErrRelationUserNotFound) {
			return ErrInvalidRelation
		}
		return err
	}
	if err = bindTx(ctx, tx, userID, parentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminBind resolves public IDs inside the serialized relation transaction,
// rechecks the actor role and appends an audit record atomically.
func (service *Service) AdminBind(ctx context.Context, actorID string, userPublicID, parentPublicID int64) (AdminRelation, error) {
	if actorID == "" || userPublicID < 10001 || parentPublicID < 10001 || userPublicID == parentPublicID {
		return AdminRelation{}, ErrInvalidRelation
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return AdminRelation{}, err
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u
		JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id
		WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actorID).Scan(&allowed); err != nil {
		return AdminRelation{}, err
	}
	if !allowed {
		return AdminRelation{}, ErrAdminBindForbidden
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('agent-relation-graph',0))`); err != nil {
		return AdminRelation{}, err
	}
	var userID, parentID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1`, userPublicID).Scan(&userID); errors.Is(err, pgx.ErrNoRows) {
		return AdminRelation{}, ErrRelationUserNotFound
	} else if err != nil {
		return AdminRelation{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1`, parentPublicID).Scan(&parentID); errors.Is(err, pgx.ErrNoRows) {
		return AdminRelation{}, ErrRelationUserNotFound
	} else if err != nil {
		return AdminRelation{}, err
	}
	if err = ensureRealRelationUsers(ctx, tx, userID, parentID); err != nil {
		return AdminRelation{}, err
	}
	if err = bindTx(ctx, tx, userID, parentID); err != nil {
		return AdminRelation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload)
		VALUES($1,$2,'admin.agent.bind','user',$3,jsonb_build_object('user_id',$4::bigint,'parent_user_id',$5::bigint))`,
		uuid.NewString(), actorID, userID, userPublicID, parentPublicID); err != nil {
		return AdminRelation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AdminRelation{}, err
	}
	return AdminRelation{UserID: userPublicID, ParentUserID: parentPublicID}, nil
}

func ensureRealRelationUsers(ctx context.Context, tx pgx.Tx, userID, parentID string) error {
	rows, err := tx.Query(ctx, `SELECT id::text,is_virtual FROM users WHERE id=ANY($1::uuid[]) ORDER BY id FOR SHARE`, []string{userID, parentID})
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		var virtual bool
		if err = rows.Scan(&id, &virtual); err != nil {
			return err
		}
		count++
		if virtual {
			return ErrVirtualRelation
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != 2 {
		return ErrRelationUserNotFound
	}
	return nil
}

func bindTx(ctx context.Context, tx pgx.Tx, userID, parentID string) error {
	var parentPath string
	err := tx.QueryRow(ctx, `SELECT COALESCE(path::text,'') FROM agent_relations WHERE user_id=$1`, parentID).Scan(&parentPath)
	if errors.Is(err, pgx.ErrNoRows) {
		parentPath = ""
	} else if err != nil {
		return err
	}
	var existing *string
	err = tx.QueryRow(ctx, `SELECT parent_user_id::text FROM agent_relations WHERE user_id=$1`, userID).Scan(&existing)
	if err == nil && existing != nil {
		return ErrRelationExists
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var cycle bool
	if err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors(id) AS (SELECT $1::uuid UNION SELECT ar.parent_user_id FROM agent_relations ar JOIN ancestors a ON ar.user_id=a.id WHERE ar.parent_user_id IS NOT NULL) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$2)`, parentID, userID).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return ErrInvalidRelation
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
	prefix := parentLabel + "."
	if parentPath != "" {
		prefix = parentPath + "."
	}
	// A root user may already have descendants whose paths start at that user.
	// Prefix those paths before inserting the root's own newly bound relation.
	if _, err = tx.Exec(ctx, `UPDATE agent_relations SET path=($2||path::text)::ltree WHERE path <@ $1::ltree`, userLabel, prefix); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,$3::ltree) ON CONFLICT(user_id) DO UPDATE SET parent_user_id=EXCLUDED.parent_user_id,path=EXCLUDED.path WHERE agent_relations.parent_user_id IS NULL`, userID, parentID, path)
	if err != nil {
		return err
	}
	return nil
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
