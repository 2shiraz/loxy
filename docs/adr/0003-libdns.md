# ADR 0003 — libdns for DNS providers (§8.16)

Status: accepted · 2026-10-14

New runtime dependencies: `github.com/libdns/libdns` v1, `github.com/libdns/hetzner` (v1: legacy dns.hetzner.com API; v2: Hetzner Console / Cloud DNS API), `github.com/libdns/cloudflare`.

- One small record API for many providers; adding a provider is one registry entry in `internal/dns`.
- The same credentials drive ACME DNS-01: `dns.ACMEProvider` implements lego's `challenge.Provider` on top of libdns, so we do **not** use lego's own DNS provider packages (which would duplicate credentials and pull many more dependencies).
- libdns/hetzner v1 has no zone lister; Loxy adds a ~20-line `ListZones` call for that API.
- Loxy does not run an authoritative DNS server (non-goal).
