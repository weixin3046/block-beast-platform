package chat

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSendMessageValidatesBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	if _, _, err := service.SendMessage(context.Background(), "room", "user", "request", "   ", false); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("empty body error = %v", err)
	}
	if _, _, err := service.SendMessage(context.Background(), "room", "user", "", "hello", false); !errors.Is(err, ErrInvalidRequestID) {
		t.Fatalf("empty request ID error = %v", err)
	}
	if _, _, err := service.SendMessage(context.Background(), "room", "user", "request", strings.Repeat("界", 2001), false); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("long body error = %v", err)
	}
}

func TestCustomerServiceMessagePersistenceAndIdempotency(t *testing.T) {
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
	userID := uuid.NewString()
	otherUserID := uuid.NewString()
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id,display_name,login_name) VALUES
		($1,'chat user',$3),($2,'other user',$4)`,
		userID, otherUserID, "chat-"+userID, "chat-"+otherUserID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, userID, otherUserID)
	})

	service := NewService(pool)
	room, err := service.OpenCustomerServiceRoom(ctx, userID)
	if err != nil {
		t.Fatalf("open room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=$1`, room.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat_messages WHERE room_id=$1`, room.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat_rooms WHERE id=$1`, room.ID)
	})
	sameRoom, err := service.OpenCustomerServiceRoom(ctx, userID)
	if err != nil || sameRoom.ID != room.ID {
		t.Fatalf("idempotent room = %+v, err = %v", sameRoom, err)
	}
	first, created, err := service.SendMessage(ctx, room.ID, userID, "request-1", "hello", false)
	if err != nil || !created {
		t.Fatalf("send message = %+v/%v/%v", first, created, err)
	}
	duplicate, created, err := service.SendMessage(ctx, room.ID, userID, "request-1", "changed body", false)
	if err != nil || created || duplicate.ID != first.ID || duplicate.Body != "hello" {
		t.Fatalf("duplicate = %+v/%v/%v", duplicate, created, err)
	}
	if _, err := service.ListMessages(ctx, room.ID, otherUserID, false, 10); !errors.Is(err, ErrRoomAccessDenied) {
		t.Fatalf("other user error = %v", err)
	}
	var eventCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type=$2`, room.ID, events.ChatMessageCreated).Scan(&eventCount)
	if err != nil || eventCount != 1 {
		t.Fatalf("event count = %d, err = %v", eventCount, err)
	}
}
