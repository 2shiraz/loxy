// Package testutil provides helpers shared by tests.
package testutil

import (
	"os"
	"os/exec"
	"testing"
)

// HAProxyImage is the pinned image used by tests (keep in sync with .env.example).
const HAProxyImage = "haproxy:3.4.6-alpine"

// HAProxyCmd returns a command prefix that runs the pinned haproxy binary with dir
// visible at the same path. It uses a local `haproxy` binary when present, otherwise
// Docker; the test is skipped when neither is available.
func HAProxyCmd(t testing.TB, dir string) []string {
	t.Helper()
	if p := os.Getenv("LOXY_TEST_HAPROXY_BIN"); p != "" {
		return []string{p}
	}
	if p, err := exec.LookPath("haproxy"); err == nil {
		return []string{p}
	}
	if _, err := exec.LookPath("docker"); err == nil {
		if exec.Command("docker", "image", "inspect", HAProxyImage).Run() != nil {
			if out, err := exec.Command("docker", "pull", HAProxyImage).CombinedOutput(); err != nil {
				t.Skipf("cannot pull %s: %v %s", HAProxyImage, err, out)
			}
		}
		return []string{"docker", "run", "--rm", "--network", "none", "-v", dir + ":" + dir + ":ro", HAProxyImage, "haproxy"}
	}
	t.Skip("no haproxy binary and no docker; skipping haproxy -c")
	return nil
}
