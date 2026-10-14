package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// Automatic listen ports for projects without an explicit port ("each project has its own port").
const autoPortFirst, autoPortLast = 20000, 29999

// servicePlan is a validated ServiceInput; ListenPort 0 means "pick a free port in the transaction".
type servicePlan struct {
	p     db.CreateServiceParams
	certs []int64
}

// reservedPort explains why a listen port cannot be used, or returns "".
func (s *Server) reservedPort(port int) string {
	switch port {
	case 80, 443:
		return "reserved for domains (shared :80/:443)"
	case s.Cfg.PanelPort:
		return "reserved for the Loxy panel"
	case 8405, 5514:
		return "reserved for Loxy internals"
	}
	if _, p, err := net.SplitHostPort(s.Cfg.Listen); err == nil && strconv.Itoa(port) == p {
		return "used by the Loxy API"
	}
	return ""
}

// buildService validates a service input (§8.5). existing is nil on create.
func (s *Server) buildService(p *state.Project, existing *state.Service, in gen.ServiceInput, field string) (*servicePlan, model.FieldErrors) {
	fe := model.FieldErrors{}
	f := func(name string) string {
		if field == "" {
			return name
		}
		return field + "." + name
	}
	sp := &servicePlan{p: db.CreateServiceParams{BalancerID: p.ID, Protocol: string(in.Protocol), DestinationPort: int64(in.DestinationPort),
		ProxyProtocol: "none", TimeoutS: 60, StickyCookieName: "LOXYSTICKY", StickyCookieLifetimeS: 300,
		HcProtocol: "tcp", HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/", CreatedAt: nowMs()}}
	c := &sp.p
	if c.Protocol != "http" && c.Protocol != "https" && c.Protocol != "tcp" {
		fe.Add(f("protocol"), "must be http, https or tcp")
	}
	if c.Protocol != "tcp" {
		c.HcProtocol = "http" // http checks by default for http services
	}
	if !model.ValidPort(in.DestinationPort) {
		fe.Add(f("destination_port"), "must be 1–65535")
	}
	if in.ListenPort != nil && *in.ListenPort != 0 {
		lp := *in.ListenPort
		c.ListenPort = int64(lp)
		if !model.ValidPort(lp) {
			fe.Add(f("listen_port"), "must be 1–65535")
		} else if why := s.reservedPort(lp); why != "" {
			fe.Add(f("listen_port"), why)
		} else {
			for _, o := range s.Snap.Get().Projects {
				for _, os := range o.Services {
					if os.ListenPort == c.ListenPort && (existing == nil || os.ID != existing.ID) {
						fe.Add(f("listen_port"), fmt.Sprintf("already used by project %q", o.Name))
					}
				}
			}
		}
	} else if existing != nil {
		c.ListenPort = existing.ListenPort // keep the port on update when none is given
	}
	if in.ProxyProtocol != nil {
		c.ProxyProtocol = string(*in.ProxyProtocol)
		if c.ProxyProtocol != "none" && c.ProxyProtocol != "v1" && c.ProxyProtocol != "v2" {
			fe.Add(f("proxy_protocol"), "must be none, v1 or v2")
		}
	}
	if in.TimeoutS != nil {
		c.TimeoutS = int64(*in.TimeoutS)
	}
	if c.TimeoutS < 1 || c.TimeoutS > 86400 {
		fe.Add(f("timeout_s"), "must be 1–86400")
	}
	if in.StickySessions != nil {
		c.StickyEnabled = b2i(*in.StickySessions)
	}
	if in.CookieName != nil && *in.CookieName != "" {
		c.StickyCookieName = *in.CookieName
	}
	if in.CookieLifetimeS != nil {
		c.StickyCookieLifetimeS = int64(*in.CookieLifetimeS)
	}
	if c.StickyEnabled != 0 && c.Protocol == "tcp" {
		fe.Add(f("sticky_sessions"), "only for http and https services")
	}
	if !model.ValidCookieName(c.StickyCookieName) {
		fe.Add(f("cookie_name"), "letters, digits, - and _ only (1–64)")
	}
	if c.StickyCookieLifetimeS < 1 || c.StickyCookieLifetimeS > 31536000 {
		fe.Add(f("cookie_lifetime_s"), "must be 1–31536000")
	}
	codes := []string{"2??", "3??"}
	if hc := in.HealthCheck; hc != nil {
		if hc.Protocol != nil {
			c.HcProtocol = string(*hc.Protocol)
		}
		if hc.Port != nil {
			c.HcPort = int64(*hc.Port)
		}
		if hc.IntervalS != nil {
			c.HcIntervalS = int64(*hc.IntervalS)
		}
		if hc.TimeoutS != nil {
			c.HcTimeoutS = int64(*hc.TimeoutS)
		}
		if hc.Retries != nil {
			c.HcRetries = int64(*hc.Retries)
		}
		if h := hc.Http; h != nil && c.HcProtocol != "tcp" {
			if h.Domain != nil {
				c.HcDomain = strings.ToLower(strings.TrimSpace(*h.Domain))
			}
			if h.Path != nil {
				c.HcPath = *h.Path
			}
			if h.Response != nil {
				c.HcResponse = *h.Response
			}
			if h.StatusCodes != nil {
				codes = *h.StatusCodes
			}
			if h.TlsVerify != nil {
				c.HcTlsVerify = b2i(*h.TlsVerify)
			}
		}
	}
	hf := func(n string) string { return f("health_check." + n) }
	if c.HcProtocol != "tcp" && c.HcProtocol != "http" && c.HcProtocol != "https" {
		fe.Add(hf("protocol"), "must be tcp, http or https")
	}
	if c.HcPort != 0 && !model.ValidPort(int(c.HcPort)) {
		fe.Add(hf("port"), "must be 1–65535")
	}
	if c.HcIntervalS < 3 || c.HcIntervalS > 3600 {
		fe.Add(hf("interval_s"), "must be 3–3600")
	}
	if c.HcTimeoutS < 1 || c.HcTimeoutS > 3600 {
		fe.Add(hf("timeout_s"), "must be 1–3600")
	} else if c.HcTimeoutS >= c.HcIntervalS {
		fe.Add(hf("timeout_s"), "must be less than the interval")
	}
	if c.HcRetries < 1 || c.HcRetries > 10 {
		fe.Add(hf("retries"), "must be 1–10")
	}
	if c.HcProtocol == "tcp" {
		c.HcDomain, c.HcResponse, c.HcTlsVerify = "", "", 0
	} else {
		if !model.ValidHCPath(c.HcPath) {
			fe.Add(hf("http.path"), "must start with / and contain no spaces, quotes, #, $ or \\")
		}
		if c.HcDomain != "" && !model.ValidHostname(c.HcDomain, false) {
			fe.Add(hf("http.domain"), "invalid hostname")
		}
		if c.HcResponse != "" && !model.ValidHCResponse(c.HcResponse) {
			fe.Add(hf("http.response"), "printable ASCII without ' \" \\ # $ (1–128)")
		}
		if _, err := model.StatusCodesToHAProxy(codes); err != nil {
			fe.Add(hf("http.status_codes"), err.Error())
		}
		if c.HcTlsVerify != 0 && c.HcDomain == "" {
			fe.Add(hf("http.domain"), "required when TLS verification is on")
		}
	}
	c.HcStatusCodes = jsonStr(codes)
	if c.Protocol == "https" {
		snap := s.Snap.Get()
		if in.Certificates != nil {
			for _, id := range *in.Certificates {
				if snap.Cert(id) == nil {
					fe.Add(f("certificates"), fmt.Sprintf("unknown certificate %d", id))
				} else if slices.Contains(sp.certs, id) {
					fe.Add(f("certificates"), fmt.Sprintf("certificate %d listed twice", id))
				} else {
					sp.certs = append(sp.certs, id)
				}
			}
		}
		if len(sp.certs) == 0 {
			fe.Add(f("certificates"), "https needs at least one certificate (a pending managed certificate is fine)")
		}
	}
	return sp, fe
}

// domainsNeed reports whether removing web service sid (deleting it or turning it into tcp)
// would leave the project's domains without an http/https service.
func (s *Server) domainsNeed(p *state.Project, sid int64) bool {
	if len(s.Snap.Get().DomainsOf(p.ID)) == 0 {
		return false
	}
	for _, o := range p.Services {
		if o.ID != sid && o.Protocol != "tcp" {
			return false
		}
	}
	return true
}

// insertService writes a planned service, picking a free listen port when none was given.
// Runs inside the write transaction (callers hold listenMu).
func (s *Server) insertService(ctx context.Context, q *db.Queries, sp *servicePlan) (db.Service, error) {
	if sp.p.ListenPort == 0 {
		port, err := s.freePort(ctx, q)
		if err != nil {
			return db.Service{}, err
		}
		sp.p.ListenPort = port
	}
	sv, err := q.CreateService(ctx, sp.p)
	if err != nil {
		return sv, err
	}
	for i, c := range sp.certs {
		if err := q.AddServiceCertificate(ctx, db.AddServiceCertificateParams{ServiceID: sv.ID, CertificateID: c, Position: int64(i)}); err != nil {
			return sv, err
		}
	}
	return sv, nil
}

func (s *Server) freePort(ctx context.Context, q *db.Queries) (int64, error) {
	used, err := q.ListListenPorts(ctx)
	if err != nil {
		return 0, err
	}
	taken := map[int64]bool{}
	for _, p := range used {
		taken[p] = true
	}
	for p := int64(autoPortFirst); p <= autoPortLast; p++ {
		if !taken[p] && s.reservedPort(int(p)) == "" {
			return p, nil
		}
	}
	return 0, errField("listen_port", "no free port left in 20000–29999; set one explicitly")
}

func (s *Server) ListServices(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, sv := range p.Services {
		out = append(out, s.viewService(p, sv))
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) respondService(w http.ResponseWriter, status int, projectID, sid int64) {
	p := s.Snap.Get().Project(projectID)
	if p == nil || p.Service(sid) == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"service": s.viewService(p, p.Service(sid))})
}

func (s *Server) CreateService(w http.ResponseWriter, r *http.Request, id gen.Id) {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.ServiceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if len(p.Services) >= 20 {
		fail(w, errField("protocol", "at most 20 services per project"))
		return
	}
	sp, fe := s.buildService(p, nil, in, "")
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	var sid int64
	if err := s.mutate(r, "service.create", "project", func() int64 { return id }, in, func(q *db.Queries) error {
		sv, err := s.insertService(r.Context(), q, sp)
		sid = sv.ID
		return err
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondService(w, 201, id, sid)
}

func (s *Server) UpdateService(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	old := p.Service(sid)
	if old == nil {
		fail(w, errNotFound("service"))
		return
	}
	if err := checkIfMatch(r, old.Version); err != nil {
		fail(w, err)
		return
	}
	var in gen.ServiceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	sp, fe := s.buildService(p, old, in, "")
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	if sp.p.Protocol == "tcp" && old.Protocol != "tcp" && s.domainsNeed(p, sid) {
		fail(w, errConflict("the project's domains need an http or https service; remove the domains or add another service first"))
		return
	}
	c := sp.p
	if err := s.mutate(r, "service.update", "project", func() int64 { return id }, in, func(q *db.Queries) error {
		if err := q.UpdateService(r.Context(), db.UpdateServiceParams{Protocol: c.Protocol, ListenPort: c.ListenPort, DestinationPort: c.DestinationPort,
			ProxyProtocol: c.ProxyProtocol, TimeoutS: c.TimeoutS, StickyEnabled: c.StickyEnabled, StickyCookieName: c.StickyCookieName,
			StickyCookieLifetimeS: c.StickyCookieLifetimeS, HcProtocol: c.HcProtocol, HcPort: c.HcPort, HcIntervalS: c.HcIntervalS,
			HcTimeoutS: c.HcTimeoutS, HcRetries: c.HcRetries, HcDomain: c.HcDomain, HcPath: c.HcPath, HcResponse: c.HcResponse,
			HcStatusCodes: c.HcStatusCodes, HcTlsVerify: c.HcTlsVerify, ID: sid}); err != nil {
			return err
		}
		if err := q.ClearServiceCertificates(r.Context(), sid); err != nil {
			return err
		}
		for i, cid := range sp.certs {
			if err := q.AddServiceCertificate(r.Context(), db.AddServiceCertificateParams{ServiceID: sid, CertificateID: cid, Position: int64(i)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondService(w, 200, id, sid)
}

func (s *Server) DeleteService(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	sv := p.Service(sid)
	if sv == nil {
		fail(w, errNotFound("service"))
		return
	}
	if sv.Protocol != "tcp" && s.domainsNeed(p, sid) {
		fail(w, errConflict("the project's domains need an http or https service; remove the domains or add another service first"))
		return
	}
	if err := s.mutate(r, "service.delete", "project", func() int64 { return id }, map[string]any{"service_id": sid, "listen_port": sv.ListenPort},
		func(q *db.Queries) error { return q.DeleteService(r.Context(), sid) }); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}
