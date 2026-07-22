package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrIdentityNotFound = errors.New("identity not found")

// PasswordCredentials 是 password 提供方下的登录凭证与账号状态。
type PasswordCredentials struct {
	UserID       string
	Status       string
	PasswordHash string
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// FindPasswordCredentials 按登录名查询密码凭证。
func (repository *PostgresRepository) FindPasswordCredentials(ctx context.Context, loginName string) (PasswordCredentials, error) {
	var credentials PasswordCredentials
	err := repository.pool.QueryRow(ctx, `
		SELECT users.id, users.status, auth_identities.password_hash
		FROM auth_identities
		JOIN users ON users.id = auth_identities.user_id
		WHERE auth_identities.provider = 'password' AND auth_identities.subject = $1`, loginName).
		Scan(&credentials.UserID, &credentials.Status, &credentials.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return PasswordCredentials{}, ErrIdentityNotFound
	}
	if err != nil {
		return PasswordCredentials{}, err
	}
	return credentials, nil
}

// UserRoles 返回用户拥有的角色代码列表。
func (repository *PostgresRepository) UserRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT roles.code
		FROM user_roles
		JOIN roles ON roles.id = user_roles.role_id
		WHERE user_roles.user_id = $1
		ORDER BY roles.code`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	codes := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return codes, nil
}
