package certs

import (
	"testing"
	"time"
)

func TestParseSelfSigned(t *testing.T) {
	c, k, err := SelfSigned("example.com", []string{"example.com", "www.example.com"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(c, "", k, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Domains) != 2 || p.Fingerprint == "" {
		t.Fatalf("%+v", p)
	}
	_, k2, _ := SelfSigned("other", nil, time.Hour)
	if _, err := Parse(c, "", k2, time.Now()); err == nil {
		t.Fatal("mismatched key accepted")
	}
	if _, err := Parse(c, "", k, time.Now().Add(48*time.Hour)); err == nil {
		t.Fatal("expired accepted")
	}
}
