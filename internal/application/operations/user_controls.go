package operations

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrUserControlForbidden = errors.New("无权修改该用户的安全信息")
var ErrUserControlInvalid = errors.New("用户安全参数无效")
var ErrResetPasswordEmpty = errors.New("新密码不能为空或全为空白")

// 用户交易密码即个人二级密码，绝不修改后台全局操作密码。
func (s *Service) ResetUserPassword(ctx context.Context, actor string, publicID int64, kind, password string) error {
	if kind != "login" && kind != "secondary" {
		return ErrUserControlInvalid
	}
	if strings.TrimSpace(password) == "" {
		return ErrResetPasswordEmpty
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return err
	}
	return s.controlUser(ctx, actor, publicID, true, "user.password.reset", func(tx pgx.Tx, id string) error {
		if kind == "login" {
			tag, e := tx.Exec(ctx, `UPDATE auth_identities SET password_hash=$2 WHERE user_id=$1 AND provider='password'`, id, hash)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return ErrUserNotFound
			}
		} else {
			if _, e := tx.Exec(ctx, `UPDATE users SET secondary_password_hash=$2,updated_at=now() WHERE id=$1`, id, hash); e != nil {
				return e
			}
		}
		_, e := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
		return e
	}, kind)
}
func (s *Service) SetUserMuted(ctx context.Context, actor string, publicID int64, muted bool) error {
	return s.controlUser(ctx, actor, publicID, false, "user.chat_mute.update", func(tx pgx.Tx, id string) error {
		_, err := tx.Exec(ctx, `UPDATE users SET chat_muted=$2,updated_at=now() WHERE id=$1`, id, muted)
		return err
	}, strconv.FormatBool(muted))
}
func (s *Service) controlUser(ctx context.Context, actor string, publicID int64, adminOnly bool, action string, fn func(pgx.Tx, string) error, detail string) error {
	if publicID < 100000 {
		return ErrUserNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var isAdmin, allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code='admin'),EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actor).Scan(&isAdmin, &allowed)
	if err != nil {
		return err
	}
	if !allowed || (adminOnly && !isAdmin) {
		return ErrUserControlForbidden
	}
	var id string
	var staff bool
	err = tx.QueryRow(ctx, `SELECT id::text,EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.code IN ('admin','operator')) FROM users u WHERE public_id=$1 FOR UPDATE`, publicID).Scan(&id, &staff)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if !isAdmin && staff {
		return ErrUserControlForbidden
	}
	if err = fn(tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,$3,'user',$4,jsonb_build_object('detail',$5::text))`, uuid.NewString(), actor, action, id, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
