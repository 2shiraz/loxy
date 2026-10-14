package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

var algorithms = map[string]bool{"roundrobin": true, "leastconn": true, "source": true, "random": true}

func (s *Server) backend(id int64) (*state.Backend, error) {
	b := s.Snap.Get().Backend(id)
	if b == nil {
		return nil, errNotFound("backend")
	}
	return b, nil
}

func (s *Server) respondBackend(w http.ResponseWriter, status int, id int64) {
	b := s.Snap.Get().Backend(id)
	if b == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"backend": s.viewBackend(b)})
}

func (s *Server) ListBackends(w http.ResponseWriter, r *http.Request, p gen.ListBackendsParams) {
	var sel model.Selector
	if p.LabelSelector != nil && *p.LabelSelector != "" {
		var err error
		if sel, err = model.ParseSelector(*p.LabelSelector); err != nil {
			fail(w, errField("label_selector", err.Error()))
			return
		}
	}
	out := []map[string]any{}
	for _, b := range s.Snap.Get().Backends {
		if sel != nil && !sel.Matches(b.Labels) {
			continue
		}
		out = append(out, s.viewBackend(b))
	}
	writeJSON(w, 200, map[string]any{"backends": out})
}

func (s *Server) GetBackend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backend": s.viewBackend(b)})
}

