package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// SeedDemo creates demo data (§17 `make seed`): hosts, an http backend pool fed by an IP
// and a label selector, a tcp backend, an http + https frontend with a host route, a tcp
// frontend and an uploaded certificate. serverIP is the address demo IP servers point to
// (e.g. the dev whoami host).
func (a *App) SeedDemo(ctx context.Context, serverIP string) error {
	now := time.Now().UnixMilli()
	c, k, err := certs.SelfSigned("demo.loxy.test", []string{"demo.loxy.test"}, 90*24*time.Hour)
	if err != nil {
		return err
	}
	p, err := certs.Parse(c, "", k, time.Now())
	if err != nil {
		return err
	}
	return a.Store.Tx(ctx, func(q *db.Queries) error {
		for i, l := range []string{"web", "web", "api"} {
			if _, err := q.CreateHost(ctx, db.CreateHostParams{Name: fmt.Sprintf("demo-%s-%d", l, i+1), Ipv4: fmt.Sprintf("10.99.0.%d", i+1),
				Labels: fmt.Sprintf(`{"role":%q,"env":"demo"}`, l)}); err != nil {
				return err
			}
		}
		cert, err := q.CreateCertificate(ctx, db.CreateCertificateParams{Name: "demo (self-signed)", Type: "uploaded", Domains: `["demo.loxy.test"]`,
			CertPem: p.CertPEM, KeyPemEnc: a.Box.Seal([]byte(p.KeyPEM)), Issuer: p.Issuer, Serial: p.Serial, FingerprintSha256: p.Fingerprint,
			NotBefore: sql.NullInt64{Int64: p.Leaf.NotBefore.UnixMilli(), Valid: true}, NotAfter: sql.NullInt64{Int64: p.Leaf.NotAfter.UnixMilli(), Valid: true},
			Status: "issued", Labels: "{}", CreatedAt: now})
		if err != nil {
			return err
		}
		be := func(name, mode, alg string, port int64) (db.Backend, error) {
			hc := "http"
			if mode == "tcp" {
				hc = "tcp"
			}
			return q.CreateBackend(ctx, db.CreateBackendParams{Name: name, Mode: mode, Algorithm: alg, Port: port, StickyCookieName: "LOXYSTICKY",
				StickyCookieLifetimeS: 300, ProxyProtocol: "none", TimeoutServerS: 60, HcProtocol: hc, HcPort: port, HcIntervalS: 15, HcTimeoutS: 10,
				HcRetries: 3, HcPath: "/", HcStatusCodes: `["2??","3??"]`, Labels: `{"env":"demo"}`, CreatedAt: now})
		}
		srv := func(b int64, typ, ip, sel string) error {
			_, err := q.CreateBackendServer(ctx, db.CreateBackendServerParams{BackendID: b, Type: typ, Ip: ip, LabelSelector: sel, Weight: 100, CreatedAt: now})
			return err
		}
		fe := func(name, mode string, tls int64, port int64, def int64) (db.Frontend, error) {
			return q.CreateFrontend(ctx, db.CreateFrontendParams{Name: name, Mode: mode, Tls: tls, Binds: fmt.Sprintf(`[{"address":"*","port":%d}]`, port),
				DefaultBackendID: sql.NullInt64{Int64: def, Valid: true}, TimeoutClientS: 60, Maxconn: 10000, ForwardedHeaders: b2i(mode == "http"),
				Enabled: 1, Labels: `{"env":"demo"}`, CreatedAt: now, UpdatedAt: now})
		}
		web, err := be("demo-web", "http", "roundrobin", 9001)
		if err != nil {
			return err
		}
		if err := srv(web.ID, "ip", serverIP, ""); err != nil {
			return err
		}
		if err := srv(web.ID, "label_selector", "", "role=web"); err != nil {
			return err
		}
		apiBE, err := be("demo-api", "http", "leastconn", 9001)
		if err != nil {
			return err
		}
		if err := srv(apiBE.ID, "ip", serverIP, ""); err != nil {
			return err
		}
		tcp, err := be("demo-tcp", "tcp", "leastconn", 9002)
		if err != nil {
			return err
		}
		if err := srv(tcp.ID, "ip", serverIP, ""); err != nil {
			return err
		}
		h, err := fe("demo-http", "http", 0, 8080, web.ID)
		if err != nil {
			return err
		}
		if err := q.AddRoute(ctx, db.AddRouteParams{FrontendID: h.ID, Hosts: `["api.loxy.test"]`, BackendID: apiBE.ID}); err != nil {
			return err
		}
		hs, err := fe("demo-https", "http", 1, 8444, web.ID)
		if err != nil {
			return err
		}
		if err := q.AddFrontendCertificate(ctx, db.AddFrontendCertificateParams{FrontendID: hs.ID, CertificateID: cert.ID}); err != nil {
			return err
		}
		if _, err := fe("demo-tcp", "tcp", 0, 7443, tcp.ID); err != nil {
			return err
		}
		if err := state.SyncSlots(ctx, q); err != nil {
			return err
		}
		_, err = q.BumpDesiredVersion(ctx, 1)
		return err
	})
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
