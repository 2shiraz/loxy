package api

import (
	"fmt"
	"net/netip"

	"github.com/2shiraz/loxy/internal/health"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/state"
)

type healthSummary struct {
	Status   string   `json:"status"`
	Healthy  int      `json:"healthy"`
	Total    int      `json:"total"`
	Messages []string `json:"messages"`
}

type targetCheck struct {
	ServiceID int64   `json:"service_id"`
	Status    string  `json:"status"`
	Check     string  `json:"check,omitempty"`
	Sessions  float64 `json:"sessions"`
}

type resolvedView struct {
	SlotID   int64         `json:"slot_id"`
	TargetID int64         `json:"-"`
	HostID   *int64        `json:"host_id"`
	Name     *string       `json:"name"`
	Address  string        `json:"address"`
	Status   string        `json:"status"`
	Checks   []targetCheck `json:"checks"`
}

func isV6(a string) bool {
	x, err := netip.ParseAddr(a)
	return err == nil && x.Is6()
}

// projectHealth computes per-target health across all services of a project (§9): a slot is
// healthy when every service's check passes, unhealthy when all fail, degraded in between.
func (s *Server) projectHealth(p *state.Project) (healthSummary, []resolvedView) {
	snap := s.Snap.Get()
	checks := map[int64]map[int64]metrics.Check{}
	if s.Metrics != nil {
		for _, sv := range p.Services {
			checks[sv.ID] = s.Metrics.Checks(sv.ID)
		}
	}
	out := []resolvedView{}
	var statuses []string
	var msgs []string
	for _, t := range p.Targets {
		for _, sl := range t.Slots {
			rv := resolvedView{SlotID: sl.ID, TargetID: t.ID, Address: sl.Address, Status: "unknown", Checks: []targetCheck{}}
			if sl.HostID != 0 {
				rv.HostID = ptr(sl.HostID)
				if h := snap.Host(sl.HostID); h != nil {
					rv.Name = ptr(h.Name)
				}
			}
			var st []string
			for _, sv := range p.Services {
				tc := targetCheck{ServiceID: sv.ID, Status: "unknown"}
				if ch, ok := checks[sv.ID][sl.ID]; ok {
					tc.Status, tc.Check, tc.Sessions = ch.Status, ch.Detail, float64(ch.Scur)
				}
				if tc.Status == "unhealthy" && len(msgs) < 3 {
					msgs = append(msgs, fmt.Sprintf("%s:%d (:%d) · %s", sl.Address, t.PortOr(sv.DestinationPort), sv.ListenPort, tc.Check))
				}
				rv.Checks = append(rv.Checks, tc)
				st = append(st, tc.Status)
			}
			rv.Status = health.Aggregate([]health.Service{{ID: t.ID, Checks: st}}).Status
			statuses = append(statuses, rv.Status)
			out = append(out, rv)
		}
	}
	sum := health.Aggregate([]health.Service{{ID: p.ID, Checks: statuses}})
	if msgs == nil {
		msgs = []string{}
		if sum.Status == "healthy" {
			msgs = append(msgs, "All targets are healthy.")
		}
	}
	return healthSummary{Status: sum.Status, Healthy: sum.Healthy, Total: sum.Total, Messages: msgs}, out
}

// serviceHealth is the health of a project's targets as seen by one service's checks.
func (s *Server) serviceHealth(p *state.Project, sv *state.Service) healthSummary {
	var checks map[int64]metrics.Check
	if s.Metrics != nil {
		checks = s.Metrics.Checks(sv.ID)
	}
	var st []string
	for _, sl := range p.Slots() {
		status := "unknown"
		if ch, ok := checks[sl.ID]; ok {
			status = ch.Status
		}
		st = append(st, status)
	}
	sum := health.Aggregate([]health.Service{{ID: sv.ID, Checks: st}})
	return healthSummary{Status: sum.Status, Healthy: sum.Healthy, Total: sum.Total, Messages: []string{}}
}

