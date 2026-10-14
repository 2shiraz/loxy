// Package state builds immutable desired-state snapshots from SQLite (spec §3.3, §6).
// API reads are served from the snapshot; the reconciler renders it.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/store/db"
)

type Node struct {
	db.Node
	Features  []string
	Addresses []db.NodeAddress
}

type Host struct {
	db.Host
	Labels map[string]string
}

type Cert struct {
	db.Certificate
	Domains []string
	Labels  map[string]string
	KeyPEM  string // decrypted; empty when not issued
}

// PEM returns leaf+chain+key as HAProxy expects, or "" if not issued.
func (c *Cert) PEM() string {
	if c.CertPem == "" || c.KeyPEM == "" {
		return ""
	}
	parts := []string{strings.TrimSpace(c.CertPem)}
	if s := strings.TrimSpace(c.ChainPem); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, strings.TrimSpace(c.KeyPEM))
	return strings.Join(parts, "\n") + "\n"
}

// Slot is one resolved HAProxy server; ID is the stable server id.
type Slot struct {
	ID       int64
	ServerID int64
	HostID   int64 // 0 for IP servers
	Address  string
}

// Server is a configured backend server (IP, host or label selector) with its resolved slots.
type Server struct {
	db.BackendServer
	Slots []*Slot
}

// Port returns the effective port of the server.
func (s *Server) PortOr(def int64) int64 {
	if s.Port.Valid && s.Port.Int64 > 0 {
		return s.Port.Int64
	}
	return def
}

type Backend struct {
	db.Backend
	Labels      map[string]string
	StatusCodes []string
	Servers     []*Server
}

