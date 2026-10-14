package auth

import (
	"testing"
	"time"
)

// RFC 6238 test vector (SHA1, secret "12345678901234567890").
func TestTOTPVector(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	got, _ := totpAt(secret, uint64(59/30))
	if got != "287082" {
		t.Fatalf("got %s", got)
	}
	now := time.Unix(1111111109, 0)
	code, _ := totpAt(secret, uint64(now.Unix()/30))
	if code != "081804" || !CheckTOTP(secret, code, now) || CheckTOTP(secret, "000000", now) {
		t.Fatal("check")
	}
}
