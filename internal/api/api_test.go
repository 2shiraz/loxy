package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
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
	if st, _ := c.req("GET", "/load_balancers", nil); st != 401 {
		t.Fatalf("unauthenticated: %d", st)
	}
	if st, _ := c.req("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "wrong"}); st != 401 {
		t.Fatalf("bad password: %d", st)
	}
	login(c)
	csrf := c.csrf
	c.csrf = ""
	if st, _ := c.req("POST", "/load_balancers", map[string]any{"name": "x", "plan_id": 1}); st != 403 {
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
	if st, _ := v.req("POST", "/load_balancers", map[string]any{"name": "x", "plan_id": 1}); st != 403 {
		t.Fatalf("viewer mutate: %d", st)
	}
	if st, _ := v.req("GET", "/load_balancers", nil); st != 200 {
		t.Fatalf("viewer read: %d", st)
	}
}

func TestLoadBalancerLifecycle(t *testing.T) {
	c, a := setup(t)
	login(c)
	st, b := c.req("POST", "/load_balancers", map[string]any{"name": "lb-a", "plan_id": 1, "labels": map[string]string{"env": "prod"},
		"services": []any{map[string]any{"protocol": "http", "listen_port": 80, "destination_port": 8080}},
		"targets":  []any{map[string]any{"type": "ip", "ip": "10.0.0.5"}, map[string]any{"type": "ip", "ip": "10.0.0.6"}}})
	if st != 201 {
		t.Fatalf("create %d %v", st, b)
	}
	lb := b["load_balancer"].(map[string]any)
	id := int64(lb["id"].(float64))
	if u := lb["usage"].(map[string]any); u["targets"].(float64) != 2 || u["services"].(float64) != 1 {
		t.Fatalf("usage %v", u)
	}
	// validation (§8.7): duplicate port, sticky on tcp, timeout ≥ interval, bad path, https without cert
	st, b = c.req("POST", fmt.Sprintf("/load_balancers/%d/services", id), map[string]any{"protocol": "tcp", "listen_port": 80, "destination_port": 1,
		"http":         map[string]any{"sticky_sessions": true},
		"health_check": map[string]any{"protocol": "http", "interval_s": 5, "timeout_s": 5, "http": map[string]any{"path": "no-slash"}}})
	f := fields(b)
	for _, want := range []string{"listen_port", "http.sticky_sessions", "health_check.timeout_s", "health_check.http.path"} {
		if !f[want] {
			t.Errorf("expected field error %s, got %v (%d)", want, f, st)
		}
	}
	st, b = c.req("POST", fmt.Sprintf("/load_balancers/%d/services", id), map[string]any{"protocol": "https", "listen_port": 443, "destination_port": 8080})
	if st != 422 || !fields(b)["http.certificates"] {
		t.Fatalf("https without cert: %d %v", st, b)
	}
	// targets: loopback rejected, quota enforced (lb11: 25)
	if st, b = c.req("POST", fmt.Sprintf("/load_balancers/%d/targets", id), map[string]any{"targets": []any{map[string]any{"type": "ip", "ip": "224.0.0.1"}}}); st != 422 {
		t.Fatalf("multicast target: %d %v", st, b)
	}
	var many []any
	for i := 0; i < 24; i++ {
		many = append(many, map[string]any{"type": "ip", "ip": fmt.Sprintf("10.1.0.%d", i+1)})
	}
	if st, b = c.req("POST", fmt.Sprintf("/load_balancers/%d/targets", id), map[string]any{"targets": many}); st != 422 || b["error"].(map[string]any)["code"] != "quota_exceeded" {
		t.Fatalf("quota: %d %v", st, b)
	}
	// label selector targets resolve against inventory
	if st, b = c.req("POST", "/servers", map[string]any{"name": "s1", "public_ipv4": "10.2.0.1", "labels": map[string]string{"role": "web"}}); st != 201 {
		t.Fatalf("server %d %v", st, b)
	}
	st, b = c.req("POST", fmt.Sprintf("/load_balancers/%d/targets", id), map[string]any{"targets": []any{map[string]any{"type": "label_selector", "label_selector": "role=web"}}})
	if st != 201 || len(b["targets"].([]any)[0].(map[string]any)["resolved"].([]any)) != 1 {
		t.Fatalf("selector: %d %v", st, b)
	}
	// rescale down blocked by usage? lb11 is smallest; change to lb21 and back works
	if st, _ = c.req("POST", fmt.Sprintf("/load_balancers/%d/actions/change_plan", id), map[string]any{"plan_id": 2}); st != 200 {
		t.Fatalf("rescale: %d", st)
	}
	// delete protection
	if st, _ = c.req("PATCH", fmt.Sprintf("/load_balancers/%d", id), map[string]any{"delete_protected": true}); st != 200 {
		t.Fatal("protect")
	}
	if st, _ = c.req("DELETE", fmt.Sprintf("/load_balancers/%d", id), nil); st != 423 {
		t.Fatalf("protected delete: %d", st)
	}
	c.req("PATCH", fmt.Sprintf("/load_balancers/%d", id), map[string]any{"delete_protected": false})
	// config endpoint renders this LB's sections only
	r, _ := c.hc.Get(c.srv.URL + fmt.Sprintf("/api/v1/load_balancers/%d/config", id))
	cfgText, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.Contains(cfgText, []byte(fmt.Sprintf("frontend lb%d_s", id))) || bytes.Contains(cfgText, []byte("sys_panel")) {
		t.Fatalf("config:\n%s", cfgText)
	}
	// audit trail
	if st, b = c.req("GET", "/audit", nil); st != 200 || len(b["audit"].([]any)) < 5 {
		t.Fatalf("audit %d %v", st, b)
	}
	if st, _ = c.req("DELETE", fmt.Sprintf("/load_balancers/%d", id), nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if a.Snap.Get().LB(id) != nil {
		t.Fatal("still in snapshot")
	}
}

func TestCertificatesAndDomains(t *testing.T) {
	c, _ := setup(t)
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
	st, b = c.req("POST", "/load_balancers", map[string]any{"name": "tls", "plan_id": 1,
		"services": []any{map[string]any{"protocol": "https", "listen_port": 443, "destination_port": 80, "http": map[string]any{"redirect_http": true, "certificates": []float64{certID}}}}})
	if st != 201 {
		t.Fatalf("tls lb %d %v", st, b)
	}
	lbID := b["load_balancer"].(map[string]any)["id"].(float64)
	// redirect occupies :80
	if st, b = c.req("POST", fmt.Sprintf("/load_balancers/%v/services", lbID), map[string]any{"protocol": "http", "listen_port": 80, "destination_port": 80}); st != 422 {
		t.Fatalf(":80 collision: %d %v", st, b)
	}
	// cert in use cannot be deleted
	if st, _ = c.req("DELETE", fmt.Sprintf("/certificates/%v", certID), nil); st != 409 {
		t.Fatalf("delete in-use cert: %d", st)
	}
	// domains
	st, b = c.req("POST", "/domains", map[string]any{"fqdn": "app.invalid", "lb_id": lbID})
	if st != 201 {
		t.Fatalf("domain %d %v", st, b)
	}
	d := b["domain"].(map[string]any)
	if d["status"] == "ok" {
		t.Fatalf("unexpected ok: %v", d)
	}
	if st, b = c.req("POST", fmt.Sprintf("/domains/%v/actions/issue_certificate", d["id"]), nil); st != 422 {
		t.Fatalf("issue before verified: %d %v", st, b)
	}
	if st, b = c.req("POST", "/domains", map[string]any{"fqdn": "*.example.com", "lb_id": lbID}); st != 422 {
		t.Fatalf("wildcard without provider: %d %v", st, b)
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
	if s := call("POST", "/load_balancers", tok, map[string]any{"name": "via-token", "plan_id": 1}); s != 201 {
		t.Fatalf("operator token create LB: %d", s)
	}
	if s := call("POST", "/users", tok, map[string]any{"email": "a@b.c", "password": "xxxxxxxxxxxxxx", "role": "viewer"}); s != 403 {
		t.Fatalf("operator token admin action: %d", s)
	}
	if s := call("GET", "/load_balancers", tok+"x", nil); s != 401 {
		t.Fatalf("bad token: %d", s)
	}
	id := b["api_token"].(map[string]any)["id"].(float64)
	if st, _ := c.req("DELETE", fmt.Sprintf("/api_tokens/%v", id), nil); st != 204 {
		t.Fatal("revoke")
	}
	if s := call("GET", "/load_balancers", tok, nil); s != 401 {
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

// Review findings #3, #4, #5, #8.
func TestReviewRegressions(t *testing.T) {
	c, _ := setup(t)
	login(c)
	// #5: IPv6 zone with injected config is rejected.
	if st, b := c.req("POST", "/nodes/1/ips", map[string]any{"address": "2001:db8::1%x\n    http-request deny"}); st != 422 {
		t.Fatalf("zone accepted: %d %v", st, b)
	}
	// #4: dual-stack LB gets both a v4 and a v6 address.
	c.req("POST", "/nodes/1/ips", map[string]any{"address": "192.0.2.10", "kind": "additional"})
	c.req("POST", "/nodes/1/ips", map[string]any{"address": "2001:db8::10", "kind": "additional"})
	st, b := c.req("POST", "/load_balancers", map[string]any{"name": "dual", "plan_id": 1})
	if st != 201 {
		t.Fatalf("dual-stack create: %d %v", st, b)
	}
	pn := b["load_balancer"].(map[string]any)["public_net"].(map[string]any)
	if pn["ipv4"] != "192.0.2.10" || pn["ipv6"] != "2001:db8::10" {
		t.Fatalf("public_net %v", pn)
	}
	id := b["load_balancer"].(map[string]any)["id"].(float64)
	// #3: absurd metric ranges are clamped instead of exhausting memory.
	st, b = c.req("GET", fmt.Sprintf("/load_balancers/%v/metrics?start=1&end=9000000000000000000&type=open_connections,bogus", id), nil)
	if st != 200 || len(b["timestamps"].([]any)) > 2100 || len(b["series"].(map[string]any)) != 1 {
		t.Fatalf("metrics clamp: %d points=%d series=%v", st, len(b["timestamps"].([]any)), b["series"] != nil)
	}
	// #8: viewers cannot list users.
	c.req("POST", "/users", map[string]any{"email": "ro@x.io", "password": "viewer-password-1", "role": "viewer"})
	jar, _ := cookiejar.New(nil)
	v := &tc{t: t, srv: c.srv, hc: &http.Client{Jar: jar}}
	_, lb := v.req("POST", "/auth/login", map[string]any{"email": "ro@x.io", "password": "viewer-password-1"})
	v.csrf = lb["csrf_token"].(string)
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
