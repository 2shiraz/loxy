package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/auth"
	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// ---------- system ----------

func (s *Server) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "version": s.Version})
}

func (s *Server) Readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, 503, "internal", "database: "+err.Error(), nil)
		return
	}
	if s.Metrics != nil && !s.Cfg.NoHAProxy {
		if _, _, last := s.Metrics.Info(); time.Since(last) > 10*time.Second {
			writeErr(w, 503, "internal", "haproxy runtime socket not responding", nil)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"status": "ready", "version": s.Version})
}

func (s *Server) ListPlans(w http.ResponseWriter, r *http.Request) {
	out := []planView{}
	for _, p := range s.Snap.Get().Plans {
		out = append(out, viewPlan(p))
	}
	writeJSON(w, 200, map[string]any{"plans": out})
}

// ---------- auth ----------

func viewUser(u db.User) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "role": u.Role, "created_at": u.CreatedAt, "last_login_at": nullI64(u.LastLoginAt)}
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var in gen.LoginRequest
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	ip := clientIP(r)
	keys := []string{"ip:" + ip, "user:" + email}
	if err := s.Limiter.Allow(keys...); err != nil {
		writeErr(w, 429, "rate_limited", "too many login attempts, try again later", nil)
		return
	}
	u, err := s.Store.Q.GetUserByEmail(r.Context(), email)
	if err != nil || u.Disabled != 0 || !auth.CheckPassword(u.PasswordHash, in.Password) {
		s.Limiter.Fail(keys...)
		writeErr(w, 401, "unauthorized", "invalid email or password", nil)
		return
	}
	s.Limiter.Reset(keys...)
	sid := auth.RandomToken(32)
	now := time.Now()
	if err := s.Store.Q.CreateSession(r.Context(), db.CreateSessionParams{IDHash: auth.HashSessionID(sid), UserID: u.ID, CreatedAt: now.UnixMilli(),
		LastSeenAt: now.UnixMilli(), ExpiresAt: now.Add(auth.AbsoluteTimeout).UnixMilli(), Ip: ip, UserAgent: r.UserAgent()}); err != nil {
		fail(w, err)
		return
	}
	_ = s.Store.Q.TouchUserLogin(r.Context(), db.TouchUserLoginParams{LastLoginAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: u.ID})
	_ = s.Store.Q.DeleteExpiredSessions(r.Context(), now.UnixMilli())
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: sid, Path: "/", HttpOnly: true, Secure: !s.Cfg.Dev,
		SameSite: http.SameSiteStrictMode, MaxAge: int(auth.AbsoluteTimeout.Seconds())})
	_ = s.Store.Q.CreateAudit(r.Context(), db.CreateAuditParams{Ts: now.UnixMilli(), ActorUserID: sql.NullInt64{Int64: u.ID, Valid: true}, Actor: u.Email, Ip: ip,
		Action: "auth.login", ResourceType: "user", ResourceID: sql.NullInt64{Int64: u.ID, Valid: true}})
	writeJSON(w, 200, map[string]any{"user": viewUser(u), "csrf_token": auth.CSRFToken(s.CSRFKey, sid)})
}

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	if p := userFrom(r); p != nil {
		_ = s.Store.Q.DeleteSession(r.Context(), auth.HashSessionID(p.SessionID))
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !s.Cfg.Dev, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(204)
}

func (s *Server) Me(w http.ResponseWriter, r *http.Request) {
	p := userFrom(r)
	writeJSON(w, 200, map[string]any{"user": viewUser(p.User), "csrf_token": auth.CSRFToken(s.CSRFKey, p.SessionID)})
}

