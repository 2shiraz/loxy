// Package health aggregates per-check status into server/backend/frontend health (spec §9).
package health

// Check status values: healthy | unhealthy | maintenance | draining | unknown.

type Service struct {
	ID      int64
	Checks  []string // status per resolved target
	Healthy int
	Total   int
}

type Summary struct {
	Status  string // healthy | degraded | unhealthy | unknown
	Healthy int
	Total   int
}

// Aggregate computes the aggregate status: Healthy if all checks healthy, Degraded if some
// unhealthy but every service has ≥1 healthy target, Unhealthy if any service has 0
// healthy targets, Unknown without targets (or without observations yet).
func Aggregate(services []Service) Summary {
	s := Summary{Status: "unknown"}
	anyObserved := false
	allHealthy := true
	anyServiceDead := false
	for i := range services {
		sv := &services[i]
		sv.Healthy, sv.Total = 0, len(sv.Checks)
		for _, c := range sv.Checks {
			if c == "healthy" {
				sv.Healthy++
			}
			if c != "unknown" {
				anyObserved = true
			}
			if c != "healthy" {
				allHealthy = false
			}
		}
		s.Healthy += sv.Healthy
		s.Total += sv.Total
		if sv.Total > 0 && sv.Healthy == 0 {
			anyServiceDead = true
		}
	}
	switch {
	case s.Total == 0 || !anyObserved:
		s.Status = "unknown"
	case allHealthy:
		s.Status = "healthy"
	case anyServiceDead:
		s.Status = "unhealthy"
	default:
		s.Status = "degraded"
	}
	return s
}
