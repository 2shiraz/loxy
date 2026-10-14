// Package metrics polls HAProxy every second, derives per-LB/per-service series with reset-safe
// deltas, keeps 1 h of 1 s points in memory, rolls up to SQLite per minute and tracks health
// (spec §9, §10).
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2shiraz/loxy/internal/agent"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/haproxy/stats"
	"github.com/2shiraz/loxy/internal/store/db"
)

// Point is one 1 s sample.
type Point struct {
	TS                     int64 // unix seconds
	Open                   float64
	CPS, RPS, In, Out      float64
	H2, H3, H4, H5, Errors float64
	RespMS                 float64
	Queue                  float64
}

const ringSize = 3600

type ring struct {
	pts  [ringSize]Point
	n    int
	head int
}

func (r *ring) add(p Point) {
	r.pts[r.head] = p
	r.head = (r.head + 1) % ringSize
	if r.n < ringSize {
		r.n++
	}
}

func (r *ring) since(ts int64) []Point {
	out := make([]Point, 0, 64)
	for i := 0; i < r.n; i++ {
		p := r.pts[(r.head-r.n+i+ringSize)%ringSize]
		if p.TS >= ts {
			out = append(out, p)
		}
	}
	return out
}

type key struct{ lb, svc int64 }

type counters struct {
	connTot, reqTot, bin, bout, h2, h3, h4, h5, errs float64
}

// Check is the observed health of one server in one backend.
type Check struct {
	LBID, ServiceID, SlotID int64
	Status                  string // healthy|unhealthy|maintenance|draining|unknown
	Raw                     string
	Detail                  string // e.g. L7OK/200 in 3ms
	Scur                    int64
}

type Collector struct {
	Node     agent.NodeTransport
	Q        *db.Queries
	Hub      *events.Hub
	Interval time.Duration

	mu      sync.RWMutex
	rings   map[key]*ring
	prev    map[key]counters
	pid     string
	checks  map[string]*Check // "lb/svc/slot"
	minute  map[key][]Point
	subs    map[int64]map[chan Point]struct{}
	info    map[string]string
	lastOK  time.Time
	reloads int
}

func New(node agent.NodeTransport, q *db.Queries, hub *events.Hub, interval time.Duration) *Collector {
	return &Collector{Node: node, Q: q, Hub: hub, Interval: interval, rings: map[key]*ring{}, prev: map[key]counters{},
		checks: map[string]*Check{}, minute: map[key][]Point{}, subs: map[int64]map[chan Point]struct{}{}}
}

func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := c.poll(ctx, now); err != nil {
				slog.Debug("stats poll", "err", err)
			}
		}
	}
}

// parseName extracts lb and service ids from lb<L>_s<S>_fe|be.
func parseName(px, suffix string) (int64, int64, bool) {
	if !strings.HasPrefix(px, "lb") || !strings.HasSuffix(px, suffix) {
		return 0, 0, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(px, "lb"), suffix)
	l, s, ok := strings.Cut(mid, "_s")
	if !ok {
		return 0, 0, false
	}
	lb, e1 := strconv.ParseInt(l, 10, 64)
	sv, e2 := strconv.ParseInt(s, 10, 64)
	return lb, sv, e1 == nil && e2 == nil
}

func healthOf(status string) string {
	s := strings.ToUpper(status)
	switch {
	case strings.HasPrefix(s, "UP"), s == "NOLB":
		return "healthy"
	case strings.HasPrefix(s, "DOWN"):
		return "unhealthy"
	case strings.HasPrefix(s, "MAINT"):
		return "maintenance"
	case s == "DRAIN":
		return "draining"
	}
	return "unknown"
}

