package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/auth"
	"github.com/block-beast/platform/internal/application/operations"
)

type UserAdminService interface {
	ListUsers(ctx context.Context, status, query string, limit int) ([]operations.User, error)
	SetUserStatus(ctx context.Context, actorUserID, userID, status string) error
	ListRoles(ctx context.Context) ([]operations.Role, error)
	SetUserRoles(ctx context.Context, actorUserID, userID string, roles []string) (operations.RoleAssignment, error)
	CurrentUser(ctx context.Context, userID string) (operations.User, error)
	UpdateCurrentProfile(ctx context.Context, userID, displayName, avatarURL string) (operations.User, error)
	SetAgentLevel(ctx context.Context, userID string, level int) error
}

func (server *Server) updateCurrentProfile(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		AvatarURL   string `json:"avatar_url"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	user, err := server.userAdmin.UpdateCurrentProfile(request.Context(), claims.Subject, input.DisplayName, input.AvatarURL)
	if errors.Is(err, operations.ErrInvalidProfile) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update profile"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "user.profile.update", TargetType: "user", TargetID: claims.Subject})
	writeJSON(writer, http.StatusOK, user)
}

func (server *Server) changePassword(writer http.ResponseWriter, request *http.Request) {
	if server.passwords == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "password changes are unavailable"})
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	err := server.passwords.ChangePassword(request.Context(), claims.Subject, input.CurrentPassword, input.NewPassword)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}
	if errors.Is(err, auth.ErrInvalidPassword) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to change password"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "user.password.update", TargetType: "user", TargetID: claims.Subject})
	writeJSON(writer, http.StatusNoContent, nil)
}

func (server *Server) setSecondaryPassword(writer http.ResponseWriter, request *http.Request) {
	if server.secondaryPasswords == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "secondary passwords are unavailable"})
		return
	}
	var input struct {
		CurrentSecondaryPassword string `json:"current_secondary_password"`
		SecondaryPassword        string `json:"secondary_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	var err error
	if input.CurrentSecondaryPassword != "" {
		err = server.secondaryPasswords.ChangeSecondaryPassword(request.Context(), claims.Subject, input.CurrentSecondaryPassword, input.SecondaryPassword)
	} else {
		err = server.secondaryPasswords.SetSecondaryPassword(request.Context(), claims.Subject, "", input.SecondaryPassword)
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "password verification failed"})
		return
	}
	if errors.Is(err, auth.ErrInvalidPassword) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "secondary_password is required"})
		return
	}
	if errors.Is(err, auth.ErrSecondaryPasswordAlreadySet) {
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update secondary password"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "user.secondary_password.update", TargetType: "user", TargetID: claims.Subject})
	writeJSON(writer, http.StatusNoContent, nil)
}

func (server *Server) verifySecondaryPassword(writer http.ResponseWriter, request *http.Request) {
	if server.secondaryPasswords == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "secondary passwords are unavailable"})
		return
	}
	var input struct {
		SecondaryPassword string `json:"secondary_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	err := server.secondaryPasswords.VerifySecondaryPassword(request.Context(), claims.Subject, input.SecondaryPassword)
	if errors.Is(err, auth.ErrSecondaryPasswordNotSet) {
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "secondary password is incorrect"})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to verify secondary password"})
		return
	}
	writeJSON(writer, http.StatusNoContent, nil)
}

func (server *Server) currentUser(writer http.ResponseWriter, request *http.Request) {
	claims, _ := ClaimsFromContext(request.Context())
	user, err := server.userAdmin.CurrentUser(request.Context(), claims.Subject)
	if errors.Is(err, operations.ErrUserNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to load current user"})
		return
	}
	writeJSON(writer, http.StatusOK, user)
}

func (server *Server) setAgentLevel(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		AgentLevel int `json:"agent_level"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := server.userAdmin.SetAgentLevel(request.Context(), request.PathValue("userID"), input.AgentLevel); errors.Is(err, operations.ErrInvalidAgentLevel) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	} else if errors.Is(err, operations.ErrUserNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	} else if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to set agent level"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "user.agent_level.update", TargetType: "user", TargetID: request.PathValue("userID"), Payload: map[string]any{"agent_level": input.AgentLevel}})
	writeJSON(writer, http.StatusOK, map[string]int{"agent_level": input.AgentLevel})
}

func (server *Server) adminRoles(writer http.ResponseWriter, request *http.Request) {
	items, err := server.userAdmin.ListRoles(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list roles"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) setUserRoles(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Roles []string `json:"roles"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	result, err := server.userAdmin.SetUserRoles(request.Context(), claims.Subject, request.PathValue("userID"), input.Roles)
	switch {
	case errors.Is(err, operations.ErrInvalidRoles):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrCannotRemoveOwnAdmin), errors.Is(err, operations.ErrCannotRemoveLastAdmin):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrUserNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update user roles"})
	default:
		server.recordAudit(request.Context(), audit.Entry{
			ActorUserID: claims.Subject, Action: "user.roles.update", TargetType: "user",
			TargetID: strconv.FormatInt(result.UserID, 10), Payload: map[string]any{"roles": result.Roles},
		})
		writeJSON(writer, http.StatusOK, result)
	}
}

func WithUserAdmin(service UserAdminService) Option {
	return func(server *Server) { server.userAdmin = service }
}

func (server *Server) adminUsers(writer http.ResponseWriter, request *http.Request) {
	items, err := server.userAdmin.ListUsers(request.Context(), request.URL.Query().Get("status"), request.URL.Query().Get("q"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list users"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) setUserStatus(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	err := server.userAdmin.SetUserStatus(request.Context(), claims.Subject, request.PathValue("userID"), input.Status)
	switch {
	case errors.Is(err, operations.ErrInvalidUserStatus):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrUserNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrCannotDisableOwnAdmin), errors.Is(err, operations.ErrCannotDisableLastAdmin):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update user status"})
	default:
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "user.status.update", TargetType: "user", TargetID: request.PathValue("userID"), Payload: map[string]any{"status": input.Status}})
		writeJSON(writer, http.StatusOK, map[string]string{"status": input.Status})
	}
}
