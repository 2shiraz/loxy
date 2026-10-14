// Package agent contains the node-side applier and the NodeTransport abstraction (spec §3.2).
// Phase 1 ships only the in-process implementation; Phase 3 adds a network transport.
package agent

import (
	"context"
	"strings"

	"github.com/2shiraz/loxy/internal/haproxy/runtime"
	"github.com/2shiraz/loxy/internal/haproxy/stats"
)

// RuntimeCmd is one CLI command; Payload is sent with the `<<` form when non-empty.
type RuntimeCmd struct {
	Cmd     string
	Payload string
}

type RuntimeResult struct {
	Output string
	Err    error
}

type NodeInfo struct {
	Version   string
	Pid       string
	UptimeSec string
	Info      map[string]string
}

// NodeTransport is how the panel talks to a node. Apply of desired state is performed by the
// reconciler on the node side (in-process for all-in-one).
type NodeTransport interface {
	Runtime(ctx context.Context, cmds []RuntimeCmd) ([]RuntimeResult, error)
	Reload(ctx context.Context) (*runtime.ReloadResult, error)
	// Stat returns `show stat` rows (servers included only when withServers) and `show info`.
	Stat(ctx context.Context, withServers bool) ([]stats.Row, map[string]string, error)
	Info(ctx context.Context) (*NodeInfo, error)
}

// Local talks to HAProxy on this host through its unix sockets.
type Local struct{ RT *runtime.Client }

func (l *Local) Runtime(ctx context.Context, cmds []RuntimeCmd) ([]RuntimeResult, error) {
	res := make([]RuntimeResult, 0, len(cmds))
	for _, c := range cmds {
		var out string
		var err error
		if c.Payload != "" {
			out, err = l.RT.ExecPayload(ctx, c.Cmd, c.Payload)
		} else {
			out, err = l.RT.Exec(ctx, c.Cmd)
		}
		if err == nil {
			err = runtime.Check(c.Cmd, out)
		}
		res = append(res, RuntimeResult{Output: out, Err: err})
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func (l *Local) Reload(ctx context.Context) (*runtime.ReloadResult, error) { return l.RT.Reload(ctx) }

func (l *Local) Stat(ctx context.Context, withServers bool) ([]stats.Row, map[string]string, error) {
	cmd := "show stat -1 3 -1" // type mask: 1 frontends | 2 backends
	if withServers {
		cmd = "show stat"
	}
	out, err := l.RT.Exec(ctx, cmd)
	if err != nil {
		return nil, nil, err
	}
	info, err := l.RT.Exec(ctx, "show info")
	if err != nil {
		return nil, nil, err
	}
	return stats.ParseStat(out), stats.ParseInfo(info), nil
}

func (l *Local) Info(ctx context.Context) (*NodeInfo, error) {
	out, err := l.RT.Exec(ctx, "show info")
	if err != nil {
		return nil, err
	}
	m := stats.ParseInfo(out)
	v := m["Version"]
	if i := strings.Index(v, "-"); i > 0 {
		v = v[:i]
	}
	return &NodeInfo{Version: v, Pid: m["Pid"], UptimeSec: m["Uptime_sec"], Info: m}, nil
}