func (c *Collector) poll(ctx context.Context, now time.Time) error {
	rows, info, err := c.Node.Stat(ctx)
	if err != nil {
		return err
	}
	ts := now.Unix()
	cur := map[key]counters{}
	pts := map[key]*Point{}
	get := func(k key) *Point {
		if p := pts[k]; p != nil {
			return p
		}
		p := &Point{TS: ts}
		pts[k] = p
		return p
	}
	newChecks := map[string]*Check{}
	for _, r := range rows {
		switch r.Type {
		case 0:
			lb, sv, ok := parseName(r.PxName, "_fe")
			if !ok {
				continue
			}
			k := key{lb, sv}
			cur[k] = counters{connTot: float64(r.Int("conn_tot")), reqTot: float64(r.Int("req_tot")), bin: float64(r.Int("bin")), bout: float64(r.Int("bout")),
				h2: float64(r.Int("hrsp_2xx")), h3: float64(r.Int("hrsp_3xx")), h4: float64(r.Int("hrsp_4xx")), h5: float64(r.Int("hrsp_5xx")),
				errs: float64(r.Int("ereq") + r.Int("dreq"))}
			get(k).Open = float64(r.Int("scur"))
		case 1:
			lb, sv, ok := parseName(r.PxName, "_be")
			if !ok {
				continue
			}
			p := get(key{lb, sv})
			p.RespMS = float64(r.Int("rtime"))
			p.Queue = float64(r.Int("qcur"))
		case 2:
			lb, sv, ok := parseName(r.PxName, "_be")
			if !ok {
				continue
			}
			sid := r.Int("sid")
			ch := &Check{LBID: lb, ServiceID: sv, SlotID: sid, Raw: r.Str("status"), Status: healthOf(r.Str("status")), Scur: r.Int("scur")}
			if cs := r.Str("check_status"); cs != "" {
				ch.Detail = strings.TrimPrefix(cs, "* ")
				if code := r.Str("check_code"); code != "" {
					ch.Detail += "/" + code
				}
				if d := r.Str("check_duration"); d != "" {
					ch.Detail += " in " + d + "ms"
				}
				if desc := r.Str("check_desc"); desc != "" && ch.Status == "unhealthy" {
					ch.Detail += " · " + desc
				}
			}
			newChecks[fmt.Sprintf("%d/%d/%d", lb, sv, sid)] = ch
		}
	}
	c.mu.Lock()
	reset := c.pid != "" && info["Pid"] != c.pid
	if reset {
		c.reloads++
	}
	c.pid = info["Pid"]
	c.info = info
	c.lastOK = now
	dt := c.Interval.Seconds()
	d := func(cur, prev float64) float64 {
		if reset || cur < prev {
			return cur / dt // counters restarted with the new worker (§10.3)
		}
		return (cur - prev) / dt
	}
	lbSum := map[int64]*Point{}
	month := now.UTC().Format("2006-01")
	traffic := map[int64][2]float64{}
	for k, p := range pts {
		if cc, ok := cur[k]; ok {
			if pv, ok := c.prev[k]; ok {
				p.CPS, p.RPS = d(cc.connTot, pv.connTot), d(cc.reqTot, pv.reqTot)
				p.In, p.Out = d(cc.bin, pv.bin), d(cc.bout, pv.bout)
				p.H2, p.H3, p.H4, p.H5 = d(cc.h2, pv.h2), d(cc.h3, pv.h3), d(cc.h4, pv.h4), d(cc.h5, pv.h5)
				p.Errors = d(cc.errs, pv.errs)
				tr := traffic[k.lb]
				traffic[k.lb] = [2]float64{tr[0] + p.In*dt, tr[1] + p.Out*dt}
			}
			c.prev[k] = cc
		}
		s := lbSum[k.lb]
		if s == nil {
			s = &Point{TS: ts}
			lbSum[k.lb] = s
		}
		s.Open += p.Open
		s.CPS += p.CPS
		s.RPS += p.RPS
		s.In += p.In
		s.Out += p.Out
		s.H2 += p.H2
		s.H3 += p.H3
		s.H4 += p.H4
		s.H5 += p.H5
		s.Errors += p.Errors
		s.Queue += p.Queue
		s.RespMS = math.Max(s.RespMS, p.RespMS)
		c.addPoint(k, *p)
	}
	for lb, p := range lbSum {
		c.addPoint(key{lb, 0}, *p)
		for ch := range c.subs[lb] {
			select {
			case ch <- *p:
			default:
			}
		}
	}
	// health transitions
	var transitions []*Check
	for id, ch := range newChecks {
		if old, ok := c.checks[id]; ok && old.Status != ch.Status && (ch.Status == "healthy" || ch.Status == "unhealthy") {
			transitions = append(transitions, ch)
		}
	}
	c.checks = newChecks
	// minute rollup
	var flush map[key][]Point
	if now.Second() == 0 || (len(c.minute) > 0 && anyOld(c.minute, ts-60)) {
		flush = c.minute
		c.minute = map[key][]Point{}
	}
	c.mu.Unlock()

	for _, ch := range transitions {
		sev, msg := "info", "is healthy again"
		if ch.Status == "unhealthy" {
			sev, msg = "warning", "is unhealthy: "+ch.Detail
		}
		c.Hub.Publish(events.Event{Kind: "target.health.changed", LBID: events.I64(ch.LBID), Severity: sev,
			Message: fmt.Sprintf("Target slot %d on service %d %s", ch.SlotID, ch.ServiceID, msg),
			Data:    map[string]any{"service_id": ch.ServiceID, "slot_id": ch.SlotID, "status": ch.Status}})
	}
	for lb, tr := range traffic {
		if c.Q != nil && (tr[0] > 0 || tr[1] > 0) {
			_ = c.Q.AddTraffic(ctx, db.AddTrafficParams{LbID: lb, Month: month, BytesIn: int64(tr[0]), BytesOut: int64(tr[1])})
		}
	}
	if flush != nil && c.Q != nil {
		c.rollup(ctx, flush)
	}
	return nil
}

