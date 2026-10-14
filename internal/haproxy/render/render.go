// Package render turns a desired-state snapshot into a deterministic HAProxy config tree
// (spec §7.2–§7.5). Every line is produced by a typed builder from validated values;
// user text never reaches the config raw (§14.4).
package render

import (
	"bytes"
	"embed"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/state"
)

//go:embed templates/*.tmpl
var tmplFS embed.FS

var tmpl = template.Must(template.ParseFS(tmplFS, "templates/*.tmpl"))

// Input is everything the renderer needs. It contains no I/O.
type Input struct {
	Root           string // absolute dir the config references (live symlink or revision dir)
	NodeID         int64
	Revision       int64
	DesiredVersion int64
	PanelPort      int
	PanelAllow     []string
	LoxyAddr       string // backend of sys_panel, e.g. 127.0.0.1:9090
	Promex         bool
	HardStopAfter  string
	GlobalMaxConn  int // default 200000; needs a matching nofile ulimit
	StateFile      string
	SocketPath     string
	LogTarget      string
	Frontends      []*state.Frontend
	Backends       []*state.Backend
	Cert           func(id int64) *state.Cert
	PanelPEM       string
	PlaceholderPEM string
	MaintPage      string
	OmitServers    bool // skeleton mode used for change classification
	// Domains are routed by Host on the shared sys_http (:80) / sys_https (:443) frontends.
	Domains []DomainRoute
	// SharedHTTP renders sys_http even without domains (managed HTTP-01 certificates are
	// validated through it).
	SharedHTTP bool
	// IPv6 adds an `ipv6@:::port v6only` listener for "*" binds. HAProxy refuses to start
	// when the host has no IPv6 support, so this follows a startup probe (LOXY_IPV6).
	IPv6 bool
}

// Output is the rendered tree, paths relative to the revision dir.
type Output struct {
	Files map[string][]byte
}

func (o *Output) Config() string { return string(o.Files["haproxy.cfg"]) }

type section struct {
	Header string
	Lines  []string
}

type block struct {
	Comment  string
	Sections []section
}

type cfg struct {
	Comment  string
	Global   []string
	Defaults []section
	System   []section
	Blocks   []block
}

func defaultsIn(in *Input) {
	if in.GlobalMaxConn == 0 {
		in.GlobalMaxConn = 200000
	}
	if in.HardStopAfter == "" {
		in.HardStopAfter = "15m"
	}
	if in.StateFile == "" {
		in.StateFile = "/var/lib/haproxy/server-state"
	}
	if in.SocketPath == "" {
		in.SocketPath = "/run/haproxy/admin.sock"
	}
	if in.LogTarget == "" {
		in.LogTarget = "127.0.0.1:5514"
	}
	if in.LoxyAddr == "" {
		in.LoxyAddr = "127.0.0.1:9090"
	}
	if in.PanelPort == 0 {
		in.PanelPort = 8443
	}
	if in.MaintPage == "" {
		in.MaintPage = "<!doctype html><title>Maintenance</title><h1>Down for maintenance</h1><p>Please try again shortly.</p>\n"
	}
	if in.Cert == nil {
		in.Cert = func(int64) *state.Cert { return nil }
	}
}

// Names (§7.3). Frontend and backend names always differ (HAProxy 3.3+ requirement).
func FrontendName(id int64) string { return fmt.Sprintf("fe_%d", id) }
func BackendName(id int64) string  { return fmt.Sprintf("be_%d", id) }
func ServerName(sv *state.Server, sl *state.Slot) string {
	if sl.HostID == 0 {
		return fmt.Sprintf("s%d", sv.ID)
	}
	return fmt.Sprintf("s%dx%d", sv.ID, sl.HostID)
}

// CertPath is the path of a certificate PEM inside the tree.
func CertPath(root string, certID int64) string {
	return path.Join(root, "certs", fmt.Sprintf("%d.pem", certID))
}
func CrtListPath(root string, fe int64) string {
	return path.Join(root, "crt-lists", fmt.Sprintf("fe%d.lst", fe))
}
func MaintMapPath(root string) string { return path.Join(root, "maps", "maint.map") }

// bindAddrs returns the bind address(es) for one frontend address. "*" means every IPv4
// address plus, when the host supports IPv6, every IPv6 address (a separate v6only socket so
// the two listeners never overlap).
func bindAddrs(in *Input, addr string, port int64) []string {
	if addr == "*" || addr == "" {
		p := strconv.FormatInt(port, 10)
		out := []string{"0.0.0.0:" + p}
		if in.IPv6 {
			out = append(out, "ipv6@:::"+p+" v6only")
		}
		return out
	}
	return []string{bindAddr(addr, port)}
}

