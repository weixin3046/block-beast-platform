package redpacket

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClaimAmountPreservesOneUnitPerRemainingPacket(t *testing.T) {
	service := &Service{random: func(max int64) (int64, error) {
		if max < 1 {
			t.Fatalf("random maximum = %d", max)
		}
		return max - 1, nil
	}}
	amount, err := service.claimAmount(10, 3)
	if err != nil || amount > 8 || amount < 1 {
		t.Fatalf("amount = %d, err = %v", amount, err)
	}
	last, err := service.claimAmount(7, 1)
	if err != nil || last != 7 {
		t.Fatalf("last amount = %d, err = %v", last, err)
	}
}

func TestRedPacketLifecycle(t *testing.T) {
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
	sender, claimant1, claimant2 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	senderWallet, wallet1, wallet2 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = pool.Exec(ctx, `
		INSERT INTO users(id,display_name,login_name) VALUES
			($1,'sender',$4),($2,'claimant 1',$5),($3,'claimant 2',$6)`,
		sender, claimant1, claimant2, "red-"+sender, "red-"+claimant1, "red-"+claimant2)
	if err == nil {
		_, err = pool.Exec(ctx, `
			INSERT INTO wallets(id,user_id,currency,available_minor) VALUES
			($1,$2,'USDT',100),($3,$4,'USDT',0),($5,$6,'USDT',0)`,
			senderWallet, sender, wallet1, claimant1, wallet2, claimant2)
	}
	if err != nil {
		t.Fatal(err)
	}
	packetIDs := make([]string, 0)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=ANY($1::text[])`, packetIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN ($1,$2,$3)`, senderWallet, wallet1, wallet2)
		_, _ = pool.Exec(ctx, `DELETE FROM red_packet_claims WHERE red_packet_id=ANY($1::uuid[])`, packetIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM red_packets WHERE id=ANY($1::uuid[])`, packetIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id IN ($1,$2,$3)`, senderWallet, wallet1, wallet2)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, sender, claimant1, claimant2)
	})
	now := time.Date(2040, 8, 1, 12, 0, 0, 0, time.UTC)
	service := NewService(pool, time.Hour)
	service.now = func() time.Time { return now }
	service.random = func(int64) (int64, error) { return 0, nil }
	input := CreateInput{
		RoomID: "00000000-0000-0000-0000-000000000001", SenderUserID: sender,
		ClientRequestID: "packet-1", Currency: "USDT", TotalMinor: 10, PacketCount: 2,
	}
	packet, created, err := service.Create(ctx, input)
	if err != nil || !created {
		t.Fatalf("create = %+v/%v/%v", packet, created, err)
	}
	packetIDs = append(packetIDs, packet.ID)
	duplicate, created, err := service.Create(ctx, input)
	if err != nil || created || duplicate.ID != packet.ID {
		t.Fatalf("duplicate create = %+v/%v/%v", duplicate, created, err)
	}
	if _, _, err := service.Claim(ctx, packet.ID, sender); !errors.Is(err, ErrSenderCannotClaim) {
		t.Fatalf("sender claim error = %v", err)
	}
	type claimResult struct {
		claim   Claim
		created bool
		err     error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for _, claimant := range []string{claimant1, claimant2} {
		wait.Add(1)
		go func(userID string) {
			defer wait.Done()
			claim, created, err := service.Claim(ctx, packet.ID, userID)
			results <- claimResult{claim: claim, created: created, err: err}
		}(claimant)
	}
	wait.Wait()
	close(results)
	claims := make(map[string]Claim)
	var claimedTotal int64
	for result := range results {
		if result.err != nil || !result.created {
			t.Fatalf("concurrent claim = %+v", result)
		}
		claims[result.claim.UserID] = result.claim
		claimedTotal += result.claim.AmountMinor
	}
	if claimedTotal != 10 || len(claims) != 2 {
		t.Fatalf("claimed total/count = %d/%d", claimedTotal, len(claims))
	}
	again, created, err := service.Claim(ctx, packet.ID, claimant1)
	if err != nil || created || again.ID != claims[claimant1].ID {
		t.Fatalf("duplicate claim = %+v/%v/%v", again, created, err)
	}
	final, err := service.Find(ctx, packet.ID, claimant1)
	if err != nil || final.Status != "exhausted" || final.RemainingMinor != 0 {
		t.Fatalf("final packet = %+v, err = %v", final, err)
	}

	input.ClientRequestID = "packet-2"
	expiring, _, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	packetIDs = append(packetIDs, expiring.ID)
	if _, _, err := service.Claim(ctx, expiring.ID, claimant1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	refunded, err := service.RefundExpired(ctx, 100)
	if err != nil || refunded != 1 {
		t.Fatalf("refund count = %d, err = %v", refunded, err)
	}
	var senderBalance int64
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, senderWallet).Scan(&senderBalance); err != nil {
		t.Fatal(err)
	}
	if senderBalance != 89 {
		t.Fatalf("sender balance = %d, want 89", senderBalance)
	}
}
