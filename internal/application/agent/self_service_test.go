package agent

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestIncomePeriodBoundaries(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, "2026-09-13T16:30:00Z") // Monday in Beijing.
	p := incomePeriods(at)
	for i, want := range []string{"2026-09-14T00:00:00+08:00", "2026-09-13T00:00:00+08:00", "2026-09-14T00:00:00+08:00", "2026-09-07T00:00:00+08:00"} {
		if got := p[i].From.Format(time.RFC3339); got != want {
			t.Fatalf("period %d: %s", i, got)
		}
	}
	if !p[1].To.Equal(p[0].From) || !p[3].To.Equal(p[2].From) {
		t.Fatal("non-contiguous boundaries")
	}
}
func TestDirectLevelPermissions(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	parent, child, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	run := func(q string, a ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	run(`INSERT INTO users(id,display_name,agent_level) VALUES($1,'level parent',3),($2,'level child',NULL),($3,'unrelated',1)`, parent, child, other)
	run(`INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,'level_child')`, child, parent)
	var id, otherID int64
	p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, child).Scan(&id)
	p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, other).Scan(&otherID)
	s := NewService(p)
	for _, l := range []int{1, 2, 2, 0} {
		if err = s.SetDirectPlayerLevel(ctx, parent, id, l); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND action='agent.direct_player.level.update'`, parent).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit count=%d err=%v", n, err)
	}
	for _, tc := range []struct {
		owner string
		id    int64
		level int
	}{{parent, id, 3}, {parent, id, 4}, {parent, otherID, 1}, {child, id, 1}, {other, id, 0}} {
		if err = s.SetDirectPlayerLevel(ctx, tc.owner, tc.id, tc.level); !errors.Is(err, ErrChildLevelForbidden) {
			t.Fatalf("unauthorized %+v: %v", tc, err)
		}
	}
	run(`UPDATE users SET agent_level=3 WHERE id=$1`, child)
	if err = s.SetDirectPlayerLevel(ctx, parent, id, 1); !errors.Is(err, ErrChildLevelForbidden) {
		t.Fatal("same-level child editable", err)
	}
	run(`UPDATE users SET agent_level=NULL,is_virtual=true WHERE id=$1`, child)
	if err = s.SetDirectPlayerLevel(ctx, parent, id, 1); !errors.Is(err, ErrChildLevelForbidden) {
		t.Fatal("virtual child editable", err)
	}
	if err = s.SetDirectPlayerLevel(ctx, parent, id, 7); !errors.Is(err, ErrChildLevelInvalid) {
		t.Fatal(err)
	}
	// Audit rows are intentionally immutable; these UUID-only fixtures remain in
	// the disposable integration database for inspection.
}
