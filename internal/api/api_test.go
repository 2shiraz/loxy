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
	if st, _ := c.req("GET", "/frontends", nil); st != 401 {
		t.Fatalf("unauthenticated: %d", st)
	}
	if st, _ := c.req("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "wrong"}); st != 401 {
		t.Fatalf("bad password: %d", st)
	}
	login(c)
	csrf := c.csrf
	c.csrf = ""
	if st, _ := c.req("POST", "/backends", map[string]any{"name": "x", "mode": "http", "port": 80}); st != 403 {
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
	if st, _ := v.req("POST", "/backends", map[string]any{"name": "x", "mode": "http", "port": 80}); st != 403 {
		t.Fatalf("viewer mutate: %d", st)
	}
	if st, _ := v.req("GET", "/frontends", nil); st != 200 {
		t.Fatalf("viewer read: %d", st)
	}
}

// mkBackend creates an http backend with the given IP servers and returns its id.
func mkBackend(c *tc, name string, ips ...string) float64 {
	c.t.Helper()
	st, b := c.req("POST", "/backends", map[string]any{"name": name, "mode": "http", "port": 8080})
	if st != 201 {
		c.t.Fatalf("backend %s: %d %v", name, st, b)
	}
	id := b["backend"].(map[string]any)["id"].(float64)
	if len(ips) > 0 {
		var srv []any
		for _, ip := range ips {
			srv = append(srv, map[string]any{"type": "ip", "ip": ip})
		}
		if st, b = c.req("POST", fmt.Sprintf("/backends/%v/servers", id), map[string]any{"servers": srv}); st != 201 {
			c.t.Fatalf("servers: %d %v", st, b)
		}
	}
	return id
}

