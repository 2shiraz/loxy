package api

import (
	"fmt"

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

type resolvedView struct {
	SlotID   int64   `json:"slot_id"`
	ServerID int64   `json:"server_id"`
	HostID   *int64  `json:"host_id"`
	Name     *string `json:"name"`
	Address  string  `json:"address"`
	Port     int64   `json:"port"`
	Status   string  `json:"status"`
	Check    string  `json:"check,omitempty"`
	Sessions float64 `json:"sessions"`
}

// backendHealth computes the health of every resolved server of a backend (§9).
func (s *Server) backendHealth(b *state.Backend) (healthSummary, []resolvedView) {
	snap := s.Snap.Get()
	out := []resolvedView{}
	var statuses []string
	var msgs []string
	var checks map[int64]metrics.Check
	if s.Metrics != nil {
		checks = s.Metrics.Checks(b.ID)
	}
	for _, sv := range b.Servers {
		for _, sl := range sv.Slots {
			rv := resolvedView{SlotID: sl.ID, ServerID: sv.ID, Address: sl.Address, Port: sv.PortOr(b.Port), Status: "unknown"}
			if sl.HostID != 0 {
				rv.HostID = ptr(sl.HostID)
				if h := snap.Host(sl.HostID); h != nil {
					rv.Name = ptr(h.Name)
				}
			}
			if ch, ok := checks[sl.ID]; ok {
				rv.Status, rv.Check, rv.Sessions = ch.Status, ch.Detail, float64(ch.Scur)
			}
			if rv.Status == "unhealthy" && len(msgs) < 3 {
				msgs = append(msgs, fmt.Sprintf("%s:%d · %s", sl.Address, rv.Port, rv.Check))
			}
			statuses = append(statuses, rv.Status)
			out = append(out, rv)
		}
	}
	sum := health.Aggregate([]health.Service{{ID: b.ID, Checks: statuses}})
	if msgs == nil {
		msgs = []string{}
		if sum.Status == "healthy" {
			msgs = append(msgs, "All servers are healthy.")
		}
	}
	return healthSummary{Status: sum.Status, Healthy: sum.Healthy, Total: sum.Total, Messages: msgs}, out
}

// frontendHealth aggregates the backends a frontend routes to.
func (s *Server) frontendHealth(f *state.Frontend) healthSummary {
	snap := s.Snap.Get()
	var svcs []health.Service
	var msgs []string
	for _, id := range f.BackendIDs() {
		b := snap.Backend(id)
		if b == nil {
			continue
		}
		sum, rs := s.backendHealth(b)
		hs := health.Service{ID: b.ID}
		for _, r := range rs {
			hs.Checks = append(hs.Checks, r.Status)
		}
		svcs = append(svcs, hs)
		if sum.Status == "unhealthy" || sum.Status == "degraded" {
			msgs = append(msgs, fmt.Sprintf("Backend %s: %d/%d healthy", b.Name, sum.Healthy, sum.Total))
		}
	}
	sum := health.Aggregate(svcs)
	if msgs == nil {
		msgs = []string{}
	}
	return healthSummary{Status: sum.Status, Healthy: sum.Healthy, Total: sum.Total, Messages: msgs}
}

func (s *Server) viewFrontend(f *state.Frontend) map[string]any {
	certs := f.CertIDs
	if certs == nil {
		certs = []int64{}
	}
	binds := f.Binds
	if binds == nil {
		binds = []state.Bind{}
	}
	routes := []map[string]any{}
	for _, r := range f.Routes {
		hosts := r.Hosts
		if hosts == nil {
			hosts = []string{}
		}
		routes = append(routes, map[string]any{"id": r.ID, "hosts": hosts, "path_prefix": r.PathPrefix, "backend_id": r.BackendID})
	}
	var p metrics.Point
	if s.Metrics != nil {
		p = s.Metrics.Latest(metrics.FE, f.ID)
	}
	return map[string]any{
		"id": f.ID, "name": f.Name, "version": f.Version, "description": f.Description, "mode": f.Mode, "tls": f.Tls != 0,
		"binds": binds, "certificates": certs, "http_redirect": f.HttpRedirect != 0, "default_backend_id": nullI64(f.DefaultBackendID),
		"routes": routes, "timeout_client_s": f.TimeoutClientS, "maxconn": f.Maxconn, "forwarded_headers": f.ForwardedHeaders != 0,
		"enabled": f.Enabled != 0, "maintenance": f.Maintenance != 0, "labels": f.Labels, "status": f.Status,
		"last_error": nullStr(f.LastError.String), "created_at": f.CreatedAt,
		"stats":  map[string]any{"open_connections": p.Open, "requests_per_second": p.RPS, "bytes_out_per_second": p.Out},
		"health": s.frontendHealth(f),
	}
}

func viewHealthCheck(b *state.Backend) map[string]any {
	codes := b.StatusCodes
	if codes == nil {
		codes = []string{}
	}
	var hcHTTP any
	if b.HcProtocol != "tcp" {
		hcHTTP = map[string]any{"domain": nullStr(b.HcDomain), "path": b.HcPath, "response": nullStr(b.HcResponse),
			"status_codes": codes, "tls_verify": b.HcTlsVerify != 0}
	}
	port := b.HcPort
	if port == 0 {
		port = b.Port
	}
	return map[string]any{"protocol": b.HcProtocol, "port": port, "interval_s": b.HcIntervalS, "timeout_s": b.HcTimeoutS,
		"retries": b.HcRetries, "http": hcHTTP}
}

func (s *Server) viewBackend(b *state.Backend) map[string]any {
	sum, rs := s.backendHealth(b)
	var p metrics.Point
	if s.Metrics != nil {
		p = s.Metrics.Latest(metrics.BE, b.ID)
	}
	return map[string]any{
		"id": b.ID, "name": b.Name, "version": b.Version, "description": b.Description, "mode": b.Mode, "algorithm": b.Algorithm,
		"port": b.Port, "health_check": viewHealthCheck(b), "sticky_sessions": b.StickyEnabled != 0, "cookie_name": b.StickyCookieName,
		"cookie_lifetime_s": b.StickyCookieLifetimeS, "proxy_protocol": b.ProxyProtocol, "timeout_server_s": b.TimeoutServerS,
		"labels": b.Labels, "created_at": b.CreatedAt, "used_by": s.Snap.Get().UsedBy(b.ID), "servers_count": len(rs),
		"health": sum, "stats": map[string]any{"sessions": p.Open, "queue": p.Queue, "response_time_ms": p.RespMS},
	}
}

func (s *Server) viewServer(b *state.Backend, sv *state.Server) map[string]any {
	_, all := s.backendHealth(b)
	resolved := []resolvedView{}
	for _, r := range all {
		if r.ServerID == sv.ID {
			resolved = append(resolved, r)
		}
	}
	var port any
	if sv.Port.Valid {
		port = sv.Port.Int64
	}
	return map[string]any{
		"id": sv.ID, "backend_id": sv.BackendID, "type": sv.Type, "ip": nullStr(sv.Ip), "host_id": nullI64(sv.HostID),
		"label_selector": nullStr(sv.LabelSelector), "port": port, "use_private_ip": sv.UsePrivateIp != 0, "weight": sv.Weight,
		"backup": sv.Backup != 0, "admin_state": sv.AdminState, "removing": sv.Removing != 0, "resolved": resolved,
	}
}

func (s *Server) viewCert(c *state.Cert) map[string]any {
	used := []int64{}
	for _, f := range s.Snap.Get().Frontends {
		for _, id := range f.CertIDs {
			if id == c.ID {
				used = append(used, f.ID)
			}
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

// frontendAddresses returns the public IPs clients should resolve to: the frontend's explicit
// bind addresses, or the node's addresses when it binds on all interfaces.
func (s *Server) frontendAddresses(f *state.Frontend) (v4, v6 []string) {
	wildcard := false
	seen := map[string]bool{}
	for _, b := range f.Binds {
		if b.Address == "*" || b.Address == "" {
			wildcard = true
			continue
		}
		if seen[b.Address] {
			continue
		}
		seen[b.Address] = true
		if isV6(b.Address) {
			v6 = append(v6, b.Address)
		} else {
			v4 = append(v4, b.Address)
		}
	}
	if wildcard {
		if n := s.Snap.Get().Node(f.NodeID); n != nil {
			for _, a := range n.Addresses {
				if seen[a.Address] {
					continue
				}
				seen[a.Address] = true
				if a.Family == 6 {
					v6 = append(v6, a.Address)
				} else {
					v4 = append(v4, a.Address)
				}
			}
		}
	}
	return v4, v6
}

func (s *Server) viewDomain(d *state.Domain) map[string]any {
	req := []map[string]any{}
	if d.FrontendID.Valid {
		if f := s.Snap.Get().Frontend(d.FrontendID.Int64); f != nil {
			v4, v6 := s.frontendAddresses(f)
			for _, a := range v4 {
				req = append(req, map[string]any{"type": "A", "name": d.Fqdn, "value": a, "ttl": d.Ttl})
			}
			for _, a := range v6 {
				req = append(req, map[string]any{"type": "AAAA", "name": d.Fqdn, "value": a, "ttl": d.Ttl})
			}
		}
	}
	a, aaaa := d.Observed.A, d.Observed.AAAA
	if a == nil {
		a = []string{}
	}
	if aaaa == nil {
		aaaa = []string{}
	}
	return map[string]any{
		"id": d.ID, "fqdn": d.Fqdn, "frontend_id": nullI64(d.FrontendID), "dns_provider_id": nullI64(d.DnsProviderID),
		"zone": nullStr(d.Zone), "auto_records": d.AutoRecords != 0, "status": d.Status, "status_detail": nullStr(d.StatusDetail.String),
		"required_records": req, "observed": map[string]any{"a": a, "aaaa": aaaa}, "checked_at": nullI64(d.CheckedAt),
		"certificate_id": nullI64(d.CertificateID), "created_at": d.CreatedAt,
	}
}
