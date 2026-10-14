package store

import (
	"context"
	"database/sql"
	"path/filepath"
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
	plans, _ := s.Q.ListPlans(ctx)
	if len(plans) != 3 || plans[0].Name != "lb11" || plans[0].MaxTargets != 25 {
		t.Fatalf("plans %+v", plans)
	}
	for _, lb := range []int64{1, 2} {
		if _, err := s.Q.CreateEvent(ctx, db.CreateEventParams{Ts: 1, LbID: sql.NullInt64{Int64: lb, Valid: true}, Kind: "k", Severity: "info", Message: "m", Data: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Q.ListEvents(ctx, db.ListEventsParams{MaxRows: 10})
	if err != nil || len(all) != 2 {
		t.Fatalf("all: %v %d", err, len(all))
	}
	one, err := s.Q.ListEvents(ctx, db.ListEventsParams{LbID: int64(2), MaxRows: 10})
	if err != nil || len(one) != 1 || one[0].LbID.Int64 != 2 {
		t.Fatalf("filtered: %v %+v", err, one)
	}
}
