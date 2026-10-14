//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/haproxy/runtime"
)

// Exercise default weights on reload and dynamic add, then lower/restore a
// weight through the API. Assert actual traffic, not only emitted commands.
func TestServerWeightDistribution(t *testing.T) {
	s := startStack(t, nil)
	ctx := context.Background()
	one, two := echoServer(t, s.dir, "one"), echoServer(t, s.dir, "two")
	spec := map[string]any{"protocol": "http", "listen_port": 8080, "destination_port": 9001,
		"health_check": map[string]any{"protocol": "http", "interval_s": 3, "timeout_s": 1, "retries": 1, "http": map[string]any{"path": "/"}}}
	status, body := s.c.do("POST", "/projects", map[string]any{"name": "weights", "algorithm": "roundrobin",
		"service": spec, "targets": []any{map[string]any{"type": "ip", "ip": one}}})
	if status != 201 {
		t.Fatalf("create project: %d %v", status, body)
	}
	pid := int64(body["project"].(map[string]any)["id"].(float64))
	project := s.a.Snap.Get().Project(pid)
	beID := project.Services[0].ID
	sid := project.Targets[0].ID
	add := func(ip string) int64 {
		t.Helper()
		status, body := s.c.do("POST", fmt.Sprintf("/projects/%d/targets", pid), map[string]any{"targets": []any{map[string]any{"type": "ip", "ip": ip}}})
		if status != 201 {
			t.Fatalf("add server: %d %v", status, body)
		}
		return int64(body["targets"].([]any)[0].(map[string]any)["id"].(float64))
	}
	apply := func() {
		t.Helper()
		if err := s.a.Rec.Apply(ctx, "weight-test"); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	secondID := add(two)
	apply() // exercises dynamically added default-weight servers too
	rt := runtime.New(filepath.Join(s.dir, "run", "admin.sock"), filepath.Join(s.dir, "run", "master.sock"))
	checkWeight := func(id int64, want int) {
		t.Helper()
		out, err := rt.Exec(ctx, fmt.Sprintf("get weight be_%d/s%d", beID, id))
		var got int
		_, scanErr := fmt.Sscan(out, &got)
		if err != nil || scanErr != nil || got != want {
			t.Fatalf("live weight %d: %q %v", want, out, err)
		}
	}
	checkWeight(sid, 100)
	checkWeight(secondID, 100)
	url := fmt.Sprintf("http://%s:8080/", s.haIP)
	hc := &http.Client{Timeout: 5 * time.Second}
	counts := func(n int) map[string]int {
		t.Helper()
		out := map[string]int{}
		for i := 0; i < n; i++ {
			r, err := hc.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil || r.StatusCode != 200 {
				t.Fatalf("request: status=%d err=%v", r.StatusCode, err)
			}
			out[strings.TrimSpace(string(b))]++
		}
		return out
	}
	waitFor(t, 15*time.Second, "both servers healthy", func() bool {
		rows, _, err := s.a.Rec.Node.Stat(ctx, true)
		if err != nil {
			return false
		}
		up := 0
		for _, row := range rows {
			if row.PxName == fmt.Sprintf("be_%d", beID) && row.Type == 2 && row.Str("status") == "UP" {
				up++
			}
		}
		return up == 2
	})
	for _, weight := range []int{100, 10, 100, 150} {
		status, body := s.c.do("PATCH", fmt.Sprintf("/projects/%d/targets/%d", pid, sid), map[string]any{"weight": weight})
		if status != 200 {
			t.Fatalf("update weight: %d %v", status, body)
		}
		apply()
		checkWeight(sid, weight)
		checkWeight(secondID, 100)
		c := counts(220)
		want := 220 * weight / (weight + 100)
		if c["one"] < want-20 || c["one"] > want+20 || c["one"]+c["two"] != 220 {
			t.Fatalf("weight %d vs 100: counts=%v, want one near %d", weight, c, want)
		}
	}
	// Changing a backend timeout forces a reload with the saved server-state file.
	spec["timeout_s"] = 61
	status, body = s.c.do("PUT", fmt.Sprintf("/projects/%d/services/%d", pid, beID), spec)
	if status != 200 {
		t.Fatalf("force reload: %d %v", status, body)
	}
	apply()
	c := counts(220)
	if c["one"] < 112 || c["one"] > 152 || c["one"]+c["two"] != 220 {
		t.Fatalf("weight 150 vs 100 after reload: %v", c)
	}
}
