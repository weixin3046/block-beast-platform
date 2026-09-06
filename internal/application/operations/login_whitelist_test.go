package operations

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestLoginWhitelistNormalizedAndInactive(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	actor := uuid.NewString()
	if _, e = p.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'whitelist admin')`, actor); e != nil {
		t.Fatal(e)
	}
	s := NewService(p)
	if _, e = s.SaveLoginWhitelist(ctx, actor, LoginWhitelistInput{IP: "203.0.113.77"}); !errors.Is(e, ErrUserControlForbidden) {
		t.Fatalf("player bypass: %v", e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, actor); e != nil {
		t.Fatal(e)
	}
	item, e := s.SaveLoginWhitelist(ctx, actor, LoginWhitelistInput{IP: "::ffff:203.0.113.77", Remark: "test"})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.SaveLoginWhitelist(ctx, actor, LoginWhitelistInput{IP: "203.0.113.77", Remark: "updated"})
	if e != nil || again.ID != item.ID || again.IP != "203.0.113.77" {
		t.Fatalf("normalization %+v %v", again, e)
	}
	list, e := s.ListLoginWhitelist(ctx, actor)
	if e != nil || list.RiskCheckEnabled || list.WhitelistEffective {
		t.Fatalf("false security claim %+v %v", list, e)
	}
	if _, e = s.SaveLoginWhitelist(ctx, actor, LoginWhitelistInput{IP: "invalid"}); !errors.Is(e, ErrInvalidLoginWhitelist) {
		t.Fatalf("bad address: %v", e)
	}
	if e = s.DeleteLoginWhitelist(ctx, actor, item.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteLoginWhitelist(ctx, actor, item.ID); e != nil {
		t.Fatal(e)
	}
}
