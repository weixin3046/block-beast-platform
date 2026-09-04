package operations

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNormalizePhrase(t *testing.T) {
	base := PhraseInput{Title: " title ", Content: " content ", Category: " cs "}
	p, err := normalizePhrase(base)
	if err != nil || p.Title != "title" || p.Content != "content" || p.Category != "cs" {
		t.Fatal(p, err)
	}
	for _, change := range []func(*PhraseInput){func(p *PhraseInput) { p.Title = " " }, func(p *PhraseInput) { p.Content = "" }, func(p *PhraseInput) { p.Title = strings.Repeat("中", 101) }, func(p *PhraseInput) { p.Title = strings.Repeat("😀", 51) }, func(p *PhraseInput) { p.Content = strings.Repeat("a", 2001) }, func(p *PhraseInput) { p.Category = strings.Repeat("a", 51) }, func(p *PhraseInput) { p.Sort = -1 }} {
		p = base
		change(&p)
		if _, err = normalizePhrase(p); !errors.Is(err, ErrPhraseInvalid) {
			t.Fatal("accepted invalid phrase")
		}
	}
}
func TestPhrasesIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := pgx.Identifier{"phrases_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE users(id uuid PRIMARY KEY,status text DEFAULT 'active');CREATE TABLE roles(id uuid,code text);CREATE TABLE user_roles(user_id uuid,role_id uuid);CREATE TABLE audit_logs(id uuid,actor_user_id uuid,action text,target_type text,target_id text,payload jsonb);`)
	migration, err := os.ReadFile("../../../migrations/0051_customer_phrases.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	admin, op := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id) VALUES($1),($2)`, admin, op)
	exec(`INSERT INTO roles VALUES($1,'admin'),($2,'operator')`, admin, op)
	exec(`INSERT INTO user_roles VALUES($1,$1),($2,$2)`, admin, op)
	s := NewService(pool)
	input := PhraseInput{Title: "欢迎", Content: "您好", Category: "客服", Enabled: true}
	key := uuid.NewString()
	p, err := s.SavePhrase(ctx, admin, 0, key, input)
	if err != nil {
		t.Fatal(err)
	}
	dup, err := s.SavePhrase(ctx, admin, 0, key, input)
	if err != nil || p.ID != dup.ID {
		t.Fatal("duplicate", err)
	}
	changed := input
	changed.Content = "changed"
	if _, err = s.SavePhrase(ctx, admin, 0, key, changed); !errors.Is(err, ErrPhraseConflict) {
		t.Fatal(err)
	}
	// 即使仍持有 operator 角色，禁用账号也必须拒绝。
	exec(`UPDATE users SET status='disabled' WHERE id=$1`, op)
	if _, err = s.SavePhrase(ctx, op, 0, uuid.NewString(), input); !errors.Is(err, ErrPhraseForbidden) {
		t.Fatal(err)
	}
	if _, err = s.ListPhrases(ctx, op, PhraseFilter{}); !errors.Is(err, ErrPhraseForbidden) {
		t.Fatal(err)
	}
	exec(`UPDATE users SET status='active' WHERE id=$1`, op)
	input.Title = "第二条"
	q, err := s.SavePhrase(ctx, admin, 0, uuid.NewString(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Category = ""
	input.Enabled = false
	r, err := s.SavePhrase(ctx, admin, 0, uuid.NewString(), input)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPhrases(ctx, admin, PhraseFilter{Limit: 1})
	if err != nil || page.Count != 3 || len(page.Items) != 1 || page.Items[0].ID != r.ID {
		t.Fatal("stable order", page, err)
	}
	enabled := true
	page, err = s.ListPhrases(ctx, admin, PhraseFilter{Category: " 客服 ", Enabled: &enabled})
	if err != nil || page.Count != 2 {
		t.Fatal(page, err)
	}
	page, err = s.ListPhrases(ctx, admin, PhraseFilter{Page: 100})
	if err != nil || page.Count != 3 || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	ordered, err := s.ReorderPhrases(ctx, admin, []int64{p.ID})
	if err != nil || ordered[0].ID != p.ID || ordered[1].ID != r.ID || ordered[2].ID != q.ID {
		t.Fatal(ordered, err)
	}
	for i, p := range ordered {
		if p.Sort != int64(i) {
			t.Fatal("sort not contiguous")
		}
	}
	for _, ids := range [][]int64{nil, {p.ID, p.ID}, {-1}} {
		if _, err = s.ReorderPhrases(ctx, admin, ids); !errors.Is(err, ErrPhraseInvalid) {
			t.Fatal(err)
		}
	}
	if _, err = s.ReorderPhrases(ctx, admin, []int64{999999}); !errors.Is(err, ErrPhraseNotFound) {
		t.Fatal(err)
	}
	updated, err := s.SavePhrase(ctx, admin, p.ID, "", changed)
	if err != nil || updated.CreatedAt != p.CreatedAt || updated.Content != "changed" {
		t.Fatal(updated, err)
	}
	updated, err = s.SetPhraseEnabled(ctx, admin, p.ID, false)
	if err != nil || updated.Enabled {
		t.Fatal(updated, err)
	}
	// 同一创建键的并发重试只能产生一条话术和一条创建审计。
	concurrentKey := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan Phrase, 5)
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := s.SavePhrase(ctx, admin, 0, concurrentKey, input)
			results <- p
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id int64
	for p := range results {
		if id != 0 && id != p.ID {
			t.Fatal("duplicate create")
		}
		id = p.ID
	}
	// 强制审计失败时，业务更新也必须回滚。
	exec(`ALTER TABLE audit_logs ADD CONSTRAINT fail_delete CHECK(action <> 'phrase.delete') NOT VALID`)
	if err = s.DeletePhrase(ctx, admin, p.ID); err == nil {
		t.Fatal("expected audit failure")
	}
	exec(`ALTER TABLE audit_logs DROP CONSTRAINT fail_delete`)
	if err = s.DeletePhrase(ctx, admin, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeletePhrase(ctx, admin, p.ID); !errors.Is(err, ErrPhraseNotFound) {
		t.Fatal(err)
	}
	if _, err = s.SavePhrase(ctx, admin, 0, key, PhraseInput{Title: "欢迎", Content: "您好", Category: "客服", Enabled: true}); !errors.Is(err, ErrPhraseConflict) {
		t.Fatal("deleted replay", err)
	}
	if _, err = s.SetPhraseEnabled(ctx, admin, p.ID, true); !errors.Is(err, ErrPhraseNotFound) {
		t.Fatal(err)
	}
	ordered, err = s.ReorderPhrases(ctx, admin, []int64{})
	if err != nil || len(ordered) != 3 {
		t.Fatal(ordered, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='phrase.create'`).Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	// operator 可以管理共享库，包括修改 admin 创建的话术。
	if _, err = s.ListPhrases(ctx, op, PhraseFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePhrase(ctx, op, q.ID, "", input); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPhraseEnabled(ctx, op, q.ID, false); err != nil {
		t.Fatal(err)
	}
	created, err := s.SavePhrase(ctx, op, 0, uuid.NewString(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReorderPhrases(ctx, op, []int64{created.ID}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeletePhrase(ctx, op, created.ID); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM user_roles WHERE user_id=$1`, op)
	if _, err = s.ListPhrases(ctx, op, PhraseFilter{}); !errors.Is(err, ErrPhraseForbidden) {
		t.Fatal("revoked operator", err)
	}
	if err = s.DeletePhrase(ctx, op, q.ID); !errors.Is(err, ErrPhraseForbidden) {
		t.Fatal("revoked operator write", err)
	}
}
