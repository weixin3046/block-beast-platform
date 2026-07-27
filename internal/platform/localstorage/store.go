package localstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/platform/objectstorage"
)

type metadata struct {
	ContentType string `json:"content_type"`
}

// Store 将上传文件保存到受控根目录。对象和元数据均通过临时文件加 rename
// 原子落盘，避免进程中断后暴露半个文件。
type Store struct {
	root string
}

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("local upload root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local upload root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create local upload root: %w", err)
	}
	return &Store{root: absolute}, nil
}

func (store *Store) PresignPut(key, _ string, _ time.Duration) (string, error) {
	return store.UploadURL(key)
}

// UploadURL 返回需要携带当前 Bearer Token 调用的站内上传地址。
func (store *Store) UploadURL(key string) (string, error) {
	if _, err := store.resolve(key); err != nil {
		return "", err
	}
	uploadID := filepath.Base(filepath.FromSlash(key))
	return "/v1/uploads/" + url.PathEscape(uploadID) + "/content", nil
}

func (store *Store) Put(_ context.Context, key, contentType string, expectedSize int64, source io.Reader) error {
	target, err := store.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create upload directory: %w", err)
	}
	objectTemp, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		return fmt.Errorf("create upload temporary file: %w", err)
	}
	objectTempName := objectTemp.Name()
	defer objectTemp.Close()
	defer os.Remove(objectTempName)
	if err := objectTemp.Chmod(0o600); err != nil {
		objectTemp.Close()
		return err
	}
	written, copyErr := io.Copy(objectTemp, io.LimitReader(source, expectedSize+1))
	if copyErr != nil {
		return fmt.Errorf("write upload: %w", copyErr)
	}
	if written != expectedSize {
		return objectstorage.ErrSizeMismatch
	}
	if _, err := objectTemp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind upload: %w", err)
	}
	sample := make([]byte, 512)
	sampleSize, err := objectTemp.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("inspect upload: %w", err)
	}
	if detected := http.DetectContentType(sample[:sampleSize]); detected != contentType {
		return objectstorage.ErrContentTypeMismatch
	}
	if err := objectTemp.Sync(); err != nil {
		return fmt.Errorf("sync upload: %w", err)
	}
	if err := objectTemp.Close(); err != nil {
		return fmt.Errorf("close upload: %w", err)
	}

	metaBytes, err := json.Marshal(metadata{ContentType: contentType})
	if err != nil {
		return err
	}
	metaTemp, err := os.CreateTemp(filepath.Dir(target), ".metadata-*")
	if err != nil {
		return fmt.Errorf("create upload metadata: %w", err)
	}
	metaTempName := metaTemp.Name()
	defer os.Remove(metaTempName)
	if err := metaTemp.Chmod(0o600); err != nil {
		metaTemp.Close()
		return err
	}
	if _, err := metaTemp.Write(metaBytes); err != nil {
		metaTemp.Close()
		return fmt.Errorf("write upload metadata: %w", err)
	}
	if err := metaTemp.Sync(); err != nil {
		metaTemp.Close()
		return err
	}
	if err := metaTemp.Close(); err != nil {
		return err
	}
	if err := os.Rename(objectTempName, target); err != nil {
		return fmt.Errorf("commit upload: %w", err)
	}
	if err := os.Rename(metaTempName, target+".json"); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("commit upload metadata: %w", err)
	}
	return nil
}

func (store *Store) Head(_ context.Context, key string) (objectstorage.ObjectInfo, error) {
	target, err := store.resolve(key)
	if err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	contentType, err := readContentType(target + ".json")
	if err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	return objectstorage.ObjectInfo{SizeBytes: info.Size(), ContentType: contentType}, nil
}

func (store *Store) Open(ctx context.Context, key string) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, objectstorage.ObjectInfo{}, err
	}
	target, err := store.resolve(key)
	if err != nil {
		return nil, objectstorage.ObjectInfo{}, err
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, objectstorage.ObjectInfo{}, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, objectstorage.ObjectInfo{}, err
	}
	contentType, err := readContentType(target + ".json")
	if err != nil {
		file.Close()
		return nil, objectstorage.ObjectInfo{}, err
	}
	return file, objectstorage.ObjectInfo{SizeBytes: info.Size(), ContentType: contentType}, nil
}

func (*Store) RequiresAuthentication() bool { return true }

func (store *Store) Delete(_ context.Context, key string) error {
	target, err := store.resolve(key)
	if err != nil {
		return err
	}
	objectErr := os.Remove(target)
	metaErr := os.Remove(target + ".json")
	if objectErr != nil && !errors.Is(objectErr, os.ErrNotExist) {
		return objectErr
	}
	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return metaErr
	}
	return nil
}

func (store *Store) resolve(key string) (string, error) {
	key = filepath.FromSlash(strings.TrimSpace(key))
	if key == "" || filepath.IsAbs(key) {
		return "", errors.New("invalid local object key")
	}
	clean := filepath.Clean(key)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid local object key")
	}
	target := filepath.Join(store.root, clean)
	relative, err := filepath.Rel(store.root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("local object key escapes upload root")
	}
	return target, nil
}

func readContentType(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var item metadata
	if err := json.Unmarshal(value, &item); err != nil {
		return "", err
	}
	return item.ContentType, nil
}
