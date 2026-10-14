// Package store owns the SQLite database: connection, embedded migrations, seed data.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/2shiraz/loxy/internal/store/db"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

func Open(file string) (*Store, error) {
	dsn := "file:" + file + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(4)
	if err := d.Ping(); err != nil {
		return nil, err
	}
	s := &Store{DB: d, Q: db.New(d)}
	if err := s.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		var one int
		err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE name = ?`, n).Scan(&one)
		if err == nil {
			continue
		}
		body, err := migrations.ReadFile(path.Join("migrations", n))
		if err != nil {
			return err
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", n, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, n, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Tx runs fn in one transaction.
func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Seed creates the default project, local node and default plans (§21 Q4 defaults).
func (s *Store) Seed(ctx context.Context) error {
	now := time.Now().UnixMilli()
	if err := s.Q.EnsureProject(ctx, now); err != nil {
		return err
	}
	if err := s.Q.EnsureLocalNode(ctx); err != nil {
		return err
	}
	n, err := s.Q.CountPlans(ctx)
	if err != nil || n > 0 {
		return err
	}
	const tb = int64(1_000_000_000_000)
	plans := []db.CreatePlanParams{
		{Name: "lb11", MaxConnections: 10000, MaxServices: 5, MaxTargets: 25, MaxCertificates: 10, TrafficQuotaBytes: 20 * tb, Sort: 1},
		{Name: "lb21", MaxConnections: 20000, MaxServices: 15, MaxTargets: 75, MaxCertificates: 25, TrafficQuotaBytes: 20 * tb, Sort: 2},
		{Name: "lb31", MaxConnections: 40000, MaxServices: 30, MaxTargets: 150, MaxCertificates: 50, TrafficQuotaBytes: 20 * tb, Sort: 3},
	}
	for _, p := range plans {
		if err := s.Q.CreatePlan(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
