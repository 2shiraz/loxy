// Package metrics polls HAProxy every second, derives per-frontend and per-backend series with
// reset-safe deltas, keeps recent 1 s points in memory, rolls up to SQLite per minute and tracks
// server health (spec §9, §10).
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
	TS int64 // unix seconds
	// float32 halves ring memory (§1.4 budget); precision is ample for rates.
	Open                   float32
	CPS, RPS, In, Out      float32
	H2, H3, H4, H5, Errors float32
	RespMS                 float32
	Queue                  float32
}

// Frontend series and the node total keep 60 min of 1 s points, backend series 15 min (§10.4).
const (
	feRing = 3600
	beRing = 900
)

type ring struct {
	pts  []Point
	n    int
	head int
}

func newRing(size int) *ring { return &ring{pts: make([]Point, size)} }

func (r *ring) add(p Point) {
	size := len(r.pts)
	r.pts[r.head] = p
	r.head = (r.head + 1) % size
	if r.n < size {
		r.n++
	}
}

func (r *ring) last() Point { return r.pts[(r.head-1+len(r.pts))%len(r.pts)] }

func (r *ring) since(ts int64) []Point {
	size := len(r.pts)
	out := make([]Point, 0, 64)
	for i := 0; i < r.n; i++ {
		p := r.pts[(r.head-r.n+i+size)%size]
		if p.TS >= ts {
			out = append(out, p)
		}
	}
	return out
}

// Scope of a series: frontend ("fe") or backend ("be"); id 0 with scope "fe" = node total.
type Scope string

const (
	FE Scope = "fe"
	BE Scope = "be"
	// PR is a project: the sum of its services' backends (covers port and domain traffic).
	PR Scope = "pr"
)

type key struct {
	scope Scope
	id    int64
}

type counters struct {
	connTot, reqTot, bin, bout, h2, h3, h4, h5, errs float64
}

// Check is the observed health of one server in one backend.
type Check struct {
	BackendID, SlotID int64
	Status            string // healthy|unhealthy|maintenance|draining|unknown
	Raw               string
	Detail            string // e.g. L7OK/200 in 3ms
	Scur              int64
}

type checkKey struct{ be, slot int64 }

type Collector struct {
	Node     agent.NodeTransport
	Q        *db.Queries
	Hub      *events.Hub
	Interval time.Duration
	// Owner maps a service id (backend be_<id>) to its project id and name for events.
	Owner func(serviceID int64) (int64, string)

	mu      sync.RWMutex
	rings   map[key]*ring
	prev    map[key]counters
	pid     string
	checks  map[checkKey]*Check
	minute  map[key][]Point
	subs    map[int64]map[chan Point]struct{}
	info    map[string]string
	lastOK  time.Time
	tick    int
	reloads int
}

