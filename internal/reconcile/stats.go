package reconcile

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// Counters are exported on loxy's own /metrics endpoint (§10.6).
type Counters struct {
	mu      sync.Mutex
	applies map[string]int64 // kind|result
	lastMs  map[string]int64 // kind → last duration
}

var Stats = &Counters{applies: map[string]int64{}, lastMs: map[string]int64{}}

func (c *Counters) record(kind, result string, ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applies[kind+"|"+result]++
	if result == "ok" {
		c.lastMs[kind] = ms
	}
}

// WriteProm writes Prometheus text format.
func (c *Counters) WriteProm(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.applies))
	for k := range c.applies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "# HELP loxy_applies_total Config applies by kind and result.\n# TYPE loxy_applies_total counter")
	for _, k := range keys {
		var kind, res string
		for i := range k {
			if k[i] == '|' {
				kind, res = k[:i], k[i+1:]
			}
		}
		fmt.Fprintf(w, "loxy_applies_total{kind=%q,result=%q} %d\n", kind, res, c.applies[k])
	}
	fmt.Fprintln(w, "# HELP loxy_apply_last_duration_ms Duration of the last successful apply.\n# TYPE loxy_apply_last_duration_ms gauge")
	for k, v := range c.lastMs {
		fmt.Fprintf(w, "loxy_apply_last_duration_ms{kind=%q} %d\n", k, v)
	}
}