// endpoints lists where a project is reachable: one URL per service port, then its domains.
func (s *Server) endpoints(p *state.Project) []map[string]any {
	snap := s.Snap.Get()
	host := "<server-ip>"
	if n := snap.Node(p.NodeID); n != nil && len(n.Addresses) > 0 {
		host = n.Addresses[0].Address
		if isV6(host) {
			host = "[" + host + "]"
		}
	}
	out := []map[string]any{}
	for _, sv := range p.Services {
		scheme := sv.Protocol
		out = append(out, map[string]any{"kind": "port", "service_id": sv.ID, "domain_id": nil,
			"url": fmt.Sprintf("%s://%s:%d", scheme, host, sv.ListenPort)})
	}
	for _, d := range snap.DomainsOf(p.ID) {
		out = append(out, map[string]any{"kind": "domain", "service_id": nil, "domain_id": d.ID, "url": s.domainURL(d)})
	}
	return out
}

func (s *Server) viewProject(p *state.Project) map[string]any {
	sum, _ := s.projectHealth(p)
	var pt metrics.Point
	if s.Metrics != nil {
		pt = s.Metrics.Latest(metrics.PR, p.ID)
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "version": p.Version, "description": p.Description, "algorithm": p.Algorithm, "labels": p.Labels,
		"maintenance": p.Maintenance != 0, "status": p.Status, "last_error": nullStr(p.LastError.String), "created_at": p.CreatedAt,
		"endpoints": s.endpoints(p),
		"counts":    map[string]int{"targets": len(p.Targets), "services": len(p.Services), "domains": len(s.Snap.Get().DomainsOf(p.ID))},
		"health":    sum,
		"stats":     map[string]any{"open_connections": pt.Open, "requests_per_second": pt.RPS, "bytes_out_per_second": pt.Out},
	}
}

func viewHealthCheck(sv *state.Service) map[string]any {
	codes := sv.StatusCodes
	if codes == nil {
		codes = []string{}
	}
	var hcHTTP any
	if sv.HcProtocol != "tcp" {
		hcHTTP = map[string]any{"domain": nullStr(sv.HcDomain), "path": sv.HcPath, "response": nullStr(sv.HcResponse),
			"status_codes": codes, "tls_verify": sv.HcTlsVerify != 0}
	}
	var port any // null: each target is checked on its own port
	if sv.HcPort != 0 {
		port = sv.HcPort
	}
	return map[string]any{"protocol": sv.HcProtocol, "port": port, "interval_s": sv.HcIntervalS, "timeout_s": sv.HcTimeoutS,
		"retries": sv.HcRetries, "http": hcHTTP}
}

func (s *Server) viewService(p *state.Project, sv *state.Service) map[string]any {
	certs := sv.CertIDs
	if certs == nil {
		certs = []int64{}
	}
	snap := s.Snap.Get()
	doms := []string{}
	for _, d := range snap.DomainsOf(p.ID) {
		if ds := snap.DomainService(d); ds != nil && ds.ID == sv.ID {
			doms = append(doms, d.Fqdn)
		}
	}
	return map[string]any{
		"id": sv.ID, "project_id": p.ID, "version": sv.Version, "protocol": sv.Protocol, "listen_port": sv.ListenPort, "destination_port": sv.DestinationPort,
		"proxy_protocol": sv.ProxyProtocol, "timeout_s": sv.TimeoutS, "certificates": certs, "sticky_sessions": sv.StickyEnabled != 0,
		"cookie_name": sv.StickyCookieName, "cookie_lifetime_s": sv.StickyCookieLifetimeS, "health_check": viewHealthCheck(sv),
		"health": s.serviceHealth(p, sv), "domains": doms,
	}
}

// targetViewer computes the project's health once and returns a view func for its targets.
func (s *Server) targetViewer(p *state.Project) func(t *state.Server) map[string]any {
	_, all := s.projectHealth(p)
	byTarget := map[int64][]resolvedView{}
	for _, r := range all {
		byTarget[r.TargetID] = append(byTarget[r.TargetID], r)
	}
	return func(t *state.Server) map[string]any {
		resolved := byTarget[t.ID]
		if resolved == nil {
			resolved = []resolvedView{}
		}
		var port any
		if t.Port.Valid {
			port = t.Port.Int64
		}
		return map[string]any{
			"id": t.ID, "project_id": t.BalancerID, "type": t.Type, "ip": nullStr(t.Ip), "host_id": nullI64(t.HostID),
			"label_selector": nullStr(t.LabelSelector), "port": port, "use_private_ip": t.UsePrivateIp != 0, "weight": t.Weight,
			"backup": t.Backup != 0, "admin_state": t.AdminState, "removing": t.Removing != 0, "resolved": resolved,
		}
	}
}

