// Package reconcile turns desired state into a running HAProxy (spec §3.3, §7.6, §7.7).
// Exactly one reconciler goroutine runs per node.
package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/2shiraz/loxy/internal/agent"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/haproxy/render"
	"github.com/2shiraz/loxy/internal/haproxy/revfs"
	"github.com/2shiraz/loxy/internal/haproxy/validate"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store"
	"github.com/2shiraz/loxy/internal/store/db"
)

// Options are node-level settings that feed the renderer.
type Options struct {
	NodeID         int64
	PanelPort      int
	PanelAllow     []string
	LoxyAddr       string
	StateDir       string // /var/lib/haproxy
	SocketPath     string
	GlobalMaxConn  int
	PanelPEM       string
	PlaceholderPEM string
	// NoHAProxy: render + validate + swap only (no sockets).
	NoHAProxy bool
}

type Reconciler struct {
	Store *store.Store
	Box   *secrets.Box
	FS    *revfs.FS
	Val   *validate.Validator
	Node  agent.NodeTransport
	Hub   *events.Hub
	Opt   Options

	trigger chan struct{}
	mu      sync.Mutex // serialises applies
	applied *applied
	promex  bool

	tokMu  sync.Mutex
	tokens map[string]string // ACME HTTP-01 token → keyAuth

	lastReload time.Time
	// OnApplied runs after every successful apply (e.g. to resume pending ACME orders).
	OnApplied func()
}

type applied struct {
	snap *state.Snapshot
	out  *render.Output
	skel string
	rev  int64
}

func New(s *store.Store, box *secrets.Box, fs *revfs.FS, v *validate.Validator, n agent.NodeTransport, hub *events.Hub, opt Options) *Reconciler {
	return &Reconciler{Store: s, Box: box, FS: fs, Val: v, Node: n, Hub: hub, Opt: opt,
		trigger: make(chan struct{}, 1), tokens: map[string]string{}}
}

func (r *Reconciler) SetPromex(on bool) { r.promex = on }

// Trigger requests a reconcile (debounced).
func (r *Reconciler) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Run is the reconciler loop: 250 ms quiet period, 1 s max wait, ≥1 s between reloads.
func (r *Reconciler) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.trigger:
		}
		first := time.Now()
		quiet := time.NewTimer(250 * time.Millisecond)
	debounce:
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.trigger:
				if time.Since(first) >= time.Second {
					break debounce
				}
				quiet.Reset(250 * time.Millisecond)
			case <-quiet.C:
				break debounce
			}
		}
		quiet.Stop()
		if err := r.Apply(ctx, "reconciler"); err != nil {
			slog.Warn("apply failed", "err", err)
		}
	}
}

// Input builds the render input for a snapshot.
func (r *Reconciler) Input(snap *state.Snapshot, root string, rev, desired int64) render.Input {
	acme := map[int64]bool{}
	for _, d := range snap.Domains {
		if d.LbID.Valid {
			acme[d.LbID.Int64] = true
		}
	}
	for _, lb := range snap.LBs {
		for _, s := range lb.Services {
			for _, cid := range s.CertIDs {
				if c := snap.Cert(cid); c != nil && c.Type == "managed" && c.Challenge == "http01" {
					acme[lb.ID] = true
				}
			}
		}
	}
	return render.Input{
		Root: root, NodeID: r.Opt.NodeID, Revision: rev, DesiredVersion: desired,
		PanelPort: r.Opt.PanelPort, PanelAllow: r.Opt.PanelAllow, LoxyAddr: r.Opt.LoxyAddr, Promex: r.promex,
		StateFile: filepath.Join(r.Opt.StateDir, "server-state"), SocketPath: r.Opt.SocketPath, GlobalMaxConn: r.Opt.GlobalMaxConn,
		LBs: snap.LBsOnNode(r.Opt.NodeID), Cert: snap.Cert, ACMEHTTP01: acme,
		PanelPEM: r.Opt.PanelPEM, PlaceholderPEM: r.Opt.PlaceholderPEM,
	}
}

// RenderLive renders the config for the current desired state as it would be written to live.
func (r *Reconciler) RenderLive(ctx context.Context) (*render.Output, error) {
	snap, err := state.Load(ctx, r.Store.Q, r.Box)
	if err != nil {
		return nil, err
	}
	node := snap.Node(r.Opt.NodeID)
	return render.Render(r.Input(snap, filepath.Join(r.FS.Root, "live"), 0, node.DesiredVersion))
}

func hashFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b := files[n]
		if n == "haproxy.cfg" {
			// ignore the header comment (revision numbers)
			if i := bytes.IndexByte(b, '\n'); i >= 0 {
				b = b[i+1:]
			}
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// liveFiles reads the current live tree (for self-healing comparison at start).
func (r *Reconciler) liveFiles() map[string][]byte {
	out := map[string][]byte{}
	live := r.FS.LiveDir()
	_ = filepath.Walk(live+"/", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(live, p)
		if rel == "haproxy.check.cfg" {
			return nil
		}
		b, _ := os.ReadFile(p)
		out[rel] = b
		return nil
	})
	return out
}

// Apply runs one reconcile pass.
func (r *Reconciler) Apply(ctx context.Context, by string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := time.Now()
	q := r.Store.Q
	snap, err := state.Load(ctx, q, r.Box)
	if err != nil {
		return err
	}
	node := snap.Node(r.Opt.NodeID)
	if node == nil {
		return errors.New("node not found")
	}
	desired := node.DesiredVersion
	maxRev, _ := q.MaxRevision(ctx, r.Opt.NodeID)
	if l := r.FS.Live(); l > maxRev {
		maxRev = l
	}
	if revs := r.FS.Revisions(); len(revs) > 0 && revs[len(revs)-1] > maxRev {
		maxRev = revs[len(revs)-1]
	}
	rev := maxRev + 1
	liveRoot := filepath.Join(r.FS.Root, "live")
	in := r.Input(snap, liveRoot, rev, desired)
	out, err := render.Render(in)
	if err != nil {
		return r.fail(ctx, snap, rev, desired, "render", err.Error(), start, by)
	}
	in.OmitServers = true
	skelOut, _ := render.Render(in)
	skel := stripHeader(skelOut.Config())

	// Nothing changed compared to what is live (also covers cold start self-healing, §7.8).
	if r.applied == nil && r.FS.Live() > 0 && hashFiles(r.liveFiles()) == hashFiles(out.Files) {
		r.applied = &applied{snap: snap, out: out, skel: skel, rev: r.FS.Live()}
		r.succeed(ctx, snap, desired)
		return nil
	}
	if r.applied != nil && hashFiles(r.applied.out.Files) == hashFiles(out.Files) && stateSig(r.applied.snap, r.Opt.NodeID) == stateSig(snap, r.Opt.NodeID) {
		r.applied.snap = snap
		r.succeed(ctx, snap, desired)
		return nil
	}

	kind, ops := "reload", []agent.RuntimeCmd(nil)
	if r.applied != nil && r.FS.Live() == r.applied.rev && !r.Opt.NoHAProxy {
		if k, o := classify(r.applied, snap, out, skel, r.Opt.NodeID, liveRoot); k == "runtime" {
			kind, ops = k, o
		}
	}
	if r.FS.Live() == 0 {
		kind = "bootstrap"
	}

	checkIn := in
	checkIn.OmitServers = false
	checkIn.Root = r.FS.RevDir(rev)
	chk, err := render.Render(checkIn)
	if err != nil {
		return r.fail(ctx, snap, rev, desired, kind, err.Error(), start, by)
	}
	if _, err := r.FS.Write(rev, out.Files, chk.Files["haproxy.cfg"]); err != nil {
		return r.fail(ctx, snap, rev, desired, kind, err.Error(), start, by)
	}
	if msg, err := r.Val.Check(ctx, filepath.Join(r.FS.RevDir(rev), "haproxy.check.cfg")); err != nil {
		return r.fail(ctx, snap, rev, desired, kind, strings.TrimSpace(msg+"\n"+err.Error()), start, by)
	}
	for _, lb := range snap.LBsOnNode(r.Opt.NodeID) {
		r.Hub.Publish(events.Event{Kind: "lb.apply.started", LBID: events.I64(lb.ID), Ephemeral: true})
	}
	prevLive := r.FS.Live()
	output := ""
	if kind == "runtime" {
		if err := r.FS.Swap(rev); err != nil {
			return r.fail(ctx, snap, rev, desired, kind, err.Error(), start, by)
		}
		if _, err := r.Node.Runtime(ctx, ops); err != nil {
			slog.Warn("runtime op failed, falling back to reload", "err", err)
			output = "runtime failed: " + err.Error() + "\n"
			kind = "reload"
		}
	}
	if kind != "runtime" {
		if !r.Opt.NoHAProxy && prevLive > 0 {
			r.dumpServerState(ctx)
		}
		if err := r.FS.Swap(rev); err != nil {
			return r.fail(ctx, snap, rev, desired, kind, err.Error(), start, by)
		}
		if !r.Opt.NoHAProxy && kind != "bootstrap" {
			if wait := time.Second - time.Since(r.lastReload); wait > 0 {
				time.Sleep(wait)
			}
			res, err := r.Node.Reload(ctx)
			r.lastReload = time.Now()
			if err != nil || !res.Success {
				msg := ""
				if res != nil {
					msg = res.Output
				}
				if err != nil {
					msg += err.Error()
				}
				if prevLive > 0 {
					_ = r.FS.Swap(prevLive)
					if output != "" {
						// Runtime ops were partially applied before this failed reload: reload the
						// previous revision so HAProxy matches the live tree again (§3.3).
						if rr, err := r.Node.Reload(ctx); err == nil && rr.Success {
							msg += "\n(previous revision re-applied to discard partial runtime changes)"
						}
					}
				}
				return r.fail(ctx, snap, rev, desired, kind, msg, start, by)
			}
			output += res.Output
			r.repushTokens(ctx)
		}
	}
	r.applied = &applied{snap: snap, out: out, skel: skel, rev: rev}
	sum := sha256.Sum256(out.Files["haproxy.cfg"])
	if _, err := q.CreateRevision(ctx, db.CreateRevisionParams{NodeID: r.Opt.NodeID, Revision: rev, DesiredVersion: desired,
		Sha256: hex.EncodeToString(sum[:]), Kind: kind, Result: "ok", DurationMs: time.Since(start).Milliseconds(),
		HaproxyOutput: truncate(output, 16000), StateJson: "{}", CreatedBy: by, CreatedAt: time.Now().UnixMilli()}); err != nil {
		slog.Warn("record revision", "err", err)
	}
	Stats.record(kind, "ok", time.Since(start).Milliseconds())
	r.succeed(ctx, snap, desired)
	r.Hub.Publish(events.Event{Kind: "config.applied", Message: fmt.Sprintf("Revision %d applied (%s) in %d ms", rev, kind, time.Since(start).Milliseconds()),
		Data: map[string]any{"revision": rev, "kind": kind}})
	r.gc(ctx)
	if r.OnApplied != nil {
		go r.OnApplied()
	}
	slog.Info("applied", "revision", rev, "kind", kind, "ops", len(ops), "ms", time.Since(start).Milliseconds())
	return nil
}

