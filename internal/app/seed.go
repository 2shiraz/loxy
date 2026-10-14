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

// SeedDemo creates demo data (§17 `make seed`): hosts, a "shop" project with an http service
// on its own port, an https service with an uploaded certificate, IP and label-selector
// targets and a demo domain, and a "db" tcp project. serverIP is the address demo IP targets
// point to (e.g. the dev whoami host).
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
		project := func(name, alg string) (db.Balancer, error) {
			return q.CreateBalancer(ctx, db.CreateBalancerParams{Name: name, Algorithm: alg, Labels: `{"env":"demo"}`, CreatedAt: now, UpdatedAt: now})
		}
		service := func(b int64, proto string, listen, dest int64) (db.Service, error) {
			hc := "http"
			if proto == "tcp" {
				hc = "tcp"
			}
			return q.CreateService(ctx, db.CreateServiceParams{BalancerID: b, Protocol: proto, ListenPort: listen, DestinationPort: dest, ProxyProtocol: "none",
				TimeoutS: 60, StickyCookieName: "LOXYSTICKY", StickyCookieLifetimeS: 300, HcProtocol: hc, HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3,
				HcPath: "/", HcStatusCodes: `["2??","3??"]`, CreatedAt: now})
		}
		target := func(b int64, typ, ip, sel string) error {
			_, err := q.CreateTarget(ctx, db.CreateTargetParams{BalancerID: b, Type: typ, Ip: ip, LabelSelector: sel, Weight: 100, CreatedAt: now})
			return err
		}
		shop, err := project("shop", "roundrobin")
		if err != nil {
			return err
		}
		if _, err := service(shop.ID, "http", 20000, 9001); err != nil {
			return err
		}
		tls, err := service(shop.ID, "https", 20001, 9001)
		if err != nil {
			return err
		}
		if err := q.AddServiceCertificate(ctx, db.AddServiceCertificateParams{ServiceID: tls.ID, CertificateID: cert.ID}); err != nil {
			return err
		}
		if err := target(shop.ID, "ip", serverIP, ""); err != nil {
			return err
		}
		if err := target(shop.ID, "label_selector", "", "role=web"); err != nil {
			return err
		}
		if _, err := q.CreateDomain(ctx, db.CreateDomainParams{Fqdn: "demo.loxy.test", BalancerID: sql.NullInt64{Int64: shop.ID, Valid: true}, Https: 0,
			CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		dbp, err := project("db", "leastconn")
		if err != nil {
			return err
		}
		if _, err := service(dbp.ID, "tcp", 20002, 9002); err != nil {
			return err
		}
		if err := target(dbp.ID, "ip", serverIP, ""); err != nil {
			return err
		}
		if err := state.SyncSlots(ctx, q); err != nil {
			return err
		}
		_, err = q.BumpDesiredVersion(ctx, 1)
		return err
	})
}
