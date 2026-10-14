package app

import (
	"context"
	"path/filepath"
	"testing"

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
	if _, err := st.DB.Exec(`INSERT INTO servers (project_id, name, public_ipv4) VALUES (1, 'web-1', '10.0.0.1')`); err != nil {
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
	srv, err := restored.Q.ListServers(ctx)
	if err != nil || len(srv) != 1 || srv[0].Name != "web-1" {
		t.Fatalf("restored servers: %v %+v", err, srv)
	}
}
