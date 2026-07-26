package operations

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNormalizeRoles(t *testing.T) {
	roles, err := normalizeRoles([]string{"operator", "PLAYER", "operator"})
	if err != nil || len(roles) != 2 || roles[0] != "operator" || roles[1] != "player" {
		t.Fatalf("roles = %#v, err = %v", roles, err)
	}
	if _, err := normalizeRoles([]string{"superadmin"}); !errors.Is(err, ErrInvalidRoles) {
		t.Fatalf("invalid role error = %v", err)
	}
}

func TestSetUserRolesRevokesSessionsAndProtectsSelf(t *testing.T) {
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
	actorID, targetID := uuid.NewString(), uuid.NewString()
	_, err = pool.Exec(ctx, `
		INSERT INTO users(id,display_name,login_name) VALUES ($1,'role actor',$3),($2,'role target',$4)`,
		actorID, targetID, "role-"+actorID, "role-"+targetID)
	if err == nil {
		_, err = pool.Exec(ctx, `
			INSERT INTO user_roles(user_id,role_id)
			SELECT $1::uuid,id FROM roles WHERE code='admin'
			UNION ALL
			SELECT $2::uuid,id FROM roles WHERE code='player'`, actorID, targetID)
	}
	if err == nil {
		_, err = pool.Exec(ctx, `
			INSERT INTO sessions(id,user_id,token_hash,audience,expires_at)
			VALUES ($1,$2,$3,'player',$4)`,
			uuid.NewString(), targetID, "session-"+uuid.NewString(), time.Now().Add(time.Hour))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN ($1,$2)`, actorID, targetID)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id IN ($1,$2)`, actorID, targetID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, actorID, targetID)
	})
	service := NewService(pool)
	result, err := service.SetUserRoles(ctx, actorID, targetID, []string{identity.RoleOperator})
	if err != nil || len(result.Roles) != 1 || result.Roles[0] != identity.RoleOperator {
		t.Fatalf("assignment = %+v, err = %v", result, err)
	}
	var sessions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, targetID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("sessions = %d, err = %v", sessions, err)
	}
	if _, err := service.SetUserRoles(ctx, actorID, actorID, []string{identity.RoleOperator}); !errors.Is(err, ErrCannotRemoveOwnAdmin) {
		t.Fatalf("self removal error = %v", err)
	}
	if err := service.SetUserStatus(ctx, actorID, actorID, "disabled"); !errors.Is(err, ErrCannotDisableOwnAdmin) {
		t.Fatalf("self disable error = %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO sessions(id,user_id,token_hash,audience,expires_at)
		VALUES ($1,$2,$3,'admin',$4)`,
		uuid.NewString(), targetID, "session-"+uuid.NewString(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetUserStatus(ctx, actorID, targetID, "disabled"); err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, targetID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("disabled user sessions = %d, err = %v", sessions, err)
	}
}
