package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRoomNotFound = errors.New("chat room not found")
var ErrRoomAccessDenied = errors.New("chat room access denied")
var ErrInvalidMessage = errors.New("message must contain 1-2000 characters")
var ErrInvalidRequestID = errors.New("client_request_id is required")

type Room struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID              string    `json:"id"`
	RoomID          string    `json:"room_id"`
	SenderUserID    *string   `json:"sender_user_id,omitempty"`
	Body            string    `json:"body"`
	Status          string    `json:"status"`
	ClientRequestID *string   `json:"client_request_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) OpenCustomerServiceRoom(ctx context.Context, userID string) (Room, error) {
	var room Room
	err := service.pool.QueryRow(ctx, `
		SELECT r.id::text,r.room_type,r.created_at
		FROM chat_rooms r JOIN chat_room_members m ON m.room_id=r.id
		WHERE r.room_type='customer_service' AND m.user_id=$1
		ORDER BY r.created_at DESC LIMIT 1`, userID).Scan(&room.ID, &room.Type, &room.CreatedAt)
	if err == nil {
		return room, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Room{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Room{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "customer-service:"+userID); err != nil {
		return Room{}, err
	}
	err = tx.QueryRow(ctx, `
		SELECT r.id::text,r.room_type,r.created_at
		FROM chat_rooms r JOIN chat_room_members m ON m.room_id=r.id
		WHERE r.room_type='customer_service' AND m.user_id=$1
		ORDER BY r.created_at DESC LIMIT 1`, userID).Scan(&room.ID, &room.Type, &room.CreatedAt)
	if err == nil {
		return room, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Room{}, err
	}
	room.ID = uuid.NewString()
	room.Type = "customer_service"
	err = tx.QueryRow(ctx, `
		INSERT INTO chat_rooms (id,room_type) VALUES ($1,'customer_service')
		RETURNING created_at`, room.ID).Scan(&room.CreatedAt)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO chat_room_members (room_id,user_id,member_role) VALUES ($1,$2,'owner')`, room.ID, userID)
	}
	if err != nil {
		return Room{}, err
	}
	return room, tx.Commit(ctx)
}

func (service *Service) ListRooms(ctx context.Context, userID string, staff bool, limit int) ([]Room, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT DISTINCT r.id::text,r.room_type,r.created_at
		FROM chat_rooms r
		LEFT JOIN chat_room_members m ON m.room_id=r.id AND m.user_id=$1
		WHERE r.room_type IN ('global','game') OR m.user_id IS NOT NULL OR ($2 AND r.room_type='customer_service')
		ORDER BY r.created_at DESC LIMIT $3`, userID, staff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Room, 0)
	for rows.Next() {
		var item Room
		if err := rows.Scan(&item.ID, &item.Type, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) ListMessages(ctx context.Context, roomID, userID string, staff bool, limit int) ([]Message, error) {
	if err := service.authorize(ctx, roomID, userID, staff); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT id::text,room_id::text,sender_user_id::text,body,status,client_request_id,created_at
		FROM chat_messages WHERE room_id=$1 AND status='visible'
		ORDER BY created_at DESC,id DESC LIMIT $2`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Message, 0)
	for rows.Next() {
		var item Message
		if err := rows.Scan(&item.ID, &item.RoomID, &item.SenderUserID, &item.Body, &item.Status, &item.ClientRequestID, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) SendMessage(ctx context.Context, roomID, senderUserID, clientRequestID, body string, staff bool) (Message, bool, error) {
	body = strings.TrimSpace(body)
	clientRequestID = strings.TrimSpace(clientRequestID)
	if body == "" || len([]rune(body)) > 2000 {
		return Message{}, false, ErrInvalidMessage
	}
	if clientRequestID == "" || len(clientRequestID) > 128 {
		return Message{}, false, ErrInvalidRequestID
	}
	if err := service.authorize(ctx, roomID, senderUserID, staff); err != nil {
		return Message{}, false, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback(ctx)
	messageID := uuid.NewString()
	var item Message
	err = tx.QueryRow(ctx, `
		INSERT INTO chat_messages (id,room_id,sender_user_id,body,client_request_id)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (room_id,sender_user_id,client_request_id) WHERE sender_user_id IS NOT NULL AND client_request_id IS NOT NULL
		DO UPDATE SET client_request_id=EXCLUDED.client_request_id
		RETURNING id::text,room_id::text,sender_user_id::text,body,status,client_request_id,created_at`,
		messageID, roomID, senderUserID, body, clientRequestID).
		Scan(&item.ID, &item.RoomID, &item.SenderUserID, &item.Body, &item.Status, &item.ClientRequestID, &item.CreatedAt)
	if err != nil {
		return Message{}, false, err
	}
	created := item.ID == messageID
	if created {
		var roomType string
		if err := tx.QueryRow(ctx, `SELECT room_type FROM chat_rooms WHERE id=$1`, roomID).Scan(&roomType); err != nil {
			return Message{}, false, err
		}
		userIDs := []string{}
		if roomType == "customer_service" || roomType == "direct" {
			rows, err := tx.Query(ctx, `SELECT user_id::text FROM chat_room_members WHERE room_id=$1`, roomID)
			if err != nil {
				return Message{}, false, err
			}
			for rows.Next() {
				var userID string
				if err := rows.Scan(&userID); err != nil {
					rows.Close()
					return Message{}, false, err
				}
				userIDs = append(userIDs, userID)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return Message{}, false, err
			}
			rows.Close()
		}
		payload, _ := json.Marshal(map[string]any{
			"room_id": roomID, "message": item, "user_ids": userIDs,
			"broadcast": roomType == "global" || roomType == "game",
		})
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events (id,aggregate_type,aggregate_id,event_type,payload,occurred_at)
			VALUES ($1,'chat_room',$2,$3,$4,$5)`,
			uuid.NewString(), roomID, events.ChatMessageCreated, payload, item.CreatedAt)
		if err != nil {
			return Message{}, false, err
		}
	}
	return item, created, tx.Commit(ctx)
}

func (service *Service) authorize(ctx context.Context, roomID, userID string, staff bool) error {
	var allowed bool
	err := service.pool.QueryRow(ctx, `
		SELECT CASE
			WHEN room_type IN ('global','game') THEN true
			WHEN $3 THEN true
			ELSE EXISTS(SELECT 1 FROM chat_room_members WHERE room_id=$1 AND user_id=$2)
		END
		FROM chat_rooms WHERE id=$1`, roomID, userID, staff).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoomNotFound
	}
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRoomAccessDenied
	}
	return nil
}
