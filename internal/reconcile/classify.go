package reconcile

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/2shiraz/loxy/internal/agent"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/state"
)

// effective admin state of a target: maint > drain (incl. graceful removal) > ready.
func effState(t *state.Target) string {
	switch {
	case t.AdminState == "maint":
		return "maint"
	case t.AdminState == "drain" || t.Removing != 0:
		return "drain"
	}
	return "ready"
}

type slotRef struct {
	t  *state.Target
	sl *state.Slot
}

func slotsByID(lb *state.LB) map[int64]slotRef {
	m := map[int64]slotRef{}
	for _, t := range lb.Targets {
		for _, sl := range t.Slots {
			m[sl.ID] = slotRef{t, sl}
		}
	}
	return m
}

// dynamicOK reports whether a backend algorithm supports dynamic servers (§7.6).
func dynamicOK(alg string) bool {
	return alg == "roundrobin" || alg == "leastconn" || alg == "random" || alg == "first"
}

// classify decides between "runtime" and "reload" (spec §7.6) and returns the runtime ops.
// Anything not expressible at runtime (listeners, check params, crt-lists, …) shows up as a
// difference in the server-less skeleton config or in a non-cert file and forces a reload.
func classify(prev *applied, snap *state.Snapshot, out *render.Output, skel string, nodeID int64, liveRoot string) (string, []agent.RuntimeCmd) {
	if prev.skel != skel {
		return "reload", nil
	}
	var ops []agent.RuntimeCmd
	for name, b := range out.Files {
		if name == "haproxy.cfg" {
			continue
		}
		old, ok := prev.out.Files[name]
		if ok && bytes.Equal(old, b) {
			continue
		}
		switch {
		case name == "maps/maint.map":
			continue // handled below per LB
		case strings.HasPrefix(name, "certs/") && ok && name != "certs/panel.pem":
			p := liveRoot + "/" + name
			ops = append(ops, agent.RuntimeCmd{Cmd: "set ssl cert " + p, Payload: string(b)}, agent.RuntimeCmd{Cmd: "commit ssl cert " + p})
		default:
			return "reload", nil
		}
	}
	for name := range prev.out.Files {
		if _, ok := out.Files[name]; !ok && !strings.HasPrefix(name, "certs/") {
			return "reload", nil
		}
	}
	mm := render.MaintMapPath(liveRoot)
	for _, lb := range snap.LBsOnNode(nodeID) {
		old := prev.snap.LB(lb.ID)
		if old == nil {
			return "reload", nil
		}
		if old.Maintenance != lb.Maintenance {
			if lb.Maintenance != 0 {
				ops = append(ops, agent.RuntimeCmd{Cmd: fmt.Sprintf("add map %s %d 1", mm, lb.ID)})
			} else {
				ops = append(ops, agent.RuntimeCmd{Cmd: fmt.Sprintf("del map %s %d", mm, lb.ID)})
			}
		}
		was, now := slotsByID(old), slotsByID(lb)
		for _, s := range lb.Services {
			be := render.BackendName(lb.ID, s.ID)
			for id, n := range now {
				name := be + "/" + render.ServerName(n.t, n.sl)
				o, existed := was[id]
				if !existed {
					if !dynamicOK(lb.Algorithm) {
						return "reload", nil
					}
					ops = append(ops,
						agent.RuntimeCmd{Cmd: "add server " + name + " " + render.ServerArgs(lb, s, n.t, n.sl)},
						agent.RuntimeCmd{Cmd: "enable health " + name})
					switch effState(n.t) {
					case "ready":
						ops = append(ops, agent.RuntimeCmd{Cmd: "enable server " + name})
					case "drain":
						ops = append(ops, agent.RuntimeCmd{Cmd: "set server " + name + " state drain"})
					}
					continue
				}
				oldSvc := old.Service(s.ID)
				if oldSvc == nil {
					return "reload", nil
				}
				// Service-level parameters (ports, checks, TLS, PROXY protocol, cookies) only
				// appear on server lines: any difference other than weight needs a reload.
				oa, na := render.ServerArgs(old, oldSvc, o.t, o.sl), render.ServerArgs(lb, s, n.t, n.sl)
				if oa != na {
					if stripWeight(oa) != stripWeight(na) {
						return "reload", nil
					}
					ops = append(ops, agent.RuntimeCmd{Cmd: fmt.Sprintf("set server %s weight %d", name, n.t.Weight)})
				}
				if es := effState(n.t); es != effState(o.t) {
					ops = append(ops, agent.RuntimeCmd{Cmd: "set server " + name + " state " + es})
				}
			}
			for id, o := range was {
				if _, still := now[id]; still {
					continue
				}
				if !dynamicOK(lb.Algorithm) {
					return "reload", nil
				}
				name := be + "/" + render.ServerName(o.t, o.sl)
				ops = append(ops,
					agent.RuntimeCmd{Cmd: "set server " + name + " state maint"},
					agent.RuntimeCmd{Cmd: "wait 5s srv-removable " + name},
					agent.RuntimeCmd{Cmd: "del server " + name})
			}
		}
	}
	return "runtime", ops
}

var weightRe = regexp.MustCompile(` weight \d+`)

func stripWeight(args string) string { return weightRe.ReplaceAllString(args, "") }

// stateSig captures runtime-only state (admin states, maintenance) that is not fully
// visible in the rendered files, so a pure state change still reaches HAProxy (§7.6).
func stateSig(snap *state.Snapshot, nodeID int64) string {
	var b strings.Builder
	for _, lb := range snap.LBsOnNode(nodeID) {
		fmt.Fprintf(&b, "L%d:%d;", lb.ID, lb.Maintenance)
		for _, t := range lb.Targets {
			fmt.Fprintf(&b, "T%d:%s;", t.ID, effState(t))
		}
	}
	return b.String()
}
