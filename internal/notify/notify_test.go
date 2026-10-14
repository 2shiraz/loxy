package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/store"
	"github.com/2shiraz/loxy/internal/store/db"
)

func TestMatches(t *testing.T) {
	r := db.AlertRule{EventKinds: `["target.*"]`, MinSeverity: "warning", LbFilter: "env=prod"}
	e := events.Event{Kind: "target.health.changed", Severity: "warning"}
	if !Matches(r, e, map[string]string{"env": "prod"}) {
		t.Fatal("should match")
	}
	if Matches(r, e, map[string]string{"env": "dev"}) || Matches(r, events.Event{Kind: "lb.apply.failed", Severity: "error"}, map[string]string{"env": "prod"}) {
		t.Fatal("should not match")
	}
	if Matches(r, events.Event{Kind: "target.health.changed", Severity: "info"}, map[string]string{"env": "prod"}) {
		t.Fatal("severity")
	}
}

func TestWebhookDeliveryWithin5s(t *testing.T) {
	got := make(chan events.Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var e events.Event
		_ = json.Unmarshal(b, &e)
		if r.Header.Get("X-Loxy-Secret") == "s3" {
			got <- e
		}
	}))
	defer srv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	box, _ := secrets.New(make([]byte, 32))
	cfg, _ := json.Marshal(map[string]string{"url": srv.URL, "secret": "s3"})
	ch, _ := st.Q.CreateChannel(ctx, db.CreateChannelParams{Name: "hook", Kind: "webhook", ConfigEnc: box.Seal(cfg), Enabled: 1, CreatedAt: 1})
	_, _ = st.Q.CreateAlertRule(ctx, db.CreateAlertRuleParams{EventKinds: `["target.health.changed"]`, ChannelID: ch.ID, MinSeverity: "warning", CooldownS: 60})
	hub := events.NewHub(st.Q)
	n := New(st.Q, box, hub, nil)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go n.Run(cctx)
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	hub.Publish(events.Event{Kind: "target.health.changed", Severity: "warning", Message: "t1 down"})
	select {
	case e := <-got:
		if e.Message != "t1 down" || time.Since(start) > 5*time.Second {
			t.Fatalf("%+v after %v", e, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alert not delivered within 5 s")
	}
}
