package secrets

import "testing"

func TestRoundTrip(t *testing.T) {
	b, err := New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := b.Seal([]byte("hello"))
	if string(s) == "hello" {
		t.Fatal("not encrypted")
	}
	p, err := b.Open(s)
	if err != nil || string(p) != "hello" {
		t.Fatalf("got %q %v", p, err)
	}
	s[len(s)-1] ^= 1
	if _, err := b.Open(s); err == nil {
		t.Fatal("tamper not detected")
	}
}
