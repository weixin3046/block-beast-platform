package uploads

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/platform/objectstorage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type stubStore struct {
	info objectstorage.ObjectInfo
}

func TestUploadLifecycleAndOwnership(t *testing.T) {
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
	userID := uuid.NewString()
	otherUserID := uuid.NewString()
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id,display_name,login_name) VALUES
		($1,'upload user',$3),($2,'other upload user',$4)`,
		userID, otherUserID, "upload-"+userID, "upload-"+otherUserID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM uploads WHERE owner_user_id IN ($1,$2)`, userID, otherUserID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, userID, otherUserID)
	})
	store := stubStore{info: objectstorage.ObjectInfo{SizeBytes: 123, ContentType: "image/png"}}
	service := NewService(pool, store, 1024, 10*time.Minute)
	authorization, err := service.Authorize(ctx, userID, "image/png", 123)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if authorization.Upload.Status != "pending" || authorization.Method != "PUT" {
		t.Fatalf("authorization = %+v", authorization)
	}
	if _, err := service.Find(ctx, authorization.Upload.ID, otherUserID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("other owner find error = %v", err)
	}
	confirmed, err := service.Confirm(ctx, authorization.Upload.ID, userID)
	if err != nil || confirmed.Status != "confirmed" {
		t.Fatalf("confirm = %+v, err = %v", confirmed, err)
	}
	again, err := service.Confirm(ctx, authorization.Upload.ID, userID)
	if err != nil || again.Status != "confirmed" {
		t.Fatalf("idempotent confirm = %+v, err = %v", again, err)
	}
	expiring, err := service.Authorize(ctx, userID, "image/png", 123)
	if err != nil {
		t.Fatalf("authorize expiring upload: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE uploads SET expires_at=now()-interval '1 minute' WHERE id=$1`, expiring.Upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := service.ExpirePending(ctx, 100)
	if err != nil || expired != 1 {
		t.Fatalf("expired count = %d, err = %v", expired, err)
	}
	item, err := service.Find(ctx, expiring.Upload.ID, userID)
	if err != nil || item.Status != "expired" {
		t.Fatalf("expired upload = %+v, err = %v", item, err)
	}
}

func (stubStore) PresignPut(string, string, time.Duration) (string, error) {
	return "https://storage.example/upload", nil
}

func (store stubStore) Head(context.Context, string) (objectstorage.ObjectInfo, error) {
	return store.info, nil
}

func TestAuthorizeValidatesMetadataBeforeDatabaseAccess(t *testing.T) {
	service := NewService(nil, stubStore{}, 1024, time.Minute)
	tests := []struct {
		contentType string
		size        int64
	}{
		{"text/html", 10},
		{"image/png", 0},
		{"image/png", 1025},
	}
	for _, testCase := range tests {
		if _, err := service.Authorize(context.Background(), "user", testCase.contentType, testCase.size); !errors.Is(err, ErrInvalidUpload) {
			t.Fatalf("%s/%d error = %v", testCase.contentType, testCase.size, err)
		}
	}
}
