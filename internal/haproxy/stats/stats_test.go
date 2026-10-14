package stats

import "testing"

func TestParseStat(t *testing.T) {
	out := "# pxname,svname,qcur,scur,type,status,check_status\nlb1_s1_fe,FRONTEND,,5,0,OPEN,\nlb1_s1_be,t1,0,2,2,UP,L7OK\n\n"
	rows := ParseStat(out)
	if len(rows) != 2 || rows[0].Int("scur") != 5 || rows[1].Str("status") != "UP" || rows[1].Type != 2 {
		t.Fatalf("%+v", rows)
	}
	info := ParseInfo("Name: HAProxy\nVersion: 3.4.6\nPid: 8\n")
	if info["Version"] != "3.4.6" || info["Pid"] != "8" {
		t.Fatal(info)
	}
}