func bindAddr(addr string, port int64) string {
	a, err := netip.ParseAddr(addr)
	if err == nil && a.Is6() {
		line := "ipv6@" + a.String() + ":" + strconv.FormatInt(port, 10)
		if a.IsUnspecified() {
			line += " v6only" // "::" means IPv6 only; "*" covers both families
		}
		return line
	}
	return addr + ":" + strconv.FormatInt(port, 10)
}

func serverAddr(addr string, port int64) string {
	a, err := netip.ParseAddr(addr)
	if err == nil && a.Is6() {
		return "[" + a.String() + "]:" + strconv.FormatInt(port, 10)
	}
	return addr + ":" + strconv.FormatInt(port, 10)
}

func validAddr(a string, allowWildcard bool) bool {
	if a == "*" || a == "" {
		return allowWildcard
	}
	x, err := netip.ParseAddr(a)
	return err == nil && x.Zone() == ""
}

// ServerArgs renders everything after "server <name>" for a slot. The same string is used for
// config lines and for the runtime `add server` command (§7.6), so dynamic servers are
// byte-identical to configured ones.
func ServerArgs(be *state.Backend, sv *state.Server, sl *state.Slot) string {
	port := sv.PortOr(be.Port)
	var b strings.Builder
	b.WriteString(serverAddr(sl.Address, port))
	fmt.Fprintf(&b, " id %d check inter %ds rise %d fall %d", sl.ID, be.HcIntervalS, be.HcRetries, be.HcRetries)
	if be.HcPort != 0 && be.HcPort != port {
		fmt.Fprintf(&b, " port %d", be.HcPort)
	}
	if be.HcProtocol == "https" {
		if be.HcTlsVerify != 0 && be.HcDomain != "" {
			fmt.Fprintf(&b, " check-ssl verify required ca-file @system-ca check-sni %s", be.HcDomain)
		} else {
			b.WriteString(" check-ssl verify none")
		}
	}
	if be.StickyEnabled != 0 && be.Mode == "http" {
		fmt.Fprintf(&b, " cookie %s", ServerName(sv, sl))
	}
	switch be.ProxyProtocol {
	case "v1":
		b.WriteString(" send-proxy check-send-proxy")
	case "v2":
		b.WriteString(" send-proxy-v2 check-send-proxy")
	}
	if sv.Weight != 100 {
		fmt.Fprintf(&b, " weight %d", sv.Weight)
	}
	if sv.Backup != 0 {
		b.WriteString(" backup")
	}
	return b.String()
}

