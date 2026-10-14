package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2shiraz/loxy/internal/store/db"
)

func TestMigrateSeedAndQueries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "loxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	for _, fe := range []int64{1, 2} {
		if _, err := s.Q.CreateEvent(ctx, db.CreateEventParams{Ts: 1, BalancerID: sql.NullInt64{Int64: fe, Valid: true}, Kind: "k", Severity: "info", Message: "m", Data: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Q.ListEvents(ctx, db.ListEventsParams{MaxRows: 10})
	if err != nil || len(all) != 2 {
		t.Fatalf("all: %v %d", err, len(all))
	}
	one, err := s.Q.ListEvents(ctx, db.ListEventsParams{BalancerID: int64(2), MaxRows: 10})
	if err != nil || len(one) != 1 || one[0].BalancerID.Int64 != 2 {
		t.Fatalf("filtered: %v %+v", err, one)
	}
}

func TestRefusesLegacySchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "old.db")
	d, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE load_balancers (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := Open(file); err == nil || !strings.Contains(err.Error(), "pre-release") {
		t.Fatalf("expected legacy-schema error, got %v", err)
	}
}

// Upgrading a database from the frontend/backend schema (0001 only) keeps domains, hosts and
// certificates and creates the project tables (migration 0002).
func TestMigrateFromFrontendBackendSchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "v1.db")
	d, err := sql.Open("sqlite", "file:"+file+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	v1, err := migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{string(v1),
		`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`,
		`INSERT INTO schema_migrations VALUES ('0001_init.sql', 1)`,
		`INSERT INTO projects (id, name, created_at) VALUES (1, 'default', 1)`,
		`INSERT INTO nodes (id, name) VALUES (1, 'local')`,
		`INSERT INTO hosts (project_id, name, ipv4) VALUES (1, 'web-1', '10.0.0.1')`,
		`INSERT INTO backends (project_id, name, mode, port, created_at) VALUES (1, 'b', 'http', 80, 1)`,
		`INSERT INTO frontends (project_id, name, mode, binds, default_backend_id, created_at, updated_at) VALUES (1, 'f', 'http', '[{"address":"*","port":80}]', 1, 1, 1)`,
		`INSERT INTO domains (project_id, fqdn, frontend_id, created_at, updated_at) VALUES (1, 'app.example.com', 1, 1, 1)`,
		`INSERT INTO events (ts, frontend_id, kind, severity, message) VALUES (1, 1, 'k', 'info', 'm')`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			t.Fatalf("%v: %.60s", err, stmt)
		}
	}
	d.Close()
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	doms, err := s.Q.ListDomains(ctx)
	if err != nil || len(doms) != 1 || doms[0].Fqdn != "app.example.com" || doms[0].BalancerID.Valid || doms[0].Https != 1 {
		t.Fatalf("domains after migration: %v %+v", err, doms)
	}
	if hosts, err := s.Q.ListHosts(ctx); err != nil || len(hosts) != 1 {
		t.Fatalf("hosts: %v %d", err, len(hosts))
	}
	if evs, err := s.Q.ListEvents(ctx, db.ListEventsParams{MaxRows: 10}); err != nil || len(evs) != 1 {
		t.Fatalf("events: %v %d", err, len(evs))
	}
	if _, err := s.Q.ListBalancers(ctx); err != nil {
		t.Fatal(err)
	}
}
