package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrIdentityNotFound = errors.New("identity not found")
var ErrLoginNameTaken = errors.New("login name is already taken")
var ErrInvitationCodeNotFound = errors.New("invitation code is invalid")
var ErrAdminAlreadyExists = errors.New("an administrator already exists")

// PasswordCredentials 是 password 提供方下的登录凭证与账号状态。
type PasswordCredentials struct {
	UserID       string
	Status       string
	PasswordHash string
}

func (repository *PostgresRepository) CreateSession(ctx context.Context, userID string, tokenHash string, audience SessionAudience, expiresAt time.Time) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, audience, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, uuid.NewString(), userID, tokenHash, audience, expiresAt)
	return err
}

func (repository *PostgresRepository) RotateSession(ctx context.Context, oldTokenHash string, newTokenHash string, audience SessionAudience, expiresAt time.Time) (string, error) {
	var userID string
	err := repository.pool.QueryRow(ctx, `
		UPDATE sessions
		SET token_hash = $2, expires_at = $3
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		  AND audience = $4
		  AND EXISTS (
			  SELECT 1 FROM users
			  WHERE users.id = sessions.user_id AND users.status = 'active'
		  )
		RETURNING user_id`, oldTokenHash, newTokenHash, expiresAt, audience).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrIdentityNotFound
	}
	return userID, err
}

func (repository *PostgresRepository) RevokeSession(ctx context.Context, tokenHash string) error {
	tag, err := repository.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIdentityNotFound
	}
	return nil
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

// PublicUserID 返回面向客户端展示的连续数字用户 ID；UUID 仅用于内部关联。
func (repository *PostgresRepository) PublicUserID(ctx context.Context, userID string) (int64, error) {
	var publicID int64
	err := repository.pool.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, userID).Scan(&publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrIdentityNotFound
	}
	return publicID, err
}

func (repository *PostgresRepository) PasswordHashByUserID(ctx context.Context, userID string) (string, error) {
	var passwordHash string
	err := repository.pool.QueryRow(ctx, `SELECT password_hash FROM auth_identities WHERE user_id=$1 AND provider='password'`, userID).Scan(&passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrIdentityNotFound
	}
	return passwordHash, err
}

// UpdatePasswordHash 使所有既有刷新令牌立即失效。
func (repository *PostgresRepository) UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE auth_identities SET password_hash=$2 WHERE user_id=$1 AND provider='password'`, userID, passwordHash)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrIdentityNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (repository *PostgresRepository) SecondaryPasswordHashByUserID(ctx context.Context, userID string) (string, error) {
	var passwordHash *string
	err := repository.pool.QueryRow(ctx, `SELECT secondary_password_hash FROM users WHERE id=$1`, userID).Scan(&passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrIdentityNotFound
	}
	if err != nil {
		return "", err
	}
	if passwordHash == nil || *passwordHash == "" {
		return "", ErrIdentityNotFound
	}
	return *passwordHash, nil
}

func (repository *PostgresRepository) UpdateSecondaryPasswordHash(ctx context.Context, userID, passwordHash string) error {
	result, err := repository.pool.Exec(ctx, `UPDATE users SET secondary_password_hash=$2,updated_at=now() WHERE id=$1`, userID, passwordHash)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrIdentityNotFound
	}
	return nil
}

// RegisterPasswordUser 在单个事务中创建用户、密码凭证、指定角色和一组货币的
// 零余额钱包。登录名冲突时返回 ErrLoginNameTaken。
func (repository *PostgresRepository) RegisterPasswordUser(ctx context.Context, loginName string, displayName string, passwordHash string, roleCode string, currencies []string, invitationCode int64) (string, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if invitationCode < 101 {
		return "", ErrInvitationCodeNotFound
	}
	var parentUserID, parentPath string
	err = tx.QueryRow(ctx, `SELECT id::text, COALESCE((SELECT path::text FROM agent_relations WHERE user_id=users.id),'') FROM users WHERE invitation_code=$1 AND agent_level IS NOT NULL`, invitationCode).Scan(&parentUserID, &parentPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvitationCodeNotFound
	}
	if err != nil {
		return "", err
	}

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
	userLabel := strings.ReplaceAll(userID, "-", "_")
	parentLabel := strings.ReplaceAll(parentUserID, "-", "_")
	path := parentLabel + "." + userLabel
	if parentPath != "" {
		path = parentPath + "." + userLabel
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,$3::ltree)`, userID, parentUserID, path); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

// CreateFirstAdmin 创建平台的首个管理员。事务级 advisory lock 保证并发执行时
// 最多只有一个命令可以成功；一旦存在任何 admin 角色账号，此入口永久拒绝执行。
func (repository *PostgresRepository) CreateFirstAdmin(ctx context.Context, loginName string, displayName string, passwordHash string) (string, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-role-management', 0))`); err != nil {
		return "", err
	}
	var adminExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM user_roles
			JOIN roles ON roles.id = user_roles.role_id
			WHERE roles.code = $1
		)`, RoleAdmin).Scan(&adminExists); err != nil {
		return "", err
	}
	if adminExists {
		return "", ErrAdminAlreadyExists
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO roles (id, code, description)
		VALUES ($1, $2, 'platform administrator')
		ON CONFLICT (code) DO NOTHING`, uuid.NewString(), RoleAdmin); err != nil {
		return "", err
	}
	var roleID string
	if err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE code = $1`, RoleAdmin).Scan(&roleID); err != nil {
		return "", err
	}

	userID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, login_name, display_name)
		VALUES ($1, $2, $3)`, userID, loginName, displayName); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrLoginNameTaken
		}
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_identities (id, user_id, provider, subject, password_hash)
		VALUES ($1, $2, 'password', $3, $4)`, uuid.NewString(), userID, loginName, passwordHash); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		VALUES ($1, $2)`, userID, roleID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_user_id, action, target_type, target_id, payload)
		VALUES ($1::uuid, $2::uuid, 'auth.bootstrap_admin', 'user', $3::text, jsonb_build_object('login_name', $4::text))`,
		uuid.NewString(), userID, userID, loginName); err != nil {
		return "", err
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
