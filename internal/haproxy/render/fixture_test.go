package render

import (
	"database/sql"

	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store/db"
)

// fixture mirrors the reference rendering of spec §7.4: LB 7 with an HTTPS service
// (redirect on, HTTP check with body match) and a TCP service with PROXY v2.
func fixture(certPEM, keyPEM string) ([]*state.LB, func(int64) *state.Cert) {
	plan := &state.Plan{ID: 1, Name: "lb11", MaxConnections: 10000, MaxServices: 5, MaxTargets: 25, MaxCertificates: 10, EnforceLbCap: 1}
	cert := &state.Cert{Certificate: db.Certificate{ID: 12, CertPem: certPEM}, KeyPEM: keyPEM}
	lb := &state.LB{
		LoadBalancer: db.LoadBalancer{ID: 7, Name: "load-balancer-1", PlanID: 1, NodeID: 1, Algorithm: "roundrobin", PublicEnabled: 1},
		Plan:         plan, IPv4: "162.55.154.22", IPv6: "2a01:4f8:1c1f:696b::1",
	}
	https := &state.Service{Service: db.Service{
		ID: 31, LbID: 7, Protocol: "https", ListenPort: 443, DestinationPort: 9000, HttpRedirect: 1,
		ProxyProtocol: "none", HttpTimeoutS: 50, HcProtocol: "http", HcPort: 9000, HcIntervalS: 15, HcTimeoutS: 13, HcRetries: 3,
		HcDomain: "app.example.com", HcPath: "/health", HcResponse: "OK", StickyCookieName: "LBSTICKY", StickyCookieLifetimeS: 300,
	}, StatusCodes: []string{"2??", "3??"}, CertIDs: []int64{12}}
	tcp := &state.Service{Service: db.Service{
		ID: 32, LbID: 7, Protocol: "tcp", ListenPort: 8443, DestinationPort: 443, ProxyProtocol: "v2", HttpTimeoutS: 60,
		HcProtocol: "tcp", HcPort: 443, HcIntervalS: 15, HcTimeoutS: 10, HcRetries: 3, HcPath: "/",
	}, StatusCodes: []string{"2??"}}
	lb.Services = []*state.Service{https, tcp}
	lb.Targets = []*state.Target{
		{Target: db.Target{ID: 101, LbID: 7, Type: "ip", Ip: "85.10.206.38", Weight: 100, AdminState: "ready"}, Slots: []*state.Slot{{ID: 101, TargetID: 101, Address: "85.10.206.38"}}},
		{Target: db.Target{ID: 102, LbID: 7, Type: "server", ServerID: sql.NullInt64{Int64: 5, Valid: true}, Weight: 50, AdminState: "maint"}, Slots: []*state.Slot{{ID: 102, TargetID: 102, ServerID: 5, Address: "2a01:4f8::5"}}},
	}
	lb2 := &state.LB{
		LoadBalancer: db.LoadBalancer{ID: 8, Name: "lb-2", PlanID: 1, NodeID: 1, Algorithm: "leastconn", PublicEnabled: 1, Maintenance: 1},
		Plan:         plan,
	}
	lb2.Services = []*state.Service{{Service: db.Service{
		ID: 40, LbID: 8, Protocol: "http", ListenPort: 80, DestinationPort: 8080, ProxyProtocol: "none", HttpTimeoutS: 60, StickyEnabled: 1,
		StickyCookieName: "LBSTICKY", StickyCookieLifetimeS: 300, HcProtocol: "https", HcPort: 8443, HcIntervalS: 5, HcTimeoutS: 2, HcRetries: 2, HcPath: "/",
	}, StatusCodes: []string{"200"}}}
	lb2.Targets = []*state.Target{{Target: db.Target{ID: 103, LbID: 8, Type: "ip", Ip: "10.0.0.9", Weight: 100, Backup: 1, AdminState: "ready"}, Slots: []*state.Slot{{ID: 103, TargetID: 103, Address: "10.0.0.9"}}}}
	certs := func(id int64) *state.Cert {
		if id == 12 {
			return cert
		}
		return nil
	}
	return []*state.LB{lb, lb2}, certs
}
