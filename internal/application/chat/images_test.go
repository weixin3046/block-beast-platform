package chat

import (
	"bytes"
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/uploads"
	"github.com/block-beast/platform/internal/platform/localstorage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestImageMessagesAndReadPermissions(t *testing.T) {
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
	owner, other, staff := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e = p.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'image owner'),($2,'outsider'),($3,'staff')`, owner, other, staff); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, staff); e != nil {
		t.Fatal(e)
	}
	s := NewService(p)
	rooms, e := s.OpenCustomerServiceRooms(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	store, e := localstorage.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	u := uploads.NewService(p, store, 1024, 10*time.Minute)
	content := []byte("\x89PNG\r\n\x1a\nimage")
	a, e := u.Authorize(ctx, owner, "image/png", int64(len(content)))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.SendMessage(ctx, rooms.Deposit.ID, owner, "pending", "", false, a.Upload.ID); !errors.Is(e, ErrInvalidMessage) {
		t.Fatalf("pending: %v", e)
	}
	if _, e = u.PutContent(ctx, a.Upload.ID, owner, "image/png", bytes.NewReader(content)); e != nil {
		t.Fatal(e)
	}
	m, fresh, e := s.SendMessage(ctx, rooms.Deposit.ID, owner, "image", "", false, a.Upload.ID)
	if e != nil || !fresh || m.ImageUploadID != a.Upload.ID || m.ImageURL == "" {
		t.Fatalf("image %+v %v", m, e)
	}
	repeat, fresh, e := s.SendMessage(ctx, rooms.Deposit.ID, owner, "image", "caption", false, a.Upload.ID)
	if e != nil || fresh || repeat.ID != m.ID {
		t.Fatalf("retry %v", e)
	}
	history, e := s.ListMessages(ctx, rooms.Deposit.ID, owner, false, 50)
	if e != nil || len(history) != 1 || history[0].ImageURL != m.ImageURL {
		t.Fatalf("history %+v %v", history, e)
	}
	if _, _, e = s.SendMessage(ctx, rooms.Deposit.ID, staff, "stolen", "", true, a.Upload.ID); !errors.Is(e, ErrInvalidMessage) {
		t.Fatalf("foreign upload %v", e)
	}
	if _, _, e = u.OpenContent(ctx, a.Upload.ID, other); !errors.Is(e, uploads.ErrUploadNotFound) {
		t.Fatalf("private leak %v", e)
	}
	stream, _, e := u.OpenContent(ctx, a.Upload.ID, staff)
	if e != nil {
		t.Fatal(e)
	}
	stream.Close()
	if _, e = p.Exec(ctx, `UPDATE chat_messages SET status='hidden' WHERE id=$1`, m.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = u.OpenContent(ctx, a.Upload.ID, staff); !errors.Is(e, uploads.ErrUploadNotFound) {
		t.Fatalf("hidden leak %v", e)
	}
	var global string
	if e = p.QueryRow(ctx, `SELECT id FROM chat_rooms WHERE room_type='global' LIMIT 1`).Scan(&global); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.SendMessage(ctx, global, owner, "global-image", "caption", false, a.Upload.ID); e != nil {
		t.Fatal(e)
	}
	stream, _, e = u.OpenContent(ctx, a.Upload.ID, other)
	if e != nil {
		t.Fatal(e)
	}
	stream.Close()
}
