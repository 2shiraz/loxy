package render

import (
	"database/sql"

	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// fixture: an HTTPS frontend (redirect, host/path routes) and a TCP frontend with PROXY v2,
// two HTTP backends and one TCP backend (spec §7.4 reference rendering).
func fixture(certPEM, keyPEM string) ([]*state.Frontend, []*state.Backend, func(int64) *state.Cert) {
	cert := &state.Cert{Certificate: db.Certificate{ID: 12, CertPem: certPEM}, KeyPEM: keyPEM}
	app := &state.Backend{BackendCfg: state.BackendCfg{ID: 4, Name: "app", Mode: "http", Algorithm: "roundrobin", Port: 9000, ProxyProtocol: "none",
		TimeoutServerS: 50, HcProtocol: "http", HcPort: 9000, HcIntervalS: 15, HcTimeoutS: 13, HcRetries: 3, HcDomain: "app.example.com",
		HcPath: "/health", HcResponse: "OK", StickyCookieName: "LBSTICKY", StickyCookieLifetimeS: 300}, StatusCodes: []string{"2??", "3??"}}
	app.Servers = []*state.Server{
		{Target: db.Target{ID: 101, BalancerID: 4, Type: "ip", Ip: "10.0.0.38", Weight: 100, AdminState: "ready"}, Slots: []*state.Slot{{ID: 101, ServerID: 101, Address: "10.0.0.38"}}},
		{Target: db.Target{ID: 102, BalancerID: 4, Type: "host", HostID: sql.NullInt64{Int64: 5, Valid: true}, Weight: 50, AdminState: "maint", Port: sql.NullInt64{Int64: 9001, Valid: true}},
			Slots: []*state.Slot{{ID: 102, ServerID: 102, HostID: 5, Address: "2001:db8::5"}}},
	}
	api := &state.Backend{BackendCfg: state.BackendCfg{ID: 5, Name: "api", Mode: "http", Algorithm: "leastconn", Port: 8080, ProxyProtocol: "none",
		TimeoutServerS: 60, StickyEnabled: 1, StickyCookieName: "SRV", StickyCookieLifetimeS: 600, HcProtocol: "https", HcPort: 8443,
		HcIntervalS: 5, HcTimeoutS: 2, HcRetries: 2, HcPath: "/"}, StatusCodes: []string{"200"}}
	api.Servers = []*state.Server{{Target: db.Target{ID: 103, BalancerID: 5, Type: "ip", Ip: "10.0.0.9", Weight: 100, Backup: 1, AdminState: "ready"},
		Slots: []*state.Slot{{ID: 103, ServerID: 103, Address: "10.0.0.9"}}}}
	db5 := &state.Backend{BackendCfg: state.BackendCfg{ID: 6, Name: "postgres", Mode: "tcp", Algorithm: "source", Port: 5432, ProxyProtocol: "v2",
		TimeoutServerS: 3600, HcProtocol: "tcp", HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/"}, StatusCodes: []string{"2??"}}
	db5.Servers = []*state.Server{{Target: db.Target{ID: 104, BalancerID: 6, Type: "ip", Ip: "10.0.1.1", Weight: 100, AdminState: "ready"},
		Slots: []*state.Slot{{ID: 104, ServerID: 104, Address: "10.0.1.1"}}}}
	web := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 3, Name: "web", Mode: "http", Tls: 1, TimeoutClientS: 50, Maxconn: 10000,
		ForwardedHeaders: 1, Enabled: 1, DefaultBackendID: sql.NullInt64{Int64: 4, Valid: true}},
		Binds:   []state.Bind{{Address: "203.0.113.10", Port: 443}, {Address: "2001:db8::1", Port: 443}},
		CertIDs: []int64{12},
		Routes: []state.Route{
			{Hosts: []string{"api.example.com", "*.api.example.com"}, PathPrefix: "/v1", BackendID: 5},
			{PathPrefix: "/api", BackendID: 5},
		}}
	pg := &state.Frontend{FrontendCfg: state.FrontendCfg{ID: 7, Name: "postgres", Mode: "tcp", TimeoutClientS: 3600, Enabled: 1, Maintenance: 1,
		DefaultBackendID: sql.NullInt64{Int64: 6, Valid: true}}, Binds: []state.Bind{{Address: "*", Port: 5432}}}
	certs := func(id int64) *state.Cert {
		if id == 12 {
			return cert
		}
		return nil
	}
	return []*state.Frontend{web, pg}, []*state.Backend{app, api, db5}, certs
}
