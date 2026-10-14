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

	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/store/db"
)

type Plan = db.Plan

type Node struct {
	db.Node
	Features []string
	IPs      []*IP
}

type IP struct {
	db.IpAddress
}

type Server struct {
	db.Server
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

type Service struct {
	db.Service
	StatusCodes []string
	CertIDs     []int64
}

type Slot struct {
	ID       int64
	TargetID int64
	ServerID int64 // 0 for IP targets
	Address  string
}

type Target struct {
	db.Target
	Slots []*Slot
}

type LBNet struct {
	NetworkID int64
	IP        string
}

type LB struct {
	db.LoadBalancer
	Labels   map[string]string
	Plan     *Plan
	IPv4     string
	IPv6     string
	Networks []LBNet
	Services []*Service
	Targets  []*Target
}

// Slots returns all resolved backend servers of the LB, sorted by id.
func (lb *LB) Slots() []*Slot {
	var out []*Slot
	for _, t := range lb.Targets {
		out = append(out, t.Slots...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (lb *LB) Target(id int64) *Target {
	for _, t := range lb.Targets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (lb *LB) Service(id int64) *Service {
	for _, s := range lb.Services {
		if s.ID == id {
			return s
		}
	}
	return nil
}

type Domain struct {
	db.Domain
	Observed struct {
		A    []string `json:"a"`
		AAAA []string `json:"aaaa"`
	}
}

type Snapshot struct {
	Plans        []*Plan
	Nodes        []*Node
	LBs          []*LB
	Servers      []*Server
	Certs        []*Cert
	Networks     []db.Network
	Domains      []*Domain
	DNSProviders []db.DnsProvider

	planByID   map[int64]*Plan
	lbByID     map[int64]*LB
	certByID   map[int64]*Cert
	serverByID map[int64]*Server
	nodeByID   map[int64]*Node
}

func (s *Snapshot) LB(id int64) *LB         { return s.lbByID[id] }
func (s *Snapshot) Plan(id int64) *Plan     { return s.planByID[id] }
func (s *Snapshot) Cert(id int64) *Cert     { return s.certByID[id] }
func (s *Snapshot) Server(id int64) *Server { return s.serverByID[id] }
func (s *Snapshot) Node(id int64) *Node     { return s.nodeByID[id] }

// LBsOnNode returns LBs placed on a node, sorted by id.
func (s *Snapshot) LBsOnNode(nodeID int64) []*LB {
	var out []*LB
	for _, lb := range s.LBs {
		if lb.NodeID == nodeID {
			out = append(out, lb)
		}
	}
	return out
}

func labels(js string) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(js), &m)
	return m
}

// Load reads the whole desired state. box may be nil (keys then stay empty).
func Load(ctx context.Context, q *db.Queries, box *secrets.Box) (*Snapshot, error) {
	s := &Snapshot{planByID: map[int64]*Plan{}, lbByID: map[int64]*LB{}, certByID: map[int64]*Cert{}, serverByID: map[int64]*Server{}, nodeByID: map[int64]*Node{}}
	plans, err := q.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	for i := range plans {
		p := plans[i]
		s.Plans = append(s.Plans, &p)
		s.planByID[p.ID] = &p
	}
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
	ips, err := q.ListIPs(ctx)
	if err != nil {
		return nil, err
	}
	ipByID := map[int64]*IP{}
	for _, ip := range ips {
		x := &IP{ip}
		ipByID[ip.ID] = x
		if n := s.nodeByID[ip.NodeID]; n != nil {
			n.IPs = append(n.IPs, x)
		}
	}
	servers, err := q.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, sv := range servers {
		x := &Server{Server: sv, Labels: labels(sv.Labels)}
		s.Servers = append(s.Servers, x)
		s.serverByID[sv.ID] = x
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
	if s.Networks, err = q.ListNetworks(ctx); err != nil {
		return nil, err
	}
	if s.DNSProviders, err = q.ListDNSProviders(ctx); err != nil {
		return nil, err
	}
	lbs, err := q.ListLoadBalancers(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range lbs {
		lb := &LB{LoadBalancer: l, Labels: labels(l.Labels), Plan: s.planByID[l.PlanID]}
		if ip := ipByID[l.Ipv4ID.Int64]; l.Ipv4ID.Valid && ip != nil {
			lb.IPv4 = ip.Address
		}
		if ip := ipByID[l.Ipv6ID.Int64]; l.Ipv6ID.Valid && ip != nil {
			lb.IPv6 = ip.Address
		}
		s.LBs = append(s.LBs, lb)
		s.lbByID[l.ID] = lb
	}
	nets, err := q.ListLBNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nets {
		if lb := s.lbByID[n.LbID]; lb != nil {
			lb.Networks = append(lb.Networks, LBNet{NetworkID: n.NetworkID, IP: n.Ip})
		}
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
		if lb := s.lbByID[sv.LbID]; lb != nil {
			lb.Services = append(lb.Services, x)
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
	targets, err := q.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	tByID := map[int64]*Target{}
	for _, t := range targets {
		x := &Target{Target: t}
		tByID[t.ID] = x
		if lb := s.lbByID[t.LbID]; lb != nil {
			lb.Targets = append(lb.Targets, x)
		}
	}
	slots, err := q.ListSlots(ctx)
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		if t := tByID[sl.TargetID]; t != nil {
			t.Slots = append(t.Slots, &Slot{ID: sl.ID, TargetID: sl.TargetID, ServerID: sl.ServerID.Int64, Address: sl.Address})
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

// ResolveAddresses computes the concrete backend addresses for a target.
// Key is (serverID, address); serverID is 0 for IP targets.
type SlotKey struct {
	ServerID int64
	Address  string
}

func ResolveTarget(t db.Target, servers []db.Server) ([]SlotKey, error) {
	addr := func(sv db.Server) string {
		if t.UsePrivateIp != 0 && sv.PrivateIpv4 != "" {
			return sv.PrivateIpv4
		}
		if sv.PublicIpv4 != "" {
			return sv.PublicIpv4
		}
		return sv.PublicIpv6
	}
	switch t.Type {
	case "ip":
		a, err := netip.ParseAddr(t.Ip)
		if err != nil {
			return nil, err
		}
		return []SlotKey{{0, a.String()}}, nil
	case "server":
		for _, sv := range servers {
			if sv.ID == t.ServerID.Int64 {
				if a := addr(sv); a != "" {
					return []SlotKey{{sv.ID, a}}, nil
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
		for _, sv := range servers {
			if sel.Matches(labels(sv.Labels)) {
				if a := addr(sv); a != "" {
					out = append(out, SlotKey{sv.ID, a})
				}
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown target type %q", t.Type)
}

// SyncSlots reconciles server_slots rows of all targets with their resolution so that
// HAProxy server ids stay stable. Must run inside the write transaction.
func SyncSlots(ctx context.Context, q *db.Queries) error {
	targets, err := q.ListTargets(ctx)
	if err != nil {
		return err
	}
	servers, err := q.ListServers(ctx)
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
		m[SlotKey{sl.ServerID.Int64, sl.Address}] = sl.ID
	}
	for _, t := range targets {
		want, err := ResolveTarget(t, servers)
		if err != nil {
			return err
		}
		have := existing[t.ID]
		wantSet := map[SlotKey]bool{}
		for _, k := range want {
			wantSet[k] = true
			if _, ok := have[k]; !ok {
				sid := sql.NullInt64{Int64: k.ServerID, Valid: k.ServerID != 0}
				if _, err := q.CreateSlot(ctx, db.CreateSlotParams{LbID: t.LbID, TargetID: t.ID, ServerID: sid, Address: k.Address}); err != nil {
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
	p atomicSnap
}

func (h *Holder) Get() *Snapshot { return h.p.Load() }

// Refresh rebuilds the snapshot from the database.
func (h *Holder) Refresh(ctx context.Context, q *db.Queries, box *secrets.Box) (*Snapshot, error) {
	s, err := Load(ctx, q, box)
	if err != nil {
		return nil, err
	}
	h.p.Store(s)
	return s, nil
}