// Render produces the full tree.
func Render(in Input) (*Output, error) {
	defaultsIn(&in)
	out := &Output{Files: map[string][]byte{}}
	c := cfg{Comment: fmt.Sprintf("# Generated by loxy. DO NOT EDIT. node=%d revision=%d desired_version=%d", in.NodeID, in.Revision, in.DesiredVersion)}
	c.Global = []string{
		"user haproxy",
		"group haproxy",
		"stats socket " + in.SocketPath + " mode 660 gid 1500 level admin expose-fd listeners",
		"stats timeout 30s",
		"log " + in.LogTarget + " len 8192 format rfc5424 local0 info",
		fmt.Sprintf("maxconn %d", in.GlobalMaxConn),
		"hard-stop-after " + in.HardStopAfter,
		"mworker-max-reloads 50",
		"server-state-file " + in.StateFile,
		// crt-list entries are relative to crt-base, so a staged revision validates against its own files.
		"crt-base " + path.Join(in.Root, "certs"),
		"ssl-default-bind-options ssl-min-ver TLSv1.2 no-tls-tickets",
		"ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305",
		"ssl-default-bind-ciphersuites TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256",
		"ssl-default-server-options ssl-min-ver TLSv1.2",
	}
	c.Defaults = []section{
		{Header: "defaults loxy_http", Lines: []string{
			"mode http", "log global", "option httplog", "option dontlognull", "option redispatch",
			"load-server-state-from-file global", "retries 3", "timeout connect 5s", "timeout client 60s",
			"timeout server 60s", "timeout http-request 10s", "timeout http-keep-alive 10s", "timeout queue 30s",
			"timeout tunnel 1h", "timeout check 10s",
		}},
		{Header: "defaults loxy_tcp", Lines: []string{
			"mode tcp", "log global", "option tcplog", "load-server-state-from-file global",
			"timeout connect 5s", "timeout client 60s", "timeout server 60s", "timeout check 10s",
		}},
	}
	panel := section{Header: "frontend sys_panel from loxy_http", Lines: []string{
		fmt.Sprintf("bind :%d ssl crt %s alpn h2,http/1.1", in.PanelPort, path.Join(in.Root, "certs", "panel.pem")),
		"option forwardfor",
		"http-response set-header Strict-Transport-Security \"max-age=31536000\"",
	}}
	if len(in.PanelAllow) > 0 {
		var allow []string
		for _, p := range in.PanelAllow {
			pf, err := netip.ParsePrefix(p)
			if err != nil {
				a, err2 := netip.ParseAddr(p)
				if err2 != nil {
					return nil, fmt.Errorf("panel allowlist: invalid %q", p)
				}
				pf = netip.PrefixFrom(a, a.BitLen())
			}
			allow = append(allow, pf.String())
		}
		sort.Strings(allow)
		out.Files["maps/panel_allow.lst"] = []byte(strings.Join(allow, "\n") + "\n")
		panel.Lines = append(panel.Lines,
			"acl panel_allowed src -f "+path.Join(in.Root, "maps", "panel_allow.lst"),
			"http-request deny if !panel_allowed")
	}
	panel.Lines = append(panel.Lines, "default_backend sys_panel_be")
	stats := section{Header: "frontend sys_stats from loxy_http", Lines: []string{"bind 127.0.0.1:8405"}}
	if in.Promex {
		stats.Lines = append(stats.Lines, "http-request use-service prometheus-exporter if { path /metrics }")
	}
	stats.Lines = append(stats.Lines, "stats enable", "stats uri /stats", "stats refresh 5s")
	c.System = []section{panel, {Header: "backend sys_panel_be from loxy_http", Lines: []string{"server loxy " + in.LoxyAddr}}, stats}
	out.Files["certs/panel.pem"] = []byte(in.PanelPEM)
	out.Files["errors/maint.http"] = []byte(in.MaintPage)

	usedCerts := map[int64]bool{}
	needPlaceholder := false
	var maint []string
	fes := append([]*state.Frontend(nil), in.Frontends...)
	sort.Slice(fes, func(i, j int) bool { return fes[i].ID < fes[j].ID })
	for _, f := range fes {
		if f.Enabled == 0 {
			continue
		}
		if f.Maintenance != 0 {
			maint = append(maint, fmt.Sprintf("%d 1", f.ID))
		}
		blk, err := renderFrontend(&in, f, usedCerts, &needPlaceholder, out)
		if err != nil {
			return nil, fmt.Errorf("frontend %d: %w", f.ID, err)
		}
		c.Blocks = append(c.Blocks, blk)
	}
	if len(in.Domains) > 0 || in.SharedHTTP {
		blk, err := renderDomains(&in, usedCerts, out)
		if err != nil {
			return nil, err
		}
		c.Blocks = append(c.Blocks, blk)
	}
	bes := append([]*state.Backend(nil), in.Backends...)
	sort.Slice(bes, func(i, j int) bool { return bes[i].ID < bes[j].ID })
	tlsOnly := tlsOnlyBackends(in.Frontends)
	for _, b := range bes {
		sec, err := renderBackend(&in, b, tlsOnly)
		if err != nil {
			return nil, fmt.Errorf("backend %d: %w", b.ID, err)
		}
		c.Blocks = append(c.Blocks, block{Comment: fmt.Sprintf("# ---------- backend %d ----------", b.ID), Sections: []section{sec}})
	}
	if len(maint) > 0 {
		out.Files["maps/maint.map"] = []byte(strings.Join(maint, "\n") + "\n")
	} else {
		out.Files["maps/maint.map"] = []byte{}
	}
	ids := make([]int64, 0, len(usedCerts))
	for id := range usedCerts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		out.Files[fmt.Sprintf("certs/%d.pem", id)] = []byte(in.Cert(id).PEM())
	}
	if needPlaceholder {
		out.Files["certs/_placeholder.pem"] = []byte(in.PlaceholderPEM)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "haproxy.cfg.tmpl", c); err != nil {
		return nil, err
	}
	out.Files["haproxy.cfg"] = buf.Bytes()
	return out, nil
}

// DomainRoute sends one hostname arriving on the shared :80/:443 to a backend.
type DomainRoute struct {
	Host      string // fqdn or *.suffix (validated again here)
	BackendID int64
	CertID    int64 // certificate served on :443 for this host (0 = none yet)
	HTTPS     bool  // certificate issued and HTTPS wanted: :80 redirects to :443
}

