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

// SeedDemo creates demo data (§17 `make seed`): servers, two LBs with services of each
// protocol, IP/server/label-selector targets and an uploaded certificate.
// targetIP is the address demo IP targets point to (e.g. the dev whoami host).
func (a *App) SeedDemo(ctx context.Context, targetIP string) error {
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
			if _, err := q.CreateServer(ctx, db.CreateServerParams{Name: fmt.Sprintf("demo-%s-%d", l, i+1), PublicIpv4: fmt.Sprintf("10.99.0.%d", i+1),
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
		mk := func(name, alg string) (db.LoadBalancer, error) {
			return q.CreateLoadBalancer(ctx, db.CreateLoadBalancerParams{Name: name, PlanID: 1, NodeID: 1, Algorithm: alg, PublicEnabled: 1, Labels: `{"env":"demo"}`, CreatedAt: now, UpdatedAt: now})
		}
		svc := func(lb int64, proto string, listen, dest int64, redirect int64, hc string) (db.Service, error) {
			return q.CreateService(ctx, db.CreateServiceParams{LbID: lb, Protocol: proto, ListenPort: listen, DestinationPort: dest, HttpRedirect: redirect,
				StickyCookieName: "LBSTICKY", StickyCookieLifetimeS: 300, ProxyProtocol: "none", HttpTimeoutS: 60, HcProtocol: hc, HcPort: dest,
				HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/", HcStatusCodes: `["2??","3??"]`, CreatedAt: now})
		}
		web, err := mk("demo-web", "roundrobin")
		if err != nil {
			return err
		}
		if _, err := svc(web.ID, "http", 8080, 9001, 0, "http"); err != nil {
			return err
		}
		tls, err := svc(web.ID, "https", 8444, 9001, 0, "http")
		if err != nil {
			return err
		}
		if err := q.AddServiceCertificate(ctx, db.AddServiceCertificateParams{ServiceID: tls.ID, CertificateID: cert.ID}); err != nil {
			return err
		}
		for _, ip := range []string{targetIP} {
			if _, err := q.CreateTarget(ctx, db.CreateTargetParams{LbID: web.ID, Type: "ip", Ip: ip, Weight: 100, CreatedAt: now}); err != nil {
				return err
			}
		}
		if _, err := q.CreateTarget(ctx, db.CreateTargetParams{LbID: web.ID, Type: "label_selector", LabelSelector: "role=web", Weight: 100, CreatedAt: now}); err != nil {
			return err
		}
		api, err := mk("demo-api", "leastconn")
		if err != nil {
			return err
		}
		if _, err := svc(api.ID, "tcp", 7443, 9002, 0, "tcp"); err != nil {
			return err
		}
		if _, err := q.CreateTarget(ctx, db.CreateTargetParams{LbID: api.ID, Type: "ip", Ip: targetIP, Weight: 100, CreatedAt: now}); err != nil {
			return err
		}
		if err := state.SyncSlots(ctx, q); err != nil {
			return err
		}
		_, err = q.BumpDesiredVersion(ctx, 1)
		return err
	})
}
