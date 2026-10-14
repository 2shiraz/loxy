package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/reconcile"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

func (s *Server) frontend(id int64) (*state.Frontend, error) {
	f := s.Snap.Get().Frontend(id)
	if f == nil {
		return nil, errNotFound("frontend")
	}
	return f, nil
}

func (s *Server) respondFrontend(w http.ResponseWriter, status int, id int64) {
	f := s.Snap.Get().Frontend(id)
	if f == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"frontend": s.viewFrontend(f)})
}

func (s *Server) ListFrontends(w http.ResponseWriter, r *http.Request, p gen.ListFrontendsParams) {
	var sel model.Selector
	if p.LabelSelector != nil && *p.LabelSelector != "" {
		var err error
		if sel, err = model.ParseSelector(*p.LabelSelector); err != nil {
			fail(w, errField("label_selector", err.Error()))
			return
		}
	}
	out := []map[string]any{}
	for _, f := range s.Snap.Get().Frontends {
		if sel != nil && !sel.Matches(f.Labels) {
			continue
		}
		out = append(out, s.viewFrontend(f))
	}
	writeJSON(w, 200, map[string]any{"frontends": out})
}

func (s *Server) GetFrontend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	f, err := s.frontend(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"frontend": s.viewFrontend(f)})
}

// frontendPlan is a validated FrontendInput ready to be written.
type frontendPlan struct {
	p       db.CreateFrontendParams
	certs   []int64
	routes  []db.AddRouteParams
	binds   []state.Bind
	enabled bool
}

func isV6(a string) bool {
	x, err := netip.ParseAddr(a)
	return err == nil && x.Is6()
}

// listeners returns "addr:port" strings a frontend occupies (explicit binds plus implicit :80).
func listeners(f *state.Frontend, acme bool) []state.Bind {
	if f.Enabled == 0 {
		return nil
	}
	out := append([]state.Bind(nil), f.Binds...)
	for _, a := range render.Ports80(f, acme) {
		out = append(out, state.Bind{Address: a, Port: 80})
	}
	return out
}

// needsACME reports whether frontend id (0 = new) answers HTTP-01 challenges on :80 with
// the given certificates: it has a domain or a managed http01 certificate (cf. reconcile.ACMEFrontends).
func needsACME(snap *state.Snapshot, id int64, certs []int64) bool {
	for _, d := range snap.Domains {
		if id != 0 && d.FrontendID.Valid && d.FrontendID.Int64 == id {
			return true
		}
	}
	for _, cid := range certs {
		if c := snap.Cert(cid); c != nil && c.Type == "managed" && c.Challenge == "http01" {
			return true
		}
	}
	return false
}

// listenerCollision describes the first overlap between a frontend's listeners (explicit and
// implicit :80) and those of another enabled frontend, or returns ""; probe.ID is skipped
// (0 for a new frontend).
func listenerCollision(snap *state.Snapshot, probe *state.Frontend, acme bool) string {
	others := reconcile.ACMEFrontends(snap)
	mine := listeners(probe, acme)
	for _, o := range snap.Frontends {
		if o.ID == probe.ID {
			continue
		}
		for _, ob := range listeners(o, others[o.ID]) {
			for _, mb := range mine {
				if bindsCollide(ob, mb) {
					return fmt.Sprintf("%s:%d collides with frontend %q (%s:%d)", mb.Address, mb.Port, o.Name, ob.Address, ob.Port)
				}
			}
		}
	}
	return ""
}

func bindsCollide(a, b state.Bind) bool {
	if a.Port != b.Port {
		return false
	}
	wa, wb := a.Address == "*" || a.Address == "", b.Address == "*" || b.Address == ""
	return wa || wb || a.Address == b.Address
}

