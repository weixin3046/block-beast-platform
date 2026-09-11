package chat

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeleteMessageInvalidIDs(t *testing.T) {
	s := &Service{}
	for _, ids := range [][2]string{{"bad", uuid.NewString()}, {uuid.NewString(), "bad"}} {
		if err := s.DeleteMessage(context.Background(), ids[0], ids[1], uuid.NewString()); !errors.Is(err, ErrInvalidChatID) {
			t.Fatal(err)
		}
	}
}

func TestDeleteMessagePermissionsAndIdempotency(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s := NewService(p)
	owner, other, admin, operator := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{owner, other, admin, operator} {
		if _, err = p.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'delete test')`, id); err != nil {
			t.Fatal(err)
		}
	}
	for role, id := range map[string]string{"admin": admin, "operator": operator} {
		if _, err = p.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=$2`, id, role); err != nil {
			t.Fatal(err)
		}
	}
	rooms, err := s.OpenCustomerServiceRooms(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	room := rooms.Deposit.ID
	m, _, err := s.SendMessage(ctx, room, owner, "delete-me", "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteMessage(ctx, room, m.ID, other); !errors.Is(err, ErrRoomAccessDenied) {
		t.Fatalf("outsider: %v", err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO chat_room_members(room_id,user_id,member_role) VALUES($1,$2,'owner')`, room, other); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteMessage(ctx, room, m.ID, other); !errors.Is(err, ErrMessageDeleteDenied) {
		t.Fatalf("foreign message: %v", err)
	}
	if err = s.DeleteMessage(ctx, rooms.Withdrawal.ID, m.ID, owner); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("wrong room: %v", err)
	}
	if err = s.DeleteMessage(ctx, room, uuid.NewString(), owner); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("missing: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.DeleteMessage(ctx, room, m.ID, owner) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	for _, query := range []string{`SELECT count(*) FROM outbox_events WHERE event_type='chat.message.deleted' AND payload->>'message_id'=$1`, `SELECT count(*) FROM audit_logs WHERE action='chat.message.delete' AND target_id=$1`} {
		if err = p.QueryRow(ctx, query, m.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("count %d: %v", count, err)
		}
	}
	history, err := s.ListMessages(ctx, room, owner, false, 50)
	if err != nil || len(history) != 0 {
		t.Fatalf("history %v: %v", history, err)
	}
	replay, fresh, err := s.SendMessage(ctx, room, owner, "delete-me", "hello", false)
	if err != nil || fresh || replay.Status != "deleted" {
		t.Fatalf("replay %+v %v", replay, err)
	}
	for _, id := range []string{admin, operator} {
		msg, _, err := s.SendMessage(ctx, room, owner, uuid.NewString(), "moderate", false)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteMessage(ctx, room, msg.ID, id); err != nil {
			t.Fatalf("staff: %v", err)
		}
	}

	// Staff privileges do not grant access to unrelated private conversations.
	direct := uuid.NewString()
	if _, err = p.Exec(ctx, `INSERT INTO chat_rooms(id,room_type) VALUES($1,'direct')`, direct); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteMessage(ctx, direct, uuid.NewString(), admin); !errors.Is(err, ErrRoomAccessDenied) {
		t.Fatalf("private staff access: %v", err)
	}
	var targeted bool
	if err = p.QueryRow(ctx, `SELECT (payload->'user_ids') ? $2 AND (payload->'user_ids') ? $3 AND (payload->'user_ids') ? $4 AND (payload->>'broadcast')::boolean=false FROM outbox_events WHERE event_type='chat.message.deleted' AND payload->>'message_id'=$1`, m.ID, owner, admin, operator).Scan(&targeted); err != nil || !targeted {
		t.Fatalf("deletion targets: %v %v", targeted, err)
	}
	if _, err = p.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteMessage(ctx, room, m.ID, other); !errors.Is(err, ErrMessageDeleteDenied) {
		t.Fatalf("inactive: %v", err)
	}
}
