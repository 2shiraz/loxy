//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/app"
	"github.com/2shiraz/loxy/internal/config"
	"github.com/2shiraz/loxy/internal/testutil"
)

// backend starts a tiny HTTP responder container answering `body` on :9001.
func backend(t *testing.T, dir, body string) string {
	cfg := filepath.Join(dir, body+".cfg")
	_ = os.WriteFile(cfg, []byte("defaults\n  mode http\n  timeout client 30s\n  timeout server 30s\n  timeout connect 5s\nfrontend f\n  bind :9001\n  http-request return status 200 content-type text/plain string \""+body+"\"\n"), 0o644)
	name := fmt.Sprintf("loxy-it-be-%s-%d", body, time.Now().UnixNano())
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "-v", cfg+":/b.cfg:ro", testutil.HAProxyImage, "haproxy", "-f", "/b.cfg").CombinedOutput(); err != nil {
		t.Fatalf("backend: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	out, _ := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name).Output()
	return strings.TrimSpace(string(out))
}

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+"/api/v1"+path, rd)
	if c.csrf != "" {
		req.Header.Set("X-LOXY-CSRF", c.csrf)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

type stack struct {
	a    *app.App
	c    *client
	dir  string
	lbIP string
}

// startStack builds the app with temp dirs, applies bootstrap, starts HAProxy and logs in.
func startStack(t *testing.T, tweak func(*config.Config)) *stack {
	dockerOK(t)
	dir := t.TempDir()
	for _, d := range []string{"conf", "run", "state", "data"} {
		_ = os.MkdirAll(filepath.Join(dir, d), 0o777)
		_ = os.Chmod(filepath.Join(dir, d), 0o777)
	}
	wrapper := filepath.Join(dir, "haproxy.sh")
	_ = os.WriteFile(wrapper, []byte("#!/bin/sh\nexec docker run --rm --network none --user 0:0 -v "+dir+":"+dir+":ro "+testutil.HAProxyImage+" haproxy \"$@\"\n"), 0o755)
	t.Setenv("LOXY_OWNER_PASSWORD", "integration-test-pw")
	t.Setenv("LOXY_HAPROXY_MAXCONN", "4000")
	t.Setenv("LOXY_DRAIN_TIMEOUT", "2s")
	cfg := &config.Config{Listen: "127.0.0.1:0", PanelPort: 18443, DataDir: filepath.Join(dir, "data"), HAProxyConfDir: filepath.Join(dir, "conf"),
		HAProxyRunDir: filepath.Join(dir, "run"), HAProxyStateDir: filepath.Join(dir, "state"), HAProxyBin: wrapper, StatsInterval: time.Second,
		Dev: true, ACMEDirectory: "https://127.0.0.1:1/dir", SecretKey: make([]byte, 32)}
	if tweak != nil {
		tweak(cfg)
	}
	a, err := app.New(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Rec.Apply(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("loxy-it-apply-%d", time.Now().UnixNano())
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "--user", "0:0", "--group-add", "1500", "-v", dir+":"+dir,
		testutil.HAProxyImage, "haproxy", "-W", "-db", "-S", filepath.Join(dir, "run", "master.sock")+",mode,660,gid,1500",
		"-f", filepath.Join(dir, "conf", "live", "haproxy.cfg")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Logf("haproxy logs:\n%s", out)
		}
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})
	out, _ := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name).Output()
	lbIP := strings.TrimSpace(string(out))
	waitFor(t, 15*time.Second, "admin socket", func() bool { _, err := os.Stat(filepath.Join(dir, "run", "admin.sock")); return err == nil })
	go a.Metrics.Run(ctx)

	srv := httptest.NewServer(a.API.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: srv.URL, hc: &http.Client{Jar: jar}}
	st, body := c.do("POST", "/auth/login", map[string]any{"email": "admin@loxy.local", "password": "integration-test-pw"})
	if st != 200 {
		t.Fatalf("login %d %v", st, body)
	}
	c.csrf = body["csrf_token"].(string)
	return &stack{a: a, c: c, dir: dir, lbIP: lbIP}
}

