package model

import "testing"

func TestStatusCodes(t *testing.T) {
	cases := []struct {
		in   []string
		want string
		err  bool
	}{
		{[]string{"2??", "3??"}, "200-399", false},
		{[]string{"20?"}, "200-209", false},
		{[]string{"204"}, "204", false},
		{[]string{"204", "200", "204"}, "200,204", false},
		{[]string{"3??", "200"}, "200,300-399", false},
		{[]string{"2?4"}, "", true},
		{[]string{"600"}, "", true},
		{[]string{"2??", "20?"}, "200-299", false},
		{nil, "", true},
	}
	for _, c := range cases {
		got, err := StatusCodesToHAProxy(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("%v: got %q,%v want %q", c.in, got, err, c.want)
		}
	}
}

func TestSelector(t *testing.T) {
	l := map[string]string{"env": "prod", "team": "web"}
	cases := map[string]bool{
		"env=prod":              true,
		"env==prod":             true,
		"env!=prod":             false,
		"env=prod,team!=ops":    true,
		"env in (prod,staging)": true,
		"env notin (prod)":      false,
		"team":                  true,
		"!team":                 false,
		"missing":               false,
		"!missing":              true,
	}
	for s, want := range cases {
		sel, err := ParseSelector(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if got := sel.Matches(l); got != want {
			t.Errorf("%s: got %v", s, got)
		}
	}
	for _, bad := range []string{"", "env=prod'", "env in prod", "a b c", "env in (a"} {
		if _, err := ParseSelector(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestValidators(t *testing.T) {
	if !ValidHostname("app.example.com", false) || ValidHostname("App.example.com", false) || ValidHostname("*.example.com", false) || !ValidHostname("*.example.com", true) {
		t.Error("hostname")
	}
	if !ValidHCPath("/health?x=1") || ValidHCPath("/a b") || ValidHCPath("/a'b") || ValidHCPath("health") {
		t.Error("path")
	}
	if !ValidHCResponse("OK") || ValidHCResponse("a'b") || ValidHCResponse("") || ValidHCResponse("a\nb") {
		t.Error("response")
	}
	for _, bad := range []string{"127.0.0.1", "::1", "fe80::1", "224.0.0.1", "0.0.0.0", "x"} {
		if _, err := ParseTargetIP(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := ParseTargetIP("10.0.0.5"); err != nil {
		t.Error(err)
	}
}
