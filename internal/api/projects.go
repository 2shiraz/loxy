package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

var algorithms = map[string]bool{"roundrobin": true, "leastconn": true, "source": true, "random": true}

func (s *Server) project(id int64) (*state.Project, error) {
	p := s.Snap.Get().Project(id)
	if p == nil {
		return nil, errNotFound("project")
	}
	return p, nil
}

func (s *Server) respondProject(w http.ResponseWriter, status int, id int64) {
	p := s.Snap.Get().Project(id)
	if p == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"project": s.viewProject(p)})
}

func (s *Server) ListProjects(w http.ResponseWriter, r *http.Request, prm gen.ListProjectsParams) {
	var sel model.Selector
	if prm.LabelSelector != nil && *prm.LabelSelector != "" {
		var err error
		if sel, err = model.ParseSelector(*prm.LabelSelector); err != nil {
			fail(w, errField("label_selector", err.Error()))
			return
		}
	}
	out := []map[string]any{}
	for _, p := range s.Snap.Get().Projects {
		if sel != nil && !sel.Matches(p.Labels) {
			continue
		}
		out = append(out, s.viewProject(p))
	}
	writeJSON(w, 200, map[string]any{"projects": out})
}

func (s *Server) GetProject(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"project": s.viewProject(p)})
}

// validProjectName checks name rules and uniqueness (self = existing id or 0).
func (s *Server) validProjectName(fe model.FieldErrors, name string, self int64) {
	if !model.ValidName(name) {
		fe.Add("name", "1–64 printable characters")
		return
	}
	for _, o := range s.Snap.Get().Projects {
		if o.ID != self && strings.EqualFold(o.Name, name) {
			fe.Add("name", "already in use")
		}
	}
}

