package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
)

type rejectingSessionValidator struct{}

func (rejectingSessionValidator) ValidateSession(context.Context, identity.AccessTokenClaims) error {
	return identity.ErrInvalidAccessToken
}

func TestRevokedSessionRejectsSignedAccessToken(t *testing.T) {
	token, err := identity.IssueAccessToken([]byte(testSecret), "user", []string{"player"}, time.Now(), time.Hour, "old-session")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	NewAuthenticator(testSecret).WithSessionValidator(rejectingSessionValidator{}).Authenticate(func(http.ResponseWriter, *http.Request) { t.Fatal("revoked session reached handler") })(w, r)
	if w.Code != 401 {
		t.Fatalf("status=%d", w.Code)
	}
}
