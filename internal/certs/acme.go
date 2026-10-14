package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/registration"

	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/store/db"
)

// HTTP01 publishes challenge tokens (via the HAProxy virt@acme_http01 map on every node).
type HTTP01 interface {
	SetACMEToken(ctx context.Context, token, keyAuth string) error
	DelACMEToken(ctx context.Context, token string)
}

// Issuer obtains and renews managed certificates with lego (spec §11.2). It is the
// CertIssuer abstraction: swapping in HAProxy's native ACME later only replaces this type.
type Issuer struct {
	Q         *db.Queries
	Box       *secrets.Box
	Directory string
	Email     string
	Insecure  bool // dev/CI against Pebble only
	HTTP      HTTP01
	DNS       func(ctx context.Context, providerID int64) (challenge.Provider, error)
	OnChange  func(certID int64)
	// Ready reports whether an HTTP-01 certificate is served by an applied config
	// (otherwise the challenge map does not exist yet and issuance waits).
	Ready func(certID int64) bool

	mu       sync.Mutex
	running  map[int64]bool
	attempts map[int64]int
	nextTry  map[int64]time.Time
}

type user struct {
	email string
	reg   *acme.ExtendedAccount
	key   crypto.Signer
}

func (u *user) GetEmail() string                       { return u.email }
func (u *user) GetRegistration() *acme.ExtendedAccount { return u.reg }
func (u *user) GetPrivateKey() crypto.Signer           { return u.key }

type httpProvider struct{ h HTTP01 }

func (p httpProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	return p.h.SetACMEToken(ctx, token, keyAuth)
}
func (p httpProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	p.h.DelACMEToken(ctx, token)
	return nil
}

func (i *Issuer) client(ctx context.Context) (*lego.Client, error) {
	acc, err := i.Q.GetACMEAccount(ctx, i.Directory)
	u := &user{email: i.Email}
	httpc := &http.Client{Timeout: 2 * time.Minute}
	if i.Insecure {
		httpc.Transport = &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // dev/CI (Pebble) only
	}
	mk := func() (*lego.Client, error) {
		cfg := lego.NewConfig(u)
		cfg.CADirURL = i.Directory
		cfg.HTTPClient = httpc
		cfg.UserAgent = "loxy"
		return lego.NewClient(cfg)
	}
	if err == nil {
		kb, err := i.Box.Open(acc.KeyPemEnc)
		if err != nil {
			return nil, err
		}
		blk, _ := pem.Decode(kb)
		if blk == nil {
			return nil, errors.New("acme account key corrupt")
		}
		k, err := x509.ParseECPrivateKey(blk.Bytes)
		if err != nil {
			return nil, err
		}
		u.key = k
		var reg acme.ExtendedAccount
		_ = json.Unmarshal([]byte(acc.Registration), &reg)
		u.reg = &reg
		return mk()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	u.key = k
	c, err := mk()
	if err != nil {
		return nil, err
	}
	reg, err := c.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, fmt.Errorf("acme register: %w", err)
	}
	u.reg = reg
	kb, _ := x509.MarshalECPrivateKey(k)
	rj, _ := json.Marshal(reg)
	if err := i.Q.CreateACMEAccount(ctx, db.CreateACMEAccountParams{DirectoryUrl: i.Directory, Email: i.Email,
		KeyPemEnc: i.Box.Seal(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})), Registration: string(rj)}); err != nil {
		return nil, err
	}
	return c, nil
}

func keyType(s string) certcrypto.KeyType {
	switch s {
	case "rsa2048":
		return certcrypto.RSA2048
	case "rsa4096":
		return certcrypto.RSA4096
	}
	return certcrypto.EC256
}

