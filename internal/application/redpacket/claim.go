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

func (service *Service) Claim(ctx context.Context, packetID, userID string) (Claim, bool, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Claim{}, false, err
	}
	defer tx.Rollback(ctx)
	existing, err := findClaim(ctx, tx, packetID, userID)
	if err == nil {
		return existing, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Claim{}, false, err
	}
	var packet Packet
	err = tx.QueryRow(ctx, `
		SELECT id::text,room_id::text,sender_user_id::text,client_request_id,currency,greeting,
			total_minor,remaining_minor,packet_count,claimed_count,status,expires_at,created_at
		FROM red_packets WHERE id=$1 FOR UPDATE`, packetID).
		Scan(&packet.ID, &packet.RoomID, &packet.SenderUserID, &packet.ClientRequestID, &packet.Currency,
			&packet.Greeting, &packet.TotalMinor, &packet.RemainingMinor, &packet.PacketCount,
			&packet.ClaimedCount, &packet.Status, &packet.ExpiresAt, &packet.CreatedAt)
	if err != nil {
		return Claim{}, false, mapNotFound(err)
	}
	if packet.SenderUserID == userID {
		return Claim{}, false, ErrSenderCannotClaim
	}
	if packet.Status != "active" || !packet.ExpiresAt.After(service.now().UTC()) ||
		packet.ClaimedCount >= packet.PacketCount || packet.RemainingMinor <= 0 {
		return Claim{}, false, ErrPacketUnavailable
	}
	broadcast, userIDs, err := roomAudience(ctx, tx, packet.RoomID, userID)
	if err != nil {
		return Claim{}, false, err
	}
	remainingSlots := int64(packet.PacketCount - packet.ClaimedCount)
	amount, err := service.claimAmount(packet.RemainingMinor, remainingSlots)
	if err != nil {
		return Claim{}, false, err
	}
	var walletID string
	var balance int64
	err = tx.QueryRow(ctx, `
		SELECT id::text,available_minor FROM wallets
		WHERE user_id=$1 AND currency=$2 FOR UPDATE`, userID, packet.Currency).Scan(&walletID, &balance)
	if err != nil {
		return Claim{}, false, err
	}
	if balance > math.MaxInt64-amount {
		return Claim{}, false, errors.New("red packet claim would overflow wallet")
	}
	balance += amount
	now := service.now().UTC()
	claim := Claim{
		ID: uuid.NewString(), RedPacketID: packet.ID, UserID: userID,
		Currency: packet.Currency, AmountMinor: amount, ClaimedAt: now,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO red_packet_claims (id,red_packet_id,user_id,wallet_id,amount_minor,claimed_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		claim.ID, claim.RedPacketID, claim.UserID, walletID, claim.AmountMinor, claim.ClaimedAt)
	if err != nil {
		return Claim{}, false, err
	}
	packet.RemainingMinor -= amount
	packet.ClaimedCount++
	if packet.ClaimedCount == packet.PacketCount || packet.RemainingMinor == 0 {
		packet.Status = "exhausted"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE red_packets SET remaining_minor=$2,claimed_count=$3,status=$4 WHERE id=$1`,
		packet.ID, packet.RemainingMinor, packet.ClaimedCount, packet.Status); err != nil {
		return Claim{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=$3 WHERE id=$1`, walletID, balance, now); err != nil {
		return Claim{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor)
		VALUES ($1,$2,'red_packet',$3,'red_packet_claim',$4,$5)`,
		uuid.NewString(), walletID, claim.ID, amount, balance); err != nil {
		return Claim{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{
		"claim": claim, "red_packet_id": packet.ID, "room_id": packet.RoomID,
		"broadcast": broadcast, "user_ids": userIDs,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (id,aggregate_type,aggregate_id,event_type,payload,occurred_at)
		VALUES ($1,'red_packet',$2,$3,$4,$5)`,
		uuid.NewString(), packet.ID, events.RedPacketClaimed, payload, now); err != nil {
		return Claim{}, false, err
	}
	return claim, true, tx.Commit(ctx)
}

func (service *Service) claimAmount(remaining, slots int64) (int64, error) {
	if slots <= 1 {
		return remaining, nil
	}
	maximum := remaining - (slots - 1)
	doubleAverage := 2 * (remaining / slots)
	if doubleAverage > 0 && doubleAverage < maximum {
		maximum = doubleAverage
	}
	random, err := service.random(maximum)
	if err != nil {
		return 0, err
	}
	return random + 1, nil
}

func findClaim(ctx context.Context, tx pgx.Tx, packetID, userID string) (Claim, error) {
	var claim Claim
	err := tx.QueryRow(ctx, `
		SELECT c.id::text,c.red_packet_id::text,c.user_id::text,p.currency,c.amount_minor,c.claimed_at
		FROM red_packet_claims c JOIN red_packets p ON p.id=c.red_packet_id
		WHERE c.red_packet_id=$1 AND c.user_id=$2`, packetID, userID).
		Scan(&claim.ID, &claim.RedPacketID, &claim.UserID, &claim.Currency, &claim.AmountMinor, &claim.ClaimedAt)
	return claim, err
}
