package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/2shiraz/loxy/internal/health"
	"github.com/2shiraz/loxy/internal/state"
)

type planView struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	MaxConnections    int64  `json:"max_connections"`
	MaxServices       int64  `json:"max_services"`
	MaxTargets        int64  `json:"max_targets"`
	MaxCertificates   int64  `json:"max_certificates"`
	TrafficQuotaBytes int64  `json:"traffic_quota_bytes"`
	PriceMonthlyCents *int64 `json:"price_monthly_cents"`
	Currency          string `json:"currency"`
}

func viewPlan(p *state.Plan) planView {
	if p == nil {
		return planView{}
	}
	return planView{ID: p.ID, Name: p.Name, MaxConnections: p.MaxConnections, MaxServices: p.MaxServices, MaxTargets: p.MaxTargets,
		MaxCertificates: p.MaxCertificates, TrafficQuotaBytes: p.TrafficQuotaBytes, PriceMonthlyCents: nullI64(p.PriceMonthlyCents), Currency: p.Currency}
}

type healthSummary struct {
	Status   string   `json:"status"`
	Healthy  int      `json:"healthy"`
	Total    int      `json:"total"`
	Messages []string `json:"messages"`
}

type checkView struct {
	TargetID int64   `json:"target_id"`
	SlotID   int64   `json:"slot_id"`
	ServerID *int64  `json:"server_id"`
	Name     *string `json:"name"`
	Address  string  `json:"address"`
	Status   string  `json:"status"`
	Check    string  `json:"check,omitempty"`
}

type serviceHealth struct {
	ServiceID int64       `json:"service_id"`
	Healthy   int         `json:"healthy"`
	Total     int         `json:"total"`
	Checks    []checkView `json:"checks"`
}

// healthTree builds per-service checks from observed state (§9).
func (s *Server) healthTree(lb *state.LB) (healthSummary, []serviceHealth) {
	snap := s.Snap.Get()
	var svcs []health.Service
	var out []serviceHealth
	var msgs []string
	for _, sv := range lb.Services {
		sh := serviceHealth{ServiceID: sv.ID, Checks: []checkView{}}
		hs := health.Service{ID: sv.ID}
		for _, t := range lb.Targets {
			for _, sl := range t.Slots {
				cv := checkView{TargetID: t.ID, SlotID: sl.ID, Address: sl.Address, Status: "unknown"}
				if sl.ServerID != 0 {
					cv.ServerID = ptr(sl.ServerID)
					if srv := snap.Server(sl.ServerID); srv != nil {
						cv.Name = ptr(srv.Name)
					}
				}
				if s.Metrics != nil {
					if ch, ok := s.Metrics.Check(lb.ID, sv.ID, sl.ID); ok {
						cv.Status, cv.Check = ch.Status, ch.Detail
					}
				}
				if cv.Status == "unhealthy" && len(msgs) < 3 {
					msgs = append(msgs, fmt.Sprintf("%s → :%d · %s", sl.Address, sv.DestinationPort, cv.Check))
				}
				hs.Checks = append(hs.Checks, cv.Status)
				sh.Checks = append(sh.Checks, cv)
			}
		}
		svcs = append(svcs, hs)
		out = append(out, sh)
	}
	sum := health.Aggregate(svcs)
	for i := range out {
		out[i].Healthy, out[i].Total = svcs[i].Healthy, svcs[i].Total
	}
	if msgs == nil {
		msgs = []string{}
		if sum.Status == "healthy" {
			msgs = append(msgs, "All services are healthy.")
		}
	}
	return healthSummary{Status: sum.Status, Healthy: sum.Healthy, Total: sum.Total, Messages: msgs}, out
}

func distinctCerts(lb *state.LB) int {
	m := map[int64]bool{}
	for _, s := range lb.Services {
		for _, c := range s.CertIDs {
			m[c] = true
		}
	}
	return len(m)
}