// buildBackend validates a backend input (§8.6).
func (s *Server) buildBackend(existing *state.Backend, in gen.BackendInput) (db.CreateBackendParams, error) {
	fe := model.FieldErrors{}
	p := db.CreateBackendParams{Name: strings.TrimSpace(in.Name), Mode: string(in.Mode), Algorithm: "roundrobin", Port: int64(in.Port),
		StickyCookieName: "LOXYSTICKY", StickyCookieLifetimeS: 300, ProxyProtocol: "none", TimeoutServerS: 60,
		HcProtocol: "tcp", HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/", Labels: "{}", CreatedAt: nowMs()}
	if !model.ValidName(p.Name) {
		fe.Add("name", "1–64 printable characters")
	}
	for _, o := range s.Snap.Get().Backends {
		if o.Name == p.Name && (existing == nil || o.ID != existing.ID) {
			fe.Add("name", "already in use")
		}
	}
	if p.Mode != "http" && p.Mode != "tcp" {
		fe.Add("mode", "must be http or tcp")
	}
	if existing != nil && existing.Mode != p.Mode {
		for _, fid := range s.Snap.Get().UsedBy(existing.ID) {
			fe.Add("mode", fmt.Sprintf("used by frontend %d in %s mode; change the frontend first", fid, existing.Mode))
			break
		}
	}
	if in.Description != nil {
		p.Description = strings.TrimSpace(*in.Description)
		if len(p.Description) > 500 {
			fe.Add("description", "at most 500 characters")
		}
	}
	if in.Algorithm != nil {
		p.Algorithm = string(*in.Algorithm)
	}
	if !algorithms[p.Algorithm] {
		fe.Add("algorithm", "must be roundrobin, leastconn, source or random")
	}
	if !model.ValidPort(in.Port) {
		fe.Add("port", "must be 1–65535")
	}
	if in.ProxyProtocol != nil {
		p.ProxyProtocol = string(*in.ProxyProtocol)
		if p.ProxyProtocol != "none" && p.ProxyProtocol != "v1" && p.ProxyProtocol != "v2" {
			fe.Add("proxy_protocol", "must be none, v1 or v2")
		}
	}
	if in.TimeoutServerS != nil {
		p.TimeoutServerS = int64(*in.TimeoutServerS)
	}
	if p.TimeoutServerS < 1 || p.TimeoutServerS > 86400 {
		fe.Add("timeout_server_s", "must be 1–86400")
	}
	if in.StickySessions != nil {
		p.StickyEnabled = b2i(*in.StickySessions)
	}
	if in.CookieName != nil && *in.CookieName != "" {
		p.StickyCookieName = *in.CookieName
	}
	if in.CookieLifetimeS != nil {
		p.StickyCookieLifetimeS = int64(*in.CookieLifetimeS)
	}
	if p.StickyEnabled != 0 && p.Mode == "tcp" {
		fe.Add("sticky_sessions", "only available in http mode")
	}
	if !model.ValidCookieName(p.StickyCookieName) {
		fe.Add("cookie_name", "letters, digits, - and _ only (1–64)")
	}
	if p.StickyCookieLifetimeS < 1 || p.StickyCookieLifetimeS > 31536000 {
		fe.Add("cookie_lifetime_s", "must be 1–31536000")
	}
	codes := []string{"2??", "3??"}
	if p.Mode == "http" {
		p.HcProtocol = "http" // §8.6: http checks by default for http backends
	}
	if hc := in.HealthCheck; hc != nil {
		if hc.Protocol != nil {
			p.HcProtocol = string(*hc.Protocol)
		}
		if hc.Port != nil {
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
	if p.HcPort != 0 && !model.ValidPort(int(p.HcPort)) {
		fe.Add("health_check.port", "must be 1–65535")
	}
	if p.HcIntervalS < 3 || p.HcIntervalS > 3600 {
		fe.Add("health_check.interval_s", "must be 3–3600")
	}
	if p.HcTimeoutS < 1 || p.HcTimeoutS > 3600 {
		fe.Add("health_check.timeout_s", "must be 1–3600")
	}
	if p.HcTimeoutS >= p.HcIntervalS {
		fe.Add("health_check.timeout_s", "must be less than the interval")
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
	if in.Labels != nil {
		model.ValidateLabels(fe, "labels", *in.Labels)
		p.Labels = jsonStr(*in.Labels)
	}
	if !fe.Empty() {
		return p, errInput(fe)
	}
	return p, nil
}

func (s *Server) CreateBackend(w http.ResponseWriter, r *http.Request) {
	var in gen.BackendInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := s.buildBackend(nil, in)
	if err != nil {
		fail(w, err)
		return
	}
	var id int64
	if err := s.mutate(r, "backend.create", "backend", func() int64 { return id }, in, func(q *db.Queries) error {
		b, err := q.CreateBackend(r.Context(), p)
		id = b.ID
		return err
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondBackend(w, 201, id)
}

func (s *Server) UpdateBackend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := checkIfMatch(r, b.Version); err != nil {
		fail(w, err)
		return
	}
	var in gen.BackendInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := s.buildBackend(b, in)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.mutate(r, "backend.update", "backend", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateBackend(r.Context(), db.UpdateBackendParams{Name: p.Name, Description: p.Description, Mode: p.Mode, Algorithm: p.Algorithm,
			Port: p.Port, StickyEnabled: p.StickyEnabled, StickyCookieName: p.StickyCookieName, StickyCookieLifetimeS: p.StickyCookieLifetimeS,
			ProxyProtocol: p.ProxyProtocol, TimeoutServerS: p.TimeoutServerS, HcProtocol: p.HcProtocol, HcPort: p.HcPort, HcIntervalS: p.HcIntervalS,
			HcTimeoutS: p.HcTimeoutS, HcRetries: p.HcRetries, HcDomain: p.HcDomain, HcPath: p.HcPath, HcResponse: p.HcResponse,
			HcStatusCodes: p.HcStatusCodes, HcTlsVerify: p.HcTlsVerify, Labels: p.Labels, ID: id})
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondBackend(w, 200, id)
}

func (s *Server) DeleteBackend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	if used := s.Snap.Get().UsedBy(id); len(used) > 0 {
		f := s.Snap.Get().Frontend(used[0])
		fail(w, errConflict(fmt.Sprintf("backend is used by frontend %q", f.Name)))
		return
	}
	if err := s.mutate(r, "backend.delete", "backend", func() int64 { return id }, map[string]any{"name": b.Name}, func(q *db.Queries) error {
		return q.DeleteBackend(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) GetBackendStatus(w http.ResponseWriter, r *http.Request, id gen.Id) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	sum, rs := s.backendHealth(b)
	writeJSON(w, 200, map[string]any{"summary": sum, "servers": rs})
}

func (s *Server) GetBackendMetrics(w http.ResponseWriter, r *http.Request, id gen.Id, p gen.GetBackendMetricsParams) {
	if _, err := s.backend(id); err != nil {
		fail(w, err)
		return
	}
	a, b, st, types := metricsParams(p.Start, p.End, p.Step, p.Type)
	s.writeSeries(w, r, metrics.BE, id, a, b, st, types)
}

// ---------- servers ----------

func (s *Server) ListBackendServers(w http.ResponseWriter, r *http.Request, id gen.Id) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	view := s.serverViewer(b)
	for _, sv := range b.Servers {
		out = append(out, view(sv))
	}
	writeJSON(w, 200, map[string]any{"servers": out})
}

// buildServers validates server inputs (§8.6).
func (s *Server) buildServers(b *state.Backend, ins []gen.BackendServerInput) ([]db.CreateBackendServerParams, error) {
	snap := s.Snap.Get()
	existing := map[string]bool{}
	for _, sv := range b.Servers {
		existing[fmt.Sprintf("%s|%s|%d|%s|%d", sv.Type, sv.Ip, sv.HostID.Int64, sv.LabelSelector, sv.PortOr(0))] = true
	}
	fe := model.FieldErrors{}
	var out []db.CreateBackendServerParams
	if len(ins) == 0 || len(ins) > 500 {
		fe.Add("servers", "1 to 500 servers")
	}
	for i, in := range ins {
		f := fmt.Sprintf("servers[%d]", i)
		p := db.CreateBackendServerParams{BackendID: b.ID, Type: string(in.Type), Weight: 100, CreatedAt: nowMs()}
		if in.Weight != nil {
			p.Weight = int64(*in.Weight)
		}
		if p.Weight < 0 || p.Weight > 256 {
			fe.Add(f+".weight", "must be 0–256")
		}
		if in.Backup != nil {
			p.Backup = b2i(*in.Backup)
		}
		if in.UsePrivateIp != nil {
			p.UsePrivateIp = b2i(*in.UsePrivateIp)
		}
		if in.Port != nil && *in.Port != 0 {
			if !model.ValidPort(*in.Port) {
				fe.Add(f+".port", "must be 1–65535")
			}
			p.Port = sql.NullInt64{Int64: int64(*in.Port), Valid: true}
		}
		switch p.Type {
		case "ip":
			if in.Ip == nil {
				fe.Add(f+".ip", "required")
				continue
			}
			a, err := model.ParseTargetIP(*in.Ip)
			if err != nil {
				// Dev mode (make dev) allows loopback servers such as local test containers.
				if la, perr := netip.ParseAddr(strings.TrimSpace(*in.Ip)); s.Cfg.Dev && perr == nil && la.IsLoopback() {
					a = la
				} else {
					fe.Add(f+".ip", err.Error())
					continue
				}
			}
			p.Ip = a.String()
		case "host":
			if in.HostId == nil || snap.Host(*in.HostId) == nil {
				fe.Add(f+".host_id", "unknown host")
				continue
			}
			p.HostID = sql.NullInt64{Int64: *in.HostId, Valid: true}
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
			fe.Add(f+".type", "must be ip, host or label_selector")
			continue
		}
		k := fmt.Sprintf("%s|%s|%d|%s|%d", p.Type, p.Ip, p.HostID.Int64, p.LabelSelector, p.Port.Int64)
		if existing[k] {
			fe.Add(f, "server already exists")
		}
		existing[k] = true
		out = append(out, p)
	}
	if !fe.Empty() {
		return nil, errInput(fe)
	}
	return out, nil
}

func (s *Server) CreateBackendServers(w http.ResponseWriter, r *http.Request, id gen.Id) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.BackendServersCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	ps, err := s.buildServers(b, in.Servers)
	if err != nil {
		fail(w, err)
		return
	}
	var ids []int64
	if err := s.mutate(r, "server.create", "backend", func() int64 { return id }, in, func(q *db.Queries) error {
		for _, p := range ps {
			sv, err := q.CreateBackendServer(r.Context(), p)
			if err != nil {
				return err
			}
			ids = append(ids, sv.ID)
		}
		return nil
	}); err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	if nb := s.Snap.Get().Backend(id); nb != nil {
		view := s.serverViewer(nb)
		for _, sid := range ids {
			if sv := nb.Server(sid); sv != nil {
				out = append(out, view(sv))
			}
		}
	}
	writeJSON(w, 201, map[string]any{"servers": out})
}

func (s *Server) UpdateBackendServer(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	sv := b.Server(sid)
	if sv == nil {
		fail(w, errNotFound("server"))
		return
	}
	var in gen.BackendServerUpdate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p := db.UpdateBackendServerParams{Weight: sv.Weight, Backup: sv.Backup, AdminState: sv.AdminState, ID: sv.ID}
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
	if err := s.mutate(r, "server.update", "server", func() int64 { return sid }, in, func(q *db.Queries) error {
		return q.UpdateBackendServer(r.Context(), p)
	}); err != nil {
		fail(w, err)
		return
	}
	nb := s.Snap.Get().Backend(id)
	if nb == nil || nb.Server(sid) == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 200, map[string]any{"server": s.serverViewer(nb)(nb.Server(sid))})
}

// DeleteBackendServer starts a graceful removal: drain → wait for sessions to end or timeout → delete (§7.6).
func (s *Server) DeleteBackendServer(w http.ResponseWriter, r *http.Request, id gen.Id, sid int64) {
	b, err := s.backend(id)
	if err != nil {
		fail(w, err)
		return
	}
	if b.Server(sid) == nil {
		fail(w, errNotFound("server"))
		return
	}
	if err := s.mutate(r, "server.delete", "server", func() int64 { return sid }, nil, func(q *db.Queries) error {
		return q.MarkBackendServerRemoving(r.Context(), sid)
	}); err != nil {
		fail(w, err)
		return
	}
	if s.Drain != nil {
		go s.Drain(id, sid)
	}
	w.WriteHeader(204)
}
