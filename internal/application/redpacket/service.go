package redpacket

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool   *pgxpool.Pool
	now    func() time.Time
	random func(max int64) (int64, error)
	ttl    time.Duration
}

func NewService(pool *pgxpool.Pool, ttl time.Duration) *Service {
	return &Service{pool: pool, now: time.Now, random: secureRandom, ttl: ttl}
}

func secureRandom(max int64) (int64, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return 0, err
	}
	return value.Int64(), nil
}

func (service *Service) Find(ctx context.Context, packetID, userID string) (Packet, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Packet{}, err
	}
	defer tx.Rollback(ctx)
	var packet Packet
	err = tx.QueryRow(ctx, `
		SELECT id::text,room_id::text,sender_user_id::text,client_request_id,currency,greeting,
			total_minor,remaining_minor,packet_count,claimed_count,status,expires_at,created_at
		FROM red_packets WHERE id=$1`, packetID).
		Scan(&packet.ID, &packet.RoomID, &packet.SenderUserID, &packet.ClientRequestID, &packet.Currency,
			&packet.Greeting, &packet.TotalMinor, &packet.RemainingMinor, &packet.PacketCount,
			&packet.ClaimedCount, &packet.Status, &packet.ExpiresAt, &packet.CreatedAt)
	if err != nil {
		return Packet{}, mapNotFound(err)
	}
	if _, _, err := roomAudience(ctx, tx, packet.RoomID, userID); err != nil {
		return Packet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Packet{}, err
	}
	return packet, nil
}
