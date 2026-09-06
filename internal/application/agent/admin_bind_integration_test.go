package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminBindSerializesGraphAndMaintainsDescendantPaths(t *testing.T) {
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

	admin, ordinary := uuid.NewString(), uuid.NewString()
	ancestor, parent := uuid.NewString(), uuid.NewString()
	target, child, grandchild := uuid.NewString(), uuid.NewString(), uuid.NewString()
	virtual := uuid.NewString()
	cycleA, cycleB := uuid.NewString(), uuid.NewString()
	ids := []string{admin, ordinary, ancestor, parent, target, child, grandchild, virtual, cycleA, cycleB}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,login_name,display_name,is_virtual) SELECT x,x::text,x::text,false FROM unnest($1::uuid[]) x`, ids[:7])
	exec(`INSERT INTO users(id,login_name,display_name,is_virtual) VALUES($1::uuid,$1::text,'virtual',true),($2::uuid,$2::text,'cycle a',false),($3::uuid,$3::text,'cycle b',false)`, virtual, cycleA, cycleB)
	exec(`INSERT INTO roles(id,code,description) VALUES(gen_random_uuid(),'admin','admin') ON CONFLICT(code) DO NOTHING`)
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, admin)
	label := func(id string) string { return strings.ReplaceAll(id, "-", "_") }
	exec(`INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES
		($1,$2,$3::ltree),($4,$5,$6::ltree),($7,$4,$8::ltree)`,
		parent, ancestor, label(ancestor)+"."+label(parent),
		child, target, label(target)+"."+label(child),
		grandchild, label(target)+"."+label(child)+"."+label(grandchild))
	defer func() {
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM agent_relations WHERE user_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=ANY($1::uuid[])`, ids)
		pool.Exec(ctx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, ids)
	}()

	service := NewService(pool)
	var targetPublic, parentPublic int64
	if err = pool.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, target).Scan(&targetPublic); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, parent).Scan(&parentPublic); err != nil {
		t.Fatal(err)
	}
	relation, err := service.AdminBind(ctx, admin, targetPublic, parentPublic)
	if err != nil || relation.UserID != targetPublic || relation.ParentUserID != parentPublic {
		t.Fatalf("admin bind: %+v %v", relation, err)
	}
	wantPrefix := label(ancestor) + "." + label(parent) + "." + label(target)
	for id, want := range map[string]string{
		target:     wantPrefix,
		child:      wantPrefix + "." + label(child),
		grandchild: wantPrefix + "." + label(child) + "." + label(grandchild),
	} {
		var got string
		if err = pool.QueryRow(ctx, `SELECT path::text FROM agent_relations WHERE user_id=$1`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("path %s=%s want=%s err=%v", id, got, want, err)
		}
	}
	if _, err = service.AdminBind(ctx, admin, targetPublic, parentPublic); !errors.Is(err, ErrRelationExists) {
		t.Fatalf("existing relation overwritten: %v", err)
	}
	if _, err = service.AdminBind(ctx, ordinary, targetPublic, parentPublic); !errors.Is(err, ErrAdminBindForbidden) {
		t.Fatalf("ordinary actor accepted: %v", err)
	}
	var virtualPublic int64
	if err = pool.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, virtual).Scan(&virtualPublic); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdminBind(ctx, admin, virtualPublic, parentPublic); !errors.Is(err, ErrVirtualRelation) {
		t.Fatalf("virtual target accepted: %v", err)
	}
	if _, err = service.AdminBind(ctx, admin, virtualPublic+9999999, parentPublic); !errors.Is(err, ErrRelationUserNotFound) {
		t.Fatalf("missing user error: %v", err)
	}
	var auditCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND action='admin.agent.bind'`, admin).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}

	// Two opposite player bindings must never both commit and form a cycle.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, pair := range [][2]string{{cycleA, cycleB}, {cycleB, cycleA}} {
		pair := pair
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- service.Bind(ctx, pair[0], pair[1])
		}()
	}
	wg.Wait()
	close(errs)
	success, rejected := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalidRelation) || errors.Is(err, ErrRelationExists) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("opposite binds success=%d rejected=%d", success, rejected)
	}
}
