package lulu

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrConfigInvalid = errors.New("invalid Lulu configuration")
var ErrConfigConflict = errors.New("Lulu configuration version is stale or account has pending orders")

type Config struct {
	APIURL                string     `json:"api_url"`
	ScanStartAt           *time.Time `json:"scan_start_at"`
	TokenConfigured       bool       `json:"token_configured"`
	ProtocolKeyConfigured bool       `json:"protocol_key_configured"`
	ReceiverUID           string     `json:"receiver_uid"`
	Enabled               bool       `json:"enabled"`
	Version               int64      `json:"version"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
type ConfigUpdate struct {
	APIURL      *string    `json:"api_url,omitempty"`
	ScanStartAt *time.Time `json:"scan_start_at,omitempty"`
	loginToken  string
	ProtocolKey string `json:"protocol_key,omitempty"`
	ReceiverUID string `json:"receiver_uid"`
	Enabled     bool   `json:"enabled"`
	Version     int64  `json:"version"`
}

const configColumns = `receiver_uid,enabled,version,updated_at,api_url,scan_start_at,token_cipher IS NOT NULL,protocol_cipher IS NOT NULL`

func readConfig(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, lock bool) (Config, error) {
	q := `SELECT ` + configColumns + ` FROM lulu_config WHERE singleton`
	if lock {
		q += " FOR SHARE"
	}
	var c Config
	err := db.QueryRow(ctx, q).Scan(&c.ReceiverUID, &c.Enabled, &c.Version, &c.UpdatedAt, &c.APIURL, &c.ScanStartAt, &c.TokenConfigured, &c.ProtocolKeyConfigured)
	return c, err
}
func (s *Service) Config(ctx context.Context) (Config, error) { return readConfig(ctx, s.pool, false) }

// UpdateConfig encrypts credentials and audits only the public configuration.
func (s *Service) UpdateConfig(ctx context.Context, actor string, in ConfigUpdate) (Config, error) {
	var out Config
	if in.Version < 1 || (in.ReceiverUID != "" && !ValidUID(in.ReceiverUID)) || (in.Enabled && !ValidUID(in.ReceiverUID)) {
		return out, ErrConfigInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	// Configuration changes require admin or operator, including calls outside HTTP.
	var allowed bool
	if err = admin(ctx, tx, actor); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1 AND r.code IN ('admin','operator'))`, actor).Scan(&allowed)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, ErrForbidden
	}
	var old Config
	err = tx.QueryRow(ctx, `SELECT `+configColumns+` FROM lulu_config WHERE singleton FOR UPDATE`).Scan(&old.ReceiverUID, &old.Enabled, &old.Version, &old.UpdatedAt, &old.APIURL, &old.ScanStartAt, &old.TokenConfigured, &old.ProtocolKeyConfigured)
	if err != nil {
		return out, err
	}
	if old.Version != in.Version {
		return out, ErrConfigConflict
	}
	if old.ReceiverUID != in.ReceiverUID {
		// First pause, then resolve all active orders; never redirect old obligations.
		if old.Enabled {
			return out, ErrConfigConflict
		}
		var pending bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lulu_orders WHERE status IN ('requested','approved','sending','unknown'))`).Scan(&pending)
		if err != nil {
			return out, err
		}
		if pending {
			return out, ErrConfigConflict
		}
	}

	apiURL := old.APIURL
	if in.APIURL != nil {
		apiURL = *in.APIURL
	}
	start := old.ScanStartAt
	if in.ScanStartAt != nil {
		start = in.ScanStartAt
	}
	if (apiURL != "" && !validAPIURL(apiURL)) || !validateStart(start) || !validToken(in.loginToken) {
		return out, ErrConfigInvalid
	}
	if old.ReceiverUID != in.ReceiverUID && in.ReceiverUID != "" && in.loginToken == "" {
		return out, ErrConfigInvalid
	}
	if old.APIURL != apiURL && old.Enabled {
		return out, ErrConfigConflict
	}
	if old.ScanStartAt != nil && start != nil && start.After(*old.ScanStartAt) {
		var scanned bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lulu_collector_state WHERE receiver_uid=$1 AND scanned_at IS NOT NULL)`, in.ReceiverUID).Scan(&scanned); err != nil {
			return out, err
		}
		if scanned {
			return out, ErrConfigConflict
		}
	}
	var tokenCipher, keyCipher []byte
	if in.loginToken != "" {
		tokenCipher, err = s.seal(in.loginToken, "token")
		if err != nil {
			return out, err
		}
	}
	if in.ProtocolKey != "" {
		if !ValidProtocolKey(in.ProtocolKey) {
			return out, ErrConfigInvalid
		}
		keyCipher, err = s.seal(in.ProtocolKey, "protocol")
		if err != nil {
			return out, err
		}
	}
	if in.Enabled {
		if apiURL == "" || start == nil || (!old.TokenConfigured && len(tokenCipher) == 0) || (!old.ProtocolKeyConfigured && len(keyCipher) == 0) {
			return out, ErrConfigInvalid
		}
		if _, err = s.aead(); err != nil {
			return out, err
		}
	}
	err = tx.QueryRow(ctx, `UPDATE lulu_config SET receiver_uid=$1,enabled=$2,version=version+1,updated_by=$3,updated_at=now(),api_url=$4,scan_start_at=$5,token_cipher=COALESCE($6,token_cipher),protocol_cipher=COALESCE($7,protocol_cipher) WHERE singleton RETURNING `+configColumns, in.ReceiverUID, in.Enabled, actor, apiURL, start, tokenCipher, keyCipher).Scan(&out.ReceiverUID, &out.Enabled, &out.Version, &out.UpdatedAt, &out.APIURL, &out.ScanStartAt, &out.TokenConfigured, &out.ProtocolKeyConfigured)
	if err != nil {
		return out, err
	}
	payload, err := json.Marshal(map[string]any{"before": old, "after": out, "token_changed": in.loginToken != "", "protocol_key_changed": in.ProtocolKey != ""})
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES(gen_random_uuid(),$1,'lulu.config.update','lulu_config','singleton',$2)`, actor, payload)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