func New(node agent.NodeTransport, q *db.Queries, hub *events.Hub, interval time.Duration) *Collector {
	return &Collector{Node: node, Q: q, Hub: hub, Interval: interval, rings: map[key]*ring{}, prev: map[key]counters{},
		checks: map[checkKey]*Check{}, minute: map[key][]Point{}, subs: map[int64]map[chan Point]struct{}{}}
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

// parseName extracts the id from fe_<id> / be_<id> (redirect "fe_<id>_rd" counts toward its frontend).
func parseName(px, prefix string) (int64, bool) {
	if !strings.HasPrefix(px, prefix) {
		return 0, false
	}
	rest := strings.TrimSuffix(px[len(prefix):], "_rd")
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil
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
	// Frontend/backend counters every tick (graphs); server rows (health) every 2nd tick:
	// they are ~90% of the CSV with 1 000 servers (§1.4 CPU budget).
	c.tick++
	withServers := c.tick%2 == 1
	rows, info, err := c.Node.Stat(ctx, withServers)
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
	newChecks := make(map[checkKey]*Check, len(c.checks))
	for _, r := range rows {
		switch r.Type {
		case 0:
			id, ok := parseName(r.PxName, "fe_")
			if !ok {
				// Shared domain frontends count towards the node total only.
				switch r.PxName {
				case "sys_http":
					id = -80
				case "sys_https":
					id = -443
				default:
					continue
				}
			}
			k := key{FE, id}
			cc := counters{connTot: float64(r.Int("conn_tot")), reqTot: float64(r.Int("req_tot")), bin: float64(r.Int("bin")), bout: float64(r.Int("bout")),
				h2: float64(r.Int("hrsp_2xx")), h3: float64(r.Int("hrsp_3xx")), h4: float64(r.Int("hrsp_4xx")), h5: float64(r.Int("hrsp_5xx")),
				errs: float64(r.Int("ereq") + r.Int("dreq"))}
			// fe_<id> and its redirect fe_<id>_rd add up into one series
			if prev, ok := cur[k]; ok {
				cc = counters{connTot: prev.connTot + cc.connTot, reqTot: prev.reqTot + cc.reqTot, bin: prev.bin + cc.bin, bout: prev.bout + cc.bout,
					h2: prev.h2 + cc.h2, h3: prev.h3 + cc.h3, h4: prev.h4 + cc.h4, h5: prev.h5 + cc.h5, errs: prev.errs + cc.errs}
			}
			cur[k] = cc
			get(k).Open += float32(r.Int("scur"))
		case 1:
			id, ok := parseName(r.PxName, "be_")
			if !ok {
				continue
			}
			k := key{BE, id}
			req := r.Int("req_tot") // http backends; tcp backends count sessions
			if req == 0 {
				req = r.Int("stot")
			}
			cur[k] = counters{connTot: float64(r.Int("stot")), reqTot: float64(req), bin: float64(r.Int("bin")), bout: float64(r.Int("bout")),
				h2: float64(r.Int("hrsp_2xx")), h3: float64(r.Int("hrsp_3xx")), h4: float64(r.Int("hrsp_4xx")), h5: float64(r.Int("hrsp_5xx")),
				errs: float64(r.Int("econ") + r.Int("eresp"))}
			p := get(k)
			p.Open = float32(r.Int("scur"))
			p.RespMS = float32(r.Int("rtime"))
			p.Queue = float32(r.Int("qcur"))
		case 2:
			id, ok := parseName(r.PxName, "be_")
			if !ok {
				continue
			}
			sid := r.Int("sid")
			ch := &Check{BackendID: id, SlotID: sid, Raw: r.Str("status"), Status: healthOf(r.Str("status")), Scur: r.Int("scur")}
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
			newChecks[checkKey{id, sid}] = ch
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
	d := func(cur, prev float64) float32 {
		if reset || cur < prev {
			return float32(cur / dt) // counters restarted with the new worker (§10.3)
		}
		return float32((cur - prev) / dt)
	}
	total := &Point{TS: ts}
	for k, p := range pts {
		if cc, ok := cur[k]; ok {
			if pv, ok := c.prev[k]; ok {
				p.CPS, p.RPS = d(cc.connTot, pv.connTot), d(cc.reqTot, pv.reqTot)
				p.In, p.Out = d(cc.bin, pv.bin), d(cc.bout, pv.bout)
				p.H2, p.H3, p.H4, p.H5 = d(cc.h2, pv.h2), d(cc.h3, pv.h3), d(cc.h4, pv.h4), d(cc.h5, pv.h5)
				p.Errors = d(cc.errs, pv.errs)
			}
			c.prev[k] = cc
		}
		if k.scope == FE {
			total.Open += p.Open
			total.CPS += p.CPS
			total.RPS += p.RPS
			total.In += p.In
			total.Out += p.Out
			total.H2 += p.H2
			total.H3 += p.H3
			total.H4 += p.H4
			total.H5 += p.H5
			total.Errors += p.Errors
		}
		c.addPoint(k, *p)
	}
	if c.Owner != nil {
		agg := map[int64]*Point{}
		for k, p := range pts {
			if k.scope != BE {
				continue
			}
			pid, _ := c.Owner(k.id)
			if pid == 0 {
				continue
			}
			a := agg[pid]
			if a == nil {
				a = &Point{TS: ts}
				agg[pid] = a
			}
			a.Open += p.Open
			a.CPS += p.CPS
			a.RPS += p.RPS
			a.In += p.In
			a.Out += p.Out
			a.H2 += p.H2
			a.H3 += p.H3
			a.H4 += p.H4
			a.H5 += p.H5
			a.Errors += p.Errors
			a.Queue += p.Queue
			a.RespMS = max(a.RespMS, p.RespMS)
		}
		for pid, a := range agg {
			c.addPoint(key{PR, pid}, *a)
			for ch := range c.subs[pid] {
				select {
				case ch <- *a:
				default:
				}
			}
		}
	}
	c.addPoint(key{FE, 0}, *total)
	var transitions []*Check
	if withServers {
		for id, ch := range newChecks {
			if old, ok := c.checks[id]; ok && old.Status != ch.Status && (ch.Status == "healthy" || ch.Status == "unhealthy") {
				transitions = append(transitions, ch)
			}
		}
		c.checks = newChecks
	}
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
		var project *int64
		name := fmt.Sprintf("service %d", ch.BackendID)
		if c.Owner != nil {
			if id, pname := c.Owner(ch.BackendID); id != 0 {
				project, name = events.I64(id), pname
			}
		}
		c.Hub.Publish(events.Event{Kind: "target.health.changed", ProjectID: project, Severity: sev,
			Message: fmt.Sprintf("Target #%d of %s %s", ch.SlotID, name, msg),
			Data:    map[string]any{"service_id": ch.BackendID, "slot_id": ch.SlotID, "status": ch.Status}})
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
		size := feRing
		if k.scope == BE {
			size = beRing
		}
		r = newRing(size)
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
		var mx float32
		for _, p := range ps {
			a.Open += p.Open
			mx = max(mx, p.Open)
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
			a.Queue += p.Queue
		}
		n := float32(len(ps))
		f := func(v float32) float64 { return float64(v / n) }
		_ = c.Q.InsertMetric1m(ctx, db.InsertMetric1mParams{Scope: string(k.scope), ScopeID: k.id, Ts: (ps[0].TS / 60) * 60,
			OpenConnAvg: f(a.Open), OpenConnMax: float64(mx), Cps: f(a.CPS), Rps: f(a.RPS), BytesIn: f(a.In), BytesOut: f(a.Out),
			RespTimeMsAvg: f(a.RespMS), Http2xx: f(a.H2), Http3xx: f(a.H3), Http4xx: f(a.H4), Http5xx: f(a.H5), Errors: f(a.Errors), Queue: f(a.Queue)})
	}
	_ = c.Q.PurgeMetrics1m(ctx, time.Now().Add(-35*24*time.Hour).Unix())
}

// Subscribe streams project-level points (1 s) for SSE.
func (c *Collector) Subscribe(project int64) (<-chan Point, func()) {
	ch := make(chan Point, 16)
	c.mu.Lock()
	if c.subs[project] == nil {
		c.subs[project] = map[chan Point]struct{}{}
	}
	c.subs[project][ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs[project], ch)
		c.mu.Unlock()
	}
}

// Check returns the observed check of a server slot in a backend.
func (c *Collector) Check(be, slot int64) (Check, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ch, ok := c.checks[checkKey{be, slot}]
	if !ok {
		return Check{}, false
	}
	return *ch, true
}

// Checks returns all observed checks of a backend keyed by slot id.
func (c *Collector) Checks(be int64) map[int64]Check {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[int64]Check{}
	for k, v := range c.checks {
		if k.be == be {
			out[k.slot] = *v
		}
	}
	return out
}

// Latest returns the most recent point of a scope (zero if none).
func (c *Collector) Latest(scope Scope, id int64) Point {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r := c.rings[key{scope, id}]
	if r == nil || r.n == 0 {
		return Point{}
	}
	return r.last()
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
	"response_time_ms", "http_2xx", "http_3xx", "http_4xx", "http_5xx", "errors_per_second", "queue"}

func pick(p Point, t string) float64 { return float64(pick32(p, t)) }

func pick32(p Point, t string) float32 {
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
	case "queue":
		return p.Queue
	}
	return 0
}

