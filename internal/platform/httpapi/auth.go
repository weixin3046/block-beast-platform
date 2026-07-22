package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
)

type claimsContextKey struct{}

// Authenticator 校验 Bearer 访问令牌并执行基于角色的访问控制。
type Authenticator struct {
	secret []byte
	now    func() time.Time
}

func NewAuthenticator(secret string) *Authenticator {
	return &Authenticator{secret: []byte(secret), now: time.Now}
}

// ClaimsFromContext 返回请求上下文中已认证的令牌声明。
func ClaimsFromContext(ctx context.Context) (identity.AccessTokenClaims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(identity.AccessTokenClaims)
	return claims, ok
}

// Authenticate 要求请求携带有效令牌，否则返回 401。
func (authenticator *Authenticator) Authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		claims, ok := authenticator.verify(request)
		if !ok {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "missing or invalid access token"})
			return
		}
		next(writer, request.WithContext(context.WithValue(request.Context(), claimsContextKey{}, claims)))
	}
}

// RequireRoles 在 Authenticate 之上要求任一指定角色，否则返回 403。
func (authenticator *Authenticator) RequireRoles(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return authenticator.Authenticate(func(writer http.ResponseWriter, request *http.Request) {
		claims, _ := ClaimsFromContext(request.Context())
		if !claims.HasRole(roles...) {
			writeJSON(writer, http.StatusForbidden, map[string]string{"error": "insufficient role"})
			return
		}
		next(writer, request)
	})
}

func (authenticator *Authenticator) verify(request *http.Request) (identity.AccessTokenClaims, bool) {
	header := request.Header.Get("Authorization")
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found || token == "" {
		return identity.AccessTokenClaims{}, false
	}
	claims, err := identity.VerifyAccessToken(authenticator.secret, token, authenticator.now())
	if err != nil {
		return identity.AccessTokenClaims{}, false
	}
	return claims, true
}

// authorizeAccount 校验已认证主体是否有权操作指定账号的资源：
// 本人或具备 operator/admin 角色的运营人员。未启用鉴权（无声明）时放行。
func authorizeAccount(request *http.Request, accountID string) bool {
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		return true
	}
	return claims.Subject == accountID || claims.HasRole(identity.RoleOperator, identity.RoleAdmin)
}
