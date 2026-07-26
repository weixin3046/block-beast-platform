package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/uploads"
)

type UploadService interface {
	Authorize(ctx context.Context, ownerUserID, contentType string, sizeBytes int64) (uploads.Authorization, error)
	Confirm(ctx context.Context, uploadID, ownerUserID string) (uploads.Upload, error)
	Find(ctx context.Context, uploadID, ownerUserID string) (uploads.Upload, error)
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
	writeJSON(writer, http.StatusCreated, result)
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
		writeJSON(writer, http.StatusOK, result)
	}
}
