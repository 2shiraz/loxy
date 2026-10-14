package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/agent"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/haproxy/runtime"
	"github.com/2shiraz/loxy/internal/haproxy/stats"
)

type fakeNode struct {
	stat string
	pid  string
}

func (f *fakeNode) Runtime(context.Context, []agent.RuntimeCmd) ([]agent.RuntimeResult, error) {
	return nil, nil
}
func (f *fakeNode) Reload(context.Context) (*runtime.ReloadResult, error) { return nil, nil }
func (f *fakeNode) Info(context.Context) (*agent.NodeInfo, error)         { return nil, nil }
func (f *fakeNode) Stat(context.Context, bool) ([]stats.Row, map[string]string, error) {
	return stats.ParseStat(f.stat), map[string]string{"Pid": f.pid}, nil
}

const hdr = "# pxname,svname,scur,conn_tot,req_tot,bin,bout,type,status,sid,check_status,check_code,check_duration,rtime,qcur\n"

func row(conn, req int) string {
	return hdr + "lb1_s1_fe,FRONTEND,3," + itoa(conn) + "," + itoa(req) + ",100,200,0,OPEN,,,,,,\n" +
		"lb1_s1_be,t1,1,,,,,2,UP,7,L7OK,200,2,,\nlb1_s1_be,BACKEND,0,,,,,1,UP,,,,,5,0\n"
}

func itoa(i int) string { return fmtInt(i) }

func (c *Collector) pollNoDB(now time.Time, _ *fakeNode) error {
	return c.poll(context.Background(), now)
}

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestDeltasAndReset(t *testing.T) {
	n := &fakeNode{stat: row(100, 1000), pid: "8"}
	c := New(n, nil, events.NewHub(nil), time.Second)
	now := time.Now().Truncate(time.Second).Add(-5 * time.Second)
	_ = c.pollNoDB(now, n)
	n.stat = row(110, 1050)
	_ = c.pollNoDB(now.Add(time.Second), n)
	n.stat, n.pid = row(5, 20), "9" // reload: new worker, counters restart
	_ = c.pollNoDB(now.Add(2*time.Second), n)
	pts := c.rings[key{1, 1}].since(0)
	if len(pts) != 3 {
		t.Fatalf("points %d", len(pts))
	}
	if pts[1].CPS != 10 || pts[1].RPS != 50 {
		t.Fatalf("delta: %+v", pts[1])
	}
	if pts[2].CPS != 5 || pts[2].RPS != 20 {
		t.Fatalf("after reset must use current value, got %+v", pts[2])
	}
	if pts[2].Open != 3 || pts[2].RespMS != 5 {
		t.Fatalf("gauges: %+v", pts[2])
	}
	ch, ok := c.Check(1, 1, 7)
	if !ok || ch.Status != "healthy" || ch.Detail != "L7OK/200 in 2ms" {
		t.Fatalf("check %+v", ch)
	}
	s, _ := c.Query(context.Background(), 1, 0, now.Unix(), now.Unix()+2, 1, []string{"requests_per_second"})
	if v := s.Values["requests_per_second"]; len(v) != 3 || v[1] == nil || *v[1] != 50 {
		t.Fatalf("query %v", v)
	}
}
