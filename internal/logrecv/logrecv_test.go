package logrecv

import "testing"

func TestParseAndRing(t *testing.T) {
	raw := `<134>1 2026-10-14T01:00:00+00:00 host haproxy 8 - - 10.0.0.1:5000 [14/Oct/2026:01:00:00.000] lb7_s31_fe lb7_s31_be/t101 0/0/1/2/3 200 512 - - ---- 1/1/0/0/0 0/0 "GET / HTTP/1.1"`
	if ParseLB(stripSyslog(raw)) != 7 {
		t.Fatal("lb id")
	}
	if ParseLB("sys_panel sys_panel_be/loxy") != 0 {
		t.Fatal("system lines must be ignored")
	}
	r := New("", 1)
	for i := 0; i < perLB+10; i++ {
		r.Add(raw)
	}
	recent, _, cancel := r.Subscribe(7)
	defer cancel()
	if len(recent) != perLB || recent[0].Text[:9] != "10.0.0.1:" {
		t.Fatalf("ring %d %q", len(recent), recent[0].Text)
	}
}
