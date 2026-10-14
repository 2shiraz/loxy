package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/app"
	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/config"
)

type tc struct {
	t    *testing.T
	srv  *httptest.Server
	hc   *http.Client
	csrf string
}

func (c *tc) req(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, c.srv.URL+"/api/v1"+path, rd)
	if c.csrf != "" {
		r.Header.Set("X-LOXY-CSRF", c.csrf)
	}
	resp, err := c.hc.Do(r)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func setup(t *testing.T) (*tc, *app.App) {
	dir := t.TempDir()
	t.Setenv("LOXY_OWNER_PASSWORD", "unit-test-password")
	cfg := &config.Config{Listen: "127.0.0.1:0", PanelPort: 8443, DataDir: dir, HAProxyConfDir: filepath.Join(dir, "conf"),
		HAProxyRunDir: dir, HAProxyStateDir: dir, HAProxyBin: "true", StatsInterval: time.Second, Dev: true, NoHAProxy: true,
		ACMEDirectory: "https://127.0.0.1:1/dir", SecretKey: make([]byte, 32)}
	a, err := app.New(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.API.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &tc{t: t, srv: srv, hc: &http.Client{Jar: jar}}, a
}

func login(c *tc) {
	st, b := c.req("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "unit-test-password"})
	if st != 200 {
		c.t.Fatalf("login %d %v", st, b)
	}
	c.csrf = b["csrf_token"].(string)
}

func fields(b map[string]any) map[string]bool {
	out := map[string]bool{}
	e, _ := b["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	fs, _ := d["fields"].([]any)
	for _, f := range fs {
		out[f.(map[string]any)["name"].(string)] = true
	}
	return out
}

func TestAuthAndCSRF(t *testing.T) {
	c, _ := setup(t)
	if st, _ := c.req("GET", "/projects", nil); st != 401 {
		t.Fatalf("unauthenticated: %d", st)
	}
	if st, _ := c.req("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "wrong"}); st != 401 {
		t.Fatalf("bad password: %d", st)
	}
	login(c)
	csrf := c.csrf
	c.csrf = ""
	if st, _ := c.req("POST", "/projects", map[string]any{"name": "x"}); st != 403 {
		t.Fatalf("missing csrf: %d", st)
	}
	c.csrf = csrf
	if st, b := c.req("GET", "/auth/me", nil); st != 200 || b["user"].(map[string]any)["role"] != "owner" {
		t.Fatalf("me: %d %v", st, b)
	}
	// viewer cannot mutate
	if st, b := c.req("POST", "/users", map[string]any{"email": "v@x.io", "password": "viewer-password-1", "role": "viewer"}); st != 201 {
		t.Fatalf("create user %d %v", st, b)
	}
	jar, _ := cookiejar.New(nil)
	v := &tc{t: t, srv: c.srv, hc: &http.Client{Jar: jar}}
	_, b := v.req("POST", "/auth/login", map[string]any{"email": "v@x.io", "password": "viewer-password-1"})
	v.csrf = b["csrf_token"].(string)
	if st, _ := v.req("POST", "/projects", map[string]any{"name": "x"}); st != 403 {
		t.Fatalf("viewer mutate: %d", st)
	}
	if st, _ := v.req("GET", "/projects", nil); st != 200 {
		t.Fatalf("viewer read: %d", st)
	}
}

// mkProject creates a project (own auto port, http → destination 8080) with IP targets.
func mkProject(c *tc, name string, ips ...string) (float64, map[string]any) {
	c.t.Helper()
	var targets []any
	for _, ip := range ips {
		targets = append(targets, map[string]any{"type": "ip", "ip": ip})
	}
	body := map[string]any{"name": name, "service": map[string]any{"protocol": "http", "destination_port": 8080}}
	if len(targets) > 0 {
		body["targets"] = targets
	}
	st, b := c.req("POST", "/projects", body)
	if st != 201 {
		c.t.Fatalf("project %s: %d %v", name, st, b)
	}
	p := b["project"].(map[string]any)
	return p["id"].(float64), p
}

func TestProjectLifecycle(t *testing.T) {
	c, a := setup(t)
	login(c)
	id, p := mkProject(c, "shop", "10.0.0.5", "10.0.0.6")
	// a new project gets its own free port (20000+) and shows it as an endpoint
	eps := p["endpoints"].([]any)
	if len(eps) != 1 || !strings.Contains(eps[0].(map[string]any)["url"].(string), ":20000") {
		t.Fatalf("endpoints %v", eps)
	}
	if cnt := p["counts"].(map[string]any); cnt["targets"].(float64) != 2 || cnt["services"].(float64) != 1 {
		t.Fatalf("counts %v", cnt)
	}
	// the second project gets the next port
	_, p2 := mkProject(c, "api")
	if u := p2["endpoints"].([]any)[0].(map[string]any)["url"].(string); !strings.Contains(u, ":20001") {
		t.Fatalf("second auto port: %s", u)
	}
	// duplicate name
	if st, b := c.req("POST", "/projects", map[string]any{"name": "SHOP"}); st != 422 || !fields(b)["name"] {
		t.Fatalf("duplicate name: %d %v", st, b)
	}
	// service validation (§8.5): reserved/taken ports, sticky on tcp, timeout ≥ interval, bad path, https without cert
	svc := fmt.Sprintf("/projects/%v/services", id)
	st, b := c.req("POST", svc, map[string]any{"protocol": "tcp", "listen_port": 20001, "destination_port": 1, "sticky_sessions": true,
		"health_check": map[string]any{"protocol": "http", "interval_s": 5, "timeout_s": 5, "http": map[string]any{"path": "no-slash"}}})
	f := fields(b)
	for _, want := range []string{"listen_port", "sticky_sessions", "health_check.timeout_s", "health_check.http.path"} {
		if !f[want] {
			t.Errorf("expected field error %s, got %v (%d)", want, f, st)
		}
	}
	for _, port := range []int{80, 443, 8443} {
		if st, b = c.req("POST", svc, map[string]any{"protocol": "http", "listen_port": port, "destination_port": 80}); st != 422 || !fields(b)["listen_port"] {
			t.Fatalf("reserved port %d: %d %v", port, st, b)
		}
	}
	if st, b = c.req("POST", svc, map[string]any{"protocol": "https", "destination_port": 8080}); st != 422 || !fields(b)["certificates"] {
		t.Fatalf("https without cert: %d %v", st, b)
	}
	st, b = c.req("POST", svc, map[string]any{"protocol": "tcp", "listen_port": 15432, "destination_port": 5432})
	if st != 201 {
		t.Fatalf("tcp service: %d %v", st, b)
	}
	tcpID := b["service"].(map[string]any)["id"].(float64)
	// targets: multicast rejected, ip:port accepted, label selectors resolve against hosts
	tg := fmt.Sprintf("/projects/%v/targets", id)
	if st, b = c.req("POST", tg, map[string]any{"targets": []any{map[string]any{"type": "ip", "ip": "224.0.0.1"}}}); st != 422 {
		t.Fatalf("multicast target: %d %v", st, b)
	}
	if st, b = c.req("POST", tg, map[string]any{"targets": []any{map[string]any{"type": "ip", "ip": "10.0.0.7:9000"}}}); st != 201 ||
		b["targets"].([]any)[0].(map[string]any)["port"].(float64) != 9000 {
		t.Fatalf("ip:port target: %d %v", st, b)
	}
	if st, b = c.req("POST", "/hosts", map[string]any{"name": "h1", "ipv4": "10.2.0.1", "labels": map[string]string{"role": "web"}}); st != 201 {
		t.Fatalf("host %d %v", st, b)
	}
	st, b = c.req("POST", tg, map[string]any{"targets": []any{map[string]any{"type": "label_selector", "label_selector": "role=web"}}})
	if st != 201 || len(b["targets"].([]any)[0].(map[string]any)["resolved"].([]any)) != 1 {
		t.Fatalf("selector: %d %v", st, b)
	}
	// every resolved target carries one check per service
	_, b = c.req("GET", tg, nil)
	r0 := b["targets"].([]any)[0].(map[string]any)["resolved"].([]any)[0].(map[string]any)
	if len(r0["checks"].([]any)) != 2 {
		t.Fatalf("checks per service: %v", r0)
	}
	// maintenance + rename
	if st, b = c.req("PATCH", fmt.Sprintf("/projects/%v", id), map[string]any{"maintenance": true, "name": "shop-2"}); st != 200 ||
		b["project"].(map[string]any)["maintenance"] != true || b["project"].(map[string]any)["name"] != "shop-2" {
		t.Fatalf("patch: %d %v", st, b)
	}
	// config shows the project's services only
	r, _ := c.hc.Get(c.srv.URL + fmt.Sprintf("/api/v1/projects/%v/config", id))
	cfgText, _ := io.ReadAll(r.Body)
	r.Body.Close()
	for _, want := range []string{"frontend fe_1 ", "bind 0.0.0.0:20000", fmt.Sprintf("frontend fe_%v from loxy_tcp", tcpID), "backend be_1 "} {
		if !bytes.Contains(cfgText, []byte(want)) {
			t.Fatalf("config missing %q:\n%s", want, cfgText)
		}
	}
	if bytes.Contains(cfgText, []byte("sys_panel")) || bytes.Contains(cfgText, []byte("fe_2 ")) {
		t.Fatalf("config leaks other sections:\n%s", cfgText)
	}
	// metrics per project and per service
	if st, b = c.req("GET", fmt.Sprintf("/projects/%v/metrics?type=requests_per_second", id), nil); st != 200 || b["series"] == nil {
		t.Fatalf("metrics %d %v", st, b)
	}
	if st, _ = c.req("GET", fmt.Sprintf("/projects/%v/metrics?service_id=999", id), nil); st != 422 {
		t.Fatalf("foreign service metrics: %d", st)
	}
	if st, b = c.req("GET", "/audit", nil); st != 200 || len(b["audit"].([]any)) < 5 {
		t.Fatalf("audit %d %v", st, b)
	}
	if st, _ = c.req("DELETE", fmt.Sprintf("/projects/%v/services/%v", id, tcpID), nil); st != 204 {
		t.Fatalf("delete service: %d", st)
	}
	if st, _ = c.req("DELETE", fmt.Sprintf("/projects/%v", id), nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if a.Snap.Get().Project(int64(id)) != nil || a.Snap.Get().Frontend(1) != nil {
		t.Fatal("still in snapshot")
	}
}

func TestCertificatesAndDomains(t *testing.T) {
	c, a := setup(t)
	login(c)
	crt, key, _ := certs.SelfSigned("app.example.com", []string{"app.example.com"}, 48*time.Hour)
	st, b := c.req("POST", "/certificates", map[string]any{"name": "app", "type": "uploaded", "certificate": crt, "private_key": key})
	if st != 201 {
		t.Fatalf("upload %d %v", st, b)
	}
	certID := b["certificate"].(map[string]any)["id"].(float64)
	_, key2, _ := certs.SelfSigned("x", nil, time.Hour)
	if st, b = c.req("POST", "/certificates", map[string]any{"name": "bad", "type": "uploaded", "certificate": crt, "private_key": key2}); st != 422 {
		t.Fatalf("mismatched key: %d %v", st, b)
	}
	if st, b = c.req("POST", "/certificates", map[string]any{"name": "w", "type": "managed", "domains": []string{"*.example.com"}, "challenge": "http01"}); st != 422 {
		t.Fatalf("wildcard http01: %d %v", st, b)
	}
	id, _ := mkProject(c, "web", "10.0.0.5")
	st, b = c.req("POST", fmt.Sprintf("/projects/%v/services", id), map[string]any{"protocol": "https", "destination_port": 8080, "certificates": []float64{certID, certID}})
	if st != 422 || !fields(b)["certificates"] {
		t.Fatalf("duplicate certs: %d %v", st, b)
	}
	if st, b = c.req("POST", fmt.Sprintf("/projects/%v/services", id), map[string]any{"protocol": "https", "destination_port": 8080, "certificates": []float64{certID}}); st != 201 {
		t.Fatalf("https service %d %v", st, b)
	}
	// cert in use cannot be deleted
	if st, _ = c.req("DELETE", fmt.Sprintf("/certificates/%v", certID), nil); st != 409 {
		t.Fatalf("delete in-use cert: %d", st)
	}
	// domains attach to a project; required records = the server's addresses
	c.req("POST", "/nodes/1/ips", map[string]any{"address": "192.0.2.10"})
	c.req("POST", "/nodes/1/ips", map[string]any{"address": "2001:db8::10"})
	if st, b = c.req("POST", "/domains", map[string]any{"fqdn": "app.invalid"}); st != 422 || !fields(b)["project_id"] {
		t.Fatalf("domain without project: %d %v", st, b)
	}
	st, b = c.req("POST", "/domains", map[string]any{"fqdn": "app.invalid", "project_id": id})
	if st != 201 {
		t.Fatalf("domain %d %v", st, b)
	}
	d := b["domain"].(map[string]any)
	if d["status"] == "ok" || d["https"] != true || d["url"] != "http://app.invalid" || d["service_id"] == nil {
		t.Fatalf("unexpected domain view: %v", d)
	}
	types := map[string]any{}
	for _, r := range d["required_records"].([]any) {
		types[r.(map[string]any)["type"].(string)] = r.(map[string]any)["value"]
	}
	if types["A"] != "192.0.2.10" || types["AAAA"] != "2001:db8::10" {
		t.Fatalf("required records %v", types)
	}
	// not resolving → no automatic certificate, manual issue refused
	if d["certificate_id"] != nil {
		t.Fatalf("certificate issued for an unresolved domain: %v", d)
	}
	if st, b = c.req("POST", fmt.Sprintf("/domains/%v/actions/issue_certificate", d["id"]), nil); st != 422 {
		t.Fatalf("issue before verified: %d %v", st, b)
	}
	if st, b = c.req("POST", "/domains", map[string]any{"fqdn": "*.example.com", "project_id": id}); st != 422 {
		t.Fatalf("wildcard without provider: %d %v", st, b)
	}
	if st, b = c.req("POST", "/domains", map[string]any{"fqdn": "app.invalid", "project_id": id}); st != 422 || !fields(b)["fqdn"] {
		t.Fatalf("duplicate domain: %d %v", st, b)
	}
	// the domain is routed on the shared :80 frontend to the project's first http service
	out, err := a.Rec.RenderLive(context.Background())
	if err != nil || !strings.Contains(out.Config(), "frontend sys_http") || !strings.Contains(out.Config(), "use_backend be_1 if { req.hdr(host),field(1,:),lower -m str app.invalid }") {
		t.Fatalf("domain routing: %v\n%s", err, out.Config())
	}
	// the last http/https service cannot go while domains need it
	_, b = c.req("GET", fmt.Sprintf("/projects/%v/services", id), nil)
	for _, sv := range b["services"].([]any) {
		sid := sv.(map[string]any)["id"].(float64)
		st, _ := c.req("DELETE", fmt.Sprintf("/projects/%v/services/%v", id, sid), nil)
		if sid == 1 && st != 204 {
			t.Fatalf("delete first web service while another exists: %d", st)
		}
		if sid != 1 && st != 409 {
			t.Fatalf("delete last web service with domains: %d", st)
		}
	}
	if st, b = c.req("POST", "/dns_providers", map[string]any{"name": "x", "kind": "nope", "credentials": map[string]string{}}); st != 422 {
		t.Fatalf("bad provider kind: %d %v", st, b)
	}
	if st, b = c.req("GET", "/dns_providers", nil); st != 200 || len(b["kinds"].([]any)) < 3 {
		t.Fatalf("kinds: %d %v", st, b)
	}
}

func TestAPITokens(t *testing.T) {
	c, _ := setup(t)
	login(c)
	st, b := c.req("POST", "/api_tokens", map[string]any{"name": "ci", "role": "operator", "expires_in_days": 30})
	if st != 201 {
		t.Fatalf("create token %d %v", st, b)
	}
	tok := b["token"].(string)
	call := func(method, path, token string, body any) int {
		var rd io.Reader
		if body != nil {
			bb, _ := json.Marshal(body)
			rd = bytes.NewReader(bb)
		}
		r, _ := http.NewRequest(method, c.srv.URL+"/api/v1"+path, rd)
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if s := call("POST", "/projects", tok, map[string]any{"name": "via-token"}); s != 201 {
		t.Fatalf("operator token create project: %d", s)
	}
	if s := call("POST", "/users", tok, map[string]any{"email": "a@b.c", "password": "xxxxxxxxxxxxxx", "role": "viewer"}); s != 403 {
		t.Fatalf("operator token admin action: %d", s)
	}
	if s := call("GET", "/projects", tok+"x", nil); s != 401 {
		t.Fatalf("bad token: %d", s)
	}
	id := b["api_token"].(map[string]any)["id"].(float64)
	if st, _ := c.req("DELETE", fmt.Sprintf("/api_tokens/%v", id), nil); st != 204 {
		t.Fatal("revoke")
	}
	if s := call("GET", "/projects", tok, nil); s != 401 {
		t.Fatalf("revoked token: %d", s)
	}
}

func TestTOTP(t *testing.T) {
	c, a := setup(t)
	login(c)
	st, b := c.req("POST", "/auth/totp/setup", nil)
	if st != 200 {
		t.Fatalf("setup %d %v", st, b)
	}
	secret := b["secret"].(string)
	if st, _ = c.req("POST", "/auth/totp/enable", map[string]any{"code": "000000"}); st != 422 {
		t.Fatalf("bad code accepted: %d", st)
	}
	code := totpNow(secret)
	if st, b = c.req("POST", "/auth/totp/enable", map[string]any{"code": code}); st != 204 {
		t.Fatalf("enable %d %v", st, b)
	}
	jar, _ := cookiejar.New(nil)
	c2 := &tc{t: t, srv: c.srv, hc: &http.Client{Jar: jar}}
	st, b = c2.req("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "unit-test-password"})
	if st != 401 || b["error"].(map[string]any)["code"] != "totp_required" {
		t.Fatalf("expected totp_required: %d %v", st, b)
	}
	if st, _ = c2.req("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "unit-test-password", "totp": totpNow(secret)}); st != 200 {
		t.Fatalf("login with totp: %d", st)
	}
	_ = a
}

// Review findings #3, #5, #8.
func TestReviewRegressions(t *testing.T) {
	c, _ := setup(t)
	login(c)
	// #5: IPv6 zone with injected config is rejected.
	if st, b := c.req("POST", "/nodes/1/ips", map[string]any{"address": "2001:db8::1%x\n    http-request deny"}); st != 422 {
		t.Fatalf("zone accepted: %d %v", st, b)
	}
	id, _ := mkProject(c, "m")
	// #3: absurd metric ranges are clamped instead of exhausting memory.
	st, b := c.req("GET", fmt.Sprintf("/projects/%v/metrics?start=1&end=9000000000000000000&type=open_connections,bogus", id), nil)
	if st != 200 || len(b["timestamps"].([]any)) > 2100 || len(b["series"].(map[string]any)) != 1 {
		t.Fatalf("metrics clamp: %d points=%d series=%v", st, len(b["timestamps"].([]any)), b["series"] != nil)
	}
	// #8: viewers cannot list users.
	c.req("POST", "/users", map[string]any{"email": "ro@x.io", "password": "viewer-password-1", "role": "viewer"})
	jar, _ := cookiejar.New(nil)
	v := &tc{t: t, srv: c.srv, hc: &http.Client{Jar: jar}}
	_, lr := v.req("POST", "/auth/login", map[string]any{"email": "ro@x.io", "password": "viewer-password-1"})
	v.csrf = lr["csrf_token"].(string)
	if st, _ := v.req("GET", "/users", nil); st != 403 {
		t.Fatalf("viewer GET /users: %d", st)
	}
	if st, _ := v.req("POST", "/auth/totp/setup", nil); st != 200 {
		t.Fatalf("viewer 2FA setup: %d", st)
	}
	// owner cannot be deleted (and is not logged out)
	if st, _ := c.req("DELETE", "/users/1", nil); st != 403 && st != 409 {
		t.Fatalf("owner delete: %d", st)
	}
	if st, _ := c.req("GET", "/auth/me", nil); st != 200 {
		t.Fatal("owner session revoked")
	}
}