func (s *Server) viewCert(c *state.Cert) map[string]any {
	snap := s.Snap.Get()
	used := []int64{}
	add := func(id int64) {
		for _, u := range used {
			if u == id {
				return
			}
		}
		used = append(used, id)
	}
	for _, p := range snap.Projects {
		for _, sv := range p.Services {
			for _, id := range sv.CertIDs {
				if id == c.ID {
					add(p.ID)
				}
			}
		}
	}
	for _, d := range snap.Domains {
		if d.CertificateID.Valid && d.CertificateID.Int64 == c.ID && d.BalancerID.Valid {
			add(d.BalancerID.Int64)
		}
	}
	domains := c.Domains
	if domains == nil {
		domains = []string{}
	}
	var ch any
	if c.Challenge != "" {
		ch = c.Challenge
	}
	return map[string]any{
		"id": c.ID, "name": c.Name, "type": c.Type, "domains": domains, "status": c.Status, "challenge": ch,
		"dns_provider_id": nullI64(c.DnsProviderID), "key_type": c.KeyType, "issuer": nullStr(c.Issuer), "serial": nullStr(c.Serial),
		"fingerprint_sha256": nullStr(c.FingerprintSha256), "not_before": nullI64(c.NotBefore), "not_after": nullI64(c.NotAfter),
		"last_error": nullStr(c.LastError.String), "used_by": used, "labels": c.Labels, "created_at": c.CreatedAt,
	}
}

// nodeAddresses returns the public addresses domains must resolve to (the node's addresses).
func (s *Server) nodeAddresses() (v4, v6 []string) {
	snap := s.Snap.Get()
	if len(snap.Nodes) == 0 {
		return nil, nil
	}
	for _, a := range snap.Nodes[0].Addresses {
		if a.Family == 6 {
			v6 = append(v6, a.Address)
		} else {
			v4 = append(v4, a.Address)
		}
	}
	return v4, v6
}

// domainURL is https://fqdn once the domain serves its certificate, else http://fqdn.
func (s *Server) domainURL(d *state.Domain) string {
	if d.Https != 0 && d.CertificateID.Valid {
		if c := s.Snap.Get().Cert(d.CertificateID.Int64); c != nil && c.PEM() != "" {
			return "https://" + d.Fqdn
		}
	}
	return "http://" + d.Fqdn
}

func (s *Server) viewDomain(d *state.Domain) map[string]any {
	req := []map[string]any{}
	if d.BalancerID.Valid {
		v4, v6 := s.nodeAddresses()
		for _, a := range v4 {
			req = append(req, map[string]any{"type": "A", "name": d.Fqdn, "value": a, "ttl": d.Ttl})
		}
		for _, a := range v6 {
			req = append(req, map[string]any{"type": "AAAA", "name": d.Fqdn, "value": a, "ttl": d.Ttl})
		}
	}
	a, aaaa := d.Observed.A, d.Observed.AAAA
	if a == nil {
		a = []string{}
	}
	if aaaa == nil {
		aaaa = []string{}
	}
	var svcID any
	if sv := s.Snap.Get().DomainService(d); sv != nil {
		svcID = sv.ID
	}
	return map[string]any{
		"id": d.ID, "fqdn": d.Fqdn, "project_id": nullI64(d.BalancerID), "service_id": svcID, "https": d.Https != 0, "url": s.domainURL(d),
		"dns_provider_id": nullI64(d.DnsProviderID), "zone": nullStr(d.Zone), "auto_records": d.AutoRecords != 0, "status": d.Status,
		"status_detail": nullStr(d.StatusDetail.String), "required_records": req, "observed": map[string]any{"a": a, "aaaa": aaaa},
		"checked_at": nullI64(d.CheckedAt), "certificate_id": nullI64(d.CertificateID), "created_at": d.CreatedAt,
	}
}
