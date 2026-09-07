package chat

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestProvisionCustomerRoomsPreservesExistingAndSkipsVirtual(t *testing.T) {
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
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := tx.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	real, virtual, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name,is_virtual) VALUES($1,'real',false),($2,'virtual',true)`, real, virtual)
	exec(`INSERT INTO user_roles(user_id,role_id) SELECT u.id,r.id FROM users u CROSS JOIN roles r WHERE u.id=ANY($1::uuid[]) AND r.code='player'`, []string{real, virtual})
	exec(`INSERT INTO chat_rooms(id,room_type,customer_user_id,service_type) VALUES($1,'customer_service',$2,'deposit')`, room, real)
	for range 2 {
		exec(`SELECT ensure_user_customer_service_rooms($1)`, real)
		exec(`SELECT ensure_user_customer_service_rooms($1)`, virtual)
	}
	var rooms, members, virtualRooms int
	var preserved bool
	if e = tx.QueryRow(ctx, `SELECT count(*),bool_or(id=$2) FROM chat_rooms WHERE customer_user_id=$1`, real, room).Scan(&rooms, &preserved); e != nil {
		t.Fatal(e)
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM chat_room_members WHERE user_id=$1 AND member_role='owner'`, real).Scan(&members); e != nil {
		t.Fatal(e)
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM chat_rooms WHERE customer_user_id=$1`, virtual).Scan(&virtualRooms); e != nil {
		t.Fatal(e)
	}
	if rooms != 2 || members != 2 || !preserved || virtualRooms != 0 {
		t.Fatalf("rooms=%d members=%d preserved=%t virtual=%d", rooms, members, preserved, virtualRooms)
	}
}