func (b *Backend) Server(id int64) *Server {
	for _, s := range b.Servers {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Slots returns all resolved servers sorted by id.
func (b *Backend) Slots() []*Slot {
	var out []*Slot
	for _, s := range b.Servers {
		out = append(out, s.Slots...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Bind is one listening address of a frontend. Address "*" = all interfaces.
type Bind struct {
	Address string `json:"address"`
	Port    int64  `json:"port"`
}

type Route struct {
	ID         int64
	Hosts      []string
	PathPrefix string
	BackendID  int64
}

type Frontend struct {
	db.Frontend
	Labels  map[string]string
	Binds   []Bind
	CertIDs []int64
	Routes  []Route
}

// BackendIDs returns default + routed backend ids (deduplicated, ordered).
func (f *Frontend) BackendIDs() []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, r := range f.Routes {
		add(r.BackendID)
	}
	add(f.DefaultBackendID.Int64)
	return out
}

type Domain struct {
	db.Domain
	Observed struct {
		A    []string `json:"a"`
		AAAA []string `json:"aaaa"`
	}
}

type Snapshot struct {
	Nodes        []*Node
	Frontends    []*Frontend
	Backends     []*Backend
	Hosts        []*Host
	Certs        []*Cert
	Domains      []*Domain
	DNSProviders []db.DnsProvider

	feByID   map[int64]*Frontend
	beByID   map[int64]*Backend
	certByID map[int64]*Cert
	hostByID map[int64]*Host
	nodeByID map[int64]*Node
}

func (s *Snapshot) Frontend(id int64) *Frontend { return s.feByID[id] }
func (s *Snapshot) Backend(id int64) *Backend   { return s.beByID[id] }
func (s *Snapshot) Cert(id int64) *Cert         { return s.certByID[id] }
func (s *Snapshot) Host(id int64) *Host         { return s.hostByID[id] }
func (s *Snapshot) Node(id int64) *Node         { return s.nodeByID[id] }

// FrontendsOnNode / BackendsOnNode return the objects placed on a node, sorted by id.
func (s *Snapshot) FrontendsOnNode(nodeID int64) []*Frontend {
	var out []*Frontend
	for _, f := range s.Frontends {
		if f.NodeID == nodeID {
			out = append(out, f)
		}
	}
	return out
}

func (s *Snapshot) BackendsOnNode(nodeID int64) []*Backend {
	var out []*Backend
	for _, b := range s.Backends {
		if b.NodeID == nodeID {
			out = append(out, b)
		}
	}
	return out
}

// UsedBy returns ids of frontends that reference a backend.
func (s *Snapshot) UsedBy(backendID int64) []int64 {
	out := []int64{}
	for _, f := range s.Frontends {
		for _, id := range f.BackendIDs() {
			if id == backendID {
				out = append(out, f.ID)
				break
			}
		}
	}
	return out
}

func labels(js string) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(js), &m)
	return m
}

func newSnapshot() *Snapshot {
	return &Snapshot{feByID: map[int64]*Frontend{}, beByID: map[int64]*Backend{}, certByID: map[int64]*Cert{}, hostByID: map[int64]*Host{}, nodeByID: map[int64]*Node{}}
}

// Load reads the whole desired state. box may be nil (keys then stay empty).
func Load(ctx context.Context, q *db.Queries, box *secrets.Box) (*Snapshot, error) {
	s := newSnapshot()
	nodes, err := q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		nn := &Node{Node: n}
		_ = json.Unmarshal([]byte(n.Features), &nn.Features)
		s.Nodes = append(s.Nodes, nn)
		s.nodeByID[n.ID] = nn
	}
	addrs, err := q.ListNodeAddresses(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if n := s.nodeByID[a.NodeID]; n != nil {
			n.Addresses = append(n.Addresses, a)
		}
	}
	hosts, err := q.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		x := &Host{Host: h, Labels: labels(h.Labels)}
		s.Hosts = append(s.Hosts, x)
		s.hostByID[h.ID] = x
	}
	certs, err := q.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range certs {
		x := &Cert{Certificate: c, Labels: labels(c.Labels)}
		_ = json.Unmarshal([]byte(c.Domains), &x.Domains)
		if box != nil && len(c.KeyPemEnc) > 0 {
			if k, err := box.Open(c.KeyPemEnc); err == nil {
				x.KeyPEM = string(k)
			}
		}
		s.Certs = append(s.Certs, x)
		s.certByID[c.ID] = x
	}
	if s.DNSProviders, err = q.ListDNSProviders(ctx); err != nil {
		return nil, err
	}
	bes, err := q.ListBackends(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bes {
		x := &Backend{Backend: b, Labels: labels(b.Labels)}
		_ = json.Unmarshal([]byte(b.HcStatusCodes), &x.StatusCodes)
		s.Backends = append(s.Backends, x)
		s.beByID[b.ID] = x
	}
	srvs, err := q.ListBackendServers(ctx)
	if err != nil {
		return nil, err
	}
	srvByID := map[int64]*Server{}
	for _, sv := range srvs {
		x := &Server{BackendServer: sv}
		srvByID[sv.ID] = x
		if b := s.beByID[sv.BackendID]; b != nil {
			b.Servers = append(b.Servers, x)
		}
	}
	slots, err := q.ListSlots(ctx)
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		if sv := srvByID[sl.ServerID]; sv != nil {
			sv.Slots = append(sv.Slots, &Slot{ID: sl.ID, ServerID: sl.ServerID, HostID: sl.HostID.Int64, Address: sl.Address})
		}
	}
	fes, err := q.ListFrontends(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range fes {
		x := &Frontend{Frontend: f, Labels: labels(f.Labels)}
		_ = json.Unmarshal([]byte(f.Binds), &x.Binds)
		s.Frontends = append(s.Frontends, x)
		s.feByID[f.ID] = x
	}
	routes, err := q.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if f := s.feByID[r.FrontendID]; f != nil {
			rt := Route{ID: r.ID, PathPrefix: r.PathPrefix, BackendID: r.BackendID}
			_ = json.Unmarshal([]byte(r.Hosts), &rt.Hosts)
			f.Routes = append(f.Routes, rt)
		}
	}
	fcs, err := q.ListFrontendCertificates(ctx)
	if err != nil {
		return nil, err
	}
	for _, fc := range fcs {
		if f := s.feByID[fc.FrontendID]; f != nil {
			f.CertIDs = append(f.CertIDs, fc.CertificateID)
		}
	}
	doms, err := q.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range doms {
		x := &Domain{Domain: d}
		_ = json.Unmarshal([]byte(d.Observed), &x.Observed)
		s.Domains = append(s.Domains, x)
	}
	return s, nil
}

