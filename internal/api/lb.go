package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

var algorithms = map[string]bool{"roundrobin": true, "leastconn": true, "source": true, "random": true}

func (s *Server) ListLoadBalancers(w http.ResponseWriter, r *http.Request, p gen.ListLoadBalancersParams) {
	var sel model.Selector
	if p.LabelSelector != nil && *p.LabelSelector != "" {
		var err error
		if sel, err = model.ParseSelector(*p.LabelSelector); err != nil {
			fail(w, errField("label_selector", err.Error()))
			return
		}
	}
	out := []map[string]any{}
	for _, lb := range s.Snap.Get().LBs {
		if p.Name != nil && *p.Name != "" && !strings.Contains(strings.ToLower(lb.Name), strings.ToLower(*p.Name)) {
			continue
		}
		if sel != nil && !sel.Matches(lb.Labels) {
			continue
		}
		out = append(out, s.viewLB(lb))
	}
	writeJSON(w, 200, map[string]any{"load_balancers": out})
}

func (s *Server) GetLoadBalancer(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"load_balancer": s.viewLB(lb)})
}

func (s *Server) respondLB(w http.ResponseWriter, status int, id int64) {
	lb := s.Snap.Get().LB(id)
	if lb == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"load_balancer": s.viewLB(lb)})
}

// freeIP picks an unassigned public address of the given family from the node pool.
func freeIP(node *state.Node, family int64, taken map[int64]bool) *state.IP {
	for _, ip := range node.IPs {
		if ip.Family == family && ip.Kind != "private" && !ip.AssignedLbID.Valid && !taken[ip.ID] {
			return ip
		}
	}
	return nil
}

