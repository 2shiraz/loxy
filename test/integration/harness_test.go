//go:build integration

// Package integration runs loxy components against the pinned HAProxy image (spec §18).
// Run with: LOXY_INTEGRATION=1 go test -tags integration ./test/integration/...
package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/haproxy/revfs"
	"github.com/2shiraz/loxy/internal/haproxy/runtime"
	"github.com/2shiraz/loxy/internal/testutil"
)

const containerConf = "/usr/local/etc/haproxy"

type env struct {
	t        *testing.T
	dir      string
	fs       *revfs.FS
	rt       *runtime.Client
	name     string
	ip       string
	panelPEM string
	rev      int64
}

func dockerOK(t *testing.T) {
	if os.Getenv("LOXY_INTEGRATION") == "" {
		t.Skip("set LOXY_INTEGRATION=1")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	testutil.HAProxyCmd(t, os.TempDir()) // pulls the image
}

// startHAProxy writes bootstrap rev 1 and starts the container in master-worker mode.
// extraDocker are additional `docker run` flags (capabilities etc.).
func startHAProxy(t *testing.T, in render.Input, extra string, extraDocker ...string) *env {
	dockerOK(t)
	dir := t.TempDir()
	for _, d := range []string{"conf", "run", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o777); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(filepath.Join(dir, d), 0o777)
	}
	c, k, _ := certs.SelfSigned("panel", nil, 24*time.Hour)
	e := &env{t: t, dir: dir, fs: revfs.New(filepath.Join(dir, "conf")), panelPEM: c + k,
		rt: runtime.New(filepath.Join(dir, "run", "admin.sock"), filepath.Join(dir, "run", "master.sock"))}
	e.apply(in, extra, false)
	e.name = fmt.Sprintf("loxy-it-%d", time.Now().UnixNano())
	args := []string{"run", "-d", "--name", e.name,
		"-v", filepath.Join(dir, "conf") + ":" + containerConf + ":ro",
		"-v", filepath.Join(dir, "run") + ":/run/haproxy",
		"-v", filepath.Join(dir, "state") + ":/var/lib/haproxy",
		// as in deploy/compose/compose.yml
		"--user", "0:0", "--group-add", "1500"}
	args = append(args, extraDocker...)
	args = append(args, testutil.HAProxyImage, "haproxy", "-W", "-db", "-S", "/run/haproxy/master.sock,mode,660,gid,1500",
		"-f", containerConf+"/live/haproxy.cfg")
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v %s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", e.name).CombinedOutput()
			t.Logf("haproxy logs:\n%s", out)
		}
		_ = exec.Command("docker", "rm", "-f", e.name).Run()
	})
	out, _ := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", e.name).Output()
	e.ip = strings.TrimSpace(string(out))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := e.rt.Exec(context.Background(), "show info"); err == nil {
			if _, err := e.rt.Master(context.Background(), "show proc"); err == nil {
				return e
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", e.name).CombinedOutput()
	t.Fatalf("haproxy did not come up:\n%s", logs)
	return nil
}

// apply renders `in` as the next revision, validates it, swaps live and (optionally) reloads.
func (e *env) apply(in render.Input, extra string, reload bool) *runtime.ReloadResult {
	e.t.Helper()
	e.rev++
	in.Revision = e.rev
	in.PanelPEM = e.panelPEM
	in.StateFile = "/var/lib/haproxy/server-state"
	in.GlobalMaxConn = 4000 // sandbox nofile limits are low
	in.Root = containerConf + "/live"
	out, err := render.Render(in)
	if err != nil {
		e.t.Fatal(err)
	}
	in.Root = e.fs.RevDir(e.rev)
	chk, err := render.Render(in)
	if err != nil {
		e.t.Fatal(err)
	}
	out.Files["haproxy.cfg"] = append(out.Files["haproxy.cfg"], []byte(extra)...)
	chkCfg := append(chk.Files["haproxy.cfg"], []byte(extra)...)
	if _, err := e.fs.Write(e.rev, out.Files, chkCfg); err != nil {
		e.t.Fatal(err)
	}
	_ = os.Chmod(filepath.Join(e.dir, "conf", "revs"), 0o755)
	for _, f := range []string{"", "certs", "maps", "errors", "crt-lists"} {
		_ = os.Chmod(filepath.Join(e.fs.RevDir(e.rev), f), 0o755)
	}
	filepath.Walk(e.fs.RevDir(e.rev), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
	if err := e.fs.Swap(e.rev); err != nil {
		e.t.Fatal(err)
	}
	if !reload {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := e.rt.Reload(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) dial(port int) (net.Conn, error) {
	return net.DialTimeout("tcp", net.JoinHostPort(e.ip, fmt.Sprint(port)), 2*time.Second)
}

func waitFor(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
