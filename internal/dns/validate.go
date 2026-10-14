package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/2shiraz/loxy/internal/model"
)

func validTarget(s string) bool {
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	return s != "" && model.ValidHostname(s, false)
}

// ValidateRecord applies the per-type rules of §8.16.
func ValidateRecord(r Record) error {
	if r.Name != "@" && !(r.Name == "*" || model.ValidHostname(strings.ToLower(strings.TrimPrefix(r.Name, "*.")), false) || strings.HasPrefix(r.Name, "_")) {
		return fmt.Errorf("name: invalid record name %q", r.Name)
	}
	if r.TTL != 0 && (r.TTL < 60 || r.TTL > 86400) {
		return errors.New("ttl: must be 60–86400")
	}
	v := strings.TrimSpace(r.Value)
	f := strings.Fields(v)
	switch r.Type {
	case "A":
		a, err := netip.ParseAddr(v)
		if err != nil || !a.Is4() {
			return errors.New("value: not an IPv4 address")
		}
	case "AAAA":
		a, err := netip.ParseAddr(v)
		if err != nil || !a.Is6() || a.Is4In6() {
			return errors.New("value: not an IPv6 address")
		}
	case "CNAME", "NS":
		if !validTarget(v) {
			return errors.New("value: not a valid hostname")
		}
	case "MX":
		if len(f) != 2 {
			return errors.New("value: expected \"<priority> <host>\"")
		}
		if p, err := strconv.Atoi(f[0]); err != nil || p < 0 || p > 65535 {
			return errors.New("value: priority must be 0–65535")
		}
		if !validTarget(f[1]) {
			return errors.New("value: invalid mail host")
		}
	case "SRV":
		if len(f) != 4 {
			return errors.New("value: expected \"<priority> <weight> <port> <target>\"")
		}
		for i := 0; i < 3; i++ {
			if n, err := strconv.Atoi(f[i]); err != nil || n < 0 || n > 65535 {
				return errors.New("value: priority, weight and port must be 0–65535")
			}
		}
		if f[3] != "." && !validTarget(f[3]) {
			return errors.New("value: invalid target")
		}
	case "CAA":
		if len(f) < 3 {
			return errors.New("value: expected `<flags> <tag> \"<value>\"`")
		}
		if n, err := strconv.Atoi(f[0]); err != nil || n < 0 || n > 255 {
			return errors.New("value: flags must be 0–255")
		}
		if f[1] != "issue" && f[1] != "issuewild" && f[1] != "iodef" {
			return errors.New("value: tag must be issue, issuewild or iodef")
		}
	case "TXT":
		if v == "" {
			return errors.New("value: required")
		}
		for _, r := range v {
			if r < 0x20 || r > 0x7e {
				return errors.New("value: printable ASCII only")
			}
		}
	}
	return nil
}

// Verification result of a domain (§8.16).
type Verification struct {
	Status string // ok | wrong_ip | missing | error
	Detail string
	A      []string
	AAAA   []string
}

// Resolver allows tests to stub DNS.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Verify resolves fqdn and compares with the LB's expected addresses.
func Verify(ctx context.Context, res Resolver, fqdn, wantV4, wantV6 string) Verification {
	if res == nil {
		res = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	look := func(network string) ([]string, error) {
		addrs, err := res.LookupNetIP(ctx, network, fqdn)
		var out []string
		for _, a := range addrs {
			out = append(out, a.Unmap().String())
		}
		sort.Strings(out)
		var de *net.DNSError
		if errors.As(err, &de) && (de.IsNotFound || de.IsTemporary == false) {
			return out, nil
		}
		return out, err
	}
	v := Verification{}
	var err4, err6 error
	v.A, err4 = look("ip4")
	v.AAAA, err6 = look("ip6")
	if v.A == nil {
		v.A = []string{}
	}
	if v.AAAA == nil {
		v.AAAA = []string{}
	}
	if err4 != nil && err6 != nil {
		return Verification{Status: "error", Detail: err4.Error(), A: v.A, AAAA: v.AAAA}
	}
	check := func(fam, want string, got []string) (bad, missing string) {
		if want == "" {
			return "", ""
		}
		if len(got) == 0 {
			return "", fam + " record missing"
		}
		for _, g := range got {
			if g != want {
				return fam + " points to " + strings.Join(got, ", ") + " (expected " + want + ")", ""
			}
		}
		return "", ""
	}
	b4, m4 := check("A", wantV4, v.A)
	b6, m6 := check("AAAA", wantV6, v.AAAA)
	switch {
	case wantV4 == "" && wantV6 == "":
		v.Status, v.Detail = "error", "load balancer has no public IP"
	case b4 != "" || b6 != "":
		v.Status, v.Detail = "wrong_ip", strings.TrimSpace(b4+" "+b6)
	case m4 != "" && (m6 != "" || wantV6 == ""):
		v.Status, v.Detail = "missing", strings.TrimSpace(m4+" "+m6)
	case m4 == "" && m6 != "" && wantV4 == "":
		v.Status, v.Detail = "missing", m6
	default:
		v.Status = "ok"
		if m6 != "" {
			v.Detail = m6 + " (IPv4 resolves correctly)"
		}
	}
	return v
}
