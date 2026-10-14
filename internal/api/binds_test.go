package api

import (
	"testing"

	"github.com/2shiraz/loxy/internal/state"
)

func TestBindsCollide(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"*", "10.0.0.5", true},
		{"*", "::1", true},
		{"0.0.0.0", "10.0.0.5", true},
		{"0.0.0.0", "::", false},
		{"::", "2001:db8::1", true},
		{"::", "10.0.0.5", false},
		{"10.0.0.5", "10.0.0.6", false},
		{"10.0.0.5", "10.0.0.5", true},
		{"2001:db8::1", "2001:db8:0::1", true},
	} {
		if got := bindsCollide(state.Bind{Address: c.a, Port: 80}, state.Bind{Address: c.b, Port: 80}); got != c.want {
			t.Errorf("%s vs %s: got %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if bindsCollide(state.Bind{Address: "*", Port: 80}, state.Bind{Address: "*", Port: 81}) {
		t.Error("different ports collide")
	}
}
