// Package store owns the SQLite database: connection, embedded migrations, seed data.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
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
	// Pre-release databases used a load-balancer schema that was rewritten in place (ADR 0005);
	// refuse them clearly instead of failing later on missing tables.
	var legacy int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'load_balancers'`).Scan(&legacy); err != nil {
		return err
	}
	if legacy > 0 {
		return errors.New("this database uses the pre-release load-balancer schema, which this version cannot read (see docs/adr/0005-product-direction.md); back it up and start with an empty data directory")
	}
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

// Seed creates the default project and the local node.
func (s *Store) Seed(ctx context.Context) error {
	if err := s.Q.EnsureProject(ctx, time.Now().UnixMilli()); err != nil {
		return err
	}
	return s.Q.EnsureLocalNode(ctx)
}
