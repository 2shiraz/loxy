package dns

import (
	"context"
	"net/netip"
	"testing"

	"github.com/libdns/libdns"
)

type fakeRes map[string][]netip.Addr

func (f fakeRes) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	return f[network], nil
}

func TestVerify(t *testing.T) {
	ok := fakeRes{"ip4": {netip.MustParseAddr("1.2.3.4")}}
	if v := Verify(context.Background(), ok, "a.example.com", "1.2.3.4", ""); v.Status != "ok" {
		t.Fatal(v)
	}
	if v := Verify(context.Background(), ok, "a.example.com", "5.6.7.8", ""); v.Status != "wrong_ip" {
		t.Fatal(v)
	}
	if v := Verify(context.Background(), fakeRes{}, "a.example.com", "1.2.3.4", ""); v.Status != "missing" {
		t.Fatal(v)
	}
}

func TestValidateRecord(t *testing.T) {
	good := []Record{{Type: "A", Name: "@", Value: "1.2.3.4"}, {Type: "AAAA", Name: "www", Value: "2001:db8::1"},
		{Type: "MX", Name: "@", Value: "10 mail.example.com."}, {Type: "SRV", Name: "_sip._tcp", Value: "10 5 5060 sip.example.com"},
		{Type: "CAA", Name: "@", Value: `0 issue "letsencrypt.org"`}, {Type: "TXT", Name: "@", Value: "v=spf1 -all"}, {Type: "CNAME", Name: "x", Value: "y.example.com"}}
	for _, r := range good {
		if err := ValidateRecord(r); err != nil {
			t.Errorf("%v: %v", r, err)
		}
	}
	bad := []Record{{Type: "A", Name: "@", Value: "::1"}, {Type: "MX", Name: "@", Value: "mail"}, {Type: "CAA", Name: "@", Value: `0 foo "x"`},
		{Type: "CNAME", Name: "x", Value: "bad host"}, {Type: "A", Name: "a b", Value: "1.2.3.4"}, {Type: "A", Name: "@", Value: "1.2.3.4", TTL: 5}}
	for _, r := range bad {
		if err := ValidateRecord(r); err == nil {
			t.Errorf("%v accepted", r)
		}
	}
	if _, err := ToLibdns(Record{Type: "CNAME", Name: "www", Value: "x.example.com"}, []Record{{Type: "A", Name: "www"}}); err == nil {
		t.Error("CNAME coexistence accepted")
	}
}

// memProvider is a libdns test double.
type memProvider struct{ recs map[string][]libdns.Record }

func (m *memProvider) GetRecords(_ context.Context, z string) ([]libdns.Record, error) {
	return m.recs[z], nil
}
func (m *memProvider) AppendRecords(_ context.Context, z string, r []libdns.Record) ([]libdns.Record, error) {
	m.recs[z] = append(m.recs[z], r...)
	return r, nil
}
func (m *memProvider) SetRecords(ctx context.Context, z string, r []libdns.Record) ([]libdns.Record, error) {
	return m.AppendRecords(ctx, z, r)
}
func (m *memProvider) DeleteRecords(_ context.Context, z string, r []libdns.Record) ([]libdns.Record, error) {
	var keep []libdns.Record
	for _, x := range m.recs[z] {
		del := false
		for _, y := range r {
			if x.RR().Name == y.RR().Name && x.RR().Data == y.RR().Data {
				del = true
			}
		}
		if !del {
			keep = append(keep, x)
		}
	}
	m.recs[z] = keep
	return r, nil
}
func (m *memProvider) ListZones(context.Context) ([]libdns.Zone, error) {
	return []libdns.Zone{{Name: "example.com."}, {Name: "sub.example.com."}}, nil
}

func TestACMEProvider(t *testing.T) {
	t.Setenv("LEGO_DISABLE_CNAME_SUPPORT", "true")
	m := &memProvider{recs: map[string][]libdns.Record{}}
	a := NewACMEProvider(m)
	if err := a.Present(context.Background(), "www.sub.example.com", "tok", "keyauth"); err != nil {
		t.Fatal(err)
	}
	recs := m.recs["sub.example.com."]
	if len(recs) != 1 || recs[0].RR().Type != "TXT" || recs[0].RR().Name != "_acme-challenge.www" {
		t.Fatalf("records: %+v", recs)
	}
	if err := a.CleanUp(context.Background(), "www.sub.example.com", "tok", "keyauth"); err != nil {
		t.Fatal(err)
	}
	if len(m.recs["sub.example.com."]) != 0 {
		t.Fatal("not cleaned up")
	}
}