// IssueAsync starts issuance in the background (one at a time per certificate).
func (i *Issuer) IssueAsync(certID int64) {
	i.mu.Lock()
	if i.running == nil {
		i.running, i.attempts, i.nextTry = map[int64]bool{}, map[int64]int{}, map[int64]time.Time{}
	}
	if i.running[certID] {
		i.mu.Unlock()
		return
	}
	i.running[certID] = true
	i.mu.Unlock()
	go func() {
		defer func() { i.mu.Lock(); delete(i.running, certID); i.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := i.Issue(ctx, certID); err != nil {
			slog.Warn("certificate issuance failed", "cert", certID, "err", err)
		}
	}()
}

var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// Issue obtains (or renews) a managed certificate synchronously.
func (i *Issuer) Issue(ctx context.Context, certID int64) error {
	c, err := i.Q.GetCertificate(ctx, certID)
	if err != nil {
		return err
	}
	if c.Type != "managed" {
		return errors.New("not a managed certificate")
	}
	var domains []string
	_ = json.Unmarshal([]byte(c.Domains), &domains)
	if c.Status == "issued" {
		_ = i.Q.SetCertificateStatus(ctx, db.SetCertificateStatusParams{Status: "renewing", ID: certID})
		i.changed(certID)
	}
	fail := func(err error) error {
		status := "failed"
		if c.CertPem != "" {
			status = "issued" // keep serving the old cert
		}
		_ = i.Q.SetCertificateStatus(ctx, db.SetCertificateStatusParams{Status: status, LastError: sql.NullString{String: err.Error(), Valid: true}, ID: certID})
		i.mu.Lock()
		if i.attempts == nil {
			i.attempts, i.nextTry = map[int64]int{}, map[int64]time.Time{}
		}
		n := i.attempts[certID]
		i.attempts[certID] = n + 1
		i.nextTry[certID] = time.Now().Add(backoff[min(n, len(backoff)-1)])
		i.mu.Unlock()
		i.changed(certID)
		return err
	}
	if c.Challenge != "dns01" && i.Ready != nil && !i.Ready(certID) {
		return nil // stays pending; Kick() retries after the next successful apply
	}
	cl, err := i.client(ctx)
	if err != nil {
		return fail(err)
	}
	if c.Challenge == "dns01" {
		if !c.DnsProviderID.Valid || i.DNS == nil {
			return fail(errors.New("DNS-01 requires a connected DNS provider"))
		}
		p, err := i.DNS(ctx, c.DnsProviderID.Int64)
		if err != nil {
			return fail(err)
		}
		if err := cl.Challenge.SetDNS01Provider(p); err != nil {
			return fail(err)
		}
	} else {
		if err := cl.Challenge.SetHTTP01Provider(httpProvider{i.HTTP}); err != nil {
			return fail(err)
		}
	}
	res, err := cl.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: domains, Bundle: true, KeyType: keyType(c.KeyType)})
	if err != nil {
		return fail(err)
	}
	p, err := Parse(string(res.Certificate), string(res.IssuerCertificate), string(res.PrivateKey), time.Now())
	if err != nil {
		return fail(err)
	}
	life := p.Leaf.NotAfter.Sub(p.Leaf.NotBefore)
	renew := p.Leaf.NotBefore.Add(life * 2 / 3)
	if err := i.Q.SetCertificateIssued(ctx, db.SetCertificateIssuedParams{CertPem: p.CertPEM, ChainPem: p.ChainPEM, KeyPemEnc: i.Box.Seal([]byte(p.KeyPEM)),
		Issuer: p.Issuer, Serial: p.Serial, FingerprintSha256: p.Fingerprint,
		NotBefore:  sql.NullInt64{Int64: p.Leaf.NotBefore.UnixMilli(), Valid: true},
		NotAfter:   sql.NullInt64{Int64: p.Leaf.NotAfter.UnixMilli(), Valid: true},
		RenewAfter: sql.NullInt64{Int64: renew.UnixMilli(), Valid: true}, ID: certID}); err != nil {
		return fail(err)
	}
	i.mu.Lock()
	delete(i.attempts, certID)
	delete(i.nextTry, certID)
	i.mu.Unlock()
	i.changed(certID)
	return nil
}

func (i *Issuer) changed(id int64) {
	if i.OnChange != nil {
		i.OnChange(id)
	}
}

// Kick starts issuance of pending certificates now (called after successful applies).
func (i *Issuer) Kick() {
	list, err := i.Q.ListCertificates(context.Background())
	if err != nil {
		return
	}
	for _, c := range list {
		if c.Type == "managed" && c.Status == "pending" {
			i.IssueAsync(c.ID)
		}
	}
}

// RunScheduler renews due certificates and retries failed ones with backoff (jittered hourly scan).
func (i *Issuer) RunScheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		list, err := i.Q.ListCertificates(ctx)
		if err != nil {
			continue
		}
		now := time.Now()
		for _, c := range list {
			if c.Type != "managed" {
				continue
			}
			i.mu.Lock()
			next := i.nextTry[c.ID]
			running := i.running[c.ID]
			i.mu.Unlock()
			if running || now.Before(next) {
				continue
			}
			due := c.Status == "pending" || c.Status == "failed" || (c.RenewAfter.Valid && now.UnixMilli() > c.RenewAfter.Int64)
			if due {
				i.IssueAsync(c.ID)
			}
		}
	}
}
