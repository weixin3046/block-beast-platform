package localstorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/platform/objectstorage"
)

func TestStoreLifecycleAndTraversalProtection(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "uploads/user-1/upload-1"
	url, err := store.UploadURL(key)
	if err != nil || url != "/v1/uploads/upload-1/content" {
		t.Fatalf("upload URL = %q, err = %v", url, err)
	}
	content := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("fake png content")...)
	if err := store.Put(context.Background(), key, "image/png", int64(len(content)), bytes.NewReader(content)); err != nil {
		t.Fatalf("put: %v", err)
	}
	info, err := store.Head(context.Background(), key)
	if err != nil || info.SizeBytes != int64(len(content)) || info.ContentType != "image/png" {
		t.Fatalf("head = %+v, err = %v", info, err)
	}
	file, openedInfo, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(got, content) || openedInfo != info {
		t.Fatalf("content = %q, info = %+v, err = %v", got, openedInfo, err)
	}
	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Head(context.Background(), key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("head deleted error = %v", err)
	}

	for _, invalid := range []string{"", "../secret", "uploads/../../secret", "/absolute"} {
		if _, err := store.resolve(invalid); err == nil {
			t.Fatalf("key %q should be rejected", invalid)
		}
	}
}

func TestStoreRejectsSizeMismatchWithoutCommittedFile(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "uploads/user/upload"
	err = store.Put(context.Background(), key, "image/png", 3, strings.NewReader("too long"))
	if !errors.Is(err, objectstorage.ErrSizeMismatch) {
		t.Fatalf("put error = %v, want size mismatch", err)
	}
	if _, err := store.Head(context.Background(), key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched object should not exist: %v", err)
	}
}

func TestStoreRejectsContentTypeSpoofing(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("<html>not an image</html>")
	err = store.Put(context.Background(), "uploads/user/upload", "image/png", int64(len(content)), bytes.NewReader(content))
	if !errors.Is(err, objectstorage.ErrContentTypeMismatch) {
		t.Fatalf("put error = %v, want content type mismatch", err)
	}
}
