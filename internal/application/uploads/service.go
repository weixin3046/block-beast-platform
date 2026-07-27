package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/platform/objectstorage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidUpload = errors.New("invalid upload content type or size")
var ErrUploadNotFound = errors.New("upload not found")
var ErrUploadExpired = errors.New("upload authorization expired")
var ErrObjectMismatch = errors.New("uploaded object does not match declared metadata")
var ErrUploadNotReady = errors.New("upload is not confirmed")
var ErrContentOperationUnsupported = errors.New("content operation is unsupported by the configured storage")

var allowedContentTypes = map[string]struct{}{
	"image/jpeg": {}, "image/png": {}, "image/webp": {}, "application/pdf": {},
}

type Store interface {
	PresignPut(key, contentType string, ttl time.Duration) (string, error)
	Head(ctx context.Context, key string) (objectstorage.ObjectInfo, error)
}

type contentStore interface {
	Put(ctx context.Context, key, contentType string, expectedSize int64, source io.Reader) error
	Open(ctx context.Context, key string) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error)
}

type authenticatedStore interface {
	RequiresAuthentication() bool
}

type Upload struct {
	ID          string     `json:"id"`
	OwnerUserID string     `json:"owner_user_id"`
	StorageKey  string     `json:"storage_key"`
	ContentType string     `json:"content_type"`
	SizeBytes   int64      `json:"size_bytes"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Authorization struct {
	Upload       Upload            `json:"upload"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	ExpiresAt    time.Time         `json:"expires_at"`
	RequiresAuth bool              `json:"requires_auth,omitempty"`
}

type Service struct {
	pool     *pgxpool.Pool
	store    Store
	maxBytes int64
	ttl      time.Duration
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, store Store, maxBytes int64, ttl time.Duration) *Service {
	return &Service{pool: pool, store: store, maxBytes: maxBytes, ttl: ttl, now: time.Now}
}

func (service *Service) Authorize(ctx context.Context, ownerUserID, contentType string, sizeBytes int64) (Authorization, error) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if _, ok := allowedContentTypes[contentType]; !ok || sizeBytes <= 0 || sizeBytes > service.maxBytes {
		return Authorization{}, ErrInvalidUpload
	}
	now := service.now().UTC()
	expiresAt := now.Add(service.ttl)
	uploadID := uuid.NewString()
	key := fmt.Sprintf("uploads/%s/%s", ownerUserID, uploadID)
	signedURL, err := service.store.PresignPut(key, contentType, service.ttl)
	if err != nil {
		return Authorization{}, err
	}
	var upload Upload
	err = service.pool.QueryRow(ctx, `
		INSERT INTO uploads (id,owner_user_id,storage_key,content_type,size_bytes,status,expires_at)
		VALUES ($1,$2,$3,$4,$5,'pending',$6)
		RETURNING id::text,owner_user_id::text,storage_key,content_type,size_bytes,status,expires_at,created_at`,
		uploadID, ownerUserID, key, contentType, sizeBytes, expiresAt).
		Scan(&upload.ID, &upload.OwnerUserID, &upload.StorageKey, &upload.ContentType, &upload.SizeBytes, &upload.Status, &upload.ExpiresAt, &upload.CreatedAt)
	if err != nil {
		return Authorization{}, err
	}
	authorization := Authorization{
		Upload: upload, Method: "PUT", URL: signedURL,
		Headers: map[string]string{"Content-Type": contentType}, ExpiresAt: expiresAt,
	}
	if store, ok := service.store.(authenticatedStore); ok {
		authorization.RequiresAuth = store.RequiresAuthentication()
	}
	return authorization, nil
}

