package operations

import (
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/domain/wallet"
	"regexp"
	"strings"
)

type UserBalance struct {
	Currency       string `json:"currency"`
	Decimals       int    `json:"decimals"`
	AvailableMinor int64  `json:"available_minor"`
	FrozenMinor    int64  `json:"frozen_minor"`
	Available      string `json:"available"`
	Frozen         string `json:"frozen"`
}
type UserSearch struct {
	Status, Query, UserType, Minimum, Maximum string
	Currencies                                []string
	Limit, Offset                             int
}

var balanceFilterPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,18})?$`)

func (s *Service) SearchUsers(ctx context.Context, f UserSearch) ([]User, error) {
	if f.UserType != "" && f.UserType != "real" && f.UserType != "virtual" {
		return nil, ErrUserControlInvalid
	}
	if (f.Minimum != "" && !balanceFilterPattern.MatchString(f.Minimum)) || (f.Maximum != "" && !balanceFilterPattern.MatchString(f.Maximum)) || f.Offset < 0 {
		return nil, ErrUserControlInvalid
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	for i := range f.Currencies {
		f.Currencies[i] = strings.ToUpper(strings.TrimSpace(f.Currencies[i]))
	}
	rows, err := s.pool.Query(ctx, `SELECT u.public_id,COALESCE(u.login_name,''),u.display_name,u.status,u.created_at,u.invitation_code,COALESCE(u.agent_level,0),u.is_virtual,u.chat_muted,
 COALESCE((SELECT array_agg(r.code ORDER BY r.code) FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id),'{}'),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('currency',w.currency,'decimals',c.decimals,'available_minor',w.available_minor,'frozen_minor',w.frozen_minor) ORDER BY c.sort_order,c.code) FROM wallets w JOIN currencies c ON c.code=w.currency WHERE w.user_id=u.id AND (cardinality($4::text[])=0 OR w.currency=ANY($4))),'[]')
 ,p.public_id,p.display_name,p.invitation_code
 FROM users u LEFT JOIN agent_relations ar ON ar.user_id=u.id LEFT JOIN users p ON p.id=ar.parent_user_id
 WHERE ($1='' OR u.status=$1) AND ($2='' OR u.public_id::text=$2 OR u.login_name ILIKE '%'||$2||'%' OR u.display_name ILIKE '%'||$2||'%')
 AND ($3='' OR u.is_virtual=($3='virtual'))
 AND ((cardinality($4::text[])=0 AND $5='' AND $6='') OR EXISTS(SELECT 1 FROM wallets w JOIN currencies c ON c.code=w.currency WHERE w.user_id=u.id AND (cardinality($4::text[])=0 OR w.currency=ANY($4)) AND ($5='' OR w.available_minor::numeric/power(10::numeric,c.decimals)>=NULLIF($5,'')::numeric) AND ($6='' OR w.available_minor::numeric/power(10::numeric,c.decimals)<=NULLIF($6,'')::numeric)))
 ORDER BY u.created_at DESC,u.public_id DESC LIMIT $7 OFFSET $8`, f.Status, f.Query, f.UserType, append([]string{}, f.Currencies...), f.Minimum, f.Maximum, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var raw []byte
		if err = rows.Scan(&u.ID, &u.LoginName, &u.DisplayName, &u.Status, &u.CreatedAt, &u.InvitationCode, &u.AgentLevel, &u.IsVirtual, &u.ChatMuted, &u.Roles, &raw, &u.ParentUserID, &u.ParentDisplayName, &u.ParentInvitationCode); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &u.Balances); err != nil {
			return nil, err
		}
		for i := range u.Balances {
			b := &u.Balances[i]
			b.Available, err = wallet.FormatDisplayAmount(b.AvailableMinor, b.Decimals)
			if err != nil {
				return nil, err
			}
			b.Frozen, err = wallet.FormatDisplayAmount(b.FrozenMinor, b.Decimals)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