// CreateProject creates a project with its first service (own free port by default) and
// optional targets, in one transaction (§8.2).
func (s *Server) CreateProject(w http.ResponseWriter, r *http.Request) {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	var in gen.ProjectCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	fe := model.FieldErrors{}
	name := strings.TrimSpace(in.Name)
	s.validProjectName(fe, name, 0)
	bp := db.CreateBalancerParams{Name: name, Algorithm: "roundrobin", Labels: "{}", CreatedAt: nowMs(), UpdatedAt: nowMs()}
	if in.Description != nil {
		bp.Description = strings.TrimSpace(*in.Description)
		if len(bp.Description) > 500 {
			fe.Add("description", "at most 500 characters")
		}
	}
	if in.Algorithm != nil {
		bp.Algorithm = string(*in.Algorithm)
	}
	if !algorithms[bp.Algorithm] {
		fe.Add("algorithm", "must be roundrobin, leastconn, source or random")
	}
	if in.Labels != nil {
		model.ValidateLabels(fe, "labels", *in.Labels)
		bp.Labels = jsonStr(*in.Labels)
	}
	svcIn := gen.ServiceInput{Protocol: "http", DestinationPort: 80}
	if in.Service != nil {
		svcIn = *in.Service
	}
	draft := &state.Project{}
	sp, sfe := s.buildService(draft, nil, svcIn, "service")
	for k, msgs := range sfe {
		fe[k] = append(fe[k], msgs...)
	}
	var targets []db.CreateTargetParams
	if in.Targets != nil && len(*in.Targets) > 0 {
		var tfe model.FieldErrors
		targets, tfe = s.buildTargets(nil, *in.Targets, "targets")
		for k, msgs := range tfe {
			fe[k] = append(fe[k], msgs...)
		}
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	var id int64
	if err := s.mutate(r, "project.create", "project", func() int64 { return id }, in, func(q *db.Queries) error {
		b, err := q.CreateBalancer(r.Context(), bp)
		if err != nil {
			return err
		}
		id = b.ID
		sp.p.BalancerID = id
		if _, err := s.insertService(r.Context(), q, sp); err != nil {
			return err
		}
		for _, t := range targets {
			t.BalancerID = id
			if _, err := q.CreateTarget(r.Context(), t); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondProject(w, 201, id)
}

func (s *Server) UpdateProject(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := checkIfMatch(r, p.Version); err != nil {
		fail(w, err)
		return
	}
	var in gen.ProjectUpdate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	u := db.UpdateBalancerParams{Name: p.Name, Description: p.Description, Algorithm: p.Algorithm, Labels: jsonStr(p.Labels),
		Maintenance: p.Maintenance, UpdatedAt: nowMs(), ID: id}
	fe := model.FieldErrors{}
	if in.Name != nil {
		u.Name = strings.TrimSpace(*in.Name)
		s.validProjectName(fe, u.Name, id)
	}
	if in.Description != nil {
		u.Description = strings.TrimSpace(*in.Description)
		if len(u.Description) > 500 {
			fe.Add("description", "at most 500 characters")
		}
	}
	if in.Algorithm != nil {
		u.Algorithm = string(*in.Algorithm)
		if !algorithms[u.Algorithm] {
			fe.Add("algorithm", "must be roundrobin, leastconn, source or random")
		}
	}
	if in.Labels != nil {
		model.ValidateLabels(fe, "labels", *in.Labels)
		u.Labels = jsonStr(*in.Labels)
	}
	if in.Maintenance != nil {
		u.Maintenance = b2i(*in.Maintenance)
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	if err := s.mutate(r, "project.update", "project", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateBalancer(r.Context(), u)
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondProject(w, 200, id)
}

func (s *Server) DeleteProject(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	doms := s.Snap.Get().DomainsOf(id)
	for _, d := range doms {
		s.removeManagedRecords(r.Context(), d)
	}
	if err := s.mutate(r, "project.delete", "project", func() int64 { return id }, map[string]any{"name": p.Name}, func(q *db.Queries) error {
		for _, d := range doms {
			if err := q.DeleteDomain(r.Context(), d.ID); err != nil {
				return err
			}
		}
		return q.DeleteBalancer(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// GetProjectConfig returns the rendered HAProxy sections of a project's services and the
// domain routing lines that point at them.
func (s *Server) GetProjectConfig(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	out, err := s.Rec.RenderLive(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	cfg := out.Config()
	var b strings.Builder
	snap := s.Snap.Get()
	for _, sv := range p.Services {
		if f := snap.Frontend(sv.ID); f != nil {
			b.WriteString(render.FrontendSections(cfg, f))
			b.WriteString("\n")
		}
	}
	var dom []string
	for _, line := range strings.Split(cfg, "\n") {
		for _, sv := range p.Services {
			if strings.Contains(line, "use_backend "+render.BackendName(sv.ID)+" ") {
				dom = append(dom, strings.TrimSpace(line))
			}
		}
	}
	if len(dom) > 0 {
		b.WriteString("# domains (frontends sys_http / sys_https)\n")
		for _, l := range dom {
			b.WriteString("    " + l + "\n")
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
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

// GetProjectMetrics returns project series (all services, port and domain traffic) or one
// service's backend series with service_id.
func (s *Server) GetProjectMetrics(w http.ResponseWriter, r *http.Request, id gen.Id, prm gen.GetProjectMetricsParams) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	scope, sid := metrics.PR, id
	if prm.ServiceId != nil {
		if p.Service(*prm.ServiceId) == nil {
			fail(w, errField("service_id", "not a service of this project"))
			return
		}
		scope, sid = metrics.BE, *prm.ServiceId
	}
	a, b, st, types := metricsParams(prm.Start, prm.End, prm.Step, prm.Type)
	series, err := s.Metrics.Query(r.Context(), scope, sid, a, b, st, types)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"start": series.Start, "end": series.End, "step": series.Step, "timestamps": series.Timestamps, "series": series.Values})
}

func (s *Server) StreamProjectMetrics(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if _, err := s.project(id); err != nil {
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
			b, _ := json.Marshal(livePoint(p))
			fmt.Fprintf(w, "event: metrics.tick\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}

// GetOverview is the dashboard summary.
func (s *Server) GetOverview(w http.ResponseWriter, r *http.Request) {
	snap := s.Snap.Get()
	projects := map[string]int{"total": len(snap.Projects), "apply_errors": 0, "healthy": 0, "degraded": 0, "unhealthy": 0}
	tg := map[string]int{"total": 0, "healthy": 0}
	for _, p := range snap.Projects {
		if p.Status == "error" {
			projects["apply_errors"]++
		}
		sum, _ := s.projectHealth(p)
		if _, ok := projects[sum.Status]; ok {
			projects[sum.Status]++
		}
		tg["total"] += sum.Total
		tg["healthy"] += sum.Healthy
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
	writeJSON(w, 200, map[string]any{"projects": projects, "targets": tg, "certificates": certs, "node": node,
		"traffic": map[string]any{"open_connections": t.Open, "requests_per_second": t.RPS, "bytes_in_per_second": t.In, "bytes_out_per_second": t.Out}})
}

// livePoint is one metrics.tick of the project stream. It carries every series of
// GET /projects/{id}/metrics (same names), so all Live charts keep updating.
func livePoint(p metrics.Point) map[string]any {
	out := map[string]any{"ts": p.TS}
	for _, t := range metrics.SeriesTypes() {
		out[t] = metrics.Pick(p, t)
	}
	return out
}
