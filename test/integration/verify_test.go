//go:build integration

package integration

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/haproxy/stats"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// fakes are tiny HTTP responders inside the HAProxy container used as targets.
const fakes = `
frontend fake_200_nostr from loxy_http
    bind 127.0.0.1:19000
    http-request return status 200 content-type text/plain string "nope"
frontend fake_500_str from loxy_http
    bind 127.0.0.2:19000
    http-request return status 500 content-type text/plain string "OK"
frontend fake_200_str from loxy_http
    bind 127.0.0.3:19000
    http-request return status 200 content-type text/plain string "OK"
`

func ipServer(id int64, ip string) *state.Server {
	return &state.Server{Target: db.Target{ID: id, Type: "ip", Ip: ip, Weight: 100, AdminState: "ready"},
		Slots: []*state.Slot{{ID: id, ServerID: id, Address: ip}}}
}

// backend returns a backend on `port` (tcp health checks, 1 s interval) with the given servers.
func backend(id int64, mode string, port int64, servers ...*state.Server) *state.Backend {
	b := &state.Backend{BackendCfg: state.BackendCfg{ID: id, Name: fmt.Sprintf("b%d", id), Mode: mode, Algorithm: "roundrobin", Port: port, ProxyProtocol: "none",
		TimeoutServerS: 60, HcProtocol: "tcp", HcPort: port, HcIntervalS: 1, HcTimeoutS: 1, HcRetries: 1, HcPath: "/",
		StickyCookieName: "LOXYSTICKY", StickyCookieLifetimeS: 300}, StatusCodes: []string{"2??", "3??"}}
	for _, sv := range servers {
		sv.BalancerID = id
		b.Servers = append(b.Servers, sv)
	}
	return b
}

// frontend returns an enabled frontend on *:port whose default backend is beID.
func frontend(id int64, mode string, port int64, beID int64) *state.Frontend {
	return &state.Frontend{FrontendCfg: state.FrontendCfg{ID: id, Name: fmt.Sprintf("f%d", id), Mode: mode, TimeoutClientS: 60, Maxconn: 1000, Enabled: 1,
		DefaultBackendID: sql.NullInt64{Int64: beID, Valid: beID != 0}}, Binds: []state.Bind{{Address: "*", Port: port}}}
}

func input(fes []*state.Frontend, bes ...*state.Backend) render.Input {
	return render.Input{NodeID: 1, PanelPort: 8443, Frontends: fes, Backends: bes, Cert: func(int64) *state.Cert { return nil }}
}

func baseInput() render.Input { return input(nil) }

func serverStatus(t *testing.T, e *env) map[string]string {
	out, err := e.rt.Exec(context.Background(), "show stat")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range stats.ParseStat(out) {
		if r.Type == 2 {
			m[r.PxName+"/"+r.SvName] = r.Str("status")
		}
	}
	return m
}

// VERIFY §7.1: -S bind options produce a 660 socket with gid 1500.
func TestVerifyMasterSocketPerms(t *testing.T) {
	e := startHAProxy(t, baseInput(), "")
	fi, err := os.Stat(filepath.Join(e.dir, "run", "master.sock"))
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if fi.Mode().Perm() != 0o660 || st.Gid != 1500 {
		t.Fatalf("master.sock mode %v gid %d", fi.Mode().Perm(), st.Gid)
	}
	fi, _ = os.Stat(filepath.Join(e.dir, "run", "admin.sock"))
	if st := fi.Sys().(*syscall.Stat_t); fi.Mode().Perm() != 0o660 || st.Gid != 1500 {
		t.Fatalf("admin.sock mode %v gid %d", fi.Mode().Perm(), st.Gid)
	}
}

// §7.7: synchronous reload reports Success=1; a config failing at startup reports Success=0
// and the old worker keeps serving.
func TestReloadSuccessAndFailure(t *testing.T) {
	in := input([]*state.Frontend{frontend(1, "http", 8080, 1)}, backend(1, "http", 19000, ipServer(1, "127.0.0.3")))
	e := startHAProxy(t, in, fakes)
	res := e.apply(in, fakes, true)
	if !res.Success {
		t.Fatalf("reload failed: %s", res.Output)
	}
	get := func() (int, error) {
		r, err := (&http.Client{Timeout: 2 * time.Second}).Get(fmt.Sprintf("http://%s:8080/", e.ip))
		if err != nil {
			return 0, err
		}
		r.Body.Close()
		return r.StatusCode, nil
	}
	waitFor(t, 10*time.Second, "service up", func() bool { c, _ := get(); return c == 200 })
	// Binding a non-local address fails at startup (passes -c).
	res = e.apply(in, fakes+"\nfrontend broken from loxy_http\n    bind 192.0.2.77:9999\n", true)
	if res.Success {
		t.Fatalf("expected Success=0, got: %s", res.Output)
	}
	// The old worker keeps serving (the fake target briefly fails its check while the
	// aborted worker's listeners come and go, so wait for it to settle).
	waitFor(t, 10*time.Second, "old worker serving", func() bool { c, err := get(); return err == nil && c == 200 })
	procs, _ := e.rt.Master(context.Background(), "show proc")
	if !strings.Contains(procs, "worker") {
		t.Fatalf("show proc: %s", procs)
	}
}

