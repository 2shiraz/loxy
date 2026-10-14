package reconcile

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

type srvSpec struct {
	id     int64
	ip     string
	weight int64
	state  string
}

func mk(alg string, maint bool, port int64, servers ...srvSpec) *state.Snapshot {
	be := &state.Backend{BackendCfg: state.BackendCfg{ID: 1, NodeID: 1, Mode: "http", Algorithm: alg, Port: port, ProxyProtocol: "none", TimeoutServerS: 60,
		HcProtocol: "tcp", HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/"}, StatusCodes: []string{"2??"}}
	for _, s := range servers {
		be.Servers = append(be.Servers, &state.Server{Target: db.Target{ID: s.id, BalancerID: 1, Type: "ip", Ip: s.ip, Weight: s.weight, AdminState: s.state},
			Slots: []*state.Slot{{ID: s.id, ServerID: s.id, Address: s.ip}}})
	}
	m := int64(0)
	if maint {
		m = 1
	}
	fe := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 1, NodeID: 1, Mode: "http", TimeoutClientS: 60, ForwardedHeaders: 1, Enabled: 1, Maintenance: m,
		DefaultBackendID: sql.NullInt64{Int64: 1, Valid: true}}, Binds: []state.Bind{{Address: "*", Port: 8080}}}
	return state.TestSnapshot([]*state.Frontend{fe}, []*state.Backend{be})
}

func prep(t *testing.T, s *state.Snapshot) *applied {
	in := render.Input{Root: "/live", NodeID: 1, Frontends: s.FrontendsOnNode(1), Backends: s.BackendsOnNode(1), Cert: s.Cert}
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
	one := srvSpec{1, "10.0.0.1", 100, "ready"}
	base := mk("roundrobin", false, 80, one)
	cases := []struct {
		name   string
		from   *state.Snapshot
		to     *state.Snapshot
		kind   string
		prefix []string
	}{
		{"add server", base, mk("roundrobin", false, 80, one, srvSpec{2, "10.0.0.2", 100, "ready"}), "runtime",
			[]string{"add server be_1/s2 10.0.0.2:80 id 2 check", "enable health be_1/s2", "enable server be_1/s2"}},
		{"remove server", base, mk("roundrobin", false, 80), "runtime", []string{"set server be_1/s1 state maint", "wait 5s srv-removable", "del server be_1/s1"}},
		{"weight", base, mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 50, "ready"}), "runtime", []string{"set server be_1/s1 weight 50"}},
		{"lower weight", base, mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 10, "ready"}), "runtime", []string{"set server be_1/s1 weight 10"}},
		{"restore default weight", mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 10, "ready"}), base, "runtime", []string{"set server be_1/s1 weight 100"}},
		{"drain", base, mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 100, "drain"}), "runtime", []string{"set server be_1/s1 state drain"}},
		{"maintenance", base, mk("roundrobin", true, 80, one), "runtime", []string{"add map /live/maps/maint.map 1 1"}},
		{"algorithm", base, mk("leastconn", false, 80, one), "reload", nil},
		{"backend port (server lines only)", base, mk("roundrobin", false, 9090, one), "reload", nil},
		{"source hash add", mk("source", false, 80, one), mk("source", false, 80, one, srvSpec{2, "10.0.0.2", 100, "ready"}), "reload", nil},
	}
	for _, c := range cases {
		kind, cmds := run(t, c.from, c.to)
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

func TestClassifyProxyProtocolReloads(t *testing.T) {
	from := mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 100, "ready"})
	to := mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 100, "ready"})
	to.Backend(1).ProxyProtocol = "v2"
	if kind, ops := run(t, from, to); kind != "reload" {
		t.Fatalf("proxy protocol change: %s %v", kind, ops)
	}
}

// Drain does not change any file, but must still produce runtime ops.
func TestStateSigDetectsDrain(t *testing.T) {
	a := mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 100, "ready"})
	b := mk("roundrobin", false, 80, srvSpec{1, "10.0.0.1", 100, "drain"})
	pa, pb := prep(t, a), prep(t, b)
	if hashFiles(pa.out.Files) != hashFiles(pb.out.Files) {
		t.Fatal("expected identical files for a drain-only change")
	}
	if stateSig(a, 1) == stateSig(b, 1) {
		t.Fatal("state signature must differ")
	}
}
