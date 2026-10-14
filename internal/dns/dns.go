// Package dns manages external DNS providers through libdns (spec §8.16): provider registry,
// zone/record access, a lego DNS-01 challenge.Provider on top of libdns (so credentials live
// in one place), domain verification and validated record editing. Loxy never serves DNS itself.
package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/libdns/cloudflare"
	hetznerv1 "github.com/libdns/hetzner"
	hetznerv2 "github.com/libdns/hetzner/v2"
	"github.com/libdns/libdns"
)

// Provider is the libdns surface Loxy uses.
type Provider interface {
	libdns.RecordGetter
	libdns.RecordAppender
	libdns.RecordSetter
	libdns.RecordDeleter
	libdns.ZoneLister
}

type Field struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
	Help   string `json:"help,omitempty"`
}

type Kind struct {
	Kind   string                                          `json:"kind"`
	Label  string                                          `json:"label"`
	Fields []Field                                         `json:"fields"`
	New    func(creds map[string]string) (Provider, error) `json:"-"`
}

// Registry: one entry per supported libdns provider. Add new providers here.
var Registry = []Kind{
	{Kind: "hetzner_console", Label: "Hetzner Console DNS (Cloud API)",
		Fields: []Field{{Name: "api_token", Label: "Cloud API token", Secret: true, Help: "Read & Write token of the Hetzner Cloud project that owns the zones"}},
		New: func(c map[string]string) (Provider, error) {
			return &hetznerv2.Provider{APIToken: c["api_token"]}, nil
		}},
	{Kind: "hetzner_dns", Label: "Hetzner DNS (dns.hetzner.com)",
		Fields: []Field{{Name: "api_token", Label: "DNS API token", Secret: true}},
		New: func(c map[string]string) (Provider, error) {
			return &hetznerLegacy{Provider: hetznerv1.Provider{AuthAPIToken: c["api_token"]}, token: c["api_token"]}, nil
		}},
	{Kind: "cloudflare", Label: "Cloudflare",
		Fields: []Field{{Name: "api_token", Label: "API token", Secret: true, Help: "Scoped token with Zone:Read and DNS:Edit"}},
		New: func(c map[string]string) (Provider, error) {
			return &cloudflare.Provider{APIToken: c["api_token"]}, nil
		}},
}

func LookupKind(k string) *Kind {
	for i := range Registry {
		if Registry[i].Kind == k {
			return &Registry[i]
		}
	}
	return nil
}

// New builds a provider and checks required fields.
func New(kind string, creds map[string]string) (Provider, error) {
	k := LookupKind(kind)
	if k == nil {
		return nil, fmt.Errorf("unknown provider kind %q", kind)
	}
	for _, f := range k.Fields {
		if strings.TrimSpace(creds[f.Name]) == "" {
			return nil, fmt.Errorf("%s is required", f.Label)
		}
	}
	return k.New(creds)
}

// hetznerLegacy adds ListZones to libdns/hetzner v1 (the deprecated DNS Console API).
type hetznerLegacy struct {
	hetznerv1.Provider
	token string
}

