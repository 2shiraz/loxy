package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/libdns/libdns"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/dns"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// DNSProvider decrypts credentials and builds the libdns provider of an account.
func (s *Server) DNSProvider(ctx context.Context, id int64) (dns.Provider, error) {
	row, err := s.Store.Q.GetDNSProvider(ctx, id)
	if err != nil {
		return nil, errNotFound("DNS provider")
	}
	raw, err := s.Box.Open(row.CredentialsEnc)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt DNS credentials (LOXY_SECRET_KEY changed?)")
	}
	creds := map[string]string{}
	_ = json.Unmarshal(raw, &creds)
	return dns.New(row.Kind, creds)
}

func viewProvider(p db.DnsProvider) map[string]any {
	return map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "status": p.Status, "last_error": nullStr(p.LastError.String),
		"configured": len(p.CredentialsEnc) > 0, "created_at": p.CreatedAt}
}

func (s *Server) ListDNSProviders(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, p := range s.Snap.Get().DNSProviders {
		out = append(out, viewProvider(p))
	}
	writeJSON(w, 200, map[string]any{"dns_providers": out, "kinds": dns.Registry})
}

func (s *Server) CreateDNSProvider(w http.ResponseWriter, r *http.Request) {
	var in gen.DNSProviderInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if !model.ValidName(in.Name) {
		fail(w, errField("name", "1–64 printable characters"))
		return
	}
	p, err := dns.New(in.Kind, in.Credentials)
	if err != nil {
		fail(w, errField("credentials", err.Error()))
		return
	}
	// Test the credentials with ListZones (§8.16).
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, lastErr := "ok", sql.NullString{}
	if _, err := p.ListZones(ctx); err != nil {
		status, lastErr = "error", sql.NullString{String: err.Error(), Valid: true}
	}
	creds, _ := json.Marshal(in.Credentials)
	var row db.DnsProvider
	if err := s.mutate(r, "dns_provider.create", "dns_provider", func() int64 { return row.ID }, map[string]any{"name": in.Name, "kind": in.Kind}, func(q *db.Queries) error {
		var err error
		row, err = q.CreateDNSProvider(r.Context(), db.CreateDNSProviderParams{Name: in.Name, Kind: in.Kind, CredentialsEnc: s.Box.Seal(creds),
			Status: status, LastError: lastErr, LastCheckedAt: sql.NullInt64{Int64: nowMs(), Valid: true}, CreatedAt: nowMs()})
		return err
	}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"dns_provider": viewProvider(row)})
}

