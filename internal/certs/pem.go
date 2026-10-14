// Package certs handles PEM parsing/validation, self-signed placeholders and ACME issuance (spec §11).
package certs

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Parsed holds a validated certificate bundle.
type Parsed struct {
	Leaf        *x509.Certificate
	CertPEM     string // leaf only
	ChainPEM    string // intermediates in issuing order
	KeyPEM      string
	Domains     []string
	Fingerprint string
	Issuer      string
	Serial      string
}

func decodeCerts(s string) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	rest := []byte(s)
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func encodeCert(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

func parseKey(s string) (crypto.Signer, string, error) {
	rest := []byte(s)
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return nil, "", errors.New("no private key found")
		}
		var k any
		var err error
		switch b.Type {
		case "PRIVATE KEY":
			k, err = x509.ParsePKCS8PrivateKey(b.Bytes)
		case "RSA PRIVATE KEY":
			k, err = x509.ParsePKCS1PrivateKey(b.Bytes)
		case "EC PRIVATE KEY":
			k, err = x509.ParseECPrivateKey(b.Bytes)
		default:
			continue
		}
		if err != nil {
			return nil, "", err
		}
		sg, ok := k.(crypto.Signer)
		if !ok {
			return nil, "", errors.New("unsupported key type")
		}
		return sg, string(pem.EncodeToMemory(b)), nil
	}
}

// Parse validates an uploaded bundle: key matches leaf, not expired; chain is reordered.
func Parse(certPEM, chainPEM, keyPEM string, now time.Time) (*Parsed, error) {
	all, err := decodeCerts(certPEM + "\n" + chainPEM)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	if len(all) == 0 {
		return nil, errors.New("certificate: no PEM certificate found")
	}
	key, keyOut, err := parseKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("private_key: %w", err)
	}
	var leaf *x509.Certificate
	var others []*x509.Certificate
	for _, c := range all {
		if leaf == nil && publicKeyEqual(c.PublicKey, key.Public()) {
			leaf = c
			continue
		}
		others = append(others, c)
	}
	if leaf == nil {
		return nil, errors.New("private_key: does not match the certificate")
	}
	if now.After(leaf.NotAfter) {
		return nil, errors.New("certificate: expired")
	}
	// Order chain: follow issuer links from the leaf.
	var chain []*x509.Certificate
	cur := leaf
	for len(others) > 0 {
		found := -1
		for i, c := range others {
			if bytes.Equal(cur.RawIssuer, c.RawSubject) {
				found = i
				break
			}
		}
		if found < 0 {
			break
		}
		chain = append(chain, others[found])
		cur = others[found]
		others = append(others[:found], others[found+1:]...)
	}
	chain = append(chain, others...)
	var cb strings.Builder
	for _, c := range chain {
		cb.WriteString(encodeCert(c))
	}
	sum := sha256.Sum256(leaf.Raw)
	domains := append([]string(nil), leaf.DNSNames...)
	if len(domains) == 0 && leaf.Subject.CommonName != "" {
		domains = []string{leaf.Subject.CommonName}
	}
	return &Parsed{
		Leaf: leaf, CertPEM: encodeCert(leaf), ChainPEM: cb.String(), KeyPEM: keyOut,
		Domains: domains, Fingerprint: hex.EncodeToString(sum[:]),
		Issuer: leaf.Issuer.CommonName, Serial: leaf.SerialNumber.Text(16),
	}, nil
}

func publicKeyEqual(a, b crypto.PublicKey) bool {
	type eq interface{ Equal(crypto.PublicKey) bool }
	if x, ok := a.(eq); ok {
		return x.Equal(b)
	}
	return false
}

// SelfSigned creates an ECDSA P-256 self-signed certificate (placeholder / panel cert).
func SelfSigned(cn string, dns []string, validity time.Duration) (certPEM, keyPEM string, err error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn, Organization: []string{"Loxy"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(validity),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: dns, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return "", "", err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})), nil
}
