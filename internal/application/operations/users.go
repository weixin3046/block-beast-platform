package operations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidUserStatus = errors.New("user status must be active, disabled, or bet_banned")
var ErrUserNotFound = errors.New("user not found")

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

func (service *Service) SetUserStatus(ctx context.Context, userID, status string) error {
	if status != "active" && status != "disabled" && status != "bet_banned" {
		return ErrInvalidUserStatus
	}
	command, err := service.pool.Exec(ctx, `UPDATE users SET status=$2,updated_at=now() WHERE id=$1`, userID, status)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}
