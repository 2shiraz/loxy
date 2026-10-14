package reconcile

import (
	"strings"
	"testing"

	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

func mk(alg string, maint bool, targets ...*state.Target) *state.Snapshot {
	lb := &state.LB{LoadBalancer: db.LoadBalancer{ID: 1, NodeID: 1, Algorithm: alg, PublicEnabled: 1, Maintenance: b(maint)},
		Plan: &state.Plan{ID: 1, Name: "lb11", MaxConnections: 100, EnforceLbCap: 1}}
	lb.Services = []*state.Service{{Service: db.Service{ID: 1, LbID: 1, Protocol: "http", ListenPort: 80, DestinationPort: 8080, ProxyProtocol: "none",
		HttpTimeoutS: 60, HcProtocol: "tcp", HcPort: 8080, HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/"}, StatusCodes: []string{"2??"}}}
	lb.Targets = targets
	return state.TestSnapshot([]*state.LB{lb})
}

func b(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func tgt(id int64, ip string, weight int64, st string) *state.Target {
	return &state.Target{Target: db.Target{ID: id, Type: "ip", Ip: ip, Weight: weight, AdminState: st}, Slots: []*state.Slot{{ID: id, TargetID: id, Address: ip}}}
}

func prep(t *testing.T, s *state.Snapshot) *applied {
	in := render.Input{Root: "/live", NodeID: 1, LBs: s.LBsOnNode(1), Cert: s.Cert}
	out, err := render.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	in.OmitServers = true
	sk, _ := render.Render(in)
	return &applied{snap: s, out: out, skel: stripHeader(sk.Config())}
}

func run(t *testing.T, from, to *state.Snapshot) (string, []string) {
	a, b := prep(t, from), prep(t, to)
	kind, ops := classify(a, to, b.out, b.skel, 1, "/live")
	var cmds []string
	for _, o := range ops {
		cmds = append(cmds, o.Cmd)
	}
	return kind, cmds
}

func TestClassify(t *testing.T) {
	base := mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "ready"))
	cases := []struct {
		name   string
		to     *state.Snapshot
		kind   string
		prefix []string
	}{
		{"add target", mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "ready"), tgt(2, "10.0.0.2", 100, "ready")), "runtime",
			[]string{"add server lb1_s1_be/t2 10.0.0.2:8080 id 2 check", "enable health lb1_s1_be/t2", "enable server lb1_s1_be/t2"}},
		{"remove target", mk("roundrobin", false), "runtime", []string{"set server lb1_s1_be/t1 state maint", "wait 5s srv-removable", "del server lb1_s1_be/t1"}},
		{"weight", mk("roundrobin", false, tgt(1, "10.0.0.1", 50, "ready")), "runtime", []string{"set server lb1_s1_be/t1 weight 50"}},
		{"drain", mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "drain")), "runtime", []string{"set server lb1_s1_be/t1 state drain"}},
		{"maintenance", mk("roundrobin", true, tgt(1, "10.0.0.1", 100, "ready")), "runtime", []string{"add map /live/maps/maint.map 1 1"}},
		{"algorithm", mk("leastconn", false, tgt(1, "10.0.0.1", 100, "ready")), "reload", nil},
		{"source hash add", mk("source", false, tgt(1, "10.0.0.1", 100, "ready"), tgt(2, "10.0.0.2", 100, "ready")), "reload", nil},
	}
	for _, c := range cases {
		from := base
		if c.name == "source hash add" {
			from = mk("source", false, tgt(1, "10.0.0.1", 100, "ready"))
		}
		kind, cmds := run(t, from, c.to)
		if kind != c.kind {
			t.Errorf("%s: kind %s want %s (%v)", c.name, kind, c.kind, cmds)
			continue
		}
		for i, p := range c.prefix {
			if i >= len(cmds) || !strings.HasPrefix(cmds[i], p) {
				t.Errorf("%s: op %d = %v, want prefix %q", c.name, i, cmds, p)
			}
		}
	}
}

// Review finding #1: service-level changes that only render on server lines must reload.
func TestClassifyServiceLevelChangeReloads(t *testing.T) {
	from := mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "ready"))
	to := mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "ready"))
	to.LB(1).Services[0].DestinationPort = 9090
	to.LB(1).Services[0].HcPort = 9090
	if kind, ops := run(t, from, to); kind != "reload" {
		t.Fatalf("destination port change: %s %v", kind, ops)
	}
	to2 := mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "ready"))
	to2.LB(1).Services[0].ProxyProtocol = "v2"
	if kind, ops := run(t, from, to2); kind != "reload" {
		t.Fatalf("proxy protocol change: %s %v", kind, ops)
	}
}

// Review finding #2: drain does not change any file, but must still produce runtime ops.
func TestStateSigDetectsDrain(t *testing.T) {
	a := mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "ready"))
	b := mk("roundrobin", false, tgt(1, "10.0.0.1", 100, "drain"))
	pa, pb := prep(t, a), prep(t, b)
	if hashFiles(pa.out.Files) != hashFiles(pb.out.Files) {
		t.Fatal("expected identical files for a drain-only change")
	}
	if stateSig(a, 1) == stateSig(b, 1) {
		t.Fatal("state signature must differ")
	}
}
