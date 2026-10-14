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

// effective admin state of a server: maint > drain (incl. graceful removal) > ready.
func effState(s *state.Server) string {
	switch {
	case s.AdminState == "maint":
		return "maint"
	case s.AdminState == "drain" || s.Removing != 0:
		return "drain"
	}
	return "ready"
}

type slotRef struct {
	sv *state.Server
	sl *state.Slot
}

func slotsByID(b *state.Backend) map[int64]slotRef {
	m := map[int64]slotRef{}
	for _, sv := range b.Servers {
		for _, sl := range sv.Slots {
			m[sl.ID] = slotRef{sv, sl}
		}
	}
	return m
}

// dynamicOK reports whether a backend algorithm supports dynamic servers (§7.6).
func dynamicOK(alg string) bool {
	return alg == "roundrobin" || alg == "leastconn" || alg == "random" || alg == "first"
}

var weightRe = regexp.MustCompile(` weight \d+`)

func stripWeight(args string) string { return weightRe.ReplaceAllString(args, "") }

// stateSig captures runtime-only state (admin states, maintenance) that is not fully visible
// in the rendered files, so a pure state change still reaches HAProxy (§7.6).
func stateSig(snap *state.Snapshot, nodeID int64) string {
	var b strings.Builder
	for _, f := range snap.FrontendsOnNode(nodeID) {
		fmt.Fprintf(&b, "F%d:%d;", f.ID, f.Maintenance)
	}
	for _, be := range snap.BackendsOnNode(nodeID) {
		for _, sv := range be.Servers {
			fmt.Fprintf(&b, "S%d:%s;", sv.ID, effState(sv))
		}
	}
	return b.String()
}

// classify decides between "runtime" and "reload" (spec §7.6) and returns the runtime ops.
// Anything not expressible at runtime (listeners, routes, checks, crt-lists, …) shows up as a
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
			continue // handled below per frontend
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
	for _, f := range snap.FrontendsOnNode(nodeID) {
		old := prev.snap.Frontend(f.ID)
		if old == nil {
			return "reload", nil
		}
		if old.Maintenance != f.Maintenance {
			if f.Maintenance != 0 {
				ops = append(ops, agent.RuntimeCmd{Cmd: fmt.Sprintf("add map %s %d 1", mm, f.ID)})
			} else {
				ops = append(ops, agent.RuntimeCmd{Cmd: fmt.Sprintf("del map %s %d", mm, f.ID)})
			}
		}
	}
	for _, b := range snap.BackendsOnNode(nodeID) {
		old := prev.snap.Backend(b.ID)
		if old == nil {
			return "reload", nil
		}
		be := render.BackendName(b.ID)
		was, now := slotsByID(old), slotsByID(b)
		for id, n := range now {
			name := be + "/" + render.ServerName(n.sv, n.sl)
			o, existed := was[id]
			if !existed {
				if !dynamicOK(b.Algorithm) {
					return "reload", nil
				}
				ops = append(ops,
					agent.RuntimeCmd{Cmd: "add server " + name + " " + render.ServerArgs(b, n.sv, n.sl)},
					agent.RuntimeCmd{Cmd: "enable health " + name})
				switch effState(n.sv) {
				case "ready":
					ops = append(ops, agent.RuntimeCmd{Cmd: "enable server " + name})
				case "drain":
					ops = append(ops, agent.RuntimeCmd{Cmd: "set server " + name + " state drain"})
				}
				continue
			}
			// Backend-level parameters (port, checks, TLS, PROXY, cookies) only appear on server
			// lines: any difference other than weight needs a reload.
			oa, na := render.ServerArgs(old, o.sv, o.sl), render.ServerArgs(b, n.sv, n.sl)
			if oa != na {
				if stripWeight(oa) != stripWeight(na) {
					return "reload", nil
				}
				ops = append(ops, agent.RuntimeCmd{Cmd: fmt.Sprintf("set server %s weight %d", name, n.sv.Weight)})
			}
			if es := effState(n.sv); es != effState(o.sv) {
				ops = append(ops, agent.RuntimeCmd{Cmd: "set server " + name + " state " + es})
			}
		}
		for id, o := range was {
			if _, still := now[id]; still {
				continue
			}
			if !dynamicOK(b.Algorithm) {
				return "reload", nil
			}
			name := be + "/" + render.ServerName(o.sv, o.sl)
			ops = append(ops,
				agent.RuntimeCmd{Cmd: "set server " + name + " state maint"},
				agent.RuntimeCmd{Cmd: "wait 5s srv-removable " + name},
				agent.RuntimeCmd{Cmd: "del server " + name})
		}
	}
	return "runtime", ops
}