func resolvedCount(lb *state.LB) int {
	n := 0
	for _, t := range lb.Targets {
		n += len(t.Slots)
	}
	return n
}

func (s *Server) viewLB(lb *state.LB) map[string]any {
	snap := s.Snap.Get()
	node := snap.Node(lb.NodeID)
	loc := map[string]any{}
	if node != nil {
		loc = map[string]any{"city": node.LocationCity, "country": node.LocationCountry, "network_zone": node.NetworkZone, "lat": node.Lat, "lon": node.Lon}
	}
	nets := []map[string]any{}
	for _, n := range lb.Networks {
		nets = append(nets, map[string]any{"network_id": n.NetworkID, "ip": n.IP})
	}
	sum, _ := s.healthTree(lb)
	var open int64
	if s.Metrics != nil {
		open = s.Metrics.OpenConnections(lb.ID)
	}
	traffic := s.trafficOut(lb.ID)
	return map[string]any{
		"id": lb.ID, "name": lb.Name, "version": lb.Version, "node_id": lb.NodeID, "plan": viewPlan(lb.Plan), "location": loc,
		"public_net":       map[string]any{"enabled": lb.PublicEnabled != 0, "ipv4": nullStr(lb.IPv4), "ipv6": nullStr(lb.IPv6)},
		"private_net":      nets,
		"algorithm":        lb.Algorithm,
		"delete_protected": lb.DeleteProtected != 0,
		"maintenance":      lb.Maintenance != 0,
		"labels":           lb.Labels,
		"status":           lb.Status,
		"last_error":       nullStr(lb.LastError.String),
		"created_at":       lb.CreatedAt,
		"usage": map[string]any{"targets": resolvedCount(lb), "services": len(lb.Services), "certificates": distinctCerts(lb),
			"open_connections": open, "traffic_out_month_bytes": traffic},
		"health": sum,
	}
}

func viewService(sv *state.Service) map[string]any {
	certs := sv.CertIDs
	if certs == nil {
		certs = []int64{}
	}
	codes := sv.StatusCodes
	if codes == nil {
		codes = []string{}
	}
	var hcHTTP any
	if sv.HcProtocol != "tcp" {
		hcHTTP = map[string]any{"domain": nullStr(sv.HcDomain), "path": sv.HcPath, "response": nullStr(sv.HcResponse),
			"status_codes": codes, "tls_verify": sv.HcTlsVerify != 0}
	}
	return map[string]any{
		"id": sv.ID, "lb_id": sv.LbID, "version": sv.Version, "protocol": sv.Protocol, "listen_port": sv.ListenPort,
		"destination_port": sv.DestinationPort, "proxy_protocol": sv.ProxyProtocol,
		"http": map[string]any{"redirect_http": sv.HttpRedirect != 0, "timeout_s": sv.HttpTimeoutS, "certificates": certs,
			"sticky_sessions": sv.StickyEnabled != 0, "cookie_name": sv.StickyCookieName, "cookie_lifetime_s": sv.StickyCookieLifetimeS},
		"health_check": map[string]any{"protocol": sv.HcProtocol, "port": sv.HcPort, "interval_s": sv.HcIntervalS,
			"timeout_s": sv.HcTimeoutS, "retries": sv.HcRetries, "http": hcHTTP},
	}
}