func (s *Server) CreateLoadBalancer(w http.ResponseWriter, r *http.Request) {
	var in gen.LoadBalancerCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	snap := s.Snap.Get()
	fe := model.FieldErrors{}
	if !model.ValidName(in.Name) {
		fe.Add("name", "1–64 printable characters")
	}
	for _, lb := range snap.LBs {
		if lb.Name == in.Name {
			fe.Add("name", "already in use")
		}
	}
	plan := snap.Plan(in.PlanId)
	if plan == nil {
		fe.Add("plan_id", "unknown plan")
	}
	nodeID := int64(1)
	if in.NodeId != nil {
		nodeID = *in.NodeId
	}
	node := snap.Node(nodeID)
	if node == nil {
		fe.Add("node_id", "unknown node")
	}
	alg := "roundrobin"
	if in.Algorithm != nil {
		alg = string(*in.Algorithm)
	}
	if !algorithms[alg] {
		fe.Add("algorithm", "invalid algorithm")
	}
	labels := map[string]string{}
	if in.Labels != nil {
		labels = *in.Labels
	}
	model.ValidateLabels(fe, "labels", labels)
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	public := in.PublicEnabled == nil || *in.PublicEnabled
	var v4, v6 *state.IP
	pick := func(id *int64, fam int64) (*state.IP, error) {
		if id != nil {
			for _, ip := range node.IPs {
				if ip.ID == *id && ip.Family == fam {
					if ip.AssignedLbID.Valid {
						return nil, errField(fmt.Sprintf("ipv%d_id", fam), "address already assigned")
					}
					return ip, nil
				}
			}
			return nil, errField(fmt.Sprintf("ipv%d_id", fam), "unknown address")
		}
		return freeIP(node, fam, nil), nil
	}
	var err error
	if v4, err = pick(in.Ipv4Id, 4); err != nil {
		fail(w, err)
		return
	}
	if v6, err = pick(in.Ipv6Id, 6); err != nil {
		fail(w, err)
		return
	}
	// Build a provisional LB to validate services/targets against.
	tmp := &state.LB{LoadBalancer: db.LoadBalancer{ID: 0, NodeID: nodeID, Algorithm: alg, PublicEnabled: b2i(public)}, Plan: plan}
	if v4 != nil {
		tmp.IPv4 = v4.Address
	}
	if v6 != nil {
		tmp.IPv6 = v6.Address
	}
	var svcs []db.CreateServiceParams
	var svcCerts [][]int64
	if in.Services != nil {
		if len(*in.Services) > int(plan.MaxServices) {
			fail(w, errQuota(fmt.Sprintf("plan %s allows %d services", plan.Name, plan.MaxServices)))
			return
		}
		for i, si := range *in.Services {
			p, certs, err := s.buildService(tmp, nil, si)
			if err != nil {
				fail(w, prefixErr(err, fmt.Sprintf("services[%d].", i)))
				return
			}
			tmp.Services = append(tmp.Services, &state.Service{Service: db.Service{ID: int64(-i - 1), Protocol: p.Protocol, ListenPort: p.ListenPort, HttpRedirect: p.HttpRedirect}, CertIDs: certs})
			svcs = append(svcs, p)
			svcCerts = append(svcCerts, certs)
		}
	}
	var targets []db.CreateTargetParams
	if in.Targets != nil {
		ts, err := s.buildTargets(tmp, *in.Targets)
		if err != nil {
			fail(w, err)
			return
		}
		targets = ts
	}
	var newID int64
	err = s.mutate(r, "load_balancer.create", "load_balancer", func() int64 { return newID }, in, func(q *db.Queries) error {
		now := nowMs()
		p := db.CreateLoadBalancerParams{Name: in.Name, PlanID: plan.ID, NodeID: nodeID, Algorithm: alg, PublicEnabled: b2i(public), Labels: jsonStr(labels), CreatedAt: now, UpdatedAt: now}
		if v4 != nil {
			p.Ipv4ID = sql.NullInt64{Int64: v4.ID, Valid: true}
		}
		if v6 != nil {
			p.Ipv6ID = sql.NullInt64{Int64: v6.ID, Valid: true}
		}
		lb, err := q.CreateLoadBalancer(r.Context(), p)
		if err != nil {
			return err
		}
		newID = lb.ID
		for _, ip := range []*state.IP{v4, v6} {
			if ip != nil {
				if err := q.AssignIP(r.Context(), db.AssignIPParams{AssignedLbID: sql.NullInt64{Int64: lb.ID, Valid: true}, ID: ip.ID}); err != nil {
					return err
				}
			}
		}
		for i, sp := range svcs {
			sp.LbID = lb.ID
			sv, err := q.CreateService(r.Context(), sp)
			if err != nil {
				return err
			}
			for pos, c := range svcCerts[i] {
				if err := q.AddServiceCertificate(r.Context(), db.AddServiceCertificateParams{ServiceID: sv.ID, CertificateID: c, Position: int64(pos)}); err != nil {
					return err
				}
			}
		}
		for _, tp := range targets {
			tp.LbID = lb.ID
			if _, err := q.CreateTarget(r.Context(), tp); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	s.respondLB(w, 201, newID)
}

func prefixErr(err error, prefix string) error {
	ae, ok := err.(*apiError)
	if !ok || ae.Fields == nil {
		return err
	}
	fe := model.FieldErrors{}
	for k, v := range ae.Fields {
		fe[prefix+k] = v
	}
	return errInput(fe)
}

func checkIfMatch(r *http.Request, version int64) error {
	if m := r.Header.Get("If-Match"); m != "" && strings.Trim(m, `"W/`) != fmt.Sprint(version) {
		return errConflict("resource was modified (version mismatch)")
	}
	return nil
}

func (s *Server) saveLB(r *http.Request, lb *state.LB, action string, after any, edit func(p *db.UpdateLoadBalancerParams) error, extra func(q *db.Queries) error) error {
	if err := checkIfMatch(r, lb.Version); err != nil {
		return err
	}
	p := db.UpdateLoadBalancerParams{Name: lb.Name, PlanID: lb.PlanID, Algorithm: lb.Algorithm, PublicEnabled: lb.PublicEnabled,
		DeleteProtected: lb.DeleteProtected, Maintenance: lb.Maintenance, Labels: jsonStr(lb.Labels), ID: lb.ID}
	if err := edit(&p); err != nil {
		return err
	}
	p.UpdatedAt = nowMs()
	return s.mutate(r, action, "load_balancer", func() int64 { return lb.ID }, after, func(q *db.Queries) error {
		if err := q.UpdateLoadBalancer(r.Context(), p); err != nil {
			return err
		}
		if extra != nil {
			return extra(q)
		}
		return nil
	})
}

func (s *Server) UpdateLoadBalancer(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.LoadBalancerUpdate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	err = s.saveLB(r, lb, "load_balancer.update", in, func(p *db.UpdateLoadBalancerParams) error {
		fe := model.FieldErrors{}
		if in.Name != nil {
			if !model.ValidName(*in.Name) {
				fe.Add("name", "1–64 printable characters")
			}
			for _, o := range s.Snap.Get().LBs {
				if o.ID != lb.ID && o.Name == *in.Name {
					fe.Add("name", "already in use")
				}
			}
			p.Name = *in.Name
		}
		if in.Labels != nil {
			model.ValidateLabels(fe, "labels", *in.Labels)
			p.Labels = jsonStr(*in.Labels)
		}
		if in.Algorithm != nil {
			if !algorithms[string(*in.Algorithm)] {
				fe.Add("algorithm", "invalid algorithm")
			}
			p.Algorithm = string(*in.Algorithm)
		}
		if in.DeleteProtected != nil {
			p.DeleteProtected = b2i(*in.DeleteProtected)
		}
		if in.Maintenance != nil {
			p.Maintenance = b2i(*in.Maintenance)
		}
		if !fe.Empty() {
			return errInput(fe)
		}
		return nil
	}, nil)
	if err != nil {
		fail(w, err)
		return
	}
	s.respondLB(w, 200, lb.ID)
}

func (s *Server) DeleteLoadBalancer(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	if lb.DeleteProtected != 0 {
		writeErr(w, 423, "protected", "delete protection is enabled", nil)
		return
	}
	err = s.mutate(r, "load_balancer.delete", "load_balancer", func() int64 { return lb.ID }, map[string]any{"name": lb.Name}, func(q *db.Queries) error {
		if err := q.ReleaseLBIPs(r.Context(), sql.NullInt64{Int64: lb.ID, Valid: true}); err != nil {
			return err
		}
		return q.DeleteLoadBalancer(r.Context(), lb.ID)
	})
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) ChangePlan(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.ChangePlanJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	plan := s.Snap.Get().Plan(in.PlanId)
	if plan == nil {
		fail(w, errField("plan_id", "unknown plan"))
		return
	}
	switch {
	case int64(len(lb.Services)) > plan.MaxServices:
		fail(w, errQuota(fmt.Sprintf("You use %d services; %s allows %d", len(lb.Services), strings.ToUpper(plan.Name), plan.MaxServices)))
		return
	case int64(resolvedCount(lb)) > plan.MaxTargets:
		fail(w, errQuota(fmt.Sprintf("You use %d targets; %s allows %d", resolvedCount(lb), strings.ToUpper(plan.Name), plan.MaxTargets)))
		return
	case int64(distinctCerts(lb)) > plan.MaxCertificates:
		fail(w, errQuota(fmt.Sprintf("You use %d certificates; %s allows %d", distinctCerts(lb), strings.ToUpper(plan.Name), plan.MaxCertificates)))
		return
	}
	if err := s.saveLB(r, lb, "load_balancer.change_plan", in, func(p *db.UpdateLoadBalancerParams) error { p.PlanID = plan.ID; return nil }, nil); err != nil {
		fail(w, err)
		return
	}
	s.respondLB(w, 200, lb.ID)
}

func (s *Server) EnablePublicInterface(w http.ResponseWriter, r *http.Request, id gen.Id) {
	s.setPublic(w, r, id, true)
}
func (s *Server) DisablePublicInterface(w http.ResponseWriter, r *http.Request, id gen.Id) {
	s.setPublic(w, r, id, false)
}

func (s *Server) setPublic(w http.ResponseWriter, r *http.Request, id int64, on bool) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	if !on && len(lb.Networks) == 0 {
		fail(w, errField("public_enabled", "attach a private network before disabling the public interface"))
		return
	}
	if err := s.saveLB(r, lb, "load_balancer.public_interface", map[string]any{"enabled": on}, func(p *db.UpdateLoadBalancerParams) error {
		p.PublicEnabled = b2i(on)
		return nil
	}, nil); err != nil {
		fail(w, err)
		return
	}
	s.respondLB(w, 200, lb.ID)
}

func (s *Server) AttachToNetwork(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.AttachNetwork
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	var cidr netip.Prefix
	found := false
	for _, n := range s.Snap.Get().Networks {
		if n.ID == in.NetworkId {
			cidr, _ = netip.ParsePrefix(n.Cidr)
			found = true
		}
	}
	if !found {
		fail(w, errField("network_id", "unknown network"))
		return
	}
	ip, err := netip.ParseAddr(in.Ip)
	if err != nil || !cidr.Contains(ip) {
		fail(w, errField("ip", "must be an address inside "+cidr.String()))
		return
	}
	for _, n := range lb.Networks {
		if n.NetworkID == in.NetworkId {
			fail(w, errConflict("already attached to this network"))
			return
		}
	}
	if err := s.saveLB(r, lb, "load_balancer.attach_network", in, func(*db.UpdateLoadBalancerParams) error { return nil }, func(q *db.Queries) error {
		return q.AttachLBNetwork(r.Context(), db.AttachLBNetworkParams{LbID: lb.ID, NetworkID: in.NetworkId, Ip: ip.String()})
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondLB(w, 200, lb.ID)
}

func (s *Server) DetachFromNetwork(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.DetachFromNetworkJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	for _, t := range lb.Targets {
		if t.UsePrivateIp != 0 {
			fail(w, errConflict("targets use private IPs through this network"))
			return
		}
	}
	if lb.PublicEnabled == 0 && len(lb.Networks) <= 1 {
		fail(w, errConflict("the public interface is disabled; this is the last network"))
		return
	}
	if err := s.saveLB(r, lb, "load_balancer.detach_network", in, func(*db.UpdateLoadBalancerParams) error { return nil }, func(q *db.Queries) error {
		return q.DetachLBNetwork(r.Context(), db.DetachLBNetworkParams{LbID: lb.ID, NetworkID: in.NetworkId})
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondLB(w, 200, lb.ID)
}

func (s *Server) RetryApply(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if _, err := s.lb(id); err != nil {
		fail(w, err)
		return
	}
	s.Rec.Trigger()
	writeJSON(w, 202, map[string]any{"action": map[string]any{"command": "retry_apply", "status": "running"}})
}

func (s *Server) GetLoadBalancerStatus(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	sum, tree := s.healthTree(lb)
	if tree == nil {
		tree = []serviceHealth{}
	}
	writeJSON(w, 200, map[string]any{"summary": sum, "services": tree})
}

func (s *Server) GetLoadBalancerConfig(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if _, err := s.lb(id); err != nil {
		fail(w, err)
		return
	}
	out, err := s.Rec.RenderLive(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(render.LBSections(out.Config(), id)))
}

// ---------- services ----------

func (s *Server) ListServices(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, sv := range lb.Services {
		out = append(out, viewService(sv))
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) GetService(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	sv := lb.Service(sid)
	if sv == nil {
		fail(w, errNotFound("service"))
		return
	}
	writeJSON(w, 200, map[string]any{"service": viewService(sv)})
}

func reservedPort(p int64, panel int) bool { return p == int64(panel) }

// addrsOverlap reports whether two LBs can collide on a port (same address or wildcard).
func addrsOverlap(a, b *state.LB) bool {
	aa, bb := render.BindAddrs(a), render.BindAddrs(b)
	for _, x := range aa {
		for _, y := range bb {
			if x == y || x == "" || y == "" {
				return true
			}
		}
	}
	return false
}

// buildService validates a service input (§8.7) and returns DB params plus certificate ids.
func (s *Server) buildService(lb *state.LB, existing *state.Service, in gen.ServiceInput) (db.CreateServiceParams, []int64, error) {
	snap := s.Snap.Get()
	fe := model.FieldErrors{}
	p := db.CreateServiceParams{Protocol: string(in.Protocol), ListenPort: int64(in.ListenPort), DestinationPort: int64(in.DestinationPort),
		ProxyProtocol: "none", HttpTimeoutS: 60, StickyCookieName: "LBSTICKY", StickyCookieLifetimeS: 300,
		HcProtocol: "tcp", HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/", HcStatusCodes: `["2??","3??"]`, CreatedAt: nowMs()}
	if p.Protocol != "tcp" && p.Protocol != "http" && p.Protocol != "https" {
		fe.Add("protocol", "must be tcp, http or https")
	}
	if !model.ValidPort(in.ListenPort) {
		fe.Add("listen_port", "must be 1–65535")
	}
	if !model.ValidPort(in.DestinationPort) {
		fe.Add("destination_port", "must be 1–65535")
	}
	if in.ProxyProtocol != nil {
		p.ProxyProtocol = string(*in.ProxyProtocol)
		if p.ProxyProtocol != "none" && p.ProxyProtocol != "v1" && p.ProxyProtocol != "v2" {
			fe.Add("proxy_protocol", "must be none, v1 or v2")
		}
	}
	var certIDs []int64
	if h := in.Http; h != nil {
		if h.RedirectHttp != nil {
			p.HttpRedirect = b2i(*h.RedirectHttp)
		}
		if h.TimeoutS != nil {
			p.HttpTimeoutS = int64(*h.TimeoutS)
		}
		if h.StickySessions != nil {
			p.StickyEnabled = b2i(*h.StickySessions)
		}
		if h.CookieName != nil && *h.CookieName != "" {
			p.StickyCookieName = *h.CookieName
		}
		if h.CookieLifetimeS != nil {
			p.StickyCookieLifetimeS = int64(*h.CookieLifetimeS)
		}
		if h.Certificates != nil {
			certIDs = *h.Certificates
		}
	}
	if p.HttpTimeoutS < 1 || p.HttpTimeoutS > 3600 {
		fe.Add("http.timeout_s", "must be 1–3600")
	}
	if !model.ValidCookieName(p.StickyCookieName) {
		fe.Add("http.cookie_name", "letters, digits, - and _ only (1–64)")
	}
	if p.StickyCookieLifetimeS < 1 || p.StickyCookieLifetimeS > 31536000 {
		fe.Add("http.cookie_lifetime_s", "must be 1–31536000")
	}
	if p.HttpRedirect != 0 && p.Protocol != "https" {
		fe.Add("http.redirect_http", "only available for https services")
	}
	if p.StickyEnabled != 0 && p.Protocol == "tcp" {
		fe.Add("http.sticky_sessions", "only available for http/https services")
	}
	if p.Protocol != "https" {
		certIDs = nil
	} else if len(certIDs) == 0 {
		fe.Add("http.certificates", "https requires at least one certificate")
	}
	seen := map[int64]bool{}
	for _, c := range certIDs {
		if snap.Cert(c) == nil {
			fe.Add("http.certificates", fmt.Sprintf("unknown certificate %d", c))
		}
		if seen[c] {
			fe.Add("http.certificates", "duplicate certificate")
		}
		seen[c] = true
	}
	p.HcPort = p.DestinationPort
	codes := []string{"2??", "3??"}
	if hc := in.HealthCheck; hc != nil {
		if hc.Protocol != nil {
			p.HcProtocol = string(*hc.Protocol)
		}
		if hc.Port != nil && *hc.Port != 0 {
			p.HcPort = int64(*hc.Port)
		}
		if hc.IntervalS != nil {
			p.HcIntervalS = int64(*hc.IntervalS)
		}
		if hc.TimeoutS != nil {
			p.HcTimeoutS = int64(*hc.TimeoutS)
		}
		if hc.Retries != nil {
			p.HcRetries = int64(*hc.Retries)
		}
		if h := hc.Http; h != nil && p.HcProtocol != "tcp" {
			if h.Domain != nil {
				p.HcDomain = strings.ToLower(strings.TrimSpace(*h.Domain))
			}
			if h.Path != nil {
				p.HcPath = *h.Path
			}
			if h.Response != nil {
				p.HcResponse = *h.Response
			}
			if h.StatusCodes != nil {
				codes = *h.StatusCodes
			}
			if h.TlsVerify != nil {
				p.HcTlsVerify = b2i(*h.TlsVerify)
			}
		}
	}
	if p.HcProtocol != "tcp" && p.HcProtocol != "http" && p.HcProtocol != "https" {
		fe.Add("health_check.protocol", "must be tcp, http or https")
	}
	if !model.ValidPort(int(p.HcPort)) {
		fe.Add("health_check.port", "must be 1–65535")
	}
	if p.HcIntervalS < 3 || p.HcIntervalS > 3600 {
		fe.Add("health_check.interval_s", "must be 3–3600")
	}
	if p.HcTimeoutS < 1 || p.HcTimeoutS > 3600 {
		fe.Add("health_check.timeout_s", "must be 1–3600")
	}
	if p.HcTimeoutS >= p.HcIntervalS {
		fe.Add("health_check.timeout_s", "must be lower than the interval")
	}
	if p.HcRetries < 1 || p.HcRetries > 10 {
		fe.Add("health_check.retries", "must be 1–10")
	}
	if p.HcProtocol == "tcp" {
		p.HcDomain, p.HcResponse, p.HcTlsVerify = "", "", 0
	} else {
		if !model.ValidHCPath(p.HcPath) {
			fe.Add("health_check.http.path", "must start with / and contain no spaces, quotes, #, $ or \\")
		}
		if p.HcDomain != "" && !model.ValidHostname(p.HcDomain, false) {
			fe.Add("health_check.http.domain", "invalid hostname")
		}
		if p.HcResponse != "" && !model.ValidHCResponse(p.HcResponse) {
			fe.Add("health_check.http.response", "printable ASCII without ' \" \\ # $ (1–128)")
		}
		if _, err := model.StatusCodesToHAProxy(codes); err != nil {
			fe.Add("health_check.http.status_codes", err.Error())
		}
		if p.HcTlsVerify != 0 && p.HcDomain == "" {
			fe.Add("health_check.http.domain", "required when TLS verification is on")
		}
	}
	p.HcStatusCodes = jsonStr(codes)
	// Port collisions (§8.7).
	if reservedPort(p.ListenPort, s.Cfg.PanelPort) || (p.HttpRedirect != 0 && reservedPort(80, s.Cfg.PanelPort)) {
		fe.Add("listen_port", "reserved for the panel")
	}
	used := map[int64]string{}
	for _, o := range lb.Services {
		if existing != nil && o.ID == existing.ID {
			continue
		}
		used[o.ListenPort] = "already in use"
		if o.Protocol == "https" && o.HttpRedirect != 0 {
			used[80] = "used by the HTTP→HTTPS redirect"
		}
	}
	if m, ok := used[p.ListenPort]; ok {
		fe.Add("listen_port", m)
	}
	if p.HttpRedirect != 0 {
		if _, ok := used[80]; ok {
			fe.Add("http.redirect_http", ":80 is already used on this load balancer")
		}
		if p.ListenPort == 80 {
			fe.Add("http.redirect_http", "cannot redirect a service that listens on :80")
		}
	}
	for _, o := range snap.LBs {
		if o.ID == lb.ID || !addrsOverlap(o, lb) {
			continue
		}
		for _, os := range o.Services {
			if os.ListenPort == p.ListenPort || (p.HttpRedirect != 0 && os.ListenPort == 80) || (os.HttpRedirect != 0 && p.ListenPort == 80) {
				fe.Add("listen_port", fmt.Sprintf("port collides with load balancer %q on the same address", o.Name))
			}
		}
	}
	if !fe.Empty() {
		return p, nil, errInput(fe)
	}
	// Certificate quota.
	if lb.Plan != nil {
		all := map[int64]bool{}
		for _, o := range lb.Services {
			if existing != nil && o.ID == existing.ID {
				continue
			}
			for _, c := range o.CertIDs {
				all[c] = true
			}
		}
		for _, c := range certIDs {
			all[c] = true
		}
		if int64(len(all)) > lb.Plan.MaxCertificates {
			return p, nil, errQuota(fmt.Sprintf("plan %s allows %d certificates", lb.Plan.Name, lb.Plan.MaxCertificates))
		}
	}
	return p, certIDs, nil
}

func (s *Server) CreateService(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.ServiceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if lb.Plan != nil && int64(len(lb.Services)) >= lb.Plan.MaxServices {
		fail(w, errQuota(fmt.Sprintf("plan %s allows %d services", lb.Plan.Name, lb.Plan.MaxServices)))
		return
	}
	p, certs, err := s.buildService(lb, nil, in)
	if err != nil {
		fail(w, err)
		return
	}
	p.LbID = lb.ID
	var sid int64
	err = s.mutate(r, "service.create", "service", func() int64 { return sid }, in, func(q *db.Queries) error {
		sv, err := q.CreateService(r.Context(), p)
		if err != nil {
			return err
		}
		sid = sv.ID
		for pos, c := range certs {
			if err := q.AddServiceCertificate(r.Context(), db.AddServiceCertificateParams{ServiceID: sv.ID, CertificateID: c, Position: int64(pos)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	s.respondService(w, 201, lb.ID, sid)
}

func (s *Server) respondService(w http.ResponseWriter, status int, lbID, sid int64) {
	lb := s.Snap.Get().LB(lbID)
	if lb == nil || lb.Service(sid) == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"service": viewService(lb.Service(sid))})
}

func (s *Server) UpdateService(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	sv := lb.Service(sid)
	if sv == nil {
		fail(w, errNotFound("service"))
		return
	}
	if err := checkIfMatch(r, sv.Version); err != nil {
		fail(w, err)
		return
	}
	var in gen.ServiceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p, certs, err := s.buildService(lb, sv, in)
	if err != nil {
		fail(w, err)
		return
	}
	err = s.mutate(r, "service.update", "service", func() int64 { return sid }, in, func(q *db.Queries) error {
		if err := q.UpdateService(r.Context(), db.UpdateServiceParams{Protocol: p.Protocol, ListenPort: p.ListenPort, DestinationPort: p.DestinationPort,
			HttpRedirect: p.HttpRedirect, StickyEnabled: p.StickyEnabled, StickyCookieName: p.StickyCookieName, StickyCookieLifetimeS: p.StickyCookieLifetimeS,
			ProxyProtocol: p.ProxyProtocol, HttpTimeoutS: p.HttpTimeoutS, HcProtocol: p.HcProtocol, HcPort: p.HcPort, HcIntervalS: p.HcIntervalS,
			HcTimeoutS: p.HcTimeoutS, HcRetries: p.HcRetries, HcDomain: p.HcDomain, HcPath: p.HcPath, HcResponse: p.HcResponse,
			HcStatusCodes: p.HcStatusCodes, HcTlsVerify: p.HcTlsVerify, ID: sid}); err != nil {
			return err
		}
		if err := q.ClearServiceCertificates(r.Context(), sid); err != nil {
			return err
		}
		for pos, c := range certs {
			if err := q.AddServiceCertificate(r.Context(), db.AddServiceCertificateParams{ServiceID: sid, CertificateID: c, Position: int64(pos)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	s.respondService(w, 200, lb.ID, sid)
}

func (s *Server) DeleteService(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	if lb.Service(sid) == nil {
		fail(w, errNotFound("service"))
		return
	}
	if err := s.mutate(r, "service.delete", "service", func() int64 { return sid }, nil, func(q *db.Queries) error {
		return q.DeleteService(r.Context(), sid)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- targets ----------

func (s *Server) ListTargets(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, t := range lb.Targets {
		out = append(out, s.viewTarget(lb, t))
	}
	writeJSON(w, 200, map[string]any{"targets": out})
}

// buildTargets validates target inputs and enforces the resolved-target quota (§8.6).
func (s *Server) buildTargets(lb *state.LB, ins []gen.TargetInput) ([]db.CreateTargetParams, error) {
	snap := s.Snap.Get()
	var servers []db.Server
	for _, sv := range snap.Servers {
		servers = append(servers, sv.Server)
	}
	own := map[string]bool{lb.IPv4: true, lb.IPv6: true}
	for _, n := range lb.Networks {
		own[n.IP] = true
	}
	existing := map[string]bool{}
	for _, t := range lb.Targets {
		existing[t.Type+"|"+t.Ip+"|"+fmt.Sprint(t.ServerID.Int64)+"|"+t.LabelSelector] = true
	}
	fe := model.FieldErrors{}
	var out []db.CreateTargetParams
	added := 0
	for i, in := range ins {
		f := fmt.Sprintf("targets[%d]", i)
		p := db.CreateTargetParams{Type: string(in.Type), Weight: 100, CreatedAt: nowMs()}
		if in.Weight != nil {
			p.Weight = int64(*in.Weight)
		}
		if p.Weight < 0 || p.Weight > 256 {
			fe.Add(f+".weight", "must be 0–256")
		}
		if in.Backup != nil {
			p.Backup = b2i(*in.Backup)
		}
		if in.UsePrivateIp != nil && *in.UsePrivateIp {
			if len(lb.Networks) == 0 {
				fe.Add(f+".use_private_ip", "the load balancer is not attached to a network")
			}
			p.UsePrivateIp = 1
		}
		switch p.Type {
		case "ip":
			if in.Ip == nil {
				fe.Add(f+".ip", "required")
				continue
			}
			a, err := model.ParseTargetIP(*in.Ip)
			if err != nil {
				fe.Add(f+".ip", err.Error())
				continue
			}
			if own[a.String()] {
				fe.Add(f+".ip", "cannot target the load balancer's own address")
			}
			p.Ip = a.String()
		case "server":
			if in.ServerId == nil || snap.Server(*in.ServerId) == nil {
				fe.Add(f+".server_id", "unknown server")
				continue
			}
			p.ServerID = sql.NullInt64{Int64: *in.ServerId, Valid: true}
		case "label_selector":
			if in.LabelSelector == nil {
				fe.Add(f+".label_selector", "required")
				continue
			}
			if _, err := model.ParseSelector(*in.LabelSelector); err != nil {
				fe.Add(f+".label_selector", err.Error())
				continue
			}
			p.LabelSelector = strings.TrimSpace(*in.LabelSelector)
		default:
			fe.Add(f+".type", "must be server, label_selector or ip")
			continue
		}
		k := p.Type + "|" + p.Ip + "|" + fmt.Sprint(p.ServerID.Int64) + "|" + p.LabelSelector
		if existing[k] {
			fe.Add(f, "target already exists")
		}
		existing[k] = true
		keys, _ := state.ResolveTarget(db.Target{Type: p.Type, Ip: p.Ip, ServerID: p.ServerID, LabelSelector: p.LabelSelector, UsePrivateIp: p.UsePrivateIp}, servers)
		added += len(keys)
		out = append(out, p)
	}
	if !fe.Empty() {
		return nil, errInput(fe)
	}
	if lb.Plan != nil && int64(resolvedCount(lb)+added) > lb.Plan.MaxTargets {
		return nil, errQuota(fmt.Sprintf("plan %s allows %d targets (you would have %d)", lb.Plan.Name, lb.Plan.MaxTargets, resolvedCount(lb)+added))
	}
	return out, nil
}

func (s *Server) CreateTargets(w http.ResponseWriter, r *http.Request, id gen.Id) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.TargetsCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	ps, err := s.buildTargets(lb, in.Targets)
	if err != nil {
		fail(w, err)
		return
	}
	var ids []int64
	err = s.mutate(r, "target.create", "load_balancer", func() int64 { return lb.ID }, in, func(q *db.Queries) error {
		for _, p := range ps {
			p.LbID = lb.ID
			t, err := q.CreateTarget(r.Context(), p)
			if err != nil {
				return err
			}
			ids = append(ids, t.ID)
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	nlb := s.Snap.Get().LB(lb.ID)
	out := []map[string]any{}
	for _, tid := range ids {
		if t := nlb.Target(tid); t != nil {
			out = append(out, s.viewTarget(nlb, t))
		}
	}
	writeJSON(w, 201, map[string]any{"targets": out})
}

func (s *Server) UpdateTarget(w http.ResponseWriter, r *http.Request, id gen.Id, tid int64) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	t := lb.Target(tid)
	if t == nil {
		fail(w, errNotFound("target"))
		return
	}
	var in gen.TargetUpdate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p := db.UpdateTargetParams{Weight: t.Weight, Backup: t.Backup, AdminState: t.AdminState, ID: t.ID}
	if in.Weight != nil {
		if *in.Weight < 0 || *in.Weight > 256 {
			fail(w, errField("weight", "must be 0–256"))
			return
		}
		p.Weight = int64(*in.Weight)
	}
	if in.AdminState != nil {
		p.AdminState = string(*in.AdminState)
		if p.AdminState != "ready" && p.AdminState != "drain" && p.AdminState != "maint" {
			fail(w, errField("admin_state", "must be ready, drain or maint"))
			return
		}
	}
	if in.Backup != nil {
		p.Backup = b2i(*in.Backup)
	}
	if err := s.mutate(r, "target.update", "target", func() int64 { return t.ID }, in, func(q *db.Queries) error {
		return q.UpdateTarget(r.Context(), p)
	}); err != nil {
		fail(w, err)
		return
	}
	nlb := s.Snap.Get().LB(lb.ID)
	writeJSON(w, 200, map[string]any{"target": s.viewTarget(nlb, nlb.Target(t.ID))})
}

// DeleteTarget starts a graceful removal: drain → wait for scur=0 or timeout → delete (§7.6).
func (s *Server) DeleteTarget(w http.ResponseWriter, r *http.Request, id gen.Id, tid int64) {
	lb, err := s.lb(id)
	if err != nil {
		fail(w, err)
		return
	}
	t := lb.Target(tid)
	if t == nil {
		fail(w, errNotFound("target"))
		return
	}
	if err := s.mutate(r, "target.delete", "target", func() int64 { return t.ID }, nil, func(q *db.Queries) error {
		return q.MarkTargetRemoving(r.Context(), t.ID)
	}); err != nil {
		fail(w, err)
		return
	}
	if s.Drain != nil {
		go s.Drain(lb.ID, t.ID)
	}
	w.WriteHeader(204)
}

func (s *Server) PreviewLabelSelector(w http.ResponseWriter, r *http.Request, p gen.PreviewLabelSelectorParams) {
	sel, err := model.ParseSelector(p.Selector)
	if err != nil {
		fail(w, errField("selector", err.Error()))
		return
	}
	out := []map[string]any{}
	for _, sv := range s.Snap.Get().Servers {
		if sel.Matches(sv.Labels) {
			out = append(out, viewServer(sv))
		}
	}
	writeJSON(w, 200, map[string]any{"servers": out})
}

// ---------- metrics ----------

func (s *Server) GetLoadBalancerMetrics(w http.ResponseWriter, r *http.Request, id gen.Id, p gen.GetLoadBalancerMetricsParams) {
	if _, err := s.lb(id); err != nil {
		fail(w, err)
		return
	}
	var types []string
	if p.Type != nil && *p.Type != "" {
		types = strings.Split(*p.Type, ",")
	}
	var start, end, svc int64
	var step int64
	if p.Start != nil {
		start = *p.Start
	}
	if p.End != nil {
		end = *p.End
	}
	if p.Step != nil {
		step = int64(*p.Step)
	}
	if p.ServiceId != nil {
		svc = *p.ServiceId
	}
	series, err := s.Metrics.Query(r.Context(), id, svc, start, end, step, types)
	if err != nil {
		fail(w, err)
		return
	}
	keys := make([]string, 0, len(series.Values))
	for k := range series.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeJSON(w, 200, map[string]any{"start": series.Start, "end": series.End, "step": series.Step, "timestamps": series.Timestamps, "series": series.Values})
}

func (s *Server) StreamLoadBalancerMetrics(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if _, err := s.lb(id); err != nil {
		fail(w, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.Metrics.Subscribe(id)
	defer cancel()
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case p := <-ch:
			b, _ := json.Marshal(map[string]any{"ts": p.TS, "open_connections": p.Open, "connections_per_second": p.CPS,
				"requests_per_second": p.RPS, "bandwidth.in": p.In, "bandwidth.out": p.Out})
			fmt.Fprintf(w, "event: metrics.tick\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}
