package api

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("got %s", got)
	}
	r.RemoteAddr = "198.51.100.1:5555" // not from the local HAProxy: header ignored
	if got := clientIP(r); got != "198.51.100.1" {
		t.Fatalf("got %s", got)
	}
}
