package identity

import "testing"

func TestPasswordHashVerification(t *testing.T) {
	hash, err := HashPassword("a-long-development-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !VerifyPassword(hash, "a-long-development-password") {
		t.Fatal("correct password was rejected")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Fatal("wrong password was accepted")
	}
}

func TestSessionTokenReturnsOpaqueRawValueAndStoredHash(t *testing.T) {
	raw, hash, err := NewSessionToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}
	if raw == "" || hash == "" || raw == hash {
		t.Fatalf("invalid token pair raw=%q hash=%q", raw, hash)
	}
}
