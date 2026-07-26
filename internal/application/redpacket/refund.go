package redpacket

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (service *Service) RefundExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := service.pool.Query(ctx, `
		SELECT id::text FROM red_packets
		WHERE status='active' AND expires_at <= $1
		ORDER BY expires_at LIMIT $2`, service.now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	refunded := 0
	for _, id := range ids {
		changed, err := service.refundOne(ctx, id)
		if err != nil {
			return refunded, err
		}
		if changed {
			refunded++
		}
	}
	return refunded, nil
}

func (service *Service) refundOne(ctx context.Context, packetID string) (bool, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var packet Packet
	var walletID string
	err = tx.QueryRow(ctx, `
		SELECT id::text,room_id::text,sender_user_id::text,client_request_id,currency,greeting,
			total_minor,remaining_minor,packet_count,claimed_count,status,expires_at,created_at,wallet_id::text
		FROM red_packets WHERE id=$1 FOR UPDATE`, packetID).
		Scan(&packet.ID, &packet.RoomID, &packet.SenderUserID, &packet.ClientRequestID, &packet.Currency,
			&packet.Greeting, &packet.TotalMinor, &packet.RemainingMinor, &packet.PacketCount,
			&packet.ClaimedCount, &packet.Status, &packet.ExpiresAt, &packet.CreatedAt, &walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if packet.Status != "active" || packet.ExpiresAt.After(service.now().UTC()) {
		return false, nil
	}
	var balance int64
	if err := tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1 FOR UPDATE`, walletID).Scan(&balance); err != nil {
		return false, err
	}
	if balance > math.MaxInt64-packet.RemainingMinor {
		return false, errors.New("red packet refund would overflow wallet")
	}
	balance += packet.RemainingMinor
	now := service.now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE red_packets SET status='refunded',remaining_minor=0 WHERE id=$1`, packet.ID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=$3 WHERE id=$1`, walletID, balance, now); err != nil {
		return false, err
	}
	if packet.RemainingMinor > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor)
			VALUES ($1,$2,'red_packet',$3,'red_packet_refund',$4,$5)`,
			uuid.NewString(), walletID, packet.ID, packet.RemainingMinor, balance); err != nil {
			return false, err
		}
	}
	broadcast, userIDs, err := roomAudience(ctx, tx, packet.RoomID, packet.SenderUserID)
	if errors.Is(err, ErrRoomAccessDenied) {
		broadcast, userIDs, err = false, []string{packet.SenderUserID}, nil
	}
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{
		"red_packet_id": packet.ID, "refund_minor": packet.RemainingMinor,
		"room_id": packet.RoomID, "broadcast": broadcast, "user_ids": userIDs,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (id,aggregate_type,aggregate_id,event_type,payload,occurred_at)
		VALUES ($1,'red_packet',$2,$3,$4,$5)`,
		uuid.NewString(), packet.ID, events.RedPacketRefunded, payload, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
