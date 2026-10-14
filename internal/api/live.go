package api

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/metrics"
)

// LiveInterval is how often the `live.tick` snapshot is pushed on /events.
const LiveInterval = 2 * time.Second

// RunLiveTicker pushes a compact live snapshot of every project (status, health, traffic) and
// the node totals as the ephemeral SSE event `live.tick`, so every page updates in real time
// without polling. `health_sig` changes whenever any target check of the project changes,
// which tells detail pages to refetch targets.
func (s *Server) RunLiveTicker(ctx context.Context) {
	t := time.NewTicker(LiveInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.Hub.Publish(events.Event{Kind: "live.tick", Ephemeral: true, Data: s.liveSnapshot()})
	}
}

func (s *Server) liveSnapshot() map[string]any {
	snap := s.Snap.Get()
	projects := make([]map[string]any, 0, len(snap.Projects))
	for _, p := range snap.Projects {
		sum, rs := s.projectHealth(p)
		h := fnv.New64a()
		for _, r := range rs {
			fmt.Fprintf(h, "%d:%s;", r.SlotID, r.Status)
			for _, c := range r.Checks {
				fmt.Fprintf(h, "%d=%s,", c.ServiceID, c.Status)
			}
		}
		var pt metrics.Point
		if s.Metrics != nil {
			pt = s.Metrics.Latest(metrics.PR, p.ID)
		}
		projects = append(projects, map[string]any{
			"id": p.ID, "status": p.Status, "maintenance": p.Maintenance != 0, "version": p.Version,
			"health":     map[string]any{"status": sum.Status, "healthy": sum.Healthy, "total": sum.Total},
			"health_sig": fmt.Sprintf("%x", h.Sum64()),
			"stats": map[string]any{"open_connections": pt.Open, "requests_per_second": pt.RPS,
				"bytes_in_per_second": pt.In, "bytes_out_per_second": pt.Out, "response_time_ms": pt.RespMS},
		})
	}
	var t metrics.Point
	if s.Metrics != nil {
		t = s.Metrics.Latest(metrics.FE, 0)
	}
	out := map[string]any{"ts": time.Now().UnixMilli(), "projects": projects,
		"traffic": map[string]any{"open_connections": t.Open, "requests_per_second": t.RPS, "bytes_in_per_second": t.In, "bytes_out_per_second": t.Out}}
	if len(snap.Nodes) > 0 {
		n := snap.Nodes[0]
		out["node"] = map[string]any{"status": n.Status, "applied_version": n.AppliedVersion, "desired_version": n.DesiredVersion}
	}
	return out
}

// LiveSnapshotForTest exposes the live.tick payload to tests.
func (s *Server) LiveSnapshotForTest() map[string]any { return s.liveSnapshot() }
