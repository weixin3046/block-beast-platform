package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrIdentityNotFound = errors.New("identity not found")
var ErrLoginNameTaken = errors.New("login name is already taken")

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

// RegisterPasswordUser 在单个事务中创建用户、密码凭证、指定角色和一组货币的
// 零余额钱包。登录名冲突时返回 ErrLoginNameTaken。
func (repository *PostgresRepository) RegisterPasswordUser(ctx context.Context, loginName string, displayName string, passwordHash string, roleCode string, currencies []string) (string, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO roles (id, code, description)
		VALUES ($1, $2, $2)
		ON CONFLICT (code) DO NOTHING`, uuid.NewString(), roleCode); err != nil {
		return "", err
	}
	var roleID string
	if err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE code = $1`, roleCode).Scan(&roleID); err != nil {
		return "", err
	}

	userID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO users (id, login_name, display_name) VALUES ($1, $2, $3)`, userID, loginName, displayName); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrLoginNameTaken
		}
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_identities (id, user_id, provider, subject, password_hash)
		VALUES ($1, $2, 'password', $3, $4)`, uuid.NewString(), userID, loginName, passwordHash); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrLoginNameTaken
		}
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID); err != nil {
		return "", err
	}
	for _, currency := range currencies {
		if _, err := tx.Exec(ctx, `INSERT INTO wallets (id, user_id, currency) VALUES ($1, $2, $3)`, uuid.NewString(), userID, currency); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
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