func stripHeader(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (r *Reconciler) succeed(ctx context.Context, snap *state.Snapshot, desired int64) {
	q := r.Store.Q
	_ = q.SetAppliedVersion(ctx, db.SetAppliedVersionParams{AppliedVersion: desired, ID: r.Opt.NodeID})
	for _, lb := range snap.LBsOnNode(r.Opt.NodeID) {
		if lb.Status != "active" || lb.LastError.Valid {
			_ = q.SetLoadBalancerStatus(ctx, db.SetLoadBalancerStatusParams{Status: "active", ID: lb.ID})
			r.Hub.Publish(events.Event{Kind: "lb.apply.succeeded", LBID: events.I64(lb.ID), Ephemeral: true})
		}
	}
}

func (r *Reconciler) fail(ctx context.Context, snap *state.Snapshot, rev, desired int64, kind, msg string, start time.Time, by string) error {
	q := r.Store.Q
	_, _ = q.CreateRevision(ctx, db.CreateRevisionParams{NodeID: r.Opt.NodeID, Revision: rev, DesiredVersion: desired, Sha256: "",
		Kind: kind, Result: "failed", DurationMs: time.Since(start).Milliseconds(), HaproxyOutput: truncate(msg, 16000),
		StateJson: "{}", CreatedBy: by, CreatedAt: time.Now().UnixMilli()})
	_ = os.RemoveAll(r.FS.RevDir(rev))
	Stats.record(kind, "failed", time.Since(start).Milliseconds())
	for _, lb := range snap.LBsOnNode(r.Opt.NodeID) {
		_ = q.SetLoadBalancerStatus(ctx, db.SetLoadBalancerStatusParams{Status: "apply_error", LastError: sql.NullString{String: truncate(msg, 4000), Valid: true}, ID: lb.ID})
		r.Hub.Publish(events.Event{Kind: "lb.apply.failed", LBID: events.I64(lb.ID), Severity: "error", Message: "Apply failed: " + firstLine(msg)})
	}
	return fmt.Errorf("apply rev %d failed: %s", rev, firstLine(msg))
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "ALERT") {
			return strings.TrimSpace(l)
		}
	}
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

