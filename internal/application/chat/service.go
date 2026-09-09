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
var ErrChatMuted = errors.New("账号已被禁言")
var ErrInvalidMessage = errors.New("message must contain 1-2000 characters")
var ErrInvalidRequestID = errors.New("client_request_id is required")

const (
	ServiceTypeDeposit    = "deposit"
	ServiceTypeWithdrawal = "withdrawal"
)

type Room struct {
	CustomerUserID         *int64    `json:"customer_user_id,omitempty"`
	CustomerDisplayName    *string   `json:"customer_display_name,omitempty"`
	CustomerInvitationCode *int64    `json:"customer_invitation_code,omitempty"`
	ID                     string    `json:"id"`
	Type                   string    `json:"type"`
	ServiceType            string    `json:"service_type,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

type CustomerServiceRooms struct {
	Deposit    Room `json:"deposit"`
	Withdrawal Room `json:"withdrawal"`
}

type MessageSender struct {
	IsStaff     bool   `json:"is_staff"`
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type Message struct {
	ImageUploadID   string         `json:"image_upload_id,omitempty"`
	ImageURL        string         `json:"image_url,omitempty"`
	ID              string         `json:"id"`
	RoomID          string         `json:"room_id"`
	Sender          *MessageSender `json:"sender,omitempty"`
	Body            string         `json:"body"`
	Status          string         `json:"status"`
	ClientRequestID *string        `json:"client_request_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) OpenCustomerServiceRooms(ctx context.Context, userID string) (CustomerServiceRooms, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return CustomerServiceRooms{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "customer-service:"+userID); err != nil {
		return CustomerServiceRooms{}, err
	}

	rooms := CustomerServiceRooms{}
	for _, serviceType := range []string{ServiceTypeDeposit, ServiceTypeWithdrawal} {
		var room Room
		err := tx.QueryRow(ctx, `
			SELECT id::text,room_type,service_type,created_at
			FROM chat_rooms
			WHERE room_type='customer_service' AND customer_user_id=$1 AND service_type=$2`, userID, serviceType).
			Scan(&room.ID, &room.Type, &room.ServiceType, &room.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			room = Room{ID: uuid.NewString(), Type: "customer_service", ServiceType: serviceType}
			err = tx.QueryRow(ctx, `
				INSERT INTO chat_rooms (id,room_type,customer_user_id,service_type)
				VALUES ($1,'customer_service',$2,$3)
				RETURNING created_at`, room.ID, userID, serviceType).Scan(&room.CreatedAt)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO chat_room_members (room_id,user_id,member_role) VALUES ($1,$2,'owner')`, room.ID, userID)
			}
		}
		if err != nil {
			return CustomerServiceRooms{}, err
		}
		if serviceType == ServiceTypeDeposit {
			rooms.Deposit = room
		} else {
			rooms.Withdrawal = room
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomerServiceRooms{}, err
	}
	return rooms, nil
}

func (service *Service) ListRooms(ctx context.Context, userID string, staff bool, limit int) ([]Room, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT DISTINCT r.id::text,r.room_type,COALESCE(r.service_type,''),r.created_at,u.public_id,u.display_name,u.invitation_code
		FROM chat_rooms r
		LEFT JOIN users u ON u.id=r.customer_user_id AND r.room_type='customer_service'
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
		if err := rows.Scan(&item.ID, &item.Type, &item.ServiceType, &item.CreatedAt, &item.CustomerUserID, &item.CustomerDisplayName, &item.CustomerInvitationCode); err != nil {
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
		SELECT m.id::text,m.room_id::text,m.body,m.status,m.client_request_id,m.created_at,m.sender_is_staff,COALESCE(m.image_upload_id::text,''),
			u.public_id,u.display_name,
			CASE WHEN u.avatar_url LIKE 'uploads/%'
				THEN '/v1/avatars/' || u.public_id::text || '?v=' || regexp_replace(u.avatar_url, '^.*/', '')
				ELSE COALESCE(u.avatar_url,'') END
		FROM chat_messages m
		LEFT JOIN users u ON u.id=m.sender_user_id
		WHERE m.room_id=$1 AND m.status='visible'
		ORDER BY m.created_at DESC,m.id DESC LIMIT $2`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Message, 0)
	for rows.Next() {
		var item Message
		var senderIsStaff bool
		var senderUserID *int64
		var senderDisplayName, senderAvatarURL *string
		if err := rows.Scan(&item.ID, &item.RoomID, &item.Body, &item.Status, &item.ClientRequestID, &item.CreatedAt, &senderIsStaff, &item.ImageUploadID,
			&senderUserID, &senderDisplayName, &senderAvatarURL); err != nil {
			return nil, err
		}
		if item.ImageUploadID != "" {
			item.ImageURL = "/v1/uploads/" + item.ImageUploadID + "/content"
		}
		item.Sender = newMessageSender(senderUserID, senderDisplayName, senderAvatarURL, senderIsStaff)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) SendMessage(ctx context.Context, roomID, senderUserID, clientRequestID, body string, staff bool, imageIDs ...string) (Message, bool, error) {
	imageID := ""
	if len(imageIDs) > 1 {
		return Message{}, false, ErrInvalidMessage
	}
	if len(imageIDs) == 1 {
		imageID = imageIDs[0]
	}
	if imageID != "" {
		if _, err := uuid.Parse(imageID); err != nil {
			return Message{}, false, ErrInvalidMessage
		}
	}
	body = strings.TrimSpace(body)
	clientRequestID = strings.TrimSpace(clientRequestID)
	if (body == "" && imageID == "") || len([]rune(body)) > 2000 {
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
	var muted bool
	if err := tx.QueryRow(ctx, `SELECT chat_muted FROM users WHERE id=$1 FOR SHARE`, senderUserID).Scan(&muted); err != nil {
		return Message{}, false, err
	}
	if muted {
		return Message{}, false, ErrChatMuted
	}
	if imageID != "" {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM uploads WHERE id=$1 AND owner_user_id=$2 AND status='confirmed' AND content_type IN ('image/jpeg','image/png','image/webp'))`, imageID, senderUserID).Scan(&allowed); err != nil {
			return Message{}, false, err
		}
		if !allowed {
			return Message{}, false, ErrInvalidMessage
		}
	}
	messageID := uuid.NewString()
	var item Message
	var senderIsStaff bool
	var senderPublicID *int64
	var senderDisplayName, senderAvatarURL *string
	err = tx.QueryRow(ctx, `
		WITH saved AS (
			INSERT INTO chat_messages (id,room_id,sender_user_id,body,client_request_id,sender_is_staff,image_upload_id)
			VALUES ($1,$2,$3,$4,$5,EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$3 AND r.code IN ('admin','operator')),NULLIF($6,'')::uuid)
			ON CONFLICT (room_id,sender_user_id,client_request_id) WHERE sender_user_id IS NOT NULL AND client_request_id IS NOT NULL
			DO UPDATE SET client_request_id=EXCLUDED.client_request_id
			RETURNING id,room_id,sender_user_id,body,status,client_request_id,created_at,sender_is_staff,image_upload_id
		)
		SELECT saved.id::text,saved.room_id::text,saved.body,saved.status,saved.client_request_id,saved.created_at,saved.sender_is_staff,COALESCE(saved.image_upload_id::text,''),
			u.public_id,u.display_name,
			CASE WHEN u.avatar_url LIKE 'uploads/%'
				THEN '/v1/avatars/' || u.public_id::text || '?v=' || regexp_replace(u.avatar_url, '^.*/', '')
				ELSE COALESCE(u.avatar_url,'') END
		FROM saved
		LEFT JOIN users u ON u.id=saved.sender_user_id`,
		messageID, roomID, senderUserID, body, clientRequestID, imageID).
		Scan(&item.ID, &item.RoomID, &item.Body, &item.Status, &item.ClientRequestID, &item.CreatedAt, &senderIsStaff, &item.ImageUploadID,
			&senderPublicID, &senderDisplayName, &senderAvatarURL)
	if err != nil {
		return Message{}, false, err
	}
	if item.ImageUploadID != "" {
		item.ImageURL = "/v1/uploads/" + item.ImageUploadID + "/content"
	}
	item.Sender = newMessageSender(senderPublicID, senderDisplayName, senderAvatarURL, senderIsStaff)
	created := item.ID == messageID
	if created {
		var roomType string
		if err := tx.QueryRow(ctx, `SELECT room_type FROM chat_rooms WHERE id=$1`, roomID).Scan(&roomType); err != nil {
			return Message{}, false, err
		}
		userIDs := []string{}
		if roomType == "customer_service" || roomType == "direct" {
			// Staff can access customer-service rooms without joining them. UNION
			// deduplicates staff who are also members or hold both staff roles.
			rows, err := tx.Query(ctx, `
				SELECT user_id::text FROM chat_room_members WHERE room_id=$1
				UNION
				SELECT ur.user_id::text FROM user_roles ur
				JOIN roles r ON r.id=ur.role_id
				WHERE $2='customer_service' AND r.code IN ('admin','operator')`, roomID, roomType)
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

func newMessageSender(userID *int64, displayName, avatarURL *string, isStaff bool) *MessageSender {
	if userID == nil {
		return nil
	}
	sender := &MessageSender{UserID: *userID, IsStaff: isStaff}
	if displayName != nil {
		sender.DisplayName = *displayName
	}
	if avatarURL != nil {
		sender.AvatarURL = *avatarURL
	}
	return sender
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