// renderDomains builds sys_http (:80: ACME answers, https redirects, Host routing) and, when
// any domain has a certificate, sys_https (:443). Unknown hosts get 404.
func renderDomains(in *Input, usedCerts map[int64]bool, out *Output) (block, error) {
	blk := block{Comment: "# ---------- domains (shared :80/:443) ----------"}
	doms := append([]DomainRoute(nil), in.Domains...)
	sort.Slice(doms, func(i, j int) bool { return doms[i].Host < doms[j].Host })
	var all, redirect []string
	byBackend := map[int64][]string{}
	var backends []int64
	var crt []string
	for _, d := range doms {
		h := strings.ToLower(strings.TrimSpace(d.Host))
		if !model.ValidHostname(h, true) {
			return blk, fmt.Errorf("invalid domain %q", d.Host)
		}
		all = append(all, h)
		if _, ok := byBackend[d.BackendID]; !ok {
			backends = append(backends, d.BackendID)
		}
		byBackend[d.BackendID] = append(byBackend[d.BackendID], h)
		if d.CertID != 0 {
			if c := in.Cert(d.CertID); c != nil && c.PEM() != "" {
				if line := fmt.Sprintf("%d.pem [alpn h2,http/1.1]", d.CertID); !slices.Contains(crt, line) {
					crt = append(crt, line)
				}
				usedCerts[d.CertID] = true
				if d.HTTPS {
					redirect = append(redirect, h)
				}
			}
		}
	}
	routes := func(sec *section) error {
		if len(all) > 0 {
			cond, err := routeCond(state.Route{Hosts: all})
			if err != nil {
				return err
			}
			sec.Lines = append(sec.Lines, `http-request return status 404 content-type text/plain string "Unknown host" unless `+cond)
		} else {
			sec.Lines = append(sec.Lines, `http-request return status 404 content-type text/plain string "Unknown host"`)
		}
		// Exact hosts first so that a wildcard never shadows a more specific domain.
		for _, wildcard := range []bool{false, true} {
			for _, id := range backends {
				var hs []string
				for _, h := range byBackend[id] {
					if strings.HasPrefix(h, "*.") == wildcard {
						hs = append(hs, h)
					}
				}
				if len(hs) == 0 {
					continue
				}
				cond, err := routeCond(state.Route{Hosts: hs})
				if err != nil {
					return err
				}
				sec.Lines = append(sec.Lines, fmt.Sprintf("use_backend %s if %s", BackendName(id), cond))
			}
		}
		return nil
	}
	h := section{Header: "frontend sys_http from loxy_http"}
	for _, b := range bindAddrs(in, "*", 80) {
		h.Lines = append(h.Lines, "bind "+b)
	}
	h.Lines = append(h.Lines, acmeLine)
	if len(redirect) > 0 {
		cond, err := routeCond(state.Route{Hosts: redirect})
		if err != nil {
			return blk, err
		}
		h.Lines = append(h.Lines, "http-request redirect scheme https code 301 if "+cond)
	}
	h.Lines = append(h.Lines, "option forwardfor", "http-request set-header X-Forwarded-Proto http")
	if err := routes(&h); err != nil {
		return blk, err
	}
	blk.Sections = append(blk.Sections, h)
	if len(crt) > 0 {
		out.Files["crt-lists/sys_https.lst"] = []byte(strings.Join(crt, "\n") + "\n")
		t := section{Header: "frontend sys_https from loxy_http"}
		for _, b := range bindAddrs(in, "*", 443) {
			t.Lines = append(t.Lines, "bind "+b+" ssl crt-list "+path.Join(in.Root, "crt-lists", "sys_https.lst")+" alpn h2,http/1.1")
		}
		t.Lines = append(t.Lines, "option forwardfor", "http-request set-header X-Forwarded-Proto https")
		if err := routes(&t); err != nil {
			return blk, err
		}
		blk.Sections = append(blk.Sections, t)
	}
	return blk, nil
}

const acmeLine = `http-request return status 200 content-type text/plain lf-string "%[path,field(-1,/),map(virt@acme_http01)]" if { path_beg /.well-known/acme-challenge/ } { path,field(-1,/),map(virt@acme_http01) -m found }`

