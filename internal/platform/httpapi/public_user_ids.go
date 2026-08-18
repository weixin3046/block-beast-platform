package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/block-beast/platform/internal/domain/identity"
)

func (server *Server) resolvePublicUserID(ctx context.Context, value string) (string, error) {
	if server.publicUsers == nil {
		// 保持未装配数据库的单元测试与本地内存实现兼容。
		return value, nil
	}
	publicID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || publicID < 100000 {
		return "", identity.ErrIdentityNotFound
	}
	return server.publicUsers.InternalUserIDByPublicID(ctx, publicID)
}

// writePublicJSON prevents internal UUID user references from leaking through
// API response DTOs that are still used by domain services.
func (server *Server) writePublicJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil || server.publicUsers == nil {
		writeJSON(writer, status, value)
		return
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		writeJSON(writer, status, value)
		return
	}
	server.replaceUserIDs(request.Context(), document)
	writeJSON(writer, status, document)
}

func (server *Server) replaceUserIDs(ctx context.Context, value any) {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			server.replaceUserIDs(ctx, child)
		}
	case map[string]any:
		for key, child := range item {
			if userID, ok := child.(string); ok && isPublicUserIDField(key) {
				if publicID, err := server.publicUsers.PublicUserID(ctx, userID); err == nil {
					item[key] = publicID
				}
			}
			server.replaceUserIDs(ctx, child)
		}
	}
}

func isPublicUserIDField(key string) bool {
	switch key {
	case "user_id", "account_id", "agent_id", "parent_user_id", "sender_user_id", "owner_user_id", "beneficiary_user_id", "operator_id", "actor_user_id", "reviewed_by", "updated_by":
		return true
	default:
		return false
	}
}
