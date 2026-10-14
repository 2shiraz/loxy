# ADR 0003 — libdns for DNS providers (§8.9)

Status: accepted · 2026-10-14

New runtime dependencies: `github.com/libdns/libdns` v1, one libdns module per supported DNS provider (initially two: `github.com/libdns/hetzner` and `github.com/libdns/cloudflare`, see spec §21 Q4).

- One small record API for many providers; adding a provider is one registry entry in `internal/dns`.
- The same credentials drive ACME DNS-01: `dns.ACMEProvider` implements lego's `challenge.Provider` on top of libdns, so we do **not** use lego's own DNS provider packages (which would duplicate credentials and pull many more dependencies).
- Where a provider module has no zone lister, Loxy adds a small `ListZones` call for that provider's API.
- Loxy does not run an authoritative DNS server (non-goal).