func (service *Service) PutContent(ctx context.Context, uploadID, ownerUserID, contentType string, source io.Reader) (Upload, error) {
	upload, err := service.find(ctx, uploadID, ownerUserID)
	if err != nil {
		return Upload{}, err
	}
	if upload.Status == "confirmed" {
		return upload, nil
	}
	if upload.ExpiresAt == nil || !upload.ExpiresAt.After(service.now().UTC()) {
		_, _ = service.pool.Exec(ctx, `UPDATE uploads SET status='expired' WHERE id=$1 AND status='pending'`, uploadID)
		return Upload{}, ErrUploadExpired
	}
	if !strings.EqualFold(strings.TrimSpace(contentType), upload.ContentType) {
		return Upload{}, ErrObjectMismatch
	}
	store, ok := service.store.(contentStore)
	if !ok {
		return Upload{}, ErrContentOperationUnsupported
	}
	if err := store.Put(ctx, upload.StorageKey, upload.ContentType, upload.SizeBytes, source); err != nil {
		if errors.Is(err, objectstorage.ErrSizeMismatch) || errors.Is(err, objectstorage.ErrContentTypeMismatch) {
			return Upload{}, ErrObjectMismatch
		}
		return Upload{}, fmt.Errorf("store upload content: %w", err)
	}
	return service.Confirm(ctx, uploadID, ownerUserID)
}

func (service *Service) OpenContent(ctx context.Context, uploadID, ownerUserID string) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error) {
	upload, err := service.find(ctx, uploadID, ownerUserID)
	if err != nil {
		return nil, objectstorage.ObjectInfo{}, err
	}
	if upload.Status != "confirmed" {
		return nil, objectstorage.ObjectInfo{}, ErrUploadNotReady
	}
	store, ok := service.store.(contentStore)
	if !ok {
		return nil, objectstorage.ObjectInfo{}, ErrContentOperationUnsupported
	}
	return store.Open(ctx, upload.StorageKey)
}

func (service *Service) Confirm(ctx context.Context, uploadID, ownerUserID string) (Upload, error) {
	upload, err := service.find(ctx, uploadID, ownerUserID)
	if err != nil {
		return Upload{}, err
	}
	if upload.Status == "confirmed" {
		return upload, nil
	}
	if upload.ExpiresAt == nil || !upload.ExpiresAt.After(service.now().UTC()) {
		_, _ = service.pool.Exec(ctx, `UPDATE uploads SET status='expired' WHERE id=$1 AND status='pending'`, uploadID)
		return Upload{}, ErrUploadExpired
	}
	info, err := service.store.Head(ctx, upload.StorageKey)
	if err != nil {
		return Upload{}, err
	}
	if info.SizeBytes != upload.SizeBytes || !strings.EqualFold(strings.TrimSpace(info.ContentType), upload.ContentType) {
		return Upload{}, ErrObjectMismatch
	}
	err = service.pool.QueryRow(ctx, `
		UPDATE uploads SET status='confirmed'
		WHERE id=$1 AND owner_user_id=$2 AND status IN ('pending','confirmed')
		RETURNING id::text,owner_user_id::text,storage_key,content_type,size_bytes,status,expires_at,created_at`,
		uploadID, ownerUserID).
		Scan(&upload.ID, &upload.OwnerUserID, &upload.StorageKey, &upload.ContentType, &upload.SizeBytes, &upload.Status, &upload.ExpiresAt, &upload.CreatedAt)
	return upload, err
}

func (service *Service) Find(ctx context.Context, uploadID, ownerUserID string) (Upload, error) {
	return service.find(ctx, uploadID, ownerUserID)
}

func (service *Service) ExpirePending(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	result, err := service.pool.Exec(ctx, `
		WITH expired AS (
			SELECT id FROM uploads
			WHERE status='pending' AND expires_at <= $1
			ORDER BY expires_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE uploads SET status='expired'
		WHERE id IN (SELECT id FROM expired)`, service.now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (service *Service) find(ctx context.Context, uploadID, ownerUserID string) (Upload, error) {
	var upload Upload
	err := service.pool.QueryRow(ctx, `
		SELECT id::text,owner_user_id::text,storage_key,content_type,size_bytes,status,expires_at,created_at
		FROM uploads WHERE id=$1 AND owner_user_id=$2`, uploadID, ownerUserID).
		Scan(&upload.ID, &upload.OwnerUserID, &upload.StorageKey, &upload.ContentType, &upload.SizeBytes, &upload.Status, &upload.ExpiresAt, &upload.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Upload{}, ErrUploadNotFound
	}
	return upload, err
}