func (r *Reconciler) dumpServerState(ctx context.Context) {
	res, err := r.Node.Runtime(ctx, []agent.RuntimeCmd{{Cmd: "show servers state"}})
	if err != nil && len(res) == 0 {
		return
	}
	out := ""
	if len(res) > 0 {
		out = res[0].Output
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "1") {
		return
	}
	p := filepath.Join(r.Opt.StateDir, "server-state")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o640); err == nil {
		_ = os.Rename(tmp, p)
	}
}

func (r *Reconciler) gc(ctx context.Context) {
	revs, err := r.Store.Q.ListRevisions(ctx, r.Opt.NodeID)
	if err != nil {
		return
	}
	pinned := map[int64]bool{}
	for _, rv := range revs {
		if rv.Pinned != 0 {
			pinned[rv.Revision] = true
		}
	}
	r.FS.GC(20, pinned)
}

// SetACMEToken publishes an HTTP-01 token at runtime (and keeps it for re-push after reloads).
func (r *Reconciler) SetACMEToken(ctx context.Context, token, keyAuth string) error {
	r.tokMu.Lock()
	r.tokens[token] = keyAuth
	r.tokMu.Unlock()
	if r.Opt.NoHAProxy {
		return nil
	}
	_, err := r.Node.Runtime(ctx, []agent.RuntimeCmd{{Cmd: "add map virt@acme_http01 " + token + " " + keyAuth}})
	return err
}

func (r *Reconciler) DelACMEToken(ctx context.Context, token string) {
	r.tokMu.Lock()
	delete(r.tokens, token)
	r.tokMu.Unlock()
	if !r.Opt.NoHAProxy {
		_, _ = r.Node.Runtime(ctx, []agent.RuntimeCmd{{Cmd: "del map virt@acme_http01 " + token}})
	}
}

func (r *Reconciler) repushTokens(ctx context.Context) {
	r.tokMu.Lock()
	defer r.tokMu.Unlock()
	for t, k := range r.tokens {
		_, _ = r.Node.Runtime(ctx, []agent.RuntimeCmd{{Cmd: "add map virt@acme_http01 " + t + " " + k}})
	}
}

// Rollback re-applies an earlier on-disk revision (validated again) and pins it.
func (r *Reconciler) Rollback(ctx context.Context, rev int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := os.Stat(r.FS.RevDir(rev)); err != nil {
		return fmt.Errorf("revision %d is no longer on disk", rev)
	}
	if msg, err := r.Val.Check(ctx, filepath.Join(r.FS.RevDir(rev), "haproxy.check.cfg")); err != nil {
		return fmt.Errorf("revision %d does not validate: %s", rev, msg)
	}
	prev := r.FS.Live()
	start := time.Now()
	if !r.Opt.NoHAProxy {
		r.dumpServerState(ctx)
	}
	if err := r.FS.Swap(rev); err != nil {
		return err
	}
	if !r.Opt.NoHAProxy {
		if wait := time.Second - time.Since(r.lastReload); wait > 0 {
			time.Sleep(wait)
		}
		res, err := r.Node.Reload(ctx)
		r.lastReload = time.Now()
		if err != nil || !res.Success {
			_ = r.FS.Swap(prev)
			return fmt.Errorf("reload failed")
		}
		r.repushTokens(ctx)
	}
	r.applied = nil // next apply compares against disk
	_, _ = r.Store.DB.ExecContext(ctx, `UPDATE config_revisions SET pinned = 1 WHERE node_id = ? AND revision = ?`, r.Opt.NodeID, rev)
	maxRev, _ := r.Store.Q.MaxRevision(ctx, r.Opt.NodeID)
	_, _ = r.Store.Q.CreateRevision(ctx, db.CreateRevisionParams{NodeID: r.Opt.NodeID, Revision: maxRev + 1, Sha256: "", Kind: "rollback",
		Result: "ok", DurationMs: time.Since(start).Milliseconds(), HaproxyOutput: fmt.Sprintf("rolled back to revision %d", rev),
		StateJson: "{}", CreatedBy: "api", CreatedAt: time.Now().UnixMilli()})
	r.Hub.Publish(events.Event{Kind: "config.rollback", Severity: "warning", Message: fmt.Sprintf("Rolled back to revision %d", rev)})
	return nil
}
