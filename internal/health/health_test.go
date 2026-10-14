package health

import "testing"

func TestAggregate(t *testing.T) {
	cases := []struct {
		in   [][]string
		want string
	}{
		{nil, "unknown"},
		{[][]string{{}}, "unknown"},
		{[][]string{{"unknown", "unknown"}}, "unknown"},
		{[][]string{{"healthy", "healthy"}, {"healthy"}}, "healthy"},
		{[][]string{{"healthy", "unhealthy"}, {"healthy"}}, "degraded"},
		{[][]string{{"healthy"}, {"unhealthy"}}, "unhealthy"},
		{[][]string{{"maintenance", "healthy"}}, "degraded"},
	}
	for _, c := range cases {
		var svcs []Service
		for i, ch := range c.in {
			svcs = append(svcs, Service{ID: int64(i), Checks: ch})
		}
		if got := Aggregate(svcs).Status; got != c.want {
			t.Errorf("%v: got %s want %s", c.in, got, c.want)
		}
	}
}
