package virtualbot

import (
	"context"
	"github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/application/betting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestPlanLifecycleAndRoundScheduling(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	user, actor, room := uuid.NewString(), uuid.NewString(), "94000000-0000-4000-8000-000000000001"
	exec := func(q string, a ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	exec("INSERT INTO users(id,display_name,is_virtual) VALUES($1,'robot-plan-test',true),($2,'robot-admin-test',false)", user, actor)
	exec("INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'", actor)
	var public int64
	if e = p.QueryRow(ctx, "SELECT public_id FROM users WHERE id=$1", user).Scan(&public); e != nil {
		t.Fatal(e)
	}
	seq := time.Now().UnixNano() / 9 * 9
	rounds := []string{uuid.NewString(), uuid.NewString()}
	defer func() {
		p.Exec(ctx, "DELETE FROM agent_relations WHERE user_id=$1", user)
		p.Exec(ctx, "DELETE FROM outbox_events WHERE aggregate_id IN (SELECT id::text FROM bets WHERE user_id=$1)", user)
		p.Exec(ctx, "DELETE FROM audit_logs WHERE actor_user_id=$1", actor)
		p.Exec(ctx, "DELETE FROM bets WHERE user_id=$1", user)
		p.Exec(ctx, "DELETE FROM robot_plans WHERE user_id=$1", user)
		p.Exec(ctx, "DELETE FROM rounds WHERE id=ANY($1::uuid[])", rounds)
		p.Exec(ctx, "DELETE FROM wallets WHERE user_id=$1", user)
		p.Exec(ctx, "DELETE FROM user_roles WHERE user_id=$1", actor)
		p.Exec(ctx, "DELETE FROM users WHERE id=ANY($1::uuid[])", []string{user, actor})
	}()
	s := NewService(p, betting.NewService(p))
	in := PlanInput{UserID: public, GameType: "hash_9", GameRoomID: room, Currency: "POINTS", MinStakeMinor: 1000, MaxStakeMinor: 1000, SkipMin: 1, SkipMax: 1, Enabled: true, Selections: []Selection{{PlayMode: "road", Pick: "odd"}}}
	key := uuid.NewString()
	plan, e := s.CreatePlan(ctx, actor, key, in)
	if e != nil {
		t.Fatal(e)
	}
	if replay, err := s.CreatePlan(ctx, actor, key, in); err != nil || replay.ID != plan.ID {
		t.Fatalf("create replay: %+v %v", replay, err)
	}
	changed := in
	changed.SkipMax = 2
	if _, err := s.CreatePlan(ctx, actor, key, changed); err != ErrPlanConflict {
		t.Fatalf("conflict: %v", err)
	}
	in.UserID = 0
	if _, e = s.CreatePlan(ctx, actor, uuid.NewString(), in); e == nil {
		t.Fatal("invalid user accepted")
	}
	in.UserID = public
	if _, e = s.CreatePlan(ctx, user, uuid.NewString(), in); e == nil {
		t.Fatal("player created plan")
	}
	// At the first observed round only schedule the next period, like the reference.
	exec("INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,'09000000-0000-4000-8000-000000000001',$2,'open',now()+interval '1 hour')", rounds[0], seq)
	if _, e = s.RunDue(ctx, 100); e != nil {
		t.Fatal(e)
	}
	result, e := s.GetPlan(ctx, plan.ID)
	if e != nil || result.NextRoundSequence == nil || *result.NextRoundSequence != seq+9 {
		t.Fatalf("schedule %+v %v", result, e)
	}
	exec("UPDATE rounds SET status='closed' WHERE id=$1", rounds[0])
	exec("INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,'09000000-0000-4000-8000-000000000001',$2,'open',now()+interval '1 hour')", rounds[1], seq+9)
	exec("UPDATE robot_plans SET last_checked_at=NULL WHERE id=$1", plan.ID)
	if _, e = s.RunDue(ctx, 100); e != nil {
		t.Fatal(e)
	}
	exec("UPDATE robot_plans SET last_checked_at=NULL WHERE id=$1", plan.ID)
	if _, e = s.RunDue(ctx, 100); e != nil {
		t.Fatal(e)
	}
	var n, balance int64
	if e = p.QueryRow(ctx, "SELECT count(*) FROM bets WHERE robot_plan_id=$1 AND is_simulated", plan.ID).Scan(&n); e != nil || n != 1 {
		t.Fatalf("bets=%d %v", n, e)
	}
	if e = p.QueryRow(ctx, "SELECT available_minor FROM wallets WHERE user_id=$1 AND currency='POINTS'", user).Scan(&balance); e != nil || balance != 0 {
		t.Fatalf("balance=%d %v", balance, e)
	}
	exec("INSERT INTO agent_relations(user_id,parent_user_id) VALUES($1,$2)", user, actor)
	exec("UPDATE bets SET status='lost',settled_at=now() WHERE robot_plan_id=$1", plan.ID)
	team, err := agent.NewService(p).TeamSummary(ctx, actor)
	if err != nil || team.DirectPlayers != 0 || len(team.Metrics) != 0 {
		t.Fatalf("robot leaked into agent team: %+v %v", team, err)
	}
	if _, e = s.SetPlanEnabled(ctx, actor, plan.ID, false); e != nil {
		t.Fatal(e)
	}
	if e = s.DeletePlan(ctx, actor, plan.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetPlan(ctx, plan.ID); e == nil {
		t.Fatal("deleted plan visible")
	}
}

func TestPlanSelectionValidation(t *testing.T) {
	in := PlanInput{UserID: 100009, GameType: "hash_9", GameRoomID: uuid.NewString(), Currency: "POINTS", MinStakeMinor: 1000, MaxStakeMinor: 2000, SkipMin: 1, SkipMax: 3, Selections: []Selection{{PlayMode: "road", Pick: "odd"}, {PlayMode: "guess", Pick: "9"}}}
	if e := validatePlan(in); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []Selection{{"road", "9"}, {"dodge", "odd"}, {"guess", "10"}, {"other", "odd"}} {
		v := in
		v.Selections = []Selection{bad}
		if validatePlan(v) == nil {
			t.Fatalf("invalid selection %+v", bad)
		}
	}
	in.SkipMin = 0
	if validatePlan(in) == nil {
		t.Fatal("zero period accepted")
	}
}
