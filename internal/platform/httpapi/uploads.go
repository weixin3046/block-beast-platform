package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/block-beast/platform/internal/application/uploads"
	"github.com/block-beast/platform/internal/platform/objectstorage"
)

type UploadService interface {
	Authorize(ctx context.Context, ownerUserID, contentType string, sizeBytes int64) (uploads.Authorization, error)
	Confirm(ctx context.Context, uploadID, ownerUserID string) (uploads.Upload, error)
	Find(ctx context.Context, uploadID, ownerUserID string) (uploads.Upload, error)
	PutContent(ctx context.Context, uploadID, ownerUserID, contentType string, source io.Reader) (uploads.Upload, error)
	OpenContent(ctx context.Context, uploadID, ownerUserID string) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error)
	OpenPublicAvatar(ctx context.Context, publicUserID int64) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error)
}

func (server *Server) publicAvatar(writer http.ResponseWriter, request *http.Request) {
	if server.uploads == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "uploads are unavailable"})
		return
	}
	userID, err := strconv.ParseInt(request.PathValue("userID"), 10, 64)
	if err != nil || userID < 100000 {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "avatar not found"})
		return
	}
	content, info, err := server.uploads.OpenPublicAvatar(request.Context(), userID)
	if errors.Is(err, uploads.ErrUploadNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "avatar not found"})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read avatar"})
		return
	}
	defer content.Close()
	writer.Header().Set("Content-Type", info.ContentType)
	// 地址按用户 ID 固定，头像更换后必须避免浏览器继续展示旧缓存。
	writer.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, request.PathValue("userID"), time.Time{}, content)
}

func (server *Server) putUploadContent(writer http.ResponseWriter, request *http.Request) {
	if server.uploads == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "uploads are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	maxBytes := server.config.UploadMaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	body := http.MaxBytesReader(writer, request.Body, maxBytes+1)
	result, err := server.uploads.PutContent(
		request.Context(), request.PathValue("uploadID"), claims.Subject,
		request.Header.Get("Content-Type"), body,
	)
	switch {
	case errors.Is(err, uploads.ErrUploadNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, uploads.ErrUploadExpired), errors.Is(err, uploads.ErrObjectMismatch):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, uploads.ErrContentOperationUnsupported):
		writeJSON(writer, http.StatusMethodNotAllowed, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to store upload"})
	default:
		server.writePublicJSON(writer, request, http.StatusOK, result)
	}
}

func (server *Server) downloadUploadContent(writer http.ResponseWriter, request *http.Request) {
	if server.uploads == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "uploads are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	content, info, err := server.uploads.OpenContent(request.Context(), request.PathValue("uploadID"), claims.Subject)
	switch {
	case errors.Is(err, uploads.ErrUploadNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, uploads.ErrUploadNotReady):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, uploads.ErrContentOperationUnsupported):
		writeJSON(writer, http.StatusMethodNotAllowed, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read upload"})
	default:
		defer content.Close()
		writer.Header().Set("Content-Type", info.ContentType)
		writer.Header().Set("Cache-Control", "private, no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(writer, request, request.PathValue("uploadID"), time.Time{}, content)
	}
}

func WithUploads(service UploadService) Option {
	return func(server *Server) { server.uploads = service }
}

func (server *Server) authorizeUpload(writer http.ResponseWriter, request *http.Request) {
	if server.uploads == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "uploads are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var input struct {
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid upload request"})
		return
	}
	result, err := server.uploads.Authorize(request.Context(), claims.Subject, input.ContentType, input.SizeBytes)
	if errors.Is(err, uploads.ErrInvalidUpload) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to authorize upload"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusCreated, result)
}

func (server *Server) confirmUpload(writer http.ResponseWriter, request *http.Request) {
	server.handleUpload(writer, request, true)
}

func (server *Server) upload(writer http.ResponseWriter, request *http.Request) {
	server.handleUpload(writer, request, false)
}

func (server *Server) handleUpload(writer http.ResponseWriter, request *http.Request, confirm bool) {
	if server.uploads == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "uploads are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var result uploads.Upload
	var err error
	if confirm {
		result, err = server.uploads.Confirm(request.Context(), request.PathValue("uploadID"), claims.Subject)
	} else {
		result, err = server.uploads.Find(request.Context(), request.PathValue("uploadID"), claims.Subject)
	}
	switch {
	case errors.Is(err, uploads.ErrUploadNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, uploads.ErrUploadExpired), errors.Is(err, uploads.ErrObjectMismatch):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "unable to verify upload"})
	default:
		server.writePublicJSON(writer, request, http.StatusOK, result)
	}
}