func (s *Server) DeleteDNSProvider(w http.ResponseWriter, r *http.Request, id gen.Id) {
	for _, c := range s.Snap.Get().Certs {
		if c.DnsProviderID.Valid && c.DnsProviderID.Int64 == id && c.Challenge == "dns01" {
			fail(w, errConflict(fmt.Sprintf("certificate %q uses this provider for DNS-01", c.Name)))
			return
		}
	}
	if err := s.mutate(r, "dns_provider.delete", "dns_provider", func() int64 { return id }, nil, func(q *db.Queries) error {
		return q.DeleteDNSProvider(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) ListDNSZones(w http.ResponseWriter, r *http.Request, id gen.Id) {
	p, err := s.DNSProvider(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	zones, err := p.ListZones(ctx)
	if err != nil {
		writeErr(w, 502, "internal", "provider: "+err.Error(), nil)
		return
	}
	out := []map[string]any{}
	for _, z := range zones {
		out = append(out, map[string]any{"name": strings.TrimSuffix(z.Name, ".")})
	}
	writeJSON(w, 200, map[string]any{"zones": out})
}

// managedSet returns "TYPE|name|value" keys of records Loxy manages in a zone.
func (s *Server) managedSet(zone string) map[string]bool {
	m := map[string]bool{}
	for _, d := range s.Snap.Get().Domains {
		if strings.TrimSuffix(d.Zone, ".") != strings.TrimSuffix(zone, ".") {
			continue
		}
		var ids []string
		_ = json.Unmarshal([]byte(d.ManagedRecordIds), &ids)
		for _, k := range ids {
			m[k] = true
		}
	}
	return m
}

func (s *Server) ListDNSRecords(w http.ResponseWriter, r *http.Request, id gen.Id, zone string) {
	p, err := s.DNSProvider(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	recs, err := dns.ListRecords(ctx, p, zone)
	if err != nil {
		writeErr(w, 502, "internal", "provider: "+err.Error(), nil)
		return
	}
	managed := s.managedSet(zone)
	for i := range recs {
		recs[i].Managed = managed[recs[i].Type+"|"+recs[i].Name+"|"+recs[i].Value]
	}
	writeJSON(w, 200, map[string]any{"records": recs})
}

func (s *Server) CreateDNSRecord(w http.ResponseWriter, r *http.Request, id gen.Id, zone string) {
	p, err := s.DNSProvider(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	var in dns.Record
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	existing, err := dns.ListRecords(ctx, p, zone)
	if err != nil {
		writeErr(w, 502, "internal", "provider: "+err.Error(), nil)
		return
	}
	rec, err := dns.ToLibdns(in, existing)
	if err != nil {
		field, msg, _ := strings.Cut(err.Error(), ": ")
		fail(w, errField(field, msg))
		return
	}
	got, err := p.AppendRecords(ctx, strings.TrimSuffix(zone, ".")+".", []libdns.Record{rec})
	if err != nil {
		writeErr(w, 502, "internal", "provider: "+err.Error(), nil)
		return
	}
	s.audit(r, "dns_record.create", "dns_provider", id, in)
	out := in
	if len(got) > 0 {
		out = dns.FromLibdns(got[0])
	}
	writeJSON(w, 201, map[string]any{"record": out})
}

func (s *Server) DeleteDNSRecord(w http.ResponseWriter, r *http.Request, id gen.Id, zone string) {
	p, err := s.DNSProvider(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	var in dns.Record
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	rec, err := dns.ToLibdns(in, nil)
	if err != nil {
		field, msg, _ := strings.Cut(err.Error(), ": ")
		fail(w, errField(field, msg))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := p.DeleteRecords(ctx, strings.TrimSuffix(zone, ".")+".", []libdns.Record{rec}); err != nil {
		writeErr(w, 502, "internal", "provider: "+err.Error(), nil)
		return
	}
	s.audit(r, "dns_record.delete", "dns_provider", id, in)
	w.WriteHeader(204)
}

// ---------- domains ----------

func (s *Server) domain(id int64) *state.Domain {
	for _, d := range s.Snap.Get().Domains {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (s *Server) ListDomains(w http.ResponseWriter, r *http.Request, prm gen.ListDomainsParams) {
	out := []map[string]any{}
	for _, d := range s.Snap.Get().Domains {
		if prm.ProjectId != nil && (!d.BalancerID.Valid || d.BalancerID.Int64 != *prm.ProjectId) {
			continue
		}
		out = append(out, s.viewDomain(d))
	}
	writeJSON(w, 200, map[string]any{"domains": out})
}

func (s *Server) GetDomain(w http.ResponseWriter, r *http.Request, id gen.Id) {
	d := s.domain(id)
	if d == nil {
		fail(w, errNotFound("domain"))
		return
	}
	writeJSON(w, 200, map[string]any{"domain": s.viewDomain(d)})
}

func (s *Server) respondDomain(w http.ResponseWriter, status int, id int64) {
	d := s.domain(id)
	if d == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"domain": s.viewDomain(d)})
}

// domainRefs validates project / service / provider references; returns the detected zone.
func (s *Server) domainRefs(ctx context.Context, fqdn string, in gen.DomainInput, fe model.FieldErrors) string {
	snap := s.Snap.Get()
	if in.ProjectId == nil || *in.ProjectId == 0 {
		fe.Add("project_id", "required")
	} else if p := snap.Project(*in.ProjectId); p == nil {
		fe.Add("project_id", "unknown project")
	} else if in.ServiceId != nil && *in.ServiceId != 0 {
		if sv := p.Service(*in.ServiceId); sv == nil {
			fe.Add("service_id", "not a service of this project")
		} else if sv.Protocol == "tcp" {
			fe.Add("service_id", "domains route HTTP traffic; choose an http or https service")
		}
	} else if p.WebService() == nil {
		fe.Add("project_id", "the project needs an http or https service to receive domain traffic")
	}
	zone := ""
	if in.DnsProviderId != nil && *in.DnsProviderId != 0 {
		p, err := s.DNSProvider(ctx, *in.DnsProviderId)
		if err != nil {
			fe.Add("dns_provider_id", "unknown DNS provider")
		} else {
			c, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if z, err := dns.FindZone(c, p, fqdn); err == nil {
				zone = strings.TrimSuffix(z, ".")
			} else {
				fe.Add("dns_provider_id", err.Error())
			}
		}
	}
	if in.AutoRecords != nil && *in.AutoRecords && (in.DnsProviderId == nil || *in.DnsProviderId == 0) {
		fe.Add("auto_records", "requires a DNS provider")
	}
	return zone
}

func (s *Server) CreateDomain(w http.ResponseWriter, r *http.Request) {
	var in gen.DomainInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	fe := model.FieldErrors{}
	fqdn := ""
	if in.Fqdn != nil {
		fqdn = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(*in.Fqdn)), ".")
	}
	hasProvider := in.DnsProviderId != nil && *in.DnsProviderId != 0
	if !model.ValidHostname(fqdn, hasProvider) || !strings.Contains(fqdn, ".") {
		fe.Add("fqdn", "invalid hostname (wildcards need a DNS provider)")
	}
	for _, o := range s.Snap.Get().Domains {
		if o.Fqdn == fqdn {
			fe.Add("fqdn", "already added")
		}
	}
	zone := ""
	if fe.Empty() {
		zone = s.domainRefs(r.Context(), fqdn, in, fe)
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	https := in.Https == nil || *in.Https
	var row db.Domain
	if err := s.mutate(r, "domain.create", "domain", func() int64 { return row.ID }, in, func(q *db.Queries) error {
		var err error
		row, err = q.CreateDomain(r.Context(), db.CreateDomainParams{Fqdn: fqdn, BalancerID: i64(in.ProjectId), ServiceID: i64(in.ServiceId),
			Https: b2i(https), DnsProviderID: i64(in.DnsProviderId), Zone: zone, AutoRecords: b2i(in.AutoRecords != nil && *in.AutoRecords),
			CreatedAt: nowMs(), UpdatedAt: nowMs()})
		return err
	}); err != nil {
		fail(w, err)
		return
	}
	s.SyncDomain(r.Context(), row.ID)
	s.VerifyDomainNow(r.Context(), row.ID)
	s.respondDomain(w, 201, row.ID)
}

func (s *Server) UpdateDomain(w http.ResponseWriter, r *http.Request, id gen.Id) {
	d := s.domain(id)
	if d == nil {
		fail(w, errNotFound("domain"))
		return
	}
	var in gen.DomainInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	merged := gen.DomainInput{ProjectId: nullI64(d.BalancerID), ServiceId: nullI64(d.ServiceID), Https: ptr(d.Https != 0),
		DnsProviderId: nullI64(d.DnsProviderID), AutoRecords: ptr(d.AutoRecords != 0)}
	if in.ProjectId != nil {
		merged.ProjectId = in.ProjectId
		if in.ServiceId == nil && (d.BalancerID.Int64 != *in.ProjectId) {
			merged.ServiceId = nil // the old service belongs to the other project
		}
	}
	if in.ServiceId != nil {
		merged.ServiceId = in.ServiceId
		if *in.ServiceId == 0 {
			merged.ServiceId = nil
		}
	}
	if in.Https != nil {
		merged.Https = in.Https
	}
	if in.DnsProviderId != nil {
		merged.DnsProviderId = in.DnsProviderId
	}
	if in.AutoRecords != nil {
		merged.AutoRecords = in.AutoRecords
	}
	fe := model.FieldErrors{}
	zone := s.domainRefs(r.Context(), d.Fqdn, merged, fe)
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	if err := s.mutate(r, "domain.update", "domain", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateDomain(r.Context(), db.UpdateDomainParams{BalancerID: i64(merged.ProjectId), ServiceID: i64(merged.ServiceId),
			Https: b2i(merged.Https != nil && *merged.Https), DnsProviderID: i64(merged.DnsProviderId), Zone: zone,
			AutoRecords: b2i(merged.AutoRecords != nil && *merged.AutoRecords), UpdatedAt: nowMs(), ID: id})
	}); err != nil {
		fail(w, err)
		return
	}
	// d is the pre-update copy: it still lists the records Loxy created.
	if d.AutoRecords != 0 && (merged.AutoRecords == nil || !*merged.AutoRecords) {
		s.removeManagedRecords(r.Context(), d)
	}
	s.SyncDomain(r.Context(), id)
	s.VerifyDomainNow(r.Context(), id)
	s.respondDomain(w, 200, id)
}

func (s *Server) DeleteDomain(w http.ResponseWriter, r *http.Request, id gen.Id) {
	d := s.domain(id)
	if d == nil {
		fail(w, errNotFound("domain"))
		return
	}
	s.removeManagedRecords(r.Context(), d)
	if err := s.mutate(r, "domain.delete", "domain", func() int64 { return id }, map[string]any{"fqdn": d.Fqdn}, func(q *db.Queries) error {
		return q.DeleteDomain(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) VerifyDomain(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if s.domain(id) == nil {
		fail(w, errNotFound("domain"))
		return
	}
	s.VerifyDomainNow(r.Context(), id)
	s.respondDomain(w, 200, id)
}

// VerifyDomainNow resolves the name, stores the verification status (§8.9) and, once the
// domain resolves to the server (or has a DNS provider), issues its certificate automatically.
func (s *Server) VerifyDomainNow(ctx context.Context, id int64) {
	d := s.domain(id)
	if d == nil {
		return
	}
	v4, v6 := s.domainAddresses(d)
	res := dns.Verify(ctx, nil, d.Fqdn, v4, v6)
	if !d.BalancerID.Valid {
		res.Status, res.Detail = "unknown", "not attached to a project"
	}
	obs, _ := json.Marshal(map[string]any{"a": res.A, "aaaa": res.AAAA})
	_ = s.Store.Q.SetDomainStatus(ctx, db.SetDomainStatusParams{Status: res.Status, StatusDetail: sql.NullString{String: res.Detail, Valid: res.Detail != ""},
		Observed: string(obs), CheckedAt: sql.NullInt64{Int64: nowMs(), Valid: true}, ID: id})
	if res.Status != d.Status {
		sev := "info"
		if res.Status != "ok" {
			sev = "warning"
		}
		s.Hub.Publish(events.Event{Kind: "domain.status.changed", ProjectID: domainProject(d), Severity: sev, Message: fmt.Sprintf("%s: %s %s", d.Fqdn, res.Status, res.Detail),
			Data: map[string]any{"domain_id": id, "status": res.Status}})
	}
	_, _ = s.Snap.Refresh(ctx, s.Store.Q, s.Box)
	if d.Https != 0 && !d.CertificateID.Valid && d.BalancerID.Valid && (res.Status == "ok" || d.DnsProviderID.Valid) {
		if err := s.autoIssue(ctx, d); err != nil {
			slog.Warn("automatic certificate", "domain", d.Fqdn, "err", err)
		}
	}
}

// autoIssue creates and starts issuing the managed certificate of an HTTPS domain (system action).
func (s *Server) autoIssue(ctx context.Context, d *state.Domain) error {
	challenge := "http01"
	var pid sql.NullInt64
	if d.DnsProviderID.Valid {
		challenge, pid = "dns01", d.DnsProviderID
	}
	var certID int64
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		c, err := q.CreateCertificate(ctx, db.CreateCertificateParams{Name: d.Fqdn, Type: "managed", Domains: jsonStr([]string{d.Fqdn}), KeyType: "ecdsa256",
			Challenge: challenge, DnsProviderID: pid, Status: "pending", Labels: "{}", CreatedAt: nowMs()})
		if err != nil {
			return err
		}
		certID = c.ID
		if err := q.SetDomainCertificate(ctx, db.SetDomainCertificateParams{CertificateID: sql.NullInt64{Int64: certID, Valid: true}, ID: d.ID}); err != nil {
			return err
		}
		if _, err := q.BumpDesiredVersion(ctx, 1); err != nil {
			return err
		}
		return q.CreateAudit(ctx, db.CreateAuditParams{Ts: nowMs(), Actor: "system", Action: "domain.issue_certificate",
			ResourceType: "domain", ResourceID: sql.NullInt64{Int64: d.ID, Valid: true}, After: sql.NullString{String: jsonStr(map[string]any{"certificate_id": certID}), Valid: true}})
	})
	if err != nil {
		return err
	}
	s.Changed(ctx)
	if s.Issuer != nil {
		s.Issuer.IssueAsync(certID)
	}
	return nil
}

func (s *Server) IssueDomainCertificate(w http.ResponseWriter, r *http.Request, id gen.Id) {
	d := s.domain(id)
	if d == nil {
		fail(w, errNotFound("domain"))
		return
	}
	if d.CertificateID.Valid {
		// Re-issue the existing managed certificate (renew now).
		if c := s.Snap.Get().Cert(d.CertificateID.Int64); c != nil && c.Type == "managed" {
			s.Issuer.IssueAsync(c.ID)
			s.respondCert(w, 202, c.ID)
			return
		}
	}
	challenge := "http01"
	var provider *int64
	if d.DnsProviderID.Valid {
		challenge, provider = "dns01", ptr(d.DnsProviderID.Int64)
	} else if d.Status != "ok" {
		fail(w, errField("status", "the domain must resolve to this server first (or attach a DNS provider for DNS-01)"))
		return
	}
	certID, err := s.createManaged(r, d.Fqdn, []string{d.Fqdn}, challenge, provider, "ecdsa256", map[string]string{})
	if err != nil {
		fail(w, err)
		return
	}
	_ = s.Store.Q.SetDomainCertificate(r.Context(), db.SetDomainCertificateParams{CertificateID: sql.NullInt64{Int64: certID, Valid: true}, ID: id})
	s.Changed(r.Context())
	s.Issuer.IssueAsync(certID)
	s.respondCert(w, 202, certID)
}

// SyncDomain creates/updates the A/AAAA records of an auto-managed domain (Phase 2 part of §8.16).
// Only records Loxy created are touched; pre-existing records are reported as a conflict.
func (s *Server) SyncDomain(ctx context.Context, id int64) {
	d := s.domain(id)
	if d == nil || d.AutoRecords == 0 || !d.DnsProviderID.Valid || !d.BalancerID.Valid || d.Zone == "" {
		return
	}
	p, err := s.DNSProvider(ctx, d.DnsProviderID.Int64)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	zone := d.Zone + "."
	name := libdns.RelativeName(d.Fqdn+".", zone)
	if name == "" {
		name = "@"
	}
	v4, v6 := s.domainAddresses(d)
	want := dns.AddressRecords(name, v4, v6, int(d.Ttl))
	var managed []string
	_ = json.Unmarshal([]byte(d.ManagedRecordIds), &managed)
	mset := map[string]bool{}
	for _, k := range managed {
		mset[k] = true
	}
	existing, err := dns.ListRecords(ctx, p, zone)
	if err != nil {
		s.Hub.Publish(events.Event{Kind: "domain.sync.failed", ProjectID: domainProject(d), Severity: "warning", Message: d.Fqdn + ": " + err.Error()})
		return
	}
	wantKeys := map[string]bool{}
	for _, rec := range want {
		wantKeys[rec.Type+"|"+rec.Name+"|"+rec.Value] = true
	}
	var toDelete, toAdd []libdns.Record
	for _, e := range existing {
		if e.Name != name || (e.Type != "A" && e.Type != "AAAA") {
			continue
		}
		k := e.Type + "|" + e.Name + "|" + e.Value
		if wantKeys[k] {
			delete(wantKeys, k)
			mset[k] = true
			continue
		}
		if !mset[k] {
			s.Hub.Publish(events.Event{Kind: "domain.sync.failed", ProjectID: domainProject(d), Severity: "warning",
				Message: fmt.Sprintf("%s: existing %s record %s was not created by Loxy; not overwriting", d.Fqdn, e.Type, e.Value)})
			return
		}
		if rr, err := dns.ToLibdns(e, nil); err == nil {
			toDelete = append(toDelete, rr)
		}
		delete(mset, k)
	}
	for _, rec := range want {
		k := rec.Type + "|" + rec.Name + "|" + rec.Value
		if wantKeys[k] {
			if rr, err := dns.ToLibdns(rec, nil); err == nil {
				toAdd = append(toAdd, rr)
				mset[k] = true
			}
		}
	}
	if len(toDelete) > 0 {
		if _, err := p.DeleteRecords(ctx, zone, toDelete); err != nil {
			s.Hub.Publish(events.Event{Kind: "domain.sync.failed", ProjectID: domainProject(d), Severity: "warning", Message: d.Fqdn + ": " + err.Error()})
			return
		}
	}
	if len(toAdd) > 0 {
		if _, err := p.AppendRecords(ctx, zone, toAdd); err != nil {
			s.Hub.Publish(events.Event{Kind: "domain.sync.failed", ProjectID: domainProject(d), Severity: "warning", Message: d.Fqdn + ": " + err.Error()})
			return
		}
	}
	keys := []string{}
	for k := range mset {
		keys = append(keys, k)
	}
	_ = s.Store.Q.SetDomainManagedRecords(ctx, db.SetDomainManagedRecordsParams{ManagedRecordIds: jsonStr(keys), ID: id})
	_, _ = s.Snap.Refresh(ctx, s.Store.Q, s.Box)
}

func (s *Server) removeManagedRecords(ctx context.Context, d *state.Domain) {
	var managed []string
	_ = json.Unmarshal([]byte(d.ManagedRecordIds), &managed)
	if len(managed) == 0 || !d.DnsProviderID.Valid || d.Zone == "" {
		return
	}
	p, err := s.DNSProvider(ctx, d.DnsProviderID.Int64)
	if err != nil {
		return
	}
	var recs []libdns.Record
	for _, k := range managed {
		parts := strings.SplitN(k, "|", 3)
		if len(parts) == 3 {
			if rr, err := dns.ToLibdns(dns.Record{Type: parts[0], Name: parts[1], Value: parts[2]}, nil); err == nil {
				recs = append(recs, rr)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := p.DeleteRecords(ctx, d.Zone+".", recs); err == nil {
		_ = s.Store.Q.SetDomainManagedRecords(ctx, db.SetDomainManagedRecordsParams{ManagedRecordIds: "[]", ID: d.ID})
	}
}

// RunDomainLoop re-verifies domains (5 min unverified, 1 h verified) and keeps auto records in sync.
func (s *Server) RunDomainLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	lastIPs := map[int64]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		for _, d := range s.Snap.Get().Domains {
			a, b := s.domainAddresses(d)
			ips := strings.Join(a, ",") + "|" + strings.Join(b, ",")
			changed := lastIPs[d.ID] != ips
			lastIPs[d.ID] = ips
			if changed && d.AutoRecords != 0 {
				s.SyncDomain(ctx, d.ID)
			}
			every := 5 * time.Minute
			if d.Status == "ok" {
				every = time.Hour
			}
			if changed || !d.CheckedAt.Valid || now.Sub(time.UnixMilli(d.CheckedAt.Int64)) > every {
				s.VerifyDomainNow(ctx, d.ID)
			}
		}
	}
}

// domainAddresses returns the addresses the domain should resolve to: the server's
// addresses once it is attached to a project.
func (s *Server) domainAddresses(d *state.Domain) (v4, v6 []string) {
	if !d.BalancerID.Valid {
		return nil, nil
	}
	return s.nodeAddresses()
}

// domainProject is the project a domain's events belong to (nil when unattached).
func domainProject(d *state.Domain) *int64 {
	if !d.BalancerID.Valid {
		return nil
	}
	return events.I64(d.BalancerID.Int64)
}