// SlotKey identifies a resolved address of a server; HostID is 0 for IP servers.
type SlotKey struct {
	HostID  int64
	Address string
}

// ResolveServer computes the concrete addresses of a backend server.
func ResolveServer(sv db.BackendServer, hosts []db.Host) ([]SlotKey, error) {
	addr := func(h db.Host) string {
		if sv.UsePrivateIp != 0 && h.PrivateIpv4 != "" {
			return h.PrivateIpv4
		}
		if h.Ipv4 != "" {
			return h.Ipv4
		}
		return h.Ipv6
	}
	switch sv.Type {
	case "ip":
		a, err := netip.ParseAddr(sv.Ip)
		if err != nil {
			return nil, err
		}
		return []SlotKey{{0, a.String()}}, nil
	case "host":
		for _, h := range hosts {
			if h.ID == sv.HostID.Int64 {
				if a := addr(h); a != "" {
					return []SlotKey{{h.ID, a}}, nil
				}
				return nil, nil
			}
		}
		return nil, nil
	case "label_selector":
		sel, err := model.ParseSelector(sv.LabelSelector)
		if err != nil {
			return nil, err
		}
		var out []SlotKey
		for _, h := range hosts {
			if sel.Matches(labels(h.Labels)) {
				if a := addr(h); a != "" {
					out = append(out, SlotKey{h.ID, a})
				}
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown server type %q", sv.Type)
}

// SyncSlots reconciles server_slots with the resolution of every backend server so that
// HAProxy server ids stay stable. Must run inside the write transaction.
func SyncSlots(ctx context.Context, q *db.Queries) error {
	srvs, err := q.ListBackendServers(ctx)
	if err != nil {
		return err
	}
	hosts, err := q.ListHosts(ctx)
	if err != nil {
		return err
	}
	slots, err := q.ListSlots(ctx)
	if err != nil {
		return err
	}
	existing := map[int64]map[SlotKey]int64{}
	for _, sl := range slots {
		m := existing[sl.ServerID]
		if m == nil {
			m = map[SlotKey]int64{}
			existing[sl.ServerID] = m
		}
		m[SlotKey{sl.HostID.Int64, sl.Address}] = sl.ID
	}
	for _, sv := range srvs {
		want, err := ResolveServer(sv, hosts)
		if err != nil {
			return err
		}
		have := existing[sv.ID]
		wantSet := map[SlotKey]bool{}
		for _, k := range want {
			wantSet[k] = true
			if _, ok := have[k]; !ok {
				hid := sql.NullInt64{Int64: k.HostID, Valid: k.HostID != 0}
				if _, err := q.CreateSlot(ctx, db.CreateSlotParams{BackendID: sv.BackendID, ServerID: sv.ID, HostID: hid, Address: k.Address}); err != nil {
					return err
				}
			}
		}
		for k, id := range have {
			if !wantSet[k] {
				if err := q.DeleteSlot(ctx, id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Holder keeps the current snapshot for lock-free API reads.
type Holder struct {
	p  atomicSnap
	mu sync.Mutex // serialises Load+Store so a slower, older load never wins
}

func (h *Holder) Get() *Snapshot { return h.p.Load() }

// Refresh rebuilds the snapshot from the database.
func (h *Holder) Refresh(ctx context.Context, q *db.Queries, box *secrets.Box) (*Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := Load(ctx, q, box)
	if err != nil {
		return nil, err
	}
	h.p.Store(s)
	return s, nil
}

// TestSnapshot builds an indexed snapshot (tests only).
func TestSnapshot(fes []*Frontend, bes []*Backend, certs ...*Cert) *Snapshot {
	s := newSnapshot()
	s.Frontends, s.Backends, s.Certs = fes, bes, certs
	for _, f := range fes {
		s.feByID[f.ID] = f
	}
	for _, b := range bes {
		s.beByID[b.ID] = b
	}
	for _, c := range certs {
		s.certByID[c.ID] = c
	}
	return s
}
