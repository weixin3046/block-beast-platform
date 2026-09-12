package adminsecurity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid       = errors.New("后台密码参数无效")
	ErrForbidden     = errors.New("无权管理或使用后台操作密码")
	ErrNotSet        = errors.New("该后台操作密码尚未设置，请联系管理员")
	ErrIncorrect     = errors.New("后台操作密码不正确")
	ErrLoginPassword = errors.New("当前管理员登录密码不正确")
	ErrLocked        = errors.New("密码验证失败次数过多，请15分钟后重试")
)

type Status struct {
	FirstSet  bool `json:"first_set"`
	SecondSet bool `json:"second_set"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }
func validLevel(level string) bool           { return level == "first" || level == "second" }
func validPassword(password string) bool {
	return strings.TrimSpace(password) != ""
}

func authorized(ctx context.Context, tx pgx.Tx, actor string, adminOnly bool) error {
	if actor == "" {
		return ErrForbidden
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND (r.code='admin' OR (NOT $2 AND r.code='operator')))`, actor, adminOnly).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
func (s *Service) Status(ctx context.Context, actor string) (Status, error) {
	var out Status
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = authorized(ctx, tx, actor, false); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_security_passwords WHERE level='first' AND password_hash<>''),EXISTS(SELECT 1 FROM admin_security_passwords WHERE level='second' AND password_hash<>'')`).Scan(&out.FirstSet, &out.SecondSet)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// guard 锁定单个账号的尝试记录，防止并发请求绕过失败次数限制。
func guard(ctx context.Context, tx pgx.Tx, actor, scope string) (int, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO admin_security_attempts(actor_id,scope) VALUES($1,$2) ON CONFLICT DO NOTHING`, actor, scope); err != nil {
		return 0, err
	}
	var failures int
	var started time.Time
	var blocked *time.Time
	err := tx.QueryRow(ctx, `SELECT failures,window_started_at,blocked_until FROM admin_security_attempts WHERE actor_id=$1 AND scope=$2 FOR UPDATE`, actor, scope).Scan(&failures, &started, &blocked)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	if blocked != nil && blocked.After(now) {
		return 0, ErrLocked
	}
	if now.Sub(started) >= 15*time.Minute {
		failures = 0
		if _, err = tx.Exec(ctx, `UPDATE admin_security_attempts SET failures=0,window_started_at=now(),blocked_until=NULL WHERE actor_id=$1 AND scope=$2`, actor, scope); err != nil {
			return 0, err
		}
	}
	return failures, nil
}
func failed(ctx context.Context, tx pgx.Tx, actor, scope string, count int, reason error) error {
	count++
	if _, err := tx.Exec(ctx, `UPDATE admin_security_attempts SET failures=$3,blocked_until=CASE WHEN $3>=5 THEN now()+interval '15 minutes' ELSE NULL END WHERE actor_id=$1 AND scope=$2`, actor, scope, count); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if count >= 5 {
		return ErrLocked
	}
	return reason
}
func succeeded(ctx context.Context, tx pgx.Tx, actor, scope string) error {
	_, err := tx.Exec(ctx, `UPDATE admin_security_attempts SET failures=0,window_started_at=now(),blocked_until=NULL WHERE actor_id=$1 AND scope=$2`, actor, scope)
	return err
}

func (s *Service) Verify(ctx context.Context, actor, level, password string) error {
	if !validLevel(level) || !validPassword(password) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = authorized(ctx, tx, actor, false); err != nil {
		return err
	}
	count, err := guard(ctx, tx, actor, level)
	if err != nil {
		return err
	}
	var hash string
	if err = tx.QueryRow(ctx, `SELECT password_hash FROM admin_security_passwords WHERE level=$1 FOR SHARE`, level).Scan(&hash); err != nil {
		return err
	}
	if hash == "" {
		return ErrNotSet
	}
	if !identity.VerifyPassword(hash, password) {
		return failed(ctx, tx, actor, level, count, ErrIncorrect)
	}
	if err = succeeded(ctx, tx, actor, level); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Set 仅 admin 可设置/重置全局密码；验证当前登录密码，不需要旧操作密码。
// 密码哈希、修改审计原子提交，不在通用配置表、响应或审计中保存密码。
func (s *Service) Set(ctx context.Context, actor, level, loginPassword, password string) error {
	if !validLevel(level) || !validPassword(password) || !validPassword(loginPassword) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = authorized(ctx, tx, actor, true); err != nil {
		return err
	}
	count, err := guard(ctx, tx, actor, "manage")
	if err != nil {
		return err
	}
	var hash string
	err = tx.QueryRow(ctx, `SELECT password_hash FROM auth_identities WHERE user_id=$1 AND provider='password' LIMIT 1`, actor).Scan(&hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err != nil || !identity.VerifyPassword(hash, loginPassword) {
		return failed(ctx, tx, actor, "manage", count, ErrLoginPassword)
	}
	hash, err = identity.HashPassword(password)
	if err != nil {
		return err
	}
	var version int64
	if err = tx.QueryRow(ctx, `UPDATE admin_security_passwords SET password_hash=$2,version=version+1,updated_by=$3,updated_at=now() WHERE level=$1 RETURNING version`, level, hash, actor).Scan(&version); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"level": level, "version": version})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,'admin.security_password.set','admin_security_password',$3,$4)`, uuid.NewString(), actor, level, payload); err != nil {
		return err
	}
	if err = succeeded(ctx, tx, actor, "manage"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
