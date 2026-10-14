package render

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// FuzzUserValues feeds arbitrary user-controlled strings through the validators the API uses;
// whatever passes must render without adding config lines or breaking quoting (§14.4).
func FuzzUserValues(f *testing.F) {
	f.Add("/health", "OK", "app.example.com", "LBSTICKY", "2??", "api.example.com", "/v1")
	f.Add("/a b", "x'y", "EVIL\nbind :1", "a;b", "999", "a b", "/x y")
	f.Add("/x\n    server evil 1.2.3.4:1", "OK\n", "a.b", "c", "20?", "*.x.io\nfoo", "/p\n")
	f.Fuzz(func(t *testing.T, path, resp, domain, cookie, code, host, prefix string) {
		if !model.ValidHCPath(path) || (resp != "" && !model.ValidHCResponse(resp)) || (domain != "" && !model.ValidHostname(domain, false)) ||
			!model.ValidCookieName(cookie) || !model.ValidHostname(host, true) || (prefix != "" && !model.ValidRoutePath(prefix)) {
			return
		}
		if _, err := model.StatusCodesToHAProxy([]string{code}); err != nil {
			return
		}
		mk := func(path, resp, domain, cookie, host, prefix string) Input {
			be := &state.Backend{BackendCfg: state.BackendCfg{ID: 1, Mode: "http", Algorithm: "roundrobin", Port: 80, ProxyProtocol: "none", TimeoutServerS: 60,
				StickyEnabled: 1, StickyCookieName: cookie, StickyCookieLifetimeS: 300, HcProtocol: "http", HcPort: 80, HcIntervalS: 15, HcTimeoutS: 10,
				HcRetries: 3, HcPath: path, HcResponse: resp, HcDomain: domain}, StatusCodes: []string{code}}
			be.Servers = []*state.Server{{Target: db.Target{ID: 1, Type: "ip", Ip: "10.0.0.1", Weight: 100, AdminState: "ready"},
				Slots: []*state.Slot{{ID: 1, ServerID: 1, Address: "10.0.0.1"}}}}
			fe := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 1, Mode: "http", TimeoutClientS: 60, ForwardedHeaders: 1, Enabled: 1,
				DefaultBackendID: sql.NullInt64{Int64: 1, Valid: true}}, Binds: []state.Bind{{Address: "*", Port: 80}},
				Routes: []state.Route{{Hosts: []string{host}, PathPrefix: prefix, BackendID: 1}}}
			return Input{Root: "/l", Frontends: []*state.Frontend{fe}, Backends: []*state.Backend{be}}
		}
		out, err := Render(mk(path, resp, domain, cookie, host, prefix))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := Render(mk("/", "x", "a.b", "c", "a.b", "/p"))
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
