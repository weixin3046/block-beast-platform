package operations

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidRoles = errors.New("roles must contain one or more of player, operator, or admin")
var ErrCannotRemoveOwnAdmin = errors.New("administrator cannot remove own admin role")
var ErrCannotRemoveLastAdmin = errors.New("cannot remove the platform's last admin role")

type Role struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type RoleAssignment struct {
	UserID int64    `json:"user_id"`
	Roles  []string `json:"roles"`
}

func (service *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := service.pool.Query(ctx, `SELECT code,description FROM roles ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Role, 0)
	for rows.Next() {
		var item Role
		if err := rows.Scan(&item.Code, &item.Description); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) SetUserRoles(ctx context.Context, actorUserID, userID string, requested []string) (RoleAssignment, error) {
	roles, err := normalizeRoles(requested)
	if err != nil {
		return RoleAssignment{}, err
	}
	publicID, parseErr := strconv.ParseInt(userID, 10, 64)
	if parseErr != nil || publicID < 100000 {
		return RoleAssignment{}, ErrUserNotFound
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return RoleAssignment{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-role-management', 0))`); err != nil {
		return RoleAssignment{}, err
	}
	var targetUserID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1`, publicID).Scan(&targetUserID); errors.Is(err, pgx.ErrNoRows) {
		return RoleAssignment{}, ErrUserNotFound
	} else if err != nil {
		return RoleAssignment{}, err
	}
	if actorUserID == targetUserID && !containsRole(roles, identity.RoleAdmin) {
		return RoleAssignment{}, ErrCannotRemoveOwnAdmin
	}
	userID = targetUserID
	var currentlyAdmin bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
			WHERE ur.user_id=$1 AND r.code='admin'
		)`, userID).Scan(&currentlyAdmin); err != nil {
		return RoleAssignment{}, err
	}
	if currentlyAdmin && !containsRole(roles, identity.RoleAdmin) {
		var otherAdmins int
		if err := tx.QueryRow(ctx, `
			SELECT count(DISTINCT ur.user_id)
			FROM user_roles ur JOIN roles r ON r.id=ur.role_id
			WHERE r.code='admin' AND ur.user_id<>$1`, userID).Scan(&otherAdmins); err != nil {
			return RoleAssignment{}, err
		}
		if otherAdmins == 0 {
			return RoleAssignment{}, ErrCannotRemoveLastAdmin
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, userID); err != nil {
		return RoleAssignment{}, err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id)
		SELECT $1,id FROM roles WHERE code=ANY($2::text[])`, userID, roles)
	if err != nil {
		return RoleAssignment{}, err
	}
	if command.RowsAffected() != int64(len(roles)) {
		return RoleAssignment{}, ErrInvalidRoles
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RoleAssignment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RoleAssignment{}, err
	}
	return RoleAssignment{UserID: publicID, Roles: roles}, nil
}

func normalizeRoles(requested []string) ([]string, error) {
	unique := make(map[string]struct{})
	for _, role := range requested {
		role = strings.ToLower(strings.TrimSpace(role))
		switch role {
		case identity.RolePlayer, identity.RoleOperator, identity.RoleAdmin:
			unique[role] = struct{}{}
		default:
			return nil, ErrInvalidRoles
		}
	}
	if len(unique) == 0 {
		return nil, ErrInvalidRoles
	}
	roles := make([]string, 0, len(unique))
	for role := range unique {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles, nil
}

func containsRole(roles []string, expected string) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}
