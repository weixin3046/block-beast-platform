package operations

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidLoginWhitelist = errors.New("白名单参数无效，请指定用户ID或单个IP地址，备注最多200字")

type LoginWhitelistInput struct {
	UserID int64  `json:"user_id"`
	IP     string `json:"ip"`
	Remark string `json:"remark"`
}
type LoginWhitelistEntry struct {
	ID        string    `json:"id"`
	UserID    *int64    `json:"user_id"`
	IP        string    `json:"ip"`
	Remark    string    `json:"remark"`
	UpdatedBy int64     `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}
type LoginWhitelist struct {
	RiskCheckEnabled   bool                  `json:"risk_check_enabled"`
	WhitelistEffective bool                  `json:"whitelist_effective"`
	Status             string                `json:"status"`
	Items              []LoginWhitelistEntry `json:"items"`
}

func whitelistStaff(ctx context.Context, tx pgx.Tx, actor string) error {
	if _, e := uuid.Parse(actor); e != nil {
		return ErrUserControlForbidden
	}
	var allowed bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actor).Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return ErrUserControlForbidden
	}
	return nil
}

const whitelistSelect = `SELECT w.id::text,u.public_id,COALESCE(host(w.ip),''),w.remark,actor.public_id,w.updated_at FROM login_risk_whitelist w LEFT JOIN users u ON u.id=w.user_id JOIN users actor ON actor.id=w.updated_by`

func scanWhitelist(row pgx.Row) (LoginWhitelistEntry, error) {
	var v LoginWhitelistEntry
	e := row.Scan(&v.ID, &v.UserID, &v.IP, &v.Remark, &v.UpdatedBy, &v.UpdatedAt)
	return v, e
}
func (s *Service) ListLoginWhitelist(ctx context.Context, actor string) (LoginWhitelist, error) {
	out := LoginWhitelist{Status: "未启用第三方IP及设备风险检测，白名单尚不产生豁免效果", Items: []LoginWhitelistEntry{}}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = whitelistStaff(ctx, tx, actor); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, whitelistSelect+` ORDER BY w.updated_at DESC,w.id`)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanWhitelist(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveLoginWhitelist(ctx context.Context, actor string, in LoginWhitelistInput) (LoginWhitelistEntry, error) {
	var out LoginWhitelistEntry
	in.IP = strings.TrimSpace(in.IP)
	if in.UserID < 0 || (in.UserID == 0) == (in.IP == "") || utf8.RuneCountInString(in.Remark) > 200 {
		return out, ErrInvalidLoginWhitelist
	}
	if in.IP != "" {
		ip, e := netip.ParseAddr(in.IP)
		if e != nil || ip.Zone() != "" {
			return out, ErrInvalidLoginWhitelist
		}
		in.IP = ip.Unmap().String()
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = whitelistStaff(ctx, tx, actor); e != nil {
		return out, e
	}
	var id string
	if in.IP != "" {
		e = tx.QueryRow(ctx, `INSERT INTO login_risk_whitelist(ip,remark,updated_by) VALUES($1::inet,$2,$3) ON CONFLICT(ip) DO UPDATE SET remark=EXCLUDED.remark,updated_by=EXCLUDED.updated_by,updated_at=now() RETURNING id::text`, in.IP, in.Remark, actor).Scan(&id)
	} else {
		e = tx.QueryRow(ctx, `INSERT INTO login_risk_whitelist(user_id,remark,updated_by) SELECT id,$2,$3 FROM users WHERE public_id=$1 ON CONFLICT(user_id) DO UPDATE SET remark=EXCLUDED.remark,updated_by=EXCLUDED.updated_by,updated_at=now() RETURNING id::text`, in.UserID, in.Remark, actor).Scan(&id)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrUserNotFound
	}
	if e != nil {
		return out, e
	}
	out, e = scanWhitelist(tx.QueryRow(ctx, whitelistSelect+` WHERE w.id=$1`, id))
	if e != nil {
		return out, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES(gen_random_uuid(),$1,'login_whitelist.save','login_whitelist',$2,jsonb_build_object('user_id',$3::bigint,'ip',$4::text,'remark',$5::text))`, actor, id, in.UserID, in.IP, in.Remark); e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
func (s *Service) DeleteLoginWhitelist(ctx context.Context, actor, id string) error {
	if _, e := uuid.Parse(id); e != nil {
		return ErrInvalidLoginWhitelist
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = whitelistStaff(ctx, tx, actor); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `DELETE FROM login_risk_whitelist WHERE id=$1`, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() > 0 {
		if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES(gen_random_uuid(),$1,'login_whitelist.delete','login_whitelist',$2,'{}')`, actor, id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
