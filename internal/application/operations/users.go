package operations

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidUserStatus = errors.New("user status must be active, disabled, or bet_banned")
var ErrUserNotFound = errors.New("user not found")
var ErrCannotDisableOwnAdmin = errors.New("administrator cannot disable own account")
var ErrCannotDisableLastAdmin = errors.New("cannot disable the platform's last active admin")
var ErrInvalidAgentLevel = errors.New("agent level must be between 1 and 6")
var ErrInvalidProfile = errors.New("display_name is required and profile fields are too long")

type User struct {
	ID             int64     `json:"id"`
	LoginName      string    `json:"login_name"`
	DisplayName    string    `json:"display_name"`
	Status         string    `json:"status"`
	InvitationCode int64     `json:"invitation_code"`
	AgentLevel     int       `json:"agent_level"`
	Roles          []string  `json:"roles,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	AvatarURL      string    `json:"avatar_url"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (service *Service) ListUsers(ctx context.Context, status, query string, limit int) ([]User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT public_id,COALESCE(login_name,''),display_name,status,created_at
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

func (service *Service) CurrentUser(ctx context.Context, userID string) (User, error) {
	var user User
	err := service.pool.QueryRow(ctx, `
		SELECT u.public_id, COALESCE(u.login_name,''), u.display_name, u.status, u.created_at,
			CASE WHEN u.avatar_url LIKE 'uploads/%' THEN '/v1/avatars/' || u.public_id::text || '?v=' || regexp_replace(u.avatar_url, '^.*/', '') ELSE COALESCE(u.avatar_url,'') END,
			u.invitation_code, COALESCE(u.agent_level,0), COALESCE(array_agg(r.code) FILTER (WHERE r.code IS NOT NULL), '{}')
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id=u.id
		LEFT JOIN roles r ON r.id=ur.role_id
		WHERE u.id=$1
	GROUP BY u.id`, userID).Scan(&user.ID, &user.LoginName, &user.DisplayName, &user.Status, &user.CreatedAt, &user.AvatarURL, &user.InvitationCode, &user.AgentLevel, &user.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (service *Service) UpdateCurrentProfile(ctx context.Context, userID, displayName, avatarURL string) (User, error) {
	displayName = strings.TrimSpace(displayName)
	avatarURL = strings.TrimSpace(avatarURL)
	if displayName == "" || len(displayName) > 100 || len(avatarURL) > 2048 {
		return User{}, ErrInvalidProfile
	}
	_, err := service.pool.Exec(ctx, `UPDATE users SET display_name=$2,avatar_url=$3,updated_at=now() WHERE id=$1`, userID, displayName, avatarURL)
	if err != nil {
		return User{}, err
	}
	return service.CurrentUser(ctx, userID)
}

func (service *Service) SetAgentLevel(ctx context.Context, userID string, level int) error {
	if level < 1 || level > 6 {
		return ErrInvalidAgentLevel
	}
	publicID, err := strconv.ParseInt(userID, 10, 64)
	if err != nil || publicID < 100000 {
		return ErrUserNotFound
	}
	var internalID string
	if err := service.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1`, publicID).Scan(&internalID); errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return err
	}
	result, err := service.pool.Exec(ctx, `UPDATE users SET agent_level=$2,updated_at=now() WHERE id=$1`, internalID, level)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	_, err = service.pool.Exec(ctx, `INSERT INTO agent_commission_rates(agent_user_id,rate_basis_points) VALUES($1,0) ON CONFLICT(agent_user_id) DO UPDATE SET rate_basis_points=0,updated_at=now()`, internalID)
	return err
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
	publicID, parseErr := strconv.ParseInt(userID, 10, 64)
	if parseErr != nil || publicID < 100000 {
		return ErrUserNotFound
	}
	var targetUserID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1`, publicID).Scan(&targetUserID); errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return err
	}
	userID = targetUserID
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
