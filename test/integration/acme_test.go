//go:build integration

package integration

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/config"
)

// TestACMEHTTP01Pebble: TLS frontend with a managed certificate → lego HTTP-01 via the
// virt@acme_http01 map → Pebble-issued certificate served on :443 and 301 on :80 (§8.7, §11.2).
// Needs `pebble` and `pebble-challtestsrv` on PATH (go install github.com/letsencrypt/pebble/v2/cmd/...).
func TestACMEHTTP01Pebble(t *testing.T) {
	dockerOK(t)
	pebbleBin, err1 := exec.LookPath("pebble")
	ctsBin, err2 := exec.LookPath("pebble-challtestsrv")
	if err1 != nil || err2 != nil {
		t.Skip("pebble / pebble-challtestsrv not installed")
	}
	pdir := t.TempDir()
	c, k, _ := certs.SelfSigned("localhost", []string{"localhost"}, 24*time.Hour)
	_ = os.WriteFile(filepath.Join(pdir, "cert.pem"), []byte(c), 0o644)
	_ = os.WriteFile(filepath.Join(pdir, "key.pem"), []byte(k), 0o644)
	_ = os.WriteFile(filepath.Join(pdir, "pebble.json"), []byte(fmt.Sprintf(`{"pebble":{"listenAddress":"127.0.0.1:14000","managementListenAddress":"127.0.0.1:15000",
"certificate":"%s/cert.pem","privateKey":"%s/key.pem","httpPort":80,"tlsPort":443,"externalAccountBindingRequired":false}}`, pdir, pdir)), 0o644)

	s := startStack(t, func(cfg *config.Config) {
		cfg.ACMEDirectory, cfg.ACMEInsecure, cfg.ACMEEmail = "https://127.0.0.1:14000/dir", true, "it@example.com"
	})
	cts := exec.Command(ctsBin, "-defaultIPv4", s.haIP, "-defaultIPv6", "", "-dnsserver", "127.0.0.1:18053", "-http01", "", "-https01", "", "-tlsalpn01", "", "-doh", "", "-management", "127.0.0.1:18055")
	if err := cts.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cts.Process.Kill() })
	peb := exec.Command(pebbleBin, "-config", filepath.Join(pdir, "pebble.json"), "-dnsserver", "127.0.0.1:18053")
	peb.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0")
	var plog strings.Builder
	peb.Stdout, peb.Stderr = &plog, &plog
	if err := peb.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = peb.Process.Kill()
		if t.Failed() {
			t.Logf("pebble:\n%s", plog.String())
		}
	})
	time.Sleep(time.Second)
	ctx := t.Context()
	go s.a.Rec.Run(ctx)
	go s.a.Issuer.RunScheduler(ctx)

	st, body := s.c.do("POST", "/certificates", map[string]any{"name": "app", "type": "managed", "domains": []string{"app.loxy.test"}, "challenge": "http01"})
	if st != 201 {
		t.Fatalf("cert %d %v", st, body)
	}
	certID := body["certificate"].(map[string]any)["id"].(float64)
	st, body = s.c.do("POST", "/backends", map[string]any{"name": "app", "mode": "http", "port": 9001})
	if st != 201 {
		t.Fatalf("backend %d %v", st, body)
	}
	beID := body["backend"].(map[string]any)["id"].(float64)
	st, body = s.c.do("POST", "/frontends", map[string]any{"name": "tls", "mode": "http", "tls": true, "http_redirect": true,
		"certificates": []float64{certID}, "binds": []any{map[string]any{"address": "*", "port": 443}}, "default_backend_id": beID})
	if st != 201 {
		t.Fatalf("frontend %d %v", st, body)
	}
	start := time.Now()
	var status string
	waitFor(t, 90*time.Second, "certificate issued", func() bool {
		_, b := s.c.do("GET", fmt.Sprintf("/certificates/%v", certID), nil)
		cert := b["certificate"].(map[string]any)
		status = cert["status"].(string)
		if status == "failed" {
			t.Fatalf("issuance failed: %v", cert["last_error"])
		}
		return status == "issued"
	})
	t.Logf("issued after %v", time.Since(start))
	var issuer string
	waitFor(t, 15*time.Second, "issued cert served on :443", func() bool {
		conn, err := tls.Dial("tcp", s.haIP+":443", &tls.Config{ServerName: "app.loxy.test", InsecureSkipVerify: true}) //nolint:gosec
		if err != nil {
			return false
		}
		defer conn.Close()
		issuer = conn.ConnectionState().PeerCertificates[0].Issuer.CommonName
		return strings.Contains(strings.ToLower(issuer), "pebble")
	})
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := hc.Get("http://" + s.haIP + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 301 {
		t.Fatalf("redirect: %d", r.StatusCode)
	}
	revs, _ := s.a.Store.Q.ListRevisions(ctx, 1)
	t.Logf("served cert issuer %q; revisions: %d", issuer, len(revs))
}
