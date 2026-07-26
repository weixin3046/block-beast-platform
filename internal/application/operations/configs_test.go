package operations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPutConfigValidatesBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	for _, testCase := range []struct {
		key   string
		input ConfigInput
	}{
		{key: "Invalid Key", input: ConfigInput{Value: json.RawMessage(`{}`), Visibility: "public"}},
		{key: "valid.key", input: ConfigInput{Value: json.RawMessage(`invalid`), Visibility: "public"}},
		{key: "valid.key", input: ConfigInput{Value: json.RawMessage(`{}`), Visibility: "secret"}},
		{key: "pqpa.secret", input: ConfigInput{Value: json.RawMessage(`"value"`), Visibility: "internal"}},
		{key: "valid.key", input: ConfigInput{Value: json.RawMessage(`{"api_key":"value"}`), Visibility: "internal"}},
	} {
		if _, err := service.PutConfig(context.Background(), testCase.key, "actor", testCase.input); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s error = %v", testCase.key, err)
		}
	}
}

func TestPlatformConfigVisibilityAndOptimisticLock(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	actorID := uuid.NewString()
	key := "test.config." + uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO users(id,display_name,login_name) VALUES ($1,'config actor',$2)`, actorID, "config-"+actorID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM platform_configs WHERE key=$1`, key)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, actorID)
	})
	service := NewService(pool)
	created, err := service.PutConfig(ctx, key, actorID, ConfigInput{
		Value: json.RawMessage(`{"enabled":true}`), Visibility: "public", ExpectedVersion: 0,
	})
	if err != nil || created.Version != 1 {
		t.Fatalf("created = %+v, err = %v", created, err)
	}
	if _, err := service.PublicConfig(ctx, key); err != nil {
		t.Fatalf("public read: %v", err)
	}
	if _, err := service.PutConfig(ctx, key, actorID, ConfigInput{
		Value: json.RawMessage(`{}`), Visibility: "public", ExpectedVersion: 0,
	}); !errors.Is(err, ErrConfigVersionConflict) {
		t.Fatalf("duplicate create error = %v", err)
	}
	updated, err := service.PutConfig(ctx, key, actorID, ConfigInput{
		Value: json.RawMessage(`{"enabled":false}`), Visibility: "internal", ExpectedVersion: 1,
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("updated = %+v, err = %v", updated, err)
	}
	if _, err := service.PublicConfig(ctx, key); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("internal public read error = %v", err)
	}
	if _, err := service.PutConfig(ctx, key, actorID, ConfigInput{
		Value: json.RawMessage(`{}`), Visibility: "internal", ExpectedVersion: 1,
	}); !errors.Is(err, ErrConfigVersionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
}