func TestFrontendBackendLifecycle(t *testing.T) {
	c, a := setup(t)
	login(c)
	web := mkBackend(c, "web", "10.0.0.5", "10.0.0.6")
	api := mkBackend(c, "api", "10.0.0.7")
	// backend validation (§8.4): timeout ≥ interval, bad path, sticky on tcp
	st, b := c.req("POST", "/backends", map[string]any{"name": "bad", "mode": "tcp", "port": 1, "sticky_sessions": true,
		"health_check": map[string]any{"protocol": "http", "interval_s": 5, "timeout_s": 5, "http": map[string]any{"path": "no-slash"}}})
	f := fields(b)
	for _, want := range []string{"sticky_sessions", "health_check.timeout_s", "health_check.http.path"} {
		if !f[want] {
			t.Errorf("expected field error %s, got %v (%d)", want, f, st)
		}
	}
	// servers: multicast rejected; label selectors resolve against the host inventory
	if st, b = c.req("POST", fmt.Sprintf("/backends/%v/servers", web), map[string]any{"servers": []any{map[string]any{"type": "ip", "ip": "224.0.0.1"}}}); st != 422 {
		t.Fatalf("multicast server: %d %v", st, b)
	}
	if st, b = c.req("POST", "/hosts", map[string]any{"name": "h1", "ipv4": "10.2.0.1", "labels": map[string]string{"role": "web"}}); st != 201 {
		t.Fatalf("host %d %v", st, b)
	}
	st, b = c.req("POST", fmt.Sprintf("/backends/%v/servers", web), map[string]any{"servers": []any{map[string]any{"type": "label_selector", "label_selector": "role=web"}}})
	if st != 201 || len(b["servers"].([]any)[0].(map[string]any)["resolved"].([]any)) != 1 {
		t.Fatalf("selector: %d %v", st, b)
	}
	// frontend validation (§8.2): tls without cert, panel port, routes need a backend of the same mode
	tcpBE := 0.0
	if st, b = c.req("POST", "/backends", map[string]any{"name": "pg", "mode": "tcp", "port": 5432}); st == 201 {
		tcpBE = b["backend"].(map[string]any)["id"].(float64)
	}
	st, b = c.req("POST", "/frontends", map[string]any{"name": "x", "mode": "http", "tls": true, "binds": []any{map[string]any{"address": "*", "port": 8443}},
		"default_backend_id": tcpBE})
	f = fields(b)
	for _, want := range []string{"certificates", "binds[0].port", "default_backend_id"} {
		if !f[want] {
			t.Errorf("expected field error %s, got %v (%d)", want, f, st)
		}
	}
	st, b = c.req("POST", "/frontends", map[string]any{"name": "edge", "mode": "http", "labels": map[string]string{"env": "prod"},
		"binds": []any{map[string]any{"address": "*", "port": 80}}, "default_backend_id": web,
		"routes": []any{map[string]any{"hosts": []string{"api.example.com"}, "path_prefix": "/v1", "backend_id": api}}})
	if st != 201 {
		t.Fatalf("create frontend %d %v", st, b)
	}
	fe := b["frontend"].(map[string]any)
	id := fe["id"].(float64)
	if len(fe["routes"].([]any)) != 1 {
		t.Fatalf("routes %v", fe["routes"])
	}
	// a second frontend on the same port collides
	if st, b = c.req("POST", "/frontends", map[string]any{"name": "dup", "mode": "http", "binds": []any{map[string]any{"address": "*", "port": 80}}, "default_backend_id": web}); st != 422 || !fields(b)["binds"] {
		t.Fatalf("bind collision: %d %v", st, b)
	}
	// a backend in use cannot be deleted
	if st, _ = c.req("DELETE", fmt.Sprintf("/backends/%v", api), nil); st != 409 {
		t.Fatalf("delete in-use backend: %d", st)
	}
	// maintenance toggle
	if st, b = c.req("PATCH", fmt.Sprintf("/frontends/%v", id), map[string]any{"maintenance": true}); st != 200 || b["frontend"].(map[string]any)["maintenance"] != true {
		t.Fatalf("maintenance: %d %v", st, b)
	}
	// config endpoint renders this frontend's sections only
	r, _ := c.hc.Get(c.srv.URL + fmt.Sprintf("/api/v1/frontends/%v/config", id))
	cfgText, _ := io.ReadAll(r.Body)
	r.Body.Close()
	for _, want := range []string{fmt.Sprintf("frontend fe_%v", id), fmt.Sprintf("use_backend be_%v", api), fmt.Sprintf("default_backend be_%v", web)} {
		if !bytes.Contains(cfgText, []byte(want)) {
			t.Fatalf("config missing %q:\n%s", want, cfgText)
		}
	}
	if bytes.Contains(cfgText, []byte("sys_panel")) {
		t.Fatalf("config leaks system sections:\n%s", cfgText)
	}
	// backend status lists resolved servers (2 IPs + 1 host)
	if st, b = c.req("GET", fmt.Sprintf("/backends/%v/status", web), nil); st != 200 || len(b["servers"].([]any)) != 3 {
		t.Fatalf("status %d %v", st, b)
	}
	if st, b = c.req("GET", "/audit", nil); st != 200 || len(b["audit"].([]any)) < 5 {
		t.Fatalf("audit %d %v", st, b)
	}
	if st, _ = c.req("DELETE", fmt.Sprintf("/frontends/%v", id), nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if a.Snap.Get().Frontend(int64(id)) != nil {
		t.Fatal("still in snapshot")
	}
	if st, _ = c.req("DELETE", fmt.Sprintf("/backends/%v", api), nil); st != 204 {
		t.Fatalf("delete freed backend: %d", st)
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
	be := mkBackend(c, "app", "10.0.0.5")
	st, b = c.req("POST", "/frontends", map[string]any{"name": "tls", "mode": "http", "tls": true, "http_redirect": true, "certificates": []float64{certID},
		"binds": []any{map[string]any{"address": "*", "port": 443}}, "default_backend_id": be})
	if st != 201 {
		t.Fatalf("tls frontend %d %v", st, b)
	}
	feID := b["frontend"].(map[string]any)["id"].(float64)
	// the redirect occupies :80
	if st, b = c.req("POST", "/frontends", map[string]any{"name": "plain", "mode": "http", "binds": []any{map[string]any{"address": "*", "port": 80}}, "default_backend_id": be}); st != 422 {
		t.Fatalf(":80 collision: %d %v", st, b)
	}
	// cert in use cannot be deleted
	if st, _ = c.req("DELETE", fmt.Sprintf("/certificates/%v", certID), nil); st != 409 {
		t.Fatalf("delete in-use cert: %d", st)
	}
	// domains
	st, b = c.req("POST", "/domains", map[string]any{"fqdn": "app.invalid", "frontend_id": feID})
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
	if st, b = c.req("POST", "/domains", map[string]any{"fqdn": "*.example.com", "frontend_id": feID}); st != 422 {
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
	if s := call("POST", "/backends", tok, map[string]any{"name": "via-token", "mode": "http", "port": 80}); s != 201 {
		t.Fatalf("operator token create backend: %d", s)
	}
	if s := call("POST", "/users", tok, map[string]any{"email": "a@b.c", "password": "xxxxxxxxxxxxxx", "role": "viewer"}); s != 403 {
		t.Fatalf("operator token admin action: %d", s)
	}
	if s := call("GET", "/frontends", tok+"x", nil); s != 401 {
		t.Fatalf("bad token: %d", s)
	}
	id := b["api_token"].(map[string]any)["id"].(float64)
	if st, _ := c.req("DELETE", fmt.Sprintf("/api_tokens/%v", id), nil); st != 204 {
		t.Fatal("revoke")
	}
	if s := call("GET", "/frontends", tok, nil); s != 401 {
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
	// #4: a wildcard frontend on a dual-stack node needs both A and AAAA records.
	c.req("POST", "/nodes/1/ips", map[string]any{"address": "192.0.2.10"})
	c.req("POST", "/nodes/1/ips", map[string]any{"address": "2001:db8::10"})
	be := mkBackend(c, "dual")
	st, b := c.req("POST", "/frontends", map[string]any{"name": "dual", "mode": "http", "binds": []any{map[string]any{"address": "*", "port": 80}}, "default_backend_id": be})
	if st != 201 {
		t.Fatalf("dual-stack create: %d %v", st, b)
	}
	id := b["frontend"].(map[string]any)["id"].(float64)
	st, b = c.req("POST", "/domains", map[string]any{"fqdn": "dual.invalid", "frontend_id": id})
	if st != 201 {
		t.Fatalf("domain %d %v", st, b)
	}
	types := map[string]any{}
	for _, r := range b["domain"].(map[string]any)["required_records"].([]any) {
		types[r.(map[string]any)["type"].(string)] = r.(map[string]any)["value"]
	}
	if types["A"] != "192.0.2.10" || types["AAAA"] != "2001:db8::10" {
		t.Fatalf("required records %v", types)
	}
	// #3: absurd metric ranges are clamped instead of exhausting memory.
	st, b = c.req("GET", fmt.Sprintf("/frontends/%v/metrics?start=1&end=9000000000000000000&type=open_connections,bogus", id), nil)
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

// Review round 2: listener collisions via enable/domains, duplicate certs, health-check defaults.
func TestFrontendBackendRegressions(t *testing.T) {
	c, _ := setup(t)
	login(c)
	be := mkBackend(c, "pool")
	// http backends default to http checks; an explicit check port equal to the backend port is kept.
	st, b := c.req("POST", "/backends", map[string]any{"name": "hc", "mode": "http", "port": 80, "health_check": map[string]any{"port": 80}})
	if st != 201 {
		t.Fatalf("backend %d %v", st, b)
	}
	hc := b["backend"].(map[string]any)["health_check"].(map[string]any)
	if hc["protocol"] != "http" || hc["port"] != float64(80) {
		t.Fatalf("health check %v", hc)
	}
	if st, b = c.req("POST", "/backends", map[string]any{"name": "fast", "mode": "http", "port": 80, "health_check": map[string]any{"interval_s": 2, "timeout_s": 1}}); st != 422 || !fields(b)["health_check.interval_s"] {
		t.Fatalf("interval < 3 accepted: %d %v", st, b)
	}
	// duplicate certificate ids are a field error, not a 500.
	crt, key, _ := certs.SelfSigned("d.example.com", []string{"d.example.com"}, 48*time.Hour)
	_, b = c.req("POST", "/certificates", map[string]any{"name": "d", "type": "uploaded", "certificate": crt, "private_key": key})
	cid := b["certificate"].(map[string]any)["id"].(float64)
	if st, b = c.req("POST", "/frontends", map[string]any{"name": "dup", "mode": "http", "tls": true, "certificates": []float64{cid, cid},
		"binds": []any{map[string]any{"address": "*", "port": 443}}, "default_backend_id": be}); st != 422 || !fields(b)["certificates"] {
		t.Fatalf("duplicate certs: %d %v", st, b)
	}
	// a disabled frontend on a taken port cannot be enabled.
	mk := func(name string, port int, extra map[string]any) float64 {
		body := map[string]any{"name": name, "mode": "http", "binds": []any{map[string]any{"address": "*", "port": port}}, "default_backend_id": be}
		for k, v := range extra {
			body[k] = v
		}
		st, b := c.req("POST", "/frontends", body)
		if st != 201 {
			t.Fatalf("frontend %s: %d %v", name, st, b)
		}
		return b["frontend"].(map[string]any)["id"].(float64)
	}
	mk("a", 80, nil)
	off := mk("b", 80, map[string]any{"enabled": false})
	if st, b = c.req("PATCH", fmt.Sprintf("/frontends/%v", off), map[string]any{"enabled": true}); st != 422 || !fields(b)["enabled"] {
		t.Fatalf("enable onto taken port: %d %v", st, b)
	}
	// attaching a domain adds a :80 ACME listener, which collides with frontend a.
	tcp := mk("c", 8081, nil)
	if st, b = c.req("POST", "/domains", map[string]any{"fqdn": "c.invalid", "frontend_id": tcp}); st != 422 || !fields(b)["frontend_id"] {
		t.Fatalf("domain ACME :80 collision: %d %v", st, b)
	}
}