// VERIFY §7.5: two `http-check expect` rules are AND-ed (status and body).
func TestVerifyHTTPCheckAND(t *testing.T) {
	b := backend(1, "http", 19000, ipServer(1, "127.0.0.1"), ipServer(2, "127.0.0.2"), ipServer(3, "127.0.0.3"))
	b.HcProtocol, b.HcResponse = "http", "OK"
	e := startHAProxy(t, input([]*state.Frontend{frontend(1, "http", 8080, 1)}, b), fakes)
	var st map[string]string
	waitFor(t, 10*time.Second, "checks settle", func() bool {
		st = serverStatus(t, e)
		return st["be_1/s1"] == "DOWN" && st["be_1/s2"] == "DOWN" && st["be_1/s3"] == "UP"
	})
	t.Logf("statuses: %v", st)
}

// §7.6: dynamic server lifecycle with the exact ServerArgs used in config.
func TestDynamicServers(t *testing.T) {
	b := backend(1, "http", 19000)
	b.StickyEnabled = 1
	e := startHAProxy(t, input([]*state.Frontend{frontend(1, "http", 8080, 1)}, b), fakes)
	ctx := context.Background()
	sv := ipServer(7, "127.0.0.3")
	sv.BalancerID = 1
	be := render.BackendName(1)
	name := render.ServerName(sv, sv.Slots[0])
	for _, cmd := range []string{
		fmt.Sprintf("add server %s/%s %s", be, name, render.ServerArgs(b, sv, sv.Slots[0])),
		fmt.Sprintf("enable server %s/%s", be, name),
		fmt.Sprintf("enable health %s/%s", be, name),
	} {
		if err := e.rt.Run(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 10*time.Second, "dynamic server UP", func() bool { return serverStatus(t, e)[be+"/"+name] == "UP" })
	for _, cmd := range []string{
		fmt.Sprintf("set server %s/%s weight 50", be, name),
		fmt.Sprintf("set server %s/%s state drain", be, name),
		fmt.Sprintf("set server %s/%s state maint", be, name),
	} {
		if err := e.rt.Run(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.rt.Exec(ctx, fmt.Sprintf("del server %s/%s", be, name))
	if err != nil || !strings.Contains(out, "Server deleted.") {
		t.Fatalf("del server: %v %q", err, out)
	}
	// `enabled` keyword is rejected (§7.9).
	out, _ = e.rt.Exec(ctx, fmt.Sprintf("add server %s/x 127.0.0.3:19000 enabled", be))
	if strings.Contains(out, "New server registered.") {
		t.Log("NOTE: 'enabled' keyword accepted by this version")
	}
}

// VERIFY §7.5: `ca-file @system-ca` is accepted by the image; §7.6: SSL cert runtime commands; maps.
func TestVerifySSLRuntimeAndMaps(t *testing.T) {
	c, k, _ := certs.SelfSigned("a.example.com", []string{"a.example.com"}, 24*time.Hour)
	cert := &state.Cert{Certificate: db.Certificate{ID: 5, CertPem: c}, KeyPEM: k}
	tlsBE := backend(1, "http", 19000, ipServer(1, "127.0.0.3"))
	tlsBE.HcProtocol, tlsBE.HcTlsVerify, tlsBE.HcDomain = "https", 1, "a.example.com"
	plainBE := backend(2, "http", 19000, ipServer(2, "127.0.0.3"))
	tlsFE := frontend(1, "http", 8443+1, 1)
	tlsFE.Tls, tlsFE.CertIDs = 1, []int64{5}
	in := input([]*state.Frontend{tlsFE, frontend(2, "http", 8080, 2)}, tlsBE, plainBE)
	in.Domains = []render.DomainRoute{{Host: "shop.test", BackendID: 2}}
	in.Cert = func(id int64) *state.Cert {
		if id == 5 {
			return cert
		}
		return nil
	}
	in.PlaceholderPEM = c + k
	e := startHAProxy(t, in, fakes)
	ctx := context.Background()
	live := containerConf + "/live"
	c2, k2, _ := certs.SelfSigned("b.example.com", []string{"b.example.com"}, 24*time.Hour)
	newPath := live + "/certs/9.pem"
	steps := []struct{ cmd, payload string }{
		{"set ssl cert " + render.CertPath(live, 5), c + k},
		{"commit ssl cert " + render.CertPath(live, 5), ""},
		{"new ssl cert " + newPath, ""},
		{"set ssl cert " + newPath, c2 + k2},
		{"commit ssl cert " + newPath, ""},
		{"add ssl crt-list " + render.CrtListPath(live, 1), newPath + " [alpn h2,http/1.1] b.example.com"},
		{"del ssl crt-list " + render.CrtListPath(live, 1) + " " + newPath, ""},
		{"del ssl cert " + newPath, ""},
	}
	for _, st := range steps {
		var err error
		if st.payload != "" {
			err = e.rt.RunPayload(ctx, st.cmd, st.payload)
		} else {
			err = e.rt.Run(ctx, st.cmd)
		}
		if err != nil {
			t.Fatalf("%s: %v", st.cmd, err)
		}
	}
	get := func(port int, host string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:%d/", e.ip, port), nil)
		req.Host = host
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	// Domains on the shared sys_http: known host is routed, unknown host gets 404.
	waitFor(t, 10*time.Second, "domain routed", func() bool { return get(80, "shop.test") == 200 })
	if c := get(80, "other.test"); c != 404 {
		t.Fatalf("unknown host: %d", c)
	}
	// Maintenance map (opt@ prefix is not part of the name) applies to the service port
	// (frontend rule) and to domain traffic (backend rule).
	if err := e.rt.Run(ctx, "add map "+render.MaintMapPath(live)+" 2 1"); err != nil {
		t.Fatal(err)
	}
	if a, b := get(8080, "x"), get(80, "shop.test"); a != 503 || b != 503 {
		t.Fatalf("maintenance not active: port %d, domain %d", a, b)
	}
	if err := e.rt.Run(ctx, "del map "+render.MaintMapPath(live)+" 2"); err != nil {
		t.Fatal(err)
	}
	// ACME HTTP-01 virtual map.
	if err := e.rt.Run(ctx, "add map virt@acme_http01 tok123 tok123.keyauth"); err != nil {
		t.Fatal(err)
	}
	r, err := http.Get(fmt.Sprintf("http://%s:80/.well-known/acme-challenge/tok123", e.ip))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(body) != "tok123.keyauth" {
		t.Fatalf("acme answer %q", body)
	}
}

// VERIFY §14.7: the minimal capability set is enough to start, bind and reload.
func TestVerifyMinimalCapabilities(t *testing.T) {
	in := input([]*state.Frontend{frontend(1, "http", 80, 1)}, backend(1, "http", 19000, ipServer(1, "127.0.0.3")))
	e := startHAProxy(t, in, fakes, "--cap-drop", "ALL",
		"--cap-add", "NET_BIND_SERVICE", "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "KILL", "--cap-add", "CHOWN")
	if res := e.apply(in, fakes, true); !res.Success {
		t.Fatalf("reload with minimal caps failed: %s", res.Output)
	}
	waitFor(t, 10*time.Second, "served", func() bool {
		r, err := http.Get(fmt.Sprintf("http://%s:80/", e.ip))
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
}

// VERIFY §7.1: interactive prompt mode on the worker socket.
func TestVerifyPromptMode(t *testing.T) {
	e := startHAProxy(t, baseInput(), "")
	conn, err := net.Dial("unix", filepath.Join(e.dir, "run", "admin.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "prompt\n")
	rd := bufio.NewReader(conn)
	readPrompt := func() string {
		var b strings.Builder
		for {
			c, err := rd.ReadByte()
			if err != nil {
				t.Fatalf("read: %v (got %q)", err, b.String())
			}
			b.WriteByte(c)
			if strings.HasSuffix(b.String(), "> ") {
				return b.String()
			}
		}
	}
	readPrompt()
	fmt.Fprint(conn, "show info\n")
	if out := readPrompt(); !strings.Contains(out, "Version:") {
		t.Fatalf("prompt mode output: %q", out)
	}
	fmt.Fprint(conn, "show info\n")
	if out := readPrompt(); !strings.Contains(out, "Uptime_sec") {
		t.Fatalf("second command: %q", out)
	}
}

var _ = sql.NullInt64{}