// routeCond builds the ACL condition of a routing rule: (exact hosts) / (suffix hosts) AND path prefix.
func routeCond(r state.Route) (string, error) {
	var exact, suffix []string
	for _, h := range r.Hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if !model.ValidHostname(h, true) {
			return "", fmt.Errorf("invalid route host %q", h)
		}
		if strings.HasPrefix(h, "*.") {
			suffix = append(suffix, h[1:]) // ".example.com"
		} else {
			exact = append(exact, h)
		}
	}
	pathCond := ""
	if r.PathPrefix != "" {
		if !model.ValidRoutePath(r.PathPrefix) {
			return "", fmt.Errorf("invalid path prefix %q", r.PathPrefix)
		}
		pathCond = " { path_beg " + r.PathPrefix + " }"
	}
	const host = "req.hdr(host),field(1,:),lower"
	var alts []string
	if len(exact) > 0 {
		alts = append(alts, "{ "+host+" -m str "+strings.Join(exact, " ")+" }"+pathCond)
	}
	if len(suffix) > 0 {
		alts = append(alts, "{ "+host+" -m end "+strings.Join(suffix, " ")+" }"+pathCond)
	}
	if len(alts) == 0 {
		if pathCond == "" {
			return "", fmt.Errorf("route needs hosts or a path prefix")
		}
		return strings.TrimSpace(pathCond), nil
	}
	return strings.Join(alts, " || "), nil
}

func renderFrontend(in *Input, f *state.Frontend, usedCerts map[int64]bool, needPlaceholder *bool, out *Output) (block, error) {
	blk := block{Comment: fmt.Sprintf("# ---------- frontend %d ----------", f.ID)}
	if len(f.Binds) == 0 {
		return blk, nil // nothing to listen on
	}
	for _, b := range f.Binds {
		if !validAddr(b.Address, true) || !model.ValidPort(int(b.Port)) {
			return blk, fmt.Errorf("invalid bind %q:%d", b.Address, b.Port)
		}
	}
	mode := "loxy_http"
	if f.Mode == "tcp" {
		mode = "loxy_tcp"
	}
	tls := f.Mode == "http" && f.Tls != 0
	fe := section{Header: fmt.Sprintf("frontend %s from %s", FrontendName(f.ID), mode)}
	for _, b := range f.Binds {
		for _, a := range bindAddrs(in, b.Address, b.Port) {
			line := "bind " + a
			if tls {
				line += " ssl crt-list " + CrtListPath(in.Root, f.ID) + " alpn h2,http/1.1"
			}
			fe.Lines = append(fe.Lines, line)
		}
	}
	if f.Maxconn > 0 {
		fe.Lines = append(fe.Lines, fmt.Sprintf("maxconn %d", f.Maxconn))
	}
	if f.TimeoutClientS != 60 {
		fe.Lines = append(fe.Lines, fmt.Sprintf("timeout client %ds", f.TimeoutClientS))
	}
	maintMap := MaintMapPath(in.Root)
	if f.Mode == "tcp" {
		fe.Lines = append(fe.Lines, fmt.Sprintf("tcp-request connection reject if { str(%d),map(opt@%s) -m found }", f.ID, maintMap))
	} else {
		if f.ForwardedHeaders != 0 {
			proto := "http"
			if tls {
				proto = "https"
			}
			fe.Lines = append(fe.Lines, "option forwardfor",
				"http-request set-header X-Forwarded-Proto "+proto,
				"http-request set-header X-Forwarded-Port %[dst_port]")
		}
		fe.Lines = append(fe.Lines, fmt.Sprintf("http-request return status 503 content-type text/html file %s if { str(%d),map(opt@%s) -m found }",
			path.Join(in.Root, "errors", "maint.http"), f.ID, maintMap))
		for _, r := range f.Routes {
			cond, err := routeCond(r)
			if err != nil {
				return blk, err
			}
			fe.Lines = append(fe.Lines, fmt.Sprintf("use_backend %s if %s", BackendName(r.BackendID), cond))
		}
	}
	if f.DefaultBackendID.Valid {
		fe.Lines = append(fe.Lines, "default_backend "+BackendName(f.DefaultBackendID.Int64))
	}
	blk.Sections = append(blk.Sections, fe)

	if tls {
		var lst []string
		for _, cid := range f.CertIDs {
			c := in.Cert(cid)
			if c == nil || c.PEM() == "" {
				continue
			}
			usedCerts[cid] = true
			lst = append(lst, fmt.Sprintf("%d.pem [alpn h2,http/1.1]", cid))
		}
		if len(lst) == 0 {
			*needPlaceholder = true
			lst = append(lst, "_placeholder.pem [alpn h2,http/1.1]")
		}
		out.Files[fmt.Sprintf("crt-lists/fe%d.lst", f.ID)] = []byte(strings.Join(lst, "\n") + "\n")
	}
	return blk, nil
}

