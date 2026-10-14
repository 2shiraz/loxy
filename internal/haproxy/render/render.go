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
	LBs            []*state.LB
	Cert           func(id int64) *state.Cert
	// ACMEHTTP01 lists LB ids that need challenge answering on :80 although no :80 frontend exists.
	ACMEHTTP01     map[int64]bool
	PanelPEM       string
	PlaceholderPEM string
	MaintPage      string
	OmitServers    bool // skeleton mode used for change classification
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

type cfg struct {
	Comment  string
	Global   []string
	Defaults []section
	System   []section
	LBs      []lbBlock
}

type lbBlock struct {
	Comment  string
	Sections []section
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
}

// Names (§7.3).
func FrontendName(lb, svc int64) string { return fmt.Sprintf("lb%d_s%d_fe", lb, svc) }
func RedirectName(lb, svc int64) string { return fmt.Sprintf("lb%d_s%d_rd", lb, svc) }
func BackendName(lb, svc int64) string  { return fmt.Sprintf("lb%d_s%d_be", lb, svc) }
func CapTable(lb int64) string          { return fmt.Sprintf("lb%d_cap", lb) }
func ACMEFrontend(lb int64) string      { return fmt.Sprintf("sys_acme_lb%d", lb) }
func ServerName(t *state.Target, sl *state.Slot) string {
	if sl.ServerID == 0 {
		return fmt.Sprintf("t%d", t.ID)
	}
	return fmt.Sprintf("t%dx%d", t.ID, sl.ServerID)
}

// CertPath is the path of a certificate PEM inside the tree.
func CertPath(root string, certID int64) string {
	return path.Join(root, "certs", fmt.Sprintf("%d.pem", certID))
}
func PlaceholderPath(root string) string { return path.Join(root, "certs", "_placeholder.pem") }
func CrtListPath(root string, svc int64) string {
	return path.Join(root, "crt-lists", fmt.Sprintf("s%d.lst", svc))
}
func MaintMapPath(root string) string { return path.Join(root, "maps", "maint.map") }