func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	p := userFrom(r)
	var in gen.ChangePasswordRequest
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if !auth.CheckPassword(p.User.PasswordHash, in.CurrentPassword) {
		fail(w, errField("current_password", "incorrect"))
		return
	}
	if len(in.NewPassword) < auth.MinPassword {
		fail(w, errField("new_password", fmt.Sprintf("at least %d characters", auth.MinPassword)))
		return
	}
	h, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.Q.SetUserPassword(r.Context(), db.SetUserPasswordParams{PasswordHash: h, ID: p.User.ID}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.Store.Q.ListUsers(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, u := range us {
		out = append(out, viewUser(u))
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

func (s *Server) CreateUser(w http.ResponseWriter, r *http.Request) {
	var in gen.UserCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	fe := model.FieldErrors{}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") || len(email) > 254 {
		fe.Add("email", "invalid email")
	}
	if len(in.Password) < auth.MinPassword {
		fe.Add("password", fmt.Sprintf("at least %d characters", auth.MinPassword))
	}
	role := string(in.Role)
	if role != "admin" && role != "operator" && role != "viewer" {
		fe.Add("role", "must be admin, operator or viewer")
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	h, err := auth.HashPassword(in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	u, err := s.Store.Q.CreateUser(r.Context(), db.CreateUserParams{Email: email, PasswordHash: h, Role: role, CreatedAt: nowMs()})
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "user.create", "user", u.ID, map[string]any{"email": email, "role": role})
	writeJSON(w, 201, map[string]any{"user": viewUser(u)})
}

func (s *Server) DeleteUser(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if p := userFrom(r); p != nil && p.User.ID == id {
		fail(w, errConflict("you cannot delete yourself"))
		return
	}
	_ = s.Store.Q.DeleteUserSessions(r.Context(), id)
	if err := s.Store.Q.DeleteUser(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "user.delete", "user", id, nil)
	w.WriteHeader(204)
}

func (s *Server) audit(r *http.Request, action, resType string, id int64, after any) {
	p := userFrom(r)
	var uid sql.NullInt64
	actor := ""
	if p != nil {
		uid, actor = sql.NullInt64{Int64: p.User.ID, Valid: true}, p.User.Email
	}
	var a sql.NullString
	if after != nil {
		a = sql.NullString{String: jsonStr(after), Valid: true}
	}
	_ = s.Store.Q.CreateAudit(r.Context(), db.CreateAuditParams{Ts: nowMs(), ActorUserID: uid, Actor: actor, Ip: clientIP(r), Action: action,
		ResourceType: resType, ResourceID: sql.NullInt64{Int64: id, Valid: id != 0}, After: a})
}

// ---------- certificates ----------

func (s *Server) ListCertificates(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, c := range s.Snap.Get().Certs {
		out = append(out, s.viewCert(c))
	}
	writeJSON(w, 200, map[string]any{"certificates": out})
}

func (s *Server) GetCertificate(w http.ResponseWriter, r *http.Request, id gen.Id) {
	c := s.Snap.Get().Cert(id)
	if c == nil {
		fail(w, errNotFound("certificate"))
		return
	}
	writeJSON(w, 200, map[string]any{"certificate": s.viewCert(c)})
}

func (s *Server) respondCert(w http.ResponseWriter, status int, id int64) {
	c := s.Snap.Get().Cert(id)
	if c == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, map[string]any{"certificate": s.viewCert(c)})
}

// createManaged inserts a pending managed certificate.
func (s *Server) createManaged(r *http.Request, name string, domains []string, challenge string, providerID *int64, keyType string, labels map[string]string) (int64, error) {
	fe := model.FieldErrors{}
	if !model.ValidName(name) {
		fe.Add("name", "1–64 printable characters")
	}
	if len(domains) == 0 || len(domains) > 100 {
		fe.Add("domains", "1 to 100 domains")
	}
	if challenge == "" {
		challenge = "http01"
	}
	if challenge != "http01" && challenge != "dns01" {
		fe.Add("challenge", "must be http01 or dns01")
	}
	for i, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		domains[i] = d
		if !model.ValidHostname(d, challenge == "dns01") {
			if strings.HasPrefix(d, "*.") {
				fe.Add("domains", "wildcard domains require the DNS-01 challenge")
			} else {
				fe.Add("domains", fmt.Sprintf("invalid domain %q", d))
			}
		}
	}
	var pid sql.NullInt64
	if challenge == "dns01" {
		if providerID == nil {
			fe.Add("dns_provider_id", "DNS-01 requires a connected DNS provider")
		} else {
			pid = sql.NullInt64{Int64: *providerID, Valid: true}
			found := false
			for _, p := range s.Snap.Get().DNSProviders {
				found = found || p.ID == *providerID
			}
			if !found {
				fe.Add("dns_provider_id", "unknown DNS provider")
			}
		}
	}
	if keyType == "" {
		keyType = "ecdsa256"
	}
	if keyType != "ecdsa256" && keyType != "rsa2048" && keyType != "rsa4096" {
		fe.Add("key_type", "must be ecdsa256, rsa2048 or rsa4096")
	}
	model.ValidateLabels(fe, "labels", labels)
	if !fe.Empty() {
		return 0, errInput(fe)
	}
	var id int64
	err := s.mutate(r, "certificate.create", "certificate", func() int64 { return id }, map[string]any{"name": name, "domains": domains}, func(q *db.Queries) error {
		c, err := q.CreateCertificate(r.Context(), db.CreateCertificateParams{Name: name, Type: "managed", Domains: jsonStr(domains), KeyType: keyType,
			Challenge: challenge, DnsProviderID: pid, Status: "pending", Labels: jsonStr(labels), CreatedAt: nowMs()})
		id = c.ID
		return err
	})
	return id, err
}

func (s *Server) CreateCertificate(w http.ResponseWriter, r *http.Request) {
	var in gen.CertificateCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	labels := map[string]string{}
	if in.Labels != nil {
		labels = *in.Labels
	}
	switch in.Type {
	case "managed":
		var domains []string
		if in.Domains != nil {
			domains = *in.Domains
		}
		ch, kt := "", ""
		if in.Challenge != nil {
			ch = string(*in.Challenge)
		}
		if in.KeyType != nil {
			kt = string(*in.KeyType)
		}
		id, err := s.createManaged(r, in.Name, domains, ch, in.DnsProviderId, kt, labels)
		if err != nil {
			fail(w, err)
			return
		}
		s.Issuer.IssueAsync(id)
		s.respondCert(w, 201, id)
	case "uploaded":
		fe := model.FieldErrors{}
		if !model.ValidName(in.Name) {
			fe.Add("name", "1–64 printable characters")
		}
		model.ValidateLabels(fe, "labels", labels)
		if in.Certificate == nil || in.PrivateKey == nil {
			fe.Add("certificate", "certificate and private_key are required")
		}
		if !fe.Empty() {
			fail(w, errInput(fe))
			return
		}
		chain := ""
		if in.Chain != nil {
			chain = *in.Chain
		}
		p, err := certs.Parse(*in.Certificate, chain, *in.PrivateKey, time.Now())
		if err != nil {
			field := "certificate"
			if strings.HasPrefix(err.Error(), "private_key") {
				field = "private_key"
			}
			fail(w, errField(field, err.Error()))
			return
		}
		var id int64
		err = s.mutate(r, "certificate.upload", "certificate", func() int64 { return id }, map[string]any{"name": in.Name, "domains": p.Domains}, func(q *db.Queries) error {
			c, err := q.CreateCertificate(r.Context(), db.CreateCertificateParams{Name: in.Name, Type: "uploaded", Domains: jsonStr(p.Domains), KeyType: "",
				CertPem: p.CertPEM, ChainPem: p.ChainPEM, KeyPemEnc: s.Box.Seal([]byte(p.KeyPEM)), Issuer: p.Issuer, Serial: p.Serial,
				FingerprintSha256: p.Fingerprint, NotBefore: sql.NullInt64{Int64: p.Leaf.NotBefore.UnixMilli(), Valid: true},
				NotAfter: sql.NullInt64{Int64: p.Leaf.NotAfter.UnixMilli(), Valid: true}, Status: "issued", Labels: jsonStr(labels), CreatedAt: nowMs()})
			id = c.ID
			return err
		})
		if err != nil {
			fail(w, err)
			return
		}
		s.respondCert(w, 201, id)
	default:
		fail(w, errField("type", "must be uploaded or managed"))
	}
}

func (s *Server) UpdateCertificate(w http.ResponseWriter, r *http.Request, id gen.Id) {
	c := s.Snap.Get().Cert(id)
	if c == nil {
		fail(w, errNotFound("certificate"))
		return
	}
	var in gen.UpdateCertificateJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	name, labels := c.Name, c.Labels
	fe := model.FieldErrors{}
	if in.Name != nil {
		name = *in.Name
		if !model.ValidName(name) {
			fe.Add("name", "1–64 printable characters")
		}
	}
	if in.Labels != nil {
		labels = *in.Labels
		model.ValidateLabels(fe, "labels", labels)
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	if err := s.mutate(r, "certificate.update", "certificate", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateCertificateMeta(r.Context(), db.UpdateCertificateMetaParams{Name: name, Labels: jsonStr(labels), ID: id})
	}); err != nil {
		fail(w, err)
		return
	}
	s.respondCert(w, 200, id)
}

func (s *Server) DeleteCertificate(w http.ResponseWriter, r *http.Request, id gen.Id) {
	c := s.Snap.Get().Cert(id)
	if c == nil {
		fail(w, errNotFound("certificate"))
		return
	}
	if used := s.viewCert(c)["used_by"].([]int64); len(used) > 0 {
		fail(w, errConflict(fmt.Sprintf("certificate is used by %d service(s)", len(used))))
		return
	}
	if err := s.mutate(r, "certificate.delete", "certificate", func() int64 { return id }, nil, func(q *db.Queries) error {
		return q.DeleteCertificate(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) RenewCertificate(w http.ResponseWriter, r *http.Request, id gen.Id) {
	c := s.Snap.Get().Cert(id)
	if c == nil || c.Type != "managed" {
		fail(w, errNotFound("managed certificate"))
		return
	}
	s.Issuer.IssueAsync(id)
	s.audit(r, "certificate.renew", "certificate", id, nil)
	writeJSON(w, 202, map[string]any{"action": map[string]any{"command": "renew_certificate", "status": "running"}})
}

// ---------- inventory ----------

func viewServer(sv *state.Server) map[string]any {
	return map[string]any{"id": sv.ID, "name": sv.Name, "public_ipv4": nullStr(sv.PublicIpv4), "public_ipv6": nullStr(sv.PublicIpv6),
		"private_ipv4": nullStr(sv.PrivateIpv4), "labels": sv.Labels, "notes": sv.Notes, "source": sv.Source}
}

func (s *Server) ListServers(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, sv := range s.Snap.Get().Servers {
		out = append(out, viewServer(sv))
	}
	writeJSON(w, 200, map[string]any{"servers": out})
}

func serverParams(in gen.ServerInput, base *state.Server) (db.CreateServerParams, error) {
	p := db.CreateServerParams{Labels: "{}"}
	if base != nil {
		p = db.CreateServerParams{Name: base.Name, PublicIpv4: base.PublicIpv4, PublicIpv6: base.PublicIpv6, PrivateIpv4: base.PrivateIpv4, Labels: jsonStr(base.Labels), Notes: base.Notes}
	}
	fe := model.FieldErrors{}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if !model.ValidName(p.Name) {
		fe.Add("name", "1–64 printable characters")
	}
	ip := func(field string, v *string, fam int, dst *string) {
		if v == nil {
			return
		}
		*dst = ""
		if strings.TrimSpace(*v) == "" {
			return
		}
		a, err := model.ParseTargetIP(*v)
		if err != nil {
			fe.Add(field, err.Error())
			return
		}
		if (fam == 4) != a.Is4() {
			fe.Add(field, fmt.Sprintf("must be an IPv%d address", fam))
		}
		*dst = a.String()
	}
	ip("public_ipv4", in.PublicIpv4, 4, &p.PublicIpv4)
	ip("public_ipv6", in.PublicIpv6, 6, &p.PublicIpv6)
	ip("private_ipv4", in.PrivateIpv4, 4, &p.PrivateIpv4)
	if in.Labels != nil {
		model.ValidateLabels(fe, "labels", *in.Labels)
		p.Labels = jsonStr(*in.Labels)
	}
	if in.Notes != nil {
		p.Notes = *in.Notes
		if len(p.Notes) > 2000 {
			fe.Add("notes", "too long")
		}
	}
	if p.PublicIpv4 == "" && p.PublicIpv6 == "" && p.PrivateIpv4 == "" {
		fe.Add("public_ipv4", "at least one address is required")
	}
	if !fe.Empty() {
		return p, errInput(fe)
	}
	return p, nil
}

func (s *Server) CreateServer(w http.ResponseWriter, r *http.Request) {
	var in gen.ServerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := serverParams(in, nil)
	if err != nil {
		fail(w, err)
		return
	}
	var id int64
	if err := s.mutate(r, "server.create", "server", func() int64 { return id }, in, func(q *db.Queries) error {
		sv, err := q.CreateServer(r.Context(), p)
		id = sv.ID
		return err
	}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"server": viewServer(s.Snap.Get().Server(id))})
}

func (s *Server) UpdateServer(w http.ResponseWriter, r *http.Request, id gen.Id) {
	base := s.Snap.Get().Server(id)
	if base == nil {
		fail(w, errNotFound("server"))
		return
	}
	var in gen.ServerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := serverParams(in, base)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.mutate(r, "server.update", "server", func() int64 { return id }, in, func(q *db.Queries) error {
		return q.UpdateServer(r.Context(), db.UpdateServerParams{Name: p.Name, PublicIpv4: p.PublicIpv4, PublicIpv6: p.PublicIpv6, PrivateIpv4: p.PrivateIpv4,
			Labels: p.Labels, Notes: p.Notes, ID: id})
	}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"server": viewServer(s.Snap.Get().Server(id))})
}

func (s *Server) DeleteServer(w http.ResponseWriter, r *http.Request, id gen.Id) {
	for _, lb := range s.Snap.Get().LBs {
		for _, t := range lb.Targets {
			if t.Type == "server" && t.ServerID.Int64 == id {
				fail(w, errConflict(fmt.Sprintf("server is a target of load balancer %q", lb.Name)))
				return
			}
		}
	}
	if err := s.mutate(r, "server.delete", "server", func() int64 { return id }, nil, func(q *db.Queries) error {
		return q.DeleteServer(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) ListNetworks(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, n := range s.Snap.Get().Networks {
		out = append(out, map[string]any{"id": n.ID, "name": n.Name, "cidr": n.Cidr})
	}
	writeJSON(w, 200, map[string]any{"networks": out})
}

func (s *Server) CreateNetwork(w http.ResponseWriter, r *http.Request) {
	var in gen.CreateNetworkJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	fe := model.FieldErrors{}
	if !model.ValidName(in.Name) {
		fe.Add("name", "1–64 printable characters")
	}
	pf, err := netip.ParsePrefix(strings.TrimSpace(in.Cidr))
	if err != nil {
		fe.Add("cidr", "invalid CIDR")
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	var n db.Network
	if err := s.mutate(r, "network.create", "network", func() int64 { return n.ID }, in, func(q *db.Queries) error {
		var err error
		n, err = q.CreateNetwork(r.Context(), db.CreateNetworkParams{Name: in.Name, Cidr: pf.Masked().String()})
		return err
	}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"network": map[string]any{"id": n.ID, "name": n.Name, "cidr": n.Cidr}})
}

func (s *Server) DeleteNetwork(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if err := s.mutate(r, "network.delete", "network", func() int64 { return id }, nil, func(q *db.Queries) error {
		return q.DeleteNetwork(r.Context(), id)
	}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- nodes & revisions ----------

func (s *Server) viewNode(n *state.Node) map[string]any {
	ips := []map[string]any{}
	for _, ip := range n.IPs {
		ips = append(ips, map[string]any{"id": ip.ID, "address": ip.Address, "family": ip.Family, "kind": ip.Kind, "assigned_lb_id": nullI64(ip.AssignedLbID)})
	}
	var running string
	var uptime int64
	reloads := 0
	status := n.Status
	if s.Metrics != nil {
		info, rl, last := s.Metrics.Info()
		reloads = rl
		if info != nil {
			running = info["Version"]
			uptime, _ = strconv.ParseInt(info["Uptime_sec"], 10, 64)
		}
		if time.Since(last) < 5*time.Second {
			status = "online"
		} else {
			status = "offline"
		}
	}
	ver, feats := "", []string{}
	if s.NodeInfo != nil {
		ver, feats = s.NodeInfo()
	}
	return map[string]any{"id": n.ID, "name": n.Name, "status": status, "haproxy_version": ver, "haproxy_running_version": running,
		"loxy_version": s.Version, "features": feats, "uptime_s": uptime, "reloads": reloads,
		"location": map[string]any{"city": n.LocationCity, "country": n.LocationCountry, "network_zone": n.NetworkZone, "lat": n.Lat, "lon": n.Lon},
		"ips":      ips, "desired_version": n.DesiredVersion, "applied_version": n.AppliedVersion}
}

func (s *Server) ListNodes(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, n := range s.Snap.Get().Nodes {
		out = append(out, s.viewNode(n))
	}
	writeJSON(w, 200, map[string]any{"nodes": out})
}

func (s *Server) node(id int64) (*state.Node, error) {
	n := s.Snap.Get().Node(id)
	if n == nil {
		return nil, errNotFound("node")
	}
	return n, nil
}

func (s *Server) UpdateNode(w http.ResponseWriter, r *http.Request, id gen.Id) {
	n, err := s.node(id)
	if err != nil {
		fail(w, err)
		return
	}
	var in gen.NodeUpdate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p := db.UpdateNodeLocationParams{Name: n.Name, LocationCity: n.LocationCity, LocationCountry: n.LocationCountry, NetworkZone: n.NetworkZone, Lat: n.Lat, Lon: n.Lon, ID: id}
	if in.Name != nil {
		if !model.ValidName(*in.Name) {
			fail(w, errField("name", "1–64 printable characters"))
			return
		}
		p.Name = *in.Name
	}
	if l := in.Location; l != nil {
		if l.City != nil {
			p.LocationCity = *l.City
		}
		if l.Country != nil {
			p.LocationCountry = strings.ToUpper(*l.Country)
		}
		if l.NetworkZone != nil {
			p.NetworkZone = *l.NetworkZone
		}
		if l.Lat != nil {
			p.Lat = float64(*l.Lat)
		}
		if l.Lon != nil {
			p.Lon = float64(*l.Lon)
		}
	}
	if err := s.Store.Q.UpdateNodeLocation(r.Context(), p); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "node.update", "node", id, in)
	_, _ = s.Snap.Refresh(r.Context(), s.Store.Q, s.Box)
	writeJSON(w, 200, map[string]any{"node": s.viewNode(s.Snap.Get().Node(id))})
}

func (s *Server) AddNodeIP(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if _, err := s.node(id); err != nil {
		fail(w, err)
		return
	}
	var in gen.AddNodeIPJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	a, err := netip.ParseAddr(strings.TrimSpace(in.Address))
	if err != nil || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() {
		fail(w, errField("address", "invalid address"))
		return
	}
	kind := "additional"
	if in.Kind != nil {
		kind = string(*in.Kind)
	}
	fam := int64(6)
	if a.Is4() {
		fam = 4
	}
	if _, err := s.Store.Q.CreateIP(r.Context(), db.CreateIPParams{NodeID: id, Address: a.String(), Family: fam, Kind: kind}); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "node.ip.add", "node", id, in)
	_, _ = s.Snap.Refresh(r.Context(), s.Store.Q, s.Box)
	writeJSON(w, 201, map[string]any{"node": s.viewNode(s.Snap.Get().Node(id))})
}

func (s *Server) DeleteNodeIP(w http.ResponseWriter, r *http.Request, id gen.Id, ipid int64) {
	n, err := s.node(id)
	if err != nil {
		fail(w, err)
		return
	}
	for _, ip := range n.IPs {
		if ip.ID == ipid && ip.AssignedLbID.Valid {
			fail(w, errConflict("address is assigned to a load balancer"))
			return
		}
	}
	if err := s.Store.Q.DeleteIP(r.Context(), db.DeleteIPParams{ID: ipid, NodeID: id}); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "node.ip.delete", "node", id, map[string]any{"ip_id": ipid})
	_, _ = s.Snap.Refresh(r.Context(), s.Store.Q, s.Box)
	w.WriteHeader(204)
}

func (s *Server) ListRevisions(w http.ResponseWriter, r *http.Request, id gen.Id) {
	revs, err := s.Store.Q.ListRevisions(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, rv := range revs {
		out = append(out, map[string]any{"id": rv.ID, "revision": rv.Revision, "desired_version": rv.DesiredVersion, "kind": rv.Kind, "result": rv.Result,
			"duration_ms": rv.DurationMs, "sha256": rv.Sha256, "haproxy_output": rv.HaproxyOutput, "created_at": rv.CreatedAt, "pinned": rv.Pinned != 0})
	}
	writeJSON(w, 200, map[string]any{"revisions": out})
}

func (s *Server) DiffRevision(w http.ResponseWriter, r *http.Request, id gen.Id, rev int64, p gen.DiffRevisionParams) {
	against := rev - 1
	if p.Against != nil {
		against = *p.Against
	}
	a, errA := s.Rec.FS.ReadFile(against, "haproxy.cfg")
	b, errB := s.Rec.FS.ReadFile(rev, "haproxy.cfg")
	if errB != nil {
		fail(w, errNotFound("revision files (garbage-collected?)"))
		return
	}
	if errA != nil {
		a = nil
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(UnifiedDiff(fmt.Sprintf("revision %d", against), fmt.Sprintf("revision %d", rev), string(a), string(b))))
}

func (s *Server) RollbackRevision(w http.ResponseWriter, r *http.Request, id gen.Id, rev int64) {
	if err := s.Rec.Rollback(r.Context(), rev); err != nil {
		fail(w, &apiError{Status: 422, Code: "apply_failed", Msg: err.Error()})
		return
	}
	s.audit(r, "revision.rollback", "node", id, map[string]any{"revision": rev})
	writeJSON(w, 202, map[string]any{"action": map[string]any{"command": "rollback", "status": "success"}})
}

// ---------- activity ----------

func (s *Server) ListEvents(w http.ResponseWriter, r *http.Request, p gen.ListEventsParams) {
	lim := int64(200)
	if p.Limit != nil && *p.Limit > 0 && *p.Limit <= 1000 {
		lim = int64(*p.Limit)
	}
	arg := db.ListEventsParams{Limit: lim}
	if p.LbId != nil {
		arg.LbID = *p.LbId
	}
	if p.Kind != nil && *p.Kind != "" {
		arg.Kind = *p.Kind
	}
	evs, err := s.Store.Q.ListEvents(r.Context(), arg)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, e := range evs {
		var data map[string]any
		_ = json.Unmarshal([]byte(e.Data), &data)
		out = append(out, map[string]any{"id": e.ID, "ts": e.Ts, "lb_id": nullI64(e.LbID), "kind": e.Kind, "severity": e.Severity, "message": e.Message, "data": data})
	}
	writeJSON(w, 200, map[string]any{"events": out})
}

func (s *Server) ListAudit(w http.ResponseWriter, r *http.Request, p gen.ListAuditParams) {
	lim := int64(200)
	if p.Limit != nil && *p.Limit > 0 && *p.Limit <= 1000 {
		lim = int64(*p.Limit)
	}
	rows, err := s.Store.Q.ListAudit(r.Context(), lim)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, a := range rows {
		var before, after any
		if a.Before.Valid {
			_ = json.Unmarshal([]byte(a.Before.String), &before)
		}
		if a.After.Valid {
			_ = json.Unmarshal([]byte(a.After.String), &after)
		}
		out = append(out, map[string]any{"id": a.ID, "ts": a.Ts, "actor": nullStr(a.Actor), "ip": nullStr(a.Ip), "action": a.Action,
			"resource_type": a.ResourceType, "resource_id": nullI64(a.ResourceID), "before": before, "after": after})
	}
	writeJSON(w, 200, map[string]any{"audit": out})
}

func (s *Server) StreamEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	var since int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		since, _ = strconv.ParseInt(v, 10, 64)
	}
	ch, cancel := s.Hub.Subscribe(since)
	defer cancel()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.TS, e.Kind, b)
			fl.Flush()
		}
	}
}

var _ = context.Background
