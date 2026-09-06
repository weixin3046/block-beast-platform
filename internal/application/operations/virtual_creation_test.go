package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/uploads"
	"github.com/block-beast/platform/internal/platform/localstorage"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestVirtualCreationDefaults(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	login := "robot-" + uuid.NewString()
	var in VirtualAccountInput
	if err = json.Unmarshal([]byte(fmt.Sprintf(`{"login_name":%q,"password":"test-password-123"}`, login)), &in); err != nil {
		t.Fatal(err)
	}
	got, err := NewService(p).CreateVirtualAccount(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != fmt.Sprintf("用户%d", got.UserID) {
		t.Fatal(got)
	}
	var virtual bool
	var avatar string
	if err = p.QueryRow(ctx, "SELECT is_virtual,COALESCE(avatar_url,'') FROM users WHERE public_id=$1", got.UserID).Scan(&virtual, &avatar); err != nil || !virtual || avatar != "" {
		t.Fatal(virtual, avatar, err)
	}
	if _, err = NewService(p).CreateVirtualAccount(ctx, in); err == nil {
		t.Fatal("duplicate login accepted")
	}
}

func TestVirtualCreationAvatarOwnershipAndRead(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	actor := uuid.NewString()
	if _, err = p.Exec(ctx, "INSERT INTO users(id,display_name) VALUES($1,'avatar admin')", actor); err != nil {
		t.Fatal(err)
	}
	store, err := localstorage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	up := uploads.NewService(p, store, 1024, time.Minute)
	content := "\x89PNG\r\n\x1a\nimage"
	a, err := up.Authorize(ctx, actor, "image/png", int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	in := VirtualAccountInput{ActorUserID: actor, LoginName: "robot-" + uuid.NewString(), DisplayName: " 自定义昵称 ", Password: "test-password-123", AvatarURL: a.Upload.StorageKey}
	s := NewService(p)
	if _, err = s.CreateVirtualAccount(ctx, in); !errors.Is(err, ErrInvalidAvatar) {
		t.Fatal("pending accepted", err)
	}
	if _, err = up.PutContent(ctx, a.Upload.ID, actor, "image/png", strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	in.ActorUserID = uuid.NewString()
	if _, err = s.CreateVirtualAccount(ctx, in); !errors.Is(err, ErrInvalidAvatar) {
		t.Fatal("foreign upload accepted", err)
	}
	in.ActorUserID = actor
	got, err := s.CreateVirtualAccount(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "自定义昵称" || !strings.HasPrefix(got.AvatarURL, "/v1/avatars/") {
		t.Fatal(got)
	}
	reader, _, err := up.OpenPublicAvatar(ctx, got.UserID)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
}
