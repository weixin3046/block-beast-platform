package operations

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUpdateCurrentProfileRequiresOwnedConfirmedImage(t *testing.T) {
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

	userID, otherUserID := uuid.NewString(), uuid.NewString()
	imageID, pendingID, pdfID, otherImageID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	imageKey := "uploads/" + userID + "/" + imageID
	pendingKey := "uploads/" + userID + "/" + pendingID
	pdfKey := "uploads/" + userID + "/" + pdfID
	otherImageKey := "uploads/" + otherUserID + "/" + otherImageID
	_, err = pool.Exec(ctx, `INSERT INTO users(id,login_name,display_name) VALUES
		($1,$3,'avatar user'),($2,$4,'other avatar user')`, userID, otherUserID, "avatar-"+userID, "avatar-"+otherUserID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO uploads(id,owner_user_id,storage_key,content_type,size_bytes,status) VALUES
			($3,$1,$4,'image/png',10,'confirmed'),
			($5,$1,$6,'image/png',10,'pending'),
			($7,$1,$8,'application/pdf',10,'confirmed'),
			($9,$2,$10,'image/webp',10,'confirmed')`,
		userID, otherUserID,
		imageID, imageKey, pendingID, pendingKey, pdfID, pdfKey, otherImageID, otherImageKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM uploads WHERE owner_user_id IN ($1,$2)`, userID, otherUserID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, userID, otherUserID)
	})

	service := NewService(pool)
	for _, invalidKey := range []string{"https://example.com/avatar.png", pendingKey, pdfKey, otherImageKey} {
		if _, err := service.UpdateCurrentProfile(ctx, userID, "new name", invalidKey); !errors.Is(err, ErrInvalidAvatar) {
			t.Fatalf("avatar %q error = %v", invalidKey, err)
		}
	}

	user, err := service.UpdateCurrentProfile(ctx, userID, "new name", imageKey)
	if err != nil {
		t.Fatal(err)
	}
	if user.DisplayName != "new name" || user.AvatarURL == "" {
		t.Fatalf("updated user = %+v", user)
	}
	user, err = service.UpdateCurrentProfile(ctx, userID, "new name", "")
	if err != nil || user.AvatarURL != "" {
		t.Fatalf("cleared avatar user = %+v, err = %v", user, err)
	}
}
