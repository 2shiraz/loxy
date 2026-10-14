package render

import (
	"strings"
	"testing"

	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// FuzzUserValues feeds arbitrary user-controlled strings through the validators the API uses;
// whatever passes must render without adding config lines or breaking quoting (§14.4).
func FuzzUserValues(f *testing.F) {
	f.Add("/health", "OK", "app.example.com", "LBSTICKY", "2??")
	f.Add("/a b", "x'y", "EVIL\nbind :1", "a;b", "999")
	f.Add("/x\n    server evil 1.2.3.4:1", "OK\n", "a.b", "c", "20?")
	f.Fuzz(func(t *testing.T, path, resp, domain, cookie, code string) {
		if !model.ValidHCPath(path) || (resp != "" && !model.ValidHCResponse(resp)) || (domain != "" && !model.ValidHostname(domain, false)) || !model.ValidCookieName(cookie) {
			return
		}
		if _, err := model.StatusCodesToHAProxy([]string{code}); err != nil {
			return
		}
		lb := &state.LB{LoadBalancer: db.LoadBalancer{ID: 1, Algorithm: "roundrobin", PublicEnabled: 1}, Plan: &state.Plan{Name: "p", MaxConnections: 10, EnforceLbCap: 1}}
		lb.Services = []*state.Service{{Service: db.Service{ID: 1, LbID: 1, Protocol: "http", ListenPort: 80, DestinationPort: 80, ProxyProtocol: "none",
			HttpTimeoutS: 60, StickyEnabled: 1, StickyCookieName: cookie, StickyCookieLifetimeS: 300, HcProtocol: "http", HcPort: 80,
			HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: path, HcResponse: resp, HcDomain: domain}, StatusCodes: []string{code}}}
		lb.Targets = []*state.Target{{Target: db.Target{ID: 1, Type: "ip", Ip: "10.0.0.1", Weight: 100, AdminState: "ready"}, Slots: []*state.Slot{{ID: 1, TargetID: 1, Address: "10.0.0.1"}}}}
		base := *lb.Services[0]
		base.HcPath, base.HcResponse, base.HcDomain, base.StickyCookieName = "/", "x", "a.b", "c"
		ref := &state.LB{LoadBalancer: lb.LoadBalancer, Plan: lb.Plan, Targets: lb.Targets, Services: []*state.Service{&base}}
		out, err := Render(Input{Root: "/l", LBs: []*state.LB{lb}, Cert: func(int64) *state.Cert { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		want, _ := Render(Input{Root: "/l", LBs: []*state.LB{ref}, Cert: func(int64) *state.Cert { return nil }})
		if strings.Count(out.Config(), "\n") != strings.Count(want.Config(), "\n") {
			t.Fatalf("user values changed the line structure:\n%s", out.Config())
		}
		for _, l := range strings.Split(out.Config(), "\n") {
			if strings.Contains(l, "http-check expect string") && strings.Count(l, "'") != 2 {
				t.Fatalf("quoting broken: %q", l)
			}
		}
	})
}