// buildFrontend validates a frontend input (§8.3).
func (s *Server) buildFrontend(existing *state.Frontend, in gen.FrontendInput) (*frontendPlan, error) {
	snap := s.Snap.Get()
	fe := model.FieldErrors{}
	name := strings.TrimSpace(in.Name)
	if !model.ValidName(name) {
		fe.Add("name", "1–64 printable characters")
	}
	for _, o := range snap.Frontends {
		if o.Name == name && (existing == nil || o.ID != existing.ID) {
			fe.Add("name", "already in use")
		}
	}
	mode := string(in.Mode)
	if mode != "http" && mode != "tcp" {
		fe.Add("mode", "must be http or tcp")
	}
	b := func(p *bool, def bool) bool {
		if p == nil {
			return def
		}
		return *p
	}
	plan := &frontendPlan{enabled: b(in.Enabled, true)}
	tls := b(in.Tls, false)
	redirect := b(in.HttpRedirect, false)
	if mode == "tcp" {
		if tls {
			fe.Add("tls", "TLS termination needs http mode (use tcp mode for TLS passthrough)")
		}
		redirect = false
	}
	if redirect && !tls {
		fe.Add("http_redirect", "only available with TLS")
	}
	// binds
	if len(in.Binds) == 0 || len(in.Binds) > 16 {
		fe.Add("binds", "1 to 16 listen addresses")
	}
	seen := map[string]bool{}
	for i, bd := range in.Binds {
		f := fmt.Sprintf("binds[%d]", i)
		addr := strings.TrimSpace(bd.Address)
		if addr == "" {
			addr = "*"
		}
		if addr != "*" {
			a, err := netip.ParseAddr(addr)
			if err != nil || a.Zone() != "" || a.IsMulticast() || a.IsUnspecified() {
				fe.Add(f+".address", `an IP address or "*" for all interfaces`)
				continue
			}
			addr = a.Unmap().String()
		}
		if !model.ValidPort(bd.Port) {
			fe.Add(f+".port", "must be 1–65535")
			continue
		}
		if bd.Port == s.Cfg.PanelPort {
			fe.Add(f+".port", "reserved for the Loxy panel")
		}
		if (bd.Port == 8405 || bd.Port == 5514) && (addr == "*" || strings.HasPrefix(addr, "127.")) {
			fe.Add(f+".port", "reserved for Loxy internals on loopback")
		}
		if redirect && bd.Port == 80 {
			fe.Add(f+".port", ":80 is used by the HTTP→HTTPS redirect")
		}
		k := fmt.Sprintf("%s|%d", addr, bd.Port)
		if seen[k] {
			fe.Add(f, "duplicate listen address")
		}
		seen[k] = true
		plan.binds = append(plan.binds, state.Bind{Address: addr, Port: int64(bd.Port)})
	}
	// certificates
	if in.Certificates != nil && tls {
		for _, c := range *in.Certificates {
			if snap.Cert(c) == nil {
				fe.Add("certificates", fmt.Sprintf("unknown certificate %d", c))
			}
			if slices.Contains(plan.certs, c) {
				fe.Add("certificates", fmt.Sprintf("certificate %d listed twice", c))
				continue
			}
			plan.certs = append(plan.certs, c)
		}
	}
	if tls && len(plan.certs) == 0 {
		fe.Add("certificates", "TLS needs at least one certificate (a pending managed certificate is fine)")
	}
	// backends
	var def sql.NullInt64
	if in.DefaultBackendId != nil && *in.DefaultBackendId != 0 {
		be := snap.Backend(*in.DefaultBackendId)
		switch {
		case be == nil:
			fe.Add("default_backend_id", "unknown backend")
		case be.Mode != mode:
			fe.Add("default_backend_id", fmt.Sprintf("backend %q is in %s mode", be.Name, be.Mode))
		}
		def = sql.NullInt64{Int64: *in.DefaultBackendId, Valid: true}
	}
	if in.Routes != nil {
		if mode != "http" && len(*in.Routes) > 0 {
			fe.Add("routes", "routing rules need http mode")
		}
		if len(*in.Routes) > 100 {
			fe.Add("routes", "at most 100 rules")
		}
		for i, rt := range *in.Routes {
			f := fmt.Sprintf("routes[%d]", i)
			be := snap.Backend(rt.BackendId)
			if be == nil {
				fe.Add(f+".backend_id", "unknown backend")
			} else if be.Mode != "http" {
				fe.Add(f+".backend_id", fmt.Sprintf("backend %q is in tcp mode", be.Name))
			}
			var hosts []string
			if rt.Hosts != nil {
				for _, h := range *rt.Hosts {
					h = strings.ToLower(strings.TrimSpace(h))
					if h == "" {
						continue
					}
					if !model.ValidHostname(h, true) || !strings.Contains(h, ".") {
						fe.Add(f+".hosts", fmt.Sprintf("invalid host %q", h))
					}
					hosts = append(hosts, h)
				}
			}
			if len(hosts) > 20 {
				fe.Add(f+".hosts", "at most 20 hosts per rule")
			}
			prefix := ""
			if rt.PathPrefix != nil {
				prefix = strings.TrimSpace(*rt.PathPrefix)
			}
			if prefix != "" && !model.ValidRoutePath(prefix) {
				fe.Add(f+".path_prefix", "must start with / and contain no spaces, quotes, ?, #, $ or \\")
			}
			if len(hosts) == 0 && prefix == "" {
				fe.Add(f, "needs hosts, a path prefix, or both")
			}
			if hosts == nil {
				hosts = []string{}
			}
			plan.routes = append(plan.routes, db.AddRouteParams{Position: int64(i), Hosts: jsonStr(hosts), PathPrefix: prefix, BackendID: rt.BackendId})
		}
	}
	timeout := int64(60)
	if in.TimeoutClientS != nil {
		timeout = int64(*in.TimeoutClientS)
	}
	if timeout < 1 || timeout > 86400 {
		fe.Add("timeout_client_s", "must be 1–86400")
	}
	maxconn := int64(0)
	if in.Maxconn != nil {
		maxconn = int64(*in.Maxconn)
	}
	if maxconn < 0 || maxconn > 1_000_000 {
		fe.Add("maxconn", "must be 0–1000000")
	}
	labels := map[string]string{}
	if in.Labels != nil {
		labels = *in.Labels
	}
	model.ValidateLabels(fe, "labels", labels)
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
		if len(desc) > 500 {
			fe.Add("description", "at most 500 characters")
		}
	}
	if !fe.Empty() {
		return nil, errInput(fe)
	}
	// Listener collisions with other frontends (incl. implicit :80 redirect/ACME listeners).
	probe := &state.Frontend{Frontend: db.Frontend{Mode: mode, Tls: b2i(tls), HttpRedirect: b2i(redirect), Enabled: b2i(plan.enabled)}, Binds: plan.binds}
	var selfID int64
	if existing != nil {
		selfID = existing.ID
	}
	probe.ID = selfID
	if msg := listenerCollision(snap, probe, needsACME(snap, selfID, plan.certs)); msg != "" {
		return nil, errField("binds", msg)
	}
	bj, _ := json.Marshal(plan.binds)
	plan.p = db.CreateFrontendParams{Name: name, Description: desc, Mode: mode, Tls: b2i(tls), Binds: string(bj), HttpRedirect: b2i(redirect),
		DefaultBackendID: def, TimeoutClientS: timeout, Maxconn: maxconn, ForwardedHeaders: b2i(b(in.ForwardedHeaders, true)),
		Enabled: b2i(plan.enabled), Labels: jsonStr(labels), CreatedAt: nowMs(), UpdatedAt: nowMs()}
	return plan, nil
}

