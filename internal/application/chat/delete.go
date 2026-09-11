package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrMessageNotFound = errors.New("chat message not found")
var ErrMessageDeleteDenied = errors.New("chat message deletion denied")
var ErrInvalidChatID = errors.New("invalid chat identifier")

// DeleteMessage preserves the original message and idempotency key for audit and retries.
func (service *Service) DeleteMessage(ctx context.Context, roomID, messageID, actorID string) error {
	for _, id := range []string{roomID, messageID} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidChatID
		}
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active, staff bool
	err = tx.QueryRow(ctx, `SELECT status='active',EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.code IN ('admin','operator')) FROM users u WHERE id=$1 FOR SHARE OF u`, actorID).Scan(&active, &staff)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMessageDeleteDenied
	}
	if err != nil {
		return err
	}
	if !active {
		return ErrMessageDeleteDenied
	}
	var roomType string
	var member bool
	err = tx.QueryRow(ctx, `SELECT room_type,EXISTS(SELECT 1 FROM chat_room_members WHERE room_id=$1 AND user_id=$2) FROM chat_rooms WHERE id=$1`, roomID, actorID).Scan(&roomType, &member)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoomNotFound
	}
	if err != nil {
		return err
	}
	if roomType != "global" && roomType != "game" && !member && !(staff && roomType == "customer_service") {
		return ErrRoomAccessDenied
	}
	var sender, status string
	err = tx.QueryRow(ctx, `SELECT COALESCE(sender_user_id::text,''),status FROM chat_messages WHERE id=$1 AND room_id=$2 FOR UPDATE`, messageID, roomID).Scan(&sender, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if !staff && sender != actorID {
		return ErrMessageDeleteDenied
	}
	if status == "deleted" {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE chat_messages SET status='deleted' WHERE id=$1`, messageID); err != nil {
		return err
	}
	userIDs := []string{}
	if roomType == "customer_service" || roomType == "direct" {
		rows, err := tx.Query(ctx, `SELECT user_id::text FROM chat_room_members WHERE room_id=$1 UNION SELECT ur.user_id::text FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE $2='customer_service' AND r.code IN ('admin','operator')`, roomID, roomType)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			userIDs = append(userIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	payload, err := json.Marshal(map[string]any{"room_id": roomID, "message_id": messageID, "status": "deleted", "user_ids": userIDs, "broadcast": roomType == "global" || roomType == "game"})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,occurred_at) VALUES($1,'chat_room',$2,$3,$4,now())`, uuid.NewString(), roomID, events.ChatMessageDeleted, payload); err != nil {
		return fmt.Errorf("enqueue chat deletion: %w", err)
	}
	audit, _ := json.Marshal(map[string]string{"room_id": roomID, "previous_status": status})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,'chat.message.delete','chat_message',$3,$4)`, uuid.NewString(), actorID, messageID, audit); err != nil {
		return fmt.Errorf("audit chat deletion: %w", err)
	}
	return tx.Commit(ctx)
}
