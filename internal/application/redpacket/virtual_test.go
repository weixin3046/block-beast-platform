package redpacket

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestVirtualCannotTransferThroughRedPackets(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	id := uuid.NewString()
	if _, e = p.Exec(ctx, "INSERT INTO users(id,display_name,is_virtual) VALUES($1,'robot-redpacket',true)", id); e != nil {
		t.Fatal(e)
	}
	defer p.Exec(ctx, "DELETE FROM users WHERE id=$1", id)
	s := NewService(p, time.Hour)
	// Reject before looking up room, packet or wallet: these are valid UUIDs but do not exist.
	_, _, e = s.Create(ctx, CreateInput{SenderUserID: id, RoomID: uuid.NewString(), ClientRequestID: uuid.NewString(), Currency: "POINTS", TotalMinor: 1000, PacketCount: 1})
	if e == nil || e.Error() != "虚拟账户不能发送或领取红包" {
		t.Fatalf("create: %v", e)
	}
	_, _, e = s.Claim(ctx, uuid.NewString(), id)
	if e == nil || e.Error() != "虚拟账户不能发送或领取红包" {
		t.Fatalf("claim: %v", e)
	}
}
