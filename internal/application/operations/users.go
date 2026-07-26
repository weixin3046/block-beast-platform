package operations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidUserStatus = errors.New("user status must be active, disabled, or bet_banned")
var ErrUserNotFound = errors.New("user not found")
var ErrCannotDisableOwnAdmin = errors.New("administrator cannot disable own account")
var ErrCannotDisableLastAdmin = errors.New("cannot disable the platform's last active admin")

type User struct {
	ID          string    `json:"id"`
	LoginName   string    `json:"login_name"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (service *Service) ListUsers(ctx context.Context, status, query string, limit int) ([]User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT id::text,COALESCE(login_name,''),display_name,status,created_at
		FROM users
		WHERE ($1='' OR status=$1)
		  AND ($2='' OR login_name ILIKE '%'||$2||'%' OR display_name ILIKE '%'||$2||'%')
		ORDER BY created_at DESC LIMIT $3`, status, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]User, 0)
	for rows.Next() {
		var item User
		if err := rows.Scan(&item.ID, &item.LoginName, &item.DisplayName, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) SetUserStatus(ctx context.Context, actorUserID, userID, status string) error {
	if status != "active" && status != "disabled" && status != "bet_banned" {
		return ErrInvalidUserStatus
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-role-management', 0))`); err != nil {
		return err
	}
	var currentStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if status != "active" {
		var targetIsAdmin bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
				WHERE ur.user_id=$1 AND r.code='admin'
			)`, userID).Scan(&targetIsAdmin); err != nil {
			return err
		}
		if targetIsAdmin && actorUserID == userID {
			return ErrCannotDisableOwnAdmin
		}
		if targetIsAdmin && currentStatus == "active" {
			var otherActiveAdmins int
			if err := tx.QueryRow(ctx, `
				SELECT count(DISTINCT ur.user_id)
				FROM user_roles ur
				JOIN roles r ON r.id=ur.role_id
				JOIN users u ON u.id=ur.user_id
				WHERE r.code='admin' AND u.status='active' AND ur.user_id<>$1`, userID).Scan(&otherActiveAdmins); err != nil {
				return err
			}
			if otherActiveAdmins == 0 {
				return ErrCannotDisableLastAdmin
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status=$2,updated_at=now() WHERE id=$1`, userID, status); err != nil {
		return err
	}
	if status != "active" {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
