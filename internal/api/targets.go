package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

const maxTargets = 500

// splitIPPort accepts "ip", "ip:port" and "[v6]:port".
func splitIPPort(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().String(), int(ap.Port()), true
	}
	return s, 0, false
}

// buildTargets validates target inputs (§8.4). field prefixes error names ("targets").
func (s *Server) buildTargets(p *state.Project, ins []gen.TargetInput, field string) ([]db.CreateTargetParams, model.FieldErrors) {
	snap := s.Snap.Get()
	key := func(typ, ip string, host int64, sel string, port int64) string {
		return fmt.Sprintf("%s|%s|%d|%s|%d", typ, ip, host, sel, port)
	}
	existing := map[string]bool{}
	n := 0
	if p != nil {
		n = len(p.Targets)
		for _, t := range p.Targets {
			existing[key(t.Type, t.Ip, t.HostID.Int64, t.LabelSelector, t.PortOr(0))] = true
		}
	}
	fe := model.FieldErrors{}
	var out []db.CreateTargetParams
	if n+len(ins) > maxTargets {
		fe.Add(field, fmt.Sprintf("at most %d targets per project", maxTargets))
	}
	for i, in := range ins {
		f := fmt.Sprintf("%s[%d]", field, i)
		t := db.CreateTargetParams{Type: string(in.Type), Weight: 100, CreatedAt: nowMs()}
		if p != nil {
			t.BalancerID = p.ID
		}
		if in.Weight != nil {
			t.Weight = int64(*in.Weight)
		}
		if t.Weight < 0 || t.Weight > 256 {
			fe.Add(f+".weight", "must be 0–256")
		}
		if in.Backup != nil {
			t.Backup = b2i(*in.Backup)
		}
		if in.UsePrivateIp != nil {
			t.UsePrivateIp = b2i(*in.UsePrivateIp)
		}
		port := 0
		if in.Port != nil {
			port = *in.Port
		}
		switch t.Type {
		case "ip":
			if in.Ip == nil || strings.TrimSpace(*in.Ip) == "" {
				fe.Add(f+".ip", "required")
				continue
			}
			ip, pp, hasPort := splitIPPort(*in.Ip)
			if hasPort {
				port = pp
			}
			a, err := model.ParseTargetIP(ip)
			if err != nil {
				// Dev mode (make dev) allows loopback targets such as local test containers.
				if la, perr := netip.ParseAddr(ip); s.Cfg.Dev && perr == nil && la.IsLoopback() {
					a = la
				} else {
					fe.Add(f+".ip", err.Error())
					continue
				}
			}
			t.Ip = a.String()
		case "host":
			if in.HostId == nil || snap.Host(*in.HostId) == nil {
				fe.Add(f+".host_id", "unknown host")
				continue
			}
			t.HostID = sql.NullInt64{Int64: *in.HostId, Valid: true}
		case "label_selector":
			if in.LabelSelector == nil || strings.TrimSpace(*in.LabelSelector) == "" {
				fe.Add(f+".label_selector", "required")
				continue
			}
			if _, err := model.ParseSelector(*in.LabelSelector); err != nil {
				fe.Add(f+".label_selector", err.Error())
				continue
			}
			t.LabelSelector = strings.TrimSpace(*in.LabelSelector)
		default:
			fe.Add(f+".type", "must be ip, host or label_selector")
			continue
		}
		if port != 0 {
			if !model.ValidPort(port) {
				fe.Add(f+".port", "must be 1–65535")
			}
			t.Port = sql.NullInt64{Int64: int64(port), Valid: true}
		}
		k := key(t.Type, t.Ip, t.HostID.Int64, t.LabelSelector, t.Port.Int64)
		if existing[k] {
			fe.Add(f, "target already exists")
		}
		existing[k] = true
		out = append(out, t)
	}
	return out, fe
}

func (s *Server) ListTargets(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	view := s.targetViewer(p)
	out := []map[string]any{}
	for _, t := range p.Targets {
		out = append(out, view(t))
	}
	writeJSON(w, 200, map[string]any{"targets": out})
}

func (s *Server) CreateTargets(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.TargetsCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if len(in.Targets) == 0 {
		fail(w, errField("targets", "add at least one target"))
		return
	}
	ts, fe := s.buildTargets(p, in.Targets, "targets")
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	var ids []int64
	if err := s.mutate(r, "target.create", "project", func() int64 { return id }, in, func(q *db.Queries) error {
		for _, t := range ts {
			row, err := q.CreateTarget(r.Context(), t)
			if err != nil {
				return err
			}
			ids = append(ids, row.ID)
		}
		return nil
	}); err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	if np := s.Snap.Get().Project(id); np != nil {
		view := s.targetViewer(np)
		for _, tid := range ids {
			if t := np.Target(tid); t != nil {
				out = append(out, view(t))
			}
		}
	}
	writeJSON(w, 201, map[string]any{"targets": out})
}

func (s *Server) UpdateTarget(w http.ResponseWriter, r *http.Request, id gen.Id, tid int64) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	t := p.Target(tid)
	if t == nil {
		fail(w, errNotFound("target"))
		return
	}
	var in gen.TargetUpdate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	u := db.UpdateTargetParams{Weight: t.Weight, Backup: t.Backup, AdminState: t.AdminState, ID: tid}
	if in.Weight != nil {
		if *in.Weight < 0 || *in.Weight > 256 {
			fail(w, errField("weight", "must be 0–256"))
			return
		}
		u.Weight = int64(*in.Weight)
	}
	if in.AdminState != nil {
		u.AdminState = string(*in.AdminState)
		if u.AdminState != "ready" && u.AdminState != "drain" && u.AdminState != "maint" {
			fail(w, errField("admin_state", "must be ready, drain or maint"))
			return
		}
	}
	if in.Backup != nil {
		u.Backup = b2i(*in.Backup)
	}
	if err := s.mutate(r, "target.update", "project", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateTarget(r.Context(), u)
	}); err != nil {
		fail(w, err)
		return
	}
	np := s.Snap.Get().Project(id)
	if np == nil || np.Target(tid) == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 200, map[string]any{"target": s.targetViewer(np)(np.Target(tid))})
}

// DeleteTarget starts a graceful removal: drain → wait for sessions to end or timeout → delete (§7.6).
func (s *Server) DeleteTarget(w http.ResponseWriter, r *http.Request, id gen.Id, tid int64) {
	p, err := s.project(id)
	if err != nil {
		fail(w, err)
		return
	}
	if p.Target(tid) == nil {
		fail(w, errNotFound("target"))
		return
	}
	if err := s.mutate(r, "target.delete", "project", func() int64 { return id }, map[string]any{"target_id": tid}, func(q *db.Queries) error {
		return q.MarkTargetRemoving(r.Context(), tid)
	}); err != nil {
		fail(w, err)
		return
	}
	if s.Drain != nil {
		go s.Drain(id, tid)
	}
	w.WriteHeader(202)
}
