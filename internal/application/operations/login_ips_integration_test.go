package operations

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestLoginIPsReturnHostsAndSharedUsersWithSingleConnection(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.MaxConns = 1
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	ids := []string{uuid.NewString(), uuid.NewString()}
	var public int64
	for _, id := range ids {
		if _, e = p.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'IP regression')`, id); e != nil {
			t.Fatal(e)
		}
	}
	defer func() {
		p.Exec(context.Background(), `DELETE FROM user_login_history WHERE user_id=ANY($1::uuid[])`, ids)
		p.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, ids)
	}()
	if e = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, ids[0]).Scan(&public); e != nil {
		t.Fatal(e)
	}
	s := NewService(p)
	for _, ip := range []string{"198.51.100.44", "2001:db8::44"} {
		for _, id := range ids {
			if e = s.RecordLogin(ctx, id, ip, "player"); e != nil {
				t.Fatal(e)
			}
		}
	}
	got, e := s.UserLoginIPs(ctx, public)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	for _, ip := range got {
		if ip.IP != "198.51.100.44" && ip.IP != "2001:db8::44" {
			t.Fatalf("not host: %s", ip.IP)
		}
		seen := map[int64]bool{}
		for _, u := range ip.Users {
			seen[u.UserID] = true
		}
		if !seen[public] || len(ip.Users) < 2 {
			t.Fatalf("shared users=%+v", ip)
		}
	}
}