func anyOld(m map[key][]Point, before int64) bool {
	for _, ps := range m {
		if len(ps) > 0 && ps[0].TS < before {
			return true
		}
	}
	return false
}

func (c *Collector) addPoint(k key, p Point) {
	r := c.rings[k]
	if r == nil {
		r = &ring{}
		c.rings[k] = r
	}
	r.add(p)
	c.minute[k] = append(c.minute[k], p)
}

func (c *Collector) rollup(ctx context.Context, m map[key][]Point) {
	for k, ps := range m {
		if len(ps) == 0 {
			continue
		}
		var a Point
		mx := 0.0
		for _, p := range ps {
			a.Open += p.Open
			mx = math.Max(mx, p.Open)
			a.CPS += p.CPS
			a.RPS += p.RPS
			a.In += p.In
			a.Out += p.Out
			a.RespMS += p.RespMS
			a.H2 += p.H2
			a.H3 += p.H3
			a.H4 += p.H4
			a.H5 += p.H5
			a.Errors += p.Errors
		}
		n := float64(len(ps))
		_ = c.Q.InsertMetric1m(ctx, db.InsertMetric1mParams{LbID: k.lb, ServiceID: k.svc, Ts: (ps[0].TS / 60) * 60,
			OpenConnAvg: a.Open / n, OpenConnMax: mx, Cps: a.CPS / n, Rps: a.RPS / n, BytesIn: a.In / n, BytesOut: a.Out / n,
			RespTimeMsAvg: a.RespMS / n, Http2xx: a.H2 / n, Http3xx: a.H3 / n, Http4xx: a.H4 / n, Http5xx: a.H5 / n, Errors: a.Errors / n})
	}
	_ = c.Q.PurgeMetrics1m(ctx, time.Now().Add(-35*24*time.Hour).Unix())
}

// Subscribe streams LB-level points (1 s) for SSE.
func (c *Collector) Subscribe(lb int64) (<-chan Point, func()) {
	ch := make(chan Point, 16)
	c.mu.Lock()
	if c.subs[lb] == nil {
		c.subs[lb] = map[chan Point]struct{}{}
	}
	c.subs[lb][ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs[lb], ch)
		c.mu.Unlock()
	}
}

// Checks returns a copy of the current checks of one LB.
func (c *Collector) Checks(lb int64) map[string]Check {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string]Check{}
	prefix := fmt.Sprintf("%d/", lb)
	for k, v := range c.checks {
		if strings.HasPrefix(k, prefix) {
			out[k] = *v
		}
	}
	return out
}

// Check returns the observed check for (lb, service, slot).
func (c *Collector) Check(lb, svc, slot int64) (Check, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ch, ok := c.checks[fmt.Sprintf("%d/%d/%d", lb, svc, slot)]
	if !ok {
		return Check{}, false
	}
	return *ch, true
}

