package api

import (
	"testing"

	"github.com/2shiraz/loxy/internal/metrics"
)

// Every series the Graphs page requests must also be in the Live stream, otherwise that chart
// stops updating (or shows "—") in Live mode.
func TestLivePointCarriesEverySeries(t *testing.T) {
	pt := livePoint(metrics.Point{TS: 1, RPS: 5, H2: 3, H4: 2, Errors: 1})
	for _, k := range metrics.SeriesTypes() {
		if _, ok := pt[k]; !ok {
			t.Errorf("live point misses %q", k)
		}
	}
	if pt["requests_per_second"] != 5.0 || pt["http_2xx"] != 3.0 || pt["http_4xx"] != 2.0 {
		t.Fatalf("values: %v", pt)
	}
}