// TestApplyPathZeroDowntime drives the real API + reconciler against HAProxy 3.4:
// create LB (reload), add/remove targets (runtime, no reload), repeated reloads under load.
func TestApplyPathZeroDowntime(t *testing.T) {
	s := startStack(t, nil)
	a, c, dir, lbIP := s.a, s.c, s.dir, s.lbIP
	ctx := context.Background()
	b1, b2 := backend(t, dir, "one"), backend(t, dir, "two")
	var st int
	var body map[string]any
	st, body = c.do("POST", "/load_balancers", map[string]any{"name": "it", "plan_id": 1,
		"services": []any{map[string]any{"protocol": "http", "listen_port": 8080, "destination_port": 9001,
			"health_check": map[string]any{"protocol": "http", "interval_s": 3, "timeout_s": 1, "retries": 1, "http": map[string]any{"path": "/"}}}},
		"targets": []any{map[string]any{"type": "ip", "ip": b1}}})
	if st != 201 {
		t.Fatalf("create %d %v", st, body)
	}
	lbID := int64(body["load_balancer"].(map[string]any)["id"].(float64))
	apply := func(wantKind string) {
		t.Helper()
		if err := a.Rec.Apply(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		revs, _ := a.Store.Q.ListRevisions(ctx, 1)
		if wantKind != "" && revs[0].Kind != wantKind {
			t.Fatalf("expected %s apply, got %s (%s)", wantKind, revs[0].Kind, revs[0].HaproxyOutput)
		}
	}
	apply("reload")
	url := fmt.Sprintf("http://%s:8080/", lbIP)
	waitFor(t, 10*time.Second, "lb serving", func() bool {
		r, err := http.Get(url)
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})

	// Load generator.
	var okN, failN atomic.Int64
	var lastErr atomic.Value
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hc := &http.Client{Timeout: 5 * time.Second}
			for {
				select {
				case <-stop:
					return
				default:
				}
				r, err := hc.Get(url)
				if err != nil {
					failN.Add(1)
					lastErr.Store(err.Error())
					continue
				}
				_, _ = io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if r.StatusCode != 200 {
					failN.Add(1)
					lastErr.Store(fmt.Sprint("status ", r.StatusCode))
					continue
				}
				okN.Add(1)
			}
		}()
	}
	// Runtime: add and remove a target under load — no reload allowed.
	st, body = c.do("POST", fmt.Sprintf("/load_balancers/%d/targets", lbID), map[string]any{"targets": []any{map[string]any{"type": "ip", "ip": b2}}})
	if st != 201 {
		t.Fatalf("add target %d %v", st, body)
	}
	apply("runtime")
	time.Sleep(4 * time.Second) // let the new server pass its first check
	tid := int64(body["targets"].([]any)[0].(map[string]any)["id"].(float64))
	st, _ = c.do("DELETE", fmt.Sprintf("/load_balancers/%d/targets/%d", lbID, tid), nil)
	if st != 204 {
		t.Fatalf("delete target %d", st)
	}
	apply("runtime") // drain
	waitFor(t, 10*time.Second, "target removed", func() bool { return a.Snap.Get().LB(lbID).Target(tid) == nil })
	apply("runtime") // del server
	// Repeated reloads under load (algorithm flip forces a reload).
	reloads := 20
	if v := os.Getenv("LOXY_IT_RELOADS"); v != "" {
		fmt.Sscan(v, &reloads)
	}
	algs := []string{"leastconn", "roundrobin"}
	for i := 0; i < reloads; i++ {
		if st, b := c.do("PATCH", fmt.Sprintf("/load_balancers/%d", lbID), map[string]any{"algorithm": algs[i%2]}); st != 200 {
			t.Fatalf("patch %d %v", st, b)
		}
		apply("reload")
	}
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	t.Logf("requests ok=%d failed=%d over %d reloads + runtime add/remove", okN.Load(), failN.Load(), reloads)
	if failN.Load() != 0 {
		t.Fatalf("%d failed requests (last: %v)", failN.Load(), lastErr.Load())
	}
	if okN.Load() < 100 {
		t.Fatalf("load generator too slow: %d", okN.Load())
	}
}
