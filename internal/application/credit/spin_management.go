package credit

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrSpinManagementForbidden = errors.New("无权管理转盘")

// Configuration lock is shared with drawing and editing, so deletion cannot
// race a charge against a configuration that has already been removed.
func (s *Service) ChangeSpinState(ctx context.Context, actor, id string, enabled *bool) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrSpinConfigNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code='admin')`, actor).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrSpinManagementForbidden
	}
	var deleted bool
	if err = tx.QueryRow(ctx, "SELECT deleted FROM spin_configs WHERE id=$1 FOR UPDATE", id).Scan(&deleted); errors.Is(err, pgx.ErrNoRows) {
		return ErrSpinConfigNotFound
	} else if err != nil {
		return err
	}
	if deleted {
		if enabled == nil {
			return tx.Commit(ctx)
		}
		return ErrSpinConfigNotFound
	}
	action := "spin.delete"
	if enabled != nil {
		if *enabled {
			v, e := findSpinConfig(ctx, tx, id, false)
			if e != nil {
				return e
			}
			if _, ok := choosePrize(v.Prizes); !ok {
				return ErrInvalidSpinConfig
			}
		}
		_, err = tx.Exec(ctx, "UPDATE spin_configs SET enabled=$2,updated_at=now() WHERE id=$1", id, *enabled)
		action = "spin.enabled"
	} else {
		_, err = tx.Exec(ctx, "UPDATE spin_configs SET deleted=true,enabled=false,updated_at=now() WHERE id=$1", id)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,$3,'spin',$4,'{}')`, uuid.NewString(), actor, action, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type SpinRecordQuery struct {
	SpinID, Currency string
	UserID           int64
	Limit, Offset    int
}

// No balances, credentials or request keys are exposed in a public win record.
type SpinRecord struct {
	ID          string    `json:"id"`
	SpinID      string    `json:"spin_id"`
	UserID      int64     `json:"user_id"`
	DisplayName string    `json:"display_name"`
	IsVirtual   bool      `json:"is_virtual"`
	PrizeID     string    `json:"prize_id"`
	PrizeLabel  string    `json:"prize_label"`
	Currency    string    `json:"currency"`
	AmountMinor int64     `json:"amount_minor"`
	CreatedAt   time.Time `json:"created_at"`
}
type SpinRecordPage struct {
	Items []SpinRecord `json:"items"`
	Total int64        `json:"total"`
}

func (s *Service) ListSpinRecords(ctx context.Context, q SpinRecordQuery) (SpinRecordPage, error) {
	out := SpinRecordPage{Items: []SpinRecord{}}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.SpinID != "" {
		if _, e := uuid.Parse(q.SpinID); e != nil {
			return out, ErrInvalidSpinConfig
		}
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	filter := ` FROM lucky_spin_records r JOIN users u ON u.id=r.user_id WHERE ($1='' OR r.spin_id::text=$1) AND ($2='' OR r.reward_currency=$2) AND ($3::bigint=0 OR u.public_id=$3)`
	if e = tx.QueryRow(ctx, "SELECT count(*)"+filter, q.SpinID, q.Currency, q.UserID).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, `SELECT r.id::text,r.spin_id::text,u.public_id,u.display_name,u.is_virtual,r.prize_id,r.prize_label,r.reward_currency,r.reward_minor,r.created_at`+filter+" ORDER BY r.created_at DESC,r.id DESC LIMIT $4 OFFSET $5", q.SpinID, q.Currency, q.UserID, q.Limit, q.Offset)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v SpinRecord
		if e = rows.Scan(&v.ID, &v.SpinID, &v.UserID, &v.DisplayName, &v.IsVirtual, &v.PrizeID, &v.PrizeLabel, &v.Currency, &v.AmountMinor, &v.CreatedAt); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
