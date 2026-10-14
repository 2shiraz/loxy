package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/config"

	"github.com/2shiraz/loxy/internal/store"
)

// Backup + restore round trip (Phase 2 DoD).
func TestBackupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "loxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO hosts (project_id, name, ipv4) VALUES (1, 'web-1', '10.0.0.1')`); err != nil {
		t.Fatal(err)
	}
	p, err := Backup(st, filepath.Join(dir, "backups"), 14)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	restored, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	srv, err := restored.Q.ListHosts(ctx)
	if err != nil || len(srv) != 1 || srv[0].Name != "web-1" {
		t.Fatalf("restored hosts: %v %+v", err, srv)
	}
}

func TestSeedDemo(t *testing.T) {
	a := newTestApp(t)
	if err := a.SeedDemo(context.Background(), "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	snap, _ := a.Snap.Refresh(context.Background(), a.Store.Q, a.Box)
	if len(snap.Frontends) != 3 || len(snap.Backends) != 3 || len(snap.Backends[0].Slots()) != 3 || len(snap.Frontends[0].Routes) != 1 {
		t.Fatalf("seed: %d frontends, %d backends", len(snap.Frontends), len(snap.Backends))
	}
	out, err := a.Rec.RenderLive(context.Background())
	if err != nil || !strings.Contains(out.Config(), "frontend fe_2") || !strings.Contains(out.Config(), "backend be_3") {
		t.Fatalf("render: %v", err)
	}
}

func newTestApp(t *testing.T) *App {
	dir := t.TempDir()
	cfg := &config.Config{Listen: "127.0.0.1:0", PanelPort: 8443, DataDir: dir, HAProxyConfDir: filepath.Join(dir, "conf"), HAProxyRunDir: dir,
		HAProxyStateDir: dir, HAProxyBin: "true", StatsInterval: time.Second, NoHAProxy: true, SecretKey: make([]byte, 32), ACMEDirectory: "https://127.0.0.1:1/dir"}
	a, err := New(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	return a
}
