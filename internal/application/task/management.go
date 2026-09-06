package task

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrTaskForbidden = errors.New("无权管理任务")

func (s *Service) ChangeTaskState(ctx context.Context, actor, id string, enabled *bool) error {
	if _, e := uuid.Parse(id); e != nil {
		return ErrTaskConfigNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code='admin')`, actor).Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return ErrTaskForbidden
	}
	var deleted bool
	if e = tx.QueryRow(ctx, "SELECT deleted FROM bet_task_configs WHERE id=$1 FOR UPDATE", id).Scan(&deleted); errors.Is(e, pgx.ErrNoRows) {
		return ErrTaskConfigNotFound
	} else if e != nil {
		return e
	}
	if deleted {
		if enabled == nil {
			return tx.Commit(ctx)
		}
		return ErrTaskConfigNotFound
	}
	action := "task.delete"
	if enabled == nil {
		_, e = tx.Exec(ctx, "UPDATE bet_task_configs SET deleted=true,enabled=false WHERE id=$1", id)
	} else {
		_, e = tx.Exec(ctx, "UPDATE bet_task_configs SET enabled=$2 WHERE id=$1", id, *enabled)
		action = "task.enabled"
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,$3,'task',$4,'{}')`, uuid.NewString(), actor, action, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type ProgressQuery struct {
	TaskID, Date  string
	UserID        int64
	Limit, Offset int
}
type ProgressRecord struct {
	TaskID               string    `json:"task_id"`
	UserID               int64     `json:"user_id"`
	DisplayName          string    `json:"display_name"`
	Title                string    `json:"title"`
	PeriodType           string    `json:"period_type"`
	PeriodDate           string    `json:"period_date"`
	AccumulationCurrency string    `json:"accumulation_currency"`
	ProgressMinor        int64     `json:"progress_minor"`
	ThresholdMinor       int64     `json:"threshold_minor"`
	CompleteCount        int       `json:"complete_count"`
	MaxCompleteCount     int       `json:"max_complete_count"`
	UpdatedAt            time.Time `json:"updated_at"`
}
type ProgressPage struct {
	Items []ProgressRecord `json:"items"`
	Total int64            `json:"total"`
}

func (s *Service) ListProgress(ctx context.Context, q ProgressQuery) (ProgressPage, error) {
	out := ProgressPage{Items: []ProgressRecord{}}
	if q.TaskID != "" {
		if _, e := uuid.Parse(q.TaskID); e != nil {
			return out, ErrInvalidTask
		}
	}
	if q.Date != "" {
		if _, e := time.Parse("2006-01-02", q.Date); e != nil {
			return out, ErrInvalidTask
		}
	}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	filter := ` FROM task_progress p JOIN bet_task_configs c ON c.id=p.config_id JOIN users u ON u.id=p.user_id WHERE NOT c.deleted AND ($1='' OR c.id::text=$1) AND ($2::bigint=0 OR u.public_id=$2) AND ($3='' OR p.period_date::text=$3)`
	if e = tx.QueryRow(ctx, "SELECT count(*)"+filter, q.TaskID, q.UserID, q.Date).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, `SELECT c.id::text,u.public_id,u.display_name,c.title,c.period_type,p.period_date::text,c.accumulation_currency,p.progress_minor,c.threshold_minor,p.complete_count,c.max_complete_count,p.updated_at`+filter+" ORDER BY p.updated_at DESC,c.id,u.public_id,p.period_date LIMIT $4 OFFSET $5", q.TaskID, q.UserID, q.Date, q.Limit, q.Offset)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v ProgressRecord
		if e = rows.Scan(&v.TaskID, &v.UserID, &v.DisplayName, &v.Title, &v.PeriodType, &v.PeriodDate, &v.AccumulationCurrency, &v.ProgressMinor, &v.ThresholdMinor, &v.CompleteCount, &v.MaxCompleteCount, &v.UpdatedAt); e != nil {
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