func (h *hetznerLegacy) ListZones(ctx context.Context) ([]libdns.Zone, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://dns.hetzner.com/api/v1/zones?per_page=100", nil)
	req.Header.Set("Auth-API-Token", h.token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hetzner dns: %s", resp.Status)
	}
	var body struct {
		Zones []struct{ Name string } `json:"zones"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]libdns.Zone, 0, len(body.Zones))
	for _, z := range body.Zones {
		out = append(out, libdns.Zone{Name: z.Name})
	}
	return out, nil
}

func fqdnDot(s string) string { return strings.TrimSuffix(strings.ToLower(s), ".") + "." }

// FindZone returns the longest zone of the provider that contains fqdn (with trailing dot).
func FindZone(ctx context.Context, p Provider, fqdn string) (string, error) {
	zones, err := p.ListZones(ctx)
	if err != nil {
		return "", err
	}
	name := fqdnDot(fqdn)
	best := ""
	for _, z := range zones {
		zn := fqdnDot(z.Name)
		if (name == zn || strings.HasSuffix(name, "."+zn)) && len(zn) > len(best) {
			best = zn
		}
	}
	if best == "" {
		return "", fmt.Errorf("no zone for %s in this provider account", strings.TrimSuffix(fqdn, "."))
	}
	return best, nil
}

// ACMEProvider implements lego's challenge.Provider (DNS-01) on top of a libdns provider.
type ACMEProvider struct {
	P       Provider
	created map[string]libdns.Record
}

func NewACMEProvider(p Provider) *ACMEProvider {
	return &ACMEProvider{P: p, created: map[string]libdns.Record{}}
}

func (a *ACMEProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(ctx, domain, keyAuth)
	zone, err := FindZone(ctx, a.P, info.EffectiveFQDN)
	if err != nil {
		return err
	}
	rec := libdns.TXT{Name: libdns.RelativeName(info.EffectiveFQDN, zone), TTL: 60 * time.Second, Text: info.Value}
	got, err := a.P.AppendRecords(ctx, zone, []libdns.Record{rec})
	if err != nil {
		return fmt.Errorf("create TXT %s: %w", info.EffectiveFQDN, err)
	}
	if len(got) > 0 {
		a.created[info.EffectiveFQDN+"|"+info.Value] = got[0]
	} else {
		a.created[info.EffectiveFQDN+"|"+info.Value] = rec
	}
	return nil
}

func (a *ACMEProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(ctx, domain, keyAuth)
	zone, err := FindZone(ctx, a.P, info.EffectiveFQDN)
	if err != nil {
		return err
	}
	k := info.EffectiveFQDN + "|" + info.Value
	rec, ok := a.created[k]
	if !ok {
		rec = libdns.TXT{Name: libdns.RelativeName(info.EffectiveFQDN, zone), Text: info.Value}
	}
	delete(a.created, k)
	_, err = a.P.DeleteRecords(ctx, zone, []libdns.Record{rec})
	return err
}

func (a *ACMEProvider) Timeout() (time.Duration, time.Duration) {
	return 3 * time.Minute, 5 * time.Second
}

// Record is the API shape of a zone record.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	TTL     int    `json:"ttl"`
	Managed bool   `json:"managed"`
}

var editable = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "TXT": true, "MX": true, "CAA": true, "SRV": true, "NS": true}

func FromLibdns(r libdns.Record) Record {
	rr := r.RR()
	name := rr.Name
	if name == "" {
		name = "@"
	}
	return Record{Type: rr.Type, Name: name, Value: rr.Data, TTL: int(rr.TTL / time.Second)}
}

// ListRecords returns editable records, sorted.
func ListRecords(ctx context.Context, p Provider, zone string) ([]Record, error) {
	recs, err := p.GetRecords(ctx, fqdnDot(zone))
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, r := range recs {
		x := FromLibdns(r)
		if editable[x.Type] {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}

// ToLibdns validates and converts an API record.
func ToLibdns(r Record, existing []Record) (libdns.Record, error) {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	if !editable[r.Type] {
		return nil, fmt.Errorf("type: unsupported record type %q", r.Type)
	}
	if err := ValidateRecord(r); err != nil {
		return nil, err
	}
	for _, e := range existing {
		if e.Name == r.Name && (r.Type == "CNAME" && e.Type != "CNAME" || r.Type != "CNAME" && e.Type == "CNAME") {
			return nil, errors.New("name: a CNAME cannot coexist with other records of the same name")
		}
	}
	if r.TTL == 0 {
		r.TTL = 300
	}
	data := r.Value
	if r.Type == "TXT" {
		data = strings.Trim(data, `"`)
	}
	rr := libdns.RR{Type: r.Type, Name: r.Name, Data: data, TTL: time.Duration(r.TTL) * time.Second}
	parsed, err := rr.Parse()
	if err != nil {
		return nil, fmt.Errorf("value: %w", err)
	}
	return parsed, nil
}

// AddressRecords builds the A/AAAA records a domain needs.
func AddressRecords(name string, ipv4, ipv6 []string, ttl int) []Record {
	var out []Record
	for _, a := range ipv4 {
		out = append(out, Record{Type: "A", Name: name, Value: a, TTL: ttl})
	}
	for _, a := range ipv6 {
		out = append(out, Record{Type: "AAAA", Name: name, Value: a, TTL: ttl})
	}
	return out
}
