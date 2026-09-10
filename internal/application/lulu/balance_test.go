package lulu

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

type balanceFixture struct{}

func (balanceFixture) Balance(context.Context) (string, error) { return "5160.07704", nil }
func TestBalancePausedAccount(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	staff, player := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name) VALUES($1,'balance staff'),($2,'balance player')`, staff, player)
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='operator'`, staff)
	s := NewService(pool, "").WithEncryptionKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	token, err := s.seal("test-token", "token")
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.seal("test-protocol", "protocol")
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE lulu_config SET receiver_uid='34445963',enabled=false,api_url='https://example.invalid',token_cipher=$1,protocol_cipher=$2 WHERE singleton`, token, key)
	calls := 0
	s.WithBalanceFactory(func(base, uid, token, key string) (BalanceReader, error) {
		calls++
		if uid != "34445963" || token != "test-token" || key != "test-protocol" {
			t.Fatal("incorrect credentials")
		}
		return balanceFixture{}, nil
	})
	if _, err := s.AccountBalance(ctx, player); !errors.Is(err, ErrForbidden) || calls != 0 {
		t.Fatal(err, calls)
	}
	out, err := s.AccountBalance(ctx, staff)
	if err != nil || out.Balance != "5160.07704" || out.QueriedAt.IsZero() || calls != 1 {
		t.Fatal(out, err, calls)
	}
	runtime, err := s.RuntimeConfig(ctx)
	if err != nil || runtime.Token != "" {
		t.Fatal("worker config behavior changed", err)
	}
}
