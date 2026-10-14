package render

import (
	"context"
	"database/sql"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/haproxy/validate"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/testutil"
)

var update = flag.Bool("update", false, "update golden files")

func input(t *testing.T, root string) Input {
	c, k, err := certs.SelfSigned("app.example.com", []string{"app.example.com"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fes, bes, cf := fixture(c, k)
	return Input{
		Root: root, NodeID: 1, Revision: 123, DesiredVersion: 456, PanelPort: 8443, PanelAllow: []string{"10.0.0.0/8", "192.0.2.1"},
		Promex: true, Frontends: fes, Backends: bes, Cert: cf, PanelPEM: c + k, PlaceholderPEM: c + k, IPv6: true,
		Domains: []DomainRoute{{Host: "shop.example.com", BackendID: 4, CertID: 12, HTTPS: true}, {Host: "*.api.example.com", BackendID: 5},
			{Host: "beta.api.example.com", BackendID: 4}},
	}
}

func TestGolden(t *testing.T) {
	out, err := Render(input(t, "/usr/local/etc/haproxy/live"))
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "reference.cfg")
	if *update {
		if err := os.WriteFile(golden, out.Files["haproxy.cfg"], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != out.Config() {
		t.Fatalf("golden mismatch; run go test ./internal/haproxy/render -update\n--- got ---\n%s", out.Config())
	}
	for _, f := range []string{"crt-lists/fe3.lst", "certs/12.pem", "maps/maint.map", "maps/panel_allow.lst", "errors/maint.http"} {
		if _, ok := out.Files[f]; !ok {
			t.Errorf("missing file %s", f)
		}
	}
	if got := string(out.Files["maps/maint.map"]); got != "7 1\n" {
		t.Errorf("maint map %q", got)
	}
	in := input(t, "/x")
	sec := FrontendSections(out.Config(), in.Frontends[0])
	if !strings.Contains(sec, "frontend fe_3 ") || !strings.Contains(sec, "backend be_5 ") || strings.Contains(sec, "fe_7") || strings.Contains(sec, "be_6") {
		t.Errorf("FrontendSections wrong:\n%s", sec)
	}
}

// TestHAProxyCheck validates the rendered tree with the pinned HAProxy binary (§18).
func TestHAProxyCheck(t *testing.T) {
	dir := t.TempDir()
	out, err := Render(input(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	WriteTree(t, dir, out)
	v := &validate.Validator{Cmd: testutil.HAProxyCmd(t, dir)}
	if msg, err := v.Check(context.Background(), filepath.Join(dir, "haproxy.cfg")); err != nil {
		t.Fatalf("%v\n%s\n%s", err, msg, out.Config())
	}
}

func WriteTree(t testing.TB, dir string, out *Output) {
	t.Helper()
	for p, b := range out.Files {
		fp := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecureCookieOnlyWhenAllFrontendsTLS(t *testing.T) {
	be := func(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }
	tls := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 1, Mode: "http", Tls: 1, Enabled: 1, DefaultBackendID: be(5)}}
	plain := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 2, Mode: "http", Enabled: 1, DefaultBackendID: be(6)}, Routes: []state.Route{{BackendID: 5}}}
	off := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 3, Mode: "http", Enabled: 0, DefaultBackendID: be(7)}}
	tlsOnly := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 4, Mode: "http", Tls: 1, Enabled: 1, DefaultBackendID: be(7)}}
	got := tlsOnlyBackends([]*state.Frontend{tls, plain, off, tlsOnly}, nil)
	if got[5] || got[6] || !got[7] {
		t.Fatalf("tlsOnly = %v (5: shared with plain HTTP, 6: plain only, 7: TLS + disabled plain)", got)
	}
	// a plain-HTTP domain (no certificate yet) also reaches backend 7: no secure cookie
	if got = tlsOnlyBackends([]*state.Frontend{tlsOnly}, []DomainRoute{{Host: "a.example.com", BackendID: 7}}); got[7] {
		t.Fatalf("plain domain kept secure: %v", got)
	}
}
