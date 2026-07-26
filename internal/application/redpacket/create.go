package redpacket

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (service *Service) Create(ctx context.Context, input CreateInput) (Packet, bool, error) {
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Greeting = strings.TrimSpace(input.Greeting)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.SenderUserID == "" || input.RoomID == "" || input.ClientRequestID == "" || len(input.ClientRequestID) > 128 ||
		(input.Currency != "USDT" && input.Currency != "POINTS") || input.TotalMinor <= 0 ||
		input.PacketCount <= 0 || input.PacketCount > 100 || input.TotalMinor < int64(input.PacketCount) ||
		len([]rune(input.Greeting)) > 100 || service.ttl <= 0 {
		return Packet{}, false, ErrInvalidPacket
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Packet{}, false, err
	}
	defer tx.Rollback(ctx)
	existing, err := findByRequest(ctx, tx, input.SenderUserID, input.ClientRequestID)
	if err == nil {
		return existing, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Packet{}, false, err
	}
	broadcast, userIDs, err := roomAudience(ctx, tx, input.RoomID, input.SenderUserID)
	if err != nil {
		return Packet{}, false, err
	}
	var walletID string
	var balance int64
	err = tx.QueryRow(ctx, `
		SELECT id::text,available_minor FROM wallets
		WHERE user_id=$1 AND currency=$2 FOR UPDATE`,
		input.SenderUserID, input.Currency).Scan(&walletID, &balance)
	if err != nil {
		return Packet{}, false, err
	}
	if balance < input.TotalMinor {
		return Packet{}, false, ErrInsufficientBalance
	}
	balance -= input.TotalMinor
	now := service.now().UTC()
	packetID := uuid.NewString()
	var packet Packet
	err = tx.QueryRow(ctx, `
		INSERT INTO red_packets (
			id,room_id,sender_user_id,wallet_id,client_request_id,currency,greeting,total_minor,
			remaining_minor,packet_count,expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10)
		RETURNING id::text,room_id::text,sender_user_id::text,client_request_id,currency,greeting,
			total_minor,remaining_minor,packet_count,claimed_count,status,expires_at,created_at`,
		packetID, input.RoomID, input.SenderUserID, walletID, input.ClientRequestID,
		input.Currency, input.Greeting, input.TotalMinor, input.PacketCount, now.Add(service.ttl)).
		Scan(&packet.ID, &packet.RoomID, &packet.SenderUserID, &packet.ClientRequestID, &packet.Currency,
			&packet.Greeting, &packet.TotalMinor, &packet.RemainingMinor, &packet.PacketCount,
			&packet.ClaimedCount, &packet.Status, &packet.ExpiresAt, &packet.CreatedAt)
	if err != nil {
		return Packet{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=$3 WHERE id=$1`, walletID, balance, now); err != nil {
		return Packet{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor)
		VALUES ($1,$2,'red_packet',$3,'red_packet_fund',$4,$5)`,
		uuid.NewString(), walletID, packetID, -input.TotalMinor, balance); err != nil {
		return Packet{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{
		"red_packet": packet, "room_id": input.RoomID, "broadcast": broadcast, "user_ids": userIDs,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (id,aggregate_type,aggregate_id,event_type,payload,occurred_at)
		VALUES ($1,'red_packet',$2,$3,$4,$5)`,
		uuid.NewString(), packetID, events.RedPacketCreated, payload, now); err != nil {
		return Packet{}, false, err
	}
	return packet, true, tx.Commit(ctx)
}

func findByRequest(ctx context.Context, tx pgx.Tx, userID, requestID string) (Packet, error) {
	var packet Packet
	err := tx.QueryRow(ctx, `
		SELECT id::text,room_id::text,sender_user_id::text,client_request_id,currency,greeting,
			total_minor,remaining_minor,packet_count,claimed_count,status,expires_at,created_at
		FROM red_packets WHERE sender_user_id=$1 AND client_request_id=$2`,
		userID, requestID).
		Scan(&packet.ID, &packet.RoomID, &packet.SenderUserID, &packet.ClientRequestID, &packet.Currency,
			&packet.Greeting, &packet.TotalMinor, &packet.RemainingMinor, &packet.PacketCount,
			&packet.ClaimedCount, &packet.Status, &packet.ExpiresAt, &packet.CreatedAt)
	return packet, err
}

func roomAudience(ctx context.Context, tx pgx.Tx, roomID, userID string) (bool, []string, error) {
	var roomType string
	err := tx.QueryRow(ctx, `SELECT room_type FROM chat_rooms WHERE id=$1`, roomID).Scan(&roomType)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, ErrRoomAccessDenied
	}
	if err != nil {
		return false, nil, err
	}
	if roomType == "global" || roomType == "game" {
		return true, nil, nil
	}
	var member bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat_room_members WHERE room_id=$1 AND user_id=$2)`, roomID, userID).Scan(&member)
	if err != nil {
		return false, nil, err
	}
	if !member {
		return false, nil, ErrRoomAccessDenied
	}
	rows, err := tx.Query(ctx, `SELECT user_id::text FROM chat_room_members WHERE room_id=$1`, roomID)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	userIDs := make([]string, 0)
	for rows.Next() {
		var memberID string
		if err := rows.Scan(&memberID); err != nil {
			return false, nil, err
		}
		userIDs = append(userIDs, memberID)
	}
	return false, userIDs, rows.Err()
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPacketNotFound
	}
	return err
}
