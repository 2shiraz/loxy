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
		if _, err := s.Q.CreateEvent(ctx, db.CreateEventParams{Ts: 1, FrontendID: sql.NullInt64{Int64: fe, Valid: true}, Kind: "k", Severity: "info", Message: "m", Data: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Q.ListEvents(ctx, db.ListEventsParams{MaxRows: 10})
	if err != nil || len(all) != 2 {
		t.Fatalf("all: %v %d", err, len(all))
	}
	one, err := s.Q.ListEvents(ctx, db.ListEventsParams{FrontendID: int64(2), MaxRows: 10})
	if err != nil || len(one) != 1 || one[0].FrontendID.Int64 != 2 {
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
