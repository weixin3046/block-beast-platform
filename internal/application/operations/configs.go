package operations

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrConfigNotFound = errors.New("platform config not found")
var ErrInvalidConfig = errors.New("config key, visibility, or JSON value is invalid")
var ErrConfigVersionConflict = errors.New("platform config version conflict")

var configKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{1,127}$`)

type PlatformConfig struct {
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	Visibility string          `json:"visibility"`
	Version    int64           `json:"version"`
	UpdatedBy  *string         `json:"updated_by,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type ConfigInput struct {
	Value           json.RawMessage `json:"value"`
	Visibility      string          `json:"visibility"`
	ExpectedVersion int64           `json:"expected_version"`
}

func (service *Service) PublicConfig(ctx context.Context, key string) (PlatformConfig, error) {
	return service.findConfig(ctx, key, true)
}

func (service *Service) ListConfigs(ctx context.Context, visibility string, limit int) ([]PlatformConfig, error) {
	if visibility != "" && visibility != "public" && visibility != "internal" {
		return nil, ErrInvalidConfig
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := service.pool.Query(ctx, `
		SELECT key,value,visibility,version,updated_by::text,updated_at
		FROM platform_configs
		WHERE $1='' OR visibility=$1
		ORDER BY key LIMIT $2`, visibility, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PlatformConfig, 0)
	for rows.Next() {
		var item PlatformConfig
		if err := rows.Scan(&item.Key, &item.Value, &item.Visibility, &item.Version, &item.UpdatedBy, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) PutConfig(ctx context.Context, key, actorUserID string, input ConfigInput) (PlatformConfig, error) {
	if !configKeyPattern.MatchString(key) || !json.Valid(input.Value) || len(input.Value) > 64<<10 ||
		(input.Visibility != "public" && input.Visibility != "internal") || input.ExpectedVersion < 0 {
		return PlatformConfig{}, ErrInvalidConfig
	}
	if containsSensitiveConfigField(key, input.Value) {
		return PlatformConfig{}, ErrInvalidConfig
	}
	var item PlatformConfig
	var err error
	if input.ExpectedVersion == 0 {
		err = service.pool.QueryRow(ctx, `
			INSERT INTO platform_configs (key,value,visibility,version,updated_by)
			VALUES ($1,$2,$3,1,$4)
			ON CONFLICT (key) DO NOTHING
			RETURNING key,value,visibility,version,updated_by::text,updated_at`,
			key, input.Value, input.Visibility, actorUserID).
			Scan(&item.Key, &item.Value, &item.Visibility, &item.Version, &item.UpdatedBy, &item.UpdatedAt)
	} else {
		err = service.pool.QueryRow(ctx, `
			UPDATE platform_configs
			SET value=$2,visibility=$3,version=version+1,updated_by=$4,updated_at=now()
			WHERE key=$1 AND version=$5
			RETURNING key,value,visibility,version,updated_by::text,updated_at`,
			key, input.Value, input.Visibility, actorUserID, input.ExpectedVersion).
			Scan(&item.Key, &item.Value, &item.Visibility, &item.Version, &item.UpdatedBy, &item.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if lookupErr := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_configs WHERE key=$1)`, key).Scan(&exists); lookupErr != nil {
			return PlatformConfig{}, lookupErr
		}
		if exists {
			return PlatformConfig{}, ErrConfigVersionConflict
		}
		if input.ExpectedVersion > 0 {
			return PlatformConfig{}, ErrConfigNotFound
		}
		return PlatformConfig{}, ErrConfigVersionConflict
	}
	return item, err
}

func containsSensitiveConfigField(key string, raw json.RawMessage) bool {
	sensitive := regexp.MustCompile(`(?i)(^|[._-])(secret|password|token|api[_-]?key|private[_-]?key|access[_-]?key)($|[._-])`)
	if sensitive.MatchString(key) {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(item any) bool {
		switch typed := item.(type) {
		case map[string]any:
			for field, nested := range typed {
				if sensitive.MatchString(field) || visit(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range typed {
				if visit(nested) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func (service *Service) findConfig(ctx context.Context, key string, publicOnly bool) (PlatformConfig, error) {
	var item PlatformConfig
	err := service.pool.QueryRow(ctx, `
		SELECT key,value,visibility,version,updated_by::text,updated_at
		FROM platform_configs
		WHERE key=$1 AND (NOT $2 OR visibility='public')`, key, publicOnly).
		Scan(&item.Key, &item.Value, &item.Visibility, &item.Version, &item.UpdatedBy, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformConfig{}, ErrConfigNotFound
	}
	return item, err
}