// Query returns a range with resolution chosen per §10.4 (memory for ≤1 h, else 1 m rollups
// aggregated to 5 m / 1 h on read).
func (c *Collector) Query(ctx context.Context, scope Scope, id, start, end, step int64, types []string) (*Series, error) {
	now := time.Now().Unix()
	if end <= 0 || end > now+60 {
		end = now
	}
	if start <= 0 || start >= end {
		start = end - 300
	}
	if end-start > 35*86400 { // retention of metrics_1m
		start = end - 35*86400
	}
	span := end - start
	if len(types) == 0 {
		types = allTypes
	} else {
		known := map[string]bool{}
		for _, t := range allTypes {
			known[t] = true
		}
		seen := map[string]bool{}
		var exp []string
		for _, t := range types {
			for _, x := range map[bool][]string{true: {"bandwidth.in", "bandwidth.out"}, false: {t}}[t == "bandwidth"] {
				if known[x] && !seen[x] {
					seen[x] = true
					exp = append(exp, x)
				}
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
	if minStep := span / 2000; step < minStep { // at most ~2000 points per series
		step = minStep
	}
	var pts []Point
	if time.Now().Unix()-start <= 3600 && step < 60 {
		c.mu.RLock()
		if r := c.rings[key{scope, id}]; r != nil {
			pts = r.since(start)
		}
		c.mu.RUnlock()
	} else {
		rows, err := c.Q.QueryMetrics1m(ctx, db.QueryMetrics1mParams{Scope: string(scope), ScopeID: id, Ts: start, Ts_2: end})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			pts = append(pts, Point{TS: r.Ts, Open: float32(r.OpenConnAvg), CPS: float32(r.Cps), RPS: float32(r.Rps), In: float32(r.BytesIn), Out: float32(r.BytesOut),
				RespMS: float32(r.RespTimeMsAvg), H2: float32(r.Http2xx), H3: float32(r.Http3xx), H4: float32(r.Http4xx), H5: float32(r.Http5xx), Errors: float32(r.Errors),
				Queue: float32(r.Queue)})
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
