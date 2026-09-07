package chat

import (
	"context"
	"encoding/json"
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
		INSERT INTO users (id,display_name,login_name,avatar_url) VALUES
		($1,'chat user',$3,'https://cdn.example/chat-user.png'),($2,'other user',$4,'')`,
		userID, otherUserID, "chat-"+userID, "chat-"+otherUserID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, userID, otherUserID)
	})
	staffIDs := []string{uuid.NewString(), uuid.NewString()}
	for i, role := range []string{"admin", "operator"} {
		id := staffIDs[i]
		if _, err := pool.Exec(ctx, `INSERT INTO users (id,display_name,login_name) VALUES ($1,'chat staff',$2)`, id, "chat-"+id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id) })
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id,role_id) SELECT $1,id FROM roles WHERE code=$2`, id, role); err != nil {
			t.Fatal(err)
		}
	}

	service := NewService(pool)
	rooms, err := service.OpenCustomerServiceRooms(ctx, userID)
	if err != nil {
		t.Fatalf("open rooms: %v", err)
	}
	room := rooms.Deposit
	if room.ID == rooms.Withdrawal.ID || room.ServiceType != ServiceTypeDeposit || rooms.Withdrawal.ServiceType != ServiceTypeWithdrawal {
		t.Fatalf("service rooms = %+v", rooms)
	}
	t.Cleanup(func() {
		for _, room := range []Room{rooms.Deposit, rooms.Withdrawal} {
			_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=$1`, room.ID)
			_, _ = pool.Exec(ctx, `DELETE FROM chat_messages WHERE room_id=$1`, room.ID)
			_, _ = pool.Exec(ctx, `DELETE FROM chat_rooms WHERE id=$1`, room.ID)
		}
	})
	sameRooms, err := service.OpenCustomerServiceRooms(ctx, userID)
	if err != nil || sameRooms.Deposit.ID != room.ID || sameRooms.Withdrawal.ID != rooms.Withdrawal.ID {
		t.Fatalf("idempotent rooms = %+v, err = %v", sameRooms, err)
	}
	listed, err := service.ListRooms(ctx, userID, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed {
		if item.ID == room.ID {
			found = true
			if item.CustomerUserID == nil || *item.CustomerUserID < 100000 || item.CustomerDisplayName == nil || *item.CustomerDisplayName != "chat user" || item.CustomerInvitationCode == nil {
				t.Fatalf("customer identity: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("own customer room missing")
	}
	otherRooms, err := service.ListRooms(ctx, otherUserID, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range otherRooms {
		if item.ID == room.ID {
			t.Fatal("other player's customer room leaked")
		}
	}
	first, created, err := service.SendMessage(ctx, room.ID, userID, "request-1", "hello", false)
	if err != nil || !created {
		t.Fatalf("send message = %+v/%v/%v", first, created, err)
	}
	if first.Sender == nil || first.Sender.UserID < 100000 || first.Sender.DisplayName != "chat user" || first.Sender.AvatarURL != "https://cdn.example/chat-user.png" {
		t.Fatalf("message sender = %+v", first.Sender)
	}
	duplicate, created, err := service.SendMessage(ctx, room.ID, userID, "request-1", "changed body", false)
	if err != nil || created || duplicate.ID != first.ID || duplicate.Body != "hello" {
		t.Fatalf("duplicate = %+v/%v/%v", duplicate, created, err)
	}
	if duplicate.Sender == nil || duplicate.Sender.UserID != first.Sender.UserID {
		t.Fatalf("duplicate sender = %+v", duplicate.Sender)
	}
	messages, err := service.ListMessages(ctx, room.ID, userID, false, 10)
	if err != nil || len(messages) != 1 || messages[0].Sender == nil || messages[0].Sender.UserID != first.Sender.UserID {
		t.Fatalf("messages = %+v, err = %v", messages, err)
	}
	if _, err := service.ListMessages(ctx, room.ID, otherUserID, false, 10); !errors.Is(err, ErrRoomAccessDenied) {
		t.Fatalf("other user error = %v", err)
	}
	var eventCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type=$2`, room.ID, events.ChatMessageCreated).
		Scan(&eventCount)
	if err != nil || eventCount != 1 {
		t.Fatalf("event count = %d, err = %v", eventCount, err)
	}
	var eventPayload string
	err = pool.QueryRow(ctx, `SELECT payload::text FROM outbox_events WHERE aggregate_id=$1 AND event_type=$2`, room.ID, events.ChatMessageCreated).
		Scan(&eventPayload)
	if err != nil {
		t.Fatalf("event payload: %v", err)
	}
	if strings.Contains(eventPayload, "sender_user_id") || !strings.Contains(eventPayload, `"display_name": "chat user"`) {
		t.Fatalf("event payload = %s", eventPayload)
	}
	assertTargets := func(messageID string) {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE aggregate_id=$1 AND payload->'message'->>'id'=$2`, room.ID, messageID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var payload struct {
			UserIDs   []string `json:"user_ids"`
			Broadcast bool     `json:"broadcast"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, id := range payload.UserIDs {
			counts[id]++
		}
		if payload.Broadcast || counts[otherUserID] != 0 {
			t.Fatal("customer message leaked", payload)
		}
		for _, id := range append([]string{userID}, staffIDs...) {
			if counts[id] != 1 {
				t.Fatalf("target %s count = %d", id, counts[id])
			}
		}
	}
	assertTargets(first.ID)
	// A staff member who is also a room member still receives only one event.
	if _, err := pool.Exec(ctx, `INSERT INTO chat_room_members (room_id,user_id,member_role) VALUES ($1,$2,'owner')`, room.ID, staffIDs[0]); err != nil {
		t.Fatal(err)
	}
	reply, created, err := service.SendMessage(ctx, room.ID, staffIDs[0], "staff-reply", "reply", true)
	if err != nil || !created {
		t.Fatalf("staff reply: %v", err)
	}
	assertTargets(reply.ID)
	if _, _, err := service.SendMessage(ctx, room.ID, otherUserID, "unauthorized", "hello", false); !errors.Is(err, ErrRoomAccessDenied) {
		t.Fatalf("unauthorized send: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET chat_muted=true WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.SendMessage(ctx, room.ID, userID, "muted-message", "not sent", false); !errors.Is(err, ErrChatMuted) {
		t.Fatal("mute not enforced", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET chat_muted=false WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.SendMessage(ctx, room.ID, userID, "unmuted-message", "sent", false); err != nil {
		t.Fatal("unmute failed", err)
	}
}
