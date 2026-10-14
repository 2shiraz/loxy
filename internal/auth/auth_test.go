package auth

import (
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse battery") || CheckPassword(h, "wrong") {
		t.Fatal("check")
	}
}

func TestCSRFAndRoles(t *testing.T) {
	k := []byte("k")
	tok := CSRFToken(k, "s1")
	if !CheckCSRF(k, "s1", tok) || CheckCSRF(k, "s2", tok) {
		t.Fatal("csrf")
	}
	if !Allows("admin", "operator") || Allows("viewer", "operator") || Allows("owner", "") {
		t.Fatal("roles")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if err := l.Allow("ip"); err != nil {
			t.Fatal(err)
		}
		l.Fail("ip")
	}
	if l.Allow("ip") == nil {
		t.Fatal("expected limit")
	}
	now = now.Add(16 * time.Minute)
	if l.Allow("ip") != nil {
		t.Fatal("expected reset")
	}
}