func writeFrontendChildren(r *http.Request, q *db.Queries, id int64, plan *frontendPlan) error {
	if err := q.ClearRoutes(r.Context(), id); err != nil {
		return err
	}
	for _, rt := range plan.routes {
		rt.FrontendID = id
		if err := q.AddRoute(r.Context(), rt); err != nil {
			return err
		}
	}
	if err := q.ClearFrontendCertificates(r.Context(), id); err != nil {
		return err
	}
	for pos, c := range plan.certs {
		if err := q.AddFrontendCertificate(r.Context(), db.AddFrontendCertificateParams{FrontendID: id, CertificateID: c, Position: int64(pos)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) CreateFrontend(w http.ResponseWriter, r *http.Request) {
	var in gen.FrontendInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	plan, err := s.buildFrontend(nil, in)
	if err != nil {
		fail(w, err)
		return
	}
	var id int64
	if err := s.mutate(r, "frontend.create", "frontend", func() int64 { return id }, in, func(q *db.Queries) error {
		f, err := q.CreateFrontend(r.Context(), plan.p)
		if err != nil {
			return err
		}
		id = f.ID
		return writeFrontendChildren(r, q, id, plan)
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondFrontend(w, 201, id)
}

func (s *Server) UpdateFrontend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	f, err := s.frontend(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := checkIfMatch(r, f.Version); err != nil {
		fail(w, err)
		return
	}
	var in gen.FrontendInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	plan, err := s.buildFrontend(f, in)
	if err != nil {
		fail(w, err)
		return
	}
	p := plan.p
	if err := s.mutate(r, "frontend.update", "frontend", func() int64 { return id }, in, func(q *db.Queries) error {
		if err := q.UpdateFrontend(r.Context(), db.UpdateFrontendParams{Name: p.Name, Description: p.Description, Mode: p.Mode, Tls: p.Tls,
			Binds: p.Binds, HttpRedirect: p.HttpRedirect, DefaultBackendID: p.DefaultBackendID, TimeoutClientS: p.TimeoutClientS,
			Maxconn: p.Maxconn, ForwardedHeaders: p.ForwardedHeaders, Enabled: p.Enabled, Maintenance: f.Maintenance, Labels: p.Labels,
			UpdatedAt: nowMs(), ID: id}); err != nil {
			return err
		}
		return writeFrontendChildren(r, q, id, plan)
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondFrontend(w, 200, id)
}

func (s *Server) PatchFrontend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	f, err := s.frontend(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.FrontendPatch
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p := db.UpdateFrontendParams{Name: f.Name, Description: f.Description, Mode: f.Mode, Tls: f.Tls, Binds: f.Frontend.Binds, HttpRedirect: f.HttpRedirect,
		DefaultBackendID: f.DefaultBackendID, TimeoutClientS: f.TimeoutClientS, Maxconn: f.Maxconn, ForwardedHeaders: f.ForwardedHeaders,
		Enabled: f.Enabled, Maintenance: f.Maintenance, Labels: jsonStr(f.Labels), UpdatedAt: nowMs(), ID: id}
	fe := model.FieldErrors{}
	if in.Name != nil {
		if !model.ValidName(*in.Name) {
			fe.Add("name", "1–64 printable characters")
		}
		for _, o := range s.Snap.Get().Frontends {
			if o.ID != id && o.Name == *in.Name {
				fe.Add("name", "already in use")
			}
		}
		p.Name = *in.Name
	}
	if in.Description != nil {
		p.Description = strings.TrimSpace(*in.Description)
		if len(p.Description) > 500 {
			fe.Add("description", "at most 500 characters")
		}
	}
	if in.Labels != nil {
		model.ValidateLabels(fe, "labels", *in.Labels)
		p.Labels = jsonStr(*in.Labels)
	}
	if in.Maintenance != nil {
		p.Maintenance = b2i(*in.Maintenance)
	}
	if in.Enabled != nil {
		p.Enabled = b2i(*in.Enabled)
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	if p.Enabled != 0 && f.Enabled == 0 {
		// Enabling brings the frontend's listeners back: they must not collide.
		probe := *f
		probe.Enabled = 1
		snap := s.Snap.Get()
		if msg := listenerCollision(snap, &probe, needsACME(snap, f.ID, f.CertIDs)); msg != "" {
			fail(w, errField("enabled", msg))
			return
		}
	}
	if err := s.mutate(r, "frontend.patch", "frontend", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateFrontend(r.Context(), p)
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondFrontend(w, 200, id)
}

func (s *Server) DeleteFrontend(w http.ResponseWriter, r *http.Request, id gen.Id) {
	f, err := s.frontend(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.mutate(r, "frontend.delete", "frontend", func() int64 { return id }, map[string]any{"name": f.Name}, func(q *db.Queries) error {
		return q.DeleteFrontend(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) GetFrontendConfig(w http.ResponseWriter, r *http.Request, id gen.Id) {
	f, err := s.frontend(id)
	if err != nil {
		fail(w, err)
		return
	}
	out, err := s.Rec.RenderLive(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(render.FrontendSections(out.Config(), f)))
}

func (s *Server) RetryApply(w http.ResponseWriter, r *http.Request) {
	s.Rec.Trigger()
	writeJSON(w, 202, map[string]any{"action": map[string]any{"command": "retry_apply", "status": "running"}})
}

func metricsParams(start, end *int64, step *int, typ *string) (int64, int64, int64, []string) {
	var a, b, st int64
	if start != nil {
		a = *start
	}
	if end != nil {
		b = *end
	}
	if step != nil {
		st = int64(*step)
	}
	var types []string
	if typ != nil && *typ != "" {
		types = strings.Split(*typ, ",")
	}
	return a, b, st, types
}

func (s *Server) writeSeries(w http.ResponseWriter, r *http.Request, scope metrics.Scope, id, start, end, step int64, types []string) {
	series, err := s.Metrics.Query(r.Context(), scope, id, start, end, step, types)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"start": series.Start, "end": series.End, "step": series.Step, "timestamps": series.Timestamps, "series": series.Values})
}

func (s *Server) GetFrontendMetrics(w http.ResponseWriter, r *http.Request, id gen.Id, p gen.GetFrontendMetricsParams) {
	if _, err := s.frontend(id); err != nil {
		fail(w, err)
		return
	}
	a, b, st, types := metricsParams(p.Start, p.End, p.Step, p.Type)
	s.writeSeries(w, r, metrics.FE, id, a, b, st, types)
}

func (s *Server) StreamFrontendMetrics(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if _, err := s.frontend(id); err != nil {
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

// GetOverview is the dashboard summary.
func (s *Server) GetOverview(w http.ResponseWriter, r *http.Request) {
	snap := s.Snap.Get()
	fes := map[string]int{"total": len(snap.Frontends), "apply_errors": 0}
	for _, f := range snap.Frontends {
		if f.Status == "apply_error" {
			fes["apply_errors"]++
		}
	}
	bes := map[string]int{"total": len(snap.Backends), "healthy": 0, "degraded": 0, "unhealthy": 0, "unknown": 0}
	srv := map[string]int{"total": 0, "healthy": 0}
	for _, b := range snap.Backends {
		sum, rs := s.backendHealth(b)
		bes[sum.Status]++
		srv["total"] += len(rs)
		srv["healthy"] += sum.Healthy
	}
	certs := map[string]int{"total": len(snap.Certs), "expiring_soon": 0, "failed": 0}
	soon := nowMs() + 21*24*3600*1000
	for _, c := range snap.Certs {
		if c.Status == "failed" {
			certs["failed"]++
		}
		if c.NotAfter.Valid && c.NotAfter.Int64 < soon {
			certs["expiring_soon"]++
		}
	}
	var t metrics.Point
	if s.Metrics != nil {
		t = s.Metrics.Latest(metrics.FE, 0)
	}
	var node any
	if len(snap.Nodes) > 0 {
		node = s.viewNode(snap.Nodes[0])
	}
	writeJSON(w, 200, map[string]any{"frontends": fes, "backends": bes, "servers": srv, "certificates": certs, "node": node,
		"traffic": map[string]any{"open_connections": t.Open, "requests_per_second": t.RPS, "bytes_in_per_second": t.In, "bytes_out_per_second": t.Out}})
}
