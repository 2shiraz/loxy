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

// Server is one target of a project as rendered into a service's backend, with its
// resolved slots. The same targets are shared by all services of a project.
type Server struct {
	db.Target
	Slots []*Slot
}

// PortOr returns the target's port override or def (the service destination port).
func (s *Server) PortOr(def int64) int64 {
	if s.Port.Valid && s.Port.Int64 > 0 {
		return s.Port.Int64
	}
	return def
}

// BackendCfg is the HAProxy backend a service compiles to (`be_<service id>`).
type BackendCfg struct {
	ID                    int64
	BalancerID            int64
	NodeID                int64
	Name                  string
	Mode                  string
	Algorithm             string
	Port                  int64
	StickyEnabled         int64
	StickyCookieName      string
	StickyCookieLifetimeS int64
	ProxyProtocol         string
	TimeoutServerS        int64
	HcProtocol            string
	HcPort                int64
	HcIntervalS           int64
	HcTimeoutS            int64
	HcRetries             int64
	HcDomain              string
	HcPath                string
	HcResponse            string
	HcTlsVerify           int64
}

type Backend struct {
	BackendCfg
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

// FrontendCfg is the HAProxy frontend a service compiles to (`fe_<service id>`).
type FrontendCfg struct {
	ID               int64
	BalancerID       int64
	NodeID           int64
	Name             string
	Mode             string
	Tls              int64
	DefaultBackendID sql.NullInt64
	TimeoutClientS   int64
	Maxconn          int64
	ForwardedHeaders int64
	Enabled          int64
	Maintenance      int64
}

type Frontend struct {
	FrontendCfg
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

// Project is one load balancer (table balancers): targets, services and domains.
type Project struct {
	db.Balancer
	Labels   map[string]string
	Services []*Service
	Targets  []*Server
}

// Service is a port a project listens on.
type Service struct {
	db.Service
	StatusCodes []string
	CertIDs     []int64
}

// Mode is the HAProxy mode of the service (https terminates TLS in http mode).
func (s *Service) Mode() string {
	if s.Protocol == "tcp" {
		return "tcp"
	}
	return "http"
}

func (p *Project) Service(id int64) *Service {
	for _, s := range p.Services {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (p *Project) Target(id int64) *Server {
	for _, t := range p.Targets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// WebService is the service domains route to by default: the first http/https service.
func (p *Project) WebService() *Service {
	for _, s := range p.Services {
		if s.Protocol != "tcp" {
			return s
		}
	}
	return nil
}

// Slots returns all resolved target addresses of the project, sorted by id.
func (p *Project) Slots() []*Slot {
	var out []*Slot
	for _, t := range p.Targets {
		out = append(out, t.Slots...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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
	Projects     []*Project
	Hosts        []*Host
	Certs        []*Cert
	Domains      []*Domain
	DNSProviders []db.DnsProvider

	// Frontends and Backends are the compiled rendering view: one pair per service.
	Frontends []*Frontend
	Backends  []*Backend

	projByID map[int64]*Project
	svcProj  map[int64]*Project
	feByID   map[int64]*Frontend
	beByID   map[int64]*Backend
	certByID map[int64]*Cert
	hostByID map[int64]*Host
	nodeByID map[int64]*Node
}

func (s *Snapshot) Project(id int64) *Project   { return s.projByID[id] }
func (s *Snapshot) Frontend(id int64) *Frontend { return s.feByID[id] }
func (s *Snapshot) Backend(id int64) *Backend   { return s.beByID[id] }
func (s *Snapshot) Cert(id int64) *Cert         { return s.certByID[id] }
func (s *Snapshot) Host(id int64) *Host         { return s.hostByID[id] }
func (s *Snapshot) Node(id int64) *Node         { return s.nodeByID[id] }

// ServiceProject returns the project owning service id.
func (s *Snapshot) ServiceProject(id int64) *Project { return s.svcProj[id] }

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

// DomainsOf returns the domains attached to a project.
func (s *Snapshot) DomainsOf(projectID int64) []*Domain {
	var out []*Domain
	for _, d := range s.Domains {
		if d.BalancerID.Valid && d.BalancerID.Int64 == projectID {
			out = append(out, d)
		}
	}
	return out
}

// DomainService resolves the service a domain routes to: its explicit service, else the
// project's first http/https service. nil when unattached or the project has none.
func (s *Snapshot) DomainService(d *Domain) *Service {
	if !d.BalancerID.Valid {
		return nil
	}
	p := s.projByID[d.BalancerID.Int64]
	if p == nil {
		return nil
	}
	if d.ServiceID.Valid {
		if sv := p.Service(d.ServiceID.Int64); sv != nil && sv.Protocol != "tcp" {
			return sv
		}
	}
	return p.WebService()
}

func labels(js string) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(js), &m)
	return m
}

func newSnapshot() *Snapshot {
	return &Snapshot{projByID: map[int64]*Project{}, svcProj: map[int64]*Project{}, feByID: map[int64]*Frontend{}, beByID: map[int64]*Backend{},
		certByID: map[int64]*Cert{}, hostByID: map[int64]*Host{}, nodeByID: map[int64]*Node{}}
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
	bals, err := q.ListBalancers(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bals {
		p := &Project{Balancer: b, Labels: labels(b.Labels)}
		s.Projects = append(s.Projects, p)
		s.projByID[b.ID] = p
	}
	svcs, err := q.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	svcByID := map[int64]*Service{}
	for _, sv := range svcs {
		x := &Service{Service: sv}
		_ = json.Unmarshal([]byte(sv.HcStatusCodes), &x.StatusCodes)
		svcByID[sv.ID] = x
		if p := s.projByID[sv.BalancerID]; p != nil {
			p.Services = append(p.Services, x)
			s.svcProj[sv.ID] = p
		}
	}
	scs, err := q.ListServiceCertificates(ctx)
	if err != nil {
		return nil, err
	}
	for _, sc := range scs {
		if sv := svcByID[sc.ServiceID]; sv != nil {
			sv.CertIDs = append(sv.CertIDs, sc.CertificateID)
		}
	}
	tgts, err := q.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	tgtByID := map[int64]*Server{}
	for _, t := range tgts {
		x := &Server{Target: t}
		tgtByID[t.ID] = x
		if p := s.projByID[t.BalancerID]; p != nil {
			p.Targets = append(p.Targets, x)
		}
	}
	slots, err := q.ListSlots(ctx)
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		if t := tgtByID[sl.TargetID]; t != nil {
			t.Slots = append(t.Slots, &Slot{ID: sl.ID, ServerID: sl.TargetID, HostID: sl.HostID.Int64, Address: sl.Address})
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
	for _, p := range s.Projects {
		for _, sv := range p.Services {
			fe, be := Compile(p, sv)
			s.Frontends = append(s.Frontends, fe)
			s.Backends = append(s.Backends, be)
			s.feByID[fe.ID] = fe
			s.beByID[be.ID] = be
		}
	}
	return s, nil
}

// Compile turns one service of a project into the HAProxy frontend/backend pair the renderer
// understands (ids = service id). All services of a project share its targets.
func Compile(p *Project, sv *Service) (*Frontend, *Backend) {
	mode := sv.Mode()
	be := &Backend{BackendCfg: BackendCfg{ID: sv.ID, BalancerID: p.ID, NodeID: p.NodeID, Name: p.Name, Mode: mode, Algorithm: p.Algorithm,
		Port: sv.DestinationPort, StickyEnabled: sv.StickyEnabled, StickyCookieName: sv.StickyCookieName, StickyCookieLifetimeS: sv.StickyCookieLifetimeS,
		ProxyProtocol: sv.ProxyProtocol, TimeoutServerS: sv.TimeoutS, HcProtocol: sv.HcProtocol, HcPort: sv.HcPort, HcIntervalS: sv.HcIntervalS,
		HcTimeoutS: sv.HcTimeoutS, HcRetries: sv.HcRetries, HcDomain: sv.HcDomain, HcPath: sv.HcPath, HcResponse: sv.HcResponse, HcTlsVerify: sv.HcTlsVerify},
		Labels: p.Labels, StatusCodes: sv.StatusCodes, Servers: p.Targets}
	var tls, fwd int64
	if sv.Protocol == "https" {
		tls = 1
	}
	if mode == "http" {
		fwd = 1
	}
	fe := &Frontend{FrontendCfg: FrontendCfg{ID: sv.ID, BalancerID: p.ID, NodeID: p.NodeID, Name: p.Name, Mode: mode, Tls: tls,
		DefaultBackendID: sql.NullInt64{Int64: sv.ID, Valid: true}, TimeoutClientS: sv.TimeoutS, ForwardedHeaders: fwd, Enabled: 1,
		Maintenance: p.Maintenance}, Labels: p.Labels, Binds: []Bind{{Address: "*", Port: sv.ListenPort}}, CertIDs: sv.CertIDs}
	return fe, be
}

// SlotKey identifies a resolved address of a target; HostID is 0 for IP targets.
type SlotKey struct {
	HostID  int64
	Address string
}

// ResolveTarget computes the concrete addresses of a target.
func ResolveTarget(t db.Target, hosts []db.Host) ([]SlotKey, error) {
	addr := func(h db.Host) string {
		if t.UsePrivateIp != 0 && h.PrivateIpv4 != "" {
			return h.PrivateIpv4
		}
		if h.Ipv4 != "" {
			return h.Ipv4
		}
		return h.Ipv6
	}
	switch t.Type {
	case "ip":
		a, err := netip.ParseAddr(t.Ip)
		if err != nil {
			return nil, err
		}
		return []SlotKey{{0, a.String()}}, nil
	case "host":
		for _, h := range hosts {
			if h.ID == t.HostID.Int64 {
				if a := addr(h); a != "" {
					return []SlotKey{{h.ID, a}}, nil
				}
				return nil, nil
			}
		}
		return nil, nil
	case "label_selector":
		sel, err := model.ParseSelector(t.LabelSelector)
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
	return nil, fmt.Errorf("unknown target type %q", t.Type)
}

// SyncSlots reconciles target_slots with the resolution of every target so that HAProxy
// server ids stay stable. Must run inside the write transaction.
func SyncSlots(ctx context.Context, q *db.Queries) error {
	tgts, err := q.ListTargets(ctx)
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
		m := existing[sl.TargetID]
		if m == nil {
			m = map[SlotKey]int64{}
			existing[sl.TargetID] = m
		}
		m[SlotKey{sl.HostID.Int64, sl.Address}] = sl.ID
	}
	for _, t := range tgts {
		want, err := ResolveTarget(t, hosts)
		if err != nil {
			return err
		}
		have := existing[t.ID]
		wantSet := map[SlotKey]bool{}
		for _, k := range want {
			wantSet[k] = true
			if _, ok := have[k]; !ok {
				hid := sql.NullInt64{Int64: k.HostID, Valid: k.HostID != 0}
				if _, err := q.CreateSlot(ctx, db.CreateSlotParams{BalancerID: t.BalancerID, TargetID: t.ID, HostID: hid, Address: k.Address}); err != nil {
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

// TestSnapshot builds an indexed snapshot from rendering objects (tests only).
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

// TestProjects builds an indexed snapshot from projects, compiling their services (tests only).
func TestProjects(ps []*Project, domains []*Domain, certs ...*Cert) *Snapshot {
	s := newSnapshot()
	s.Projects, s.Domains, s.Certs = ps, domains, certs
	for _, c := range certs {
		s.certByID[c.ID] = c
	}
	for _, p := range ps {
		s.projByID[p.ID] = p
		for _, sv := range p.Services {
			s.svcProj[sv.ID] = p
			fe, be := Compile(p, sv)
			s.Frontends = append(s.Frontends, fe)
			s.Backends = append(s.Backends, be)
			s.feByID[fe.ID], s.beByID[be.ID] = fe, be
		}
	}
	return s
}