// OpenConnections returns the latest LB-level open connections.
func (c *Collector) OpenConnections(lb int64) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r := c.rings[key{lb, 0}]
	if r == nil || r.n == 0 {
		return 0
	}
	return int64(r.pts[(r.head-1+ringSize)%ringSize].Open)
}

func (c *Collector) Info() (map[string]string, int, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info, c.reloads, c.lastOK
}

// Series is the columnar API format (§10.5).
type Series struct {
	Start, End int64
	Step       int64
	Timestamps []int64
	Values     map[string][]*float64
}

var allTypes = []string{"open_connections", "connections_per_second", "requests_per_second", "bandwidth.in", "bandwidth.out",
	"response_time_ms", "http_2xx", "http_3xx", "http_4xx", "http_5xx", "errors_per_second"}

func pick(p Point, t string) float64 {
	switch t {
	case "open_connections":
		return p.Open
	case "connections_per_second":
		return p.CPS
	case "requests_per_second":
		return p.RPS
	case "bandwidth.in":
		return p.In
	case "bandwidth.out":
		return p.Out
	case "response_time_ms":
		return p.RespMS
	case "http_2xx":
		return p.H2
	case "http_3xx":
		return p.H3
	case "http_4xx":
		return p.H4
	case "http_5xx":
		return p.H5
	case "errors_per_second":
		return p.Errors
	}
	return 0
}

// Query returns a range with resolution chosen per §10.4 (memory for ≤1 h, else 1 m rollups
// aggregated to 5 m / 1 h on read).
func (c *Collector) Query(ctx context.Context, lb, svc, start, end, step int64, types []string) (*Series, error) {
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 || start >= end {
		start = end - 300
	}
	span := end - start
	if len(types) == 0 {
		types = allTypes
	} else {
		var exp []string
		for _, t := range types {
			if t == "bandwidth" {
				exp = append(exp, "bandwidth.in", "bandwidth.out")
			} else {
				exp = append(exp, t)
			}
		}
		types = exp
	}
	if step <= 0 {
		switch {
		case span <= 1800:
			step = 1
		case span <= 3600:
			step = 10
		case span <= 86400:
			step = 60
		case span <= 7*86400:
			step = 300
		default:
			step = 3600
		}
	}
	var pts []Point
	if time.Now().Unix()-start <= 3600 && step < 60 {
		c.mu.RLock()
		if r := c.rings[key{lb, svc}]; r != nil {
			pts = r.since(start)
		}
		c.mu.RUnlock()
	} else {
		rows, err := c.Q.QueryMetrics1m(ctx, db.QueryMetrics1mParams{LbID: lb, ServiceID: svc, Ts: start, Ts_2: end})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			pts = append(pts, Point{TS: r.Ts, Open: r.OpenConnAvg, CPS: r.Cps, RPS: r.Rps, In: r.BytesIn, Out: r.BytesOut, RespMS: r.RespTimeMsAvg,
				H2: r.Http2xx, H3: r.Http3xx, H4: r.Http4xx, H5: r.Http5xx, Errors: r.Errors})
		}
		if step < 60 {
			step = 60
		}
	}
	// bucket by step (average), fill gaps with null
	s := &Series{Start: start - start%step, End: end, Step: step, Values: map[string][]*float64{}}
	type acc struct {
		sum map[string]float64
		n   int
	}
	buckets := map[int64]*acc{}
	for _, p := range pts {
		if p.TS < start || p.TS > end {
			continue
		}
		b := p.TS - p.TS%step
		a := buckets[b]
		if a == nil {
			a = &acc{sum: map[string]float64{}}
			buckets[b] = a
		}
		for _, t := range types {
			a.sum[t] += pick(p, t)
		}
		a.n++
	}
	for t := s.Start; t <= end; t += step {
		s.Timestamps = append(s.Timestamps, t)
		a := buckets[t]
		for _, ty := range types {
			if a == nil {
				s.Values[ty] = append(s.Values[ty], nil)
				continue
			}
			v := math.Round(a.sum[ty]/float64(a.n)*100) / 100
			s.Values[ty] = append(s.Values[ty], &v)
		}
	}
	return s, nil
}

var _ = stats.Row{}