// tlsOnlyBackends returns the backends that are used, and only by enabled frontends that
// terminate TLS: their sticky cookie can be marked secure (§7.5).
func tlsOnlyBackends(fes []*state.Frontend) map[int64]bool {
	out := map[int64]bool{}
	for _, f := range fes {
		if f.Enabled == 0 {
			continue
		}
		tls := f.Mode == "http" && f.Tls != 0
		for _, id := range f.BackendIDs() {
			if v, seen := out[id]; !seen || v {
				out[id] = tls
			}
		}
	}
	return out
}

func renderBackend(in *Input, b *state.Backend, tlsOnly map[int64]bool) (section, error) {
	mode := "loxy_http"
	if b.Mode == "tcp" {
		mode = "loxy_tcp"
	}
	be := section{Header: fmt.Sprintf("backend %s from %s", BackendName(b.ID), mode)}
	be.Lines = append(be.Lines, "balance "+b.Algorithm)
	if b.Mode == "http" && slices.ContainsFunc(in.Domains, func(d DomainRoute) bool { return d.BackendID == b.ID }) {
		// Domain traffic reaches this backend through sys_http/sys_https, not fe_<id>: apply the
		// project's maintenance mode here too (same map key as the service frontend).
		maintMap := MaintMapPath(in.Root)
		be.Lines = append(be.Lines, fmt.Sprintf("http-request return status 503 content-type text/html file %s if { str(%d),map(opt@%s) -m found }",
			path.Join(in.Root, "errors", "maint.http"), b.ID, maintMap))
	}
	if b.TimeoutServerS != 60 {
		be.Lines = append(be.Lines, fmt.Sprintf("timeout server %ds", b.TimeoutServerS))
	}
	if b.StickyEnabled != 0 && b.Mode == "http" {
		if !model.ValidCookieName(b.StickyCookieName) {
			return be, fmt.Errorf("invalid cookie name")
		}
		secure := ""
		if tlsOnly[b.ID] {
			secure = " secure"
		}
		be.Lines = append(be.Lines, fmt.Sprintf("cookie %s insert indirect nocache httponly%s maxlife %ds", b.StickyCookieName, secure, b.StickyCookieLifetimeS))
	}
	if b.HcProtocol == "http" || b.HcProtocol == "https" {
		be.Lines = append(be.Lines, "option httpchk")
		send := "http-check send meth GET uri " + b.HcPath
		if b.HcDomain != "" {
			send += " ver HTTP/1.1 hdr Host " + b.HcDomain
		}
		be.Lines = append(be.Lines, send)
		codes, err := model.StatusCodesToHAProxy(b.StatusCodes)
		if err != nil {
			return be, err
		}
		be.Lines = append(be.Lines, "http-check expect status "+codes)
		if b.HcResponse != "" {
			be.Lines = append(be.Lines, "http-check expect string '"+b.HcResponse+"'")
		}
	}
	if b.HcTimeoutS != 10 {
		be.Lines = append(be.Lines, fmt.Sprintf("timeout check %ds", b.HcTimeoutS))
	}
	if in.OmitServers {
		return be, nil
	}
	type srv struct {
		id   int64
		line string
	}
	var lines []srv
	for _, sv := range b.Servers {
		for _, sl := range sv.Slots {
			if !validAddr(sl.Address, false) {
				return be, fmt.Errorf("invalid server address %q", sl.Address)
			}
			line := "server " + ServerName(sv, sl) + " " + ServerArgs(b, sv, sl)
			if sv.AdminState == "maint" {
				line += " disabled"
			}
			lines = append(lines, srv{sl.ID, line})
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].id < lines[j].id })
	for _, l := range lines {
		be.Lines = append(be.Lines, l.line)
	}
	return be, nil
}

// FrontendSections extracts the config of one frontend and the backends it uses (GET /config).
func FrontendSections(cfgText string, f *state.Frontend) string {
	names := map[string]bool{FrontendName(f.ID): true}
	for _, id := range f.BackendIDs() {
		names[BackendName(id)] = true
	}
	var b strings.Builder
	in := false
	for _, line := range strings.Split(cfgText, "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' {
			fl := strings.Fields(line)
			in = len(fl) >= 2 && (fl[0] == "frontend" || fl[0] == "backend") && names[fl[1]]
		}
		if in {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
