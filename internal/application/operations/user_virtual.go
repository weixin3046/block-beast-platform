package operations

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ConvertUserToVirtual preserves wallets and the funding mode saved on existing bets.
func (s *Service) ConvertUserToVirtual(ctx context.Context, actor string, publicID int64) error {
	if publicID < 10001 || publicID > 99999 {
		return ErrUserNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Keep conversion mutually exclusive with role assignment so an account
	// cannot become staff while it is being converted to a virtual player.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-role-management', 0))`); err != nil {
		return err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actor).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrUserControlForbidden
	}
	var id string
	var virtual, player, staff bool
	err = tx.QueryRow(ctx, `SELECT id::text,is_virtual,
 EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.code='player'),
 EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.code IN ('admin','operator'))
 FROM users u WHERE public_id=$1 FOR UPDATE`, publicID).Scan(&id, &virtual, &player, &staff)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if staff || !player {
		return ErrUserControlForbidden
	}
	if virtual {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET is_virtual=true,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,'user.virtual.convert','user',$3,'{"before_is_virtual":false,"is_virtual":true}'::jsonb)`, uuid.NewString(), actor, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