func (s *Server) viewTarget(lb *state.LB, t *state.Target) map[string]any {
	snap := s.Snap.Get()
	_, tree := s.healthTree(lb)
	resolved := []map[string]any{}
	for _, sl := range t.Slots {
		h, total := 0, 0
		for _, sh := range tree {
			for _, c := range sh.Checks {
				if c.SlotID == sl.ID {
					total++
					if c.Status == "healthy" {
						h++
					}
				}
			}
		}
		item := map[string]any{"slot_id": sl.ID, "address": sl.Address, "healthy": h, "total": total, "server_id": nil, "name": nil,
			"public_ip": nil, "private_ip": nil}
		if sl.ServerID != 0 {
			item["server_id"] = sl.ServerID
			if srv := snap.Server(sl.ServerID); srv != nil {
				item["name"] = srv.Name
				item["public_ip"] = nullStr(firstNonEmpty(srv.PublicIpv4, srv.PublicIpv6))
				item["private_ip"] = nullStr(srv.PrivateIpv4)
			}
		} else {
			item["public_ip"] = sl.Address
		}
		resolved = append(resolved, item)
	}
	return map[string]any{
		"id": t.ID, "type": t.Type, "server_id": nullI64(t.ServerID), "label_selector": nullStr(t.LabelSelector), "ip": nullStr(t.Ip),
		"use_private_ip": t.UsePrivateIp != 0, "weight": t.Weight, "backup": t.Backup != 0, "admin_state": t.AdminState,
		"removing": t.Removing != 0, "resolved": resolved,
	}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func (s *Server) viewCert(c *state.Cert) map[string]any {
	used := []int64{}
	for _, lb := range s.Snap.Get().LBs {
		for _, sv := range lb.Services {
			for _, id := range sv.CertIDs {
				if id == c.ID {
					used = append(used, sv.ID)
				}
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

// lbAddresses returns the public IPs clients should resolve to (assigned IPs, else the node's primary IPs).
func (s *Server) lbAddresses(lb *state.LB) (string, string) {
	v4, v6 := lb.IPv4, lb.IPv6
	if v4 == "" && v6 == "" {
		if n := s.Snap.Get().Node(lb.NodeID); n != nil {
			for _, ip := range n.IPs {
				if ip.Kind == "primary" && ip.Family == 4 && v4 == "" {
					v4 = ip.Address
				}
				if ip.Kind == "primary" && ip.Family == 6 && v6 == "" {
					v6 = ip.Address
				}
			}
		}
	}
	return v4, v6
}

func (s *Server) viewDomain(d *state.Domain) map[string]any {
	req := []map[string]any{}
	if d.LbID.Valid {
		if lb := s.Snap.Get().LB(d.LbID.Int64); lb != nil {
			v4, v6 := s.lbAddresses(lb)
			if v4 != "" {
				req = append(req, map[string]any{"type": "A", "name": d.Fqdn, "value": v4, "ttl": d.Ttl})
			}
			if v6 != "" {
				req = append(req, map[string]any{"type": "AAAA", "name": d.Fqdn, "value": v6, "ttl": d.Ttl})
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
		"id": d.ID, "fqdn": d.Fqdn, "lb_id": nullI64(d.LbID), "service_id": nullI64(d.ServiceID), "dns_provider_id": nullI64(d.DnsProviderID),
		"zone": nullStr(d.Zone), "auto_records": d.AutoRecords != 0, "status": d.Status, "status_detail": nullStr(d.StatusDetail.String),
		"required_records": req, "observed": map[string]any{"a": a, "aaaa": aaaa}, "checked_at": nullI64(d.CheckedAt),
		"certificate_id": nullI64(d.CertificateID), "created_at": d.CreatedAt,
	}
}

var trafficCache struct {
	sync.Mutex
	at  time.Time
	out map[int64]int64
}

// trafficOut returns month-to-date bytes out, cached for 10 s (keeps list reads in memory).
func (s *Server) trafficOut(lb int64) int64 {
	trafficCache.Lock()
	defer trafficCache.Unlock()
	if time.Since(trafficCache.at) > 10*time.Second || trafficCache.out == nil {
		m := map[int64]int64{}
		rows, _ := s.Store.Q.ListTrafficMonth(context.Background(), time.Now().UTC().Format("2006-01"))
		for _, r := range rows {
			m[r.LbID] = r.BytesOut
		}
		trafficCache.out, trafficCache.at = m, time.Now()
	}
	return trafficCache.out[lb]
}