func hostPort(addr string, port int64) string {
	a, err := netip.ParseAddr(addr)
	if err == nil && a.Is6() {
		return "ipv6@" + a.String() + ":" + strconv.FormatInt(port, 10)
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

// BindAddrs returns the addresses an LB listens on (sorted, deterministic).
// With the public interface enabled but no assigned IP, the LB shares the node's
// addresses (wildcard bind, §21 Q3 default).
func BindAddrs(lb *state.LB) []string {
	var out []string
	if lb.PublicEnabled != 0 {
		if lb.IPv4 != "" {
			out = append(out, lb.IPv4)
		}
		if lb.IPv6 != "" {
			out = append(out, lb.IPv6)
		}
		if lb.IPv4 == "" && lb.IPv6 == "" {
			out = append(out, "")
		}
	}
	for _, n := range lb.Networks {
		out = append(out, n.IP)
	}
	return out
}

// ServerArgs renders everything after "server <name>" for a slot. The same string is
// used for config lines and for the runtime `add server` command (§7.6), so dynamic
// servers are byte-identical to configured ones.
func ServerArgs(lb *state.LB, svc *state.Service, t *state.Target, sl *state.Slot) string {
	var b strings.Builder
	b.WriteString(serverAddr(sl.Address, svc.DestinationPort))
	fmt.Fprintf(&b, " id %d check inter %ds rise %d fall %d", sl.ID, svc.HcIntervalS, svc.HcRetries, svc.HcRetries)
	if svc.HcPort != 0 && svc.HcPort != svc.DestinationPort {
		fmt.Fprintf(&b, " port %d", svc.HcPort)
	}
	if svc.HcProtocol == "https" {
		if svc.HcTlsVerify != 0 && svc.HcDomain != "" {
			fmt.Fprintf(&b, " check-ssl verify required ca-file @system-ca check-sni %s", svc.HcDomain)
		} else {
			b.WriteString(" check-ssl verify none")
		}
	}
	if svc.StickyEnabled != 0 && svc.Protocol != "tcp" {
		fmt.Fprintf(&b, " cookie %s", ServerName(t, sl))
	}
	switch svc.ProxyProtocol {
	case "v1":
		b.WriteString(" send-proxy check-send-proxy")
	case "v2":
		b.WriteString(" send-proxy-v2 check-send-proxy")
	}
	if t.Weight != 100 {
		fmt.Fprintf(&b, " weight %d", t.Weight)
	}
	if t.Backup != 0 {
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
	// System sections.
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
	c.System = []section{
		panel,
		{Header: "backend sys_panel_be from loxy_http", Lines: []string{"server loxy " + in.LoxyAddr}},
		stats,
	}
	out.Files["certs/panel.pem"] = []byte(in.PanelPEM)
	out.Files["errors/maint.http"] = []byte(in.MaintPage)

	var maint []string
	usedCerts := map[int64]bool{}
	needPlaceholder := false
	lbs := append([]*state.LB(nil), in.LBs...)
	sort.Slice(lbs, func(i, j int) bool { return lbs[i].ID < lbs[j].ID })
	maintMap := MaintMapPath(in.Root)
	for _, lb := range lbs {
		if lb.Maintenance != 0 {
			maint = append(maint, fmt.Sprintf("%d 1", lb.ID))
		}
		blk, err := renderLB(&in, lb, maintMap, usedCerts, &needPlaceholder, out)
		if err != nil {
			return nil, fmt.Errorf("lb %d: %w", lb.ID, err)
		}
		c.LBs = append(c.LBs, blk)
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

func renderLB(in *Input, lb *state.LB, maintMap string, usedCerts map[int64]bool, needPlaceholder *bool, out *Output) (lbBlock, error) {
	planName, maxconn, enforce := "-", int64(0), false
	if lb.Plan != nil {
		planName, maxconn, enforce = lb.Plan.Name, lb.Plan.MaxConnections, lb.Plan.EnforceLbCap != 0
	}
	blk := lbBlock{Comment: fmt.Sprintf("# ---------- LB %d (plan %s) ----------", lb.ID, planName)}
	addrs := BindAddrs(lb)
	if enforce && maxconn > 0 {
		blk.Sections = append(blk.Sections, section{Header: "backend " + CapTable(lb.ID), Lines: []string{
			"stick-table type integer size 1 expire 24h store conn_cur",
		}})
	}
	svcs := append([]*state.Service(nil), lb.Services...)
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].ID < svcs[j].ID })
	has80 := false
	for _, s := range svcs {
		if (s.ListenPort == 80 && s.Protocol == "http") || (s.Protocol == "https" && s.HttpRedirect != 0) {
			has80 = true
		}
	}
	acmeLine := `http-request return status 200 content-type text/plain lf-string "%[path,field(-1,/),map(virt@acme_http01)]" if { path_beg /.well-known/acme-challenge/ } { path,field(-1,/),map(virt@acme_http01) -m found }`
	maintHTTP := fmt.Sprintf("http-request return status 503 content-type text/html file %s if { str(%d),map(opt@%s) -m found }", path.Join(in.Root, "errors", "maint.http"), lb.ID, maintMap)
	maintTCP := fmt.Sprintf("tcp-request connection reject if { str(%d),map(opt@%s) -m found }", lb.ID, maintMap)
	capLines := func() []string {
		if !enforce || maxconn <= 0 {
			return nil
		}
		return []string{
			fmt.Sprintf("tcp-request connection track-sc0 int(1) table %s", CapTable(lb.ID)),
			fmt.Sprintf("tcp-request connection reject if { sc0_conn_cur gt %d }", maxconn),
		}
	}
	for _, s := range svcs {
		mode := "loxy_http"
		if s.Protocol == "tcp" {
			mode = "loxy_tcp"
		}
		if len(addrs) > 0 {
			fe := section{Header: fmt.Sprintf("frontend %s from %s", FrontendName(lb.ID, s.ID), mode)}
			for _, a := range addrs {
				line := "bind " + hostPort(a, s.ListenPort)
				if s.Protocol == "https" {
					line += " ssl crt-list " + CrtListPath(in.Root, s.ID) + " alpn h2,http/1.1"
				}
				fe.Lines = append(fe.Lines, line)
			}
			if maxconn > 0 {
				fe.Lines = append(fe.Lines, fmt.Sprintf("maxconn %d", maxconn))
			}
			fe.Lines = append(fe.Lines, capLines()...)
			if s.HttpTimeoutS != 60 {
				fe.Lines = append(fe.Lines, fmt.Sprintf("timeout client %ds", s.HttpTimeoutS))
			}
			if s.Protocol == "tcp" {
				fe.Lines = append(fe.Lines, maintTCP)
			} else {
				proto := "http"
				if s.Protocol == "https" {
					proto = "https"
				}
				fe.Lines = append(fe.Lines,
					"option forwardfor",
					"http-request set-header X-Forwarded-Proto "+proto,
					"http-request set-header X-Forwarded-Port %[dst_port]")
				if s.ListenPort == 80 && s.Protocol == "http" {
					fe.Lines = append(fe.Lines, acmeLine)
				}
				fe.Lines = append(fe.Lines, maintHTTP)
			}
			fe.Lines = append(fe.Lines, "default_backend "+BackendName(lb.ID, s.ID))
			blk.Sections = append(blk.Sections, fe)

			if s.Protocol == "https" && s.HttpRedirect != 0 {
				rd := section{Header: fmt.Sprintf("frontend %s from loxy_http", RedirectName(lb.ID, s.ID))}
				for _, a := range addrs {
					rd.Lines = append(rd.Lines, "bind "+hostPort(a, 80))
				}
				rd.Lines = append(rd.Lines, acmeLine, "http-request redirect scheme https code 301")
				blk.Sections = append(blk.Sections, rd)
			}
		}
		if s.Protocol == "https" {
			var lst []string
			for _, cid := range s.CertIDs {
				c := in.Cert(cid)
				if c == nil || c.PEM() == "" {
					continue
				}
				usedCerts[cid] = true
				lst = append(lst, CertPath(in.Root, cid)+" [alpn h2,http/1.1]")
			}
			if len(lst) == 0 {
				*needPlaceholder = true
				lst = append(lst, PlaceholderPath(in.Root)+" [alpn h2,http/1.1]")
			}
			out.Files[fmt.Sprintf("crt-lists/s%d.lst", s.ID)] = []byte(strings.Join(lst, "\n") + "\n")
		}

		be := section{Header: fmt.Sprintf("backend %s from %s", BackendName(lb.ID, s.ID), mode)}
		be.Lines = append(be.Lines, "balance "+lb.Algorithm)
		if s.HttpTimeoutS != 60 {
			be.Lines = append(be.Lines, fmt.Sprintf("timeout server %ds", s.HttpTimeoutS))
		}
		if s.StickyEnabled != 0 && s.Protocol != "tcp" {
			line := fmt.Sprintf("cookie %s insert indirect nocache httponly", s.StickyCookieName)
			if s.Protocol == "https" {
				line += " secure"
			}
			be.Lines = append(be.Lines, line+fmt.Sprintf(" maxlife %ds", s.StickyCookieLifetimeS))
		}
		if s.HcProtocol == "http" || s.HcProtocol == "https" {
			be.Lines = append(be.Lines, "option httpchk")
			send := "http-check send meth GET uri " + s.HcPath
			if s.HcDomain != "" {
				send += " ver HTTP/1.1 hdr Host " + s.HcDomain
			}
			be.Lines = append(be.Lines, send)
			codes, err := model.StatusCodesToHAProxy(s.StatusCodes)
			if err != nil {
				return blk, err
			}
			be.Lines = append(be.Lines, "http-check expect status "+codes)
			if s.HcResponse != "" {
				be.Lines = append(be.Lines, "http-check expect string '"+s.HcResponse+"'")
			}
		}
		if s.HcTimeoutS != 10 {
			be.Lines = append(be.Lines, fmt.Sprintf("timeout check %ds", s.HcTimeoutS))
		}
		if !in.OmitServers {
			targets := append([]*state.Target(nil), lb.Targets...)
			sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
			type srv struct {
				id   int64
				line string
			}
			var lines []srv
			for _, t := range targets {
				for _, sl := range t.Slots {
					line := "server " + ServerName(t, sl) + " " + ServerArgs(lb, s, t, sl)
					if t.AdminState == "maint" {
						line += " disabled"
					}
					lines = append(lines, srv{sl.ID, line})
				}
			}
			sort.Slice(lines, func(i, j int) bool { return lines[i].id < lines[j].id })
			for _, l := range lines {
				be.Lines = append(be.Lines, l.line)
			}
		}
		blk.Sections = append(blk.Sections, be)
	}
	if !has80 && in.ACMEHTTP01[lb.ID] && len(addrs) > 0 {
		fe := section{Header: fmt.Sprintf("frontend %s from loxy_http", ACMEFrontend(lb.ID))}
		for _, a := range addrs {
			fe.Lines = append(fe.Lines, "bind "+hostPort(a, 80))
		}
		fe.Lines = append(fe.Lines, acmeLine, "http-request return status 404")
		blk.Sections = append(blk.Sections, fe)
	}
	return blk, nil
}

// LBSections extracts the config lines that belong to one LB (for GET /config).
func LBSections(cfgText string, lbID int64) string {
	prefixes := []string{fmt.Sprintf("lb%d_", lbID), ACMEFrontend(lbID)}
	var b strings.Builder
	in := false
	for _, line := range strings.Split(cfgText, "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' {
			f := strings.Fields(line)
			in = false
			if len(f) >= 2 && (f[0] == "frontend" || f[0] == "backend") {
				for _, p := range prefixes {
					if strings.HasPrefix(f[1], p) {
						in = true
					}
				}
			}
		}
		if in {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
